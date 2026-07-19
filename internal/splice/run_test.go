package splice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type runFakeProvider struct{}

func (runFakeProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 8)
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	var args string
	switch toolName {
	case "submit_code":
		out := schemas.CodeWriterOutput{
			Files: []schemas.FileChange{
				{Path: "main.go", Content: "package main\n\nfunc Hello() string { return \"hello\" }\n", ChangeType: "create"},
			},
			Language:   "go",
			Intent:     "add Hello function",
			Confidence: 0.95,
		}
		b, _ := json.Marshal(out)
		args = string(b)
	case "submit_tests":
		out := schemas.TestGeneratorOutput{
			Files: []schemas.FileChange{
				{Path: "main_test.go", Content: "package main\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"wrong greeting\")\n\t}\n}\n", ChangeType: "create"},
			},
			Language:   "go",
			Intent:     "add Hello test",
			Confidence: 0.9,
		}
		b, _ := json.Marshal(out)
		args = string(b)
	default:
		args = "{}"
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 5}}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

type runFailingProvider struct{}

func (runFailingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 8)
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	var args string
	switch toolName {
	case "submit_code":
		out := schemas.CodeWriterOutput{
			Files: []schemas.FileChange{
				{Path: "main.go", Content: "package main\n\nfunc Hello() string { return \"wrong\" }\n", ChangeType: "create"},
			},
			Language:   "go",
			Intent:     "add broken Hello function",
			Confidence: 0.95,
		}
		b, _ := json.Marshal(out)
		args = string(b)
	case "submit_tests":
		out := schemas.TestGeneratorOutput{
			Files: []schemas.FileChange{
				{Path: "main_test.go", Content: "package main\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"wrong greeting\")\n\t}\n}\n", ChangeType: "create"},
			},
			Language:   "go",
			Intent:     "add Hello test",
			Confidence: 0.9,
		}
		b, _ := json.Marshal(out)
		args = string(b)
	default:
		args = "{}"
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 7, OutputTokens: 3}}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

type stubStore struct {
	bundle   schemas.MemoryBundle
	err      error
	gotQuery *schemas.MemoryQuery
	upserts  *[]schemas.MemoryObservation
}

func (s *stubStore) Search(ctx context.Context, q schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	s.gotQuery = &q
	return s.bundle, s.err
}

func (s *stubStore) Upsert(ctx context.Context, obs schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	if s.upserts != nil {
		*s.upserts = append(*s.upserts, obs)
	}
	return obs, nil
}

type capturingStage struct {
	inputs *[]schemas.HarnessStageInput
}

func (s *capturingStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	*s.inputs = append(*s.inputs, input)
	return schemas.HarnessStageOutput{
		Summary:    "captured",
		Detail:     "captured",
		Confidence: 1,
	}, nil
}

type outputStage struct {
	output schemas.HarnessStageOutput
}

func (s outputStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	return s.output, nil
}

type capturedStageCall struct {
	provider zeroruntime.Provider
	options  stages.StageOptions
}

type stageCallCapturer struct {
	calls map[string]capturedStageCall
}

func (s *stageCallCapturer) Run(_ context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls[input.StageName] = capturedStageCall{provider: provider, options: options}
	return schemas.HarnessStageOutput{Summary: "captured", Confidence: 1}, nil
}

type contextRequestStage struct {
	inputs *[]schemas.HarnessStageInput
}

func (s *contextRequestStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	*s.inputs = append(*s.inputs, input)
	if input.Context == nil {
		symbol := "foo"
		return schemas.HarnessStageOutput{
			Summary:    "needs context",
			Confidence: 0.5,
			ContextRequest: &schemas.ContextRequest{
				Reason: "inspect symbol",
				Queries: []schemas.ContextQuery{{
					QueryType:  schemas.ContextGetSymbol,
					Symbol:     &symbol,
					MaxResults: 5,
					MaxChars:   1000,
				}},
			},
		}, nil
	}
	return schemas.HarnessStageOutput{
		Summary:    "context handled",
		Detail:     "context handled",
		Confidence: 1,
	}, nil
}

func TestRunPassInjectsMemoryBundleAndSkipsRetrievalErrors(t *testing.T) {
	workDir := t.TempDir()
	intent := strings.Repeat("界", 201) + " done"
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: intent,
		Stages:        []schemas.ExecutionStage{{Name: "memory_stage"}},
	}
	bundle := schemas.MemoryBundle{
		RequestingAgent: "memory_stage",
		Observations: []schemas.MemoryObservation{{
			Scope:      "project",
			OwnerAgent: "memory_stage",
			Visibility: "shareable",
			MemoryType: "decision",
			Title:      "Use cached context",
			Content:    "Prefer the previously selected implementation path.",
		}},
	}
	retriever := &stubStore{bundle: bundle}
	var inputs []schemas.HarnessStageInput

	records, outputs, completed, err := runPass(context.Background(), "run-memory", 1, plan, stageRegistry{
		"memory_stage": &capturingStage{inputs: &inputs},
	}, runFakeProvider{}, agent.Options{}, workDir, nil, nil, retriever)
	if err != nil {
		t.Fatalf("runPass with memory: %v", err)
	}
	if !completed || len(records) != 1 || records[0].Status != schemas.StageCompleted || len(outputs) != 1 {
		t.Fatalf("unexpected pass result: completed=%v records=%#v outputs=%#v", completed, records, outputs)
	}
	if len(inputs) != 1 || inputs[0].MemoryBundle == nil {
		t.Fatalf("expected captured input with memory bundle, got %#v", inputs)
	}
	if inputs[0].MemoryBundle.RequestingAgent != bundle.RequestingAgent || len(inputs[0].MemoryBundle.Observations) != 1 {
		t.Fatalf("memory bundle not injected: %#v", inputs[0].MemoryBundle)
	}
	if retriever.gotQuery == nil {
		t.Fatal("expected memory query")
	}
	if retriever.gotQuery.RequestingAgent != "memory_stage" {
		t.Fatalf("requesting agent = %q, want stage name", retriever.gotQuery.RequestingAgent)
	}
	if retriever.gotQuery.ProjectPath == nil || *retriever.gotQuery.ProjectPath != workDir {
		t.Fatalf("project path = %#v, want %q", retriever.gotQuery.ProjectPath, workDir)
	}
	if retriever.gotQuery.Limit != 5 {
		t.Fatalf("limit = %d, want 5", retriever.gotQuery.Limit)
	}
	if retriever.gotQuery.Query != strings.Repeat("界", 200) {
		t.Fatalf("query was not truncated by runes: got %d runes", len([]rune(retriever.gotQuery.Query)))
	}
	wantScopes := []string{"project", "global"}
	if len(retriever.gotQuery.Scopes) != len(wantScopes) {
		t.Fatalf("scopes = %#v, want %#v", retriever.gotQuery.Scopes, wantScopes)
	}
	for i, s := range wantScopes {
		if retriever.gotQuery.Scopes[i] != s {
			t.Fatalf("scopes[%d] = %q, want %q", i, retriever.gotQuery.Scopes[i], s)
		}
	}

	errorRetriever := &stubStore{err: errors.New("sidecar down")}
	var errorInputs []schemas.HarnessStageInput
	var progress []string
	_, _, completed, err = runPass(context.Background(), "run-memory-error", 1, plan, stageRegistry{
		"memory_stage": &capturingStage{inputs: &errorInputs},
	}, runFakeProvider{}, agent.Options{OnReasoning: func(text string) { progress = append(progress, text) }}, workDir, nil, nil, errorRetriever)
	if err != nil || !completed {
		t.Fatalf("memory retrieval error should not fail run: completed=%v err=%v", completed, err)
	}
	if len(errorInputs) != 1 || errorInputs[0].MemoryBundle != nil {
		t.Fatalf("expected no memory bundle after retrieval error, got %#v", errorInputs)
	}
	if !strings.Contains(strings.Join(progress, ""), "[memory_stage] memory retrieval skipped: sidecar down") {
		t.Fatalf("expected memory skip progress, got %q", strings.Join(progress, ""))
	}

	var nilInputs []schemas.HarnessStageInput
	_, _, completed, err = runPass(context.Background(), "run-memory-nil", 1, plan, stageRegistry{
		"memory_stage": &capturingStage{inputs: &nilInputs},
	}, runFakeProvider{}, agent.Options{}, workDir, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("nil retriever should complete: completed=%v err=%v", completed, err)
	}
	if len(nilInputs) != 1 || nilInputs[0].MemoryBundle != nil {
		t.Fatalf("expected nil retriever to leave memory unset, got %#v", nilInputs)
	}
}

