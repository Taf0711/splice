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

// TestStagePromptsRequireReadBeforeWrite pins the rule that the model
// never writes unread content. C3 migrates the MECHANISM: the historical
// "read with read_file first" instruction named a tool that does not
// exist on the model surface (B1's raw seam is host-only). The rule now
// rides the source-view contract: the model may only edit text present
// in the base content its context views delivered, and the materializer
// plus the write tool's expected-base recheck enforce it deterministically.
// If either phrase drifts, the regression is a silent data-loss bug.
func TestStagePromptsRequireReadBeforeWrite(t *testing.T) {
	const sourceAccessRule = "there is no model-visible read_file tool"
	const unreadSpanRule = "only modify text present in the base content your views delivered"
	if !strings.Contains(codeWriterSystemPrompt, sourceAccessRule) {
		t.Fatal("code writer prompt must pin the source-view access contract")
	}
	if !strings.Contains(codeWriterSystemPrompt, unreadSpanRule) {
		t.Fatal("code writer prompt must pin the unread-span edit rule")
	}
	if !strings.Contains(testGeneratorSystemPrompt, sourceAccessRule) {
		t.Fatal("test generator prompt must pin the source-view access contract")
	}
	for _, symbolRule := range []string{
		"Preserve every existing symbol: constructors, types, fields, methods, and their signatures.",
	} {
		if !strings.Contains(codeWriterSystemPrompt, symbolRule) {
			t.Fatalf("code writer prompt must pin symbol preservation: %q", symbolRule)
		}
	}
}
