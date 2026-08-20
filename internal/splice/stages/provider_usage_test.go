package stages

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestUsageFromCollectedCarriesWebSearchUsage(t *testing.T) {
	// Regression guard: the conversion dropped web-search requests and engine.
	got := usageFromCollected(&zeroruntime.CollectedStream{Usage: zeroruntime.Usage{
		WebSearchRequests: 2,
		WebSearchEngine:   "parallel",
	}})
	want := &schemas.StageUsage{WebSearchRequests: 2, WebSearchEngine: "parallel"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usageFromCollected = %#v, want %#v", got, want)
	}
}

func TestUsageFromCollectedWithoutSearchIsUnchanged(t *testing.T) {
	got := usageFromCollected(&zeroruntime.CollectedStream{Usage: zeroruntime.Usage{
		InputTokens:  10,
		OutputTokens: 5,
	}})
	want := &schemas.StageUsage{InputTokens: 10, OutputTokens: 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usageFromCollected = %#v, want %#v", got, want)
	}
	if got := usageFromCollected(&zeroruntime.CollectedStream{}); got != nil {
		t.Fatalf("usageFromCollected(empty) = %#v, want nil", got)
	}
}

// rawReasoningFakeProvider reports reasoning exceeding completion on attempt 1
// (an un-normalized provider report) and a normal report on attempt 2.
type rawReasoningFakeProvider struct {
	attempt int
}

func (p *rawReasoningFakeProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.attempt++
	events := make(chan zeroruntime.StreamEvent, 4)
	args := `{"wrong":true}`
	usage := zeroruntime.Usage{ReasoningTokens: 8407}
	if p.attempt > 1 {
		args = `{"ok":true}`
		usage = zeroruntime.Usage{InputTokens: 4778, OutputTokens: 11146, ReasoningTokens: 8416, PromptTokens: 4778, CompletionTokens: 11146}
	}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "submit_code"}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "c1", ArgumentsFragment: args}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c1"}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: usage}
	close(events)
	return events, nil
}

// The live divergence: an un-normalized attempt (reasoning > completion) must
// not let the stage's summed usage diverge from what the request ledger
// records. The stage sums the ledger-recorded value; on failure both sides
// zero the attempt, so the totals must be equal for every field.
func TestValidatedToolUseUnnormalizedReasoningMatchesLedger(t *testing.T) {
	provider := &rawReasoningFakeProvider{}
	var ledgerIn, ledgerOut, ledgerReasoning []int
	callbacks := &zeroruntime.CollectOptions{
		OnUsageResult: func(usage zeroruntime.Usage, reported bool, cost *float64) {
			u := usage
			if _, err := zeroruntime.NormalizeUsage(zeroruntime.TokenUsage{
				InputTokens: u.InputTokens, PromptTokens: u.PromptTokens,
				OutputTokens: u.OutputTokens, CompletionTokens: u.CompletionTokens,
				ReasoningTokens: u.ReasoningTokens,
			}); err != nil {
				u = zeroruntime.Usage{}
			}
			ledgerIn = append(ledgerIn, u.EffectiveInputTokens())
			ledgerOut = append(ledgerOut, u.EffectiveOutputTokens())
			ledgerReasoning = append(ledgerReasoning, u.ReasoningTokens)
		},
	}
	validate := func(collected *zeroruntime.CollectedStream) error {
		call := findToolCall(collected, "submit_code")
		if call == nil || call.Arguments != `{"ok":true}` {
			return errors.New("invalid typed output")
		}
		return nil
	}
	tool := zeroruntime.ToolDefinition{Name: "submit_code"}
	collected, err := callValidatedToolUse(context.Background(), provider, "m", "", "", "p", nil, tool, 0, callbacks, validate, "")
	if err != nil {
		t.Fatalf("callValidatedToolUse: %v", err)
	}
	sum := func(xs []int) int {
		s := 0
		for _, v := range xs {
			s += v
		}
		return s
	}
	if got, want := collected.Usage.EffectiveInputTokens(), sum(ledgerIn); got != want {
		t.Fatalf("input: stage=%d ledger=%d", got, want)
	}
	if got, want := collected.Usage.EffectiveOutputTokens(), sum(ledgerOut); got != want {
		t.Fatalf("output: stage=%d ledger=%d", got, want)
	}
	if got, want := collected.Usage.ReasoningTokens, sum(ledgerReasoning); got != want {
		t.Fatalf("reasoning: stage=%d ledger=%d (the live divergence)", got, want)
	}
}
