package zeroruntime

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type mockProvider struct {
	events []StreamEvent
}

func (provider mockProvider) StreamCompletion(
	ctx context.Context,
	request CompletionRequest,
) (<-chan StreamEvent, error) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		for _, event := range provider.events {
			select {
			case <-ctx.Done():
				return
			case events <- event:
			}
		}
	}()
	return events, nil
}

func TestSeedMessagesProducesSystemAndUserTurns(t *testing.T) {
	messages := SeedMessages("you are a helper", "inspect this repo")

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Role != MessageRoleSystem || messages[0].Content != "you are a helper" {
		t.Fatalf("unexpected system message: %#v", messages[0])
	}
	if messages[1].Role != MessageRoleUser || messages[1].Content != "inspect this repo" {
		t.Fatalf("unexpected user message: %#v", messages[1])
	}
}

func TestServerToolRequestAndBlocksRoundTripVerbatim(t *testing.T) {
	payload := `{"query":"café \"splice\"","encrypted_content":"opaque-東京"}`
	want := Message{
		Role:    MessageRoleAssistant,
		Content: "search complete",
		ServerToolBlocks: []ServerToolBlock{{
			Provider: "anthropic",
			Type:     "web_search_tool_result",
			Data:     payload,
		}},
	}
	request := CompletionRequest{
		Messages:    []Message{want},
		ServerTools: []ServerTool{{Kind: ServerToolWebSearch, MaxUses: 3}},
	}
	got := request.Messages[0]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("message changed across request round trip: got %#v, want %#v", got, want)
	}
	if request.ServerTools[0].Kind != ServerToolWebSearch || request.ServerTools[0].MaxUses != 3 {
		t.Fatalf("server tool request changed: %#v", request.ServerTools)
	}
}

func TestStreamEventNamesMatchProviderContract(t *testing.T) {
	cases := map[StreamEventType]string{
		StreamEventText:             "text",
		StreamEventReasoning:        "reasoning",
		StreamEventServerToolUse:    "server-tool-use",
		StreamEventServerToolResult: "server-tool-result",
		StreamEventToolCallStart:    "tool-call-start",
		StreamEventToolCallDelta:    "tool-call-delta",
		StreamEventToolCallEnd:      "tool-call-end",
		StreamEventUsage:            "usage",
		StreamEventDone:             "done",
		StreamEventError:            "error",
	}

	for eventType, want := range cases {
		if string(eventType) != want {
			t.Fatalf("event type %s = %q, want %q", eventType, string(eventType), want)
		}
	}
}

func TestAgentEventNamesMatchPRDContract(t *testing.T) {
	cases := map[AgentEventType]string{
		AgentEventText:       "text",
		AgentEventToolCall:   "tool_call",
		AgentEventToolResult: "tool_result",
		AgentEventThinking:   "thinking",
		AgentEventUsage:      "usage",
		AgentEventPlanUpdate: "plan_update",
		AgentEventError:      "error",
		AgentEventTurnEnd:    "turn_end",
	}

	for eventType, want := range cases {
		if string(eventType) != want {
			t.Fatalf("event type %s = %q, want %q", eventType, string(eventType), want)
		}
	}
}

func TestNormalizeUsageMapsProviderAliasesAndReasoningTokens(t *testing.T) {
	usage, err := NormalizeUsage(TokenUsage{
		PromptTokens:      100,
		CachedInputTokens: 25,
		CompletionTokens:  40,
		ReasoningTokens:   10,
		WebSearchRequests: 3,
	})
	if err != nil {
		t.Fatalf("NormalizeUsage returned error: %v", err)
	}

	if usage.EffectiveInputTokens() != 100 {
		t.Fatalf("input tokens = %d, want 100", usage.EffectiveInputTokens())
	}
	if usage.EffectiveOutputTokens() != 40 {
		t.Fatalf("output tokens = %d, want 40", usage.EffectiveOutputTokens())
	}
	if usage.CachedInputTokens != 25 {
		t.Fatalf("cached input tokens = %d, want 25", usage.CachedInputTokens)
	}
	if usage.ReasoningTokens != 10 {
		t.Fatalf("reasoning tokens = %d, want 10", usage.ReasoningTokens)
	}
	if usage.WebSearchRequests != 3 {
		t.Fatalf("web search requests = %d, want 3", usage.WebSearchRequests)
	}
	if usage.TotalTokens() != 140 {
		t.Fatalf("total tokens = %d, want 140", usage.TotalTokens())
	}
	if usage.BillableOutputTokens() != 40 {
		t.Fatalf("billable output tokens = %d, want 40", usage.BillableOutputTokens())
	}
}

