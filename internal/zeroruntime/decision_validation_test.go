package zeroruntime

import (
	"strings"
	"testing"
)

func TestValidateDecisionResult(t *testing.T) {
	req := DecisionRequest{Model: "m", State: "{}", Questions: []DecisionQuestion{{ID: "q", Kind: DecisionKindChoice, Instructions: "choose", Criteria: []DecisionCriterion{{Label: "a"}, {Label: "b"}}}}}
	for _, tc := range []struct {
		name  string
		model string
		p     map[string]float64
		bad   bool
	}{
		{"omitted", "m-2026", map[string]float64{"a": 1}, false},
		{"sum", "m", map[string]float64{"a": 0.5}, true},
		{"drift", "other", map[string]float64{"a": 1}, true},
		{"unknown", "m", map[string]float64{"a": 1, "x": 0}, true},
		{"negative", "m", map[string]float64{"a": 1.1, "b": -0.1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := DecisionResult{ResolvedModel: tc.model, Answers: []DecisionAnswer{{QuestionID: "q", Kind: DecisionKindChoice, Choice: "a", Probabilities: tc.p}}}
			err := ValidateDecisionResult(req, result)
			if (err != nil) != tc.bad {
				t.Fatalf("error = %v", err)
			}
			if !tc.bad {
				if _, ok := result.Answers[0].Probabilities["b"]; !ok {
					t.Fatal("omitted label lost")
				}
			}
		})
	}
}

func TestDecisionUsageErrorRejectsInvalidAccounting(t *testing.T) {
	negative := -.01
	for _, usage := range []*DecisionResult{{InputTokens: -1}, {OutputTokens: -1}, {ReportedCostUSD: &negative}} {
		err := &DecisionResponseError{Usage: usage}
		if err.DecisionUsage() != nil {
			t.Fatal("invalid accounting could reduce the advisor allowance")
		}
	}
}

func TestValidateDecisionResultPreservesErrorOrder(t *testing.T) {
	req := DecisionRequest{
		Model: "m",
		State: "{}",
		Questions: []DecisionQuestion{{
			ID:           "q",
			Kind:         DecisionKindChoice,
			Instructions: "Choose.",
			Criteria:     []DecisionCriterion{{Label: "a"}, {Label: "b"}},
		}},
	}
	for _, tc := range []struct {
		name   string
		answer DecisionAnswer
		want   string
	}{
		{
			name:   "confidence before kind",
			answer: DecisionAnswer{QuestionID: "q", Kind: DecisionKindNoul, ConfidenceSet: true, Confidence: 2},
			want:   "confidence",
		},
		{
			name:   "kind before absent selected label",
			answer: DecisionAnswer{QuestionID: "q", Kind: DecisionKindNoul, TrueProbability: .9},
			want:   "does not match question kind",
		},
		{
			name:   "absent selected label before probabilities",
			answer: DecisionAnswer{QuestionID: "q", Kind: DecisionKindChoice, Probabilities: map[string]float64{"a": .1}},
			want:   "without a selected label",
		},
		{
			name:   "unknown selected label before probabilities",
			answer: DecisionAnswer{QuestionID: "q", Kind: DecisionKindChoice, Choice: "unknown"},
			want:   "not one of the criteria",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDecisionResult(req, DecisionResult{ResolvedModel: req.Model, Answers: []DecisionAnswer{tc.answer}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %s", err, tc.want)
			}
		})
	}
}
