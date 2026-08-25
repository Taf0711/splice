package splice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/sandbox"
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
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 5, CachedInputTokens: 2, CacheWriteTokens: 1, ReasoningTokens: 2}}
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
				{Path: "main.go", Content: "package main\n\nfunc Hello() string { return \"wrong\" }\n", ChangeType: "modify"},
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
				{Path: "main_test.go", Content: "package main\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"wrong greeting\")\n\t}\n}\n", ChangeType: "modify"},
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
	// queries logs every Search in call order, so tests can assert exactly which
	// stages triggered retrieval.
	queries []schemas.MemoryQuery
	upserts *[]schemas.MemoryObservation
}

func (s *stubStore) Search(ctx context.Context, q schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	s.gotQuery = &q
	s.queries = append(s.queries, q)
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
	caps   stages.Capabilities
}

func (s *capturingStage) Capabilities() stages.Capabilities { return s.caps }

func (s *capturingStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	*s.inputs = append(*s.inputs, input)
	return schemas.HarnessStageOutput{
		Summary:    "captured",
		Detail:     "captured",
		Confidence: 1,
	}, nil
}

type stageFunc func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error)

func (stageFunc) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (f stageFunc) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	return f(ctx, input, provider, options)
}

type outputStage struct {
	output schemas.HarnessStageOutput
}

func (outputStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (s outputStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	return s.output, nil
}

type capturedStageCall struct {
	provider zeroruntime.Provider
	options  stages.StageOptions
}

type stageCallCapturer struct {
	calls map[string]capturedStageCall
	caps  stages.Capabilities
}

func (s *stageCallCapturer) Capabilities() stages.Capabilities { return s.caps }

func (s *stageCallCapturer) Run(_ context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls[input.StageName] = capturedStageCall{provider: provider, options: options}
	return schemas.HarnessStageOutput{Summary: "captured", Confidence: 1}, nil
}

type contextRequestStage struct {
	inputs *[]schemas.HarnessStageInput
}

func (*contextRequestStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (s *contextRequestStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	*s.inputs = append(*s.inputs, input)
	if input.Context == nil {
		symbol := "foo"
		usage := schemas.StageUsage{InputTokens: 4, OutputTokens: 3, CachedInputTokens: 1, CacheWriteTokens: 1, ReasoningTokens: 1}
		if options.Stream.OnUsageResult != nil {
			options.Stream.OnUsageResult(zeroruntime.Usage{InputTokens: 4, OutputTokens: 3, CachedInputTokens: 1, CacheWriteTokens: 1, ReasoningTokens: 1}, true, nil)
		}
		return schemas.HarnessStageOutput{
			Summary:    "needs context",
			Confidence: 0.5,
			Usage:      &usage,
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
	usage := schemas.StageUsage{InputTokens: 6, OutputTokens: 5, CachedInputTokens: 2, CacheWriteTokens: 1, ReasoningTokens: 2}
	if options.Stream.OnUsageResult != nil {
		options.Stream.OnUsageResult(zeroruntime.Usage{InputTokens: 6, OutputTokens: 5, CachedInputTokens: 2, CacheWriteTokens: 1, ReasoningTokens: 2}, true, nil)
	}
	return schemas.HarnessStageOutput{
		Summary:    "context handled",
		Detail:     "context handled",
		Confidence: 1,
		Usage:      &usage,
	}, nil
}

type contextRetryFailureStage struct{ calls int }

func (*contextRetryFailureStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (s *contextRetryFailureStage) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	if s.calls == 1 {
		return schemas.HarnessStageOutput{
			Summary:        "needs context",
			Confidence:     0.5,
			Usage:          &schemas.StageUsage{InputTokens: 4, OutputTokens: 3, CachedInputTokens: 1, CacheWriteTokens: 1, ReasoningTokens: 1},
			ContextRequest: &schemas.ContextRequest{Reason: "inspect", Queries: []schemas.ContextQuery{{QueryType: schemas.ContextGetSymbol, Symbol: Ptr("foo"), MaxResults: 1, MaxChars: 100}}},
		}, nil
	}
	return schemas.HarnessStageOutput{}, meteredStageFailure{usage: &schemas.StageUsage{InputTokens: 6, OutputTokens: 5, CachedInputTokens: 2, CacheWriteTokens: 1, ReasoningTokens: 2}}
}

func TestRunStageWithContextFailurePreservesBothAttemptsUsage(t *testing.T) {
	stage := &contextRetryFailureStage{}
	_, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID:     "run-context-failure",
		StageName: "context_stage",
	}, stage, 1, agent.ModelSelection{Provider: runFakeProvider{}}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, nil, 0, nil)
	if err == nil {
		t.Fatal("runStageWithContext returned nil error")
	}
	var metered interface{ StageUsage() *schemas.StageUsage }
	if !errors.As(err, &metered) {
		t.Fatalf("error %T does not preserve usage", err)
	}
	want := schemas.StageUsage{InputTokens: 10, OutputTokens: 8, CachedInputTokens: 3, CacheWriteTokens: 2, ReasoningTokens: 3}
	if got := metered.StageUsage(); got == nil || *got != want {
		t.Fatalf("failure usage = %#v, want %#v", got, want)
	}
}

func TestRunPassInjectsMemoryBundleAndSkipsRetrievalErrors(t *testing.T) {
	workDir := t.TempDir()
	intent := strings.Repeat("界", 201) + " done"
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: intent,
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	project := workDir
	bundle := schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations: []schemas.MemoryObservation{{
			ID:          11,
			ProjectPath: &project,
			Scope:       "project",
			OwnerAgent:  "code_writer",
			Visibility:  "shareable",
			MemoryType:  "decision",
			Title:       "Use cached context",
			Content:     "Prefer the previously selected implementation path.",
		}},
	}
	retriever := &stubStore{bundle: bundle}
	var inputs []schemas.HarnessStageInput

	records, outputs, completed, err := runPass(context.Background(), "run-memory", 1, plan, stageRegistry{
		"code_writer": &capturingStage{inputs: &inputs, caps: stages.Capabilities{ConsumesMemory: true, PullContext: true}},
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, retriever, nil)
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
	if retriever.gotQuery.RequestingAgent != "code_writer" {
		t.Fatalf("requesting agent = %q, want code_writer", retriever.gotQuery.RequestingAgent)
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
		"code_writer": &capturingStage{inputs: &errorInputs, caps: stages.Capabilities{ConsumesMemory: true, PullContext: true}},
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{OnReasoning: func(text string) { progress = append(progress, text) }}), workDir, nil, time.Time{}, nil, errorRetriever, nil)
	if err != nil || !completed {
		t.Fatalf("memory retrieval error should not fail run: completed=%v err=%v", completed, err)
	}
	if len(errorInputs) != 1 || errorInputs[0].MemoryBundle != nil {
		t.Fatalf("expected no memory bundle after retrieval error, got %#v", errorInputs)
	}
	if !strings.Contains(strings.Join(progress, ""), "[code_writer] memory retrieval skipped: sidecar down") {
		t.Fatalf("expected memory skip progress, got %q", strings.Join(progress, ""))
	}

	var nilInputs []schemas.HarnessStageInput
	_, _, completed, err = runPass(context.Background(), "run-memory-nil", 1, plan, stageRegistry{
		"code_writer": &capturingStage{inputs: &nilInputs, caps: stages.Capabilities{ConsumesMemory: true, PullContext: true}},
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("nil retriever should complete: completed=%v err=%v", completed, err)
	}
	if len(nilInputs) != 1 || nilInputs[0].MemoryBundle != nil {
		t.Fatalf("expected nil retriever to leave memory unset, got %#v", nilInputs)
	}
}

// TestRunPassSearchesOnlyMemoryConsumingStages: the orchestrator must call
// MemoryStore.Search only for stages that consume HarnessStageInput.MemoryBundle
// (code_writer and test_generator). Every other stage must receive no
// MemoryBundle and trigger no search, even when a memory store is live.
func TestRunPassSearchesOnlyMemoryConsumingStages(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierStandard,
		RequestIntent: "implement the feature",
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer"},
			{Name: "static_analyzer"},
			{Name: "test_generator"},
			{Name: "test_runner"},
			{Name: "custom_stage"},
		},
	}
	retriever := &stubStore{}
	inputNames := []string{"code_writer", "static_analyzer", "test_generator", "test_runner", "custom_stage"}
	registry := stageRegistry{}
	var inputs []schemas.HarnessStageInput
	for _, name := range inputNames {
		stage := &capturingStage{inputs: &inputs}
		if name == "code_writer" || name == "test_generator" {
			stage.caps = stages.Capabilities{ConsumesMemory: true, PullContext: true}
		}
		registry[name] = stage
	}

	_, _, completed, err := runPass(context.Background(), "run-memory-consumers", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, retriever, nil)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	if got := len(retriever.queries); got != 2 {
		t.Fatalf("memory searches = %d, want exactly 2 (code_writer, test_generator); queries=%#v", got, retriever.queries)
	}
	if got := retriever.queries[0].RequestingAgent; got != "code_writer" {
		t.Fatalf("first search requesting agent = %q, want code_writer", got)
	}
	if got := retriever.queries[1].RequestingAgent; got != "test_generator" {
		t.Fatalf("second search requesting agent = %q, want test_generator", got)
	}
	if len(inputs) != len(inputNames) {
		t.Fatalf("captured inputs = %d, want %d", len(inputs), len(inputNames))
	}
	for _, input := range inputs {
		switch input.StageName {
		case "code_writer", "test_generator":
			// Zero eligible items after deterministic admission means no
			// delivered memory: the bundle stays absent rather than empty.
			if input.MemoryBundle != nil && len(input.MemoryBundle.Observations)+len(input.MemoryBundle.Exemplars) == 0 {
				t.Fatalf("consuming stage %s received an empty memory bundle; want none", input.StageName)
			}
			if retriever.queries[0].RequestingAgent == "" {
				t.Fatal("expected at least one memory query")
			}
		default:
			if input.MemoryBundle != nil {
				t.Fatalf("non-consuming stage %s received a memory bundle: %#v", input.StageName, input.MemoryBundle)
			}
		}
	}
}