// captureRequestProvider records the last CompletionRequest and always returns the
// provided submit_code tool call with no file changes.
type captureRequestProvider struct {
	request zeroruntime.CompletionRequest
}

func (p *captureRequestProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.request = request
	output := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{},
		Language:   "go",
		Intent:     "no changes",
		Confidence: 0.9,
	}
	args, _ := json.Marshal(output)
	events := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: "submit_code"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: string(args)},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"},
		{Type: zeroruntime.StreamEventDone},
	}
	ch := make(chan zeroruntime.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func TestRunPassInjectsSelectedMemoryIntoConsumingStage(t *testing.T) {
	workDir := t.TempDir()
	intent := "add a helper"
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: intent,
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	bundle := schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations: []schemas.MemoryObservation{{
			Scope:      "project",
			OwnerAgent: "orchestrator",
			Visibility: "shareable",
			MemoryType: "decision",
			Title:      "Use gofmt",
			Content:    "Run gofmt on all generated files.",
		}},
	}
	store := &stubStore{bundle: bundle}
	provider := &captureRequestProvider{}
	fakeRunner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: ""}, nil
	})

	records, _, completed, err := runPass(context.Background(), "run-memory-consume", 1, plan, stageRegistry{
		"code_writer": stages.CodeWriter{},
	}, provider, agent.Options{}, workDir, fakeRunner, nil, store)
	if err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if !completed || len(records) != 1 || records[0].Status != schemas.StageCompleted {
		t.Fatalf("unexpected pass result: completed=%v records=%#v", completed, records)
	}
	if len(provider.request.Messages) < 2 {
		t.Fatalf("expected user message in captured payload, got %d messages", len(provider.request.Messages))
	}
	payload := provider.request.Messages[1].Content
	var cwInput schemas.CodeWriterInput
	if err := json.Unmarshal([]byte(payload), &cwInput); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(cwInput.Memory) != 1 {
		t.Fatalf("expected 1 selected memory in stage input, got %#v", cwInput.Memory)
	}
	if cwInput.Memory[0].Title != "Use gofmt" || cwInput.Memory[0].Scope != "project" {
		t.Fatalf("unexpected selected memory: %#v", cwInput.Memory[0])
	}
}

func TestRunPassPersistsDiscoveredTestCommand(t *testing.T) {
	workDir := t.TempDir()
	runID := "run-test-command"

	t.Run("test runner command", func(t *testing.T) {
		plan := schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: "run tests",
			Stages:        []schemas.ExecutionStage{{Name: "test_runner"}},
		}
		var upserts []schemas.MemoryObservation
		store := &stubStore{upserts: &upserts}

		_, _, completed, err := runPass(context.Background(), runID, 1, plan, stageRegistry{
			"test_runner": outputStage{output: schemas.HarnessStageOutput{
				Summary:    "passed",
				Confidence: 1,
				Data:       map[string]any{"test_command": []string{"go", "test", "./..."}},
			}},
		}, runFakeProvider{}, agent.Options{}, workDir, nil, nil, store)
		if err != nil || !completed {
			t.Fatalf("runPass: completed=%v err=%v", completed, err)
		}
		if len(upserts) != 1 {
			t.Fatalf("upserts = %d, want 1: %#v", len(upserts), upserts)
		}
		obs := upserts[0]
		if obs.OwnerAgent != "orchestrator" {
			t.Fatalf("OwnerAgent = %q, want orchestrator", obs.OwnerAgent)
		}
		if obs.Visibility != "shareable" {
			t.Fatalf("Visibility = %q, want shareable", obs.Visibility)
		}
		if obs.MemoryType != "test_command" {
			t.Fatalf("MemoryType = %q, want test_command", obs.MemoryType)
		}
		if obs.Scope != "project" {
			t.Fatalf("Scope = %q, want project", obs.Scope)
		}
		if obs.TopicKey == nil || *obs.TopicKey != "test_command" {
			t.Fatalf("TopicKey = %#v, want test_command", obs.TopicKey)
		}
		if obs.Content != "go test ./..." {
			t.Fatalf("Content = %q, want go test ./...", obs.Content)
		}
		if obs.SourceStage == nil || *obs.SourceStage != "test_runner" {
			t.Fatalf("SourceStage = %#v, want test_runner", obs.SourceStage)
		}
		if obs.SourceRunID == nil || *obs.SourceRunID != runID {
			t.Fatalf("SourceRunID = %#v, want %q", obs.SourceRunID, runID)
		}
		if obs.ProjectPath == nil || *obs.ProjectPath != workDir {
			t.Fatalf("ProjectPath = %#v, want %q", obs.ProjectPath, workDir)
		}
	})

	t.Run("non test runner stage", func(t *testing.T) {
		plan := schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: "run tests",
			Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		}
		var upserts []schemas.MemoryObservation
		store := &stubStore{upserts: &upserts}

		_, _, completed, err := runPass(context.Background(), runID, 1, plan, stageRegistry{
			"code_writer": outputStage{output: schemas.HarnessStageOutput{
				Summary:    "not a test runner",
				Confidence: 1,
				Data:       map[string]any{"test_command": []string{"go", "test", "./..."}},
			}},
		}, runFakeProvider{}, agent.Options{}, workDir, nil, nil, store)
		if err != nil || !completed {
			t.Fatalf("runPass: completed=%v err=%v", completed, err)
		}
		if len(upserts) != 0 {
			t.Fatalf("upserts = %d, want 0: %#v", len(upserts), upserts)
		}
	})
}

