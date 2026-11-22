package stages

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestDecodePlanCritiqueDerivesBlockingFlag pins that the blocking flag
// follows the severities, whatever the model sent.
func TestDecodePlanCritiqueDerivesBlockingFlag(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		mustFix  bool
		want     bool
	}{
		{"high severity with false flag", "high", false, true},
		{"low severity with true flag", "low", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{
				"critiques": []map[string]any{{
					"category":             schemas.CritiqueCategories()[0],
					"severity":             tt.severity,
					"issue":                "Step order is unclear.",
					"suggested_mitigation": "Reorder the steps.",
				}},
				"cross_cutting_concerns":    []string{},
				"must_fix_before_execution": tt.mustFix,
				"overall_assessment":        "Reviewed.",
			})
			collected := &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: string(args)}}}
			critique, err := decodePlanCritique(collected)
			if err != nil {
				t.Fatalf("decodePlanCritique: %v", err)
			}
			if critique.MustFixBeforeExecution != tt.want {
				t.Fatalf("must_fix_before_execution = %t, want %t", critique.MustFixBeforeExecution, tt.want)
			}
		})
	}
}

// TestDecodePlanCritiqueInvalidArgumentsShowExcerpt pins that a parse failure
// quotes the start of what the model sent.
func TestDecodePlanCritiqueInvalidArgumentsShowExcerpt(t *testing.T) {
	args := "The plan\nlooks fine " + strings.Repeat("x", 300)
	collected := &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: args}}}
	_, err := decodePlanCritique(collected)
	if err == nil || !strings.Contains(err.Error(), "arguments began:") {
		t.Fatalf("expected excerpt in error, got %v", err)
	}
	want := fmt.Sprintf("%q", "The plan looks fine "+strings.Repeat("x", 160-len("The plan looks fine ")))
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %s", err.Error(), want)
	}
}