func TestRunPassPopulatesPipelineRoster(t *testing.T) {
	workDir := t.TempDir()
	stageNames := []string{"code_writer", "test_generator", "static_analyzer", "test_runner"}
	registry := stageRegistry{}
	var inputs []schemas.HarnessStageInput
	for _, name := range stageNames {
		registry[name] = &capturingStage{inputs: &inputs}
	}
	plan := schemas.ExecutionPlan{Tier: schemas.TierStandard, RequestIntent: "standard tier roster"}
	for _, name := range stageNames {
		plan.Stages = append(plan.Stages, schemas.ExecutionStage{Name: name})
	}

	_, _, completed, err := runPass(context.Background(), "run-roster-standard", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	if len(inputs) != len(stageNames) {
		t.Fatalf("captured %d inputs, want %d", len(inputs), len(stageNames))
	}
	for i, in := range inputs {
		if strings.Join(in.PipelineStages, ",") != strings.Join(stageNames, ",") {
			t.Fatalf("stage %q pipeline_stages = %v, want %v", in.StageName, in.PipelineStages, stageNames)
		}
		wantNext := ""
		if i+1 < len(stageNames) {
			wantNext = stageNames[i+1]
		}
		if in.NextStage != wantNext {
			t.Fatalf("stage %q next_stage = %q, want %q", in.StageName, in.NextStage, wantNext)
		}
	}
	if inputs[0].StageName != "code_writer" || inputs[0].NextStage != "test_generator" {
		t.Fatalf("code_writer input = %+v, want next_stage test_generator", inputs[0])
	}
	last := inputs[len(inputs)-1]
	if last.StageName != "test_runner" || last.NextStage != "" {
		t.Fatalf("last stage input = %+v, want empty next_stage", last)
	}
}

func TestRunPassCarriesWriterChangedPathsToTestGenerator(t *testing.T) {
	workDir := t.TempDir()
	var testInput schemas.HarnessStageInput
	codeOut := schemas.CodeWriterOutput{
		Files: []schemas.FileChange{
			{Path: "storage.go", Content: "package storage\n", ChangeType: "create"},
		},
		Language: "go", Intent: "write storage", Confidence: 1,
	}
	registry := stageRegistry{
		"code_writer": outputStage{output: schemas.HarnessStageOutput{
			Summary: "implemented storage", Confidence: 1,
			Data: map[string]any{"code_writer_output": codeOut},
		}},
		"test_generator": stageFunc(func(_ context.Context, input schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
			testInput = input
			return schemas.HarnessStageOutput{Summary: "generated tests", Confidence: 1}, nil
		}),
	}
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierStandard, RequestIntent: "write storage tests",
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_generator"}},
	}
	_, _, completed, err := runPass(context.Background(), "run-writer-paths", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	if got := testInput.PriorChangedFiles["code_writer"]; len(got) != 1 || got[0] != "storage.go" {
		t.Fatalf("test generator prior changed files = %v, want [storage.go]", got)
	}
}

func TestBuildRevisionContextCarriesPriorOutputFiles(t *testing.T) {
	output := schemas.HarnessStageOutput{Data: map[string]any{
		"test_generator_output": schemas.TestGeneratorOutput{
			Files: []schemas.FileChange{{Path: "storage_test.go", ChangeType: "modify"}},
		},
	}}
	got := buildRevisionContext("revise tests", nil, nil, []schemas.HarnessStageOutput{output}, "retry")
	if !strings.Contains(got, "storage_test.go") || !strings.Contains(got, "overwrite: true") {
		t.Fatalf("revision context did not surface prior file and overwrite guidance: %q", got)
	}
}

func TestRunPassPopulatesPipelineRosterTrivialTier(t *testing.T) {
	workDir := t.TempDir()
	var inputs []schemas.HarnessStageInput
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierTrivial,
		RequestIntent: "trivial tier roster",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	registry := stageRegistry{"code_writer": &capturingStage{inputs: &inputs}}

	_, _, completed, err := runPass(context.Background(), "run-roster-trivial", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	if len(inputs) != 1 {
		t.Fatalf("captured %d inputs, want 1", len(inputs))
	}
	if got := inputs[0].PipelineStages; len(got) != 1 || got[0] != "code_writer" {
		t.Fatalf("pipeline_stages = %v, want [code_writer]", got)
	}
	if inputs[0].NextStage != "" {
		t.Fatalf("next_stage = %q, want empty for the only stage", inputs[0].NextStage)
	}
}

func TestRunPassSkipsStagesAfterWallDeadline(t *testing.T) {
	capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}}
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "wall deadline test",
		Stages:        []schemas.ExecutionStage{{Name: "a"}, {Name: "b"}},
	}
	_, _, completed, err := runPass(context.Background(), "run-wall", 1, plan, stageRegistry{
		"a": capturer,
		"b": capturer,
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Now().Add(-time.Second), nil, nil, nil)
	if !errors.Is(err, errWallTimeExceeded) {
		t.Fatalf("err = %v, want wall-time sentinel", err)
	}
	if len(capturer.calls) != 0 {
		t.Fatalf("stages ran after deadline: %v", capturer.calls)
	}
	if completed {
		t.Fatal("completed = true, want false")
	}
}

