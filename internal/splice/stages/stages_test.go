package stages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/providers"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/memoryreason"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// fakeProvider returns a channel with the provided events.
type fakeProvider struct {
	events []zeroruntime.StreamEvent
}

func (f *fakeProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func toolCallEvent(name, args string) []zeroruntime.StreamEvent {
	return []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: name},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"},
		{Type: zeroruntime.StreamEventDone},
	}
}

func newHarnessInput(intent string) schemas.HarnessStageInput {
	return schemas.HarnessStageInput{
		RunID:          "run-1",
		StageName:      "test",
		Sequence:       1,
		PlanTier:       schemas.TierStandard,
		RequestIntent:  intent,
		PriorSummaries: map[string]string{},
	}
}

func TestCodeWriterWritesFiles(t *testing.T) {
	workDir := t.TempDir()
	output := schemas.CodeWriterOutput{
		Files: []schemas.FileChange{
			{Path: "main.go", Content: "package main\n", ChangeType: "create"},
		},
		Language:   "go",
		Intent:     "create main package",
		Confidence: 0.9,
	}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_code", string(args))}

	stage := CodeWriter{}
	input := newHarnessInput("create main.go")
	result, err := stage.Run(context.Background(), input, provider, StageOptions{WorkDir: workDir, Language: "go"})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if result.Summary != output.Intent {
		t.Fatalf("expected summary %q, got %q", output.Intent, result.Summary)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "main.go"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "package main\n" {
		t.Fatalf("unexpected content: %q", string(content))
	}
	assertPipelineMetaPrompt(t, provider.request)
}

func registryRunTool(t *testing.T, workDir string) func(context.Context, string, map[string]any) (ToolResult, error) {
	t.Helper()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewDeleteFileTool(workDir))
	return func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		res := registry.RunWithOptions(ctx, name, args, tools.RunOptions{PermissionGranted: true})
		return ToolResult{OK: res.Status == tools.StatusOK, Output: res.Output}, nil
	}
}

const bashCommandMustBeString = "Error: Invalid arguments for bash: command must be a string"

// schemaEnforcingBashRunTool rejects a non-string bash command the same way
// the real bash tool does, then calls next. Quality-check tests use this so a
// []string command cannot hide behind a permissive mock.
func schemaEnforcingBashRunTool(next func(context.Context, string, map[string]any) (ToolResult, error)) func(context.Context, string, map[string]any) (ToolResult, error) {
	return func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "bash" {
			if _, ok := args["command"].(string); !ok {
				return ToolResult{OK: false, Output: bashCommandMustBeString}, nil
			}
		}
		if next == nil {
			if name == "bash" {
				return ToolResult{OK: true}, nil
			}
			return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
		}
		return next(ctx, name, args)
	}
}

func TestSelectMemoryNilForNilBundle(t *testing.T) {
	if got := selectMemory(nil); got != nil {
		t.Fatalf("expected nil for nil bundle, got %#v", got)
	}
	if got := selectMemory(&schemas.MemoryBundle{RequestingAgent: "x"}); got != nil {
		t.Fatalf("expected nil for empty bundle, got %#v", got)
	}
}

func TestSelectMemoryTruncatesContentOver500Runes(t *testing.T) {
	long := strings.Repeat("界", 501)
	project := "/repo"
	bundle := &schemas.MemoryBundle{
		RequestingAgent: "x",
		Observations: []schemas.MemoryObservation{{
			ID:          9,
			ProjectPath: &project,
			Scope:       "project",
			OwnerAgent:  "splice",
			Visibility:  "shareable",
			Title:       "long note",
			Content:     long,
			MemoryType:  "note",
		}},
	}
	// Truncation is an admission concern now: admit, then select.
	admitted := memoryreason.Admit(bundle, "/repo", 0)
	got := selectMemory(admitted.Bundle)
	if len(got) != 1 {
		t.Fatalf("expected 1 selected observation, got %d", len(got))
	}
	runes := []rune(got[0].Content)
	if len(runes) != 503 {
		t.Fatalf("expected 503 runes (500 + ellipsis), got %d", len(runes))
	}
	if !strings.HasSuffix(got[0].Content, "...") {
		t.Fatalf("expected truncation suffix, got %q", got[0].Content)
	}
}

func TestSelectMemoryRendersExemplars(t *testing.T) {
	bundle := &schemas.MemoryBundle{
		RequestingAgent: "x",
		Observations: []schemas.MemoryObservation{{
			Title: "note", Content: "body", MemoryType: "note", Scope: "project",
		}},
		Exemplars: []schemas.Exemplar{
			{RunID: "run-1", Content: "intent: add a Hello function"},
		},
	}
	got := selectMemory(bundle)
	if len(got) != 2 {
		t.Fatalf("expected 1 observation + 1 exemplar = 2 items, got %d", len(got))
	}
	exemplar := got[1]
	if exemplar.MemoryType != "exemplar" {
		t.Fatalf("memory_type = %q, want exemplar", exemplar.MemoryType)
	}
	if exemplar.RunID != "run-1" {
		t.Fatalf("run_id = %q, want run-1", exemplar.RunID)
	}
	if exemplar.Content != "intent: add a Hello function" {
		t.Fatalf("content = %q", exemplar.Content)
	}
}

// requestCapturingProvider records the CompletionRequest passed to StreamCompletion.
type requestCapturingProvider struct {
	request zeroruntime.CompletionRequest
	events  []zeroruntime.StreamEvent
}

func (p *requestCapturingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.request = request
	ch := make(chan zeroruntime.StreamEvent, len(p.events))
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func assertPipelineMetaPrompt(t *testing.T, request zeroruntime.CompletionRequest) {
	t.Helper()
	meta := strings.TrimSpace(pipelineMetaPrompt)
	if meta == "" {
		t.Fatal("pipeline meta prompt is empty")
	}
	systemPrompts := 0
	for _, message := range request.Messages {
		if message.Role != zeroruntime.MessageRoleSystem {
			continue
		}
		systemPrompts++
		if count := strings.Count(message.Content, meta); count != 1 {
			t.Fatalf("pipeline meta prompt count = %d, want exactly 1 in %q", count, message.Content)
		}
	}
	if systemPrompts != 1 {
		t.Fatalf("system prompt count = %d, want 1", systemPrompts)
	}
}

// panickingProvider panics if StreamCompletion is ever called. Used to prove
// that deterministic stages never invoke the provider (F14a).
type panickingProvider struct{}

func (panickingProvider) StreamCompletion(_ context.Context, _ zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	panic("panickingProvider.StreamCompletion was called") // unreachable for deterministic stages
}

var _ zeroruntime.Provider = panickingProvider{}

type retryScriptProvider struct {
	requests []zeroruntime.CompletionRequest
	scripts  [][]zeroruntime.StreamEvent
	errs     []error
}

func (provider *retryScriptProvider) StreamCompletion(_ context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	index := len(provider.requests)
	provider.requests = append(provider.requests, request)
	if index < len(provider.errs) && provider.errs[index] != nil {
		return nil, provider.errs[index]
	}
	var events []zeroruntime.StreamEvent
	if index < len(provider.scripts) {
		events = provider.scripts[index]
	}
	ch := make(chan zeroruntime.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestValidatedToolUseRetriesContractFailuresAndAccumulatesUsage(t *testing.T) {
	valid := schemas.CodeWriterOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	validArgs, _ := json.Marshal(valid)
	provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 4, OutputTokens: 3, CachedInputTokens: 1, CacheWriteTokens: 1, ReasoningTokens: 1}},
			{Type: zeroruntime.StreamEventDone},
		},
		append([]zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 5, OutputTokens: 4, CachedInputTokens: 1, CacheWriteTokens: 2, ReasoningTokens: 2}}}, toolCallEvent(codeWriterToolName, `{`)...),
		append([]zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 6, OutputTokens: 5, CachedInputTokens: 2, CacheWriteTokens: 1, ReasoningTokens: 3}}}, toolCallEvent(codeWriterToolName, string(validArgs))...),
	}}

	collected, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, nil, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseCodeWriterOutput(collected)
		return err
	}, "")
	if err != nil {
		t.Fatalf("retrying typed output: %v", err)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(provider.requests))
	}
	wantUsage := zeroruntime.Usage{InputTokens: 15, OutputTokens: 12, CachedInputTokens: 4, CacheWriteTokens: 4, ReasoningTokens: 6}
	if collected.Usage.InputTokens != wantUsage.InputTokens || collected.Usage.OutputTokens != wantUsage.OutputTokens || collected.Usage.CachedInputTokens != wantUsage.CachedInputTokens || collected.Usage.CacheWriteTokens != wantUsage.CacheWriteTokens || collected.Usage.ReasoningTokens != wantUsage.ReasoningTokens {
		t.Fatalf("accumulated usage = %#v, want %#v", collected.Usage, wantUsage)
	}
	for _, index := range []int{1, 2} {
		user := provider.requests[index].Messages[1].Content
		if !strings.Contains(user, "typed output contract") || !strings.Contains(user, codeWriterToolName) {
			t.Fatalf("retry %d lacks corrective feedback: %q", index+1, user)
		}
	}
}

func TestValidatedToolUseRetriesSchemaInvalidArguments(t *testing.T) {
	invalid := `{"files":[],"language":"","intent":"","confidence":2}`
	valid := schemas.CodeWriterOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	validArgs, _ := json.Marshal(valid)
	provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{
		toolCallEvent(codeWriterToolName, invalid),
		toolCallEvent(codeWriterToolName, string(validArgs)),
	}}
	_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, nil, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseCodeWriterOutput(collected)
		return err
	}, "")
	if err != nil {
		t.Fatalf("schema-invalid retry: %v", err)
	}
	if len(provider.requests) != 2 || !strings.Contains(provider.requests[1].Messages[1].Content, "language is required") {
		t.Fatalf("schema-invalid retry requests = %#v", provider.requests)
	}
}

// TestValidatedToolUseUsageMatchesStreamCallbacks pins the retry accounting:
// the accumulated stage usage (usageFromCollected over the summed total) must
// equal the per-stream usage-callback sum (the ledger's view). A validation
// retry (fail once, then succeed) must not diverge, or applyRequestLedger trips.
func TestValidatedToolUseUsageMatchesStreamCallbacks(t *testing.T) {
	valid := schemas.CodeWriterOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	validArgs, _ := json.Marshal(valid)
	provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 4, OutputTokens: 3, ReasoningTokens: 1}},
			{Type: zeroruntime.StreamEventDone},
		},
		append([]zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 5, OutputTokens: 4, ReasoningTokens: 2}}}, toolCallEvent(codeWriterToolName, string(validArgs))...),
	}}

	var ledgerIn, ledgerOut, ledgerReasoning int
	callbacks := &zeroruntime.CollectOptions{
		OnUsageResult: func(u zeroruntime.Usage, _ bool, _ *float64) {
			ledgerIn += u.EffectiveInputTokens()
			ledgerOut += u.EffectiveOutputTokens()
			ledgerReasoning += u.ReasoningTokens
		},
	}

	collected, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, callbacks, func(c *zeroruntime.CollectedStream) error {
		_, e := parseCodeWriterOutput(c)
		return e
	}, "")
	if err != nil {
		t.Fatalf("callValidatedToolUse: %v", err)
	}

	usage := usageFromCollected(collected)
	if usage.InputTokens != ledgerIn || usage.OutputTokens != ledgerOut || usage.ReasoningTokens != ledgerReasoning {
		t.Fatalf("stage usage %+v diverges from stream-callback sum {in:%d out:%d reasoning:%d}", usage, ledgerIn, ledgerOut, ledgerReasoning)
	}
}