func TestRunExecutionPlanPersistsConfigObservation(t *testing.T) {
	workDir := t.TempDir()
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		t.Fatalf("abs work dir: %v", err)
	}
	runID := "run-config"
	intent := "raw user intent should not be persisted"
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: intent,
		Stages: []schemas.ExecutionStage{{
			Name:   "config_stage",
			Budget: schemas.StageBudget{Skippable: true},
		}},
	}
	var upserts []schemas.MemoryObservation
	store := &stubStore{upserts: &upserts}

	_, err = runExecutionPlan(context.Background(), runID, plan, runFakeProvider{}, agent.Options{Cwd: workDir, MaxTurns: 1}, store, nil)
	if err != nil {
		t.Fatalf("runExecutionPlan: %v", err)
	}
	if len(upserts) != 1 {
		t.Fatalf("upserts = %d, want 1: %#v", len(upserts), upserts)
	}
	obs := upserts[0]
	if obs.MemoryType != "run_config" {
		t.Fatalf("MemoryType = %q, want run_config", obs.MemoryType)
	}
	if obs.OwnerAgent != "orchestrator" {
		t.Fatalf("OwnerAgent = %q, want orchestrator", obs.OwnerAgent)
	}
	if obs.Visibility != "shareable" {
		t.Fatalf("Visibility = %q, want shareable", obs.Visibility)
	}
	if obs.TopicKey == nil || *obs.TopicKey != "run_config" {
		t.Fatalf("TopicKey = %#v, want run_config", obs.TopicKey)
	}
	if !strings.Contains(obs.Content, "tier=light") || !strings.Contains(obs.Content, "stages=config_stage") {
		t.Fatalf("Content = %q, want tier and stages", obs.Content)
	}
	if strings.Contains(obs.Content, intent) {
		t.Fatalf("Content contains raw intent: %q", obs.Content)
	}
	if obs.SourceRunID == nil || *obs.SourceRunID != runID {
		t.Fatalf("SourceRunID = %#v, want %q", obs.SourceRunID, runID)
	}
	if obs.SourceStage != nil {
		t.Fatalf("SourceStage = %#v, want nil", obs.SourceStage)
	}
	if obs.ProjectPath == nil || *obs.ProjectPath != absWorkDir {
		t.Fatalf("ProjectPath = %#v, want %q", obs.ProjectPath, absWorkDir)
	}
}

func TestRunStageWithContextPersistsToolDegradationObservation(t *testing.T) {
	workDir := t.TempDir()
	stageName := "context_stage"
	runID := "run-degraded-context"
	var inputs []schemas.HarnessStageInput
	var upserts []schemas.MemoryObservation
	store := &stubStore{upserts: &upserts}
	stage := &contextRequestStage{inputs: &inputs}

	output, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID:     runID,
		StageName: stageName,
	}, stage, runFakeProvider{}, "", "", agent.Options{}, workDir, nil, store)
	if err != nil {
		t.Fatalf("runStageWithContext: %v", err)
	}
	if output.Summary != "context handled" {
		t.Fatalf("output summary = %q, want context handled", output.Summary)
	}
	if len(inputs) != 2 || inputs[1].Context == nil {
		t.Fatalf("expected two stage calls with context on second call, got %#v", inputs)
	}
	if len(upserts) != 1 {
		t.Fatalf("upserts = %d, want 1: %#v", len(upserts), upserts)
	}
	obs := upserts[0]
	if obs.MemoryType != "tool_degradation" {
		t.Fatalf("MemoryType = %q, want tool_degradation", obs.MemoryType)
	}
	if obs.OwnerAgent != stageName {
		t.Fatalf("OwnerAgent = %q, want %q", obs.OwnerAgent, stageName)
	}
	if obs.Visibility != "private" {
		t.Fatalf("Visibility = %q, want private", obs.Visibility)
	}
	if obs.TopicKey == nil || *obs.TopicKey != "tool_degradation:get_symbol" {
		t.Fatalf("TopicKey = %#v, want tool_degradation:get_symbol", obs.TopicKey)
	}
	wantContent := "get_symbol requires AST inspection, deferred for v1; use find_symbol + read_file"
	if obs.Content != wantContent {
		t.Fatalf("Content = %q, want %q", obs.Content, wantContent)
	}
	if obs.SourceRunID == nil || *obs.SourceRunID != runID {
		t.Fatalf("SourceRunID = %#v, want %q", obs.SourceRunID, runID)
	}
	if obs.SourceStage == nil || *obs.SourceStage != stageName {
		t.Fatalf("SourceStage = %#v, want %q", obs.SourceStage, stageName)
	}
	if obs.ProjectPath == nil || *obs.ProjectPath != workDir {
		t.Fatalf("ProjectPath = %#v, want %q", obs.ProjectPath, workDir)
	}
}

func TestRunPipelineEndToEnd(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewListDirectoryTool(workDir))
	registry.Register(tools.NewGrepTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))

	var texts []string
	var reasoning []string
	result, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		OnText:         func(s string) { texts = append(texts, s) },
		OnReasoning:    func(s string) { reasoning = append(reasoning, s) },
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("expected completed run, got incomplete: %s", result.IncompleteReason)
	}
	if _, err := os.Stat(filepath.Join(workDir, "main.go")); err != nil {
		t.Fatalf("main.go not created: %v", err)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Pipeline completed") {
		t.Fatalf("expected completion summary text, got:\n%s", joined)
	}
	joinedReasoning := strings.Join(reasoning, "")
	if !strings.Contains(joinedReasoning, "Starting pipeline iteration 1") {
		t.Fatalf("expected stage progress reasoning, got:\n%s", joinedReasoning)
	}
}

func TestRunHonorsPermissionModeAsk(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewListDirectoryTool(workDir))
	registry.Register(tools.NewGrepTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))

	permissionRequested := false
	var permissionEvents []agent.PermissionEvent
	_, _ = Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAsk,
		OnPermissionRequest: func(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
			permissionRequested = true
			return agent.PermissionDecision{Action: agent.PermissionDecisionDeny}, nil
		},
		OnPermission: func(event agent.PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	}, nil, nil)
	if !permissionRequested {
		t.Fatal("expected a permission request when writing files in PermissionModeAsk")
	}
	if len(permissionEvents) < 2 {
		t.Fatalf("expected permission prompt and decision events, got %d", len(permissionEvents))
	}
	if permissionEvents[0].Action != agent.PermissionActionPrompt {
		t.Fatalf("first permission event action = %s, want prompt", permissionEvents[0].Action)
	}
	if permissionEvents[1].Action != agent.PermissionActionDeny {
		t.Fatalf("second permission event action = %s, want deny", permissionEvents[1].Action)
	}
}

func TestRunRecordsStageFailedOnRequiredFileApplicationFailure(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewListDirectoryTool(workDir))
	registry.Register(tools.NewGrepTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))

	agentResult, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAsk,
		MaxTurns:       1,
		OnPermissionRequest: func(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
			return agent.PermissionDecision{Action: agent.PermissionDecisionDeny}, nil
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(workDir, "main.go")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file created after denied application")
	}

	// Inspect the FinalAnswer JSON directly because Run is not required to return
	// an error when the pipeline finishes in a failed state.
	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(agentResult.FinalAnswer), &result); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if result.Status == "completed" {
		t.Fatalf("expected non-completed pipeline, got %q", result.Status)
	}
	foundFailed := false
	for _, record := range result.Stages {
		if record.Name == "code_writer" && record.Status == schemas.StageFailed {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		t.Fatalf("expected code_writer StageFailed in records, got %+v", result.Stages)
	}
}