func TestRunPassStageCrossingWallDeadlineAborts(t *testing.T) {
	started := false
	stage := stageFunc(func(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
		started = true
		<-ctx.Done()
		return schemas.HarnessStageOutput{}, context.Canceled
	})
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "wall deadline crossing",
		Stages:        []schemas.ExecutionStage{{Name: "blocker"}},
	}
	parentCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, _, err := runPass(parentCtx, "run-wall-cross", 1, plan, stageRegistry{
		"blocker": stage,
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Now().Add(50*time.Millisecond), nil, nil, nil)
	if !started {
		t.Fatal("stage did not start")
	}
	if parentCtx.Err() != nil {
		t.Fatalf("parent context ended: %v", parentCtx.Err())
	}
	if !errors.Is(err, errWallTimeExceeded) {
		t.Fatalf("err = %#v, want errWallTimeExceeded", err)
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
	project := workDir
	bundle := schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations: []schemas.MemoryObservation{{
			ID:          7,
			ProjectPath: &project,
			Scope:       "project",
			OwnerAgent:  "orchestrator",
			Visibility:  "shareable",
			MemoryType:  "decision",
			Title:       "Use gofmt",
			Content:     "Run gofmt on all generated files.",
		}},
	}
	store := &stubStore{bundle: bundle}
	provider := &captureRequestProvider{}
	fakeRunner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: ""}, nil
	})

	records, _, completed, err := runPass(context.Background(), "run-memory-consume", 1, plan, stageRegistry{
		"code_writer": stages.CodeWriter{},
	}, provider, PipelineConfigFromAgentOptions(agent.Options{}), workDir, fakeRunner, time.Time{}, nil, store, nil)
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
		}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, store, nil)
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
		}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, store, nil)
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

	_, err = runExecutionPlan(context.Background(), runID, plan, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 1}), store, nil)
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
	var attributed []agent.AttributedUsage
	selection := agent.ModelSelection{Provider: runFakeProvider{}, ProviderName: "provider-a", Model: "model-a"}

	output, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID:     runID,
		StageName: stageName,
	}, stage, 1, selection, PipelineConfigFromAgentOptions(agent.Options{OnAttributedUsage: func(usage agent.AttributedUsage) {
		attributed = append(attributed, usage)
	}}), workDir, nil, store, 0, nil)
	if err != nil {
		t.Fatalf("runStageWithContext: %v", err)
	}
	if output.Summary != "context handled" {
		t.Fatalf("output summary = %q, want context handled", output.Summary)
	}
	wantUsage := schemas.StageUsage{InputTokens: 10, OutputTokens: 8, CachedInputTokens: 3, CacheWriteTokens: 2, ReasoningTokens: 3}
	if output.Usage == nil || *output.Usage != wantUsage {
		t.Fatalf("merged usage = %#v, want %#v", output.Usage, wantUsage)
	}
	if len(inputs) != 2 || inputs[1].Context == nil {
		t.Fatalf("expected two stage calls with context on second call, got %#v", inputs)
	}
	if len(attributed) != 2 {
		t.Fatalf("attributed usage calls = %d, want 2", len(attributed))
	}
	for _, got := range attributed {
		if !got.UsageReported || got.ProviderName != "provider-a" || got.Model != "model-a" || got.Stage != stageName || got.Iteration != 1 {
			t.Fatalf("attributed context usage = %+v", got)
		}
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

func TestTrustedWorkspaceReadAndWriteDoNotPromptInAskMode(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewListDirectoryTool(workDir))
	registry.Register(tools.NewGrepTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))

	readRequests := 0
	writeRequests := 0
	runner := newAgentToolRunner(PipelineConfigFromAgentOptions(agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAsk,
		TrustedWorkspace: true,
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: workDir,
			Policy:        sandbox.DefaultPolicy(),
		}),
		OnPermissionRequest: func(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
			switch req.ToolName {
			case "read_file":
				readRequests++
			case "write_file":
				writeRequests++
			}
			return agent.PermissionDecision{Action: agent.PermissionDecisionDeny}, nil
		},
	}), workDir)

	// Deterministic pipeline reads (read_file, list_directory) run inside the
	// workspace and declare PermissionAllow, so they must not reach the
	// permission request callback even in Ask mode.
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"read_file", map[string]any{"path": filepath.Join(workDir, "a.go")}},
		{"list_directory", map[string]any{"path": workDir}},
	} {
		res, err := runner.RunTool(context.Background(), call.name, call.args)
		if err != nil {
			t.Fatalf("%s error = %v", call.name, err)
		}
		if !res.OK {
			t.Fatalf("%s failed: %s", call.name, res.Output)
		}
	}
	if readRequests != 0 {
		t.Fatalf("trusted in-workspace reads triggered %d permission requests, want 0", readRequests)
	}

	// Trust auto-allows an in-workspace file mutation. The registry and sandbox
	// still run the call and keep the path inside the workspace.
	if res, err := runner.RunTool(context.Background(), "write_file", map[string]any{"path": "b.go", "content": "package x\n"}); err != nil || !res.OK {
		t.Fatalf("trusted write_file failed: result=%#v err=%v", res, err)
	}
	if writeRequests != 0 {
		t.Fatalf("trusted write_file triggered %d permission requests, want 0", writeRequests)
	}

	// An external path still prompts and is denied by the callback.
	outParent, err := os.MkdirTemp(".", ".splice-trust-outside-")
	if err != nil {
		t.Fatalf("create external directory: %v", err)
	}
	defer os.RemoveAll(outParent)
	outside, err := filepath.Abs(filepath.Join(outParent, "outside.go"))
	if err != nil {
		t.Fatalf("resolve external path: %v", err)
	}
	if res, _ := runner.RunTool(context.Background(), "write_file", map[string]any{"path": outside, "content": "package x\n"}); res.OK {
		t.Fatal("external write_file unexpectedly succeeded")
	}
	if writeRequests != 1 {
		t.Fatalf("external write_file triggered %d permission requests, want 1", writeRequests)
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
	capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}, caps: stages.Capabilities{ModelFree: true}}
	registry := stageRegistry{}
	modelFreeNames := []string{"static_analyzer", "security_auditor", "test_runner"}
	plan := schemas.ExecutionPlan{Tier: schemas.TierSubstantial, RequestIntent: "verify deterministically"}
	for _, name := range modelFreeNames {
		plan.Stages = append(plan.Stages, schemas.ExecutionStage{Name: name})
		registry[name] = capturer
	}
	resolverCalls := 0
	records, _, completed, err := runPass(context.Background(), "run-model-free", 1, plan, registry, &namedProvider{name: "default"}, PipelineConfigFromAgentOptions(agent.Options{
		Model:           "default-model",
		ReasoningEffort: "high",
		ProviderName:    "default-provider",
		StageModelResolver: func(stageName string) (agent.ModelSelection, error) {
			resolverCalls++
			return agent.ModelSelection{Provider: &namedProvider{name: "unexpected"}, ProviderName: "unexpected-provider", Model: "unexpected-model", ReasoningEffort: "low"}, nil
		},
	}), workDir, nil, time.Time{}, nil, nil, nil)
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
	records, _, completed, err := runPass(context.Background(), "run-model-backed", 1, plan, registry, &namedProvider{name: "default"}, PipelineConfigFromAgentOptions(agent.Options{
		Model:           "default-model",
		ReasoningEffort: "medium",
		ProviderName:    "default-provider",
		StageModelResolver: func(stageName string) (agent.ModelSelection, error) {
			resolved = append(resolved, stageName)
			return agent.ModelSelection{Provider: routedProvider, ProviderName: "routed-provider", Model: "routed-model", ReasoningEffort: "high"}, nil
		},
	}), workDir, nil, time.Time{}, nil, nil, nil)
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
		if record.Model == nil || *record.Model != "routed-model" || record.Provider == nil || *record.Provider != "routed-provider" {
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
		StageModelResolver: func(stageName string) (agent.ModelSelection, error) {
			resolvedStages = append(resolvedStages, stageName)
			return agent.ModelSelection{Provider: fakeProvider, ProviderName: "test-provider", Model: "test-model", ReasoningEffort: "high"}, nil
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

	var foundCompleteUsage bool
	var input, output, cached, cacheWrite, reasoning int
	for _, r := range result.Stages {
		input += r.TokensInput
		output += r.TokensOutput
		cached += r.TokensCached
		cacheWrite += r.TokensCacheWrite
		reasoning += r.TokensReasoning
		if r.TokensInput > 0 && r.TokensOutput > 0 && r.TokensCached > 0 && r.TokensCacheWrite > 0 && r.TokensReasoning > 0 {
			foundCompleteUsage = true
		}
	}
	if !foundCompleteUsage {
		t.Fatalf("expected a stage with all token dimensions, got %+v", result.Stages)
	}
	if result.TotalTokensInput != input || result.TotalTokensOutput != output || result.TotalTokensCached != cached || result.TotalTokensCacheWrite != cacheWrite || result.TotalTokensReasoning != reasoning {
		t.Fatalf("pipeline totals = %+v, want input=%d output=%d cached=%d cacheWrite=%d reasoning=%d", result, input, output, cached, cacheWrite, reasoning)
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

func TestRunMaxTurnsDoesNotCapPipelineIterations(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)

	result, err := Run(context.Background(), "add a security service with tests", runFailingProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
		FileTracker:    tools.NewFileTracker(),
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Incomplete {
		t.Fatal("expected incomplete result for a run that never succeeds")
	}
	var pipeline schemas.PipelineResult
	if err := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); err != nil {
		t.Fatalf("parse final answer: %v", err)
	}
	if pipeline.Status != "aborted" {
		t.Fatalf("pipeline status = %s, want aborted", pipeline.Status)
	}
	maxIteration := 0
	for _, record := range pipeline.Stages {
		if record.Iteration > maxIteration {
			maxIteration = record.Iteration
		}
	}
	if maxIteration != defaultMaxIterations {
		t.Fatalf("max pipeline iteration = %d, want %d despite MaxTurns=1", maxIteration, defaultMaxIterations)
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
	// Git-derived output requires the deterministic stage spawn to run, which
	// needs a host native sandbox backend.
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
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
	if summary.DegradedReason != "" {
		t.Fatalf("DegradedReason = %q, want empty on the git happy path", summary.DegradedReason)
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

// noBackendPath builds a PATH directory that hides every native sandbox
// backend executable while keeping the tools the test needs. It returns the
// directory path and sets PATH for the caller's test.
func noBackendPath(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	for _, name := range []string{"git", "sh", "env", "uname", "python3"} {
		resolved, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s unavailable: %v", name, err)
		}
		if err := os.Symlink(resolved, filepath.Join(binDir, name)); err != nil {
			t.Skipf("cannot symlink %s into the no-backend PATH: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir)
	return binDir
}

// TestSummarizeWorkspaceChangesRepositoryWithoutConfinement is the T1 pin: a
// real repository must stay a repository when the Git read is refused. The
// summary walks the tree instead, reports IsRepo true, and names the cause in
// DegradedReason.
func TestSummarizeWorkspaceChangesRepositoryWithoutConfinement(t *testing.T) {
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
	noBackendPath(t)

	summary := summarizeWorkspaceChanges(context.Background(), workDir)
	if !summary.IsRepo {
		t.Fatal("a repository must stay a repository without confinement: IsRepo=false")
	}
	if summary.DegradedReason == "" {
		t.Fatal("DegradedReason must be non-empty when the Git read could not run")
	}
	if !strings.Contains(summary.DegradedReason, "git") {
		t.Fatalf("DegradedReason must name the cause, got %q", summary.DegradedReason)
	}
	if len(summary.ChangedFiles) == 0 {
		t.Fatal("changed-file list must still be populated from the walk")
	}
	if err := summary.Validate(); err != nil {
		t.Fatalf("degraded repository summary must validate: %v", err)
	}
}

// TestSummarizeWorkspaceChangesPlainDirectoryUnchanged is the T2 pin: a plain
// directory keeps IsRepo false and an empty DegradedReason.
func TestSummarizeWorkspaceChangesPlainDirectoryUnchanged(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	summary := summarizeWorkspaceChanges(context.Background(), workDir)
	if summary.IsRepo {
		t.Fatal("plain directory must not be a repository")
	}
	if summary.DegradedReason != "" {
		t.Fatalf("DegradedReason = %q, want empty for a non-repository", summary.DegradedReason)
	}
	if err := summary.Validate(); err != nil {
		t.Fatalf("plain directory summary must validate: %v", err)
	}
}

func TestEmitPipelinePlanProducesTypedStageRoster(t *testing.T) {
	var events []agent.PipelinePlanEvent
	options := PipelineConfigFromAgentOptions(agent.Options{
		OnPipelinePlan: func(event agent.PipelinePlanEvent) { events = append(events, event) },
	})
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierStandard,
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer"},
			{Name: "test_runner"},
			{Name: "acceptance_verifier"},
		},
	}

	emitPipelinePlan(options, plan)

	if len(events) != 1 {
		t.Fatalf("pipeline plan events = %d, want 1", len(events))
	}
	want := []string{"code_writer", "test_runner", "acceptance_verifier"}
	if !slices.Equal(events[0].Stages, want) {
		t.Fatalf("planned stages = %v, want %v", events[0].Stages, want)
	}
}

func TestEmitStageEventProducesTypedEventAndMarker(t *testing.T) {
	var got []string
	var events []agent.StageEvent
	options := PipelineConfigFromAgentOptions(agent.Options{
		OnReasoning:  func(s string) { got = append(got, s) },
		OnStageEvent: func(event agent.StageEvent) { events = append(events, event) },
	})

	emitStageEvent(options, "code_writer", "running", "writing files", 50, []string{"main.go"})

	if len(events) != 1 {
		t.Fatalf("expected 1 typed stage event, got %d", len(events))
	}
	if events[0].Name != "code_writer" || events[0].Status != "running" || events[0].Progress != 50 {
		t.Fatalf("typed event = %+v", events[0])
	}
	if len(events[0].ChangedFiles) != 1 || events[0].ChangedFiles[0] != "main.go" {
		t.Fatalf("changed files = %v", events[0].ChangedFiles)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 reasoning marker, got %d", len(got))
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
	options := PipelineConfigFromAgentOptions(agent.Options{})
	emitStageEvent(options, "code_writer", "running", "", 0, nil)
	// Should not panic.
}

type meteredStageFailure struct {
	usage *schemas.StageUsage
}

func (failure meteredStageFailure) Error() string                   { return "typed output exhausted" }
func (failure meteredStageFailure) StageUsage() *schemas.StageUsage { return failure.usage }

type meteredFailingStage struct{}

func (meteredFailingStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (meteredFailingStage) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	return schemas.HarnessStageOutput{}, meteredStageFailure{usage: &schemas.StageUsage{InputTokens: 12, OutputTokens: 7, CachedInputTokens: 3}}
}

type terminalDetailStage struct{}

func (terminalDetailStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (terminalDetailStage) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	return schemas.HarnessStageOutput{}, errors.New(`provider request error: {"detail":"Unsupported parameter: max_output_tokens"}`)
}

func TestRunTerminalStageFailureIncludesOutputSummary(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "test terminal stage failure detail",
		Stages:        []schemas.ExecutionStage{{Name: "terminal_failure"}},
	}
	result, err := runIterationLoop(context.Background(), "run-terminal-failure", plan, stageRegistry{
		"terminal_failure": terminalDetailStage{},
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{MaxTurns: 1}), t.TempDir(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("pipeline status = %q, want failed", result.Status)
	}
	if result.AbortReason == nil {
		t.Fatalf("abort reason = %#v, want terminal stage detail", result.AbortReason)
	}
	reason := *result.AbortReason
	// MaxTurns=1 no longer suppresses stage-failure recovery, so the identical
	// terminal failure is retried once and then reported as repeated. The
	// terminal detail must still be preserved in the abort reason.
	if !strings.Contains(reason, "repeated unchanged stage failure") || !strings.Contains(reason, `provider request error: {"detail":"Unsupported parameter: max_output_tokens"}`) {
		t.Fatalf("abort reason = %q, want repeated unchanged stage failure with terminal detail", reason)
	}
}

type repeatedContextFailureStage struct {
	calls int
	err   error
}

func (*repeatedContextFailureStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (stage *repeatedContextFailureStage) Run(_ context.Context, input schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
	stage.calls++
	if input.Context == nil {
		return schemas.HarnessStageOutput{
			Summary:    "inspect files",
			Confidence: 0.5,
			ContextRequest: &schemas.ContextRequest{
				Reason:  "inspect files",
				Queries: []schemas.ContextQuery{{QueryType: schemas.ContextListFiles, MaxResults: 100, MaxChars: 1000}},
			},
		}, nil
	}
	return schemas.HarnessStageOutput{}, stage.err
}

func TestRunIterationLoopStopsRepeatedIdenticalStageFailure(t *testing.T) {
	stage := &repeatedContextFailureStage{err: errors.New("stream error: auth error: invalid API key")}
	toolCalls := 0
	runner := ToolRunnerFunc(func(context.Context, string, map[string]any) (ToolResult, error) {
		toolCalls++
		return ToolResult{OK: true, Output: "Contents of .:\nmain.go"}, nil
	})
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "reproduce repeated stage failure",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}

	result, err := runIterationLoop(context.Background(), "run-repeated-failure", plan, stageRegistry{
		"code_writer": stage,
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{MaxTurns: 50}), t.TempDir(), runner, nil, nil, nil)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if stage.calls != 4 || toolCalls != 2 {
		t.Fatalf("stage calls = %d, tool calls = %d; want 4/2 before abort", stage.calls, toolCalls)
	}
	if reason := DerefString(result.AbortReason); !strings.Contains(reason, "repeated unchanged stage failure") {
		t.Fatalf("abort reason = %q, want repeated failure", reason)
	}
}

type changingFailureStage struct{ calls int }

func (*changingFailureStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (stage *changingFailureStage) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	stage.calls++
	return schemas.HarnessStageOutput{}, fmt.Errorf("failure %d", stage.calls)
}

func TestRunIterationLoopCapsChangingStageFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		maxTurns int
	}{
		{name: "max turns below pipeline cap", maxTurns: 3},
		{name: "max turns above pipeline cap", maxTurns: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage := &changingFailureStage{}
			plan := schemas.ExecutionPlan{
				Tier:          schemas.TierLight,
				RequestIntent: "bound changing stage failures",
				Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
			}

			result, err := runIterationLoop(context.Background(), "run-changing-failure", plan, stageRegistry{
				"code_writer": stage,
			}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{MaxTurns: tc.maxTurns}), t.TempDir(), nil, nil, nil, nil)
			if err != nil {
				t.Fatalf("runIterationLoop: %v", err)
			}
			if result.Status != "failed" {
				t.Fatalf("status = %q, want failed", result.Status)
			}
			if stage.calls != defaultMaxIterations {
				t.Fatalf("changing failing stage calls = %d, want %d", stage.calls, defaultMaxIterations)
			}
		})
	}
}

func TestRunPassRecordsUsageFromFailedTypedOutput(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "test local typed output failure",
		Stages:        []schemas.ExecutionStage{{Name: "metered_failure"}},
	}
	records, _, completed, err := runPass(context.Background(), "run-metered-failure", 1, plan, stageRegistry{
		"metered_failure": meteredFailingStage{},
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{Model: "qwen-local", ProviderName: "ollama"}), t.TempDir(), nil, time.Time{}, nil, nil, nil)
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
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{OnReasoning: func(s string) { reasoning = append(reasoning, s) }}), workDir, nil, time.Time{}, nil, retriever, nil)
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