func TestCodeWriterDoesNotRetryApplicationFailure(t *testing.T) {
	output := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "main.go", Content: "package main\n", ChangeType: "create"}},
		Language:   "go",
		Intent:     "write code",
		Confidence: 0.9,
	}
	args, _ := json.Marshal(output)
	provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{toolCallEvent(codeWriterToolName, string(args))}}
	_, err := (CodeWriter{}).Run(context.Background(), newHarnessInput("write code"), provider, StageOptions{
		WorkDir:       t.TempDir(),
		Language:      "go",
		ModelOverride: "qwen-local",
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: false, Output: "permission denied"}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("application error = %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("application failure provider calls = %d, want 1", len(provider.requests))
	}
}

func TestValidatedToolUseDoesNotRetryTransportErrors(t *testing.T) {
	provider := &retryScriptProvider{errs: []error{errors.New("connection refused")}}
	_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, nil, func(*zeroruntime.CollectedStream) error { return nil }, "")
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("transport error = %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("transport error calls = %d, want 1", len(provider.requests))
	}
}

func TestValidatedToolUseDoesNotRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := &retryScriptProvider{}
	_, err := callValidatedToolUse(ctx, provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, nil, func(*zeroruntime.CollectedStream) error { return errors.New("invalid") }, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("canceled request calls = %d, want 0", len(provider.requests))
	}
}

func TestValidatedToolUseForcedChoiceFallbackIsBoundedAndNarrow(t *testing.T) {
	t.Run("repeated rejection aborts after one fallback", func(t *testing.T) {
		provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{
			{{Type: zeroruntime.StreamEventError, Error: "provider request error: Provider returned error"}},
			{{Type: zeroruntime.StreamEventError, Error: "provider request error: Provider returned error"}},
		}}
		_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, nil, func(*zeroruntime.CollectedStream) error { return nil }, "")
		if err == nil || !strings.Contains(err.Error(), "provider request error: Provider returned error") {
			t.Fatalf("repeated rejection error = %v", err)
		}
		if len(provider.requests) != 2 {
			t.Fatalf("provider calls = %d, want exactly 2 (no loop)", len(provider.requests))
		}
	})
	t.Run("different request rejection is not retried", func(t *testing.T) {
		provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{
			{{Type: zeroruntime.StreamEventError, Error: "provider request error: model does not exist"}},
		}}
		_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, nil, func(*zeroruntime.CollectedStream) error { return nil }, "")
		if err == nil || !strings.Contains(err.Error(), "model does not exist") {
			t.Fatalf("request error = %v", err)
		}
		if len(provider.requests) != 1 {
			t.Fatalf("request error calls = %d, want 1", len(provider.requests))
		}
	})
	t.Run("auth rejection is not retried", func(t *testing.T) {
		provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{
			{{Type: zeroruntime.StreamEventError, Error: "auth error: your API key is missing or invalid"}},
		}}
		_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, nil, func(*zeroruntime.CollectedStream) error { return nil }, "")
		if err == nil || !strings.Contains(err.Error(), "auth error:") {
			t.Fatalf("auth error = %v", err)
		}
		if len(provider.requests) != 1 {
			t.Fatalf("auth error calls = %d, want 1", len(provider.requests))
		}
	})
}

func TestValidatedToolUseExhaustionIsActionableAndMetered(t *testing.T) {
	missing := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 4, OutputTokens: 3, CachedInputTokens: 1, CacheWriteTokens: 1, ReasoningTokens: 1}},
		{Type: zeroruntime.StreamEventDone},
	}
	provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{missing, missing, missing}}
	_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 0, nil, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseCodeWriterOutput(collected)
		return err
	}, "")
	var typedErr *TypedOutputError
	if !errors.As(err, &typedErr) {
		t.Fatalf("exhaustion error = %T %v, want TypedOutputError", err, err)
	}
	if !strings.Contains(err.Error(), "OpenAI-compatible for local runtimes") || !strings.Contains(err.Error(), codeWriterToolName) || !strings.Contains(err.Error(), "qwen-local") {
		t.Fatalf("exhaustion error is not actionable: %v", err)
	}
	usage := typedErr.StageUsage()
	wantUsage := schemas.StageUsage{InputTokens: 12, OutputTokens: 9, CachedInputTokens: 3, CacheWriteTokens: 3, ReasoningTokens: 3}
	if usage == nil || *usage != wantUsage {
		t.Fatalf("exhausted usage = %#v, want %#v", usage, wantUsage)
	}
}

func TestCodeWriterRetriesThroughKeylessLocalOpenAIAdapter(t *testing.T) {
	valid := schemas.CodeWriterOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	validArgs, _ := json.Marshal(valid)
	requests := 0
	var authHeaders []string
	var userPrompts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		authHeaders = append(authHeaders, request.Header.Get("Authorization"))
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode local request: %v", err)
		}
		for _, message := range body.Messages {
			if message.Role == "user" {
				userPrompts = append(userPrompts, message.Content)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"plain text\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		event := map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0,
				"id":    "local-call",
				"type":  "function",
				"function": map[string]any{
					"name":      codeWriterToolName,
					"arguments": string(validArgs),
				},
			}}},
			"finish_reason": "tool_calls",
		}}}
		encoded, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
	defer server.Close()

	provider, err := providers.New(config.ProviderProfile{
		Name:         "ollama",
		CatalogID:    "ollama",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      server.URL + "/v1",
		Model:        "qwen-local",
	}, providers.Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (CodeWriter{}).Run(context.Background(), newHarnessInput("write code"), provider, StageOptions{WorkDir: t.TempDir(), Language: "go", ModelOverride: "qwen-local"})
	if err != nil {
		t.Fatalf("local adapter stage run: %v", err)
	}
	if result.Summary != valid.Intent || requests != 2 {
		t.Fatalf("local retry result=%q requests=%d", result.Summary, requests)
	}
	if authHeaders[0] != "" || authHeaders[1] != "" {
		t.Fatalf("keyless local adapter sent Authorization headers: %q", authHeaders)
	}
	if len(userPrompts) != 2 || !strings.Contains(userPrompts[1], "typed output contract") {
		t.Fatalf("local corrective prompts = %#v", userPrompts)
	}
}

func TestCodeWriterRunIncludesMemoryInPayload(t *testing.T) {
	workDir := t.TempDir()
	output := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{},
		Language:   "go",
		Intent:     "no changes",
		Confidence: 0.9,
	}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_code", string(args))}
	bundle := &schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations: []schemas.MemoryObservation{{
			ID:         1,
			Title:      "Use gofmt",
			Content:    "Run gofmt on all generated files.",
			MemoryType: "decision",
			Scope:      "project",
		}},
	}

	stage := CodeWriter{}
	input := newHarnessInput("write code")
	input.MemoryBundle = bundle
	_, err := stage.Run(context.Background(), input, provider, StageOptions{WorkDir: workDir, Language: "go"})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if len(provider.request.Messages) < 2 {
		t.Fatalf("expected user message, got %d messages", len(provider.request.Messages))
	}
	payload := provider.request.Messages[1].Content
	var cwInput schemas.CodeWriterInput
	if err := json.Unmarshal([]byte(payload), &cwInput); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(cwInput.Memory) != 1 {
		t.Fatalf("expected 1 memory entry, got %#v", cwInput.Memory)
	}
	if cwInput.Memory[0].Title != "Use gofmt" {
		t.Fatalf("unexpected memory title: %q", cwInput.Memory[0].Title)
	}
	if !strings.Contains(payload, "\"memory\"") {
		t.Fatalf("payload should contain memory field: %s", payload)
	}
}

// TestCodeWriterPayloadCarriesPipelineRoster pins the roster in the marshalled model payload.
func TestCodeWriterPayloadCarriesPipelineRoster(t *testing.T) {
	output := schemas.CodeWriterOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_code", string(args))}
	input := newHarnessInput("write code")
	input.PipelineStages = []string{"code_writer", "test_generator", "test_runner"}
	input.NextStage = "test_generator"

	if _, err := (CodeWriter{}).Run(context.Background(), input, provider, StageOptions{WorkDir: t.TempDir(), Language: "go"}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	payload := modelUserPayload(t, provider.request)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("unmarshal model payload: %v", err)
	}
	var roster []string
	if raw, ok := fields["pipeline_stages"]; !ok {
		t.Fatalf("payload missing \"pipeline_stages\" key: %s", payload)
	} else if err := json.Unmarshal(raw, &roster); err != nil {
		t.Fatalf("unmarshal pipeline_stages: %v", err)
	}
	if !reflect.DeepEqual(roster, input.PipelineStages) {
		t.Fatalf("pipeline_stages = %#v, want %#v", roster, input.PipelineStages)
	}
	var next string
	if raw, ok := fields["next_stage"]; !ok {
		t.Fatalf("payload missing \"next_stage\" key: %s", payload)
	} else if err := json.Unmarshal(raw, &next); err != nil {
		t.Fatalf("unmarshal next_stage: %v", err)
	}
	if next != input.NextStage {
		t.Fatalf("next_stage = %q, want %q", next, input.NextStage)
	}
}

// TestLastPipelineStageOmitsNextStage pins omitempty for the last roster stage.
func TestLastPipelineStageOmitsNextStage(t *testing.T) {
	output := schemas.CodeWriterOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_code", string(args))}
	input := newHarnessInput("write code")
	input.PipelineStages = []string{"code_writer"}

	if _, err := (CodeWriter{}).Run(context.Background(), input, provider, StageOptions{WorkDir: t.TempDir(), Language: "go"}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	payload := modelUserPayload(t, provider.request)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("unmarshal model payload: %v", err)
	}
	if _, ok := fields["next_stage"]; ok {
		t.Fatalf("payload should omit empty next_stage: %s", payload)
	}
}

