package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// The acceptance terms sit inside ComputeScore's chain of subtractions, so the
// passing term is a lone "+" among "-" operators and a later edit can silently
// invert it. A failing criterion that raised the score would reward building
// the wrong thing.
func TestAcceptanceScoreSigns(t *testing.T) {
	base := ComputeScore(schemas.IterationState{})
	passing := ComputeScore(schemas.IterationState{AcceptanceFactsPassing: 1})
	failing := ComputeScore(schemas.IterationState{AcceptanceFactsFailing: 1})
	failingTest := ComputeScore(schemas.IterationState{TestsFailing: 1})

	if passing <= base {
		t.Errorf("a passing acceptance fact must raise the score: %v -> %v", base, passing)
	}
	if failing >= base {
		t.Errorf("a failing acceptance fact must lower the score: %v -> %v", base, failing)
	}
	// Building the wrong thing is worse than a red test.
	if failing >= failingTest {
		t.Errorf("a failed acceptance fact must cost at least a failed test: acceptance=%v test=%v", failing, failingTest)
	}
}