func TestNormalizeUsageRejectsNegativeWebSearchRequests(t *testing.T) {
	_, err := NormalizeUsage(TokenUsage{WebSearchRequests: -1})
	if err == nil || !strings.Contains(err.Error(), "web search requests") {
		t.Fatalf("error = %v, want web search requests validation", err)
	}
}

func TestNormalizeUsageRejectsExcessiveCachedInputTokens(t *testing.T) {
	_, err := NormalizeUsage(TokenUsage{
		InputTokens:       5,
		CachedInputTokens: 12,
		OutputTokens:      3,
	})
	if err == nil {
		t.Fatal("expected cached input tokens error")
	}
	if !strings.Contains(err.Error(), "cached input tokens") {
		t.Fatalf("error = %q, want cached input tokens", err.Error())
	}
}

func TestNormalizeUsageRejectsExcessiveReasoningTokens(t *testing.T) {
	_, err := NormalizeUsage(TokenUsage{
		InputTokens:     10,
		OutputTokens:    4,
		ReasoningTokens: 9,
	})
	if err == nil {
		t.Fatal("expected reasoning tokens error")
	}
	if !strings.Contains(err.Error(), "reasoning tokens") {
		t.Fatalf("error = %q, want reasoning tokens", err.Error())
	}
}

func TestNormalizeUsageRejectsCacheWriteExceedingInput(t *testing.T) {
	_, err := NormalizeUsage(TokenUsage{
		InputTokens:       100,
		CachedInputTokens: 60,
		CacheWriteTokens:  50,
		OutputTokens:      10,
	})
	if err == nil {
		t.Fatal("expected cache write plus cached input exceeds input error")
	}
	if !strings.Contains(err.Error(), "cache write tokens") {
		t.Fatalf("error = %q, want cache write tokens", err.Error())
	}
}

func TestNormalizeUsageRejectsNegativeTokenCounts(t *testing.T) {
	_, err := NormalizeUsage(TokenUsage{OutputTokens: -1})
	if err == nil {
		t.Fatal("expected negative output token validation error")
	}
	if !strings.Contains(err.Error(), "output tokens") {
		t.Fatalf("error = %q, want output tokens", err.Error())
	}

	_, err = NormalizeUsage(TokenUsage{InputTokens: 1, PromptTokens: -1})
	if err == nil {
		t.Fatal("expected negative prompt token alias validation error")
	}
	if !strings.Contains(err.Error(), "input tokens alias") {
		t.Fatalf("error = %q, want input tokens alias", err.Error())
	}
}

