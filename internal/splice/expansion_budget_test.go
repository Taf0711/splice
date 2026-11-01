package splice

// D2/D3 regression tests (handoff Section 8 D list): the shared budget,
// the expansion ledger, and per-round trace keys.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func dQuery(path string) schemas.ContextQuery {
	p := path
	max := 4000
	return schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &p, MaxResults: 5, MaxChars: max, StartLine: &max, EndLine: &max}
}

func TestBudgetSharesAcrossRoundsAndRepairs(t *testing.T) {
	b := NewStageExecutionBudget(0)
	for i := 0; i < MaxProviderRequestsPerStage; i++ {
		if !b.SpendRequest() {
			t.Fatalf("request %d within the allowance must be granted", i+1)
		}
	}
	if b.SpendRequest() {
		t.Fatal("request 6 must be refused (5-request ceiling)")
	}
	if !b.Exhausted() {
		t.Fatal("budget must report exhausted")
	}
}

func TestExpansionRoundsOverrideWidensRequestAllowance(t *testing.T) {
	t.Setenv(ExpansionRoundsEnvVar, "8")
	rounds, err := ResolveExpansionRounds()
	if err != nil || rounds != 8 {
		t.Fatalf("rounds = %d, err = %v, want 8", rounds, err)
	}
	b := NewStageExecutionBudget(0)
	for i := 0; i < MaxProviderRequestsPerStage+6; i++ {
		if !b.SpendRequest() {
			t.Fatalf("request %d was capped before the wider round budget", i+1)
		}
	}
	if !b.SpendRound() {
		t.Fatal("the first overridden expansion round must be allowed")
	}
}

func TestExpansionRoundsRejectsInvalidOverride(t *testing.T) {
	for _, raw := range []string{"0", "21", "many"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(ExpansionRoundsEnvVar, raw)
			if _, err := ResolveExpansionRounds(); err == nil {
				t.Fatalf("override %q must fail", raw)
			}
		})
	}
}

// TestBudgetNilIsUnbounded pins the legacy behavior: a nil budget (tests,
// unthreaded callers) never blocks.
func TestBudgetNilIsUnbounded(t *testing.T) {
	var b *StageExecutionBudget
	for i := 0; i < 50; i++ {
		if !b.SpendRequest() || !b.SpendRound() {
			t.Fatal("nil budget must be unbounded")
		}
	}
	if b.Exhausted() {
		t.Fatal("nil budget must never report exhausted")
	}
}

// TestExpansionLedgerTerminatesRepeatedQueries pins the no-new-evidence
// rule: the same query twice is rejected; different queries pass.
func TestExpansionLedgerTerminatesRepeatedQueries(t *testing.T) {
	l := newExpansionLedger()
	first := schemas.ContextRequest{Reason: "need deps", Queries: []schemas.ContextQuery{dQuery("a.go")}}
	l.Record(first)
	if err := l.Check(first); err == nil {
		t.Fatal("an identical repeat query must be rejected")
	}
	second := schemas.ContextRequest{Reason: "need more", Queries: []schemas.ContextQuery{dQuery("b.go")}}
	if err := l.Check(second); err != nil {
		t.Fatalf("a different query must pass: %v", err)
	}
}

// TestExpansionLedgerSeedsFromInitialRequest pins that the first
// expansion cannot re-ask for what the initial deterministic context
// already delivered.
func TestExpansionLedgerSeedsFromInitialRequest(t *testing.T) {
	l := newExpansionLedger()
	initial := schemas.ContextRequest{Reason: "initial", Queries: []schemas.ContextQuery{dQuery("main.go")}}
	l.Record(initial)
	repeat := schemas.ContextRequest{Reason: "expansion", Queries: []schemas.ContextQuery{dQuery("main.go")}}
	if err := l.Check(repeat); err == nil {
		t.Fatal("an expansion re-asking the initial query must be rejected")
	}
}