// TestSelectRelevantContextIncludesAndOrdersPriorSummaries pins live prior summaries and deterministic ordering.
func TestSelectRelevantContextIncludesAndOrdersPriorSummaries(t *testing.T) {
	prior := map[string]string{
		"static_analyzer":  "static summary",
		"code_writer":      "code summary",
		"security_auditor": "security summary",
	}
	roster := []string{"code_writer", "static_analyzer"}
	want := []string{"static context", "code_writer: code summary", "static_analyzer: static summary", "security_auditor: security summary"}
	for i := 0; i < 20; i++ {
		got := selectRelevantContext([]string{"static context"}, prior, nil, roster)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: context = %#v, want %#v", i, got, want)
		}
	}
	keyOrder := selectRelevantContext(nil, map[string]string{"z": "z summary", "a": "a summary"}, nil, nil)
	if wantKeys := []string{"a: a summary", "z: z summary"}; !reflect.DeepEqual(keyOrder, wantKeys) {
		t.Fatalf("key fallback order = %#v, want %#v", keyOrder, wantKeys)
	}
}

// TestTestGeneratorPayloadDoesNotDuplicateCodeWriterSummary pins the existing single summary edge.
func TestTestGeneratorPayloadDoesNotDuplicateCodeWriterSummary(t *testing.T) {
	output := schemas.TestGeneratorOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_tests", string(args))}
	input := newHarnessInput("write tests")
	input.PriorSummaries = map[string]string{"code_writer": "implemented the fix"}

	if _, err := (TestGenerator{}).Run(context.Background(), input, provider, StageOptions{WorkDir: t.TempDir(), Language: "go"}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	payload := modelUserPayload(t, provider.request)
	if count := strings.Count(payload, "code_writer: implemented the fix"); count != 1 {
		t.Fatalf("code_writer summary count = %d, want 1 in payload %s", count, payload)
	}
}

// Regression: the stage received only a prose summary, so it wrote tests for
// symbols that did not exist and the run never went green.
func TestTestGeneratorPayloadCarriesWriterChangedPaths(t *testing.T) {
	output := schemas.TestGeneratorOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_tests", string(args))}
	input := newHarnessInput("write tests for the implementation")
	input.PriorSummaries = map[string]string{"code_writer": "implemented storage"}
	input.PriorChangedFiles = map[string][]string{"code_writer": {"storage.go", "storage_session.go"}}

	if _, err := (TestGenerator{}).Run(context.Background(), input, provider, StageOptions{WorkDir: t.TempDir(), Language: "go"}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	var payload schemas.TestGeneratorInput
	if err := json.Unmarshal([]byte(modelUserPayload(t, provider.request)), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !reflect.DeepEqual(payload.WriterChangedPaths, []string{"storage.go", "storage_session.go"}) {
		t.Fatalf("writer_changed_paths = %v, want actual writer paths", payload.WriterChangedPaths)
	}
	if !slices.Contains(payload.RelevantContext, "code_writer: implemented storage") {
		t.Fatalf("writer summary missing from relevant context: %v", payload.RelevantContext)
	}
}

func TestTestGeneratorPayloadBoundsWriterChangedPaths(t *testing.T) {
	output := schemas.TestGeneratorOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_tests", string(args))}
	paths := make([]string, maxWriterChangedPaths+25)
	for i := range paths {
		paths[i] = fmt.Sprintf("generated_%02d.go", i)
	}
	input := newHarnessInput("write tests")
	input.PriorChangedFiles = map[string][]string{"code_writer": paths}

	if _, err := (TestGenerator{}).Run(context.Background(), input, provider, StageOptions{WorkDir: t.TempDir(), Language: "go"}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	var payload schemas.TestGeneratorInput
	if err := json.Unmarshal([]byte(modelUserPayload(t, provider.request)), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := len(payload.WriterChangedPaths); got != maxWriterChangedPaths {
		t.Fatalf("writer_changed_paths length = %d, want cap %d", got, maxWriterChangedPaths)
	}
	if payload.WriterChangedPaths[maxWriterChangedPaths-1] != paths[maxWriterChangedPaths-1] {
		t.Fatalf("writer_changed_paths was not bounded to the first paths: %v", payload.WriterChangedPaths)
	}
}

// Regression: the stage's own output blocked its retry.
func TestTestGeneratorRetryCanRewritePriorFile(t *testing.T) {
	workDir := t.TempDir()
	first := schemas.TestGeneratorOutput{
		Files:      []schemas.FileChange{{Path: "storage_test.go", Content: "package storage\n", ChangeType: "create"}},
		Language:   "go",
		Intent:     "create storage tests",
		Confidence: 0.9,
	}
	firstArgs, _ := json.Marshal(first)
	firstProvider := &requestCapturingProvider{events: toolCallEvent("submit_tests", string(firstArgs))}
	if _, err := (TestGenerator{}).Run(context.Background(), newHarnessInput("write storage tests"), firstProvider, StageOptions{WorkDir: workDir, Language: "go"}); err != nil {
		t.Fatalf("first stage run: %v", err)
	}

	revision := "Files written by the prior iteration: storage_test.go"
	second := first
	second.Files[0] = schemas.FileChange{Path: "storage_test.go", Content: "package storage\n\n// revised\n", ChangeType: "modify"}
	secondArgs, _ := json.Marshal(second)
	secondProvider := &requestCapturingProvider{events: toolCallEvent("submit_tests", string(secondArgs))}
	input := newHarnessInput("revise storage tests")
	input.RevisionContext = &revision
	if _, err := (TestGenerator{}).Run(context.Background(), input, secondProvider, StageOptions{WorkDir: workDir, Language: "go"}); err != nil {
		t.Fatalf("retry stage run: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "storage_test.go"))
	if err != nil {
		t.Fatalf("read rewritten test file: %v", err)
	}
	if !strings.Contains(string(content), "revised") {
		t.Fatalf("retry did not rewrite prior file: %q", content)
	}
	if !strings.Contains(secondProvider.request.Messages[0].Content, "overwrite: true") {
		t.Fatalf("retry system prompt did not explain deliberate overwrite: %q", secondProvider.request.Messages[0].Content)
	}
	payload := modelUserPayload(t, secondProvider.request)
	var typedPayload schemas.TestGeneratorInput
	if err := json.Unmarshal([]byte(payload), &typedPayload); err != nil {
		t.Fatalf("unmarshal retry payload: %v", err)
	}
	if typedPayload.RevisionContext == nil || !strings.Contains(*typedPayload.RevisionContext, "storage_test.go") {
		t.Fatalf("retry payload revision_context omitted prior file path: %q", payload)
	}
}

// TestPlanCriticPayloadOmitsRosterWithoutPipeline pins design-phase absence of roster fields.
func TestPlanCriticPayloadOmitsRosterWithoutPipeline(t *testing.T) {
	plan := schemas.DesignPlan{
		Source: "conversation", Epic: "add feature", Requirements: []string{"it works"},
		InScope: []string{"code"}, Tasks: []schemas.Task{{ID: "t1", Title: "write code", Intent: "impl"}},
	}
	critique := schemas.PlanCritique{Critiques: []schemas.Critique{}, CrossCuttingConcerns: []string{}, OverallAssessment: "sound"}
	args, _ := json.Marshal(critique)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_critique", string(args))}
	if _, err := (PlanCritic{}).Run(context.Background(), newHarnessInput("review plan"), provider, StageOptions{Plan: &plan}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	payload := modelUserPayload(t, provider.request)
	if strings.Contains(payload, "pipeline_stages") || strings.Contains(payload, "next_stage") {
		t.Fatalf("design-phase payload should omit roster fields: %s", payload)
	}
}

func TestPlanCriticPayloadCarriesPreviousCritique(t *testing.T) {
	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "add feature",
		Requirements: []string{"it works"},
		InScope:      []string{"code"},
		Tasks:        []schemas.Task{{ID: "t1", Title: "write code", Intent: "impl"}},
	}
	previousPlan := plan
	previousPlan.Epic = "previous feature"
	previousCritique := schemas.PlanCritique{
		Critiques:         []schemas.Critique{{Category: "correctness", Severity: schemas.SeverityMedium, Issue: "the old issue"}},
		OverallAssessment: "address this issue",
	}
	args, _ := json.Marshal(schemas.PlanCritique{OverallAssessment: "new review"})
	provider := &requestCapturingProvider{events: toolCallEvent("submit_critique", string(args))}
	if _, err := (PlanCritic{}).Run(context.Background(), newHarnessInput("review plan"), provider, StageOptions{
		Plan:             &plan,
		PreviousPlan:     &previousPlan,
		PreviousCritique: &previousCritique,
	}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	payload := modelUserPayload(t, provider.request)
	var input schemas.PlanCriticInput
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatalf("unmarshal plan critic payload: %v", err)
	}
	if input.PreviousCritique == nil || input.PreviousCritique.Critiques[0].Issue != "the old issue" {
		t.Fatalf("previous critique missing from payload: %#v", input.PreviousCritique)
	}
	if input.PreviousPlan == nil || input.PreviousPlan.Epic != "previous feature" {
		t.Fatalf("previous plan missing from payload: %#v", input.PreviousPlan)
	}
}

func modelUserPayload(t *testing.T, request zeroruntime.CompletionRequest) string {
	t.Helper()
	for _, message := range request.Messages {
		if message.Role == zeroruntime.MessageRoleUser {
			return message.Content
		}
	}
	t.Fatalf("model request has no user payload: %#v", request.Messages)
	return ""
}

func TestCodeWriterRunOmitsMemoryFieldWhenNil(t *testing.T) {
	workDir := t.TempDir()
	output := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{},
		Language:   "go",
		Intent:     "no changes",
		Confidence: 0.9,
	}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_code", string(args))}

	stage := CodeWriter{}
	input := newHarnessInput("write code")
	_, err := stage.Run(context.Background(), input, provider, StageOptions{WorkDir: workDir, Language: "go"})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if len(provider.request.Messages) < 2 {
		t.Fatalf("expected user message, got %d messages", len(provider.request.Messages))
	}
	payload := provider.request.Messages[1].Content
	if strings.Contains(payload, "\"memory\"") {
		t.Fatalf("payload should omit memory field: %s", payload)
	}
	var cwInput schemas.CodeWriterInput
	if err := json.Unmarshal([]byte(payload), &cwInput); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if cwInput.Memory != nil {
		t.Fatalf("expected nil memory slice, got %#v", cwInput.Memory)
	}
}

func TestApplyFileChangesRegistryBacked(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		workDir := t.TempDir()
		files := []schemas.FileChange{{Path: "a.go", Content: "package a\n", ChangeType: "create"}}
		res, err := applyFileChanges(context.Background(), workDir, files, registryRunTool(t, workDir))
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(res.Applied) != 1 {
			t.Fatalf("applied = %d, want 1", len(res.Applied))
		}
		if _, err := os.Stat(filepath.Join(workDir, "a.go")); err != nil {
			t.Fatalf("file not created: %v", err)
		}
	})

	t.Run("modify", func(t *testing.T) {
		workDir := t.TempDir()
		path := filepath.Join(workDir, "b.go")
		if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files := []schemas.FileChange{{Path: "b.go", Content: "new\n", ChangeType: "modify"}}
		res, err := applyFileChanges(context.Background(), workDir, files, registryRunTool(t, workDir))
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(res.Applied) != 1 || res.Applied[0].BytesRead != 4 {
			t.Fatalf("unexpected applied: %+v", res.Applied)
		}
		content, _ := os.ReadFile(path)
		if string(content) != "new\n" {
			t.Fatalf("unexpected content: %q", string(content))
		}
	})

	t.Run("delete", func(t *testing.T) {
		workDir := t.TempDir()
		path := filepath.Join(workDir, "c.go")
		if err := os.WriteFile(path, []byte("gone\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files := []schemas.FileChange{{Path: "c.go", ChangeType: "delete"}}
		res, err := applyFileChanges(context.Background(), workDir, files, registryRunTool(t, workDir))
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(res.Applied) != 1 || res.Applied[0].BytesRead != 5 {
			t.Fatalf("unexpected applied: %+v", res.Applied)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected file removed, stat err=%v", err)
		}
	})
}

// staticScope satisfies tools.PathScope with a fixed root list; Roots()[0]
// must be the workspace root, mirroring sandbox.Scope ordering.
type staticScope []string

func (s staticScope) Roots() []string { return s }

// TestApplyFileChangesHonorsScopeGrantedRoot pins the --add-dir contract: in
// registry mode the scoped tool is the confinement authority, so a target
// inside an explicitly granted extra write root must apply successfully even
// though it does not resolve inside the workspace.
func TestApplyFileChangesHonorsScopeGrantedRoot(t *testing.T) {
	workDir := t.TempDir()
	extraRoot := t.TempDir()
	scope := staticScope{workDir, extraRoot}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedWriteFileTool(workDir, scope))
	registry.Register(tools.NewScopedDeleteFileTool(workDir, scope))
	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		res := registry.RunWithOptions(ctx, name, args, tools.RunOptions{PermissionGranted: true})
		return ToolResult{OK: res.Status == tools.StatusOK, Output: res.Output}, nil
	}

	target := filepath.Join(extraRoot, "granted.txt")
	files := []schemas.FileChange{{Path: target, Content: "granted", ChangeType: "create"}}
	res, err := applyFileChanges(context.Background(), workDir, files, runTool)
	if err != nil {
		t.Fatalf("apply scope-granted create: %v", err)
	}
	if len(res.Applied) != 1 || res.Applied[0].Path != target {
		t.Fatalf("unexpected applied record: %+v", res.Applied)
	}
	content, rerr := os.ReadFile(target)
	if rerr != nil || string(content) != "granted" {
		t.Fatalf("scope-granted file not written: err=%v content=%q", rerr, string(content))
	}

	// Without the grant, the same escaping path must still fail via the tool.
	ungranted := tools.NewRegistry()
	ungranted.Register(tools.NewWriteFileTool(workDir))
	ungranted.Register(tools.NewDeleteFileTool(workDir))
	ungrantedRun := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		res := ungranted.RunWithOptions(ctx, name, args, tools.RunOptions{PermissionGranted: true})
		return ToolResult{OK: res.Status == tools.StatusOK, Output: res.Output}, nil
	}
	escape := filepath.Join(extraRoot, "escape.txt")
	_, err = applyFileChanges(context.Background(), workDir, []schemas.FileChange{{Path: escape, Content: "x", ChangeType: "create"}}, ungrantedRun)
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("expected workspace confinement error from tool, got %v", err)
	}
	if _, serr := os.Stat(escape); !os.IsNotExist(serr) {
		t.Fatalf("escape file must not exist, stat err=%v", serr)
	}
}

func TestApplyFileChangesHandlesFailures(t *testing.T) {
	t.Run("denied permission", func(t *testing.T) {
		workDir := t.TempDir()
		files := []schemas.FileChange{{Path: "x.go", Content: "x", ChangeType: "create"}}
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			return ToolResult{OK: false, Output: "permission denied"}, nil
		}
		_, err := applyFileChanges(context.Background(), workDir, files, runTool)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected permission error, got %v", err)
		}
	})

	t.Run("out of workspace", func(t *testing.T) {
		workDir := t.TempDir()
		files := []schemas.FileChange{{Path: "../escape.txt", Content: "x", ChangeType: "create"}}
		_, err := applyFileChanges(context.Background(), workDir, files, nil)
		if err == nil || !strings.Contains(err.Error(), "outside workspace") {
			t.Fatalf("expected workspace error, got %v", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		workDir := t.TempDir()
		files := []schemas.FileChange{{Path: "x.go", Content: "x", ChangeType: "create"}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := applyFileChanges(ctx, workDir, files, nil)
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("expected cancellation error, got %v", err)
		}
	})
}

func TestApplyFileChangesDirectFallback(t *testing.T) {
	workDir := t.TempDir()

	t.Run("create does not overwrite", func(t *testing.T) {
		path := filepath.Join(workDir, "d.go")
		if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
			t.Fatal(err)
		}
		files := []schemas.FileChange{{Path: "d.go", Content: "second", ChangeType: "create"}}
		_, err := applyFileChanges(context.Background(), workDir, files, nil)
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected overwrite refusal, got %v", err)
		}
	})

	t.Run("modify requires existing regular file", func(t *testing.T) {
		files := []schemas.FileChange{{Path: "e.go", Content: "x", ChangeType: "modify"}}
		_, err := applyFileChanges(context.Background(), workDir, files, nil)
		if err == nil || !strings.Contains(err.Error(), "no such file") {
			t.Fatalf("expected missing-file error, got %v", err)
		}
	})

	t.Run("delete removes regular file", func(t *testing.T) {
		path := filepath.Join(workDir, "f.go")
		if err := os.WriteFile(path, []byte("remove"), 0o644); err != nil {
			t.Fatal(err)
		}
		files := []schemas.FileChange{{Path: "f.go", ChangeType: "delete"}}
		res, err := applyFileChanges(context.Background(), workDir, files, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(res.Applied) != 1 {
			t.Fatalf("applied = %d, want 1", len(res.Applied))
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected file removed, stat err=%v", err)
		}
	})

	t.Run("delete refuses directory", func(t *testing.T) {
		dir := filepath.Join(workDir, "sub")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		files := []schemas.FileChange{{Path: "sub", ChangeType: "delete"}}
		_, err := applyFileChanges(context.Background(), workDir, files, nil)
		if err == nil || !strings.Contains(err.Error(), "is a directory") {
			t.Fatalf("expected directory refusal, got %v", err)
		}
	})
}

func TestCodeWriterRequestsContext(t *testing.T) {
	stage := CodeWriter{}
	input := newHarnessInput("fix main.go bug")
	result, err := stage.Run(context.Background(), input, &fakeProvider{}, StageOptions{PullContext: true})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if result.ContextRequest == nil {
		t.Fatal("expected context request")
	}
	if result.ContextRequest.Queries[0].QueryType != schemas.ContextListFiles {
		t.Fatalf("expected list_files query, got %v", result.ContextRequest.Queries[0].QueryType)
	}
	foundRead := false
	for _, q := range result.ContextRequest.Queries {
		if q.QueryType == schemas.ContextReadFile && q.Path != nil && *q.Path == "main.go" {
			foundRead = true
		}
	}
	if !foundRead {
		t.Fatalf("expected read_file main.go query, got %+v", result.ContextRequest.Queries)
	}
}

func TestTestGeneratorWritesTests(t *testing.T) {
	workDir := t.TempDir()
	output := schemas.TestGeneratorOutput{
		Files: []schemas.FileChange{
			{Path: "main_test.go", Content: "package main\n", ChangeType: "create"},
		},
		Language:   "go",
		Intent:     "add unit tests",
		Confidence: 0.85,
	}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_tests", string(args))}

	stage := TestGenerator{}
	input := newHarnessInput("add tests")
	result, err := stage.Run(context.Background(), input, provider, StageOptions{WorkDir: workDir, Language: "go"})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if result.Summary != output.Intent {
		t.Fatalf("expected summary %q, got %q", output.Intent, result.Summary)
	}
	if _, err := os.Stat(filepath.Join(workDir, "main_test.go")); err != nil {
		t.Fatalf("expected test file created: %v", err)
	}
	assertPipelineMetaPrompt(t, provider.request)
}

func TestTestRunnerPassesAndFails(t *testing.T) {
	// The runner executes stage commands under the deterministic profile,
	// which fails closed without native platform confinement.
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	stage := TestRunner{}
	workDir := t.TempDir()

	pass, err := stage.Run(context.Background(), newHarnessInput("run tests"), &fakeProvider{}, StageOptions{Command: []string{"true"}, TimeoutSeconds: 5, WorkDir: workDir, Sandbox: procrun.NewStageEngine(workDir)})
	if err != nil {
		t.Fatalf("pass run: %v", err)
	}
	if pass.Confidence != 1.0 {
		t.Fatalf("expected confidence 1.0, got %v", pass.Confidence)
	}
	if !strings.Contains(pass.Summary, "passed") {
		t.Fatalf("expected passed summary, got %q", pass.Summary)
	}
	if _, ok := pass.Data["test_command"]; !ok {
		t.Fatalf("expected test_command in output data, got %#v", pass.Data)
	}

	fail, err := stage.Run(context.Background(), newHarnessInput("run tests"), &fakeProvider{}, StageOptions{Command: []string{"false"}, TimeoutSeconds: 5, WorkDir: workDir, Sandbox: procrun.NewStageEngine(workDir)})
	if err != nil {
		t.Fatalf("fail run: %v", err)
	}
	if fail.Confidence != 0.8 {
		t.Fatalf("expected confidence 0.8, got %v", fail.Confidence)
	}
	if !strings.Contains(fail.Summary, "failed") {
		t.Fatalf("expected failed summary, got %q", fail.Summary)
	}
}

func TestTestRunnerRunToolPath(t *testing.T) {
	stage := TestRunner{}
	workDir := t.TempDir()
	input := newHarnessInput("run tests")

	t.Run("denied permission", func(t *testing.T) {
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			return ToolResult{
				OK:     false,
				Output: "Permission required for bash: Shell commands can read, write, or execute programs.",
			}, nil
		}
		_, err := stage.Run(context.Background(), input, &fakeProvider{}, StageOptions{
			WorkDir:        workDir,
			Command:        []string{"go", "test", "./..."},
			TimeoutSeconds: 5,
			RunTool:        runTool,
		})
		if err == nil || !strings.Contains(err.Error(), "denied") {
			t.Fatalf("expected permission denied error, got %v", err)
		}
	})

	t.Run("auto-approved pass", func(t *testing.T) {
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			return ToolResult{
				OK:     true,
				Output: "Command completed with no output.",
				Meta:   map[string]string{"exit_code": "0"},
			}, nil
		}
		result, err := stage.Run(context.Background(), input, &fakeProvider{}, StageOptions{
			WorkDir:        workDir,
			Command:        []string{"go", "test", "./..."},
			TimeoutSeconds: 5,
			RunTool:        runTool,
		})
		if err != nil {
			t.Fatalf("stage run: %v", err)
		}
		if result.Confidence != 1.0 {
			t.Fatalf("expected confidence 1.0, got %v", result.Confidence)
		}
		if !strings.Contains(result.Summary, "passed") {
			t.Fatalf("expected passed summary, got %q", result.Summary)
		}
	})

	t.Run("test failure", func(t *testing.T) {
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			return ToolResult{
				OK:     false,
				Output: "exit_code: 1",
				Meta:   map[string]string{"exit_code": "1"},
			}, nil
		}
		result, err := stage.Run(context.Background(), input, &fakeProvider{}, StageOptions{
			WorkDir:        workDir,
			Command:        []string{"go", "test", "./..."},
			TimeoutSeconds: 5,
			RunTool:        runTool,
		})
		if err != nil {
			t.Fatalf("stage run: %v", err)
		}
		if result.Confidence != 0.8 {
			t.Fatalf("expected confidence 0.8, got %v", result.Confidence)
		}
		results, _ := result.Data["test_results"].(schemas.TestRunResults)
		if results.ExitCode != 1 {
			t.Fatalf("expected exit code 1, got %d", results.ExitCode)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			return ToolResult{
				OK:     false,
				Output: "Error: Command timed out after 5000ms.",
				Meta:   map[string]string{"exit_code": "-1"},
			}, nil
		}
		result, err := stage.Run(context.Background(), input, &fakeProvider{}, StageOptions{
			WorkDir:        workDir,
			Command:        []string{"go", "test", "./..."},
			TimeoutSeconds: 5,
			RunTool:        runTool,
		})
		if err != nil {
			t.Fatalf("stage run: %v", err)
		}
		results, _ := result.Data["test_results"].(schemas.TestRunResults)
		if results.ExitCode != 124 {
			t.Fatalf("expected exit code 124, got %d", results.ExitCode)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			return ToolResult{}, ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := stage.Run(ctx, input, &fakeProvider{}, StageOptions{
			WorkDir:        workDir,
			Command:        []string{"go", "test", "./..."},
			TimeoutSeconds: 5,
			RunTool:        runTool,
		})
		if err == nil {
			t.Fatal("expected cancellation error")
		}
	})
}

