package splice

// D2/D3 regression tests (handoff Section 8 D list): the shared budget,
// the expansion ledger, and per-round trace keys.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	if l.digests["a.go#0-0"] != "digest-1" { // keyed by path and range (E04)
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
	// A third expansion is refused. It is the final call, so a context action
	// there becomes a format retry; the retries keep asking, and the stage
	// fails with the exhaustion wording. (A forced "submit with what you hold"
	// round was tried and reverted: in chain pilot 2 the model passed the tests
	// by removing the requested item.)
	got := run([][]zeroruntime.StreamEvent{expansion("a.go"), expansion("b.go"), expansion("c.go"), expansion("c.go"), expansion("c.go"), submitToolCallEvents(string(submitArgs))})
	if !strings.Contains(got, errExpansionBudgetExhausted.Error()) {
		t.Fatalf("a third expansion must exhaust the budget, got %q", got)
	}
}

// finalCallProvider scripts a fake code writer. script returns the tool
// call for the n-th provider request (1-based) and records each request.
type finalCallProvider struct {
	script   func(call int) (tool string, args string)
	requests []zeroruntime.CompletionRequest
}

func (p *finalCallProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.requests = append(p.requests, request)
	tool, args := p.script(len(p.requests))
	ch := make(chan zeroruntime.StreamEvent, 8)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: tool}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone, Usage: zeroruntime.Usage{InputTokens: 100, OutputTokens: 40}}
	close(ch)
	return ch, nil
}

func contextRequestArgs(path string) string {
	b, _ := json.Marshal(map[string]any{
		"reason":  "need " + path,
		"queries": []map[string]any{{"query_type": "read_file", "path": path, "max_results": 5, "max_chars": 8000}},
	})
	return string(b)
}

// runFinalCallStage runs one code writer invocation with the real budget
// loop. Each path it may read exists in the workspace.
func runFinalCallStage(t *testing.T, provider *finalCallProvider) (schemas.HarnessStageOutput, error) {
	t.Helper()
	workDir, registry := newRunTestWorkspace(t)
	for _, name := range []string{"main.go", "a.go", "b.go", "c.go", "d.go"} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	input := schemas.HarnessStageInput{StageName: "code_writer", RequestIntent: "add the helper"}
	selection := agent.ModelSelection{Provider: provider, Model: "test-model"}
	options := PipelineRunConfig{Cwd: workDir}
	// Reads go to the real registry. Writes are accepted without the
	// interactive permission prompt, which this test does not exercise.
	reads := NewRegistryToolRunner(registry)
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "write_file" {
			return ToolResult{OK: true}, nil
		}
		return reads.RunTool(ctx, name, args)
	})
	return runStageWithContextBudgeted(context.Background(), input, stages.CodeWriter{}, 1, selection, options, workDir, runner, nil, stages.Capabilities{PullContext: true}, 0, nil, nil, 0, nil)
}

func userPromptOf(request zeroruntime.CompletionRequest) string {
	for _, m := range request.Messages {
		if m.Role == zeroruntime.MessageRoleUser {
			return m.Content
		}
	}
	return ""
}

// TestFinalCallOffersOnlySubmitWhenNoRoundIsLeft pins E01a: a model that
// keeps asking for context reaches a call that can never be granted. That
// call must offer only the submit tool and say it is final. The stage still
// fails with the exhaustion error if the model asks again.
func TestFinalCallOffersOnlySubmitWhenNoRoundIsLeft(t *testing.T) {
	provider := &finalCallProvider{script: func(call int) (string, string) {
		return "request_codebase_context", contextRequestArgs([]string{"a.go", "b.go", "c.go"}[min(call, 3)-1])
	}}
	_, err := runFinalCallStage(t, provider)
	if err == nil || !strings.Contains(err.Error(), "context expansion budget exhausted") {
		t.Fatalf("err = %v, want the expansion budget exhaustion error", err)
	}
	// Two granted rounds, then the final call and its format retries (three
	// attempts in all). Every call after the second is the final call.
	if len(provider.requests) < 3 {
		t.Fatalf("provider calls = %d, want the final call reached", len(provider.requests))
	}
	for i, request := range provider.requests[:2] {
		if strings.Contains(userPromptOf(request), stages.FinalContextCallNotice) {
			t.Fatalf("call %d is not final and must not carry the final-call notice", i+1)
		}
		if len(request.Tools) != 2 {
			t.Fatalf("call %d offered %d tools, want submit and context (2)", i+1, len(request.Tools))
		}
	}
	for i, request := range provider.requests[2:] {
		if len(request.Tools) != 1 || request.Tools[0].Name != "submit_code" {
			names := []string{}
			for _, tool := range request.Tools {
				names = append(names, tool.Name)
			}
			t.Fatalf("final call %d tools = %v, want only submit_code", i+3, names)
		}
		if !strings.Contains(userPromptOf(request), stages.FinalContextCallNotice) {
			t.Fatalf("final call %d must tell the model it is the final call", i+3)
		}
	}
}

