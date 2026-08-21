package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	"github.com/Taf0711/splice/internal/worktrees"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestMain(m *testing.M) {
	// Tests without a SessionStore use the default store, which writes to the user data directory.
	dataDir, err := os.MkdirTemp("", "splice-tui-test-")
	if err != nil {
		panic(fmt.Sprintf("create temporary XDG data directory: %v", err))
	}
	if err := os.Setenv("XDG_DATA_HOME", dataDir); err != nil {
		_ = os.RemoveAll(dataDir)
		panic(fmt.Sprintf("set XDG_DATA_HOME: %v", err))
	}
	tuiSpliceRun = func(ctx context.Context, prompt string, provider agent.Provider, options agent.Options, mem splicerun.MemoryStore, recovery splicerun.WorkspaceRecovery) (agent.Result, error) {
		if recovery != nil {
			panic("TUI must not receive workspace recovery authority")
		}
		return agent.Run(ctx, prompt, provider, options)
	}
	tuiResolveMemory = func(context.Context) (*memd.Client, error) { return nil, nil }
	disableTUIWorktreesForTest = true
	code := m.Run()
	_ = os.RemoveAll(dataDir)
	os.Exit(code)
}

func TestPipelinePlanSeedsStableStageRoster(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.activeRunID = 3
	updated, _ := m.Update(pipelinePlanMsg{
		runID: 3,
		event: agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner", "acceptance_verifier"}},
	})
	m = updated.(model)

	if len(m.pipeline.stages) != 3 {
		t.Fatalf("stages = %d, want 3", len(m.pipeline.stages))
	}
	for i, want := range []string{"code_writer", "test_runner", "acceptance_verifier"} {
		if got := m.pipeline.stages[i]; got.name != want || got.status != pipelineStagePending {
			t.Fatalf("stage %d = %#v, want pending %q", i, got, want)
		}
	}

	updated, _ = m.Update(pipelineStageEventMsg{
		runID: 3,
		event: agent.StageEvent{Name: "code_writer", Status: "running", Detail: "writing code changes"},
	})
	m = updated.(model)
	if len(m.pipeline.stages) != 3 || m.pipeline.stages[0].status != pipelineStageRunning {
		t.Fatalf("stage event changed roster: %#v", m.pipeline.stages)
	}
}

func TestPipelinePresentationComputesOverallProgress(t *testing.T) {
	var state pipelinePanelState
	state.applyPlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner", "acceptance_verifier"}})
	state.applyStageEvent(agent.StageEvent{Name: "code_writer", Status: "completed", Progress: 100})
	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "running", Progress: 50})

	presentation := state.presentation()
	if presentation.done != 1 || presentation.total != 3 {
		t.Fatalf("counts = %d/%d, want 1/3", presentation.done, presentation.total)
	}
	if presentation.progress != 50 {
		t.Fatalf("progress = %d, want 50", presentation.progress)
	}
	if presentation.current == nil || presentation.current.name != "test_runner" {
		t.Fatalf("current = %#v, want test_runner", presentation.current)
	}
}

func TestPipelinePanelApplyStageEvent(t *testing.T) {
	var state pipelinePanelState
	state.applyStageEvent(agent.StageEvent{
		Name:         "code_writer",
		Status:       "running",
		Detail:       "writing files",
		Progress:     50,
		ChangedFiles: []string{"main.go"},
	})
	if len(state.stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(state.stages))
	}
	stage := state.stages[0]
	if stage.name != "code_writer" || stage.status != pipelineStageRunning || stage.progress != 50 {
		t.Fatalf("stage = %#v", stage)
	}
	if len(state.changedFiles) != 1 || state.changedFiles[0] != "main.go" {
		t.Fatalf("changed files = %v", state.changedFiles)
	}
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

