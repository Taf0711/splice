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

func TestPlanCriticPromptMarksContextConfirmedFactsVerified(t *testing.T) {
	const contextVerifiedRule = "A fact confirmed in that context is verified."
	if !strings.Contains(planCriticSystemPrompt, contextVerifiedRule) {
		t.Fatalf("plan critic prompt must contain context verification phrase %q", contextVerifiedRule)
	}
}

// TestStagePromptsRequireReadBeforeWrite pins the unconditional read-before-
// write rule in the code writer and test generator prompts. The rule exists
// because models that skip the read reinvent file contents and drop live
// symbols across repair iterations; a conditional ("when relevant_context
// includes...") was not enough. If either phrase drifts, the regression is a
// silent data-loss bug, not a wording change.
func TestStagePromptsRequireReadBeforeWrite(t *testing.T) {
	const readRule = "Never write a file you have not read in this session."
	if !strings.Contains(codeWriterSystemPrompt, readRule) {
		t.Fatal("code writer prompt must contain the unconditional read-before-write rule")
	}
	if !strings.Contains(testGeneratorSystemPrompt, readRule) {
		t.Fatal("test generator prompt must contain the unconditional read-before-write rule")
	}
	for _, symbolRule := range []string{
		"Preserve every existing symbol: constructors, types, fields, methods, and their signatures.",
	} {
		if !strings.Contains(codeWriterSystemPrompt, symbolRule) {
			t.Fatalf("code writer prompt must pin symbol preservation: %q", symbolRule)
		}
	}
}
