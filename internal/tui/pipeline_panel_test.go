package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestMain(m *testing.M) {
	tuiSpliceRun = func(ctx context.Context, prompt string, provider agent.Provider, options agent.Options, mem splicerun.MemoryStore, recovery splicerun.WorkspaceRecovery) (agent.Result, error) {
		if recovery != nil {
			panic("TUI must not receive workspace recovery authority")
		}
		return agent.Run(ctx, prompt, provider, options)
	}
	tuiResolveMemory = func(context.Context) (*memd.Client, error) { return nil, nil }
	os.Exit(m.Run())
}

func TestPipelinePanelApplyStageMarker(t *testing.T) {
	var state pipelinePanelState
	marker := "\x00STAGE{\"name\":\"code_writer\",\"status\":\"running\",\"detail\":\"\",\"progress\":0,\"changedFiles\":[]}\x00"
	if !state.applyStageMarker(marker) {
		t.Fatal("applyStageMarker returned false for stage marker")
	}
	if len(state.stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(state.stages))
	}
	stage := state.stages[0]
	if stage.name != "code_writer" || stage.status != pipelineStageRunning {
		t.Fatalf("stage = %#v, want code_writer running", stage)
	}
}

func TestPipelinePanelApplyStageMarkerIgnoresNormalText(t *testing.T) {
	var state pipelinePanelState
	if state.applyStageMarker("ordinary reasoning") {
		t.Fatal("applyStageMarker consumed non-marker text")
	}
}

func TestPipelinePanelRenderSectionGlyphs(t *testing.T) {
	state := pipelinePanelState{
		active: true,
		stages: []pipelineStageRow{
			{name: "planner", status: pipelineStageCompleted},
			{name: "code_writer", status: pipelineStageRunning, detail: "writing", progress: 50},
			{name: "verifier", status: pipelineStagePending},
		},
	}
	plain := plainRender(t, strings.Join(state.renderSection(40, 0), "\n"))
	for _, want := range []string{"✓", "◜", "○"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("renderSection missing %q in %q", want, plain)
		}
	}
}

func TestPipelineStageGlyphAdvancesWithPhase(t *testing.T) {
	for phase, want := range map[int]string{0: "◜", 1: "◠", 2: "◝", 5: "◟", 6: "◜"} {
		g, _ := pipelineStageGlyphAndStyle(pipelineStageRunning, phase)
		if !strings.Contains(g, want) {
			t.Errorf("phase %d: glyph = %q, want %q", phase, g, want)
		}
	}
	if g, _ := pipelineStageGlyphAndStyle(pipelineStageCompleted, 3); !strings.Contains(g, "✓") {
		t.Errorf("completed glyph = %q, want ✓", g)
	}
	if g, _ := pipelineStageGlyphAndStyle(pipelineStagePending, 3); !strings.Contains(g, "○") {
		t.Errorf("pending glyph = %q, want ○", g)
	}
}

func TestPipelinePanelResetClearLifecycle(t *testing.T) {
	state := pipelinePanelState{stages: []pipelineStageRow{{name: "old", status: pipelineStageCompleted}}, active: true, changedFiles: []string{"old.go"}}
	state.reset()
	if !state.active || len(state.stages) != 0 || len(state.changedFiles) != 0 {
		t.Fatalf("reset state = %#v, want active empty state", state)
	}
	if !state.isEmpty() {
		t.Fatal("reset with no stages should be empty for rendering")
	}
	state.applyStageMarker("\x00STAGE{\"name\":\"planner\",\"status\":\"completed\",\"detail\":\"done\",\"progress\":100,\"changedFiles\":[\"main.go\"]}\x00")
	if state.isEmpty() || len(state.changedFiles) != 1 || state.changedFiles[0] != "main.go" {
		t.Fatalf("marker after reset state = %#v", state)
	}
	state.clear()
	if state.active || len(state.stages) != 0 || len(state.changedFiles) != 0 || !state.isEmpty() {
		t.Fatalf("clear state = %#v, want inactive empty state", state)
	}
}

type tuiRoutingTestProvider struct {
	model string
}

func (*tuiRoutingTestProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	return nil, nil
}