func TestRunRecordsTestRunnerFailedOnDeniedBash(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewListDirectoryTool(workDir))
	registry.Register(tools.NewGrepTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))

	var permissionEvents []agent.PermissionEvent
	agentResult, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAsk,
		MaxTurns:       1,
		OnPermissionRequest: func(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
			if req.ToolName == "bash" {
				return agent.PermissionDecision{Action: agent.PermissionDecisionDeny}, nil
			}
			return agent.PermissionDecision{Action: agent.PermissionDecisionAllow}, nil
		},
		OnPermission: func(event agent.PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(workDir, "main.go")); os.IsNotExist(statErr) {
		t.Fatalf("expected main.go to be created after allowed write_file")
	}

	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(agentResult.FinalAnswer), &result); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if result.Status == "completed" {
		t.Fatalf("expected non-completed pipeline, got %q", result.Status)
	}
	foundDeniedBash := false
	for _, event := range permissionEvents {
		if event.ToolName == "bash" && event.Action == agent.PermissionActionDeny {
			foundDeniedBash = true
		}
	}
	if !foundDeniedBash {
		t.Fatalf("expected a bash permission denial event, got %+v", permissionEvents)
	}
	foundFailedRunner := false
	for _, record := range result.Stages {
		if record.Name == "test_runner" && record.Status == schemas.StageFailed {
			foundFailedRunner = true
			break
		}
	}
	if !foundFailedRunner {
		t.Fatalf("expected test_runner StageFailed in records, got %+v", result.Stages)
	}
}

func TestRunForwardsUsagePerStageCall(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)

	var usages []agent.Usage
	_, err := Run(context.Background(), "add a security service with tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		OnUsage:        func(usage agent.Usage) { usages = append(usages, usage) },
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(usages) < 2 {
		t.Fatalf("expected usage from code_writer and test_generator, got %d", len(usages))
	}
	for i, usage := range usages {
		if usage.EffectiveInputTokens() == 0 || usage.EffectiveOutputTokens() == 0 {
			t.Fatalf("usage %d not forwarded: %+v", i, usage)
		}
	}
}

// badOutputProvider emits a submit_code tool call whose output has an empty
// Summary, so HarnessStageOutput.Validate fails and the stage must be marked
// StageFailed instead of StageCompleted.
type badOutputProvider struct{}

func (badOutputProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 4)
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	// Return a valid CodeWriterOutput but set its intent (which becomes the
	// stage Summary) to empty.
	out := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "x.go", Content: "package x\n", ChangeType: "create"}},
		Language:   "go",
		Intent:     "",
		Confidence: 0.9,
	}
	b, _ := json.Marshal(out)
	args := string(b)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestRunRejectsInvalidStageOutput(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)

	agentResult, err := Run(context.Background(), "add a Hello function", badOutputProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(agentResult.FinalAnswer), &result); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if result.Status == "completed" {
		t.Fatalf("expected non-completed pipeline for invalid output")
	}
	foundFailed := false
	for _, r := range result.Stages {
		if r.Status == schemas.StageFailed {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		t.Fatalf("expected a StageFailed record, got %+v", result.Stages)
	}
}

func TestRunPassModelFreeStageCapabilities(t *testing.T) {
	workDir := t.TempDir()
	capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}}
	registry := stageRegistry{}
	modelFreeNames := []string{"static_analyzer", "security_auditor", "test_runner"}
	plan := schemas.ExecutionPlan{Tier: schemas.TierSubstantial, RequestIntent: "verify deterministically"}
	for _, name := range modelFreeNames {
		plan.Stages = append(plan.Stages, schemas.ExecutionStage{Name: name})
		registry[name] = capturer
	}
	resolverCalls := 0
	records, _, completed, err := runPass(context.Background(), "run-model-free", 1, plan, registry, &namedProvider{name: "default"}, agent.Options{
		Model:           "default-model",
		ReasoningEffort: "high",
		ProviderName:    "default-provider",
		StageModelResolver: func(stageName string) (agent.Provider, string, string, error) {
			resolverCalls++
			return &namedProvider{name: "unexpected"}, "unexpected-model", "low", nil
		},
	}, workDir, nil, nil, nil)
	if err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if !completed {
		t.Fatalf("model-free pass did not complete: %+v", records)
	}
	if resolverCalls != 0 {
		t.Fatalf("StageModelResolver calls = %d, want 0", resolverCalls)
	}
	for _, name := range modelFreeNames {
		call, ok := capturer.calls[name]
		if !ok {
			t.Fatalf("stage %q was not called", name)
		}
		if call.provider != nil {
			t.Fatalf("stage %q provider = %T, want nil", name, call.provider)
		}
		if call.options.ModelOverride != "" || call.options.ReasoningEffort != "" {
			t.Fatalf("stage %q model options = %q/%q, want empty", name, call.options.ModelOverride, call.options.ReasoningEffort)
		}
	}
	for _, record := range records {
		if record.Model != nil || record.Provider != nil {
			t.Fatalf("model-free record has attribution: %+v", record)
		}
	}
}

func TestRunPassModelBackedAndCustomStageRouting(t *testing.T) {
	workDir := t.TempDir()
	capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}}
	registry := stageRegistry{}
	stageNames := []string{"code_writer", "test_generator", "custom_stage"}
	plan := schemas.ExecutionPlan{Tier: schemas.TierStandard, RequestIntent: "route model-backed stages"}
	for _, name := range stageNames {
		plan.Stages = append(plan.Stages, schemas.ExecutionStage{Name: name})
		registry[name] = capturer
	}
	routedProvider := &namedProvider{name: "routed"}
	var resolved []string
	records, _, completed, err := runPass(context.Background(), "run-model-backed", 1, plan, registry, &namedProvider{name: "default"}, agent.Options{
		Model:           "default-model",
		ReasoningEffort: "medium",
		ProviderName:    "default-provider",
		StageModelResolver: func(stageName string) (agent.Provider, string, string, error) {
			resolved = append(resolved, stageName)
			return routedProvider, "routed-model", "high", nil
		},
	}, workDir, nil, nil, nil)
	if err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if !completed {
		t.Fatalf("model-backed pass did not complete: %+v", records)
	}
	if strings.Join(resolved, ",") != strings.Join(stageNames, ",") {
		t.Fatalf("resolved stages = %v, want %v", resolved, stageNames)
	}
	for _, name := range stageNames {
		call := capturer.calls[name]
		if call.provider != routedProvider || call.options.ModelOverride != "routed-model" || call.options.ReasoningEffort != "high" {
			t.Fatalf("stage %q routing = provider %T options %q/%q", name, call.provider, call.options.ModelOverride, call.options.ReasoningEffort)
		}
	}
	for _, record := range records {
		if record.Model == nil || *record.Model != "routed-model" || record.Provider == nil || *record.Provider != "default-provider" {
			t.Fatalf("model-backed record attribution = %+v", record)
		}
	}
}

func TestRunResolvesPerStageModel(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)

	var resolvedStages []string
	var fakeProvider zeroruntime.Provider = &runFakeProvider{}
	opts := agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
		StageModelResolver: func(stageName string) (agent.Provider, string, string, error) {
			resolvedStages = append(resolvedStages, stageName)
			return fakeProvider, "test-model", "high", nil
		},
	}

	agentResult, err := Run(context.Background(), "add a Hello function and tests", fakeProvider, opts, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(agentResult.FinalAnswer), &result); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if len(resolvedStages) == 0 {
		t.Fatal("expected StageModelResolver to be called for at least one stage")
	}
	var foundModel bool
	for _, r := range result.Stages {
		if r.Model != nil && *r.Model == "test-model" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Fatalf("expected a stage with model test-model, got %+v", result.Stages)
	}
}