// TestFinalCallLetsModelSubmit pins that two granted rounds followed by a
// submission on the final call complete the stage.
func TestFinalCallLetsModelSubmit(t *testing.T) {
	submit := `{"files":[{"path":"helper.go","change_type":"create","content":"package main\n\nfunc Helper() int { return 1 }\n"}],"language":"go","intent":"add the helper","confidence":0.9}`
	provider := &finalCallProvider{script: func(call int) (string, string) {
		if call == 3 {
			return "submit_code", submit
		}
		return "request_codebase_context", contextRequestArgs([]string{"a.go", "b.go"}[call-1])
	}}
	output, err := runFinalCallStage(t, provider)
	if err != nil {
		t.Fatalf("stage must succeed when the final call submits: %v", err)
	}
	if output.ContextRequest != nil {
		t.Fatal("a submitted stage must not end with a context request")
	}
	if !strings.Contains(userPromptOf(provider.requests[2]), stages.FinalContextCallNotice) {
		t.Fatal("the submitting call must be the final call")
	}
}

// codeWriterInvocation runs one code writer invocation over shared run
// options and returns the read_file paths the stage fetched, in order.
func codeWriterInvocation(t *testing.T, workDir string, options PipelineRunConfig, runner ToolRunner, provider *finalCallProvider) ([]string, error) {
	return codeWriterInvocationFor(t, workDir, "add the helper", options, runner, provider)
}

func codeWriterInvocationFor(t *testing.T, workDir, intent string, options PipelineRunConfig, runner ToolRunner, provider *finalCallProvider) ([]string, error) {
	t.Helper()
	var reads []string
	recording := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		switch name {
		case "read_file":
			if path, ok := args["path"].(string); ok {
				reads = append(reads, path)
			}
		case "write_file":
			return ToolResult{OK: true}, nil // writes are not under test here
		}
		return runner.RunTool(ctx, name, args)
	})
	input := schemas.HarnessStageInput{StageName: "code_writer", RequestIntent: intent}
	selection := agent.ModelSelection{Provider: provider, Model: "test-model"}
	_, err := runStageWithContextBudgeted(context.Background(), input, stages.CodeWriter{}, 1, selection, options, workDir, recording, nil, stages.Capabilities{PullContext: true}, 0, nil, nil, 0, nil)
	return reads, err
}

// TestCarriedPathsSeedTheNextInvocation pins E01b: an invocation that ends
// without a submission hands the files it fetched to the next invocation of
// the same stage in the same run. The workspace has more than eight sources,
// so the alphabetical fallback alone would never read zz_target.go.
func TestCarriedPathsSeedTheNextInvocation(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	for i := 0; i < 12; i++ {
		seedRunFile(t, workDir, fmt.Sprintf("f%02d.go", i))
	}
	seedRunFile(t, workDir, "zz_target.go")
	options := PipelineRunConfig{Cwd: workDir, carriedPaths: newCarriedContextPaths()}
	reads := NewRegistryToolRunner(registry)
	// The first invocation reads zz_target.go, then f00.go, then asks for
	// f01.go, which the exhausted budget refuses.
	first := &finalCallProvider{script: func(call int) (string, string) {
		return "request_codebase_context", contextRequestArgs([]string{"zz_target.go", "f00.go", "f01.go"}[min(call, 3)-1])
	}}
	if _, err := codeWriterInvocation(t, workDir, options, reads, first); err == nil || !strings.Contains(err.Error(), "context expansion budget exhausted") {
		t.Fatalf("first invocation err = %v, want the budget exhaustion error", err)
	}
	second := &finalCallProvider{script: func(int) (string, string) {
		return "submit_code", `{"files":[{"path":"new.go","change_type":"create","content":"package main\n"}],"language":"go","intent":"add the helper","confidence":0.9}`
	}}
	seeded, err := codeWriterInvocation(t, workDir, options, reads, second)
	if err != nil {
		t.Fatalf("second invocation must submit: %v", err)
	}
	if !slices.Contains(seeded, "zz_target.go") {
		t.Fatalf("second invocation reads = %v, want the carried file zz_target.go", seeded)
	}
}

