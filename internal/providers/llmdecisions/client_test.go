package llmdecisions

import (
	"context"
	"errors"
	"testing"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

type retryProvider struct {
	requests []zeroruntime.CompletionRequest
}

func (p *retryProvider) StreamCompletion(_ context.Context, req zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) == 1 {
		return nil, errors.New("provider request error: Provider returned error")
	}
	events := make(chan zeroruntime.StreamEvent, 4)
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolName: "answer"}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ArgumentsFragment: `{"answers":[{"question_id":"q","probabilities":{"true":0.7,"false":0.3}}]}`}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 7, OutputTokens: 2}}
	close(events)
	return events, nil
}
func TestForcedToolCompatibilityAndNoul(t *testing.T) {
	provider := &retryProvider{}
	client := NewClient(provider, Options{Model: "model"})
	req := zeroruntime.DecisionRequest{Model: "model", State: "{}", Questions: []zeroruntime.DecisionQuestion{{ID: "q", Kind: zeroruntime.DecisionKindNoul, Instructions: "Is this true?", Criteria: []zeroruntime.DecisionCriterion{{Label: "false"}, {Label: "true"}}}}}
	result, err := client.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 || provider.requests[0].ToolChoice != "answer" || provider.requests[1].ToolChoice != "" {
		t.Fatalf("requests=%+v", provider.requests)
	}
	if result.Answers[0].TrueProbability != .7 || result.InputTokens != 7 || result.ReportedCostUSD != nil {
		t.Fatalf("result=%+v", result)
	}
}
