package stages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
			critique, _, err := decodePlanCritique(collected)
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
	_, _, err := decodePlanCritique(collected)
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
			_, _, err := decodePlanCritique(collected)
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
	critique, _, err := decodePlanCritique(collected)
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
	_, _, err := decodePlanCritique(collected)
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
	critique, _, err := decodePlanCritique(collected)
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
	if _, _, err := decodePlanCritique(collected); err != nil {
		t.Fatalf("valid arguments: %v", err)
	}
	invalid := `{"critiques": [], "overall_assessment": "The plan is fine.", "must_fix_before_execution": nul}`
	collected = &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: invalid}}}
	_, _, err := decodePlanCritique(collected)
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

// repairTestFields returns a valid critique with one high-severity item and a
// blocking flag that matches it. Tests change one field at a time.
func repairTestFields() map[string]any {
	return map[string]any{
		"critiques": []map[string]any{{
			"category":             schemas.CritiqueCategories()[0],
			"severity":             "high",
			"issue":                "Step order is unclear.",
			"suggested_mitigation": "Reorder the steps.",
		}},
		"cross_cutting_concerns":    []string{},
		"must_fix_before_execution": true,
		"overall_assessment":        "Reviewed.",
	}
}

// TestDecodePlanCritiqueReportsRepairs pins which repair phrases the decode
// reports for each kind of model mistake.
func TestDecodePlanCritiqueReportsRepairs(t *testing.T) {
	encodedCritiques := `[{"category": "` + schemas.CritiqueCategories()[0] + `", "severity": "high", "issue": "Step order is unclear."}]`
	tests := []struct {
		name   string
		args   func() string
		repair []string
	}{
		{
			name:   "valid arguments",
			args:   func() string { b, _ := json.Marshal(repairTestFields()); return string(b) },
			repair: nil,
		},
		{
			name: "critiques as a string-encoded array",
			args: func() string {
				f := repairTestFields()
				f["critiques"] = encodedCritiques
				b, _ := json.Marshal(f)
				return string(b)
			},
			repair: []string{"critiques sent as a string"},
		},
		{
			name: "cross_cutting_concerns as one sentence",
			args: func() string {
				f := repairTestFields()
				f["cross_cutting_concerns"] = "Watch the migration order."
				b, _ := json.Marshal(f)
				return string(b)
			},
			repair: []string{"cross_cutting_concerns sent as one sentence"},
		},
		{
			name: "cross_cutting_concerns as a string-encoded array",
			args: func() string {
				f := repairTestFields()
				f["cross_cutting_concerns"] = `["Watch the migration order."]`
				b, _ := json.Marshal(f)
				return string(b)
			},
			repair: []string{"cross_cutting_concerns sent as a string"},
		},
		{
			name: "high severity with must_fix false",
			args: func() string {
				f := repairTestFields()
				f["must_fix_before_execution"] = false
				b, _ := json.Marshal(f)
				return string(b)
			},
			repair: []string{"must_fix_before_execution corrected to match severities"},
		},
		{
			name: "low severity with must_fix false",
			args: func() string {
				f := repairTestFields()
				f["must_fix_before_execution"] = false
				f["critiques"] = []map[string]any{{
					"category": schemas.CritiqueCategories()[0],
					"severity": "low",
					"issue":    "Minor wording.",
				}}
				b, _ := json.Marshal(f)
				return string(b)
			},
			repair: nil,
		},
		{
			name:   "captured unquoted last value",
			args:   func() string { return capturedCritiqueArgs("The plan is dangerous because X.") },
			repair: []string{"last value had no quotes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collected := &zeroruntime.CollectedStream{ToolCalls: []zeroruntime.ToolCall{{Name: planCriticToolName, Arguments: tt.args()}}}
			_, got, err := decodePlanCritique(collected)
			if err != nil {
				t.Fatalf("decodePlanCritique: %v", err)
			}
			if !slices.Equal(got, tt.repair) {
				t.Fatalf("repairs = %q, want %q", got, tt.repair)
			}
		})
	}
}

// TestPlanCriticStageReportsRepairs pins the Summary suffix and the Data key
// that the stage builds from the repairs.
func TestPlanCriticStageReportsRepairs(t *testing.T) {
	plan := schemas.DesignPlan{
		Source: "conversation", Epic: "add feature", Requirements: []string{"it works"},
		InScope: []string{"code"}, Tasks: []schemas.Task{{ID: "t1", Title: "write code", Intent: "impl"}},
	}
	t.Run("repaired output", func(t *testing.T) {
		provider := &fakeProvider{events: toolCallEvent("submit_critique", capturedCritiqueArgs("The plan is dangerous."))}
		result, err := (PlanCritic{}).Run(context.Background(), newHarnessInput("review plan"), provider, StageOptions{Plan: &plan})
		if err != nil {
			t.Fatalf("stage run: %v", err)
		}
		wantSummary := "Plan critique: 1 issue(s) Repaired model output: last value had no quotes."
		if result.Summary != wantSummary {
			t.Fatalf("summary = %q, want %q", result.Summary, wantSummary)
		}
		repairs, ok := result.Data["model_output_repairs"].([]string)
		if !ok || !slices.Equal(repairs, []string{"last value had no quotes"}) {
			t.Fatalf("model_output_repairs = %#v", result.Data["model_output_repairs"])
		}
	})
	t.Run("valid output", func(t *testing.T) {
		args, _ := json.Marshal(schemas.PlanCritique{CrossCuttingConcerns: []string{}, OverallAssessment: "sound"})
		provider := &fakeProvider{events: toolCallEvent("submit_critique", string(args))}
		result, err := (PlanCritic{}).Run(context.Background(), newHarnessInput("review plan"), provider, StageOptions{Plan: &plan})
		if err != nil {
			t.Fatalf("stage run: %v", err)
		}
		if result.Summary != "Plan critique: 0 issue(s)" {
			t.Fatalf("summary = %q", result.Summary)
		}
		if _, ok := result.Data["model_output_repairs"]; ok {
			t.Fatalf("valid output must not carry model_output_repairs")
		}
	})
}
