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
	bundle := &schemas.MemoryBundle{
		RequestingAgent: "x",
		Observations: []schemas.MemoryObservation{{
			Title:      "long note",
			Content:    long,
			MemoryType: "note",
			Scope:      "project",
		}},
	}
	got := selectMemory(bundle)
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
			{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 2, OutputTokens: 1}},
			{Type: zeroruntime.StreamEventDone},
		},
		append([]zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 3, OutputTokens: 2}}}, toolCallEvent(codeWriterToolName, `{`)...),
		append([]zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 5, OutputTokens: 4}}}, toolCallEvent(codeWriterToolName, string(validArgs))...),
	}}

	collected, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(), nil, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseCodeWriterOutput(collected)
		return err
	})
	if err != nil {
		t.Fatalf("retrying typed output: %v", err)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(provider.requests))
	}
	if collected.Usage.InputTokens != 10 || collected.Usage.OutputTokens != 7 {
		t.Fatalf("accumulated usage = %#v, want input=10 output=7", collected.Usage)
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
	_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(), nil, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseCodeWriterOutput(collected)
		return err
	})
	if err != nil {
		t.Fatalf("schema-invalid retry: %v", err)
	}
	if len(provider.requests) != 2 || !strings.Contains(provider.requests[1].Messages[1].Content, "language is required") {
		t.Fatalf("schema-invalid retry requests = %#v", provider.requests)
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
	_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(), nil, func(*zeroruntime.CollectedStream) error { return nil })
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
	_, err := callValidatedToolUse(ctx, provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(), nil, func(*zeroruntime.CollectedStream) error { return errors.New("invalid") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("canceled request calls = %d, want 0", len(provider.requests))
	}
}

func TestValidatedToolUseExhaustionIsActionableAndMetered(t *testing.T) {
	missing := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 2, OutputTokens: 1}},
		{Type: zeroruntime.StreamEventDone},
	}
	provider := &retryScriptProvider{scripts: [][]zeroruntime.StreamEvent{missing, missing, missing}}
	_, err := callValidatedToolUse(context.Background(), provider, "qwen-local", "", "system", "payload", nil, submitCodeToolDefinition(), nil, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseCodeWriterOutput(collected)
		return err
	})
	var typedErr *TypedOutputError
	if !errors.As(err, &typedErr) {
		t.Fatalf("exhaustion error = %T %v, want TypedOutputError", err, err)
	}
	if !strings.Contains(err.Error(), "OpenAI-compatible for local runtimes") || !strings.Contains(err.Error(), codeWriterToolName) || !strings.Contains(err.Error(), "qwen-local") {
		t.Fatalf("exhaustion error is not actionable: %v", err)
	}
	usage := typedErr.StageUsage()
	if usage == nil || usage.InputTokens != 6 || usage.OutputTokens != 3 {
		t.Fatalf("exhausted usage = %#v, want input=6 output=3", usage)
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
	stage := TestRunner{}

	pass, err := stage.Run(context.Background(), newHarnessInput("run tests"), &fakeProvider{}, StageOptions{Command: []string{"true"}, TimeoutSeconds: 5})
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

	fail, err := stage.Run(context.Background(), newHarnessInput("run tests"), &fakeProvider{}, StageOptions{Command: []string{"false"}, TimeoutSeconds: 5})
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
	var lastCommand []string
	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "bash" {
			calls++
			if cmd, ok := args["command"].([]string); ok {
				lastCommand = cmd
			}
			return ToolResult{OK: false, Output: "SyntaxError: invalid syntax"}, nil
		}
		return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
	}

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
	if len(lastCommand) < 4 || lastCommand[0] != "python" || lastCommand[1] != "-m" || lastCommand[2] != "py_compile" {
		t.Fatalf("unexpected command %v", lastCommand)
	}
	for _, f := range files {
		if !slices.Contains(lastCommand, filepath.Join(workDir, f)) {
			t.Fatalf("command missing %s: %v", f, lastCommand)
		}
	}
	if !slices.Contains(lastCommand, bad) {
		t.Fatalf("command missing bad.py: %v", lastCommand)
	}
	if len(res.Findings) == 0 {
		t.Fatalf("expected at least one finding, got %+v", res.Findings)
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
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			if name == "bash" {
				return ToolResult{OK: false, Output: "SyntaxError: Unexpected token"}, nil
			}
			return ToolResult{}, fmt.Errorf("unexpected %s", name)
		}
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
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			if name == "bash" {
				return ToolResult{OK: false, Output: "bash: node: command not found"}, nil
			}
			return ToolResult{}, fmt.Errorf("unexpected %s", name)
		}
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
		runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			if name == "bash" {
				return ToolResult{OK: true, Output: "index.ts(1,20): error TS2304: Cannot find name 'y'.\n"}, nil
			}
			return ToolResult{}, fmt.Errorf("unexpected %s", name)
		}
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
