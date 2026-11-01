package httpdecisions

import (
	"errors"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestDecodeResponseChoiceAndErrorOrder(t *testing.T) {
	req := zeroruntime.DecisionRequest{
		Model: "model",
		State: "{}",
		Questions: []zeroruntime.DecisionQuestion{{
			ID:           "q",
			Kind:         zeroruntime.DecisionKindChoice,
			Instructions: "Choose.",
			Criteria:     []zeroruntime.DecisionCriterion{{Label: "continue"}, {Label: "stop"}},
		}},
	}
	for _, tc := range []struct {
		name, answer, want string
	}{
		{
			name:   "selected label from a distribution",
			answer: `{"question_id":"q","kind":"choice","probabilities":{"stop":1}}`,
		},
		{
			name:   "kind before absent noul probability",
			answer: `{"question_id":"q","kind":"noul"}`,
			want:   "does not match question kind",
		},
		{
			name:   "confidence before kind",
			answer: `{"question_id":"q","kind":"noul","confidence":2}`,
			want:   "confidence",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"resolved_model":"model","answers":[` + tc.answer + `],"usage":{"input_tokens":12,"cost_usd":0.01}}`
			result, err := DecodeResponse(req, []byte(raw))
			if tc.want == "" {
				if err != nil || result.Answers[0].Choice != "stop" || result.Answers[0].Probabilities["continue"] != 0 {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				return
			}
			var billed zeroruntime.DecisionUsageError
			if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.As(err, &billed) || billed.DecisionUsage() == nil || billed.DecisionUsage().InputTokens != 12 {
				t.Fatalf("result=%+v error=%v, want %s with usage", result, err, tc.want)
			}
		})
	}
}

func TestDecodeResponseNoulRequiresProbability(t *testing.T) {
	req := zeroruntime.DecisionRequest{
		Model: "model",
		State: "{}",
		Questions: []zeroruntime.DecisionQuestion{{
			ID:           "q",
			Kind:         zeroruntime.DecisionKindNoul,
			Instructions: "Is this true?",
			Criteria:     []zeroruntime.DecisionCriterion{{Label: "false"}, {Label: "true"}},
		}},
	}
	for _, value := range []string{"", `,"true_probability":0`} {
		raw := `{"resolved_model":"model","answers":[{"question_id":"q","kind":"noul"` + value + `}]}`
		_, err := DecodeResponse(req, []byte(raw))
		if (err != nil) != (value == "") {
			t.Fatalf("probability field=%q error=%v", value, err)
		}
	}
}