func TestPipelinePanelMapsEveryEmittedStageStatus(t *testing.T) {
	cases := []struct {
		status string
		want   pipelineStageStatus
	}{
		{status: "skipped", want: pipelineStageSkipped},
		{status: "running", want: pipelineStageRunning},
		{status: "failed", want: pipelineStageFailed},
		{status: "incomplete", want: pipelineStageIncomplete},
		{status: "completed", want: pipelineStageCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			var state pipelinePanelState
			marker := fmt.Sprintf("\x00STAGE{\"name\":\"stage\",\"status\":%q}\x00", tc.status)
			if !state.applyStageMarker(marker) {
				t.Fatal("applyStageMarker returned false")
			}
			if got := state.stages[0].status; got != tc.want {
				t.Fatalf("status %q mapped to %v, want %v", tc.status, got, tc.want)
			}
		})
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

func TestPipelinePanelAlwaysShowsOverallProgress(t *testing.T) {
	var state pipelinePanelState
	state.applyPlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner", "acceptance_verifier"}})
	state.applyStageEvent(agent.StageEvent{Name: "code_writer", Status: "running", Progress: 0})

	plain := plainRender(t, strings.Join(state.renderSection(40, 0), "\n"))
	if !strings.Contains(plain, "0%") {
		t.Fatalf("running pipeline hid indeterminate progress: %q", plain)
	}

	state.applyStageEvent(agent.StageEvent{Name: "code_writer", Status: "completed", Progress: 100})
	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "running", Progress: 50})
	plain = plainRender(t, strings.Join(state.renderSection(40, 0), "\n"))
	if !strings.Contains(plain, "50%") {
		t.Fatalf("aggregate pipeline progress missing: %q", plain)
	}
}

func TestPipelinePanelIncompleteIsTerminalAndNeverCurrent(t *testing.T) {
	state := pipelinePanelState{
		active: true,
		stages: []pipelineStageRow{{name: "acceptance_verifier", status: pipelineStageIncomplete, detail: "partial"}},
	}
	done, total, allDone := state.counts()
	if done != 1 || total != 1 || !allDone {
		t.Fatalf("counts = %d/%d allDone=%v, want 1/1 true", done, total, allDone)
	}
	plain := plainRender(t, strings.Join(state.renderSection(40, 0), "\n"))
	if !strings.Contains(plain, "◐") {
		t.Fatalf("incomplete stage missing partial glyph: %q", plain)
	}
	if strings.Contains(plain, "CURRENT") {
		t.Fatalf("incomplete stage rendered as current: %q", plain)
	}
}

func TestPipelinePanelHeaderReportsFailedTerminalRosterInRed(t *testing.T) {
	state := pipelinePanelState{
		active: true,
		stages: []pipelineStageRow{
			{name: "skipped", status: pipelineStageSkipped},
			{name: "failed", status: pipelineStageFailed},
			{name: "incomplete", status: pipelineStageIncomplete},
			{name: "completed", status: pipelineStageCompleted},
		},
	}
	got := state.headerLineWithChip(40, "")
	want := sidebarHeaderWithCount("PIPELINE", "4/4", zeroTheme.red, 40)
	if got != want {
		t.Fatalf("failed pipeline header = %q, want %q", got, want)
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
	if g, _ := pipelineStageGlyphAndStyle(pipelineStageIncomplete, 3); g != zeroTheme.amber.Render("◐") {
		t.Errorf("incomplete glyph = %q, want amber partial glyph", g)
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

func TestPipelinePanelMessageDoesNotResetStage(t *testing.T) {
	var state pipelinePanelState
	state.applyStageMarker("\x00STAGE{\"name\":\"test_runner\",\"status\":\"completed\",\"detail\":\"tests passed\",\"progress\":100}\x00")
	before := state.stages[0]

	state.applyStageEvent(agent.StageEvent{
		Name:   "test_runner",
		Status: "message",
		Detail: "revision_request -> code_writer: 2 failing tests",
	})

	if len(state.stages) != 1 {
		t.Fatalf("stages = %d, want 1 (message must not create a stage row)", len(state.stages))
	}
	if after := state.stages[0]; after != before {
		t.Fatalf("test_runner stage row changed after message: before=%#v after=%#v", before, after)
	}
	if len(state.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(state.messages))
	}
	msg := state.messages[0]
	if msg.from != "test_runner" || msg.to != "code_writer" || msg.resolved {
		t.Fatalf("message = %#v, want from=test_runner to=code_writer resolved=false", msg)
	}
}

func TestPipelinePanelRepairedResolvesLatestUnresolved(t *testing.T) {
	var state pipelinePanelState
	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "message", Detail: "revision_request -> code_writer: 1 test"})
	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "message", Detail: "revision_request -> code_writer: 2 tests"})

	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "repaired", Detail: "revision resolved: tests pass"})

	if len(state.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(state.messages))
	}
	if state.messages[0].resolved {
		t.Fatal("first message should stay unresolved")
	}
	if !state.messages[1].resolved {
		t.Fatal("latest message should be resolved")
	}
}

