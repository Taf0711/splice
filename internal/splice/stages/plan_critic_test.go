package stages

import (
	"encoding/json"
	"errors"
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

// TestDecodePlanCritiqueSyntaxErrorShowsNearByte pins that a syntax error
// quotes the text around the failing byte.
func TestDecodePlanCritiqueSyntaxErrorShowsNearByte(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{
			name: "bare literal",
			args: `{"critiques": [], "must_fix_before_execution": True, "overall_assessment": "x"}`,
			want: "True",
		},
		{
			name: "unquoted value",
			args: `{"critiques": [], "must_fix_before_execution": false, "overall_assessment": The plan is fine}`,
			want: "The plan",
		},
		{
			name: "failure past the excerpt",
			args: `{"overall_assessment": "` + strings.Repeat("x", 300) + `", "must_fix_before_execution": True}`,
			want: "True",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collected := &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: tt.args}}}
			_, err := decodePlanCritique(collected)
			if err == nil || !strings.Contains(err.Error(), "near byte") {
				t.Fatalf("expected near-byte context, got %v", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

// TestJSONErrorContextNoOffset pins that an error without an offset adds nothing.
func TestJSONErrorContextNoOffset(t *testing.T) {
	if got := jsonErrorContext(`{"a": 1}`, errors.New("boom")); got != "" {
		t.Fatalf("jsonErrorContext for non-JSON error = %q, want empty", got)
	}
}

// TestJSONErrorContextUnmarshalTypeErrorShowsField pins the field and offset
// for a type mismatch.
func TestJSONErrorContextUnmarshalTypeErrorShowsField(t *testing.T) {
	args := `{"critiques": "not a list"}`
	err := &json.UnmarshalTypeError{Field: "critiques", Offset: 15}
	got := jsonErrorContext(args, err)
	if !strings.Contains(got, `field "critiques"`) || !strings.Contains(got, "near byte 15") {
		t.Fatalf("jsonErrorContext = %q, want field and near byte", got)
	}
}