func TestRunRecordsStageUsageInStageRecordAndTotals(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)

	agentResult, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(agentResult.FinalAnswer), &result); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}

	var foundNonZeroTokens bool
	for _, r := range result.Stages {
		if r.TokensInput > 0 || r.TokensOutput > 0 {
			foundNonZeroTokens = true
			break
		}
	}
	if !foundNonZeroTokens {
		t.Fatalf("expected at least one stage with non-zero tokens, got %+v", result.Stages)
	}
	if result.TotalTokensInput == 0 || result.TotalTokensOutput == 0 {
		t.Fatalf("expected non-zero totals, got input=%d output=%d", result.TotalTokensInput, result.TotalTokensOutput)
	}
}

func TestRunEmitsPairedToolCallbacks(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)

	calls := map[string]agent.ToolCall{}
	var results []agent.ToolResult
	_, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		OnToolCall: func(call agent.ToolCall) {
			if call.ID == "" {
				t.Fatalf("tool call %s has empty ID", call.Name)
			}
			calls[call.ID] = call
		},
		OnToolResult: func(result agent.ToolResult) {
			results = append(results, result)
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("expected tool calls")
	}
	if len(results) != len(calls) {
		t.Fatalf("tool result count = %d, tool call count = %d", len(results), len(calls))
	}
	seenWrite := false
	for _, result := range results {
		call, ok := calls[result.ToolCallID]
		if !ok {
			t.Fatalf("tool result %q has no paired call", result.ToolCallID)
		}
		if call.Name != result.Name {
			t.Fatalf("tool result name = %s, call name = %s", result.Name, call.Name)
		}
		if result.Name == "write_file" {
			seenWrite = true
		}
	}
	if !seenWrite {
		t.Fatal("expected write_file tool callback")
	}
}

func TestRunStreamsStageToolArguments(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)

	var starts []string
	var deltas []string
	_, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		OnToolCallStart: func(id, name string) {
			starts = append(starts, name)
		},
		OnToolCallDelta: func(id, fragment string) {
			deltas = append(deltas, fragment)
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsString(starts, "submit_code") {
		t.Fatalf("expected submit_code stream start, got %v", starts)
	}
	if !strings.Contains(strings.Join(deltas, ""), `"files"`) {
		t.Fatalf("expected streamed structured arguments, got %q", strings.Join(deltas, ""))
	}
}

func TestRunHonorsMaxTurnsAsIterationCap(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)

	result, err := Run(context.Background(), "add a security service with tests", runFailingProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Incomplete {
		t.Fatal("expected incomplete result when MaxTurns caps a failing run")
	}
	var pipeline schemas.PipelineResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if pipeline.Status != "aborted" {
		t.Fatalf("pipeline status = %s, want aborted", pipeline.Status)
	}
	for _, record := range pipeline.Stages {
		if record.Iteration > 1 {
			t.Fatalf("found iteration %d despite MaxTurns=1", record.Iteration)
		}
	}
	if pipeline.AbortReason == nil || !strings.Contains(*pipeline.AbortReason, "Maximum iteration count reached") {
		t.Fatalf("unexpected abort reason: %#v", pipeline.AbortReason)
	}
}

func newRunTestWorkspace(t *testing.T) (string, *tools.Registry) {
	t.Helper()
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewListDirectoryTool(workDir))
	registry.Register(tools.NewGrepTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewDeleteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))
	return workDir, registry
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSummarizeWorkspaceChangesFallbackBounded(t *testing.T) {
	workDir := t.TempDir()
	for i := 0; i < 250; i++ {
		name := fmt.Sprintf("file_%03d.go", i)
		if err := os.WriteFile(filepath.Join(workDir, name), []byte("package main\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(workDir, "node_modules"), 0755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "node_modules", "skip.go"), []byte("skip"), 0644); err != nil {
		t.Fatalf("write skip.go: %v", err)
	}

	summary := summarizeWorkspaceChanges(context.Background(), workDir)
	if summary.IsRepo {
		t.Fatal("expected IsRepo=false for plain directory")
	}
	if len(summary.ChangedFiles) != defaultMaxSummaryFiles {
		t.Fatalf("file count = %d, want %d", len(summary.ChangedFiles), defaultMaxSummaryFiles)
	}
	if !summary.Truncated {
		t.Fatal("expected truncated file cap")
	}
	for _, f := range summary.ChangedFiles {
		if f.Path == "node_modules/skip.go" {
			t.Fatal("node_modules should be skipped")
		}
	}
}

func TestSummarizeWorkspaceChangesFallbackPerFileCap(t *testing.T) {
	workDir := t.TempDir()
	big := make([]byte, defaultMaxFileBytes+100)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(workDir, "big.go"), big, 0644); err != nil {
		t.Fatalf("write big.go: %v", err)
	}

	summary := summarizeWorkspaceChanges(context.Background(), workDir)
	if summary.IsRepo {
		t.Fatal("expected IsRepo=false")
	}
	if !summary.Truncated {
		t.Fatal("expected truncated per-file cap")
	}
	if len(summary.DiffText) > defaultMaxDiffBytes+len(big)+100 {
		t.Fatalf("diff length %d exceeds cap", len(summary.DiffText))
	}
}

func TestSummarizeWorkspaceChangesGitAware(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workDir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = workDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "test@splice.local")
	run("git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	run("git", "add", "main.go")
	run("git", "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("modify main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "new.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write new.go: %v", err)
	}

	summary := summarizeWorkspaceChanges(context.Background(), workDir)
	if !summary.IsRepo {
		t.Fatal("expected IsRepo=true")
	}
	paths := make(map[string]string)
	for _, f := range summary.ChangedFiles {
		paths[f.Path] = f.Status
	}
	if paths["main.go"] != "modified" {
		t.Fatalf("main.go status = %q, want modified", paths["main.go"])
	}
	if paths["new.go"] != "created" {
		t.Fatalf("new.go status = %q, want created", paths["new.go"])
	}
	if !strings.Contains(summary.DiffText, "func main()") && !strings.Contains(summary.DiffText, "# untracked file: new.go") {
		t.Fatalf("expected diff text to contain tracked or untracked content, got:\n%s", summary.DiffText)
	}
	if len(summary.DiffText) > defaultMaxDiffBytes {
		t.Fatalf("diff length %d exceeds cap", len(summary.DiffText))
	}
}

func TestEmitStageEventProducesMarker(t *testing.T) {
	var got []string
	options := agent.Options{OnReasoning: func(s string) { got = append(got, s) }}

	emitStageEvent(options, "code_writer", "running", "writing files", 50, []string{"main.go"})

	if len(got) != 1 {
		t.Fatalf("expected 1 reasoning call, got %d", len(got))
	}
	line := got[0]
	if !strings.HasPrefix(line, stageEventMarkerBegin) || !strings.HasSuffix(line, stageEventMarkerEnd) {
		t.Fatalf("line does not have stage markers: %q", line)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(line, stageEventMarkerBegin), stageEventMarkerEnd)
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("parse marker payload: %v", err)
	}
	if event["name"] != "code_writer" {
		t.Fatalf("name = %v, want code_writer", event["name"])
	}
	if event["status"] != "running" {
		t.Fatalf("status = %v, want running", event["status"])
	}
	if event["progress"] != float64(50) {
		t.Fatalf("progress = %v, want 50", event["progress"])
	}
}