func TestPipelinePanelRenderMessagesCapsAtLastThree(t *testing.T) {
	var state pipelinePanelState
	state.active = true
	state.stages = []pipelineStageRow{{name: "test_runner", status: pipelineStageCompleted}}
	for i := 0; i < 4; i++ {
		state.applyStageEvent(agent.StageEvent{
			Name:   "test_runner",
			Status: "message",
			Detail: fmt.Sprintf("revision_request -> code_writer: test %d", i),
		})
	}
	plain := plainRender(t, strings.Join(state.renderSection(80, 0), "\n"))
	if strings.Contains(plain, "test 0") {
		t.Fatalf("oldest message not hidden: %q", plain)
	}
	for _, want := range []string{"test 1", "test 2", "test 3"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("render missing %q in %q", want, plain)
		}
	}
	if !strings.Contains(plain, "MESSAGES") {
		t.Fatalf("render missing MESSAGES header: %q", plain)
	}
}

func TestPipelinePanelResetClearMessages(t *testing.T) {
	var state pipelinePanelState
	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "message", Detail: "revision_request -> code_writer: 1 test"})
	if len(state.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(state.messages))
	}
	state.reset()
	if len(state.messages) != 0 {
		t.Fatalf("reset messages = %d, want 0", len(state.messages))
	}
	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "message", Detail: "revision_request -> code_writer: 1 test"})
	state.clear()
	if len(state.messages) != 0 {
		t.Fatalf("clear messages = %d, want 0", len(state.messages))
	}
}

func TestParseMessageTo(t *testing.T) {
	cases := []struct{ detail, want string }{
		{"revision_request -> code_writer: 2 failing tests", "code_writer"},
		{"revision_request -> code_writer 2 tests", "code_writer"},
		{"revision_request -> code_writer", "code_writer"},
		{"no arrow here", ""},
		{"revision_request -> ", ""},
	}
	for _, tc := range cases {
		if got := parseMessageTo(tc.detail); got != tc.want {
			t.Fatalf("parseMessageTo(%q) = %q, want %q", tc.detail, got, tc.want)
		}
	}
}

func TestPipelinePanelMessageViaLegacyMarker(t *testing.T) {
	var state pipelinePanelState
	marker := "\x00STAGE{\"name\":\"test_runner\",\"status\":\"message\",\"detail\":\"revision_request -> code_writer: 1 test\"}\x00"
	if !state.applyStageMarker(marker) {
		t.Fatal("applyStageMarker returned false")
	}
	if len(state.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(state.messages))
	}
	if state.messages[0].to != "code_writer" {
		t.Fatalf("message.to = %q, want code_writer", state.messages[0].to)
	}
	if len(state.stages) != 0 {
		t.Fatalf("stages = %d, want 0 (message must not create a stage row)", len(state.stages))
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
		selection, err := options.StageModelResolver("code_writer")
		if err != nil {
			t.Fatalf("resolve code_writer route: %v", err)
		}
		if selection.Provider == nil {
			t.Fatal("configured stage route returned nil provider")
		}
		routedModels = append(routedModels, selection.Model)
		routedEfforts = append(routedEfforts, selection.ReasoningEffort)
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
	ch := make(chan zeroruntime.StreamEvent, 6)
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
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 11, OutputTokens: 5}}
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
	for _, want := range []string{"PIPELINE", "code_writer", "static_analyzer", "test_runner", "acceptance_verifier", "completed", "4 stages"} {
		if !strings.Contains(view, want) {
			t.Fatalf("final TUI view missing %q:\n%s", want, view)
		}
	}
	for _, stage := range []string{"code_writer", "static_analyzer", "test_runner", "acceptance_verifier"} {
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
	if storedResult.Status != "completed" || len(storedResult.Stages) != 4 {
		t.Fatalf("stored pipeline result = %#v", storedResult)
	}
	if len(response.usageEvents) != 1 {
		t.Fatalf("response usage events = %d, want 1", len(response.usageEvents))
	}
	var usagePayload map[string]any
	for _, event := range response.sessionEvents {
		if event.Type == sessions.EventUsage {
			usagePayload, _ = event.Payload.(map[string]any)
			break
		}
	}
	if usagePayload["model"] != "qwen-local" || usagePayload["provider"] != "local" || usagePayload["usageSequence"] != 1 || usagePayload["costStatus"] != agent.CostStatusUnpriced {
		t.Fatalf("attributed TUI usage payload = %#v", usagePayload)
	}
}