func TestTUIReloadsStageModelRoutingForEachPipelineRun(t *testing.T) {
	originalRun := tuiSpliceRun
	defer func() { tuiSpliceRun = originalRun }()

	dir := t.TempDir()
	userConfigPath := filepath.Join(dir, "config.json")
	stageConfigPath := filepath.Join(dir, "stage-models.json")
	profile := config.ProviderProfile{Name: "local", CatalogID: "ollama", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "http://localhost:11434/v1", Model: "base-model"}
	builtModels := []string{}
	m := newModel(context.Background(), Options{
		UserConfigPath:  userConfigPath,
		ProviderName:    profile.Name,
		ModelName:       profile.Model,
		ProviderProfile: profile,
		SavedProviders:  []config.ProviderProfile{profile},
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			builtModels = append(builtModels, profile.Model)
			return &tuiRoutingTestProvider{model: profile.Model}, nil
		},
	})

	var routedModels []string
	var routedEfforts []string
	tuiSpliceRun = func(_ context.Context, _ string, _ agent.Provider, options agent.Options, _ splicerun.MemoryStore, recovery splicerun.WorkspaceRecovery) (agent.Result, error) {
		if recovery != nil {
			t.Fatal("TUI supplied workspace recovery authority")
		}
		if options.StageModelResolver == nil {
			t.Fatal("TUI pipeline run has no stage model resolver")
		}
		provider, model, effort, err := options.StageModelResolver("code_writer")
		if err != nil {
			t.Fatalf("resolve code_writer route: %v", err)
		}
		if provider == nil {
			t.Fatal("configured stage route returned nil provider")
		}
		routedModels = append(routedModels, model)
		routedEfforts = append(routedEfforts, effort)
		return agent.Result{FinalAnswer: "done"}, nil
	}

	writeConfig := func(model, effort string) {
		t.Helper()
		content := `{"default":{"provider_profile":"local","model":"` + model + `","reasoning_effort":"` + effort + `"}}`
		if err := os.WriteFile(stageConfigPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("qwen-first", "low")
	if msg := m.runAgentWithOptions(1, context.Background(), "first", nil, tuiAgentRunOptions{})(); msg.(agentResponseMsg).err != nil {
		t.Fatalf("first run: %v", msg.(agentResponseMsg).err)
	}
	writeConfig("qwen-second", "high")
	if msg := m.runAgentWithOptions(2, context.Background(), "second", nil, tuiAgentRunOptions{})(); msg.(agentResponseMsg).err != nil {
		t.Fatalf("second run: %v", msg.(agentResponseMsg).err)
	}

	if strings.Join(routedModels, ",") != "qwen-first,qwen-second" {
		t.Fatalf("routed models = %v, want config reloaded for each run", routedModels)
	}
	if strings.Join(routedEfforts, ",") != "low,high" {
		t.Fatalf("routed efforts = %v", routedEfforts)
	}
	if strings.Join(builtModels, ",") != "qwen-first,qwen-second" {
		t.Fatalf("provider factory models = %v", builtModels)
	}
}

type tuiPipelineFeatureProvider struct {
	toolNames []string
}

func (provider *tuiPipelineFeatureProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	if len(request.Tools) != 1 {
		return nil, fmt.Errorf("expected one stage output tool, got %d", len(request.Tools))
	}
	toolName := request.Tools[0].Name
	provider.toolNames = append(provider.toolNames, toolName)
	var arguments []byte
	switch toolName {
	case "submit_code":
		arguments, _ = json.Marshal(schemas.CodeWriterOutput{
			Files: []schemas.FileChange{
				{Path: "go.mod", Content: "module example\n\ngo 1.25\n", ChangeType: "create"},
				{Path: "hello.go", Content: "package example\n\nfunc Hello() string { return \"hello\" }\n", ChangeType: "create"},
				{Path: "hello_test.go", Content: "package example\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"wrong greeting\")\n\t}\n}\n", ChangeType: "create"},
			},
			Language:   "go",
			Intent:     "create a hello function",
			Confidence: 0.95,
		})
	default:
		return nil, fmt.Errorf("unexpected LLM stage tool %q", toolName)
	}
	ch := make(chan zeroruntime.StreamEvent, 5)
	select {
	case <-ctx.Done():
		close(ch)
		return ch, ctx.Err()
	default:
	}
	callID := "feature-" + toolName
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: callID, ToolName: toolName}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: callID, ArgumentsFragment: string(arguments)}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: callID}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

type rejectingTUIPipelineProvider struct {
	calls int
}

func (provider *rejectingTUIPipelineProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	provider.calls++
	return nil, fmt.Errorf("active provider must not be used when stage routing selects local")
}