func TestEmitStageEventNilOnReasoning(t *testing.T) {
	options := agent.Options{}
	emitStageEvent(options, "code_writer", "running", "", 0, nil)
	// Should not panic.
}

type meteredStageFailure struct {
	usage *schemas.StageUsage
}

func (failure meteredStageFailure) Error() string                   { return "typed output exhausted" }
func (failure meteredStageFailure) StageUsage() *schemas.StageUsage { return failure.usage }

type meteredFailingStage struct{}

func (meteredFailingStage) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	return schemas.HarnessStageOutput{}, meteredStageFailure{usage: &schemas.StageUsage{InputTokens: 12, OutputTokens: 7, CachedInputTokens: 3}}
}

func TestRunPassRecordsUsageFromFailedTypedOutput(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "test local typed output failure",
		Stages:        []schemas.ExecutionStage{{Name: "metered_failure"}},
	}
	records, _, completed, err := runPass(context.Background(), "run-metered-failure", 1, plan, stageRegistry{
		"metered_failure": meteredFailingStage{},
	}, runFakeProvider{}, agent.Options{Model: "qwen-local", ProviderName: "ollama"}, t.TempDir(), nil, nil, nil)
	if err != nil || completed {
		t.Fatalf("runPass err=%v completed=%v, want recorded stage failure", err, completed)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	record := records[0]
	if record.Status != schemas.StageFailed || record.TokensInput != 12 || record.TokensOutput != 7 || record.TokensCached != 3 {
		t.Fatalf("metered failed record = %#v", record)
	}
	if record.Model == nil || *record.Model != "qwen-local" || record.Provider == nil || *record.Provider != "ollama" {
		t.Fatalf("failed model attribution = %#v", record)
	}
}

func TestRunPassEmitsStageEvents(t *testing.T) {
	workDir := t.TempDir()
	intent := "test task"
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: intent,
		Stages:        []schemas.ExecutionStage{{Name: "memory_stage"}},
	}
	var reasoning []string
	var inputs []schemas.HarnessStageInput
	retriever := &stubStore{}
	_, _, completed, err := runPass(context.Background(), "run-stage-test", 1, plan, stageRegistry{
		"memory_stage": &capturingStage{inputs: &inputs},
	}, runFakeProvider{}, agent.Options{OnReasoning: func(s string) { reasoning = append(reasoning, s) }}, workDir, nil, nil, retriever)
	if err != nil || !completed {
		t.Fatalf("runPass failed: err=%v completed=%v", err, completed)
	}
	// Collect all stage markers from the reasoning stream.
	var markers []string
	for _, line := range reasoning {
		if strings.HasPrefix(line, stageEventMarkerBegin) {
			markers = append(markers, line)
		}
	}
	if len(markers) < 2 {
		t.Fatalf("expected at least 2 stage markers (running + completed), got %d: %v", len(markers), markers)
	}
	// First marker should be "running", last should be "completed".
	first := strings.TrimSuffix(strings.TrimPrefix(markers[0], stageEventMarkerBegin), stageEventMarkerEnd)
	var firstEvent map[string]any
	if err := json.Unmarshal([]byte(first), &firstEvent); err != nil {
		t.Fatalf("parse first marker: %v", err)
	}
	if firstEvent["status"] != "running" {
		t.Fatalf("first marker status = %v, want running", firstEvent["status"])
	}
	last := strings.TrimSuffix(strings.TrimPrefix(markers[len(markers)-1], stageEventMarkerBegin), stageEventMarkerEnd)
	var lastEvent map[string]any
	if err := json.Unmarshal([]byte(last), &lastEvent); err != nil {
		t.Fatalf("parse last marker: %v", err)
	}
	if lastEvent["status"] != "completed" {
		t.Fatalf("last marker status = %v, want completed", lastEvent["status"])
	}
	if lastEvent["progress"] != float64(100) {
		t.Fatalf("completed progress = %v, want 100", lastEvent["progress"])
	}
}

func TestRunPassEmitsStageEventsWithChangedFiles(t *testing.T) {
	workDir := t.TempDir()
	intent := "test task"
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: intent,
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	var reasoning []string
	codeOut := schemas.CodeWriterOutput{
		Files: []schemas.FileChange{
			{Path: "main.go", Content: "package main", ChangeType: "create"},
			{Path: "util.go", Content: "package util", ChangeType: "create"},
		},
		Language:   "go",
		Intent:     intent,
		Confidence: 1.0,
	}
	stage := &outputStage{
		output: schemas.HarnessStageOutput{
			Summary:    "wrote code",
			Detail:     "created files",
			Confidence: 1.0,
			Data: map[string]any{
				"code_writer_output": codeOut,
			},
		},
	}
	retriever := &stubStore{}
	_, _, completed, err := runPass(context.Background(), "run-changed-files", 1, plan, stageRegistry{
		"code_writer": stage,
	}, runFakeProvider{}, agent.Options{OnReasoning: func(s string) { reasoning = append(reasoning, s) }}, workDir, nil, nil, retriever)
	if err != nil || !completed {
		t.Fatalf("runPass failed: err=%v completed=%v", err, completed)
	}
	// Find the completed marker and check changedFiles.
	for _, line := range reasoning {
		if strings.HasPrefix(line, stageEventMarkerBegin) {
			payload := strings.TrimSuffix(strings.TrimPrefix(line, stageEventMarkerBegin), stageEventMarkerEnd)
			var evt map[string]any
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				t.Fatalf("parse marker: %v", err)
			}
			if evt["status"] == "completed" {
				cf, ok := evt["changedFiles"]
				if !ok {
					t.Fatal("completed marker missing changedFiles")
				}
				files, ok := cf.([]any)
				if !ok || len(files) != 2 {
					t.Fatalf("expected 2 changedFiles, got %v (type %T)", cf, cf)
				}
				if files[0] != "main.go" {
					t.Fatalf("changedFiles[0] = %v, want main.go", files[0])
				}
				if files[1] != "util.go" {
					t.Fatalf("changedFiles[1] = %v, want util.go", files[1])
				}
				return
			}
		}
	}
	t.Fatal("no completed stage marker found in reasoning stream")
}

