package llmdecisions

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

// effortProvider records the completion request and answers with one choice.
type effortProvider struct {
	requests []zeroruntime.CompletionRequest
}

func (p *effortProvider) StreamCompletion(_ context.Context, req zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.requests = append(p.requests, req)
	events := make(chan zeroruntime.StreamEvent, 3)
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolName: "answer"}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ArgumentsFragment: `{"answers":[{"question_id":"q","probabilities":{"true":0.7,"false":0.3}}]}`}
	close(events)
	return events, nil
}

// The configured reasoning effort reaches every decision request.
func TestReasoningEffortIsSentWhenConfigured(t *testing.T) {
	req := zeroruntime.DecisionRequest{Model: "m", State: "{}", Questions: []zeroruntime.DecisionQuestion{{ID: "q", Kind: zeroruntime.DecisionKindNoul, Instructions: "Is this true?", Criteria: []zeroruntime.DecisionCriterion{{Label: "false"}, {Label: "true"}}}}}
	for _, effort := range []string{"", "low"} {
		provider := &effortProvider{}
		if _, err := NewClient(provider, Options{Model: "m", ReasoningEffort: effort}).Evaluate(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if got := provider.requests[0].ReasoningEffort; got != effort {
			t.Fatalf("effort %q: request carried %q", effort, got)
		}
	}
}