func TestRunningStageEventCarriesDescription(t *testing.T) {
	const description = "checking the stage detail"
	var events []agent.StageEvent
	var inputs []schemas.HarnessStageInput
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "test stage detail",
		Stages:        []schemas.ExecutionStage{{Name: "described_stage"}},
	}
	_, _, completed, err := runPass(context.Background(), "run-stage-detail", 1, plan, stageRegistry{
		"described_stage": &capturingStage{inputs: &inputs, caps: stages.Capabilities{Description: description}},
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{
		OnStageEvent: func(event agent.StageEvent) { events = append(events, event) },
	}), t.TempDir(), nil, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass failed: err=%v completed=%v", err, completed)
	}
	if len(events) < 2 {
		t.Fatalf("events = %d, want running and completed", len(events))
	}
	running := events[0]
	if running.Status != "running" || running.Detail != description || running.Progress != 0 {
		t.Fatalf("running event = %+v, want detail %q and progress 0", running, description)
	}
}

func TestRunningStageEventOmitsEmptyDescription(t *testing.T) {
	var events []agent.StageEvent
	var inputs []schemas.HarnessStageInput
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "test empty stage detail",
		Stages:        []schemas.ExecutionStage{{Name: "empty_description_stage"}},
	}
	_, _, completed, err := runPass(context.Background(), "run-empty-stage-detail", 1, plan, stageRegistry{
		"empty_description_stage": &capturingStage{inputs: &inputs},
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{
		OnStageEvent: func(event agent.StageEvent) { events = append(events, event) },
	}), t.TempDir(), nil, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass failed: err=%v completed=%v", err, completed)
	}
	if len(events) < 2 {
		t.Fatalf("events = %d, want running and completed", len(events))
	}
	running := events[0]
	if running.Status != "running" || running.Detail != "" || running.Progress != 0 {
		t.Fatalf("running event = %+v, want empty detail and progress 0", running)
	}
}

func TestModelBackedStageEventNamesModel(t *testing.T) {
	t.Run("model-backed", func(t *testing.T) {
		const description = "writing code changes"
		const model = "claude-opus-5"
		var events []agent.StageEvent
		var inputs []schemas.HarnessStageInput
		plan := schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: "test resolved model detail",
			Stages:        []schemas.ExecutionStage{{Name: "model_stage"}},
		}
		_, _, completed, err := runPass(context.Background(), "run-model-stage-detail", 1, plan, stageRegistry{
			"model_stage": &capturingStage{inputs: &inputs, caps: stages.Capabilities{Description: description}},
		}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{
			StageModelResolver: func(string) (agent.ModelSelection, error) {
				return agent.ModelSelection{Provider: &namedProvider{name: "resolved-provider"}, ProviderName: "resolved-provider", Model: model}, nil
			},
			OnStageEvent: func(event agent.StageEvent) { events = append(events, event) },
		}), t.TempDir(), nil, time.Time{}, nil, nil, nil)
		if err != nil || !completed {
			t.Fatalf("runPass failed: err=%v completed=%v", err, completed)
		}
		if len(events) < 3 {
			t.Fatalf("events = %d, want two running events and completed", len(events))
		}
		if events[0].Status != "running" || events[0].Detail != description || events[0].Progress != 0 {
			t.Fatalf("initial running event = %+v", events[0])
		}
		if events[1].Status != "running" || events[1].Detail != description+" · "+model || events[1].Progress != 0 {
			t.Fatalf("resolved running event = %+v", events[1])
		}
	})

	t.Run("model-free", func(t *testing.T) {
		var events []agent.StageEvent
		var inputs []schemas.HarnessStageInput
		resolverCalls := 0
		plan := schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: "test model-free stage detail",
			Stages:        []schemas.ExecutionStage{{Name: "model_free_stage"}},
		}
		_, _, completed, err := runPass(context.Background(), "run-model-free-stage-detail", 1, plan, stageRegistry{
			"model_free_stage": &capturingStage{inputs: &inputs, caps: stages.Capabilities{ModelFree: true, Description: "running local checks"}},
		}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{
			StageModelResolver: func(string) (agent.ModelSelection, error) {
				resolverCalls++
				return agent.ModelSelection{Provider: &namedProvider{name: "unexpected"}, Model: "unexpected-model"}, nil
			},
			OnStageEvent: func(event agent.StageEvent) { events = append(events, event) },
		}), t.TempDir(), nil, time.Time{}, nil, nil, nil)
		if err != nil || !completed {
			t.Fatalf("runPass failed: err=%v completed=%v", err, completed)
		}
		if resolverCalls != 0 {
			t.Fatalf("resolver calls = %d, want 0", resolverCalls)
		}
		runningCount := 0
		for _, event := range events {
			if event.Status == "running" {
				runningCount++
			}
		}
		if runningCount != 1 {
			t.Fatalf("running event count = %d, want 1: %+v", runningCount, events)
		}
	})

	t.Run("model-backed-empty-description", func(t *testing.T) {
		const model = "claude-opus-5"
		var events []agent.StageEvent
		var inputs []schemas.HarnessStageInput
		plan := schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: "test empty resolved model detail",
			Stages:        []schemas.ExecutionStage{{Name: "empty_model_stage"}},
		}
		_, _, completed, err := runPass(context.Background(), "run-empty-model-stage-detail", 1, plan, stageRegistry{
			"empty_model_stage": &capturingStage{inputs: &inputs, caps: stages.Capabilities{}},
		}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{
			StageModelResolver: func(string) (agent.ModelSelection, error) {
				return agent.ModelSelection{Provider: &namedProvider{name: "resolved-provider"}, Model: model}, nil
			},
			OnStageEvent: func(event agent.StageEvent) { events = append(events, event) },
		}), t.TempDir(), nil, time.Time{}, nil, nil, nil)
		if err != nil || !completed {
			t.Fatalf("runPass failed: err=%v completed=%v", err, completed)
		}
		if len(events) < 3 {
			t.Fatalf("events = %d, want two running events and completed", len(events))
		}
		if events[1].Detail != model {
			t.Fatalf("resolved running detail = %q, want %q", events[1].Detail, model)
		}
	})
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
	}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{OnReasoning: func(s string) { reasoning = append(reasoning, s) }}), workDir, nil, time.Time{}, nil, retriever, nil)
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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
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
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 5}),
		workDir,
		nil,
		nil,
		nil, nil,
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

func (*plateauStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (s *plateauStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	if options.WorkDir != "" {
		if err := os.WriteFile(filepath.Join(options.WorkDir, fmt.Sprintf("plateau-%d.go", s.calls)), []byte("package x\n"), 0o644); err != nil {
			return schemas.HarnessStageOutput{}, err
		}
	}
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

func (*cycleStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
			OverflowPolicy:    "abort",
		},
	}

	cs := &cycleStage{}
	defaultProvider := &namedProvider{name: "default"}
	routedProvider := &namedProvider{name: "routed"}
	escalationProvider := &namedProvider{name: "escalated"}
	resolverCalls := 0
	stageResolverCalls := 0

	escalationResolver := func() (agent.ModelSelection, error) {
		resolverCalls++
		return agent.ModelSelection{Provider: escalationProvider, ProviderName: "escalated-provider", Model: "escalated-model", ReasoningEffort: "high"}, nil
	}
	stageResolver := func(string) (agent.ModelSelection, error) {
		stageResolverCalls++
		return agent.ModelSelection{Provider: routedProvider, ProviderName: "routed-provider", Model: "routed-model"}, nil
	}

	result, err := runIterationLoop(
		context.Background(),
		"escalation-run",
		plan,
		stageRegistry{"code_writer": cs},
		defaultProvider,
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 5, StageModelResolver: stageResolver, EscalationModelResolver: escalationResolver}),
		workDir,
		nil,
		nil,
		nil, nil,
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
	// Iterations 1 and 2 use the routed provider. The cycle fires at
	// iteration 2, so iterations 3+ bypass stage routing and use escalation.
	if len(cs.providers) != 5 {
		t.Fatalf("recorded %d providers, want 5", len(cs.providers))
	}
	if cs.providers[0] != routedProvider {
		t.Fatalf("iteration 1 provider = %p, want routed", cs.providers[0])
	}
	if cs.providers[2] != escalationProvider {
		t.Fatalf("iteration 3 provider = %p, want escalated (provider swap did not take effect)", cs.providers[2])
	}
	if stageResolverCalls != 2 {
		t.Fatalf("stage resolver calls = %d, want 2 before escalation", stageResolverCalls)
	}
	if len(result.Stages) < 3 || result.Stages[2].Provider == nil || *result.Stages[2].Provider != "escalated-provider" || result.Stages[2].Model == nil || *result.Stages[2].Model != "escalated-model" {
		t.Fatalf("iteration 3 record attribution = %+v, want escalated provider/model", result.Stages)
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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
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
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 3}),
		workDir,
		nil,
		nil,
		nil, nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted after cycle with max iterations, got status=%q", result.Status)
	}
	if cs.calls != defaultMaxIterations {
		t.Fatalf("stage calls = %d, want %d at the default pipeline cap", cs.calls, defaultMaxIterations)
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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
			OverflowPolicy:    "abort",
		},
	}

	cs := &cycleStage{}
	errMsg := "simulated resolver error"
	escalationResolver := func() (agent.ModelSelection, error) {
		return agent.ModelSelection{}, fmt.Errorf("%s", errMsg)
	}

	result, err := runIterationLoop(
		context.Background(),
		"escalation-err",
		plan,
		stageRegistry{"code_writer": cs},
		&namedProvider{name: "default"},
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 3, EscalationModelResolver: escalationResolver}),
		workDir,
		nil,
		nil,
		nil, nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted after cycle with max iterations, got status=%q", result.Status)
	}
	if cs.calls != defaultMaxIterations {
		t.Fatalf("stage calls = %d, want %d at the default pipeline cap", cs.calls, defaultMaxIterations)
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
	calls               int
	lastRevisionContext *string
}