func TestTUIPipelineEndToEndFeature(t *testing.T) {
	originalRun := tuiSpliceRun
	defer func() { tuiSpliceRun = originalRun }()
	tuiSpliceRun = splicerun.Run

	dir := t.TempDir()
	userConfigPath := filepath.Join(dir, "config.json")
	stageConfig := `{"default":{"provider_profile":"local","model":"qwen-local","reasoning_effort":"medium"}}`
	if err := os.WriteFile(filepath.Join(dir, "stage-models.json"), []byte(stageConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	activeProfile := config.ProviderProfile{Name: "cloud", ProviderKind: config.ProviderKindOpenAI, Model: "cloud-model"}
	localProfile := config.ProviderProfile{Name: "local", CatalogID: "ollama", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "http://localhost:11434/v1", Model: "qwen-local"}
	activeProvider := &rejectingTUIPipelineProvider{}
	localProvider := &tuiPipelineFeatureProvider{}
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(dir) {
		registry.Register(tool)
	}
	var builtProfiles []config.ProviderProfile
	var runtimeMessages []tea.Msg
	m := newModel(context.Background(), Options{
		Cwd:             dir,
		UserConfigPath:  userConfigPath,
		ProviderName:    activeProfile.Name,
		ModelName:       activeProfile.Model,
		ProviderProfile: activeProfile,
		SavedProviders:  []config.ProviderProfile{activeProfile, localProfile},
		Provider:        activeProvider,
		Registry:        registry,
		PermissionMode:  agent.PermissionModeAuto,
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			builtProfiles = append(builtProfiles, profile)
			if profile.Name != "local" || profile.Model != "qwen-local" {
				return nil, fmt.Errorf("unexpected routed profile %s/%s", profile.Name, profile.Model)
			}
			return localProvider, nil
		},
		RuntimeMessageSink: func(msg tea.Msg) { runtimeMessages = append(runtimeMessages, msg) },
		AltScreen:          true,
	})
	m.width = 120
	m.height = 40
	m.input.SetValue("/exec create a hello function")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("normal prompt did not start a TUI run")
	}
	responseMsg := execCmd(cmd)
	response, ok := responseMsg.(agentResponseMsg)
	if !ok {
		t.Fatalf("run command returned %T, want agentResponseMsg", responseMsg)
	}
	if response.err != nil {
		t.Fatalf("real TUI pipeline failed: %v", response.err)
	}
	for _, msg := range runtimeMessages {
		updated, _ = m.Update(msg)
		m = updated.(model)
	}
	updated, _ = m.Update(response)
	m = updated.(model)

	if activeProvider.calls != 0 {
		t.Fatalf("active cloud provider calls = %d, want routed local provider only", activeProvider.calls)
	}
	if len(builtProfiles) != 1 || builtProfiles[0].Name != "local" || builtProfiles[0].Model != "qwen-local" {
		t.Fatalf("routed provider builds = %#v", builtProfiles)
	}
	if strings.Join(localProvider.toolNames, ",") != "submit_code" {
		t.Fatalf("local provider tools = %v, want submit_code", localProvider.toolNames)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.go")); err != nil {
		t.Fatalf("pipeline did not apply generated file: %v", err)
	}

	view := plainRender(t, m.View())
	for _, want := range []string{"PIPELINE", "code_writer", "static_analyzer", "test_runner", "completed", "3 stages"} {
		if !strings.Contains(view, want) {
			t.Fatalf("final TUI view missing %q:\n%s", want, view)
		}
	}
	for _, stage := range []string{"code_writer", "static_analyzer", "test_runner"} {
		foundCompleted := false
		for _, row := range m.pipeline.stages {
			if row.name == stage && row.status == pipelineStageCompleted {
				foundCompleted = true
				break
			}
		}
		if !foundCompleted {
			t.Fatalf("pipeline stage %q was not completed: %#v", stage, m.pipeline.stages)
		}
	}

	var storedResult schemas.PipelineResult
	for _, event := range response.sessionEvents {
		if event.Type != sessions.EventMessage {
			continue
		}
		payload, ok := event.Payload.(map[string]any)
		if !ok || payload["role"] != "assistant" {
			continue
		}
		content, _ := payload["content"].(string)
		if json.Unmarshal([]byte(content), &storedResult) == nil && storedResult.Status != "" {
			break
		}
	}
	if storedResult.Status != "completed" || len(storedResult.Stages) != 3 {
		t.Fatalf("stored pipeline result = %#v", storedResult)
	}
}

func TestPipelineMemoryStoreNilPath(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil memory path panicked: %v", r)
		}
	}()
	var mem splicerun.MemoryStore
	var memClient *memd.Client
	if memClient != nil {
		mem = memClient
	}
	if mem != nil {
		t.Fatal("nil *memd.Client must not become a non-nil MemoryStore interface")
	}
}
