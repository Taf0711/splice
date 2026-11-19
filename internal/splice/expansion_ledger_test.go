package splice

// D2 no-progress tests: an exhausted budget must fail loud. A repeated
// expansion is a no-new-bytes round, not a failure (F3). Neither may loop
// silently, and neither may push the model to invent source it never received.

import (
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