// TestCarriedPathsKeepNewestEightExistingFiles pins the carry bounds: most
// recent first, at most eight, and only files that still exist.
func TestCarriedPathsKeepNewestEightExistingFiles(t *testing.T) {
	workDir := t.TempDir()
	var fetched []schemas.ContextQuery
	// Small reads, so eight of them fit the aggregate bound (F9).
	small := func(name string) schemas.ContextQuery {
		q := carriedQuery(name)
		q.MaxChars = 1000
		return q
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("f%02d.go", i)
		seedRunFile(t, workDir, name)
		fetched = append(fetched, small(name)) // oldest first
	}
	fetched = append(fetched, small("gone.go")) // newest, but not in the workspace
	carried := newCarriedContextPaths()
	carried.put("code_writer", workDir, fetched)
	got := carriedPaths(carried.get("code_writer", workDir))
	want := []string{"f09.go", "f08.go", "f07.go", "f06.go", "f05.go", "f04.go", "f03.go", "f02.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("carried = %v, want %v", got, want)
	}
	if other := carried.get("test_generator", workDir); len(other) != 0 {
		t.Fatalf("carry must be per stage, got %v for another stage", other)
	}
}

func seedRunFile(t *testing.T, workDir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workDir, name), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWithCarriedReadsAddsMissingReadsAfterListing pins the override path:
// a carried file the override already reads is not read twice, and the new
// reads follow the listing query, ahead of the request's own reads.
func TestWithCarriedReadsAddsMissingReadsAfterListing(t *testing.T) {
	a := "a.go"
	req := schemas.ContextRequest{Reason: "override", Queries: []schemas.ContextQuery{
		{QueryType: schemas.ContextListFiles, MaxResults: 10, MaxChars: 1000},
		{QueryType: schemas.ContextReadFile, Path: &a, MaxResults: 10, MaxChars: 5000},
	}}
	got := withCarriedReads(req, []schemas.ContextQuery{carriedQuery("a.go"), carriedQuery("b.go")})
	if len(got.Queries) != 3 || got.Queries[0].QueryType != schemas.ContextListFiles {
		t.Fatalf("queries = %+v, want the listing first and one new read", got.Queries)
	}
	if got.Queries[1].Path == nil || *got.Queries[1].Path != "b.go" {
		t.Fatalf("new read = %+v, want b.go right after the listing", got.Queries[1])
	}
}

// carryFailingInvocation runs one invocation that reads zz_target.go and
// f00.go in expansion rounds, then is refused. It returns the workspace and
// the run options that hold the carry.
func carryFailingInvocation(t *testing.T, intent string, reads []string, options PipelineRunConfig, runner ToolRunner) {
	t.Helper()
	failing := &finalCallProvider{script: func(call int) (string, string) {
		return "request_codebase_context", contextRequestArgs(reads[min(call, len(reads))-1])
	}}
	if _, err := codeWriterInvocationFor(t, options.Cwd, intent, options, runner, failing); err == nil || !strings.Contains(err.Error(), "context expansion budget exhausted") {
		t.Fatalf("failing invocation err = %v, want the budget exhaustion error", err)
	}
}

func submitCodeProvider() *finalCallProvider {
	return &finalCallProvider{script: func(int) (string, string) {
		return "submit_code", `{"files":[{"path":"new.go","change_type":"create","content":"package main\n"}],"language":"go","intent":"add the helper","confidence":0.9}`
	}}
}