func TestTUIPipelineRunUsesPreparedWorktree(t *testing.T) {
	origDisable := disableTUIWorktreesForTest
	origPrepare := tuiPrepareWorktree
	origUnlock := tuiUnlockWorktree
	origRun := tuiSpliceRun
	defer func() {
		disableTUIWorktreesForTest = origDisable
		tuiPrepareWorktree = origPrepare
		tuiUnlockWorktree = origUnlock
		tuiSpliceRun = origRun
	}()
	disableTUIWorktreesForTest = false

	workDir := t.TempDir()
	preparedPath := filepath.Join(workDir, "wt")
	if err := os.Mkdir(preparedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	locked := false
	unlocked := false
	tuiPrepareWorktree = func(_ context.Context, options worktrees.Options) (worktrees.Result, error) {
		if options.Cwd != workDir {
			t.Fatalf("prepare cwd = %q, want %q", options.Cwd, workDir)
		}
		if !strings.HasPrefix(options.Name, "tui-") {
			t.Fatalf("prepare name = %q, want tui- prefix", options.Name)
		}
		locked = true
		return worktrees.Result{Name: options.Name, Path: preparedPath, RepoRoot: workDir, Locked: true}, nil
	}
	tuiUnlockWorktree = func(context.Context, worktrees.UnlockOptions) error {
		if !locked {
			t.Fatal("unlock before lock")
		}
		unlocked = true
		return nil
	}
	var gotCwd string
	var gotRecovery splicerun.WorkspaceRecovery
	tuiSpliceRun = func(_ context.Context, _ string, _ agent.Provider, options agent.Options, _ splicerun.MemoryStore, recovery splicerun.WorkspaceRecovery) (agent.Result, error) {
		if recovery == nil {
			t.Fatal("worktree run must pass iteration recovery")
		}
		gotRecovery = recovery
		gotCwd = options.Cwd
		return agent.Result{FinalAnswer: `{"status":"completed"}`}, nil
	}

	m := newModel(context.Background(), Options{Cwd: workDir, Worktrees: config.WorktreesConfig{}})
	m.activeRunID = 7
	m.activeSession.SessionID = "sess1"
	msg := m.runAgentWithOptions(7, context.Background(), "do work", nil, tuiAgentRunOptions{})()
	resp := msg.(agentResponseMsg)
	if resp.err != nil {
		t.Fatalf("run: %v", resp.err)
	}
	if gotCwd != preparedPath {
		t.Fatalf("pipeline cwd = %q, want %q", gotCwd, preparedPath)
	}
	if gotRecovery == nil {
		t.Fatal("expected iteration recovery for a worktree run")
	}
	if _, ok := gotRecovery.(*worktrees.IterationRecovery); !ok {
		t.Fatalf("recovery = %T, want *worktrees.IterationRecovery", gotRecovery)
	}
	if resp.worktree == nil || resp.worktree.Path != preparedPath {
		t.Fatalf("response worktree = %#v", resp.worktree)
	}
	if unlocked {
		t.Fatal("lock must stay held until the worktree review")
	}
}

func TestTUIPipelineRunFallsBackWhenPrepareFails(t *testing.T) {
	origDisable := disableTUIWorktreesForTest
	origPrepare := tuiPrepareWorktree
	origRun := tuiSpliceRun
	defer func() {
		disableTUIWorktreesForTest = origDisable
		tuiPrepareWorktree = origPrepare
		tuiSpliceRun = origRun
	}()
	disableTUIWorktreesForTest = false

	workDir := t.TempDir()
	tuiPrepareWorktree = func(context.Context, worktrees.Options) (worktrees.Result, error) {
		return worktrees.Result{}, fmt.Errorf("not a git repository")
	}
	var gotCwd string
	tuiSpliceRun = func(_ context.Context, _ string, _ agent.Provider, options agent.Options, _ splicerun.MemoryStore, recovery splicerun.WorkspaceRecovery) (agent.Result, error) {
		if recovery != nil {
			t.Fatal("fallback must not pass recovery")
		}
		gotCwd = options.Cwd
		return agent.Result{FinalAnswer: `{"status":"completed"}`}, nil
	}

	m := newModel(context.Background(), Options{Cwd: workDir})
	msg := m.runAgentWithOptions(1, context.Background(), "do work", nil, tuiAgentRunOptions{})()
	resp := msg.(agentResponseMsg)
	if resp.err != nil {
		t.Fatalf("run: %v", resp.err)
	}
	if gotCwd != workDir {
		t.Fatalf("fallback cwd = %q, want live checkout %q", gotCwd, workDir)
	}
	if resp.worktreeNotice != tuiWorktreeFallbackNotice {
		t.Fatalf("notice = %q", resp.worktreeNotice)
	}
}

func TestTUIDesignAndSpecRunsSkipWorktreePrepare(t *testing.T) {
	origDisable := disableTUIWorktreesForTest
	origPrepare := tuiPrepareWorktree
	defer func() {
		disableTUIWorktreesForTest = origDisable
		tuiPrepareWorktree = origPrepare
	}()
	disableTUIWorktreesForTest = false
	called := false
	tuiPrepareWorktree = func(context.Context, worktrees.Options) (worktrees.Result, error) {
		called = true
		return worktrees.Result{}, fmt.Errorf("should not prepare")
	}
	m := newModel(context.Background(), Options{Cwd: t.TempDir()})
	_ = m.runAgentWithOptions(1, context.Background(), "plan", nil, tuiAgentRunOptions{runKind: tuiRunDesignConversation})()
	_ = m.runAgentWithOptions(2, context.Background(), "spec", nil, tuiAgentRunOptions{runKind: tuiRunSpecDraft})()
	if called {
		t.Fatal("design and spec-draft runs must not prepare a worktree")
	}
}

func TestWorktreeChipRendersNearModel(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})
	m.activeWorktree = &worktrees.Result{Name: "tui-sess-1"}
	got := m.titleModelSegment()
	if !strings.Contains(got, "wt:tui-sess-1") {
		t.Fatalf("model segment = %q, want worktree chip", got)
	}
}