func (*surfaceToUserStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (s *surfaceToUserStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	s.lastRevisionContext = input.RevisionContext
	if options.WorkDir != "" {
		if err := os.WriteFile(filepath.Join(options.WorkDir, fmt.Sprintf("call-%d.go", s.calls)), []byte("package x\n"), 0o644); err != nil {
			return schemas.HarnessStageOutput{}, err
		}
	}
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

type noProgressStage struct {
	calls int
}

func (*noProgressStage) Capabilities() stages.Capabilities { return stages.Capabilities{} }

func (s *noProgressStage) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	return schemas.HarnessStageOutput{
		Summary:    fmt.Sprintf("thrash output %d", s.calls),
		Confidence: 0.8,
		Data: map[string]any{
			"code_writer_output": schemas.CodeWriterOutput{
				Files:      []schemas.FileChange{{Path: "main.go", Content: fmt.Sprintf("package main\n// %d\n", s.calls), ChangeType: "create"}},
				Language:   "go",
				Intent:     "create",
				Confidence: 0.8,
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

func TestNoProgressBrakeStepsBackOnceThenAborts(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "no progress thrash",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  100000,
			TotalOutputBudget: 100000,
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
			OverflowPolicy:    "abort",
		},
	}
	stage := &noProgressStage{}
	provider := &stepBackRunFakeProvider{analysis: schemas.StepBackAnalysis{
		HypothesizedRootCause: "no workspace change",
		Evidence:              []string{"empty diff"},
		RecommendedApproach:   "stop thrashing",
		Confidence:            0.8,
	}}
	result, err := runIterationLoop(
		context.Background(),
		"no-progress-run",
		plan,
		stageRegistry{"code_writer": stage},
		provider,
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 5}),
		workDir,
		nil,
		nil,
		nil, nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" {
		t.Fatalf("expected aborted, got status=%q abort_reason=%v", result.Status, DerefString(result.AbortReason))
	}
	if result.AbortReason == nil || !strings.Contains(*result.AbortReason, "abort_no_progress") {
		t.Fatalf("expected abort_no_progress, got %q", DerefString(result.AbortReason))
	}
	if provider.stepBackCallCount != 1 {
		t.Fatalf("step-back count = %d, want 1", provider.stepBackCallCount)
	}
	if stage.calls != 4 {
		t.Fatalf("stage calls = %d, want 4 (step-back at 3, abort at 4)", stage.calls)
	}
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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
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
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 5}),
		workDir,
		nil,
		nil,
		nil, nil,
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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
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
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 5, OnSurfaceToUser: onSurfaceToUser}),
		workDir,
		nil,
		nil,
		nil, nil,
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
	if stage.lastRevisionContext == nil {
		t.Fatal("expected the user guidance in the next iteration revision context, got nil")
	}
	if *stage.lastRevisionContext != "try a different approach: focus on edge cases" {
		t.Fatalf("next iteration revision context = %q, want the user guidance", *stage.lastRevisionContext)
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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
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
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 5, OnSurfaceToUser: onSurfaceToUser}),
		workDir,
		nil,
		nil,
		nil, nil,
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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
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
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 5, OnSurfaceToUser: onSurfaceToUser}),
		workDir,
		nil,
		nil,
		nil, nil,
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
			PerStage:          map[string]schemas.StageBudget{"code_writer": {InputMax: 10000, OutputMax: 10000}},
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
		PipelineConfigFromAgentOptions(agent.Options{Cwd: workDir, MaxTurns: 5, OnSurfaceToUser: onSurfaceToUser}),
		workDir,
		nil,
		nil,
		nil, nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if stage.calls != 3 {
		t.Fatalf("stage calls = %d, want 3", stage.calls)
	}
}

// Request ledger tests.

func TestRequestLedgerRecordsOnceAndPreservesCallbackModes(t *testing.T) {
	ledger := newRequestLedger()
	var attributed []agent.AttributedUsage
	legacyCalls := 0
	options := ledger.recordingOptions(PipelineConfigFromAgentOptions(agent.Options{
		OnUsage: func(agent.Usage) { legacyCalls++ },
		OnAttributedUsage: func(usage agent.AttributedUsage) {
			attributed = append(attributed, usage)
		},
	}))
	options.OnAttributedUsage(agent.AttributedUsage{
		Usage: zeroruntime.Usage{InputTokens: 100, OutputTokens: 50}, UsageReported: true,
		Stage: "code_writer", Iteration: 1,
	})
	if len(ledger.records) != 1 || len(attributed) != 1 || legacyCalls != 0 {
		t.Fatalf("records=%d attributed=%d legacy=%d", len(ledger.records), len(attributed), legacyCalls)
	}
	if attributed[0].Sequence != 1 || ledger.records[0].Sequence != 1 || ledger.records[0].CostStatus != schemas.CostStatusUnpriced {
		t.Fatalf("attributed=%+v record=%+v", attributed[0], ledger.records[0])
	}

	legacyLedger := newRequestLedger()
	legacyOptions := legacyLedger.recordingOptions(PipelineConfigFromAgentOptions(agent.Options{OnUsage: func(agent.Usage) { legacyCalls++ }}))
	legacyOptions.OnAttributedUsage(agent.AttributedUsage{UsageReported: false, Stage: "code_writer", Iteration: 1})
	legacyOptions.OnAttributedUsage(agent.AttributedUsage{Usage: zeroruntime.Usage{InputTokens: 1}, UsageReported: true, Stage: "code_writer", Iteration: 1})
	if legacyCalls != 1 || len(legacyLedger.records) != 2 {
		t.Fatalf("legacy calls=%d records=%d", legacyCalls, len(legacyLedger.records))
	}
}

func TestRequestLedgerRejectsMalformedUsageBeforePricing(t *testing.T) {
	ledger := newRequestLedger()
	estimatorCalls := 0
	var got agent.AttributedUsage
	options := ledger.recordingOptions(PipelineConfigFromAgentOptions(agent.Options{
		EstimateUsageCost: func(string, agent.Usage, bool) agent.UsageCostEstimate {
			estimatorCalls++
			return agent.UsageCostEstimate{}
		},
		OnAttributedUsage: func(usage agent.AttributedUsage) { got = usage },
	}))
	options.OnAttributedUsage(agent.AttributedUsage{
		Usage: zeroruntime.Usage{InputTokens: 10, CachedInputTokens: 11}, UsageReported: true,
		Stage: "code_writer", Iteration: 1,
	})
	if estimatorCalls != 0 || got.Cost.Status != agent.CostStatusError || len(ledger.records) != 1 || ledger.records[0].InputTokens != 0 || ledger.records[0].CostStatus != schemas.CostStatusError {
		t.Fatalf("estimator calls=%d attributed=%+v records=%+v", estimatorCalls, got, ledger.records)
	}
}

// TestRequestLedgerUsesReportedCostOverEstimate proves that a provider-reported
// cost (AttributedUsage.ReportedCostUSD, e.g. from OpenRouter's usage.cost)
// wins over the registry estimate: the estimator is never invoked, the final
// Cost carries the exact reported value with Provenance=CostProvenanceReported,
// and that provenance survives into the persisted pipeline usage record.
func TestRequestLedgerUsesReportedCostOverEstimate(t *testing.T) {
	ledger := newRequestLedger()
	estimatorCalls := 0
	var captured agent.AttributedUsage
	reported := 0.00054
	options := ledger.recordingOptions(PipelineConfigFromAgentOptions(agent.Options{
		EstimateUsageCost: func(string, agent.Usage, bool) agent.UsageCostEstimate {
			estimatorCalls++
			// A deliberately different value: if this ever leaks into the
			// result, the test fails on the wrong number rather than silently.
			wrong := 99.0
			return agent.UsageCostEstimate{
				CostUSD: &wrong, Status: agent.CostStatusPriced,
				Provenance:    agent.CostProvenanceRuntimeEstimate,
				PricingSource: "registry", PricingAsOf: "2026-01-01",
			}
		},
		OnAttributedUsage: func(usage agent.AttributedUsage) { captured = usage },
	}))
	options.OnAttributedUsage(agent.AttributedUsage{
		Usage:           zeroruntime.Usage{InputTokens: 100, OutputTokens: 50},
		UsageReported:   true,
		Stage:           "code_writer",
		Iteration:       1,
		ReportedCostUSD: &reported,
	})

	if estimatorCalls != 0 {
		t.Fatalf("estimator calls = %d, want 0 (reported cost must bypass the registry estimate)", estimatorCalls)
	}
	if captured.Cost.CostUSD == nil || *captured.Cost.CostUSD != reported {
		t.Fatalf("Cost.CostUSD = %v, want %v", captured.Cost.CostUSD, reported)
	}
	if captured.Cost.Status != agent.CostStatusPriced {
		t.Fatalf("Cost.Status = %s, want priced", captured.Cost.Status)
	}
	if captured.Cost.Provenance != agent.CostProvenanceReported {
		t.Fatalf("Cost.Provenance = %s, want %s", captured.Cost.Provenance, agent.CostProvenanceReported)
	}
	if len(ledger.records) != 1 {
		t.Fatalf("records = %d, want 1", len(ledger.records))
	}
	rec := ledger.records[0]
	if rec.CostUSD == nil || *rec.CostUSD != reported {
		t.Fatalf("record CostUSD = %v, want %v", rec.CostUSD, reported)
	}
	if rec.CostProvenance != agent.CostProvenanceReported {
		t.Fatalf("record CostProvenance = %s, want %s", rec.CostProvenance, agent.CostProvenanceReported)
	}
}

// TestRequestLedgerFallsBackToEstimateWithoutReportedCost proves the inverse:
// when ReportedCostUSD is nil (every non-OpenRouter provider, and OpenRouter
// responses that omit "cost"), behavior is unchanged from before this
// feature — the registry estimator runs and its value is used verbatim.
func TestRequestLedgerFallsBackToEstimateWithoutReportedCost(t *testing.T) {
	ledger := newRequestLedger()
	estimatorCalls := 0
	estimated := 0.42
	var captured agent.AttributedUsage
	options := ledger.recordingOptions(PipelineConfigFromAgentOptions(agent.Options{
		EstimateUsageCost: func(string, agent.Usage, bool) agent.UsageCostEstimate {
			estimatorCalls++
			return agent.UsageCostEstimate{
				CostUSD: &estimated, Status: agent.CostStatusPriced,
				Provenance:    agent.CostProvenanceRuntimeEstimate,
				PricingSource: "registry", PricingAsOf: "2026-01-01",
			}
		},
		OnAttributedUsage: func(usage agent.AttributedUsage) { captured = usage },
	}))
	options.OnAttributedUsage(agent.AttributedUsage{
		Usage:         zeroruntime.Usage{InputTokens: 100, OutputTokens: 50},
		UsageReported: true,
		Stage:         "code_writer",
		Iteration:     1,
		// ReportedCostUSD deliberately left nil.
	})

	if estimatorCalls != 1 {
		t.Fatalf("estimator calls = %d, want 1", estimatorCalls)
	}
	if captured.Cost.CostUSD == nil || *captured.Cost.CostUSD != estimated {
		t.Fatalf("Cost.CostUSD = %v, want %v", captured.Cost.CostUSD, estimated)
	}
	if captured.Cost.Provenance != agent.CostProvenanceRuntimeEstimate {
		t.Fatalf("Cost.Provenance = %s, want %s", captured.Cost.Provenance, agent.CostProvenanceRuntimeEstimate)
	}
}