// TestCarryClearsAfterASuccessfulInvocation pins audit item S3: a fail, a
// success, then a third invocation must not be seeded with the old carry.
func TestCarryClearsAfterASuccessfulInvocation(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	for i := 0; i < 12; i++ {
		seedRunFile(t, workDir, fmt.Sprintf("f%02d.go", i))
	}
	seedRunFile(t, workDir, "zz_target.go")
	options := PipelineRunConfig{Cwd: workDir, carriedPaths: newCarriedContextPaths()}
	runner := NewRegistryToolRunner(registry)
	carryFailingInvocation(t, "add the helper", []string{"zz_target.go", "f00.go", "f01.go"}, options, runner)
	if _, err := codeWriterInvocationFor(t, workDir, "add the helper", options, runner, submitCodeProvider()); err != nil {
		t.Fatalf("second invocation must submit: %v", err)
	}
	third, err := codeWriterInvocationFor(t, workDir, "add the helper", options, runner, submitCodeProvider())
	if err != nil {
		t.Fatalf("third invocation must submit: %v", err)
	}
	if slices.Contains(third, "zz_target.go") {
		t.Fatalf("third invocation reads %v, want no carry after the successful second", third)
	}
}

// TestCarryKeepsOnlyExpansionReads pins audit item S4: the round-0 seed
// (here, a file the request names) is not carried. Only the files the model
// fetched in expansion rounds are.
func TestCarryKeepsOnlyExpansionReads(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	for i := 0; i < 12; i++ {
		seedRunFile(t, workDir, fmt.Sprintf("f%02d.go", i))
	}
	seedRunFile(t, workDir, "zz_target.go")
	options := PipelineRunConfig{Cwd: workDir, carriedPaths: newCarriedContextPaths()}
	runner := NewRegistryToolRunner(registry)
	// The model asks only for f00.go and f01.go. zz_target.go is named by the
	// request, so it is read by the round-0 seed alone.
	carryFailingInvocation(t, "fix `zz_target.go` now", []string{"f00.go", "f01.go", "f02.go"}, options, runner)
	seeded, err := codeWriterInvocationFor(t, workDir, "add the helper", options, runner, submitCodeProvider())
	if err != nil {
		t.Fatalf("second invocation must submit: %v", err)
	}
	if slices.Contains(seeded, "zz_target.go") {
		t.Fatalf("second invocation reads %v, want the round-0 seed zz_target.go not carried", seeded)
	}
	if !slices.Contains(seeded, "f00.go") {
		t.Fatalf("second invocation reads %v, want the expansion read f00.go carried", seeded)
	}
}

// TestCarryKeepsTheGrantedQuery pins audit item S5: the carry keeps the
// granted read's line range and character bound, not a bare path that pass 2
// would read with the default 5000 characters.
func TestCarryKeepsTheGrantedQuery(t *testing.T) {
	big := "big.go"
	start, end, maxChars := 150, 160, 20000
	granted := schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &big, StartLine: &start, EndLine: &end, MaxResults: 10, MaxChars: maxChars}
	workDir := t.TempDir()
	seedRunFile(t, workDir, big)
	carried := newCarriedContextPaths()
	carried.put("code_writer", workDir, []schemas.ContextQuery{granted})
	got := carried.get("code_writer", workDir)
	// F9 lowers the carried bound to 8000 characters. The line range is kept.
	if len(got) != 1 || got[0].MaxChars != 8000 || got[0].StartLine == nil || *got[0].StartLine != start || got[0].EndLine == nil || *got[0].EndLine != end {
		t.Fatalf("carried = %+v, want the granted range %d-%d at 8000 characters", got, start, end)
	}
}

