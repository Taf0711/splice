package splice

// D2 no-progress tests: a repeated expansion query and an exhausted
// budget must both fail loud. Neither may loop silently, and neither may
// push the model to invent source it never received.

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func expansionFixtureRequest(t *testing.T, path string) schemas.ContextRequest {
	t.Helper()
	req := schemas.ContextRequest{
		Reason: "need source that was not delivered",
		Queries: []schemas.ContextQuery{{
			QueryType:  schemas.ContextReadFile,
			Path:       &path,
			MaxResults: 10,
			MaxChars:   12000,
		}},
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("fixture request must be valid: %v", err)
	}
	return req
}

// TestExpansionLedgerRejectsRepeatedQuery pins the no-progress rule: the
// same query twice cannot yield new evidence, so it is an explicit error.
func TestExpansionLedgerRejectsRepeatedQuery(t *testing.T) {
	ledger := newExpansionLedger()
	req := expansionFixtureRequest(t, "internal/cache/client.go")

	if err := ledger.Check(req); err != nil {
		t.Fatalf("first request must be accepted: %v", err)
	}
	ledger.Record(req)

	err := ledger.Check(req)
	if err == nil {
		t.Fatal("a repeated query must be rejected: no new evidence would arrive")
	}
	if !strings.Contains(err.Error(), "no new evidence") {
		t.Fatalf("the rejection must name the no-progress reason, got %v", err)
	}
}

// TestExpansionLedgerAcceptsNewQueryAfterRecord pins that the ledger
// rejects only repeats, not progress.
func TestExpansionLedgerAcceptsNewQueryAfterRecord(t *testing.T) {
	ledger := newExpansionLedger()
	first := expansionFixtureRequest(t, "internal/cache/client.go")
	ledger.Record(first)

	second := expansionFixtureRequest(t, "internal/cache/pool.go")
	if err := ledger.Check(second); err != nil {
		t.Fatalf("a new query must be accepted: %v", err)
	}
}

// TestExpansionBudgetExhaustionIsExplicit pins the bound: the second call
// past the allowance reports exhaustion instead of extending the loop.
func TestExpansionBudgetExhaustionIsExplicit(t *testing.T) {
	budget := NewStageExecutionBudget(1)
	if !budget.SpendRequest() {
		t.Fatal("the first request must be within the allowance")
	}
	if budget.SpendRequest() {
		t.Fatal("the second request must exceed the allowance")
	}
	if !budget.Exhausted() {
		t.Fatal("the budget must report exhaustion")
	}
	if budget.RequestsUsed() != 2 {
		t.Fatalf("requests used = %d, want 2", budget.RequestsUsed())
	}
	if MaxExpansionRoundsPerStage < 1 || MaxProviderRequestsPerStage < 1 {
		t.Fatal("the expansion bounds must be positive ceilings")
	}
}

// TestNilBudgetAndLedgerAreUnbounded pins the documented nil behavior so a
// legacy caller is never failed closed mid-loop.
func TestNilBudgetAndLedgerAreUnbounded(t *testing.T) {
	var budget *StageExecutionBudget
	if !budget.SpendRequest() {
		t.Fatal("a nil budget must be unbounded")
	}
	var ledger *expansionLedger
	req := expansionFixtureRequest(t, "internal/cache/client.go")
	if err := ledger.Check(req); err != nil {
		t.Fatalf("a nil ledger must accept: %v", err)
	}
	ledger.Record(req)
}