func TestTUIWorktreeReviewDecisions(t *testing.T) {
	origMerge := tuiMergeBackWorktree
	origPreserve := tuiPreserveWorktree
	origRemove := tuiRemoveWorktree
	origUnlock := tuiUnlockWorktree
	defer func() {
		tuiMergeBackWorktree = origMerge
		tuiPreserveWorktree = origPreserve
		tuiRemoveWorktree = origRemove
		tuiUnlockWorktree = origUnlock
	}()

	wt := worktrees.Result{Name: "tui-sess-1", Path: "/tmp/wt", RepoRoot: "/tmp/repo", Locked: true}

	tests := []struct {
		name         string
		answer       string
		reasonAnswer string
		wantKept     bool
		wantMerge    bool
		wantPin      bool
		wantForce    bool
		wantNotice   string
	}{
		{
			name:       "accept",
			answer:     worktreeReviewAccept,
			wantMerge:  true,
			wantNotice: "merged splice/tui-sess-1",
		},
		{
			name:         "reject",
			answer:       worktreeReviewReject,
			reasonAnswer: worktreeRejectStillFailing,
			wantPin:      true,
			wantForce:    true,
			wantNotice:   "Worktree removed. Work remains on branch splice/tui-sess-1 if you change your mind.",
		},
		{
			name:       "keep",
			answer:     worktreeReviewKeep,
			wantKept:   true,
			wantNotice: "Worktree kept at /tmp/wt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, pinned, removed, unlocked, force := false, false, false, false, false
			tuiMergeBackWorktree = func(_ context.Context, options worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
				merged = true
				if options.WorktreePath != wt.Path || options.RepoRoot != wt.RepoRoot {
					t.Fatalf("merge options = %#v", options)
				}
				return worktrees.MergeBackResult{Status: worktrees.MergeBackMerged, Message: "merged splice/tui-sess-1"}, nil
			}
			tuiPreserveWorktree = func(_ context.Context, options worktrees.MergeBackOptions) (string, error) {
				pinned = true
				if options.WorktreePath != wt.Path || options.Name != wt.Name {
					t.Fatalf("preserve options = %#v", options)
				}
				return "splice/" + options.Name, nil
			}
			tuiRemoveWorktree = func(_ context.Context, options worktrees.RemoveOptions) error {
				removed = true
				force = options.Force
				return nil
			}
			tuiUnlockWorktree = func(context.Context, worktrees.UnlockOptions) error {
				unlocked = true
				return nil
			}

			m := newModel(context.Background(), Options{})
			next, _ := m.maybeOfferWorktreeReview(&wt, false)
			if next.pendingAskUser == nil || !next.pendingAskUser.keepOnEsc {
				t.Fatal("expected keep-on-esc worktree review prompt")
			}
			next.pendingAskUser.states[0].answer = tt.answer
			submittedModel, cmd := next.submitAskUser()
			submitted := submittedModel.(model)
			if tt.reasonAnswer != "" {
				// Reject asks for a reason before the removal runs.
				if submitted.pendingAskUser == nil || submitted.pendingAskUser.reviewDecision != worktreeReviewReject {
					t.Fatal("expected reject-reason prompt after Reject")
				}
				submitted.pendingAskUser.states[0].answer = tt.reasonAnswer
				var cmd2 tea.Cmd
				submittedModel, cmd2 = submitted.submitAskUser()
				submitted = submittedModel.(model)
				cmd = cmd2
			}
			if submitted.pendingAskUser != nil {
				t.Fatal("review prompt still pending after submit")
			}
			if cmd == nil {
				t.Fatal("expected review command")
			}
			msg := cmd().(worktreeReviewResultMsg)
			if !strings.Contains(msg.notice, tt.wantNotice) {
				t.Fatalf("notice = %q, want %q", msg.notice, tt.wantNotice)
			}
			if (msg.kept != nil) != tt.wantKept {
				t.Fatalf("kept = %#v, wantKept %v", msg.kept, tt.wantKept)
			}
			if merged != tt.wantMerge {
				t.Fatalf("merged = %v, want %v", merged, tt.wantMerge)
			}
			if pinned != tt.wantPin {
				t.Fatalf("pinned = %v, want %v", pinned, tt.wantPin)
			}
			if !unlocked {
				t.Fatal("lock must be released on every decision")
			}
			if tt.wantKept {
				if removed {
					t.Fatal("keep must not remove the worktree")
				}
			} else if !removed {
				t.Fatal("expected worktree removal")
			}
			if force != tt.wantForce {
				t.Fatalf("force = %v, want %v", force, tt.wantForce)
			}
		})
	}
}

