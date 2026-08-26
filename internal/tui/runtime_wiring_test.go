package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestRunPathsFollowCallbackPolicies(t *testing.T) {
	root := t.TempDir()
	store := testSessionStore(t)
	provider := &fakeProvider{}

	var execOptions agent.Options
	execModel := newDesignModeTestModel(root, provider, store)
	execModel.designMode = false
	execModel.captureRunOptions = func(options agent.Options) { execOptions = options }
	if msg := execModel.runAgentWithOptions(1, context.Background(), "run", nil, tuiAgentRunOptions{})(); msg == nil {
		t.Fatal("expected exec run result")
	}

	session, err := store.Create(sessions.CreateInput{SessionID: "runtime-wiring-approve", Cwd: root})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	approveModel := newDesignModeTestModel(root, provider, store)
	approveModel.activeSession = session
	approveModel.pendingPlan = &schemas.DesignPlan{
		Epic:         "wire callbacks",
		Requirements: []string{"run the task"},
		InScope:      []string{"hello.go"},
		Source:       "authored",
		Tasks:        []schemas.Task{{ID: "task-1", Title: "run", Intent: "create hello.go"}},
	}
	approveModel = persistPlanForApproval(t, approveModel)
	var approveOptions agent.Options
	approveModel.captureRunOptions = func(options agent.Options) { approveOptions = options }
	_, cmd := approveModel.startApprovalConfirmed(splicerun.DesignTransitionSourceManual, "plan-1")
	if cmd == nil {
		t.Fatal("expected approve run command")
	}
	if result := execCmd(cmd); result == nil {
		t.Fatal("expected approve run result")
	} else if run, ok := result.(planExecutionResultMsg); ok && run.err != nil {
		t.Fatalf("approve run failed: %v", run.err)
	}

	type callbackPolicy struct {
		class         string
		execNonNil    bool
		approveNonNil bool
	}
	policies := map[string]callbackPolicy{
		"OnText":              {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnReasoning":         {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnToolCall":          {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnToolCallStart":     {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnToolCallDelta":     {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnPermissionRequest": {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnPermission":        {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnAskUser":           {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnToolResult":        {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnUsage":             {class: "path-specific", execNonNil: true, approveNonNil: false},
		"OnCompactionUsage":   {class: "intentionally-nil", execNonNil: false, approveNonNil: false},
		"OnAttributedUsage":   {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnToolProgress":      {class: "path-specific", execNonNil: true, approveNonNil: false},
		"OnToolOutput":        {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnContext":           {class: "intentionally-nil", execNonNil: false, approveNonNil: false},
		"OnSurfaceToUser":     {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnPipelinePlan":      {class: "shared-live", execNonNil: true, approveNonNil: true},
		"OnStageEvent":        {class: "shared-live", execNonNil: true, approveNonNil: true},
		// OnPresentationState is exec-path-only for now: the CLI wires it to
		// the presentation accumulator (P1.1). The TUI joins in P1.2 when it
		// renders from presentation state, at which point this becomes
		// shared-live.
		"OnPresentationState": {class: "path-specific", execNonNil: true, approveNonNil: false},
	}

	execValue := reflect.ValueOf(execOptions)
	approveValue := reflect.ValueOf(approveOptions)
	optionsType := execValue.Type()
	seen := make(map[string]bool, len(policies))
	for index := 0; index < execValue.NumField(); index++ {
		field := optionsType.Field(index)
		if !strings.HasPrefix(field.Name, "On") || field.Type.Kind() != reflect.Func {
			continue
		}
		policy, ok := policies[field.Name]
		if !ok {
			t.Errorf("callback %s has no explicit runtime policy", field.Name)
			continue
		}
		seen[field.Name] = true
		execNonNil := !execValue.Field(index).IsNil()
		approveNonNil := !approveValue.Field(index).IsNil()
		if execNonNil != policy.execNonNil || approveNonNil != policy.approveNonNil {
			t.Errorf("%s (%s): exec non-nil=%v approve non-nil=%v, want %v/%v", field.Name, policy.class, execNonNil, approveNonNil, policy.execNonNil, policy.approveNonNil)
		}
	}
	for name := range policies {
		if !seen[name] {
			t.Errorf("callback policy %s does not match an agent.Options func field", name)
		}
	}
}

func TestApprovePathFeedsPipelinePanel(t *testing.T) {
	root := t.TempDir()
	store := testSessionStore(t)
	provider := &fakeProvider{events: approvePlanEvents()}
	m := newDesignModeTestModel(root, provider, store)
	var messages []tea.Msg
	m.runtimeMessageSink = func(msg tea.Msg) {
		messages = append(messages, msg)
		if prompt, ok := msg.(permissionRequestMsg); ok {
			prompt.decide(agent.PermissionDecision{Action: agent.PermissionDecisionAllow})
		}
	}
	session, err := store.Create(sessions.CreateInput{SessionID: "pipeline-panel-approve", Cwd: root})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = session
	m.pendingPlan = &schemas.DesignPlan{
		Epic:         "pipeline panel",
		Requirements: []string{"write hello"},
		InScope:      []string{"hello.go"},
		Source:       "authored",
		Tasks:        []schemas.Task{{ID: "task-1", Title: "Write hello", Intent: "Create hello.go"}},
	}
	m = persistPlanForApproval(t, m)
	started, cmd := m.startApprovalConfirmed(splicerun.DesignTransitionSourceManual, "plan-1")
	if cmd == nil {
		t.Fatal("expected approve run command")
	}
	resultMsg := execCmd(cmd)
	if resultMsg == nil {
		t.Fatal("expected approve run result")
	}
	if result, ok := resultMsg.(planExecutionResultMsg); ok && result.err != nil {
		t.Fatalf("approve run failed: %v", result.err)
	}
	planAt, stageAt := -1, -1
	for i, msg := range messages {
		switch msg.(type) {
		case pipelinePlanMsg:
			if planAt == -1 {
				planAt = i
			}
		case pipelineStageEventMsg:
			if stageAt == -1 {
				stageAt = i
			}
		}
	}
	if planAt < 0 || stageAt < 0 || planAt >= stageAt {
		t.Fatalf("runtime message order: plan=%d stage=%d, want plan before stage", planAt, stageAt)
	}
	for _, msg := range messages {
		updated, _ := started.Update(msg)
		started = updated.(model)
	}
	if len(started.pipeline.stages) == 0 {
		t.Fatalf("pipeline panel is empty; messages=%T", messages)
	}
	stage := started.pipeline.stages[0]
	if !strings.Contains(stage.name, "code_writer") {
		t.Fatalf("stage name = %q, want code_writer", stage.name)
	}
	if stage.status == pipelineStageIncomplete {
		t.Fatalf("stage status = %v, want a live or completed stage", stage.status)
	}
}

func TestReasoningFilterDropsStageMarkers(t *testing.T) {
	assertNoStageMarker := func(t *testing.T, messages []tea.Msg, transcript []transcriptRow) {
		t.Helper()
		for _, msg := range messages {
			if reasoning, ok := msg.(agentReasoningMsg); ok && strings.HasPrefix(reasoning.delta, "\x00STAGE") {
				t.Fatalf("stage marker reached reasoning message: %q", reasoning.delta)
			}
		}
		for _, row := range transcript {
			if strings.Contains(row.text, "\x00STAGE") {
				t.Fatalf("stage marker reached transcript: %q", row.text)
			}
		}
	}

	t.Run("exec", func(t *testing.T) {
		provider := &fakeProvider{events: []zeroruntime.StreamEvent{
			{Type: zeroruntime.StreamEventReasoning, Content: "\x00STAGE{\"name\":\"code_writer\"}"},
			{Type: zeroruntime.StreamEventReasoning, Content: "visible reasoning"},
			{Type: zeroruntime.StreamEventDone},
		}}
		var messages []tea.Msg
		m := newDesignModeTestModel(t.TempDir(), provider, testSessionStore(t))
		m.designMode = false
		m.runtimeMessageSink = func(msg tea.Msg) { messages = append(messages, msg) }
		msg := m.runAgentWithOptions(1, context.Background(), "run", nil, tuiAgentRunOptions{})()
		response, ok := msg.(agentResponseMsg)
		if !ok {
			t.Fatalf("run result = %T, want agentResponseMsg", msg)
		}
		assertNoStageMarker(t, messages, response.rows)
	})

	t.Run("approve", func(t *testing.T) {
		events := append([]zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventReasoning, Content: "\x00STAGE{\"name\":\"code_writer\"}"}}, approvePlanEvents()...)
		provider := &fakeProvider{events: events}
		store := testSessionStore(t)
		root := t.TempDir()
		m := newDesignModeTestModel(root, provider, store)
		var messages []tea.Msg
		m.runtimeMessageSink = func(msg tea.Msg) {
			messages = append(messages, msg)
			if prompt, ok := msg.(permissionRequestMsg); ok {
				prompt.decide(agent.PermissionDecision{Action: agent.PermissionDecisionAllow})
			}
		}
		session, err := store.Create(sessions.CreateInput{SessionID: "reasoning-filter-approve", Cwd: root})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		m.activeSession = session
		m.pendingPlan = &schemas.DesignPlan{Epic: "filter", Requirements: []string{"write"}, InScope: []string{"hello.go"}, Source: "authored", Tasks: []schemas.Task{{ID: "task-1", Title: "Write", Intent: "write"}}}
		m = persistPlanForApproval(t, m)
		started, cmd := m.startApprovalConfirmed(splicerun.DesignTransitionSourceManual, "plan-1")
		if cmd == nil {
			t.Fatal("expected approve run command")
		}
		resultMsg := execCmd(cmd)
		if result, ok := resultMsg.(planExecutionResultMsg); ok && result.err != nil {
			t.Fatalf("approve run failed: %v", result.err)
		}
		for _, live := range messages {
			updated, _ := started.Update(live)
			started = updated.(model)
		}
		assertNoStageMarker(t, messages, started.transcript)
	})
}

func TestReasoningRowPrecedesAssistantTextStream(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventReasoning, Content: "private thought"},
		{Type: zeroruntime.StreamEventText, Content: "public answer"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, testSessionStore(t))
	m.designMode = false
	var order []string
	m.runtimeMessageSink = func(msg tea.Msg) {
		switch typed := msg.(type) {
		case agentTextMsg:
			order = append(order, "text")
		case agentRowMsg:
			if typed.row.kind == rowReasoning {
				order = append(order, "reasoning-row")
			}
		}
	}
	if msg := m.runAgentWithOptions(1, context.Background(), "run", nil, tuiAgentRunOptions{})(); msg == nil {
		t.Fatal("expected exec run result")
	}
	if len(order) < 2 {
		t.Fatalf("stream order = %v, want reasoning-row and text", order)
	}
	if order[0] != "reasoning-row" || order[1] != "text" {
		t.Fatalf("stream order = %v, want [reasoning-row text]", order)
	}
}

func TestDecorateDoesNotManufactureUnownedCallbacks(t *testing.T) {
	decorated := (runtimeWiring{}).decorate(agent.Options{})
	value := reflect.ValueOf(decorated)
	for _, name := range []string{
		"OnToolCall",
		"OnPermission",
		"OnToolResult",
		"OnUsage",
		"OnCompactionUsage",
		"OnAttributedUsage",
		"OnToolProgress",
		"OnContext",
	} {
		if !value.FieldByName(name).IsNil() {
			t.Errorf("%s became non-nil without shared behavior", name)
		}
	}
}

func TestDecorateTransportsPipelinePlan(t *testing.T) {
	var messages []tea.Msg
	decorated := (runtimeWiring{runID: 7, send: func(msg tea.Msg) { messages = append(messages, msg) }}).decorate(agent.Options{})
	decorated.OnPipelinePlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner"}})

	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	msg, ok := messages[0].(pipelinePlanMsg)
	if !ok {
		t.Fatalf("message = %T, want pipelinePlanMsg", messages[0])
	}
	if msg.runID != 7 || !reflect.DeepEqual(msg.event.Stages, []string{"code_writer", "test_runner"}) {
		t.Fatalf("pipeline plan message = %#v", msg)
	}
}

func TestDecorateCallsPriorCallbacksOnce(t *testing.T) {
	calls := map[string]int{}
	increment := func(name string) { calls[name]++ }
	options := agent.Options{
		OnText: func(string) { increment("text") },
		OnReasoning: func(string) {
			increment("reasoning")
		},
		OnToolCallStart: func(string, string) { increment("tool-start") },
		OnToolCallDelta: func(string, string) { increment("tool-delta") },
		OnPermissionRequest: func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error) {
			increment("permission-request")
			return agent.PermissionDecision{Action: agent.PermissionDecisionAllow}, nil
		},
		OnAskUser: func(context.Context, agent.AskUserRequest) (agent.AskUserResponse, error) {
			increment("ask-user")
			return agent.AskUserResponse{}, nil
		},
		OnToolOutput: func(tools.OutputSnapshot) { increment("tool-output") },
		OnSurfaceToUser: func(context.Context, agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error) {
			increment("surface")
			return agent.SurfaceToUserDecision{Action: agent.SurfaceToUserContinue}, nil
		},
		OnPipelinePlan: func(agent.PipelinePlanEvent) { increment("pipeline-plan") },
		OnStageEvent:   func(agent.StageEvent) { increment("stage") },
	}
	decorated := (runtimeWiring{runID: 11, send: func(tea.Msg) {}, beforeText: func(string) { increment("before-text") }}).decorate(options)
	decorated.OnText("text")
	decorated.OnReasoning("reasoning")
	decorated.OnToolCallStart("call", "write_file")
	decorated.OnToolCallDelta("call", "fragment")
	if _, err := decorated.OnPermissionRequest(context.Background(), agent.PermissionRequest{}); err != nil {
		t.Fatalf("permission callback: %v", err)
	}
	if _, err := decorated.OnAskUser(context.Background(), agent.AskUserRequest{}); err != nil {
		t.Fatalf("ask callback: %v", err)
	}
	decorated.OnToolOutput(tools.OutputSnapshot{})
	if _, err := decorated.OnSurfaceToUser(context.Background(), agent.SurfaceToUserRequest{}); err != nil {
		t.Fatalf("surface callback: %v", err)
	}
	decorated.OnPipelinePlan(agent.PipelinePlanEvent{Stages: []string{"code_writer"}})
	decorated.OnStageEvent(agent.StageEvent{Name: "code_writer", Status: "running"})

	for _, name := range []string{"before-text", "text", "reasoning", "tool-start", "tool-delta", "permission-request", "ask-user", "tool-output", "surface", "pipeline-plan", "stage"} {
		if calls[name] != 1 {
			t.Errorf("%s calls = %d, want 1", name, calls[name])
		}
	}
}

func TestDecorateProvidesSurfaceToUserFallback(t *testing.T) {
	decorated := (runtimeWiring{runID: 17, send: func(msg tea.Msg) {
		prompt, ok := msg.(askUserRequestMsg)
		if !ok {
			return
		}
		prompt.answer([]string{"continue with focused tests"})
	}}).decorate(agent.Options{})
	decision, err := decorated.OnSurfaceToUser(context.Background(), agent.SurfaceToUserRequest{RunID: "run", Iteration: 2})
	if err != nil {
		t.Fatalf("surface fallback: %v", err)
	}
	if decision.Action != agent.SurfaceToUserContinue || decision.Message != "continue with focused tests" {
		t.Fatalf("surface decision = %#v", decision)
	}
}
