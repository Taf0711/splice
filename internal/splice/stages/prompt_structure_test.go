package stages

import (
	"strings"
	"testing"
)

func TestComposedStagePromptStatesNoOutsideContextRuleOnce(t *testing.T) {
	const rule = "Use only files, chat history"
	if got := strings.Count(composeSystemPrompt(codeWriterSystemPrompt), rule); got != 1 {
		t.Fatalf("composed stage prompt must state the no-outside-context rule once, got %d occurrences", got)
	}
}

func TestPlanCriticPromptCalibratesSeverity(t *testing.T) {
	const unverifiedFactsRule = "A critique that depends on a fact the critic was not shown may not exceed medium severity. State that the fact is unverified."
	const harmRule = "Reserve high and critical for a defect that causes harm when the plan is executed as written: data loss, a security hole, or a correctness fault that ships. A plan that is merely silent about a detail the implementer will decide is at most medium."
	for _, phrase := range []string{unverifiedFactsRule, harmRule} {
		if !strings.Contains(planCriticSystemPrompt, phrase) {
			t.Fatalf("plan critic prompt must contain calibration rule %q", phrase)
		}
	}
}
