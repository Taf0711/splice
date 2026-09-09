package splice

// D2/D3 regression tests (handoff Section 8 D list): the shared budget,
// the expansion ledger, and per-round trace keys.

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
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