func TestStaticAnalyzerDetectsGoSyntaxError(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "bad.go"), []byte("package main\n\nfunc main( {\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stage, err := NewStaticAnalyzer(DefaultQualityChecks()...)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	result, err := stage.Run(context.Background(), newHarnessInput("analyze code"), nil, StageOptions{WorkDir: workDir, Language: "go"})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	report, ok := result.Data["static_analyzer_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("static_analyzer_output missing or wrong type, got %T", result.Data["static_analyzer_output"])
	}
	if len(report.Findings) == 0 {
		t.Fatalf("expected deterministic syntax error, got %+v", report)
	}
	if report.Status != schemas.VerificationFindings {
		t.Fatalf("status = %q, want findings", report.Status)
	}
	if result.Confidence != 1.0 {
		t.Fatalf("confidence = %v, want 1.0", result.Confidence)
	}
}

func TestStaticAnalyzerFindingsIgnoreProvider(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "bad.go"), []byte("package main\n\nfunc main( {\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stage, err := NewStaticAnalyzer(DefaultQualityChecks()...)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	input := newHarnessInput("analyze code")
	baseline, err := stage.Run(context.Background(), input, nil, StageOptions{WorkDir: workDir, Language: "go"})
	if err != nil {
		t.Fatalf("baseline run: %v", err)
	}
	withProvider, err := stage.Run(context.Background(), input, panickingProvider{}, StageOptions{
		WorkDir:       workDir,
		Language:      "go",
		ModelOverride: "must-not-be-used",
	})
	if err != nil {
		t.Fatalf("provider-supplied run: %v", err)
	}
	if !reflect.DeepEqual(withProvider.Data["static_analyzer_output"], baseline.Data["static_analyzer_output"]) {
		t.Fatalf("provider changed deterministic output:\nwith provider: %#v\nbaseline: %#v", withProvider.Data["static_analyzer_output"], baseline.Data["static_analyzer_output"])
	}
	if withProvider.Usage != nil {
		t.Fatalf("model-free analyzer reported usage: %#v", withProvider.Usage)
	}
}