func TestStepBackIntegration(t *testing.T) {
	workDir := t.TempDir()

	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "step back plateau test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	ps := &plateauStage{}
	provider := &stepBackRunFakeProvider{analysis: schemas.StepBackAnalysis{
		HypothesizedRootCause: "test hypothesis",
		Evidence:              []string{"score plateau"},
		RecommendedApproach:   "test approach",
		Confidence:            0.8,
	}}

	result, err := runIterationLoop(
		context.Background(),
		"step-back-run",
		plan,
		stageRegistry{"code_writer": ps},
		provider,
		agent.Options{Cwd: workDir, MaxTurns: 5},
		workDir,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	// The distinct code content avoids the cycle-detection rule, while the
	// flat test score triggers the plateau rule. Step-back fires at least once,
	// but because the score never improves the run eventually hits the hard
	// iteration limit (maxIterations=5) and aborts.
	if result.Status != "aborted" {
		t.Fatalf("expected aborted after step-back plateau, got status=%q abort_reason=%v", result.Status, DerefString(result.AbortReason))
	}
	if result.AbortReason == nil || !strings.Contains(*result.AbortReason, "Maximum iteration count reached") {
		t.Fatalf("expected hard-limit abort, got %q", DerefString(result.AbortReason))
	}
	if provider.stepBackCallCount < 1 {
		t.Fatalf("step-back was not called (count=%d)", provider.stepBackCallCount)
	}
	if provider.analysis.HypothesizedRootCause != "test hypothesis" || provider.analysis.RecommendedApproach != "test approach" {
		t.Fatalf("step-back analysis did not round-trip: %+v", provider.analysis)
	}
	if ps.calls != 5 {
		t.Fatalf("stage calls = %d, want 5", ps.calls)
	}
}

// plateauStage implements stages.Stage and produces distinct code content on
// every call while keeping the test score flat (1 pass, 1 fail). The distinct
// content avoids the trajectory cycle-detection rule, so the plateau rule
// eventually fires ActionStepBack.
type plateauStage struct {
	calls int
}

func (s *plateauStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	return schemas.HarnessStageOutput{
		Summary:    "plateau output",
		Confidence: 0.7,
		Data: map[string]any{
			"code_writer_output": schemas.CodeWriterOutput{
				Files:      []schemas.FileChange{{Path: "main.go", Content: fmt.Sprintf("package main\n// iteration %d\n", s.calls), ChangeType: "create"}},
				Language:   "go",
				Intent:     "create",
				Confidence: 0.7,
			},
			"test_results": schemas.TestRunResults{
				Command: []string{"go", "test"},
				Tests: []schemas.TestCaseResult{
					{Name: "TestA", Status: "passed", DurationMs: 1},
					{Name: "TestB", Status: "failed", DurationMs: 2, Message: "not working"},
				},
				ExitCode: 1,
			},
		},
	}, nil
}

// stepBackRunFakeProvider handles both submit_code and submit_step_back.
type stepBackRunFakeProvider struct {
	analysis          schemas.StepBackAnalysis
	stepBackCallCount int
}

func (f *stepBackRunFakeProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 8)
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	var args string
	switch toolName {
	case "submit_step_back":
		f.stepBackCallCount++
		b, _ := json.Marshal(f.analysis)
		args = string(b)
	case "submit_code":
		out := schemas.CodeWriterOutput{
			Files: []schemas.FileChange{
				{Path: "main.go", Content: "package main\n", ChangeType: "create"},
			},
			Language:   "go",
			Intent:     "create",
			Confidence: 0.7,
		}
		b, _ := json.Marshal(out)
		args = string(b)
	default:
		args = "{}"
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: args}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	if toolName == "submit_step_back" {
		ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 5, OutputTokens: 3}}
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

// cycleStage produces the same output every call, triggering cycle detection.
// It records the provider it receives on each call so tests can assert that
// escalation actually swapped the provider for subsequent iterations.
type cycleStage struct {
	calls     int
	providers []agent.Provider
}

func (s *cycleStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	s.providers = append(s.providers, provider)
	// Identical output every time = same state hash = cycle detected.
	// Include a failing test so passSucceeded returns false and the
	// trajectory evaluation fires.
	return schemas.HarnessStageOutput{
		Summary:    "cycle output",
		Confidence: 0.7,
		Data: map[string]any{
			"code_writer_output": schemas.CodeWriterOutput{
				Files:      []schemas.FileChange{{Path: "main.go", Content: "package main\n", ChangeType: "create"}},
				Language:   "go",
				Intent:     "create",
				Confidence: 0.7,
			},
			"test_results": schemas.TestRunResults{
				Command: []string{"go", "test"},
				Tests: []schemas.TestCaseResult{
					{Name: "TestA", Status: "passed", DurationMs: 1},
					{Name: "TestB", Status: "failed", DurationMs: 2, Message: "always fails"},
				},
				ExitCode: 1,
			},
		},
	}, nil
}

// namedProvider is a provider that reports its name for test assertions.
type namedProvider struct {
	name string
}

func (p *namedProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, 8)
	toolName := ""
	if len(request.Tools) > 0 {
		toolName = request.Tools[0].Name
	}
	out := schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "main.go", Content: "package main\n", ChangeType: "create"}},
		Language:   "go",
		Intent:     "create",
		Confidence: 0.7,
	}
	b, _ := json.Marshal(out)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "1", ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "1", ArgumentsFragment: string(b)}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "1"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestRunEscalatesOnCycle(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "escalation cycle test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	cs := &cycleStage{}
	defaultProvider := &namedProvider{name: "default"}
	escalationProvider := &namedProvider{name: "escalated"}
	resolverCalls := 0

	escalationResolver := func() (agent.Provider, string, string, error) {
		resolverCalls++
		return escalationProvider, "escalated-model", "high", nil
	}

	result, err := runIterationLoop(
		context.Background(),
		"escalation-run",
		plan,
		stageRegistry{"code_writer": cs},
		defaultProvider,
		agent.Options{Cwd: workDir, MaxTurns: 5, EscalationModelResolver: escalationResolver},
		workDir,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted after cycle with max iterations, got status=%q abort_reason=%v", result.Status, DerefString(result.AbortReason))
	}
	if result.AbortReason == nil || !strings.Contains(*result.AbortReason, "Maximum iteration count reached") {
		t.Fatalf("expected hard-limit abort, got %q", DerefString(result.AbortReason))
	}
	// Escalation resolver should be called exactly once.
	if resolverCalls != 1 {
		t.Fatalf("escalation resolver called %d times, want 1", resolverCalls)
	}
	// Stage should have been called for all 5 iterations.
	if cs.calls != 5 {
		t.Fatalf("stage calls = %d, want 5", cs.calls)
	}
	// Iterations 1 and 2 use the default provider; the cycle fires at
	// iteration 2, so iterations 3+ must use the escalated provider.
	if len(cs.providers) != 5 {
		t.Fatalf("recorded %d providers, want 5", len(cs.providers))
	}
	if cs.providers[0] != defaultProvider {
		t.Fatalf("iteration 1 provider = %p, want default", cs.providers[0])
	}
	if cs.providers[2] != escalationProvider {
		t.Fatalf("iteration 3 provider = %p, want escalated (provider swap did not take effect)", cs.providers[2])
	}
}

func TestRunEscalationNilResolverNonFatal(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "escalation nil resolver test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	cs := &cycleStage{}
	// No EscalationModelResolver set. Cycle/oscillation should fall through
	// to revision context recovery and continue.
	result, err := runIterationLoop(
		context.Background(),
		"escalation-nil",
		plan,
		stageRegistry{"code_writer": cs},
		&namedProvider{name: "default"},
		agent.Options{Cwd: workDir, MaxTurns: 3},
		workDir,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted after cycle with max iterations, got status=%q", result.Status)
	}
	if cs.calls != 3 {
		t.Fatalf("stage calls = %d, want 3", cs.calls)
	}
}

func TestRunEscalationErrorResolverNonFatal(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "escalation error resolver test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	cs := &cycleStage{}
	errMsg := "simulated resolver error"
	escalationResolver := func() (agent.Provider, string, string, error) {
		return nil, "", "", fmt.Errorf("%s", errMsg)
	}

	result, err := runIterationLoop(
		context.Background(),
		"escalation-err",
		plan,
		stageRegistry{"code_writer": cs},
		&namedProvider{name: "default"},
		agent.Options{Cwd: workDir, MaxTurns: 3, EscalationModelResolver: escalationResolver},
		workDir,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted after cycle with max iterations, got status=%q", result.Status)
	}
	if cs.calls != 3 {
		t.Fatalf("stage calls = %d, want 3", cs.calls)
	}
}