func TestCollectStreamAccumulatesTextToolCallsAndUsage(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventText, Content: "Hello "}
		events <- StreamEvent{Type: StreamEventText, Content: "world"}
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "read_file"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{"pa`}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `th":"README.md"}`}
		events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "call_1"}
		events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{PromptTokens: 12, CompletionTokens: 8, CachedInputTokens: 3}}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	collected := CollectStream(context.Background(), events)

	if collected.Text != "Hello world" {
		t.Fatalf("text = %q, want %q", collected.Text, "Hello world")
	}
	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_1" || toolCall.Name != "read_file" || toolCall.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool call: %#v", toolCall)
	}
	if collected.Usage.PromptTokens != 12 || collected.Usage.CompletionTokens != 8 || collected.Usage.CachedInputTokens != 3 {
		t.Fatalf("unexpected usage: %#v", collected.Usage)
	}
	if collected.Usage.TotalTokens() != 20 {
		t.Fatalf("total tokens = %d, want 20", collected.Usage.TotalTokens())
	}
}

func TestCollectStreamAccumulatesNormalizedUsageAliases(t *testing.T) {
	usage, err := NormalizeUsage(TokenUsage{InputTokens: 12, OutputTokens: 5, ReasoningTokens: 3})
	if err != nil {
		t.Fatalf("NormalizeUsage returned error: %v", err)
	}

	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventUsage, Usage: usage}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	collected := CollectStream(context.Background(), events)

	if collected.Usage.EffectiveInputTokens() != 12 {
		t.Fatalf("input tokens = %d, want 12", collected.Usage.EffectiveInputTokens())
	}
	if collected.Usage.EffectiveOutputTokens() != 5 {
		t.Fatalf("output tokens = %d, want 5", collected.Usage.EffectiveOutputTokens())
	}
	if collected.Usage.ReasoningTokens != 3 {
		t.Fatalf("reasoning tokens = %d, want 3", collected.Usage.ReasoningTokens)
	}
	if collected.Usage.TotalTokens() != 17 {
		t.Fatalf("total tokens = %d, want 17", collected.Usage.TotalTokens())
	}
}

func TestCollectStreamMergesMixedUsageSnapshots(t *testing.T) {
	normalizedUsage, err := NormalizeUsage(TokenUsage{InputTokens: 6, OutputTokens: 3, ReasoningTokens: 2})
	if err != nil {
		t.Fatalf("NormalizeUsage returned error: %v", err)
	}

	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{PromptTokens: 10, CompletionTokens: 4, CachedInputTokens: 3}}
		events <- StreamEvent{Type: StreamEventUsage, Usage: normalizedUsage}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	collected := CollectStream(context.Background(), events)

	if collected.Usage.EffectiveInputTokens() != 6 {
		t.Fatalf("input tokens = %d, want 6", collected.Usage.EffectiveInputTokens())
	}
	if collected.Usage.EffectiveOutputTokens() != 3 {
		t.Fatalf("output tokens = %d, want 3", collected.Usage.EffectiveOutputTokens())
	}
	if collected.Usage.CachedInputTokens != 3 {
		t.Fatalf("cached input tokens = %d, want 3", collected.Usage.CachedInputTokens)
	}
	if collected.Usage.ReasoningTokens != 2 {
		t.Fatalf("reasoning tokens = %d, want 2", collected.Usage.ReasoningTokens)
	}
	if collected.Usage.TotalTokens() != 9 {
		t.Fatalf("total tokens = %d, want 9", collected.Usage.TotalTokens())
	}
}

func TestCollectStreamMergesWebSearchRequestUsage(t *testing.T) {
	events := make(chan StreamEvent, 3)
	events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{InputTokens: 6, WebSearchRequests: 2, WebSearchEngine: "parallel"}}
	events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{OutputTokens: 3, WebSearchRequests: 3}}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	collected := CollectStream(context.Background(), events)
	if collected.Usage.WebSearchRequests != 3 {
		t.Fatalf("web search requests = %d, want latest count 3", collected.Usage.WebSearchRequests)
	}
	if collected.Usage.WebSearchEngine != "parallel" {
		t.Fatalf("web search engine = %q, want parallel", collected.Usage.WebSearchEngine)
	}
}

func TestCollectStreamReportsOneMergedUsageCallback(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{
			PromptTokens:      100,
			CachedInputTokens: 20,
			CacheWriteTokens:  10,
		}}
		events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{
			CompletionTokens: 12,
			ReasoningTokens:  4,
		}}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	var usageEvents []Usage
	collected := CollectStreamWithOptions(context.Background(), events, CollectOptions{
		OnUsage: func(usage Usage) { usageEvents = append(usageEvents, usage) },
	})

	if len(usageEvents) != 1 {
		t.Fatalf("usage callbacks = %#v, want one merged callback", usageEvents)
	}
	if collected.Usage.EffectiveInputTokens() != 100 ||
		collected.Usage.EffectiveOutputTokens() != 12 ||
		collected.Usage.CachedInputTokens != 20 ||
		collected.Usage.CacheWriteTokens != 10 ||
		collected.Usage.ReasoningTokens != 4 {
		t.Fatalf("merged usage = %#v", collected.Usage)
	}
}

func TestCollectStreamWithOptionsEmitsTextReasoningAndUsageCallbacks(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventText, Content: "Hello "}
		events <- StreamEvent{Type: StreamEventReasoning, Content: "Thinking. "}
		events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{PromptTokens: 12, CompletionTokens: 5, CachedInputTokens: 2}}
		events <- StreamEvent{Type: StreamEventText, Content: "splice"}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	var textDeltas []string
	var reasoningDeltas []string
	var usageEvents []Usage
	collected := CollectStreamWithOptions(context.Background(), events, CollectOptions{
		OnText:      func(delta string) { textDeltas = append(textDeltas, delta) },
		OnReasoning: func(delta string) { reasoningDeltas = append(reasoningDeltas, delta) },
		OnUsage:     func(usage Usage) { usageEvents = append(usageEvents, usage) },
	})

	if collected.Text != "Hello splice" {
		t.Fatalf("text = %q, want Hello splice", collected.Text)
	}
	if !collected.HasReasoning {
		t.Fatal("expected reasoning stream to mark collected turn as reasoning-bearing")
	}
	if len(textDeltas) != 2 || textDeltas[0] != "Hello " || textDeltas[1] != "splice" {
		t.Fatalf("unexpected text callbacks: %#v", textDeltas)
	}
	if len(reasoningDeltas) != 1 || reasoningDeltas[0] != "Thinking. " {
		t.Fatalf("unexpected reasoning callbacks: %#v", reasoningDeltas)
	}
	if len(usageEvents) != 1 {
		t.Fatalf("expected one usage callback, got %#v", usageEvents)
	}
	if usageEvents[0].PromptTokens != 12 || usageEvents[0].CompletionTokens != 5 || usageEvents[0].CachedInputTokens != 2 {
		t.Fatalf("unexpected usage callback: %#v", usageEvents[0])
	}
}

func TestCollectStreamWithOptionsEmitsServerToolCallbacksAndBlocks(t *testing.T) {
	events := make(chan StreamEvent, 3)
	events <- StreamEvent{Type: StreamEventServerToolUse, ServerToolKind: "web_search", ServerToolQuery: "café"}
	events <- StreamEvent{
		Type:                  StreamEventServerToolResult,
		ServerToolKind:        "web_search",
		ServerToolResultCount: 2,
		ServerToolBlocks: []ServerToolBlock{{
			Provider: "anthropic",
			Type:     "server_tool_use",
			Data:     `{"encrypted_content":"opaque-東京"}`,
		}},
	}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	var uses []string
	var results []int
	collected := CollectStreamWithOptions(context.Background(), events, CollectOptions{
		OnServerToolUse:    func(kind, query string) { uses = append(uses, kind+":"+query) },
		OnServerToolResult: func(kind string, resultCount int) { results = append(results, resultCount) },
	})
	if !reflect.DeepEqual(uses, []string{"web_search:café"}) {
		t.Fatalf("server tool use callbacks = %#v", uses)
	}
	if !reflect.DeepEqual(results, []int{2}) {
		t.Fatalf("server tool result callbacks = %#v", results)
	}
	if len(collected.ServerToolBlocks) != 1 || collected.ServerToolBlocks[0].Data != `{"encrypted_content":"opaque-東京"}` {
		t.Fatalf("server tool blocks = %#v", collected.ServerToolBlocks)
	}
}

func TestCollectStreamKeepsArgumentDeltasBeforeToolCallStart(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_buffered", ArgumentsFragment: `{"path":`}
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_buffered", ToolName: "read_file"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_buffered", ArgumentsFragment: `"README.md"}`}
		events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "call_buffered"}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	collected := CollectStream(context.Background(), events)

	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one buffered tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_buffered" || toolCall.Name != "read_file" || toolCall.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected buffered tool call: %#v", toolCall)
	}
}

func TestCollectStreamFlushesOpenToolCallsWhenChannelCloses(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_closed", ToolName: "grep"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_closed", ArgumentsFragment: `{"query":"`}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_closed", ArgumentsFragment: `splice"}`}
	}()

	collected := CollectStream(context.Background(), events)

	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one flushed tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_closed" || toolCall.Name != "grep" || toolCall.Arguments != `{"query":"splice"}` {
		t.Fatalf("unexpected flushed tool call: %#v", toolCall)
	}
}