func TestSecurityAuditorMockBandit(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x.py"), []byte("eval(input())\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	report := map[string]any{
		"results": []map[string]any{
			{"filename": "x.py", "line_range": []int{1}, "issue_text": "Use of eval", "issue_severity": "HIGH", "test_id": "B307"},
		},
	}
	mockReport, _ := json.Marshal(report)
	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "bandit" {
			return ToolResult{OK: true, Output: string(mockReport)}, nil
		}
		return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
	}

	stage, _ := NewSecurityAuditor(DefaultSecurityChecks()...)
	input := newHarnessInput("audit security")
	result, err := stage.Run(context.Background(), input, &fakeProvider{}, StageOptions{WorkDir: workDir, RunTool: runTool})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if !strings.Contains(result.Summary, "1 verification finding") {
		t.Fatalf("expected 1 finding summary, got %q", result.Summary)
	}
}

func TestSecurityAuditorEmptyLineRangeNoPanic(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x.py"), []byte("eval(input())\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	report := map[string]any{
		"results": []map[string]any{
			{"filename": "x.py", "line_range": []int{}, "issue_text": "Use of eval", "issue_severity": "HIGH", "test_id": "B307"},
		},
	}
	mockReport, _ := json.Marshal(report)
	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "bandit" {
			return ToolResult{OK: true, Output: string(mockReport)}, nil
		}
		return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
	}

	stage2, _ := NewSecurityAuditor(DefaultSecurityChecks()...)
	input := newHarnessInput("audit security")
	var result schemas.HarnessStageOutput
	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("stage panicked on empty line_range: %v", r)
			}
		}()
		result, runErr = stage2.Run(context.Background(), input, &fakeProvider{}, StageOptions{WorkDir: workDir, RunTool: runTool})
	}()
	if runErr != nil {
		t.Fatalf("stage run: %v", runErr)
	}
	vReport, _ := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if len(vReport.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(vReport.Findings))
	}
	finding := vReport.Findings[0]
	if finding.Line != nil && *finding.Line != 0 {
		t.Fatalf("expected nil or zero line for empty line_range, got %v", *finding.Line)
	}
}

func TestSecurityAuditorBanditUnavailableDegrades(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x.py"), []byte("eval(input())\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "bandit" {
			return ToolResult{
				OK:     false,
				Output: "Bandit is not installed or not available: exec: \"python\": executable file not found in $PATH",
			}, nil
		}
		return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
	}

	stage3, _ := NewSecurityAuditor(DefaultSecurityChecks()...)
	input := newHarnessInput("audit security")
	result, err := stage3.Run(context.Background(), input, &fakeProvider{}, StageOptions{WorkDir: workDir, RunTool: runTool})
	if err != nil {
		t.Fatalf("expected stage to degrade, got error: %v", err)
	}
	report, _ := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if report.Status != schemas.VerificationIncomplete {
		t.Fatalf("expected incomplete status, got %q", report.Status)
	}
	if !strings.Contains(report.Summary, "not installed") {
		t.Fatalf("expected not installed summary, got %q", report.Summary)
	}
}

func TestSecurityAuditorPermissionDeniedFails(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x.py"), []byte("eval(input())\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "bandit" {
			return ToolResult{
				OK:     false,
				Output: "Permission required for bandit: Runs the Bandit security scanner as a subprocess.",
			}, nil
		}
		return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
	}

	stage4, _ := NewSecurityAuditor(DefaultSecurityChecks()...)
	input := newHarnessInput("audit security")
	_, err := stage4.Run(context.Background(), input, &fakeProvider{}, StageOptions{WorkDir: workDir, RunTool: runTool})
	if err == nil {
		t.Fatal("expected permission denied error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "permission") {
		t.Fatalf("expected permission error, got %v", err)
	}
}

// TestPlanCriticCategoryEnumMatchesValidator guards the fix for the
// crystallize failure where the plan critic tool schema did not declare the
// category enum but Critique.Validate enforced it: the model had no source
// for the valid values and the run failed after retries. The schema enum and
// the validator must stay the same set.
func TestPlanCriticCategoryEnumMatchesValidator(t *testing.T) {
	tool := planCriticToolDefinition()
	properties, ok := tool.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing")
	}
	critiques, ok := properties["critiques"].(map[string]any)
	if !ok {
		t.Fatalf("schema critiques missing")
	}
	items, ok := critiques["items"].(map[string]any)
	if !ok {
		t.Fatalf("schema critique items missing")
	}
	itemProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema critique item properties missing")
	}
	category, ok := itemProperties["category"].(map[string]any)
	if !ok {
		t.Fatalf("schema category field missing")
	}
	enum, ok := category["enum"].([]string)
	if !ok || len(enum) == 0 {
		t.Fatalf("schema category must declare a non-empty enum (got %#v); without it the model has no source for the valid categories", category["enum"])
	}

	// Every schema enum value must pass the validator, and the schema enum
	// must contain every value the validator accepts, so the machine contract
	// and Critique.Validate cannot drift apart.
	for _, value := range schemas.CritiqueCategories() {
		found := false
		for _, schemaValue := range enum {
			if schemaValue == value {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("validator category %q is not in the plan critic schema enum %v", value, enum)
		}
	}
	for _, schemaValue := range enum {
		critique := schemas.Critique{Category: schemaValue, Severity: schemas.SeverityLow, Issue: "x"}
		if err := critique.Validate(); err != nil {
			t.Errorf("schema enum category %q fails Critique.Validate: %v", schemaValue, err)
		}
	}
	bogus := schemas.Critique{Category: "performance", Severity: schemas.SeverityLow, Issue: "x"}
	if err := bogus.Validate(); err == nil {
		t.Errorf("expected validator to reject category \"performance\" (the value seen in the production failure)")
	}
}

func TestPlanCriticReturnsCritique(t *testing.T) {
	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "add feature",
		Requirements: []string{"it works"},
		InScope:      []string{"code"},
		OutOfScope:   []string{"docs"},
		SystemDesign: "keep it simple",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "write code", Intent: "impl"},
		},
	}
	critique := schemas.PlanCritique{
		Critiques: []schemas.Critique{
			{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "vague"},
		},
		CrossCuttingConcerns:   []string{},
		MustFixBeforeExecution: true,
		OverallAssessment:      "too vague",
	}
	args, _ := json.Marshal(critique)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_critique", string(args))}

	stage := PlanCritic{}
	input := newHarnessInput("review plan")
	result, err := stage.Run(context.Background(), input, provider, StageOptions{Plan: &plan})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if !strings.Contains(result.Summary, "1 issue") {
		t.Fatalf("expected critique summary, got %q", result.Summary)
	}
	if result.Detail != "too vague" {
		t.Fatalf("expected detail too vague, got %q", result.Detail)
	}
	assertPipelineMetaPrompt(t, provider.request)
}