func TestRequestLedgerRecordingOptionsCases(t *testing.T) {
	zero := 0.0
	tests := []struct {
		name      string
		estimator func(string, agent.Usage, bool) agent.UsageCostEstimate
		usages    []agent.AttributedUsage
		check     func(*testing.T, *requestLedger, []agent.AttributedUsage)
	}{
		{
			name: "missing usage is unpriced",
			usages: []agent.AttributedUsage{{
				UsageReported: false, Stage: "test_runner", Iteration: 1,
			}},
			check: func(t *testing.T, ledger *requestLedger, _ []agent.AttributedUsage) {
				if len(ledger.records) != 1 {
					t.Fatalf("records = %d, want 1", len(ledger.records))
				}
				rec := ledger.records[0]
				if rec.CostStatus != schemas.CostStatusUnpriced {
					t.Fatalf("cost_status = %s, want unpriced", rec.CostStatus)
				}
				if rec.UsageReported {
					t.Fatal("expected usage_reported=false")
				}
				if rec.InputTokens != 0 || rec.OutputTokens != 0 {
					t.Fatal("expected zero tokens for missing usage")
				}
			},
		},
		{
			name: "preserves priced zero",
			estimator: func(string, agent.Usage, bool) agent.UsageCostEstimate {
				return agent.UsageCostEstimate{
					CostUSD: &zero, Status: schemas.CostStatusPriced,
					Provenance:    agent.CostProvenanceRuntimeEstimate,
					PricingSource: "test", PricingAsOf: "2026-01-01",
				}
			},
			usages: []agent.AttributedUsage{{
				Usage:         zeroruntime.Usage{InputTokens: 50, OutputTokens: 10},
				UsageReported: true, Stage: "code_writer", Iteration: 1,
			}},
			check: func(t *testing.T, ledger *requestLedger, captured []agent.AttributedUsage) {
				if captured[0].Cost.CostUSD == nil || *captured[0].Cost.CostUSD != 0 {
					t.Fatalf("expected priced zero, got %+v", captured[0].Cost)
				}
				if ledger.records[0].CostStatus != schemas.CostStatusPriced {
					t.Fatalf("expected priced status, got %s", ledger.records[0].CostStatus)
				}
			},
		},
		{
			name: "prices routed models independently",
			estimator: func(model string, u agent.Usage, reported bool) agent.UsageCostEstimate {
				if !reported {
					return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "not reported"}
				}
				var inputRate float64
				switch model {
				case "model-a":
					inputRate = 0.01
				case "model-b":
					inputRate = 0.001
				default:
					return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "unknown"}
				}
				cost := float64(u.EffectiveInputTokens()) * inputRate
				return agent.UsageCostEstimate{
					CostUSD: &cost, Status: schemas.CostStatusPriced,
					Provenance:    agent.CostProvenanceRuntimeEstimate,
					PricingSource: "test", PricingAsOf: "2026-01-01",
				}
			},
			usages: []agent.AttributedUsage{
				{Usage: zeroruntime.Usage{InputTokens: 100}, UsageReported: true, Stage: "code_writer", Iteration: 1, Model: "model-a"},
				{Usage: zeroruntime.Usage{InputTokens: 100}, UsageReported: true, Stage: "test_generator", Iteration: 1, Model: "model-b"},
			},
			check: func(t *testing.T, ledger *requestLedger, _ []agent.AttributedUsage) {
				if len(ledger.records) != 2 {
					t.Fatalf("records = %d, want 2", len(ledger.records))
				}
				if ledger.records[0].CostUSD == nil || *ledger.records[0].CostUSD != 1.0 {
					t.Fatalf("model-a cost = %v, want $1.00", ledger.records[0].CostUSD)
				}
				if ledger.records[1].CostUSD == nil || *ledger.records[1].CostUSD != 0.1 {
					t.Fatalf("model-b cost = %v, want $0.10", ledger.records[1].CostUSD)
				}
			},
		},
		{
			name: "does not add reasoning to output",
			usages: []agent.AttributedUsage{{
				Usage:         zeroruntime.Usage{InputTokens: 100, OutputTokens: 60, ReasoningTokens: 30},
				UsageReported: true, Stage: "code_writer", Iteration: 1,
			}},
			check: func(t *testing.T, ledger *requestLedger, _ []agent.AttributedUsage) {
				rec := ledger.records[0]
				if rec.OutputTokens != 60 {
					t.Fatalf("output = %d, want 60", rec.OutputTokens)
				}
				if rec.Reasoning != 30 {
					t.Fatalf("reasoning = %d, want 30", rec.Reasoning)
				}
				result := schemas.PipelineResult{
					RunID: "test", Status: "completed", Tier: schemas.TierLight,
					Stages: []schemas.StageRecord{{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1}},
				}
				applyRequestLedger(&result, ledger)
				if result.TotalTokensOutput != 60 {
					t.Fatalf("total output = %d, want 60", result.TotalTokensOutput)
				}
				if result.TotalTokensReasoning != 30 {
					t.Fatalf("total reasoning = %d, want 30", result.TotalTokensReasoning)
				}
			},
		},
		{
			name: "includes step back only in pipeline totals",
			usages: []agent.AttributedUsage{
				{Usage: zeroruntime.Usage{InputTokens: 100, OutputTokens: 50}, UsageReported: true, Stage: "code_writer", Iteration: 1},
				{Usage: zeroruntime.Usage{InputTokens: 20, OutputTokens: 10}, UsageReported: true, Stage: "step_back", Iteration: 1},
				{Usage: zeroruntime.Usage{InputTokens: 110, OutputTokens: 55}, UsageReported: true, Stage: "code_writer", Iteration: 2},
			},
			check: func(t *testing.T, ledger *requestLedger, _ []agent.AttributedUsage) {
				result := schemas.PipelineResult{
					RunID: "test", Status: "completed", Tier: schemas.TierLight,
					Stages: []schemas.StageRecord{
						{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1},
						{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 2},
					},
				}
				applyRequestLedger(&result, ledger)
				if result.TotalTokensInput != 230 {
					t.Fatalf("total input = %d, want 230 (including step_back)", result.TotalTokensInput)
				}
				if result.TotalTokensOutput != 115 {
					t.Fatalf("total output = %d, want 115", result.TotalTokensOutput)
				}
				for _, s := range result.Stages {
					if s.Name == "step_back" {
						t.Fatal("step_back should not be a stage record")
					}
				}
			},
		},
		{
			name: "groups context retry by stage",
			usages: []agent.AttributedUsage{
				{Usage: zeroruntime.Usage{InputTokens: 4, OutputTokens: 3, CachedInputTokens: 1, CacheWriteTokens: 1, ReasoningTokens: 1}, UsageReported: true, Stage: "code_writer", Iteration: 1},
				{Usage: zeroruntime.Usage{InputTokens: 6, OutputTokens: 5, CachedInputTokens: 2, CacheWriteTokens: 1, ReasoningTokens: 2}, UsageReported: true, Stage: "code_writer", Iteration: 1},
			},
			check: func(t *testing.T, ledger *requestLedger, _ []agent.AttributedUsage) {
				if len(ledger.records) != 2 {
					t.Fatalf("records = %d, want 2", len(ledger.records))
				}
				result := schemas.PipelineResult{
					RunID: "test", Status: "completed", Tier: schemas.TierLight,
					Stages: []schemas.StageRecord{{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1}},
				}
				applyRequestLedger(&result, ledger)
				if result.Stages[0].TokensInput != 10 {
					t.Fatalf("stage input = %d, want 10", result.Stages[0].TokensInput)
				}
				if result.Stages[0].TokensOutput != 8 {
					t.Fatalf("stage output = %d, want 8", result.Stages[0].TokensOutput)
				}
				if result.TotalTokensInput != 10 {
					t.Fatalf("total input = %d, want 10", result.TotalTokensInput)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ledger := newRequestLedger()
			var captured []agent.AttributedUsage
			options := ledger.recordingOptions(PipelineConfigFromAgentOptions(agent.Options{
				EstimateUsageCost: tc.estimator,
				OnAttributedUsage: func(usage agent.AttributedUsage) { captured = append(captured, usage) },
			}))
			for _, usage := range tc.usages {
				options.OnAttributedUsage(usage)
			}
			tc.check(t, ledger, captured)
		})
	}
}

func TestRequestLedgerDerivesCoverageStates(t *testing.T) {
	makeLedger := func(records ...schemas.PipelineUsageRecord) *requestLedger {
		l := newRequestLedger()
		for _, r := range records {
			l.append(r)
		}
		return l
	}
	makeResult := func() schemas.PipelineResult {
		return schemas.PipelineResult{
			RunID: "test", Status: "completed", Tier: schemas.TierLight,
			Stages: []schemas.StageRecord{{Name: "s", Status: schemas.StageCompleted}},
		}
	}
	zero := 0.0
	priced := schemas.PipelineUsageRecord{
		Sequence: 1, Stage: "s", Iteration: 1, UsageReported: true,
		InputTokens: 10, OutputTokens: 5,
		CostStatus: schemas.CostStatusPriced, CostUSD: &zero,
		CostProvenance: "runtime_estimate", PricingSource: "test", PricingAsOf: "2026-01-01",
	}
	unpriced := schemas.PipelineUsageRecord{
		Sequence: 2, Stage: "s", Iteration: 1, CostStatus: schemas.CostStatusUnpriced, UnpricedReason: "no model",
	}
	errRec := schemas.PipelineUsageRecord{
		Sequence: 3, Stage: "s", Iteration: 1, UsageReported: true, InputTokens: 10, OutputTokens: 5,
		CostStatus: schemas.CostStatusError, UnpricedReason: "malformed",
	}
	u := unpriced
	u.Sequence = 1
	e := errRec
	e.Sequence = 2
	tests := []struct {
		name         string
		records      []schemas.PipelineUsageRecord
		wantCoverage string
		checkCounts  func(*testing.T, schemas.PipelineResult)
	}{
		{name: "not-applicable", wantCoverage: schemas.CostCoverageNotApplicable},
		{
			name: "complete", records: []schemas.PipelineUsageRecord{priced},
			wantCoverage: schemas.CostCoverageComplete,
			checkCounts: func(t *testing.T, result schemas.PipelineResult) {
				if result.PricedRequestCount != 1 || result.UnpricedRequestCount != 0 {
					t.Fatalf("complete counts: priced=%d unpriced=%d", result.PricedRequestCount, result.UnpricedRequestCount)
				}
			},
		},
		{name: "partial", records: []schemas.PipelineUsageRecord{priced, unpriced}, wantCoverage: schemas.CostCoveragePartial},
		{
			name: "unavailable", records: []schemas.PipelineUsageRecord{u, e},
			wantCoverage: schemas.CostCoverageUnavailable,
			checkCounts: func(t *testing.T, result schemas.PipelineResult) {
				if result.ErrorRequestCount != 1 || result.UnpricedRequestCount != 1 {
					t.Fatalf("unavailable counts: error=%d unpriced=%d", result.ErrorRequestCount, result.UnpricedRequestCount)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := makeResult()
			applyRequestLedger(&result, makeLedger(tc.records...))
			if result.CostCoverage != tc.wantCoverage {
				t.Fatalf("coverage = %s, want %s", result.CostCoverage, tc.wantCoverage)
			}
			if tc.checkCounts != nil {
				tc.checkCounts(t, result)
			}
		})
	}
}

// TestRunPassThreadsStageOutputBudgetToCompletionRequest proves an
// ExecutionStage output budget lands as the CompletionRequest output cap.
func TestRunPassThreadsStageOutputBudgetToCompletionRequest(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "write code",
		Stages: []schemas.ExecutionStage{{
			Name:   "code_writer",
			Budget: schemas.StageBudget{InputMax: 1000, OutputMax: 8192},
		}},
	}
	provider := &captureRequestProvider{}
	fakeRunner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: ""}, nil
	})

	_, _, completed, err := runPass(context.Background(), "run-budget-8192", 1, plan, stageRegistry{
		"code_writer": stages.CodeWriter{},
	}, provider, PipelineConfigFromAgentOptions(agent.Options{}), workDir, fakeRunner, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	if provider.request.MaxOutputTokens != 8192 {
		t.Fatalf("request.MaxOutputTokens = %d, want stage budget 8192", provider.request.MaxOutputTokens)
	}
}

// TestRunPassZeroOutputBudgetSendsNoOverride proves OutputMax=0 (model-free
// stages, or a stage without a budget) leaves the request cap at zero.
func TestRunPassZeroOutputBudgetSendsNoOverride(t *testing.T) {
	workDir := t.TempDir()
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "write code",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	provider := &captureRequestProvider{}
	fakeRunner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: ""}, nil
	})

	_, _, completed, err := runPass(context.Background(), "run-budget-zero", 1, plan, stageRegistry{
		"code_writer": stages.CodeWriter{},
	}, provider, PipelineConfigFromAgentOptions(agent.Options{}), workDir, fakeRunner, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	if provider.request.MaxOutputTokens != 0 {
		t.Fatalf("request.MaxOutputTokens = %d, want 0 (no override)", provider.request.MaxOutputTokens)
	}
}