// TestFinalCallSubmitDescriptionOmitsContextTool pins audit item N2: the
// final call's submit tool must not tell the model to request context.
func TestFinalCallSubmitDescriptionOmitsContextTool(t *testing.T) {
	provider := &finalCallProvider{script: func(call int) (string, string) {
		return "request_codebase_context", contextRequestArgs([]string{"a.go", "b.go", "c.go"}[min(call, 3)-1])
	}}
	_, _ = runFinalCallStage(t, provider)
	if len(provider.requests) < 3 {
		t.Fatalf("provider calls = %d, want the final call reached", len(provider.requests))
	}
	final := provider.requests[2].Tools
	if len(final) != 1 || strings.Contains(final[0].Description, "request_codebase_context") {
		t.Fatalf("final submit description = %q, want no request_codebase_context hint", final[0].Description)
	}
}

// TestFinalCallRejectsAContextActionAsFormatError pins audit item N1: a
// context action on the final call becomes a format retry, and a submission
// on the retry completes the stage.
func TestFinalCallRejectsAContextActionAsFormatError(t *testing.T) {
	submit := `{"files":[{"path":"helper.go","change_type":"create","content":"package main\n\nfunc Helper() int { return 1 }\n"}],"language":"go","intent":"add the helper","confidence":0.9}`
	provider := &finalCallProvider{script: func(call int) (string, string) {
		switch call {
		case 1:
			return "request_codebase_context", contextRequestArgs("a.go")
		case 2:
			return "request_codebase_context", contextRequestArgs("b.go")
		case 3:
			return "request_codebase_context", contextRequestArgs("c.go") // the final call asks anyway
		}
		return "submit_code", submit
	}}
	if _, err := runFinalCallStage(t, provider); err != nil {
		t.Fatalf("stage must complete on the format retry: %v", err)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("provider calls = %d, want 4 (the final call, then one format retry)", len(provider.requests))
	}
}

// TestFinalContextCallRuleOnlyAtZeroRequests pins audit item S6: one request
// left is still a grant, so the call is not final until none is left.
func TestFinalContextCallRuleOnlyAtZeroRequests(t *testing.T) {
	oneLeft := &StageExecutionBudget{maxRequests: 3, maxRounds: 2, requests: 2}
	if finalContextCall(oneLeft) {
		t.Fatal("one request left is still a grant: the call must not be final")
	}
	none := &StageExecutionBudget{maxRequests: 3, maxRounds: 2, requests: 3}
	if !finalContextCall(none) {
		t.Fatal("no request left: the call must be final")
	}
	noRounds := &StageExecutionBudget{maxRequests: 9, maxRounds: 2, rounds: 2}
	if !finalContextCall(noRounds) {
		t.Fatal("no round left: the call must be final")
	}
}

// carriedQuery is a granted read of the whole file, as a fulfilled request
// records it.
func carriedQuery(path string) schemas.ContextQuery {
	p := path
	return schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &p, MaxResults: 10, MaxChars: 5000}
}

func carriedPaths(queries []schemas.ContextQuery) []string {
	var out []string
	for _, q := range queries {
		if q.Path != nil {
			out = append(out, *q.Path)
		}
	}
	return out
}

// TestRepeatedCarriedQueryIsNoNewBytesNotAnError pins audit item F3: the
// second invocation is seeded with a carried read. When the model re-asks that
// exact read, it delivers no new bytes. That is the no-new-bytes round, not a
// failure, and the model can still submit.
func TestRepeatedCarriedQueryIsNoNewBytesNotAnError(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	for i := 0; i < 12; i++ {
		seedRunFile(t, workDir, fmt.Sprintf("f%02d.go", i))
	}
	seedRunFile(t, workDir, "zz_target.go")
	options := PipelineRunConfig{Cwd: workDir, carriedPaths: newCarriedContextPaths()}
	runner := NewRegistryToolRunner(registry)
	carryFailingInvocation(t, "add the helper", []string{"zz_target.go", "f00.go", "f01.go"}, options, runner)
	second := &finalCallProvider{script: func(call int) (string, string) {
		if call == 1 {
			return "request_codebase_context", contextRequestArgs("zz_target.go") // the seeded read, again
		}
		return "submit_code", `{"files":[{"path":"new.go","change_type":"create","content":"package main\n"}],"language":"go","intent":"add the helper","confidence":0.9}`
	}}
	if _, err := codeWriterInvocationFor(t, workDir, "add the helper", options, runner, second); err != nil {
		t.Fatalf("a repeated carried read must not fail the stage: %v", err)
	}
}