func TestStepBackReturnsAnalysis(t *testing.T) {
	analysis := schemas.StepBackAnalysis{
		HypothesizedRootCause: "wrong data structure for lookups",
		Evidence:              []string{"O(n) scan on every request"},
		RecommendedApproach:   "switch to a hash map",
		Confidence:            0.75,
	}
	args, _ := json.Marshal(analysis)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_step_back", string(args))}

	report := StepBackReport{
		Intent:       "add a lookup function",
		RecentScores: []float64{60, 60, 60},
		Reason:       "score plateaued over last 3 iterations",
	}
	opts := StageOptions{WorkDir: t.TempDir()}
	got, err := StepBack(context.Background(), provider, opts, report)
	if err != nil {
		t.Fatalf("StepBack: %v", err)
	}
	if got.HypothesizedRootCause != analysis.HypothesizedRootCause {
		t.Fatalf("root cause = %q, want %q", got.HypothesizedRootCause, analysis.HypothesizedRootCause)
	}
	if got.RecommendedApproach != analysis.RecommendedApproach {
		t.Fatalf("approach = %q, want %q", got.RecommendedApproach, analysis.RecommendedApproach)
	}
	if got.Confidence != analysis.Confidence {
		t.Fatalf("confidence = %v, want %v", got.Confidence, analysis.Confidence)
	}
	assertPipelineMetaPrompt(t, provider.request)
}

func TestStepBackNilProvider(t *testing.T) {
	report := StepBackReport{Intent: "x", Reason: "plateau"}
	_, err := StepBack(context.Background(), nil, StageOptions{}, report)
	if err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestStepBackInvalidAnalysis(t *testing.T) {
	// Empty root cause should fail validation.
	analysis := schemas.StepBackAnalysis{
		HypothesizedRootCause: "",
		RecommendedApproach:   "try something",
		Confidence:            0.5,
	}
	args, _ := json.Marshal(analysis)
	provider := &fakeProvider{events: toolCallEvent("submit_step_back", string(args))}

	report := StepBackReport{Intent: "x", Reason: "plateau"}
	_, err := StepBack(context.Background(), provider, StageOptions{}, report)
	if err == nil {
		t.Fatal("expected validation error for empty root cause")
	}
}

func TestStepBackMissingToolCall(t *testing.T) {
	// Provider returns a different tool call, not submit_step_back.
	provider := &fakeProvider{events: toolCallEvent("submit_code", `{}`)}
	report := StepBackReport{Intent: "x", Reason: "plateau"}
	_, err := StepBack(context.Background(), provider, StageOptions{}, report)
	if err == nil || !strings.Contains(err.Error(), "did not call") {
		t.Fatalf("expected missing tool call error, got %v", err)
	}
}

func TestStepBackFallbackModelIsTierLabel(t *testing.T) {
	// With no ModelOverride the fallback label is what the typed-output error
	// reports. It must stay a tier label: naming a specific model tells a user
	// on another provider to fix a model they never configured.
	provider := &fakeProvider{events: toolCallEvent("submit_code", `{}`)}
	_, err := StepBack(context.Background(), provider, StageOptions{}, StepBackReport{Intent: "x", Reason: "plateau"})
	var typedErr *TypedOutputError
	if !errors.As(err, &typedErr) {
		t.Fatalf("error = %T %v, want TypedOutputError", err, err)
	}
	if typedErr.Model != "reasoning" {
		t.Fatalf("Model = %q, want %q", typedErr.Model, "reasoning")
	}
}

func TestDesignCrystallizer(t *testing.T) {
	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "feature",
		Requirements: []string{"works"},
		InScope:      []string{"code"},
		OutOfScope:   []string{"docs"},
		SystemDesign: "keep it simple",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "task one", Intent: "do it"},
		},
	}
	args, _ := json.Marshal(plan)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_design_plan", string(args))}
	stage := DesignCrystallizer{}
	input := schemas.DesignConversationInput{
		History: []schemas.ConversationMessage{
			{Role: "user", Content: "Do it."},
		},
	}
	got, err := stage.Crystallize(context.Background(), provider, StageOptions{}, input)
	if err != nil {
		t.Fatalf("crystallize: %v", err)
	}
	if got.Epic != "feature" {
		t.Fatalf("expected feature, got %q", got.Epic)
	}
	if got.Source != "conversation" {
		t.Fatalf("expected source conversation, got %q", got.Source)
	}
	assertPipelineMetaPrompt(t, provider.request)
}

// The design conversation can use auto tool calls on models whose OpenRouter
// endpoint rejects forced tool_choice requests. Crystallization must retry once
// in auto mode and keep its typed plan validation.
func TestDesignCrystallizerFallsBackWhenForcedToolChoiceIsRejected(t *testing.T) {
	plan := schemas.DesignPlan{
		Epic:         "feature",
		Requirements: []string{"works"},
		InScope:      []string{"code"},
		Tasks:        []schemas.Task{{ID: "t1", Title: "task one", Intent: "do it"}},
	}
	args, _ := json.Marshal(plan)
	toolName := designPlanToolDefinition().Name
	provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{
		{{Type: zeroruntime.StreamEventError, Error: "provider request error: Provider returned error"}},
		toolCallEvent(toolName, string(args)),
	}}
	input := schemas.DesignConversationInput{History: []schemas.ConversationMessage{{Role: "user", Content: "Do it."}}}

	got, err := (DesignCrystallizer{}).Crystallize(context.Background(), provider, StageOptions{}, input)
	if err != nil {
		t.Fatalf("crystallize: %v", err)
	}
	if got.Epic != plan.Epic || got.Source != "conversation" {
		t.Fatalf("plan = %#v", got)
	}
	if len(provider.requests) != 2 || provider.requests[0].ToolChoice != toolName || provider.requests[1].ToolChoice != "" {
		t.Fatalf("requests = %#v, want forced then auto", provider.requests)
	}
}

func TestDesignCrystallizerSetsSourceBeforeValidation(t *testing.T) {
	plan := schemas.DesignPlan{
		Source:       "", // empty; Validate would reject this if not set first
		Epic:         "feature",
		Requirements: []string{"works"},
		InScope:      []string{"code"},
		OutOfScope:   []string{"docs"},
		SystemDesign: "keep it simple",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "task one", Intent: "do it"},
		},
	}
	args, _ := json.Marshal(plan)
	provider := &fakeProvider{events: toolCallEvent("submit_design_plan", string(args))}
	stage := DesignCrystallizer{}
	input := schemas.DesignConversationInput{
		History: []schemas.ConversationMessage{
			{Role: "user", Content: "Do it."},
		},
	}
	got, err := stage.Crystallize(context.Background(), provider, StageOptions{}, input)
	if err != nil {
		t.Fatalf("crystallize should set Source before validation: %v", err)
	}
	if got.Source != "conversation" {
		t.Fatalf("expected source conversation, got %q", got.Source)
	}
}

func TestDesignCrystallizerRejectsPlanMissingInScope(t *testing.T) {
	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "feature",
		Requirements: []string{"works"},
		InScope:      nil, // missing required field
		OutOfScope:   []string{"docs"},
		SystemDesign: "keep it simple",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "task one", Intent: "do it"},
		},
	}
	args, _ := json.Marshal(plan)
	provider := &fakeProvider{events: toolCallEvent("submit_design_plan", string(args))}
	stage := DesignCrystallizer{}
	input := schemas.DesignConversationInput{
		History: []schemas.ConversationMessage{
			{Role: "user", Content: "Do it."},
		},
	}
	_, err := stage.Crystallize(context.Background(), provider, StageOptions{}, input)
	if err == nil {
		t.Fatalf("expected error for plan missing in_scope")
	}
}

func TestDesignConversationPrompt(t *testing.T) {
	prompt := DesignConversationPrompt()
	if prompt == "" {
		t.Fatalf("DesignConversationPrompt should not be empty")
	}
	if !strings.Contains(prompt, "Design Conversation") {
		t.Fatalf("prompt %q should contain 'Design Conversation'", prompt)
	}
}

// A real user asked the design agent to "hand off and start". The agent had a
// read-only registry and no Task tool, so it invented a specialist and then
// called skill, which errored. The prompt must state the limit and name the
// commands that actually unblock execution.
func TestDesignConversationPromptStatesItsLimits(t *testing.T) {
	prompt := DesignConversationPrompt()
	for _, want := range []string{"/crystallize", "/approve", "read-only"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("design prompt must mention %q so the agent can redirect the user", want)
		}
	}
	// The execution-phase contract is false here: this phase is free-form and
	// holds real read tools. Injecting it would tell the agent it cannot read.
	if strings.Contains(prompt, "Your input is a typed JSON structure") {
		t.Fatal("design prompt must not carry the execution-phase typed input/output contract")
	}
	// The shared overview must be present so the agent knows the two phases.
	if !strings.Contains(prompt, SpliceOverviewPrompt()) {
		t.Fatal("design prompt must include the shared Splice overview")
	}
}

func TestPipelineStagePromptStatesItsLimits(t *testing.T) {
	composed := composeSystemPrompt("STAGE BODY")
	if !strings.Contains(composed, SpliceOverviewPrompt()) {
		t.Fatal("composed stage prompt must include the shared overview")
	}
	if !strings.Contains(composed, "Your input is a typed JSON structure") {
		t.Fatal("composed stage prompt must include the execution-phase contract")
	}
	if !strings.Contains(composed, "STAGE BODY") {
		t.Fatal("composed stage prompt must include the stage's own prompt")
	}
}

func TestExtractPlanCritique(t *testing.T) {
	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "add feature",
		Requirements: []string{"it works"},
		InScope:      []string{"code"},
		OutOfScope:   []string{"docs"},
		SystemDesign: "keep it simple",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "write code", Intent: "impl"},
		},
	}
	critique := schemas.PlanCritique{
		Critiques: []schemas.Critique{
			{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "vague", SuggestedMitigation: "add detail"},
		},
		CrossCuttingConcerns:   []string{},
		MustFixBeforeExecution: true,
		OverallAssessment:      "too vague",
	}
	args, _ := json.Marshal(critique)
	provider := &fakeProvider{events: toolCallEvent("submit_critique", string(args))}

	stage := PlanCritic{}
	input := newHarnessInput("review plan")
	result, err := stage.Run(context.Background(), input, provider, StageOptions{Plan: &plan})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	extracted, err := ExtractPlanCritique(result)
	if err != nil {
		t.Fatalf("extract critique: %v", err)
	}
	if extracted.OverallAssessment != "too vague" {
		t.Fatalf("assessment = %q, want %q", extracted.OverallAssessment, "too vague")
	}
	if len(extracted.Critiques) != 1 {
		t.Fatalf("critiques = %d, want 1", len(extracted.Critiques))
	}
	if extracted.Critiques[0].SuggestedMitigation != "add detail" {
		t.Fatalf("suggested_mitigation = %q, want %q", extracted.Critiques[0].SuggestedMitigation, "add detail")
	}
}