func TestTUIWorktreeReviewDirtyMainRefusesAccept(t *testing.T) {
	origMerge := tuiMergeBackWorktree
	origRemove := tuiRemoveWorktree
	origUnlock := tuiUnlockWorktree
	defer func() {
		tuiMergeBackWorktree = origMerge
		tuiRemoveWorktree = origRemove
		tuiUnlockWorktree = origUnlock
	}()
	merged := false
	tuiMergeBackWorktree = func(context.Context, worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
		merged = true
		return worktrees.MergeBackResult{}, nil
	}
	unlocked := false
	tuiUnlockWorktree = func(context.Context, worktrees.UnlockOptions) error {
		unlocked = true
		return nil
	}
	tuiRemoveWorktree = func(context.Context, worktrees.RemoveOptions) error {
		t.Fatal("dirty-main refusal must not remove the worktree")
		return nil
	}

	wt := worktrees.Result{Name: "tui-sess-1", Path: "/tmp/wt", RepoRoot: "/tmp/repo", Locked: true}
	m := newModel(context.Background(), Options{})
	next, _ := m.maybeOfferWorktreeReview(&wt, true)
	if next.pendingAskUser == nil {
		t.Fatal("expected review prompt")
	}
	for _, option := range next.pendingAskUser.request.Questions[0].Options {
		if option == worktreeReviewAccept {
			t.Fatal("accept must not be offered when the main checkout is dirty")
		}
	}
	if !transcriptContains(next.transcript, worktreeReviewDirtyNotice) {
		t.Fatal("expected dirty-main notice")
	}
	// A typed accept still refuses merge and keeps the worktree.
	next.pendingAskUser.states[0].answer = worktreeReviewAccept
	_, cmd := next.submitAskUser()
	msg := cmd().(worktreeReviewResultMsg)
	if merged {
		t.Fatal("dirty-main accept must not call merge-back")
	}
	if !unlocked {
		t.Fatal("lock must be released after dirty-main refusal")
	}
	if msg.kept == nil || !strings.Contains(msg.notice, worktreeReviewDirtyNotice) {
		t.Fatalf("refusal result = %#v", msg)
	}
}

func TestTUIWorktreeReviewEscKeepsAndUnlocks(t *testing.T) {
	origUnlock := tuiUnlockWorktree
	origRemove := tuiRemoveWorktree
	defer func() {
		tuiUnlockWorktree = origUnlock
		tuiRemoveWorktree = origRemove
	}()
	unlocked := false
	tuiUnlockWorktree = func(context.Context, worktrees.UnlockOptions) error {
		unlocked = true
		return nil
	}
	tuiRemoveWorktree = func(context.Context, worktrees.RemoveOptions) error {
		t.Fatal("esc keep must not remove the worktree")
		return nil
	}

	wt := worktrees.Result{Name: "tui-sess-1", Path: "/tmp/wt", RepoRoot: "/tmp/repo", Locked: true}
	m := newModel(context.Background(), Options{})
	m, _ = m.maybeOfferWorktreeReview(&wt, false)
	updated, cmd := m.Update(testKey(tea.KeyEsc))
	next := updated.(model)
	if next.pendingAskUser != nil {
		t.Fatal("esc should dismiss the review prompt")
	}
	if cmd == nil {
		t.Fatal("expected keep command")
	}
	msg := cmd().(worktreeReviewResultMsg)
	if msg.kept == nil || !strings.Contains(msg.notice, "Worktree kept at /tmp/wt") {
		t.Fatalf("esc result = %#v", msg)
	}
	if !unlocked {
		t.Fatal("esc keep must release the lock")
	}
}