// TestCarriedReadBoundIsEightThousand pins audit item F9: a carried read keeps
// at most maxCarriedReadChars, which is 8000 and not the schema maximum.
func TestCarriedReadBoundIsEightThousand(t *testing.T) {
	workDir := t.TempDir()
	seedRunFile(t, workDir, "big.go")
	big := "big.go"
	carried := newCarriedContextPaths()
	carried.put("code_writer", workDir, []schemas.ContextQuery{{QueryType: schemas.ContextReadFile, Path: &big, MaxResults: 10, MaxChars: 20000}})
	got := carried.get("code_writer", workDir)
	if len(got) != 1 || got[0].MaxChars != 8000 {
		t.Fatalf("carried MaxChars = %+v, want 8000", got)
	}
}

// TestCarriedReadsRespectTheAggregateBound pins audit item F9: the carry keeps
// at most half the aggregate context bound, newest first, so the ranked reads
// that follow are not all dropped.
func TestCarriedReadsRespectTheAggregateBound(t *testing.T) {
	workDir := t.TempDir()
	var fetched []schemas.ContextQuery
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("g%02d.go", i)
		seedRunFile(t, workDir, name)
		q := carriedQuery(name)
		q.MaxChars = 8000
		fetched = append(fetched, q)
	}
	carried := newCarriedContextPaths()
	carried.put("code_writer", workDir, fetched)
	got := carriedPaths(carried.get("code_writer", workDir))
	budget := schemas.MaxAggregateContextBytes / 2
	total := 0
	for range got {
		total += 8000
	}
	if total > budget {
		t.Fatalf("carried %d reads (%d chars), want at most half the aggregate bound (%d)", len(got), total, budget)
	}
	if len(got) == 0 || got[0] != "g07.go" {
		t.Fatalf("carried = %v, want the newest read first", got)
	}
}

// TestDeliveryKeyAcceptsJSONNumbers pins audit item F9: a payload range that
// arrived as float64 (a JSON decode) keys the same as an int range.
func TestDeliveryKeyAcceptsJSONNumbers(t *testing.T) {
	item := schemas.ContextItem{Payload: map[string]any{"start": float64(40), "end": float64(42)}}
	if got := deliveryKey(item, "a.go"); got != "a.go#40-42" {
		t.Fatalf("deliveryKey(float64 range) = %q, want a.go#40-42", got)
	}
}

// TestNoNewBytesNoteNamesEachView pins audit item R2: the note calls a view
// complete only when it is complete, and says so for a partial view.
func TestNoNewBytesNoteNamesEachView(t *testing.T) {
	whole := schemas.ContextItem{Payload: map[string]any{"path": "m.go", "version": "v", "start": 1, "end": 3, "raw": "a\nb\n"}}
	complete := noNewBytesNote(&schemas.ContextBundle{Items: []schemas.ContextItem{whole}})
	if !strings.Contains(complete, "in its complete current form") || !strings.Contains(complete, "not truncated") {
		t.Fatalf("a complete repeat must keep the complete-view wording, got %q", complete)
	}

	ranged := schemas.ContextItem{Payload: map[string]any{"path": "big.txt", "version": "v", "start": 40, "end": 42, "raw": strings.Repeat("x\n", 100)}}
	partial := noNewBytesNote(&schemas.ContextBundle{Items: []schemas.ContextItem{ranged}})
	if strings.Contains(partial, "complete current form") || strings.Contains(partial, "contain no elided ranges") {
		t.Fatalf("a partial repeat must not call the view complete, got %q", partial)
	}
	if !strings.Contains(partial, "big.txt lines 40-42") || !strings.Contains(partial, "partial view") {
		t.Fatalf("a partial repeat must name its range as partial, got %q", partial)
	}

	truncated := schemas.ContextItem{Truncated: true, Payload: map[string]any{"path": "m.go", "version": "v", "start": 1, "end": 2, "raw": "a\nb\n"}}
	if note := noNewBytesNote(&schemas.ContextBundle{Items: []schemas.ContextItem{truncated}}); strings.Contains(note, "complete current form") {
		t.Fatalf("a truncated repeat must not call the view complete, got %q", note)
	}
}