func TestExtractPlanCritiqueMissingKey(t *testing.T) {
	output := schemas.HarnessStageOutput{Data: map[string]any{}}
	_, err := ExtractPlanCritique(output)
	if err == nil {
		t.Fatalf("expected error when plan_critic_output is absent")
	}
}

func TestPlanCriticToolSchemaUsesSuggestedMitigation(t *testing.T) {
	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "add feature",
		Requirements: []string{"it works"},
		InScope:      []string{"code"},
		OutOfScope:   []string{"docs"},
		SystemDesign: "keep it simple",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "write code", Intent: "impl"},
		},
	}
	critique := schemas.PlanCritique{
		Critiques:              []schemas.Critique{{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "vague"}},
		CrossCuttingConcerns:   []string{},
		MustFixBeforeExecution: true,
		OverallAssessment:      "too vague",
	}
	args, _ := json.Marshal(critique)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_critique", string(args))}

	stage := PlanCritic{}
	input := newHarnessInput("review plan")
	_, err := stage.Run(context.Background(), input, provider, StageOptions{Plan: &plan})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if len(provider.request.Tools) == 0 {
		t.Fatalf("expected request to have tools")
	}
	schemaJSON, err := json.Marshal(provider.request.Tools[0].Parameters)
	if err != nil {
		t.Fatalf("marshal tool parameters: %v", err)
	}
	schemaStr := string(schemaJSON)
	if !strings.Contains(schemaStr, "suggested_mitigation") {
		t.Fatalf("tool parameters missing suggested_mitigation: %s", schemaStr)
	}
	if strings.Contains(schemaStr, "\"mitigation\"") {
		t.Fatalf("tool parameters still contain mitigation key: %s", schemaStr)
	}
}

func TestCrystallizeToolSchemaUsesIntentAndStatement(t *testing.T) {
	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "feature",
		Requirements: []string{"works"},
		InScope:      []string{"code"},
		OutOfScope:   []string{"docs"},
		SystemDesign: "keep it simple",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "task one", Intent: "do it", AcceptanceFacts: []schemas.AcceptanceFact{{Statement: "it works"}}},
		},
	}
	args, _ := json.Marshal(plan)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_design_plan", string(args))}
	stage := DesignCrystallizer{}
	input := schemas.DesignConversationInput{
		History: []schemas.ConversationMessage{
			{Role: "user", Content: "Do it."},
		},
	}
	_, err := stage.Crystallize(context.Background(), provider, StageOptions{}, input)
	if err != nil {
		t.Fatalf("crystallize: %v", err)
	}
	if len(provider.request.Tools) == 0 {
		t.Fatalf("expected request to have tools")
	}
	schemaJSON, err := json.Marshal(provider.request.Tools[0].Parameters)
	if err != nil {
		t.Fatalf("marshal tool parameters: %v", err)
	}
	schemaStr := string(schemaJSON)
	if !strings.Contains(schemaStr, "\"intent\"") {
		t.Fatalf("tool parameters missing intent field: %s", schemaStr)
	}
	if strings.Contains(schemaStr, "\"description\"") {
		t.Fatalf("tool parameters still contain description field in task: %s", schemaStr)
	}
	if !strings.Contains(schemaStr, "\"statement\"") {
		t.Fatalf("tool parameters missing statement field: %s", schemaStr)
	}
	if strings.Contains(schemaStr, "\"fact\"") {
		t.Fatalf("tool parameters still contain fact field in acceptance_facts: %s", schemaStr)
	}
}