func TestPipelineDisablesBashContextFulfillment(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	var results []agent.ToolResult
	runner := newAgentToolRunner(PipelineConfigFromAgentOptions(agent.Options{
		Cwd:           workDir,
		Registry:      registry,
		DisabledTools: []string{"bash"},
		OnToolResult: func(result agent.ToolResult) {
			results = append(results, result)
		},
	}), workDir)

	res, err := runner.RunTool(context.Background(), "bash", map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("RunTool error: %v", err)
	}
	if res.OK {
		t.Fatal("disabled bash unexpectedly succeeded")
	}
	if res.DenialReason != agent.DenialFiltered {
		t.Fatalf("DenialReason = %q, want %q; output=%q", res.DenialReason, agent.DenialFiltered, res.Output)
	}
	if !strings.Contains(res.Output, `Tool "bash" is not enabled`) {
		t.Fatalf("output = %q, want filter denial", res.Output)
	}
	if len(results) != 1 || results[0].DenialReason != agent.DenialFiltered || results[0].Name != "bash" {
		t.Fatalf("OnToolResult missing attributed bash filter denial: %+v", results)
	}

	py := filepath.Join(workDir, "broken.py")
	if err := os.WriteFile(py, []byte("def broken(\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	analyzer, err := stages.NewStaticAnalyzer(stages.DefaultQualityChecks()...)
	if err != nil {
		t.Fatalf("analyzer: %v", err)
	}
	output, err := analyzer.Run(context.Background(), schemas.HarnessStageInput{
		RunID:         "run-sd12-bash",
		StageName:     "static_analyzer",
		Sequence:      1,
		PlanTier:      schemas.TierStandard,
		RequestIntent: "check python",
	}, nil, stages.StageOptions{
		WorkDir:  workDir,
		Language: "python",
		RunTool: func(ctx context.Context, name string, args map[string]any) (stages.ToolResult, error) {
			res, err := runner.RunTool(ctx, name, args)
			return stages.ToolResult{OK: res.OK, Output: res.Output, Truncated: res.Truncated, Meta: res.Meta}, err
		},
	})
	if err != nil {
		t.Fatalf("static analyzer: %v", err)
	}
	report, ok := output.Data["static_analyzer_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("missing report, got %T", output.Data["static_analyzer_output"])
	}
	if report.Status == schemas.VerificationPassed {
		t.Fatal("quality check passed despite disabled bash")
	}
	found := false
	for _, f := range report.Findings {
		if f.RuleID == "PY_COMPILE" {
			found = true
			if !strings.Contains(f.Message, `Tool "bash" is not enabled`) && !strings.Contains(f.Message, "not enabled for this run") {
				t.Fatalf("PY_COMPILE message = %q, want bash filter denial", f.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected PY_COMPILE finding from disabled bash, got status=%q summary=%q findings=%+v", report.Status, report.Summary, report.Findings)
	}
}

func TestPipelineBeforeToolHookFires(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	source := filepath.Join(workDir, "a.go")
	if err := os.WriteFile(source, []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	audit, err := hooks.NewAuditStore(hooks.AuditStoreOptions{
		AuditPath: filepath.Join(t.TempDir(), "hooks-audit.jsonl"),
	})
	if err != nil {
		t.Fatalf("audit store: %v", err)
	}
	dispatcher := hooks.NewDispatcher(hooks.DispatcherOptions{
		Config: hooks.Config{
			Enabled: true,
			Hooks: []hooks.Definition{{
				ID:      "sd12-before-read",
				Event:   hooks.EventBeforeTool,
				Matcher: "read_file",
				Command: "true",
				Enabled: true,
			}},
		},
		Audit: audit,
		Cwd:   workDir,
	})
	runner := newAgentToolRunner(PipelineConfigFromAgentOptions(agent.Options{
		Cwd:      workDir,
		Registry: registry,
		Hooks:    dispatcher,
	}), workDir)
	res, err := runner.RunTool(context.Background(), "read_file", map[string]any{"path": source})
	if err != nil {
		t.Fatalf("RunTool error: %v", err)
	}
	if !res.OK {
		t.Fatalf("read_file failed: %s", res.Output)
	}
	events, err := audit.ReadEvents()
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	started := false
	for _, event := range events {
		if event.HookID == "sd12-before-read" && event.Event == hooks.EventBeforeTool {
			started = true
			break
		}
	}
	if !started {
		t.Fatalf("beforeTool hook did not fire; audit=%+v", events)
	}
}

func TestPipelineAutoGrantPermissionEventIsGranted(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(workDir))
	var events []agent.PermissionEvent
	runner := newAgentToolRunner(PipelineConfigFromAgentOptions(agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		OnPermission: func(event agent.PermissionEvent) {
			events = append(events, event)
		},
	}), workDir)
	res, err := runner.RunTool(context.Background(), "write_file", map[string]any{"path": "b.go", "content": "package x\n"})
	if err != nil {
		t.Fatalf("RunTool: %v", err)
	}
	if !res.OK {
		t.Fatalf("write_file failed: %s", res.Output)
	}
	if len(events) == 0 {
		t.Fatal("expected an auto-grant permission event")
	}
	last := events[len(events)-1]
	if last.Action != agent.PermissionActionAllow || !last.PermissionGranted {
		t.Fatalf("auto-grant event = action=%s granted=%v, want allow/true", last.Action, last.PermissionGranted)
	}
}

// repairTestPlan is a two-stage plan (code_writer, test_runner) with a valid
// token budget so the repair loop can re-enter code_writer.
func repairTestPlan() schemas.ExecutionPlan {
	return schemas.ExecutionPlan{
		Tier:          schemas.TierStandard,
		RequestIntent: "implement the feature",
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 1000, OutputMax: 1000}},
			{Name: "test_runner", Budget: schemas.StageBudget{}},
		},
		TokenBudget: schemas.TokenBudget{TotalInputBudget: 10000, TotalOutputBudget: 10000, OverflowPolicy: "abort"},
	}
}

func repairCodeWriterOutput() schemas.HarnessStageOutput {
	return schemas.HarnessStageOutput{
		Summary:    "wrote implementation",
		Confidence: 0.9,
		Usage:      &schemas.StageUsage{InputTokens: 100, OutputTokens: 50},
		Data:       map[string]any{"code_writer_output": schemas.CodeWriterOutput{Files: []schemas.FileChange{{Path: "main.go", ChangeType: "create"}}}},
	}
}

func repairTestResults(status string) schemas.HarnessStageOutput {
	return schemas.HarnessStageOutput{
		Summary:    "tests run",
		Confidence: 0.8,
		Data: map[string]any{"test_results": schemas.TestRunResults{
			Command: []string{"go", "test"},
			Tests:   []schemas.TestCaseResult{{Name: "TestAdd", Status: status, Message: "assertion failed"}},
		}},
	}
}

func TestRunPassRepairsFailingTests(t *testing.T) {
	plan := repairTestPlan()
	writerCalls := 0
	testCalls := 0
	registry := stageRegistry{
		"code_writer": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			writerCalls++
			return repairCodeWriterOutput(), nil
		}),
		"test_runner": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			testCalls++
			status := "failed"
			if testCalls >= 2 {
				status = "passed"
			}
			return repairTestResults(status), nil
		}),
	}

	records, _, completed, err := runPass(context.Background(), "run-repair", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	if writerCalls != 2 || testCalls != 2 {
		t.Fatalf("calls = code_writer %d test_runner %d, want 2/2", writerCalls, testCalls)
	}

	// The landmine: a SECOND code_writer record for the iteration makes
	// applyRequestLedger error. The repair must merge, never append.
	var writerRecords, runnerRecords int
	for _, rec := range records {
		switch rec.Name {
		case "code_writer":
			writerRecords++
			if rec.TokensInput != 200 || rec.TokensOutput != 100 {
				t.Fatalf("merged code_writer usage = %d/%d, want 200/100", rec.TokensInput, rec.TokensOutput)
			}
		case "test_runner":
			runnerRecords++
		}
	}
	if writerRecords != 1 || runnerRecords != 1 {
		t.Fatalf("records = %#v, want exactly one code_writer and one test_runner record", records)
	}
}

func TestRunPassRepairRevisionContextNamesFailingTest(t *testing.T) {
	plan := repairTestPlan()
	var writerInputs []schemas.HarnessStageInput
	testCalls := 0
	registry := stageRegistry{
		"code_writer": stageFunc(func(_ context.Context, input schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
			writerInputs = append(writerInputs, input)
			return repairCodeWriterOutput(), nil
		}),
		"test_runner": stageFunc(func(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
			testCalls++
			status := "failed"
			if testCalls >= 2 {
				status = "passed"
			}
			return repairTestResults(status), nil
		}),
	}

	if _, _, _, err := runPass(context.Background(), "run-repair-ctx", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Time{}, nil, nil, nil); err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if len(writerInputs) != 2 {
		t.Fatalf("code_writer inputs = %d, want 2 (initial + repair)", len(writerInputs))
	}
	rev := writerInputs[1].RevisionContext
	if rev == nil || !strings.Contains(*rev, "TestAdd") {
		t.Fatalf("repair RevisionContext = %#v, want it to name the failing test", rev)
	}
}

func TestRunPassRepairCapsAtTwo(t *testing.T) {
	plan := repairTestPlan()
	writerCalls := 0
	testCalls := 0
	registry := stageRegistry{
		"code_writer": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			writerCalls++
			return repairCodeWriterOutput(), nil
		}),
		"test_runner": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			testCalls++
			return repairTestResults("failed"), nil // always failing
		}),
	}

	records, _, completed, err := runPass(context.Background(), "run-repair-cap", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Time{}, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	if writerCalls != 3 || testCalls != 3 {
		t.Fatalf("calls = code_writer %d test_runner %d, want 3/3 (initial + 2 capped repairs)", writerCalls, testCalls)
	}
	var writerRecords, runnerRecords int
	for _, rec := range records {
		switch rec.Name {
		case "code_writer":
			writerRecords++
		case "test_runner":
			runnerRecords++
		}
	}
	if writerRecords != 1 || runnerRecords != 1 {
		t.Fatalf("records = %#v, want one record each after capped repairs", records)
	}
}

func TestRunPassNoRepairWhenTestsPass(t *testing.T) {
	plan := repairTestPlan()
	writerCalls := 0
	registry := stageRegistry{
		"code_writer": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			writerCalls++
			return repairCodeWriterOutput(), nil
		}),
		"test_runner": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			return repairTestResults("passed"), nil
		}),
	}

	if _, _, _, err := runPass(context.Background(), "run-repair-pass", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Time{}, nil, nil, nil); err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if writerCalls != 1 {
		t.Fatalf("code_writer calls = %d, want 1 (no repair when tests pass)", writerCalls)
	}
}

func TestRunPassNoRepairWhenTestResultsAbsent(t *testing.T) {
	plan := repairTestPlan()
	writerCalls := 0
	registry := stageRegistry{
		"code_writer": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			writerCalls++
			return repairCodeWriterOutput(), nil
		}),
		"test_runner": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			return schemas.HarnessStageOutput{Summary: "no test results", Confidence: 1}, nil
		}),
	}

	if _, _, _, err := runPass(context.Background(), "run-repair-absent", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Time{}, nil, nil, nil); err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if writerCalls != 1 {
		t.Fatalf("code_writer calls = %d, want 1 (no repair when test_results absent)", writerCalls)
	}
}

// repairTraceRegistry builds a code_writer + test_runner registry whose
// test_runner status flips to passed on its second call, exercising one
// successful repair.
func repairTraceRegistry(writerCalls, testCalls *int) stageRegistry {
	return stageRegistry{
		"code_writer": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			*writerCalls++
			return repairCodeWriterOutput(), nil
		}),
		"test_runner": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			*testCalls++
			status := "failed"
			if *testCalls >= 2 {
				status = "passed"
			}
			return repairTestResults(status), nil
		}),
	}
}