func TestCollectStreamFlushesOpenToolCallsWhenContextCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan StreamEvent)

	go func() {
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_cancelled", ToolName: "read_file"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_cancelled", ArgumentsFragment: `{"path":"README`}
		cancel()
	}()

	collected := CollectStream(ctx, events)

	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one flushed tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_cancelled" || toolCall.Name != "read_file" || toolCall.Arguments != `{"path":"README` {
		t.Fatalf("unexpected flushed tool call after cancel: %#v", toolCall)
	}
	if collected.Error == "" {
		t.Fatal("expected context cancellation to surface as collected error")
	}
	if collected.Error != context.Canceled.Error() {
		t.Fatalf("error = %q, want %q", collected.Error, context.Canceled.Error())
	}
}

func TestCollectStreamSurfacesStreamErrors(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_error", ToolName: "bash"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_error", ArgumentsFragment: `{"command":"go test`}
		events <- StreamEvent{Type: StreamEventError, Error: "provider stream failed"}
	}()

	collected := CollectStream(context.Background(), events)

	if collected.Error != "provider stream failed" {
		t.Fatalf("error = %q, want provider stream failed", collected.Error)
	}
	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one flushed tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_error" || toolCall.Name != "bash" || toolCall.Arguments != `{"command":"go test` {
		t.Fatalf("unexpected flushed tool call after error: %#v", toolCall)
	}
}

func TestCollectStreamWithOptionsReportsFinalUsageStateOnce(t *testing.T) {
	tests := []struct {
		name     string
		events   []StreamEvent
		reported bool
		usage    Usage
	}{
		{name: "reported", events: []StreamEvent{{Type: StreamEventUsage, Usage: Usage{InputTokens: 10, OutputTokens: 5}}, {Type: StreamEventDone}}, reported: true, usage: Usage{InputTokens: 10, OutputTokens: 5}},
		{name: "not reported", events: []StreamEvent{{Type: StreamEventText, Content: "hello"}, {Type: StreamEventDone}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := make(chan StreamEvent, len(test.events))
			for _, event := range test.events {
				events <- event
			}
			close(events)

			calls := 0
			CollectStreamWithOptions(context.Background(), events, CollectOptions{
				OnUsageResult: func(usage Usage, reported bool, _ *float64) {
					calls++
					if usage.EffectiveInputTokens() != test.usage.EffectiveInputTokens() || usage.EffectiveOutputTokens() != test.usage.EffectiveOutputTokens() || reported != test.reported {
						t.Fatalf("usage result = (%+v, %t), want (%+v, %t)", usage, reported, test.usage, test.reported)
					}
				},
			})
			if calls != 1 {
				t.Fatalf("OnUsageResult calls = %d, want 1", calls)
			}
		})
	}
}

func TestCollectStreamWithOptionsReportsMalformedUsageOnce(t *testing.T) {
	events := make(chan StreamEvent, 2)
	events <- StreamEvent{Type: StreamEventUsageError, Error: "reasoning exceeds output"}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	resultCalls := 0
	errorCalls := 0
	CollectStreamWithOptions(context.Background(), events, CollectOptions{
		OnUsageResult: func(Usage, bool, *float64) { resultCalls++ },
		OnUsageError: func(reason string) {
			errorCalls++
			if reason != "reasoning exceeds output" {
				t.Fatalf("usage error = %q", reason)
			}
		},
	})
	if resultCalls != 0 || errorCalls != 1 {
		t.Fatalf("usage result calls=%d error calls=%d", resultCalls, errorCalls)
	}
}

func TestCollectStreamWithOptionsLegacyOnUsageStillRequiresUsage(t *testing.T) {
	events := make(chan StreamEvent, 2)
	events <- StreamEvent{Type: StreamEventText, Content: "x"}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	called := false
	CollectStreamWithOptions(context.Background(), events, CollectOptions{
		OnUsage: func(Usage) { called = true },
	})
	if called {
		t.Fatal("legacy OnUsage fired without a usage event")
	}
}

func TestProviderContractCanBeImplementedByMock(t *testing.T) {
	var provider Provider = mockProvider{
		events: []StreamEvent{
			{Type: StreamEventText, Content: "ok"},
			{Type: StreamEventDone},
		},
	}

	stream, err := provider.StreamCompletion(context.Background(), CompletionRequest{
		Messages: SeedMessages("system", "user"),
		Tools: []ToolDefinition{
			{
				Name:        "read_file",
				Description: "Read a file",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatalf("StreamCompletion returned error: %v", err)
	}

	collected := CollectStream(context.Background(), stream)
	if collected.Text != "ok" {
		t.Fatalf("text = %q, want ok", collected.Text)
	}
}

func TestNormalizeUsageRejectsAllMalformedSubsets(t *testing.T) {
	tests := []struct {
		name    string
		input   TokenUsage
		wantErr string
	}{
		{"cached exceeds input", TokenUsage{InputTokens: 10, CachedInputTokens: 15, OutputTokens: 5}, "cached input tokens"},
		{"cache write plus cached exceeds input", TokenUsage{InputTokens: 100, CachedInputTokens: 60, CacheWriteTokens: 50, OutputTokens: 10}, "cache write tokens"},
		{"reasoning exceeds output", TokenUsage{InputTokens: 100, OutputTokens: 10, ReasoningTokens: 20}, "reasoning tokens"},
		{"all bad", TokenUsage{InputTokens: 10, CachedInputTokens: 10, CacheWriteTokens: 10, OutputTokens: 5, ReasoningTokens: 6}, "cached input tokens"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeUsage(tc.input)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestNormalizeUsageAcceptsValidSubsets(t *testing.T) {
	usage, err := NormalizeUsage(TokenUsage{
		InputTokens:       100,
		CachedInputTokens: 40,
		CacheWriteTokens:  20,
		OutputTokens:      50,
		ReasoningTokens:   10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.CachedInputTokens != 40 || usage.CacheWriteTokens != 20 || usage.ReasoningTokens != 10 {
		t.Fatalf("unexpected tokens: %+v", usage)
	}
}