// surfaceToUserStage implements stages.Stage and produces distinct content per
// call (avoiding cycle detection), improving scores (avoiding plateau and
// rollback), strictly decreasing confidence (triggering ActionSurfaceToUser),
// and 1 failing test per iteration (passSucceeded=false). Intended for tests
// that exercise the ActionSurfaceToUser trajectory decision.
// Confidence = max(0.1, 0.9 - 0.2*(calls-1)). Pass count increments to create
// improving scores: iter1=0 pass, iter2=1 pass, iter3+=2 pass.
type surfaceToUserStage struct {
	calls int
}

func (s *surfaceToUserStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	confidence := 0.9 - 0.2*float64(s.calls-1)
	if confidence < 0.1 {
		confidence = 0.1
	}
	// Always keep TestB failing so passSucceeded returns false. Add extra
	// passing tests to create improving scores (avoiding the plateau rule):
	// iter1=0 extra, iter2=1 extra, iter3+=2 extra.
	extraPassed := 0
	if s.calls >= 2 {
		extraPassed = 1
	}
	if s.calls >= 3 {
		extraPassed = 2
	}
	tests := []schemas.TestCaseResult{
		{Name: "TestA", Status: "passed", DurationMs: 1},
		{Name: "TestB", Status: "failed", DurationMs: 2, Message: "not working"},
	}
	for i := 0; i < extraPassed; i++ {
		tests = append(tests, schemas.TestCaseResult{
			Name: fmt.Sprintf("TestExtra%d", i), Status: "passed", DurationMs: 1,
		})
	}
	return schemas.HarnessStageOutput{
		Summary:    "surface_to_user output",
		Confidence: confidence,
		Data: map[string]any{
			"code_writer_output": schemas.CodeWriterOutput{
				Files: []schemas.FileChange{
					{Path: "main.go", Content: fmt.Sprintf("package main\n// call %d\n", s.calls), ChangeType: "create"},
				},
				Language:   "go",
				Intent:     "create",
				Confidence: confidence,
			},
			"test_results": schemas.TestRunResults{
				Command:  []string{"go", "test"},
				Tests:    tests,
				ExitCode: 1,
			},
		},
	}, nil
}

func TestSurfaceToUserNilCallbackAborts(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "surface_to_user test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	stage := &surfaceToUserStage{}
	result, err := runIterationLoop(
		context.Background(),
		"s2u-nil-cb",
		plan,
		stageRegistry{"code_writer": stage},
		runFakeProvider{},
		agent.Options{Cwd: workDir, MaxTurns: 5},
		workDir,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted, got status=%q", result.Status)
	}
	if result.AbortReason == nil || !strings.Contains(*result.AbortReason, "surface_to_user") {
		t.Fatalf("expected surface_to_user in abort reason, got %q", DerefString(result.AbortReason))
	}
	if stage.calls != 3 {
		t.Fatalf("stage calls = %d, want 3 (surface_to_user fires at iter 3, aborting)", stage.calls)
	}
}

func TestSurfaceToUserContinue(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "surface_to_user continue test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	stage := &surfaceToUserStage{}
	var gotRequest agent.SurfaceToUserRequest

	onSurfaceToUser := func(ctx context.Context, req agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error) {
		gotRequest = req
		return agent.SurfaceToUserDecision{
			Action:  agent.SurfaceToUserContinue,
			Message: "try a different approach: focus on edge cases",
		}, nil
	}

	result, err := runIterationLoop(
		context.Background(),
		"s2u-continue",
		plan,
		stageRegistry{"code_writer": stage},
		runFakeProvider{},
		agent.Options{Cwd: workDir, MaxTurns: 5, OnSurfaceToUser: onSurfaceToUser},
		workDir,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted (max iterations), got status=%q", result.Status)
	}
	if result.AbortReason == nil || !strings.Contains(*result.AbortReason, "Maximum iteration count reached") {
		t.Fatalf("expected hard-limit abort, got %q", DerefString(result.AbortReason))
	}
	if stage.calls != 5 {
		t.Fatalf("stage calls = %d, want 5 (continue callback allows remaining iterations)", stage.calls)
	}
	if gotRequest.Reason == "" {
		t.Fatalf("expected non-empty reason in request, got %+v", gotRequest)
	}
	if len(gotRequest.RecentConfidences) != 3 {
		t.Fatalf("expected 3 recent confidences, got %v", gotRequest.RecentConfidences)
	}
}

func TestSurfaceToUserAbort(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "surface_to_user abort test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	stage := &surfaceToUserStage{}
	onSurfaceToUser := func(ctx context.Context, req agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error) {
		return agent.SurfaceToUserDecision{
			Action:  agent.SurfaceToUserAbort,
			Message: "this approach is wrong, start over",
		}, nil
	}

	result, err := runIterationLoop(
		context.Background(),
		"s2u-abort",
		plan,
		stageRegistry{"code_writer": stage},
		runFakeProvider{},
		agent.Options{Cwd: workDir, MaxTurns: 5, OnSurfaceToUser: onSurfaceToUser},
		workDir,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted, got status=%q", result.Status)
	}
	if result.AbortReason == nil || !strings.Contains(*result.AbortReason, "user aborted") {
		t.Fatalf("expected 'user aborted' in abort reason, got %q", DerefString(result.AbortReason))
	}
	if !strings.Contains(DerefString(result.AbortReason), "this approach is wrong, start over") {
		t.Fatalf("expected user message in abort reason, got %q", DerefString(result.AbortReason))
	}
	if stage.calls != 3 {
		t.Fatalf("stage calls = %d, want 3 (abort at iter 3)", stage.calls)
	}
}

func TestSurfaceToUserCallbackError(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "surface_to_user error test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	stage := &surfaceToUserStage{}
	expectedErr := fmt.Errorf("simulated callback failure")
	onSurfaceToUser := func(ctx context.Context, req agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error) {
		return agent.SurfaceToUserDecision{}, expectedErr
	}

	result, err := runIterationLoop(
		context.Background(),
		"s2u-error",
		plan,
		stageRegistry{"code_writer": stage},
		runFakeProvider{},
		agent.Options{Cwd: workDir, MaxTurns: 5, OnSurfaceToUser: onSurfaceToUser},
		workDir,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected failed, got status=%q", result.Status)
	}
	if result.AbortReason == nil || !strings.Contains(*result.AbortReason, "surface_to_user callback") {
		t.Fatalf("expected callback error in abort reason, got %q", DerefString(result.AbortReason))
	}
	if stage.calls != 3 {
		t.Fatalf("stage calls = %d, want 3", stage.calls)
	}
}

func TestSurfaceToUserCancellation(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "surface_to_user cancel test",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000, ModelTier: "small"}},
			OverflowPolicy:    "abort",
		},
	}

	stage := &surfaceToUserStage{}
	onSurfaceToUser := func(ctx context.Context, req agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error) {
		return agent.SurfaceToUserDecision{}, context.Canceled
	}

	_, err := runIterationLoop(
		context.Background(),
		"s2u-cancel",
		plan,
		stageRegistry{"code_writer": stage},
		runFakeProvider{},
		agent.Options{Cwd: workDir, MaxTurns: 5, OnSurfaceToUser: onSurfaceToUser},
		workDir,
		nil,
		nil,
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if stage.calls != 3 {
		t.Fatalf("stage calls = %d, want 3", stage.calls)
	}
}