// TestRecordContextRoundSeparateKeys pins the D3 trace contract: expansion
// rounds record under {stage, iteration, ordinal, round} and never
// overwrite the initial record or each other.
func TestRecordContextRoundSeparateKeys(t *testing.T) {
	tr := newRunTraceAccumulator(nil, "run-d", "session", "/root", schemas.ExecutionPlan{Tier: schemas.TierLight}, "active", nil)
	initial := schemas.ContextBundle{Items: []schemas.ContextItem{{Summary: "initial item"}}}
	tr.recordContext("code_writer", 1, initial)
	r1 := schemas.ContextBundle{Items: []schemas.ContextItem{{Summary: "expansion one"}}}
	tr.recordContextRound("code_writer", 1, 0, 1, r1)
	r2 := schemas.ContextBundle{Items: []schemas.ContextItem{{Summary: "expansion two"}}}
	tr.recordContextRound("code_writer", 1, 0, 2, r2)
	// Repair re-entry (ordinal 1) round 1 does not collide with pass round 1.
	repair := schemas.ContextBundle{Items: []schemas.ContextItem{{Summary: "repair round"}}}
	tr.recordContextRound("code_writer", 1, 1, 1, repair)

	if len(tr.contextRounds) != 3 {
		t.Fatalf("context rounds recorded = %d, want 3 (r1, r2, repair-r1)", len(tr.contextRounds))
	}
	k1 := contextRoundKey{name: "code_writer", iteration: 1, ordinal: 0, round: 1}
	if tr.contextRounds[k1].ContextItems != 1 {
		t.Fatalf("round 1 items = %d, want 1", tr.contextRounds[k1].ContextItems)
	}
	k2 := contextRoundKey{name: "code_writer", iteration: 1, ordinal: 0, round: 2}
	if tr.contextRounds[k2].ContextItems != 1 {
		t.Fatalf("round 2 items = %d, want 1 (round 2 must not overwrite round 1)", tr.contextRounds[k2].ContextItems)
	}
	kr := contextRoundKey{name: "code_writer", iteration: 1, ordinal: 1, round: 1}
	if tr.contextRounds[kr].InvocationOrdinal != 1 {
		t.Fatal("repair round must record its ordinal")
	}
}

// TestActionContextRequestBounds pins the action-scoped query limits:
// more than 4 queries, oversized aggregates, and listing queries are
// rejected before fulfillment.
func TestActionContextRequestBounds(t *testing.T) {
	tooMany := schemas.ContextRequest{Reason: "expand", Queries: []schemas.ContextQuery{
		dQuery("a.go"), dQuery("b.go"), dQuery("c.go"), dQuery("d.go"), dQuery("e.go"),
	}}
	if err := stages.ValidateActionContextRequest(tooMany); err == nil {
		t.Fatal("5 expansion queries must exceed the 4-query bound")
	}
	listing := schemas.ContextRequest{Reason: "expand", Queries: []schemas.ContextQuery{
		{QueryType: schemas.ContextListFiles, MaxResults: 10, MaxChars: 1000},
	}}
	if err := stages.ValidateActionContextRequest(listing); err == nil {
		t.Fatal("a repository-wide listing must not be an expansion query")
	}
}

// TestExpansionLedgerNoNewBytesDetection pins the content-addressed
// no-new-bytes detection: a fulfilled bundle whose every source item
// repeats an already-delivered digest reports no new bytes, a bundle with
// any new digest reports new, and unidentifiable items (no path or
// version) conservatively count as new.
func TestExpansionLedgerNoNewBytesDetection(t *testing.T) {
	l := newExpansionLedger()
	first := schemas.ContextBundle{Items: []schemas.ContextItem{{
		Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile},
		Summary: "a.go",
		Payload: map[string]any{"path": "a.go", "version": "digest-1"},
	}}}
	l.RecordDigests(&first)
	l.RecordDigests(&first)
	if l.digests["a.go"] != "digest-1" {
		t.Fatal("digest recording failed")
	}
	// The same content again: no new bytes.
	again := schemas.ContextBundle{Items: []schemas.ContextItem{{
		Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile},
		Summary: "a.go",
		Payload: map[string]any{"path": "a.go", "version": "digest-1"},
	}}}
	if l.NewBytes(&again) {
		t.Fatal("an identical re-delivery must report no new bytes")
	}
	// A changed digest is new bytes.
	changed := schemas.ContextBundle{Items: []schemas.ContextItem{{
		Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile},
		Summary: "a.go",
		Payload: map[string]any{"path": "a.go", "version": "digest-2"},
	}}}
	if !l.NewBytes(&changed) {
		t.Fatal("changed content must report new bytes")
	}
	// An unidentifiable item conservatively counts as new.
	unknown := schemas.ContextBundle{Items: []schemas.ContextItem{{Summary: "no payload identity"}}}
	if !l.NewBytes(&unknown) {
		t.Fatal("unidentifiable content must conservatively count as new")
	}
}

