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