func TestRunRepairPersistsInteraction(t *testing.T) {
	plan := repairTestPlan()
	store := &recordingTraceStore{}
	tr := newRunTraceAccumulator(store, "run-interaction", "sess-1", "/repo", plan, "active", nil)
	var writerCalls, testCalls int

	records, _, completed, err := runPass(context.Background(), "run-interaction", 1, plan, repairTraceRegistry(&writerCalls, &testCalls), runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Time{}, nil, nil, tr)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}

	outcome, err := tr.buildOutcome(records, "completed", "")
	if err != nil {
		t.Fatalf("buildOutcome: %v", err)
	}
	if len(outcome.Interactions) != 1 {
		t.Fatalf("Interactions = %d, want 1", len(outcome.Interactions))
	}
	interaction := outcome.Interactions[0]
	if interaction.Message.Kind != schemas.MessageKindRevisionRequest {
		t.Fatalf("Interaction.Message.Kind = %q, want revision_request", interaction.Message.Kind)
	}
	if !interaction.Resolved {
		t.Fatal("Interaction.Resolved = false, want true")
	}
	if interaction.Repairs < 1 {
		t.Fatalf("Interaction.Repairs = %d, want >= 1", interaction.Repairs)
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("outcome invalid: %v", err)
	}
}

func TestRunNoRepairLeavesInteractionsEmpty(t *testing.T) {
	plan := repairTestPlan()
	store := &recordingTraceStore{}
	tr := newRunTraceAccumulator(store, "run-nointeraction", "sess-1", "/repo", plan, "active", nil)
	var writerCalls int
	registry := stageRegistry{
		"code_writer": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			writerCalls++
			return repairCodeWriterOutput(), nil
		}),
		"test_runner": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			return repairTestResults("passed"), nil
		}),
	}

	records, _, _, err := runPass(context.Background(), "run-nointeraction", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Time{}, nil, nil, tr)
	if err != nil {
		t.Fatalf("runPass: %v", err)
	}
	outcome, err := tr.buildOutcome(records, "completed", "")
	if err != nil {
		t.Fatalf("buildOutcome: %v", err)
	}
	if len(outcome.Interactions) != 0 {
		t.Fatalf("Interactions = %d, want 0", len(outcome.Interactions))
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("outcome invalid: %v", err)
	}
}

func TestRunRepairExhaustedInteractionUnresolved(t *testing.T) {
	plan := repairTestPlan()
	store := &recordingTraceStore{}
	tr := newRunTraceAccumulator(store, "run-exhausted", "sess-1", "/repo", plan, "active", nil)
	registry := stageRegistry{
		"code_writer": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			return repairCodeWriterOutput(), nil
		}),
		"test_runner": stageFunc(func(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
			return repairTestResults("failed"), nil // always failing
		}),
	}

	records, _, completed, err := runPass(context.Background(), "run-exhausted", 1, plan, registry, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), t.TempDir(), nil, time.Time{}, nil, nil, tr)
	if err != nil || !completed {
		t.Fatalf("runPass: completed=%v err=%v", completed, err)
	}
	outcome, err := tr.buildOutcome(records, "completed", "")
	if err != nil {
		t.Fatalf("buildOutcome: %v", err)
	}
	if len(outcome.Interactions) != 1 {
		t.Fatalf("Interactions = %d, want 1", len(outcome.Interactions))
	}
	if outcome.Interactions[0].Resolved {
		t.Fatal("Resolved = true, want false (exhausted)")
	}
	if outcome.Interactions[0].Repairs != maxLocalRepairs {
		t.Fatalf("Repairs = %d, want %d", outcome.Interactions[0].Repairs, maxLocalRepairs)
	}
}

// TestBuildRevisionContextCarriesFailureEvidence pins the repair-loop
// transport: a failed test_runner record's OutputSummary — which now embeds
// the bounded failing-evidence excerpt — must reach the revision context the
// re-entered code_writer receives, verbatim.
func TestBuildRevisionContextCarriesFailureEvidence(t *testing.T) {
	summary := "Test command failed with exit code 1.\nFailing evidence:\n--- FAIL: TestAuditTombstone (0.00s)\n    audit_test.go:20: last audit action = \"delete\", want tombstone"
	records := []schemas.StageRecord{{
		Name:          "test_runner",
		Status:        schemas.StageFailed,
		OutputSummary: &summary,
	}}
	got := buildRevisionContext("fix the service", nil, records, nil, "")
	for _, want := range []string{"--- FAIL: TestAuditTombstone", `last audit action = "delete", want tombstone`, "test_runner:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("revision context missing %q:\n%s", want, got)
		}
	}
}

// engineRootProbeTool records the workspace root and granted write roots of
// whatever engine reaches RunWithOptions, so tests can pin the engine
// instance at the tool boundary.
type engineRootProbeTool struct {
	recordedRoot  *string
	recordedRoots *[]string
}

func (t engineRootProbeTool) Name() string        { return "engine_root_probe" }
func (t engineRootProbeTool) Description() string { return "records the sandbox engine root" }
func (t engineRootProbeTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object"}
}
func (t engineRootProbeTool) Safety() tools.Safety {
	return tools.Safety{Permission: tools.PermissionAllow}
}
func (t engineRootProbeTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK}
}
func (t engineRootProbeTool) RunWithOptions(ctx context.Context, args map[string]any, options tools.RunOptions) tools.Result {
	if options.Sandbox != nil {
		*t.recordedRoot = options.Sandbox.WorkspaceRoot()
		*t.recordedRoots = options.Sandbox.Scope().Roots()
	} else {
		*t.recordedRoot = ""
		*t.recordedRoots = nil
	}
	return tools.Result{Status: tools.StatusOK}
}

// TestPipelineStageToolsUseRunScopedEngine pins the plan-preparation half of
// the /exec worktree fix: the engine handed to stage tool execution must be
// rooted at the run workdir, not the session workspace. A session-rooted
// engine reaching procrun.Prepare refuses the worktree cwd before any command
// runs. Add-dir style grants on the caller engine must survive the re-root.
func TestPipelineStageToolsUseRunScopedEngine(t *testing.T) {
	base := t.TempDir()
	sessionRoot := filepath.Join(base, "session")
	worktree := filepath.Join(base, "worktree")
	for _, dir := range []string{sessionRoot, worktree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	sessionEngine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: sessionRoot,
		Policy:        sandbox.DefaultPolicy(),
	})

	// Grant an add-dir style extra write root on the session engine; the
	// re-rooted engine must carry it so --add-dir grants survive worktree
	// binding. The grant must live outside the default temp write roots:
	// every t.TempDir path sits under one on macOS, and Scope.Add silently
	// treats covered roots as already granted.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("user home unavailable: %v", err)
	}
	extraRoot, err := os.MkdirTemp(filepath.Join(home, ".cache"), "splice-reroot-test-")
	if err != nil {
		t.Skipf("home cache dir unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(extraRoot) })
	if _, err := sessionEngine.Scope().Add(extraRoot); err != nil {
		t.Fatalf("scope add: %v", err)
	}

	var recordedRoot string
	var recordedRoots []string
	registry := tools.NewRegistry()
	registry.Register(engineRootProbeTool{recordedRoot: &recordedRoot, recordedRoots: &recordedRoots})

	config := PipelineConfigFromAgentOptions(agent.Options{
		Cwd:            worktree,
		Registry:       registry,
		Sandbox:        sessionEngine,
		PermissionMode: agent.PermissionModeAuto,
	})
	runner := newAgentToolRunner(config, worktree)

	res, err := runner.RunTool(context.Background(), "engine_root_probe", map[string]any{})
	if err != nil {
		t.Fatalf("RunTool error: %v", err)
	}
	if !res.OK {
		t.Fatalf("probe failed: %+v", res)
	}
	if recordedRoot != worktree {
		t.Fatalf("stage tool engine root = %q, want run workdir %q", recordedRoot, worktree)
	}

	// Add-dir style grants survive the re-root: the extra root stays in the
	// granted write roots alongside the new run directory.
	// Scope grants are symlink-resolved on Add; compare against resolved paths.
	resolvedWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatalf("resolve worktree: %v", err)
	}
	resolvedExtra, err := filepath.EvalSymlinks(extraRoot)
	if err != nil {
		t.Fatalf("resolve extra root: %v", err)
	}
	foundExtra := false
	foundWorktree := false
	for _, root := range recordedRoots {
		switch root {
		case resolvedExtra:
			foundExtra = true
		case resolvedWorktree:
			foundWorktree = true
		}
	}
	if !foundExtra || !foundWorktree {
		t.Fatalf("re-rooted scope roots = %v, want both %q and %q", recordedRoots, worktree, extraRoot)
	}
}

// TestPipelineToolScopeFollowsRunWorktree pins the TUI /exec worktree fix:
// a pipeline run bound to a worktree after startup keeps the sandbox engine
// rooted at the session workspace, but stage tool execution must evaluate
// against the run worktree. Commands inside the worktree succeed; a write
// outside BOTH the session root and the worktree is still refused.
func TestPipelineToolScopeFollowsRunWorktree(t *testing.T) {
	sessionRoot := t.TempDir()
	worktree := t.TempDir()
	outsideBoth := t.TempDir()

	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: sessionRoot,
		Policy:        sandbox.DefaultPolicy(),
	})
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(worktree))

	runner := newAgentToolRunner(PipelineConfigFromAgentOptions(agent.Options{
		Cwd:            worktree,
		Registry:       registry,
		Sandbox:        engine,
		PermissionMode: agent.PermissionModeAuto,
	}), worktree)

	res, err := runner.RunTool(context.Background(), "bash", map[string]any{"command": "echo run > marker.txt && cat marker.txt"})
	if err != nil {
		t.Fatalf("RunTool error: %v", err)
	}
	if !res.OK {
		t.Fatalf("worktree bash refused: %s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(worktree, "marker.txt")); err != nil {
		t.Fatalf("marker missing from worktree: %v", err)
	}

	// Stage tool calls carry the run directory in the cwd argument; that is
	// the exact shape the demo captures showed being refused pre-fix.
	res, err = runner.RunTool(context.Background(), "bash", map[string]any{"command": "echo run > marker2.txt", "cwd": worktree})
	if err != nil {
		t.Fatalf("RunTool error: %v", err)
	}
	if !res.OK {
		t.Fatalf("bash scoped to the run worktree refused: %s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(worktree, "marker2.txt")); err != nil {
		t.Fatalf("marker2 missing from worktree: %v", err)
	}

	res, err = runner.RunTool(context.Background(), "bash", map[string]any{"command": "echo x > escape.txt", "cwd": outsideBoth})
	if err != nil {
		t.Fatalf("RunTool error: %v", err)
	}
	if res.OK {
		t.Fatal("write outside both roots unexpectedly succeeded")
	}
	// Either the tool-scoped path guard or the sandbox decision must refuse;
	// both name the boundary instead of silently succeeding.
	if !strings.Contains(res.Output, "outside_workspace") && !strings.Contains(res.Output, "must stay inside the workspace") {
		t.Fatalf("output = %q, want an out-of-workspace refusal", res.Output)
	}
	if _, err := os.Stat(filepath.Join(outsideBoth, "escape.txt")); err == nil {
		t.Fatal("escape file was created despite sandbox block")
	}
}

// TestPipelineConfigMapsTraceWriteWarn pins the seam wiring: the exec surface
// sets Options.TraceWriteWarn and the pipeline config must carry it through so
// the accumulator can warn exactly once on telemetry loss.
func TestPipelineConfigMapsTraceWriteWarn(t *testing.T) {
	fn := func(msg string) {}
	cfg := PipelineConfigFromAgentOptions(agent.Options{TraceWriteWarn: fn})
	if cfg.TraceWriteWarn == nil {
		t.Fatal("TraceWriteWarn lost the callback in config mapping")
	}
	if cfg2 := PipelineConfigFromAgentOptions(agent.Options{}); cfg2.TraceWriteWarn != nil {
		t.Fatal("nil TraceWriteWarn must map to nil TraceWriteWarn")
	}
}