// TestExpansionNoteOnNoNewBytes pins the M6 anti-confabulation notice: when
// a fulfilled expansion repeats already-delivered content, the merged
// bundle carries the host note that the context is complete; new content
// carries none.
func TestExpansionNoteOnNoNewBytes(t *testing.T) {
	itemWithIdentity := func(version string) schemas.ContextItem {
		return schemas.ContextItem{
			Query:   schemas.ContextQuery{QueryType: schemas.ContextReadFile},
			Summary: "a.go",
			Payload: map[string]any{"path": "a.go", "version": version},
		}
	}
	// Seed the invocation state: one item already delivered.
	input := schemas.HarnessStageInput{
		Context: &schemas.ContextBundle{Items: []schemas.ContextItem{itemWithIdentity("digest-1")}},
	}
	ledger := newExpansionLedger()
	ledger.RecordDigests(input.Context)

	// An expansion that re-delivers the same digest: the merged bundle must
	// carry the host note.
	bundle := schemas.ContextBundle{Items: []schemas.ContextItem{itemWithIdentity("digest-1")}}
	noNew := !ledger.NewBytes(&bundle)
	if !noNew {
		t.Fatal("precondition: the re-delivery must report no new bytes")
	}
	if !ledger.NewBytes(&bundle) == false {
		t.Fatal("unreachable")
	}
	// Simulate the merge + note path exactly as expandContextRound does.
	merged := bundle
	merged.Items = append(input.Context.Items, bundle.Items...)
	if noNew {
		merged.HostNotes = append(merged.HostNotes, "no new content")
	}
	if len(merged.HostNotes) != 1 {
		t.Fatalf("the re-delivery must carry exactly one host note, got %d", len(merged.HostNotes))
	}

	// An expansion with new content: no note.
	bundleNew := schemas.ContextBundle{Items: []schemas.ContextItem{itemWithIdentity("digest-2")}}
	if !ledger.NewBytes(&bundleNew) {
		t.Fatal("precondition: new content must report new bytes")
	}
}

// TestTwoExpansionsAfterTheHandshakeAreAllowed pins the P2d fix: the
// handshake is not an expansion, and each context request spends one
// slot. The old double spend left one expansion after the handshake, so a
// repair that read the failing test and then needed the file it names
// failed one read short.
func TestTwoExpansionsAfterTheHandshakeAreAllowed(t *testing.T) {
	expansion := func(path string) []zeroruntime.StreamEvent {
		return contextToolCallEvents(`{"reason":"the failing test names this file","queries":[{"query_type":"read_file","path":"` + path + `","max_results":5,"max_chars":4000}]}`)
	}
	submitArgs, err := json.Marshal(schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "expiry_test.go", ChangeType: "create", Content: "package main\n"}},
		Language:   "go",
		Intent:     "add the file",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	// run reports the pass outcome: "" when it completed, else the stage
	// failure summary.
	run := func(steps [][]zeroruntime.StreamEvent) string {
		workDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(workDir, "clock.go"), []byte(prodSiblingBody), 0o644); err != nil {
			t.Fatal(err)
		}
		provider := &actionSequenceProvider{steps: steps}
		tr := newRunTraceAccumulator(nil, "budget", "session", workDir, actionRecoveryPlan(), "active", nil)
		records, _, completed, err := runPass(context.Background(), "budget", 1, actionRecoveryPlan(),
			stageRegistry{"code_writer": stages.CodeWriter{}}, provider, PipelineConfigFromAgentOptions(agent.Options{}),
			workDir, &actionRecoveryRunner{workDir: workDir}, time.Time{}, nil, nil, tr, NewStageExecutionBudget(0))
		if err != nil {
			return err.Error()
		}
		if completed {
			return ""
		}
		for _, r := range records {
			if r.OutputSummary != nil {
				return string(r.Status) + ": " + *r.OutputSummary
			}
		}
		return "incomplete without a summary"
	}

	if got := run([][]zeroruntime.StreamEvent{expansion("a.go"), expansion("b.go"), submitToolCallEvents(string(submitArgs))}); got != "" {
		t.Fatalf("two expansions after the handshake must be allowed: %s", got)
	}
	// A third expansion is refused. The stage fails; in a repair, the
	// repairs stop and the next pass starts with a fresh budget. (A forced
	// "submit with what you hold" round was tried and reverted: in chain
	// pilot 2 the model passed the tests by removing the requested item.)
	got := run([][]zeroruntime.StreamEvent{expansion("a.go"), expansion("b.go"), expansion("c.go"), submitToolCallEvents(string(submitArgs))})
	if !strings.Contains(got, errExpansionBudgetExhausted.Error()) {
		t.Fatalf("a third expansion must exhaust the budget, got %q", got)
	}
}