func TestDesignCrystallizerDropsVestigialTierFields(t *testing.T) {
	raw := `{
		"epic": "feature",
		"requirements": ["works"],
		"in_scope": ["code"],
		"out_of_scope": ["docs"],
		"system_design": "keep it simple",
		"recommended_tier": "substantial",
		"recommended_model_tier": "large",
		"tasks": [{
			"id": "t1",
			"title": "task one",
			"intent": "do it",
			"estimated_tier": "architectural"
		}]
	}`
	provider := &requestCapturingProvider{events: toolCallEvent("submit_design_plan", raw)}
	input := schemas.DesignConversationInput{
		History: []schemas.ConversationMessage{{Role: "user", Content: "Do it."}},
	}
	got, err := (DesignCrystallizer{}).Crystallize(context.Background(), provider, StageOptions{}, input)
	if err != nil {
		t.Fatalf("crystallize: %v", err)
	}
	schemaJSON, err := json.Marshal(provider.request.Tools[0].Parameters)
	if err != nil {
		t.Fatalf("marshal tool parameters: %v", err)
	}
	schemaStr := string(schemaJSON)
	for _, field := range []string{"recommended_tier", "recommended_model_tier", "estimated_tier"} {
		if strings.Contains(schemaStr, "\""+field+"\"") {
			t.Fatalf("tool schema still contains %s: %s", field, schemaStr)
		}
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != "t1" {
		t.Fatalf("unexpected plan tasks: %+v", got.Tasks)
	}
}

// TestGoFormatFinding verifies the in-process Go profile reports files that
// are not gofmt-clean via GO_FORMAT (low severity) without spawning a process.
func TestGoFormatFinding(t *testing.T) {
	workDir := t.TempDir()
	// Valid Go, but missing the gofmt spacing around '='.
	content := "package main\n\nfunc main() {\nvar x=1\n}\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := (goSyntaxCheck{}).Run(context.Background(), VerificationCheckRequest{
		WorkDir:  workDir,
		Language: "go",
		Paths:    []string{filepath.Join(workDir, "main.go")},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ToolRun.Status == schemas.VerificationIncomplete {
		t.Fatalf("unexpected incomplete: %s", res.ToolRun.Summary)
	}
	var formatFinding *schemas.VerificationFinding
	for i := range res.Findings {
		f := &res.Findings[i]
		if f.RuleID == "GO_FORMAT" {
			formatFinding = f
		}
		if f.RuleID == "GO_SYNTAX" {
			t.Fatalf("valid file should not produce GO_SYNTAX: %+v", f)
		}
	}
	if formatFinding == nil {
		t.Fatalf("expected GO_FORMAT finding, got %+v", res.Findings)
	}
	if formatFinding.Severity != schemas.SeverityLow {
		t.Fatalf("expected low severity, got %v", formatFinding.Severity)
	}
	if formatFinding.Message != "File is not gofmt-clean" {
		t.Fatalf("unexpected message %q", formatFinding.Message)
	}
}

// TestPythonBatchedCompile verifies the Python profile compiles the whole file
// set in a single py_compile invocation rather than one process per file.
func TestPythonBatchedCompile(t *testing.T) {
	workDir := t.TempDir()
	files := []string{"a.py", "b.py", "c.py"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte("print('ok')\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	bad := filepath.Join(workDir, "bad.py")
	if err := os.WriteFile(bad, []byte("def broken(\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var calls int
	var lastCommand string
	runTool := schemaEnforcingBashRunTool(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "bash" {
			calls++
			lastCommand = args["command"].(string)
			return ToolResult{OK: false, Output: "SyntaxError: invalid syntax"}, nil
		}
		return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
	})

	paths := []string{filepath.Join(workDir, "a.py"), filepath.Join(workDir, "b.py"), filepath.Join(workDir, "c.py"), bad}
	res, err := (pythonSyntaxCheck{}).Run(context.Background(), VerificationCheckRequest{
		WorkDir:  workDir,
		Language: "python",
		Paths:    paths,
		RunTool:  runTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 py_compile call, got %d", calls)
	}
	if !strings.Contains(lastCommand, "'python'") || !strings.Contains(lastCommand, "'-m'") || !strings.Contains(lastCommand, "'py_compile'") {
		t.Fatalf("unexpected command %q", lastCommand)
	}
	for _, f := range files {
		if !strings.Contains(lastCommand, "'"+filepath.Join(workDir, f)+"'") {
			t.Fatalf("command missing %s: %q", f, lastCommand)
		}
	}
	if !strings.Contains(lastCommand, "'"+bad+"'") {
		t.Fatalf("command missing bad.py: %q", lastCommand)
	}
	if len(res.Findings) == 0 {
		t.Fatalf("expected at least one finding, got %+v", res.Findings)
	}
}

func TestSchemaEnforcingBashRunToolRejectsArrayCommand(t *testing.T) {
	runTool := schemaEnforcingBashRunTool(func(context.Context, string, map[string]any) (ToolResult, error) {
		t.Fatal("next must not run for a non-string command")
		return ToolResult{}, nil
	})
	res, err := runTool(context.Background(), "bash", map[string]any{
		"command": []string{"python", "-m", "py_compile", "a.py"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatal("expected rejected array command")
	}
	if res.Output != bashCommandMustBeString {
		t.Fatalf("output = %q, want %q", res.Output, bashCommandMustBeString)
	}
}

func TestPythonRuffPassUsesStringCommand(t *testing.T) {
	workDir := t.TempDir()
	py := filepath.Join(workDir, "a.py")
	if err := os.WriteFile(py, []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "ruff.toml"), []byte("[lint]\n"), 0o644); err != nil {
		t.Fatalf("write ruff config: %v", err)
	}
	var commands []string
	runTool := schemaEnforcingBashRunTool(func(_ context.Context, name string, args map[string]any) (ToolResult, error) {
		if name != "bash" {
			return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
		}
		command := args["command"].(string)
		commands = append(commands, command)
		if strings.Contains(command, "'ruff'") {
			return ToolResult{OK: true, Output: `{"results":[]}`}, nil
		}
		return ToolResult{OK: true}, nil
	})
	res, err := (pythonSyntaxCheck{}).Run(context.Background(), VerificationCheckRequest{
		WorkDir:  workDir,
		Language: "python",
		Paths:    []string{py},
		RunTool:  runTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ToolRun.Status != schemas.VerificationPassed {
		t.Fatalf("status = %q, want passed; findings=%+v", res.ToolRun.Status, res.Findings)
	}
	if len(commands) != 2 {
		t.Fatalf("expected py_compile then ruff, got %v", commands)
	}
	if !strings.Contains(commands[1], "'ruff'") || !strings.Contains(commands[1], "'check'") {
		t.Fatalf("unexpected ruff command %q", commands[1])
	}
}

func TestPythonSyntaxCheckRealBashTool(t *testing.T) {
	workDir := t.TempDir()
	bad := filepath.Join(workDir, "bad.py")
	if err := os.WriteFile(bad, []byte("def broken(\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(workDir))
	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		res := registry.RunWithOptions(ctx, name, args, tools.RunOptions{PermissionGranted: true})
		return ToolResult{OK: res.Status == tools.StatusOK, Output: res.Output}, nil
	}
	res, err := (pythonSyntaxCheck{}).Run(context.Background(), VerificationCheckRequest{
		WorkDir:  workDir,
		Language: "python",
		Paths:    []string{bad},
		RunTool:  runTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(res.ToolRun.Summary, bashCommandMustBeString) || strings.Contains(res.ToolRun.Summary, "command must be a string") {
		t.Fatalf("real bash tool rejected the command: %s", res.ToolRun.Summary)
	}
	if res.ToolRun.Status != schemas.VerificationFindings {
		t.Fatalf("status = %q summary=%q, want findings", res.ToolRun.Status, res.ToolRun.Summary)
	}
	found := false
	for _, f := range res.Findings {
		if f.RuleID == "PY_COMPILE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected PY_COMPILE finding, got %+v summary=%q output-like=%q", res.Findings, res.ToolRun.Summary, res.ToolRun.Summary)
	}
}

// TestJSSyntaxCheck exercises the Node-based JavaScript syntax adapter.
func TestJSSyntaxCheck(t *testing.T) {
	t.Run("bad js", func(t *testing.T) {
		workDir := t.TempDir()
		bad := filepath.Join(workDir, "bad.js")
		if err := os.WriteFile(bad, []byte("const x = ;\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		runTool := schemaEnforcingBashRunTool(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			if name == "bash" {
				return ToolResult{OK: false, Output: "SyntaxError: Unexpected token"}, nil
			}
			return ToolResult{}, fmt.Errorf("unexpected %s", name)
		})
		res, err := (jsSyntaxCheck{}).Run(context.Background(), VerificationCheckRequest{
			WorkDir:  workDir,
			Language: "javascript",
			Paths:    []string{bad},
			RunTool:  runTool,
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.ToolRun.Status == schemas.VerificationIncomplete {
			t.Fatalf("unexpected incomplete: %s", res.ToolRun.Summary)
		}
		found := false
		for _, f := range res.Findings {
			if f.RuleID == "JS_SYNTAX" {
				found = true
				if f.Severity != schemas.SeverityHigh {
					t.Fatalf("expected high severity, got %v", f.Severity)
				}
			}
		}
		if !found {
			t.Fatalf("expected JS_SYNTAX finding, got %+v", res.Findings)
		}
	})

	t.Run("missing node", func(t *testing.T) {
		workDir := t.TempDir()
		f := filepath.Join(workDir, "a.js")
		if err := os.WriteFile(f, []byte("const x = 1;\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		runTool := schemaEnforcingBashRunTool(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			if name == "bash" {
				return ToolResult{OK: false, Output: "bash: node: command not found"}, nil
			}
			return ToolResult{}, fmt.Errorf("unexpected %s", name)
		})
		res, err := (jsSyntaxCheck{}).Run(context.Background(), VerificationCheckRequest{
			WorkDir:  workDir,
			Language: "javascript",
			Paths:    []string{f},
			RunTool:  runTool,
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.ToolRun.Status != schemas.VerificationIncomplete {
			t.Fatalf("expected incomplete, got %v", res.ToolRun.Status)
		}
		if !strings.Contains(res.ToolRun.Summary, "Node.js is not installed") {
			t.Fatalf("unexpected summary %q", res.ToolRun.Summary)
		}
	})

	t.Run("no js files", func(t *testing.T) {
		workDir := t.TempDir()
		res, err := (jsSyntaxCheck{}).Run(context.Background(), VerificationCheckRequest{
			WorkDir:  workDir,
			Language: "go",
			Paths:    []string{filepath.Join(workDir, "a.go")},
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.ToolRun.Status != schemas.VerificationNotApplicable {
			t.Fatalf("expected not_applicable, got %v", res.ToolRun.Status)
		}
	})
}

// TestTSTypeCheck exercises the project-local TypeScript compiler adapter.
func TestTSTypeCheck(t *testing.T) {
	t.Run("finding", func(t *testing.T) {
		workDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(workDir, "tsconfig.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(workDir, "node_modules", ".bin"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workDir, "node_modules", ".bin", "tsc"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write tsc: %v", err)
		}
		tsFile := filepath.Join(workDir, "index.ts")
		if err := os.WriteFile(tsFile, []byte("const x: number = y;\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		runTool := schemaEnforcingBashRunTool(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			if name == "bash" {
				return ToolResult{OK: true, Output: "index.ts(1,20): error TS2304: Cannot find name 'y'.\n"}, nil
			}
			return ToolResult{}, fmt.Errorf("unexpected %s", name)
		})
		res, err := (tsTypeCheck{}).Run(context.Background(), VerificationCheckRequest{
			WorkDir:  workDir,
			Language: "typescript",
			Paths:    []string{tsFile},
			RunTool:  runTool,
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.ToolRun.Status == schemas.VerificationIncomplete {
			t.Fatalf("unexpected incomplete: %s", res.ToolRun.Summary)
		}
		found := false
		for _, f := range res.Findings {
			if f.RuleID == "TS2304" {
				found = true
				if f.Severity != schemas.SeverityHigh {
					t.Fatalf("expected high severity, got %v", f.Severity)
				}
				if f.Line == nil || *f.Line != 1 {
					t.Fatalf("expected line 1, got %v", f.Line)
				}
			}
		}
		if !found {
			t.Fatalf("expected TS2304 finding, got %+v", res.Findings)
		}
	})

	t.Run("missing tsc", func(t *testing.T) {
		workDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(workDir, "tsconfig.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		tsFile := filepath.Join(workDir, "index.ts")
		if err := os.WriteFile(tsFile, []byte("const x = 1;\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		res, err := (tsTypeCheck{}).Run(context.Background(), VerificationCheckRequest{
			WorkDir:  workDir,
			Language: "typescript",
			Paths:    []string{tsFile},
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.ToolRun.Status != schemas.VerificationIncomplete {
			t.Fatalf("expected incomplete, got %v", res.ToolRun.Status)
		}
		if !strings.Contains(res.ToolRun.Summary, "TypeScript compiler not found") {
			t.Fatalf("unexpected summary %q", res.ToolRun.Summary)
		}
	})

	t.Run("no tsconfig", func(t *testing.T) {
		workDir := t.TempDir()
		res, err := (tsTypeCheck{}).Run(context.Background(), VerificationCheckRequest{
			WorkDir:  workDir,
			Language: "go",
			Paths:    []string{filepath.Join(workDir, "a.go")},
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.ToolRun.Status != schemas.VerificationNotApplicable {
			t.Fatalf("expected not_applicable, got %v", res.ToolRun.Status)
		}
	})
}

// TestSecurityAuditorGoRepoGosecMissingIncomplete verifies a Go workspace
// without gosec installed reports incomplete (via the gosec check) rather
// than a false pass. Pre-R1 this short-circuited at the stage level for all
// non-Python repos; now each check reports its own honest state.
func TestSecurityAuditorGoRepoGosecMissingIncomplete(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "gosec" {
			return ToolResult{
				OK:     false,
				Output: "Gosec is not installed or not available: exec: \"gosec\": executable file not found in $PATH",
			}, nil
		}
		return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
	}

	stage, _ := NewSecurityAuditor(DefaultSecurityChecks()...)
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), &fakeProvider{}, StageOptions{WorkDir: workDir, RunTool: runTool})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	report, ok := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("missing report, got %T", result.Data["security_auditor_output"])
	}
	if report.Status != schemas.VerificationIncomplete {
		t.Fatalf("expected incomplete (gosec missing), got %v", report.Status)
	}
	if !strings.Contains(report.Summary, "Gosec is not installed") {
		t.Fatalf("unexpected summary %q", report.Summary)
	}
}

// TestCallToolUseForcesToolChoice confirms callToolUse sets ToolChoice to the
// single tool's name on the request, so typed stages force the model to call
// that exact tool instead of answering in prose.
func TestCallToolUseForcesToolChoice(t *testing.T) {
	provider := &requestCapturingProvider{}
	tool := zeroruntime.ToolDefinition{Name: "submit_design_plan", Parameters: map[string]any{"type": "object"}}

	_, err := callToolUse(context.Background(), provider, "model-test", "", "system", "user", nil, tool, 0, nil, "", true)
	if err != nil {
		t.Fatalf("callToolUse: %v", err)
	}
	if provider.request.ToolChoice != "submit_design_plan" {
		t.Fatalf("ToolChoice = %q, want submit_design_plan", provider.request.ToolChoice)
	}
	if len(provider.request.Tools) != 1 || provider.request.Tools[0].Name != "submit_design_plan" {
		t.Fatalf("tools = %#v, want one submit_design_plan", provider.request.Tools)
	}
}

func TestCallToolUsePromptCacheKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "provided", key: "session-1:code_writer", want: "session-1:code_writer"},
		{name: "empty", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &requestCapturingProvider{}
			tool := zeroruntime.ToolDefinition{Name: "submit_code", Parameters: map[string]any{"type": "object"}}
			if _, err := callToolUse(context.Background(), provider, "model-test", "", "system", "user", nil, tool, 0, nil, tt.key, true); err != nil {
				t.Fatalf("callToolUse: %v", err)
			}
			if got := provider.request.PromptCacheKey; got != tt.want {
				t.Fatalf("PromptCacheKey = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStageOptionsMaxOutputTokensReachesCompletionRequest proves a
// StageOptions cap appears on the CompletionRequest the stage sends.
func TestStageOptionsMaxOutputTokensReachesCompletionRequest(t *testing.T) {
	output := schemas.CodeWriterOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	args, _ := json.Marshal(output)
	provider := &requestCapturingProvider{events: toolCallEvent("submit_code", string(args))}
	input := newHarnessInput("write code")

	if _, err := (CodeWriter{}).Run(context.Background(), input, provider, StageOptions{
		WorkDir:         t.TempDir(),
		Language:        "go",
		MaxOutputTokens: 8192,
	}); err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if provider.request.MaxOutputTokens != 8192 {
		t.Fatalf("request.MaxOutputTokens = %d, want 8192", provider.request.MaxOutputTokens)
	}
}

// TestValidatedToolUseRetainsOutputCapOnEveryAttempt proves the stage cap is
// sent unchanged on every typed-output retry attempt.
func TestValidatedToolUseRetainsOutputCapOnEveryAttempt(t *testing.T) {
	valid := schemas.CodeWriterOutput{Files: []schemas.FileChange{}, Language: "go", Intent: "no changes", Confidence: 0.9}
	validArgs, _ := json.Marshal(valid)
	provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{
		{{Type: zeroruntime.StreamEventDone}},
		append([]zeroruntime.StreamEvent{}, toolCallEvent(codeWriterToolName, `{`)...),
		toolCallEvent(codeWriterToolName, string(validArgs)),
	}}
	_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(false), 8192, nil, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseCodeWriterOutput(collected)
		return err
	}, "")
	if err != nil {
		t.Fatalf("retrying typed output: %v", err)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(provider.requests))
	}
	for i, request := range provider.requests {
		if request.MaxOutputTokens != 8192 {
			t.Fatalf("attempt %d request.MaxOutputTokens = %d, want 8192 on every retry", i+1, request.MaxOutputTokens)
		}
	}
}
