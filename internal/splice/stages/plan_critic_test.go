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
			name: "unquoted value before another field",
			args: `{"critiques": [], "overall_assessment": The plan is fine, "must_fix_before_execution": false}`,
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

// plan critic arguments with a valid high-severity critique and a last string
// value that has no quotes.
func capturedCritiqueArgs(bare string) string {
	return fmt.Sprintf(`{"critiques": [{"category": %q, "severity": "high", "issue": "Step order is unclear.", "suggested_mitigation": "Reorder the steps."}], "cross_cutting_concerns": [], "must_fix_before_execution": true, "overall_assessment": %s}`,
		schemas.CritiqueCategories()[0], bare)
}

// TestDecodePlanCritiqueRepairsUnquotedLastValue pins the captured failure: the
// last string value has no quotes and runs to the closing brace.
func TestDecodePlanCritiqueRepairsUnquotedLastValue(t *testing.T) {
	const assessment = "The plan correctly identifies the core change but is dangerous because X."
	args := capturedCritiqueArgs(assessment)
	collected := &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: args}}}
	critique, err := decodePlanCritique(collected)
	if err != nil {
		t.Fatalf("decodePlanCritique: %v", err)
	}
	if critique.OverallAssessment != assessment {
		t.Fatalf("overall_assessment = %q, want %q", critique.OverallAssessment, assessment)
	}
	if !critique.MustFixBeforeExecution {
		t.Fatalf("must_fix_before_execution = false, want true")
	}
	if len(critique.Critiques) != 1 {
		t.Fatalf("critiques = %d, want 1", len(critique.Critiques))
	}
}

// TestDecodePlanCritiqueDoesNotRepairBareMiddleValue pins that a bare value
// before another field keeps the original error.
func TestDecodePlanCritiqueDoesNotRepairBareMiddleValue(t *testing.T) {
	args := `{"overall_assessment": The plan is fine, "critiques": []}`
	collected := &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: args}}}
	_, err := decodePlanCritique(collected)
	if err == nil || !strings.Contains(err.Error(), "near byte") {
		t.Fatalf("expected original near-byte error, got %v", err)
	}
	if strings.Contains(err.Error(), "repair") {
		t.Fatalf("error %q mentions a repair", err.Error())
	}
}

// TestDecodePlanCritiqueDoesNotRepairSwallowedFields pins that a bare value
// which contains another key is not repaired.
func TestDecodePlanCritiqueDoesNotRepairSwallowedFields(t *testing.T) {
	args := `{"critiques": [], "overall_assessment": The plan "must_fix_before_execution": true}`
	collected := &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: args}}}
	critique, err := decodePlanCritique(collected)
	if err == nil {
		t.Fatalf("expected an error, got critique %+v", critique)
	}
	if !strings.Contains(err.Error(), "near byte") {
		t.Fatalf("expected original near-byte error, got %v", err)
	}
}

// TestDecodePlanCritiqueLiteralLastValueNotRepaired pins that a last value that
// starts with a JSON literal is not treated as bare text.
func TestDecodePlanCritiqueLiteralLastValueNotRepaired(t *testing.T) {
	valid := `{"critiques": [], "overall_assessment": "The plan is fine.", "must_fix_before_execution": true}`
	collected := &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: valid}}}
	if _, err := decodePlanCritique(collected); err != nil {
		t.Fatalf("valid arguments: %v", err)
	}
	invalid := `{"critiques": [], "overall_assessment": "The plan is fine.", "must_fix_before_execution": nul}`
	collected = &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: invalid}}}
	_, err := decodePlanCritique(collected)
	if err == nil || !strings.Contains(err.Error(), "near byte") {
		t.Fatalf("expected original near-byte error, got %v", err)
	}
}

// TestRepairUnquotedTrailingValue pins the rebuild rules of the repair helper.
func TestRepairUnquotedTrailingValue(t *testing.T) {
	t.Run("no closing brace", func(t *testing.T) {
		if _, ok := repairUnquotedTrailingValue(`{"overall_assessment": The plan`); ok {
			t.Fatal("repair applied to text that does not end with }")
		}
	})
	t.Run("captured shape", func(t *testing.T) {
		in := `{"critiques": [], "must_fix_before_execution": true, "overall_assessment": The plan is dangerous.}`
		want := `{"critiques": [], "must_fix_before_execution": true, "overall_assessment": "The plan is dangerous."}`
		got, ok := repairUnquotedTrailingValue(in)
		if !ok || got != want {
			t.Fatalf("repairUnquotedTrailingValue = %q, %t; want %q, true", got, ok, want)
		}
	})
	t.Run("quotes and backslashes are escaped", func(t *testing.T) {
		const bare = `Don't use C:\tmp and say "no"`
		in := `{"critiques": [], "overall_assessment": ` + bare + `}`
		got, ok := repairUnquotedTrailingValue(in)
		if !ok {
			t.Fatal("repair not applied")
		}
		var critique schemas.PlanCritique
		if err := json.Unmarshal([]byte(got), &critique); err != nil {
			t.Fatalf("rebuilt text %q does not decode: %v", got, err)
		}
		if critique.OverallAssessment != bare {
			t.Fatalf("overall_assessment = %q, want %q", critique.OverallAssessment, bare)
		}
	})
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
