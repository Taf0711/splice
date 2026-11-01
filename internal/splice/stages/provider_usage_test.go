package stages

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

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
		OnUsageResult: func(usage zeroruntime.Usage, reported bool, cost *float64, _ string, _ bool) {
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
	collected, err := callValidatedToolUse(context.Background(), provider, "m", "", "", "p", nil, []zeroruntime.ToolDefinition{tool}, 0, callbacks, validate, "")
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

// cutOffScriptProvider plays one scripted outcome per request: "bad" is a
// complete call that fails validation, "cut" is the provider's HTTP 400 for
// a tool call cut off at max_tokens, "len" is a stream that ends with the
// length finish reason, and "ok" is a valid call.
type cutOffScriptProvider struct {
	script   []string
	requests []zeroruntime.CompletionRequest
}

func (p *cutOffScriptProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	step := p.script[len(p.requests)]
	p.requests = append(p.requests, request)
	events := make(chan zeroruntime.StreamEvent, 6)
	usage := zeroruntime.Usage{InputTokens: 100, OutputTokens: 50, PromptTokens: 100, CompletionTokens: 50}
	switch step {
	case "reset":
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "submit_code"}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: usage}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: "provider stream error: read tcp 192.168.1.157:56347->104.18.3.115:443: read: connection reset by peer"}
	case "cut":
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: "provider error: Upstream error from BaseTen: HttpError: HTTP 400: Tool calls cutoff by max_tokens."}
	case "len":
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "submit_code"}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "c1", ArgumentsFragment: `{"files": [`}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: usage}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone, FinishReason: zeroruntime.FinishReasonLength}
	default:
		args := `{"ok":true}`
		if step == "bad" {
			args = `{"wrong":true}`
		}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "submit_code"}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "c1", ArgumentsFragment: args}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c1"}
		events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: usage}
	}
	close(events)
	return events, nil
}

func validateOK(c *zeroruntime.CollectedStream) error {
	if len(c.ToolCalls) == 1 && c.ToolCalls[0].Arguments == `{"ok":true}` {
		return nil
	}
	return errors.New("bad arguments")
}

// TestCutOffToolCallRetriesWithALargerCap: a tool call cut off at the output
// cap is retried with a doubled cap and a request for a smaller submission,
// in both the HTTP 400 form and the length finish-reason form.
func TestCutOffToolCallRetriesWithALargerCap(t *testing.T) {
	for _, cut := range []string{"cut", "len"} {
		p := &cutOffScriptProvider{script: []string{cut, "ok"}}
		_, err := callValidatedToolUse(context.Background(), p, "m", "", "system", "payload", nil, []zeroruntime.ToolDefinition{submitCodeToolDefinition()}, 8192, nil, validateOK, "")
		if err != nil {
			t.Fatalf("%s: err = %v", cut, err)
		}
		if len(p.requests) != 2 || p.requests[1].MaxOutputTokens != 16384 {
			t.Fatalf("%s: requests = %d, second cap = %d, want 2 and 16384", cut, len(p.requests), p.requests[len(p.requests)-1].MaxOutputTokens)
		}
		if !strings.Contains(fmt.Sprint(p.requests[1].Messages), "cut off at the output token limit") {
			t.Fatalf("%s: the retry does not ask for a smaller submission", cut)
		}
	}
	if raisedOutputCap(30000) != 32768 || raisedOutputCap(0) != 0 || raisedOutputCap(40000) != 40000 {
		t.Fatalf("raisedOutputCap: %d %d %d", raisedOutputCap(30000), raisedOutputCap(0), raisedOutputCap(40000))
	}
}

// TestProviderErrorAfterARetryCarriesTheSummedUsage: the ledger records
// every attempt. When a later attempt ends in a provider error, the stage
// error must carry the sum of all attempts, or the run ends on "usage does
// not match request usage" (the V2.2 large-08 read).
func TestProviderErrorAfterARetryCarriesTheSummedUsage(t *testing.T) {
	p := &cutOffScriptProvider{script: []string{"bad", "cut", "cut"}}
	_, err := callValidatedToolUse(context.Background(), p, "m", "", "system", "payload", nil, []zeroruntime.ToolDefinition{submitCodeToolDefinition()}, 8192, nil, validateOK, "")
	if err == nil {
		t.Fatal("want the last cutoff to fail the call")
	}
	var metered interface{ StageUsage() *schemas.StageUsage }
	if !errors.As(err, &metered) || metered.StageUsage() == nil || metered.StageUsage().InputTokens != 100 {
		t.Fatalf("stage usage = %+v, want the first attempt's 100 input tokens", metered)
	}
}

// TestDroppedConnectionIsSentAgain pins the P6 fix: a connection reset in
// the middle of a stage request is sent again, twice at most, without using
// a format attempt, and the stage usage keeps every attempt the ledger
// recorded. A third drop fails the call.
func TestDroppedConnectionIsSentAgain(t *testing.T) {
	saved := transportRetryWaits
	transportRetryWaits = []time.Duration{0, 0}
	defer func() { transportRetryWaits = saved }()

	p := &cutOffScriptProvider{script: []string{"reset", "reset", "bad", "bad", "ok"}}
	collected, err := callValidatedToolUse(context.Background(), p, "m", "", "system", "payload", nil, []zeroruntime.ToolDefinition{submitCodeToolDefinition()}, 8192, nil, validateOK, "")
	if err != nil {
		t.Fatalf("two drops then a valid call: err = %v", err)
	}
	if len(p.requests) != 5 || collected.Usage.InputTokens != 500 {
		t.Fatalf("requests = %d, input tokens = %d; want 5 and 500 (drops use no format attempt)", len(p.requests), collected.Usage.InputTokens)
	}

	p = &cutOffScriptProvider{script: []string{"reset", "reset", "reset", "ok"}}
	if _, err := callValidatedToolUse(context.Background(), p, "m", "", "system", "payload", nil, []zeroruntime.ToolDefinition{submitCodeToolDefinition()}, 8192, nil, validateOK, ""); err == nil || len(p.requests) != 3 {
		t.Fatalf("a third drop must fail the call: err = %v, requests = %d", err, len(p.requests))
	}

	if !transportDropped(context.Background(), errors.New("provider stream error: read: connection reset by peer")) {
		t.Fatal("a provider stream reset must be sent again")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if transportDropped(ctx, errors.New("provider stream error: read: connection reset by peer")) {
		t.Fatal("a cancelled stage must not be sent again")
	}
}