func TestTUIDemoUnsetLeavesReplayOff(t *testing.T) {
	for _, env := range []string{"", "off", "other"} {
		t.Setenv("SPLICE_TUI_DEMO", env)
		if tuiDemoReplayActive() {
			t.Fatalf("env %q must not activate the demo replay", env)
		}
	}
	t.Setenv("SPLICE_TUI_DEMO", tuiDemoWorktreeReject)
	if !tuiDemoReplayActive() {
		t.Fatal("exact worktree-reject must activate the demo replay")
	}
}

func TestTUIWorktreeRejectReasonMapsThrough(t *testing.T) {
	origPreserve := tuiPreserveWorktree
	origRemove := tuiRemoveWorktree
	origUnlock := tuiUnlockWorktree
	defer func() {
		tuiPreserveWorktree = origPreserve
		tuiRemoveWorktree = origRemove
		tuiUnlockWorktree = origUnlock
	}()
	tuiPreserveWorktree = func(_ context.Context, options worktrees.MergeBackOptions) (string, error) {
		return "splice/" + options.Name, nil
	}
	tuiRemoveWorktree = func(context.Context, worktrees.RemoveOptions) error { return nil }
	tuiUnlockWorktree = func(context.Context, worktrees.UnlockOptions) error { return nil }

	wt := worktrees.Result{Name: "tui-sess-1", Path: "/tmp/wt", RepoRoot: "/tmp/repo", Locked: true}
	cases := []struct {
		answer string
		want   string
	}{
		{answer: worktreeRejectWrongApproach, want: worktreeRejectWrongApproach},
		{answer: worktreeRejectStillFailing, want: worktreeRejectStillFailing},
		{answer: worktreeRejectChangedMind, want: worktreeRejectChangedMind},
		{answer: worktreeRejectOther, want: worktreeRejectOther},
	}
	for _, tc := range cases {
		m := newModel(context.Background(), Options{})
		m, _ = m.maybeOfferWorktreeReview(&wt, false)
		m.pendingAskUser.states[0].answer = worktreeReviewReject
		m2, _ := m.submitAskUser()
		m = m2.(model)
		if m.pendingAskUser == nil || m.pendingAskUser.reviewDecision != worktreeReviewReject {
			t.Fatalf("reason %q: expected reject-reason prompt", tc.answer)
		}
		m.pendingAskUser.states[0].answer = tc.answer
		_, cmd := m.submitAskUser()
		msg := cmd().(worktreeReviewResultMsg)
		if msg.decision != worktreeReviewReject {
			t.Fatalf("reason %q: decision = %q", tc.answer, msg.decision)
		}
		if msg.reason != tc.want {
			t.Fatalf("reason %q: got %q", tc.answer, msg.reason)
		}
		if msg.kept != nil {
			t.Fatalf("reason %q: reject must remove the worktree", tc.answer)
		}
	}
}

func TestTUIWorktreeRejectEscYieldsUnspecified(t *testing.T) {
	origPreserve := tuiPreserveWorktree
	origRemove := tuiRemoveWorktree
	origUnlock := tuiUnlockWorktree
	defer func() {
		tuiPreserveWorktree = origPreserve
		tuiRemoveWorktree = origRemove
		tuiUnlockWorktree = origUnlock
	}()
	removed := false
	tuiPreserveWorktree = func(_ context.Context, options worktrees.MergeBackOptions) (string, error) {
		return "splice/" + options.Name, nil
	}
	tuiRemoveWorktree = func(context.Context, worktrees.RemoveOptions) error {
		removed = true
		return nil
	}
	tuiUnlockWorktree = func(context.Context, worktrees.UnlockOptions) error { return nil }

	wt := worktrees.Result{Name: "tui-sess-1", Path: "/tmp/wt", RepoRoot: "/tmp/repo", Locked: true}
	m := newModel(context.Background(), Options{})
	m, _ = m.maybeOfferWorktreeReview(&wt, false)
	m.pendingAskUser.states[0].answer = worktreeReviewReject
	m2, _ := m.submitAskUser()
	m = m2.(model)
	// Esc on the reason prompt means unspecified and still removes.
	updated, cmd := m.Update(testKey(tea.KeyEsc))
	next := updated.(model)
	if next.pendingAskUser != nil {
		t.Fatal("esc should dismiss the reason prompt")
	}
	msg := cmd().(worktreeReviewResultMsg)
	if msg.reason != worktreeRejectUnspecified {
		t.Fatalf("reason = %q, want unspecified", msg.reason)
	}
	if !removed {
		t.Fatal("esc with no reason must still remove the worktree")
	}
}

func TestTUIWorktreeReviewAcceptKeepSkipReasonPrompt(t *testing.T) {
	wt := worktrees.Result{Name: "tui-sess-1", Path: "/tmp/wt", RepoRoot: "/tmp/repo", Locked: true}
	for _, answer := range []string{worktreeReviewAccept, worktreeReviewKeep} {
		m := newModel(context.Background(), Options{})
		m, _ = m.maybeOfferWorktreeReview(&wt, false)
		m.pendingAskUser.states[0].answer = answer
		submittedModel, _ := m.submitAskUser()
		submitted := submittedModel.(model)
		if submitted.pendingAskUser != nil {
			t.Fatalf("%s must not show the reject-reason prompt", answer)
		}
	}
}

func TestTUIDemoPipelineRunOffersReview(t *testing.T) {
	t.Setenv("SPLICE_TUI_DEMO", tuiDemoWorktreeReject)
	origPause := demoStepPause
	origRun := tuiSpliceRun
	origDisable := disableTUIWorktreesForTest
	demoStepPause = 0
	tuiSpliceRun = tuiSpliceRunOrDemo
	disableTUIWorktreesForTest = false
	defer func() {
		demoStepPause = origPause
		tuiSpliceRun = origRun
		disableTUIWorktreesForTest = origDisable
	}()

	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=demo", "GIT_AUTHOR_EMAIL=demo@local", "GIT_COMMITTER_NAME=demo", "GIT_COMMITTER_EMAIL=demo@local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "demo@local")
	runGit("config", "user.name", "demo")
	if err := os.WriteFile(filepath.Join(repo, "add.go"), []byte("package add\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "seed")

	m := newModel(context.Background(), Options{Cwd: repo, Provider: &fakeProvider{}})
	m.activeRunID = 1
	msg := m.runAgentWithOptions(1, context.Background(), "fix the failing test", nil, tuiAgentRunOptions{})()
	resp, ok := msg.(agentResponseMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if resp.err != nil {
		t.Fatalf("run: %v", resp.err)
	}
	if resp.worktree == nil || resp.worktree.Path == "" {
		t.Fatalf("expected prepared worktree, notice=%q", resp.worktreeNotice)
	}
	updated, _ := m.Update(resp)
	next := updated.(model)
	if next.pendingAskUser == nil || !next.pendingAskUser.keepOnEsc {
		t.Fatal("expected worktree review after demo pipeline run")
	}
}

func TestTUIDemoReplayEmitsStepBack(t *testing.T) {
	t.Setenv("SPLICE_TUI_DEMO", tuiDemoWorktreeReject)
	origPause := demoStepPause
	demoStepPause = 0
	defer func() { demoStepPause = origPause }()
	var events []agent.StageEvent
	result, err := replayWorktreeRejectDemo(context.Background(), agent.Options{
		OnStageEvent: func(event agent.StageEvent) { events = append(events, event) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer == "" {
		t.Fatal("expected a final answer")
	}
	skipped := false
	for _, event := range events {
		if event.Status == "skipped" {
			skipped = true
		}
	}
	if !skipped {
		t.Fatal("demo replay must emit a skipped step-back event")
	}
}

func TestTUICancelledRunUnlocksWorktree(t *testing.T) {
	origUnlock := tuiUnlockWorktree
	defer func() { tuiUnlockWorktree = origUnlock }()
	unlocked := false
	tuiUnlockWorktree = func(context.Context, worktrees.UnlockOptions) error {
		unlocked = true
		return nil
	}
	m := newModel(context.Background(), Options{})
	m.activeRunID = 2
	wt := &worktrees.Result{Name: "tui-old", Path: "/tmp/wt", RepoRoot: "/tmp/repo", Locked: true}
	_, _ = m.Update(agentResponseMsg{runID: 1, worktree: wt})
	if !unlocked {
		t.Fatal("cancelled run must release the worktree lock")
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
