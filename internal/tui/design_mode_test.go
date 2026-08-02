package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestDesignCommandEntersDesignMode(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.input.SetValue("/design")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatalf("expected /design to return immediately, got cmd %v", cmd)
	}
	if !next.designMode {
		t.Fatalf("expected designMode to be true, got false")
	}
	if !transcriptContains(next.transcript, "Design conversation") {
		t.Fatalf("expected design welcome in transcript, got %#v", next.transcript)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one session, got %d", len(list))
	}
	events, err := store.ReadEvents(list[0].SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if !eventTypesContain(events, sessions.EventDesignModeEntered) {
		t.Fatalf("expected design_mode_entered event, got %#v", eventTypes(events))
	}
}

func TestDesignCommandBlockedWhilePending(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))
	m.designMode = false
	m.pending = true
	m.input.SetValue("/design")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.designMode {
		t.Fatal("expected designMode to stay false while pending")
	}
	if !transcriptContains(next.transcript, "Cannot enter design mode while a run is active") {
		t.Fatalf("expected blocked message, got %#v", next.transcript)
	}
}

func TestExecCommandLeavesDesignMode(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))
	m.designMode = true
	m.input.SetValue("/exec")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatalf("expected empty /exec to return immediately, got cmd %v", cmd)
	}
	if next.designMode {
		t.Fatal("expected designMode to be false after /exec")
	}
	if !transcriptContains(next.transcript, "Execution mode") {
		t.Fatalf("expected exec welcome in transcript, got %#v", next.transcript)
	}
}

func TestExecCommandRunsPrompt(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: " Running in exec mode"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	m.designMode = true
	m.input.SetValue("/exec implement the plan")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected /exec <prompt> to start a run")
	}
	if next.designMode {
		t.Fatal("expected designMode to be false after exec prompt")
	}

	updated, _ = next.Update(execCmd(cmd))
	next = updated.(model)
	if !transcriptContains(next.transcript, " Running in exec mode") {
		t.Fatalf("expected exec response in transcript, got %#v", next.transcript)
	}
}

func TestDesignConversationRegistryIsReadOnly(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(t.TempDir()) {
		registry.Register(tool)
	}
	registry.Register(tools.NewToolSearchTool(registry))
	registry.Register(designRegistryTestTool{name: "Task"})
	filtered := designConversationRegistry(registry)

	for _, name := range []string{
		"read_file", "list_directory", "grep", "ask_user", "read_minified_file",
		"glob", "lsp_navigate", "skill", "web_fetch", tools.ToolSearchToolName,
	} {
		if _, ok := filtered.Get(name); !ok {
			t.Fatalf("expected %s to be in design conversation registry", name)
		}
	}
	for _, name := range []string{"write_file", "edit_file", "bash", "Task"} {
		if _, ok := filtered.Get(name); ok {
			t.Fatalf("expected %s to be excluded from design conversation registry", name)
		}
	}
}

type designRegistryTestTool struct{ name string }

func (tool designRegistryTestTool) Name() string             { return tool.name }
func (tool designRegistryTestTool) Description() string      { return "test tool" }
func (tool designRegistryTestTool) Parameters() tools.Schema { return tools.Schema{} }
func (tool designRegistryTestTool) Safety() tools.Safety     { return tools.Safety{} }
func (tool designRegistryTestTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{}
}

func TestDesignRunUsesSessionPermissionMode(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventDone}}}
	m := newDesignModeTestModel(t.TempDir(), provider, testSessionStore(t))
	m.permissionMode = agent.PermissionModeAuto
	var captured agent.Options
	m.captureRunOptions = func(options agent.Options) { captured = options }
	m.input.SetValue("research this change")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected design prompt to start an agent run")
	}
	_, _ = updated.(model).Update(execCmd(cmd))
	if captured.PermissionMode != agent.PermissionModeAuto {
		t.Fatalf("design run permission mode = %q, want %q", captured.PermissionMode, agent.PermissionModeAuto)
	}
}

func TestDesignRunRequestsWebSearchServerTool(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventText, Content: "done"}, {Type: zeroruntime.StreamEventDone}}}
	m := newDesignModeTestModel(t.TempDir(), provider, testSessionStore(t))
	m.designMode = true
	var captured agent.Options
	m.captureRunOptions = func(options agent.Options) { captured = options }
	m.input.SetValue("research this change")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected design prompt to start an agent run")
	}
	_, _ = updated.(model).Update(execCmd(cmd))
	if len(captured.ServerTools) != 1 || captured.ServerTools[0].Kind != zeroruntime.ServerToolWebSearch {
		t.Fatalf("design ServerTools = %#v, want web_search", captured.ServerTools)
	}
}

func TestPlainRunDoesNotRequestWebSearchServerTool(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventText, Content: "done"}, {Type: zeroruntime.StreamEventDone}}}
	m := newDesignModeTestModel(t.TempDir(), provider, testSessionStore(t))
	m.designMode = false
	var captured agent.Options
	m.captureRunOptions = func(options agent.Options) { captured = options }
	m.input.SetValue("answer this")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected prompt to start an agent run")
	}
	_, _ = updated.(model).Update(execCmd(cmd))
	if len(captured.ServerTools) != 0 {
		t.Fatalf("plain-run ServerTools = %#v, want empty", captured.ServerTools)
	}
}

func TestDesignConversationRegistryNilIsEmpty(t *testing.T) {
	filtered := designConversationRegistry(nil)
	if len(filtered.All()) != 0 {
		t.Fatalf("expected nil registry to produce empty registry, got %d tools", len(filtered.All()))
	}
}

func TestCrystallizeCommandRequiresDesignMode(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))
	m.designMode = false

	updated, cmd := m.handleCrystallizeCommand()
	if cmd != nil {
		t.Fatalf("expected no cmd, got %v", cmd)
	}
	if !transcriptContains(updated.transcript, "Must be in design mode") {
		t.Fatalf("expected design mode error, got %#v", updated.transcript)
	}
}

func TestCrystallizeCommandBlockedWhilePending(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))
	m.designMode = true
	m.pending = true

	updated, cmd := m.handleCrystallizeCommand()
	if cmd != nil {
		t.Fatalf("expected no cmd, got %v", cmd)
	}
	if !transcriptContains(updated.transcript, "Cannot crystallize while a run is active") {
		t.Fatalf("expected pending error, got %#v", updated.transcript)
	}
}

func TestCrystallizeCommandEmitsResultMessage(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.designMode = true
	sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess

	_, cmd := m.handleCrystallizeCommand()
	if cmd == nil {
		t.Fatal("expected /crystallize to return a cmd")
	}
	msg := execCmd(cmd)
	if _, ok := msg.(crystallizeResultMsg); !ok {
		t.Fatalf("expected crystallizeResultMsg, got %T", msg)
	}
}

func TestCrystallizeResultMessageCarriesRoutedUsage(t *testing.T) {
	store := testSessionStore(t)
	planArgs, _ := json.Marshal(schemas.DesignPlan{
		Epic:         "Build a feature",
		Requirements: []string{"must work"},
		InScope:      []string{"backend"},
		OutOfScope:   []string{"frontend"},
		SystemDesign: "Use Go.",
		Tasks:        []schemas.Task{{ID: "t1", Title: "Implement it", Intent: "Write code"}},
	})
	critiqueArgs, _ := json.Marshal(schemas.PlanCritique{OverallAssessment: "Looks good"})
	crystallizeProvider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "plan", ToolName: "submit_design_plan"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "plan", ArgumentsFragment: string(planArgs)},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "plan"},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 5}},
		{Type: zeroruntime.StreamEventDone},
	}}
	criticProvider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "critique", ToolName: "submit_critique"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "critique", ArgumentsFragment: string(critiqueArgs)},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "critique"},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 20, OutputTokens: 5}},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newDesignModeTestModel(t.TempDir(), crystallizeProvider, store)
	m.designMode = true
	m.modelCatalog = mustTestModelRegistry(t,
		testModelEntry("routed-plan", 2000, []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat}),
		testModelEntry("routed-critic", 2000, []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat}),
	)
	sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	if _, err := store.AppendEvent(sess.SessionID, sessions.AppendEventInput{Type: sessions.EventDesignModeEntered}); err != nil {
		t.Fatalf("append design mode event: %v", err)
	}
	if _, err := store.AppendEvent(sess.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "build it"}}); err != nil {
		t.Fatalf("append message: %v", err)
	}
	m.sessionEvents, err = store.ReadEvents(sess.SessionID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	m.stageModelResolver = func(stage string) (agent.ModelSelection, error) {
		if stage == "design_crystallize" {
			return agent.ModelSelection{Provider: crystallizeProvider, ProviderName: "provider-a", Model: "routed-plan"}, nil
		}
		return agent.ModelSelection{Provider: criticProvider, ProviderName: "provider-b", Model: "routed-critic"}, nil
	}
	_, cmd := m.handleCrystallizeCommand()
	msg, ok := execCmd(cmd).(crystallizeResultMsg)
	if !ok {
		t.Fatalf("expected crystallizeResultMsg")
	}
	if msg.err != nil {
		t.Fatalf("crystallize failed: %v", msg.err)
	}
	var payloads []map[string]any
	for _, event := range msg.sessionEvents {
		if event.Type == sessions.EventUsage {
			payloads = append(payloads, event.Payload.(map[string]any))
		}
	}
	if len(payloads) != 2 {
		t.Fatalf("usage events = %d, want 2", len(payloads))
	}
	if payloads[0]["model"] != "routed-plan" || payloads[1]["model"] != "routed-critic" {
		t.Fatalf("usage models = %#v", payloads)
	}
	for _, payload := range payloads {
		cost, ok := payload["costUsd"].(float64)
		if !ok || cost <= 0 {
			t.Fatalf("costUsd = %#v, want positive", payload["costUsd"])
		}
	}
}

func TestCrystallizeResultMsgDisplaysPlan(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 42

	plan := schemas.DesignPlan{
		Epic:         "Build a feature",
		Requirements: []string{"must work"},
		InScope:      []string{"backend"},
		OutOfScope:   []string{"frontend"},
		SystemDesign: "Use Go.",
		Tasks:        []schemas.Task{{ID: "t1", Title: "Implement it", Intent: "Write code"}},
	}
	critique := schemas.PlanCritique{
		OverallAssessment: "Looks good",
		Critiques: []schemas.Critique{
			{Category: "correctness", Severity: schemas.SeverityLow, Issue: "Add tests", SuggestedMitigation: "Write unit tests"},
		},
		MustFixBeforeExecution: false,
	}

	msg := crystallizeResultMsg{runID: 42, plan: plan, critique: critique, store: store, sessionID: sess.SessionID}
	updated, _ := m.Update(msg)
	next := updated.(model)

	if next.pendingPlan == nil || next.pendingPlan.Epic != plan.Epic {
		t.Fatalf("expected pendingPlan set, got %#v", next.pendingPlan)
	}
	if next.pendingCritique == nil || next.pendingCritique.OverallAssessment != critique.OverallAssessment {
		t.Fatalf("expected pendingCritique set, got %#v", next.pendingCritique)
	}
	if !transcriptContains(next.transcript, plan.Epic) {
		t.Fatalf("expected plan epic in transcript, got %#v", next.transcript)
	}
	if !transcriptContains(next.transcript, critique.OverallAssessment) {
		t.Fatalf("expected critique assessment in transcript, got %#v", next.transcript)
	}
}

func TestCrystallizeResultMsgOutcomes(t *testing.T) {
	validPlan := schemas.DesignPlan{
		Epic:         "Build a feature",
		Requirements: []string{"must work"},
		InScope:      []string{"backend"},
		OutOfScope:   []string{"frontend"},
		SystemDesign: "Use Go.",
		Tasks:        []schemas.Task{{ID: "t1", Title: "Implement it", Intent: "Write code"}},
		Source:       "conversation",
	}
	validCritique := schemas.PlanCritique{OverallAssessment: "Looks good"}

	tests := []struct {
		name              string
		plan              schemas.DesignPlan
		critique          schemas.PlanCritique
		err               error
		initialCritique   *schemas.PlanCritique
		wantPlan          bool
		wantCritique      bool
		wantSessionEvents bool
		wantTranscript    []string
		notWantTranscript []string
	}{
		{
			name:              "critic failure keeps valid plan",
			plan:              validPlan,
			err:               fmt.Errorf("critic unavailable"),
			initialCritique:   &schemas.PlanCritique{OverallAssessment: "stale critique"},
			wantPlan:          true,
			wantSessionEvents: true,
			wantTranscript: []string{
				"Plan critique failed: critic unavailable",
				"Plan is ready. Type /approve to execute without a critique.",
				validPlan.Epic,
			},
			notWantTranscript: []string{"Crystallization failed:"},
		},
		{
			name:              "crystallize failure rejects empty plan",
			plan:              schemas.DesignPlan{},
			err:               fmt.Errorf("crystallizer unavailable"),
			wantTranscript:    []string{"Crystallization failed: crystallizer unavailable"},
			notWantTranscript: []string{"Plan is ready."},
		},
		{
			name:              "critique persistence failure keeps must-fix critique",
			plan:              validPlan,
			critique:          schemas.PlanCritique{OverallAssessment: "Needs work", MustFixBeforeExecution: true},
			err:               fmt.Errorf("persist critique_recorded: disk full"),
			wantPlan:          true,
			wantCritique:      true,
			wantSessionEvents: true,
			wantTranscript: []string{
				validPlan.Epic,
				"Needs work",
				"Plan critique not saved: persist critique_recorded: disk full",
				"Critic flagged must-fix issues. /approve is blocked.",
			},
			notWantTranscript: []string{
				"Plan critique failed:",
				"without a critique",
			},
		},
		{
			name:              "clean critique persistence failure keeps critique",
			plan:              validPlan,
			critique:          validCritique,
			err:               fmt.Errorf("persist critique_recorded: disk full"),
			wantPlan:          true,
			wantCritique:      true,
			wantSessionEvents: true,
			wantTranscript:    []string{validCritique.OverallAssessment, "Plan critique not saved: persist critique_recorded: disk full", "Plan is ready. Type /approve to execute."},
			notWantTranscript: []string{"without a critique", "Plan critique failed:"},
		},
		{
			name:              "success stores plan and critique",
			plan:              validPlan,
			critique:          validCritique,
			wantPlan:          true,
			wantCritique:      true,
			wantSessionEvents: true,
			wantTranscript: []string{
				validPlan.Epic,
				validCritique.OverallAssessment,
				"Plan is ready. Type /approve to execute.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testSessionStore(t)
			m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
			sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			if _, err := store.AppendEvent(sess.SessionID, sessions.AppendEventInput{Type: sessions.EventDesignModeEntered}); err != nil {
				t.Fatalf("append session event: %v", err)
			}
			m.activeSession = sess
			m.activeRunID = 42
			m.pendingCritique = tt.initialCritique

			msg := crystallizeResultMsg{
				runID:     42,
				plan:      tt.plan,
				critique:  tt.critique,
				err:       tt.err,
				store:     store,
				sessionID: sess.SessionID,
			}
			updated, _ := m.Update(msg)
			next := updated.(model)

			if got := next.pendingPlan != nil; got != tt.wantPlan {
				t.Fatalf("pendingPlan present = %v, want %v", got, tt.wantPlan)
			}
			if got := next.pendingCritique != nil; got != tt.wantCritique {
				t.Fatalf("pendingCritique present = %v, want %v", got, tt.wantCritique)
			}
			if got := len(next.sessionEvents) > 0; got != tt.wantSessionEvents {
				t.Fatalf("sessionEvents present = %v, want %v", got, tt.wantSessionEvents)
			}
			for _, want := range tt.wantTranscript {
				if !transcriptContains(next.transcript, want) {
					t.Errorf("expected transcript to contain %q, got %#v", want, next.transcript)
				}
			}
			for _, unwanted := range tt.notWantTranscript {
				if transcriptContains(next.transcript, unwanted) {
					t.Errorf("expected transcript not to contain %q, got %#v", unwanted, next.transcript)
				}
			}
		})
	}
}

// TestApproveAfterCriticFailureFindsPlan pins the reported symptom. A failed
// advisory critic discarded a plan that was already on disk, so /approve
// answered "No pending plan". The provider is nil, so the guard chain stops at
// the provider check. That check proves the pending-plan guard passed.
func TestApproveAfterCriticFailureFindsPlan(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "critic-failure", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 7

	updated, _ := m.Update(crystallizeResultMsg{
		runID: 7,
		plan: schemas.DesignPlan{
			Epic:         "Build a feature",
			Requirements: []string{"must work"},
			InScope:      []string{"backend"},
			OutOfScope:   []string{"frontend"},
			SystemDesign: "Use Go.",
			Tasks:        []schemas.Task{{ID: "t1", Title: "Implement it", Intent: "Write code"}},
			Source:       "conversation",
		},
		err:       fmt.Errorf("critic unavailable"),
		store:     store,
		sessionID: sess.SessionID,
	})
	next := updated.(model)
	next.provider = nil

	approved, _ := next.handleApproveCommand()
	if transcriptContains(approved.transcript, "No pending plan") {
		t.Fatalf("approve rejected a plan that survived a critic failure: %#v", approved.transcript)
	}
	if !transcriptContains(approved.transcript, "No provider configured") {
		t.Fatalf("expected the guard chain to reach the provider check, got %#v", approved.transcript)
	}
}

func TestApproveAfterCritiquePersistenceFailureBlocksMustFixPlan(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "critique-persist-failure", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 8

	updated, _ := m.Update(crystallizeResultMsg{
		runID: 8,
		plan: schemas.DesignPlan{
			Epic:         "Build a feature",
			Requirements: []string{"must work"},
			InScope:      []string{"backend"},
			OutOfScope:   []string{"frontend"},
			SystemDesign: "Use Go.",
			Tasks:        []schemas.Task{{ID: "t1", Title: "Implement it", Intent: "Write code"}},
			Source:       "conversation",
		},
		critique: schemas.PlanCritique{
			OverallAssessment:      "Needs work",
			MustFixBeforeExecution: true,
		},
		err:       fmt.Errorf("persist critique_recorded: disk full"),
		store:     store,
		sessionID: sess.SessionID,
	})
	next := updated.(model)

	approved, cmd := next.handleApproveCommand()
	if cmd != nil {
		t.Fatalf("expected approve to be blocked, got cmd %v", cmd)
	}
	if !transcriptContains(approved.transcript, "Plan has must-fix issues. Revise and re-run /crystallize.") {
		t.Fatalf("expected must-fix error, got %#v", approved.transcript)
	}
}

func TestCrystallizeResultMsgMustFixBlocksApprove(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 42

	plan := schemas.DesignPlan{Epic: "Build it"}
	critique := schemas.PlanCritique{
		OverallAssessment:      "Needs work",
		MustFixBeforeExecution: true,
	}

	msg := crystallizeResultMsg{runID: 42, plan: plan, critique: critique, store: store, sessionID: sess.SessionID}
	updated, _ := m.Update(msg)
	next := updated.(model)

	if !transcriptContains(next.transcript, "/approve is blocked") {
		t.Fatalf("expected blocked message, got %#v", next.transcript)
	}
	if transcriptContains(next.transcript, "Plan is ready") {
		t.Fatalf("did not expect ready message, got %#v", next.transcript)
	}
}

func TestApproveCommandRequiresPendingPlan(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))

	updated, cmd := m.handleApproveCommand()
	if cmd != nil {
		t.Fatalf("expected no cmd, got %v", cmd)
	}
	if !transcriptContains(updated.transcript, "No pending plan") {
		t.Fatalf("expected pending plan error, got %#v", updated.transcript)
	}
}

func TestApproveCommandBlockedWhilePending(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))
	m.pendingPlan = &schemas.DesignPlan{Epic: "Build it"}
	m.pending = true

	updated, cmd := m.handleApproveCommand()
	if cmd != nil {
		t.Fatalf("expected no cmd, got %v", cmd)
	}
	if !transcriptContains(updated.transcript, "Cannot approve while a run is active") {
		t.Fatalf("expected pending error, got %#v", updated.transcript)
	}
}

func TestApproveCommandMustFixBlocks(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))
	m.pendingPlan = &schemas.DesignPlan{Epic: "Build it"}
	m.pendingCritique = &schemas.PlanCritique{
		OverallAssessment:      "Needs work",
		MustFixBeforeExecution: true,
	}

	updated, cmd := m.handleApproveCommand()
	if cmd != nil {
		t.Fatalf("expected no cmd, got %v", cmd)
	}
	if !transcriptContains(updated.transcript, "must-fix") {
		t.Fatalf("expected must-fix error, got %#v", updated.transcript)
	}
}

func TestApproveCommandEmitsResultMessage(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.pendingPlan = &schemas.DesignPlan{Epic: "Build it", Source: "authored"}

	_, cmd := m.handleApproveCommand()
	if cmd == nil {
		t.Fatal("expected /approve to return a cmd")
	}
	msg := execCmd(cmd)
	if _, ok := msg.(planExecutionResultMsg); !ok {
		t.Fatalf("expected planExecutionResultMsg, got %T", msg)
	}
}

// This test pins the failure path to refreshing session events. A failed plan
// run still appended the approval, any tasks that started, and each stage's
// usage; returning early on the error left the live session disagreeing with
// its own log for exactly the run a user needs to inspect.
func TestFailedPlanExecutionStillRefreshesSessionEvents(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "failed-plan-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 7
	m.pending = true

	// An event the run appended that the in-memory model has not seen yet.
	if _, err := store.AppendEvent(sess.SessionID, sessions.AppendEventInput{
		Type:    sessions.EventPlanApproved,
		Payload: splicerun.PlanApprovedPayload{PlanID: "plan-failed"},
	}); err != nil {
		t.Fatalf("append plan approved: %v", err)
	}

	next, _ := m.Update(planExecutionResultMsg{
		runID:     7,
		err:       errors.New("task step-1 stopped with status failed"),
		store:     store,
		sessionID: sess.SessionID,
	})
	updated, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	if len(updated.sessionEvents) == 0 {
		t.Fatal("failed plan execution left session events stale (none loaded)")
	}
	if !transcriptContains(updated.transcript, "Plan execution failed") {
		t.Fatalf("expected the failure to be reported, got %#v", updated.transcript)
	}
}

func approvePlanEvents() []zeroruntime.StreamEvent {
	args, _ := json.Marshal(schemas.CodeWriterOutput{
		Files:      []schemas.FileChange{{Path: "hello.go", Content: "package hello\n", ChangeType: "create"}},
		Language:   "go",
		Intent:     "create hello.go",
		Confidence: 0.9,
	})
	return []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "stage text"},
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "submit", ToolName: "submit_code"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "submit", ArgumentsFragment: string(args)},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "submit"},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 5}},
		{Type: zeroruntime.StreamEventDone},
	}
}

// TestApproveAskModeSurfacesPermissionRegression covers the reported bug:
// /approve used to fail every gated tool in ask mode because its permission
// callback was nil instead of surfacing a TUI prompt.
func TestApproveAskModeSurfacesPermissionRegression(t *testing.T) {
	var prompts int
	provider := &fakeProvider{events: approvePlanEvents()}
	store := testSessionStore(t)
	root := t.TempDir()
	m := newDesignModeTestModel(root, provider, store)
	m.runtimeMessageSink = func(msg tea.Msg) {
		if prompt, ok := msg.(permissionRequestMsg); ok {
			prompts++
			prompt.decide(agent.PermissionDecision{Action: agent.PermissionDecisionAllow})
		}
	}
	sess, err := store.Create(sessions.CreateInput{SessionID: "approve-session", Cwd: root})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	tier := schemas.TierTrivial
	m.pendingPlan = &schemas.DesignPlan{Epic: "Approve callbacks", Requirements: []string{"write the file"}, InScope: []string{"hello.go"}, Source: "authored", Tasks: []schemas.Task{{ID: "t1", Title: "Write hello", Intent: "Create hello.go", EstimatedTier: &tier}}}

	_, cmd := m.handleApproveCommand()
	if cmd == nil {
		t.Fatal("expected /approve command")
	}
	msg := execCmd(cmd)
	result, ok := msg.(planExecutionResultMsg)
	if !ok {
		t.Fatalf("expected planExecutionResultMsg, got %T", msg)
	}
	if result.err != nil {
		t.Fatalf("approved plan failed: %v", result.err)
	}
	if prompts != 1 {
		t.Fatalf("permission prompts = %d, want 1", prompts)
	}
}

func TestApprovePlanStreamsTextAndToolCall(t *testing.T) {
	var messages []tea.Msg
	provider := &fakeProvider{events: approvePlanEvents()}
	store := testSessionStore(t)
	root := t.TempDir()
	m := newDesignModeTestModel(root, provider, store)
	m.runtimeMessageSink = func(msg tea.Msg) {
		messages = append(messages, msg)
		if prompt, ok := msg.(permissionRequestMsg); ok {
			prompt.decide(agent.PermissionDecision{Action: agent.PermissionDecisionAllow})
		}
	}
	sess, err := store.Create(sessions.CreateInput{SessionID: "approve-session", Cwd: root})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	tier := schemas.TierTrivial
	m.pendingPlan = &schemas.DesignPlan{Epic: "Approve callbacks", Requirements: []string{"write the file"}, InScope: []string{"hello.go"}, Source: "authored", Tasks: []schemas.Task{{ID: "t1", Title: "Write hello", Intent: "Create hello.go", EstimatedTier: &tier}}}
	started, cmd := m.handleApproveCommand()
	msg := execCmd(cmd)
	for _, live := range messages {
		updated, _ := started.Update(live)
		started = updated.(model)
	}
	if !transcriptContains(started.transcript, "stage text") {
		t.Fatalf("expected stage text in transcript, got %#v", started.transcript)
	}
	foundTool := false
	for _, live := range messages {
		if _, ok := live.(agentRowMsg); ok {
			foundTool = true
			break
		}
		if _, ok := live.(toolCallStreamStartMsg); ok {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatalf("expected tool call card or stream message, got %#v", messages)
	}
	if _, ok := msg.(planExecutionResultMsg); !ok {
		t.Fatalf("expected planExecutionResultMsg, got %T", msg)
	}
}

func TestApprovePlanPersistsAttributedUsage(t *testing.T) {
	provider := &fakeProvider{events: approvePlanEvents()}
	store := testSessionStore(t)
	root := t.TempDir()
	m := newDesignModeTestModel(root, provider, store)
	m.runtimeMessageSink = func(msg tea.Msg) {
		if prompt, ok := msg.(permissionRequestMsg); ok {
			prompt.decide(agent.PermissionDecision{Action: agent.PermissionDecisionAllow})
		}
	}
	sess, err := store.Create(sessions.CreateInput{SessionID: "approve-session", Cwd: root})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	tier := schemas.TierTrivial
	m.pendingPlan = &schemas.DesignPlan{Epic: "Approve callbacks", Requirements: []string{"write the file"}, InScope: []string{"hello.go"}, Source: "authored", Tasks: []schemas.Task{{ID: "t1", Title: "Write hello", Intent: "Create hello.go", EstimatedTier: &tier}}}
	_, cmd := m.handleApproveCommand()
	if msg := execCmd(cmd); msg == nil {
		t.Fatal("expected plan result")
	}
	events, err := store.ReadEvents(m.activeSession.SessionID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if !eventTypesContain(events, sessions.EventUsage) {
		t.Fatalf("expected usage event, got %v", eventTypes(events))
	}
}

func TestApprovePlanCancellationDuringPermissionPromptDoesNotHang(t *testing.T) {
	permissionSeen := make(chan struct{})
	provider := &fakeProvider{events: approvePlanEvents()}
	store := testSessionStore(t)
	root := t.TempDir()
	m := newDesignModeTestModel(root, provider, store)
	m.runtimeMessageSink = func(msg tea.Msg) {
		if _, ok := msg.(permissionRequestMsg); ok {
			close(permissionSeen)
		}
	}
	sess, err := store.Create(sessions.CreateInput{SessionID: "approve-session", Cwd: root})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	tier := schemas.TierTrivial
	m.pendingPlan = &schemas.DesignPlan{Epic: "Approve callbacks", Requirements: []string{"write the file"}, InScope: []string{"hello.go"}, Source: "authored", Tasks: []schemas.Task{{ID: "t1", Title: "Write hello", Intent: "Create hello.go", EstimatedTier: &tier}}}
	started, cmd := m.handleApproveCommand()
	resultCh := make(chan tea.Msg, 1)
	go func() { resultCh <- execCmd(cmd) }()
	select {
	case <-permissionSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("permission prompt did not surface")
	}
	started.runCancel()
	select {
	case <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("approve run hung after cancellation")
	}
}

func TestPlanExecutionResultMsgDisplaysResult(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 42
	m.pendingPlan = &schemas.DesignPlan{Epic: "Build it"}
	m.pendingCritique = &schemas.PlanCritique{OverallAssessment: "Looks good"}

	msg := planExecutionResultMsg{runID: 42, result: agent.Result{FinalAnswer: "All done"}, store: store, sessionID: sess.SessionID}
	updated, _ := m.Update(msg)
	next := updated.(model)

	if next.pendingPlan != nil {
		t.Fatalf("expected pendingPlan cleared, got %#v", next.pendingPlan)
	}
	if next.pendingCritique != nil {
		t.Fatalf("expected pendingCritique cleared, got %#v", next.pendingCritique)
	}
	if !transcriptContains(next.transcript, "All done") {
		t.Fatalf("expected final answer in transcript, got %#v", next.transcript)
	}
}

func newDesignModeTestModel(root string, provider zeroruntime.Provider, store *sessions.Store) model {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(root) {
		registry.Register(tool)
	}
	return newModel(context.Background(), Options{
		Cwd:            root,
		ProviderName:   "openai",
		ModelName:      "gpt-4.1",
		Provider:       provider,
		Registry:       registry,
		SessionStore:   store,
		PermissionMode: agent.PermissionModeAsk,
	})
}

func ptrBool(v bool) *bool { return &v }

func TestCompactionDisabledConfigSetsContextWindowZero(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "ack"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	m.compaction = config.CompactionConfig{Enabled: ptrBool(false)}

	var captured agent.Options
	m.captureRunOptions = func(opts agent.Options) { captured = opts }

	m.input.SetValue("hello")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	updated, _ = next.Update(execCmd(cmd))
	_ = updated.(model)

	if captured.ContextWindow != 0 {
		t.Fatalf("ContextWindow = %d, want 0 when compaction is disabled", captured.ContextWindow)
	}
}

func TestCompactionEnabledConfigPassesReserveAndKeep(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "ack"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	m.compaction = config.CompactionConfig{ReserveTokens: 1234, KeepRecentTokens: 5678}

	var captured agent.Options
	m.captureRunOptions = func(opts agent.Options) { captured = opts }

	m.input.SetValue("hello")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	updated, _ = next.Update(execCmd(cmd))
	_ = updated.(model)

	if captured.ContextWindow == 0 {
		t.Fatal("ContextWindow must be non-zero when compaction is enabled")
	}
	if captured.CompactionReserveTokens != 1234 {
		t.Fatalf("CompactionReserveTokens = %d, want 1234", captured.CompactionReserveTokens)
	}
	if captured.CompactionKeepRecentTokens != 5678 {
		t.Fatalf("CompactionKeepRecentTokens = %d, want 5678", captured.CompactionKeepRecentTokens)
	}
}

func TestCompactionCheapestPricingTierContextWindow(t *testing.T) {
	for _, test := range []struct {
		name       string
		stay       *bool
		withTiers  bool
		wantWindow int
	}{
		{name: "default caps tiered model", wantWindow: 272_000, withTiers: true},
		{name: "explicit false keeps full window", stay: ptrBool(false), wantWindow: 1_050_000, withTiers: true},
		{name: "model without tiers keeps full window", wantWindow: 1_050_000},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := testSessionStore(t)
			provider := &fakeProvider{events: []zeroruntime.StreamEvent{
				{Type: zeroruntime.StreamEventText, Content: "ack"},
				{Type: zeroruntime.StreamEventDone},
			}}
			m := newDesignModeTestModel(t.TempDir(), provider, store)
			entry := testModelEntry("gpt-5.5-like", 1_050_000, []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat})
			if test.withTiers {
				entry.Cost.Tiers = []modelregistry.ModelCostTier{
					{UpToInputTokens: 272_000, InputPerMillion: 5, OutputPerMillion: 30},
					{InputPerMillion: 10, OutputPerMillion: 45},
				}
			}
			m.modelCatalog = mustTestModelRegistry(t, entry)
			m.modelName = entry.ID
			m.compaction.StayInCheapestPricingTier = test.stay

			var captured agent.Options
			m.captureRunOptions = func(opts agent.Options) { captured = opts }
			m.input.SetValue("hello")
			updated, cmd := m.Update(testKey(tea.KeyEnter))
			next := updated.(model)
			if cmd == nil {
				t.Fatal("expected prompt submit to start an agent run")
			}
			_, _ = next.Update(execCmd(cmd))
			if captured.ContextWindow != test.wantWindow {
				t.Fatalf("ContextWindow = %d, want %d", captured.ContextWindow, test.wantWindow)
			}
		})
	}
}

func eventTypesContain(events []sessions.Event, want sessions.EventType) bool {
	for _, e := range events {
		if e.Type == want {
			return true
		}
	}
	return false
}

func TestReconstructDesignState_NoEvents(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))
	m = m.reconstructDesignState()
	if m.designMode {
		t.Fatal("designMode should be false with no events")
	}
	if m.pendingPlan != nil {
		t.Fatal("pendingPlan should be nil with no events")
	}
}

func TestReconstructDesignState_ConversationPhase(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m, err := m.ensureActiveSession("test")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)

	m.designMode = false // simulate a fresh load
	m = m.reconstructDesignState()
	if !m.designMode {
		t.Fatal("designMode should be true after design_mode_entered event")
	}
	if m.pendingPlan != nil {
		t.Fatal("pendingPlan should be nil in conversation phase")
	}
}

func TestReconstructDesignState_ReviewPhase(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m, err := m.ensureActiveSession("test")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)

	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "test epic",
		Requirements: []string{"req"},
		InScope:      []string{"in"},
		OutOfScope:   []string{"out"},
		SystemDesign: "design",
		Tasks:        []schemas.Task{{ID: "t1", Title: "Task 1", Intent: "do it"}},
	}
	planJSON, _ := json.Marshal(plan)
	m, _ = m.appendSessionEvent(sessions.EventPlanCrystallized, splicerun.PlanCrystallizedPayload{
		PlanID:   "plan-1",
		Revision: 1,
		Plan:     planJSON,
	})

	m.designMode = false
	m.pendingPlan = nil
	m = m.reconstructDesignState()
	if !m.designMode {
		t.Fatal("designMode should be true in review phase")
	}
	if m.pendingPlan == nil || m.pendingPlan.Epic != "test epic" {
		t.Fatalf("pendingPlan not reconstructed: %#v", m.pendingPlan)
	}
}

// This test pins resume after plan crystallization when critique recording never occurs.
func TestReconstructDesignState_CrystallizedPlanWithoutCritique(t *testing.T) {
	store := testSessionStore(t)
	sess, err := store.Create(sessions.CreateInput{SessionID: "crystallized-without-critique", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.AppendEvent(sess.SessionID, sessions.AppendEventInput{
		Type: sessions.EventDesignModeEntered,
	}); err != nil {
		t.Fatalf("append design mode event: %v", err)
	}

	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "resume epic",
		Requirements: []string{"req"},
		InScope:      []string{"in"},
		OutOfScope:   []string{"out"},
		SystemDesign: "design",
		Tasks:        []schemas.Task{{ID: "t1", Title: "Task 1", Intent: "do it"}},
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if _, err := store.AppendEvent(sess.SessionID, sessions.AppendEventInput{
		Type: sessions.EventPlanCrystallized,
		Payload: splicerun.PlanCrystallizedPayload{
			PlanID:   "plan-1",
			Revision: 1,
			Plan:     planJSON,
		},
	}); err != nil {
		t.Fatalf("append crystallized plan event: %v", err)
	}

	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.activeSession = sess
	m.sessionEvents, err = store.ReadEvents(sess.SessionID)
	if err != nil {
		t.Fatalf("read session events: %v", err)
	}
	m = m.reconstructDesignState()

	if m.pendingPlan == nil || m.pendingPlan.Epic != plan.Epic {
		t.Fatalf("expected pending plan %q, got %#v", plan.Epic, m.pendingPlan)
	}
	if m.pendingCritique != nil {
		t.Fatalf("expected no pending critique, got %#v", m.pendingCritique)
	}
	if !m.designMode {
		t.Fatal("expected design mode to be enabled after resume")
	}

	m.provider = nil
	approved, cmd := m.handleApproveCommand()
	if cmd != nil {
		t.Fatalf("expected provider guard to stop approve, got cmd %v", cmd)
	}
	if transcriptContains(approved.transcript, "No pending plan") {
		t.Fatalf("approve rejected the resumed plan: %#v", approved.transcript)
	}
	if !transcriptContains(approved.transcript, "No provider configured") {
		t.Fatalf("expected provider guard after resume, got %#v", approved.transcript)
	}
}

func TestReconstructDesignState_ExecutingPhase(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m, err := m.ensureActiveSession("test")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	plan := schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "test",
		Requirements: []string{"req"},
		InScope:      []string{"in"},
		OutOfScope:   []string{"out"},
		SystemDesign: "design",
		Tasks:        []schemas.Task{{ID: "t1", Title: "T", Intent: "i"}},
	}
	planJSON, _ := json.Marshal(plan)
	m, _ = m.appendSessionEvent(sessions.EventPlanCrystallized, splicerun.PlanCrystallizedPayload{
		PlanID: "plan-1", Revision: 1, Plan: planJSON,
	})
	m, _ = m.appendSessionEvent(sessions.EventPlanApproved, splicerun.PlanApprovedPayload{PlanID: "plan-1"})

	m.designMode = true
	m = m.reconstructDesignState()
	if m.designMode {
		t.Fatal("designMode should be false in executing phase")
	}
}

func TestStartNewSessionClearsDesignState(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m, err := m.ensureActiveSession("test")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m.designMode = true
	m.pendingPlan = &schemas.DesignPlan{Epic: "stale"}
	m.pendingCritique = &schemas.PlanCritique{OverallAssessment: "stale"}
	m.memoryStatus = "active"
	m.memoryCount = 99
	m.memoryByType = map[string]int{"decision": 99}
	m.memoryNoticed = true

	m = m.startNewSession()
	if !m.designMode {
		t.Fatal("designMode should be true after /new")
	}
	if m.pendingPlan != nil {
		t.Fatal("pendingPlan should be nil after /new")
	}
	if m.pendingCritique != nil {
		t.Fatal("pendingCritique should be nil after /new")
	}
	if m.memoryStatus != "" {
		t.Fatal("memoryStatus should be reset after /new")
	}
	if m.memoryCount != 0 {
		t.Fatal("memoryCount should be 0 after /new")
	}
	if m.memoryByType != nil {
		t.Fatal("memoryByType should be nil after /new")
	}
	if m.memoryNoticed {
		t.Fatal("memoryNoticed should be false after /new")
	}
}

func TestPlanExecutionResultMsgErrorPreservesDesignState(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 42
	m.designMode = true
	m.pendingPlan = &schemas.DesignPlan{Epic: "Build it"}
	m.pendingCritique = &schemas.PlanCritique{OverallAssessment: "Looks good"}

	// A failed execution must NOT clear design mode or pending plan: the user
	// needs to stay in the review state to revise and re-crystallize.
	msg := planExecutionResultMsg{runID: 42, err: fmt.Errorf("task t1: pipeline failed")}
	updated, _ := m.Update(msg)
	next := updated.(model)

	if !next.designMode {
		t.Fatal("designMode should stay true after execution error so user can revise")
	}
	if next.pendingPlan == nil {
		t.Fatal("pendingPlan should be preserved after execution error")
	}
	if next.pendingCritique == nil {
		t.Fatal("pendingCritique should be preserved after execution error")
	}
	if !transcriptContains(next.transcript, "Plan execution failed") {
		t.Fatalf("expected error in transcript, got %#v", next.transcript)
	}
}

func TestPlanExecutionResultMsgSuccessClearsDesignState(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	sess, err := store.Create(sessions.CreateInput{SessionID: "test-session", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess
	m.activeRunID = 42
	m.designMode = true
	m.pendingPlan = &schemas.DesignPlan{Epic: "Build it"}
	m.pendingCritique = &schemas.PlanCritique{OverallAssessment: "Looks good"}

	msg := planExecutionResultMsg{runID: 42, result: agent.Result{FinalAnswer: "All done"}, store: store, sessionID: sess.SessionID}
	updated, _ := m.Update(msg)
	next := updated.(model)

	if next.designMode {
		t.Fatal("designMode should be false after successful execution")
	}
	if next.pendingPlan != nil {
		t.Fatal("pendingPlan should be nil after successful execution")
	}
	if next.pendingCritique != nil {
		t.Fatal("pendingCritique should be nil after successful execution")
	}
}

func TestFreshSessionComposeRoutesToDesignConversation(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "hello from design"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	m.input.SetValue("hello")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	if !next.designMode {
		t.Fatal("fresh session should stay in design mode after submit")
	}

	updated, _ = next.Update(execCmd(cmd))
	next = updated.(model)
	if !transcriptContains(next.transcript, "hello from design") {
		t.Fatalf("expected design response in transcript, got %#v", next.transcript)
	}
	if len(provider.requests) == 0 {
		t.Fatal("expected provider request")
	}
	systemPrompt := provider.requests[0].Messages[0].Content
	if !strings.Contains(systemPrompt, "Design Conversation agent") {
		t.Fatalf("expected design conversation system prompt, got:\n%s", systemPrompt)
	}
}

func TestDesignRunPassesFullPriorContent(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "ack"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	m, err := m.ensureActiveSession("test")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m.designNoticeShown = true // do not let launchPrompt record a second design epoch
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{
		"role":    "user",
		"content": "What should we build?",
	})
	longAssistant := strings.Repeat("a", 1200)
	m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{
		"role":    "assistant",
		"content": longAssistant,
	})
	if len(longAssistant) <= 500 {
		t.Fatal("test setup: assistant content must exceed the old truncation cap")
	}

	m.input.SetValue("refine the plan")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	if !next.designMode {
		t.Fatal("expected design mode to stay active")
	}

	updated, _ = next.Update(execCmd(cmd))
	next = updated.(model)
	if !transcriptContains(next.transcript, "ack") {
		t.Fatalf("expected design response in transcript, got %#v", next.transcript)
	}
	if len(provider.requests) == 0 {
		t.Fatal("expected provider request")
	}

	var foundPrior bool
	var lastUser string
	for _, msg := range provider.requests[0].Messages {
		if msg.Role == zeroruntime.MessageRoleAssistant && msg.Content == longAssistant {
			foundPrior = true
		}
		if msg.Role == zeroruntime.MessageRoleUser {
			lastUser = msg.Content
		}
	}
	if !foundPrior {
		t.Fatalf("expected full prior assistant content in messages, got %+v", provider.requests[0].Messages)
	}
	if !strings.Contains(lastUser, "refine the plan") {
		t.Fatalf("expected current raw prompt as final user message, got %q", lastUser)
	}
}

func TestExecPromptBypassesDesignMode(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "hello from exec"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	m.input.SetValue("/exec run through pipeline")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected /exec <prompt> to start a run")
	}
	if next.designMode {
		t.Fatal("expected designMode to be false after /exec <prompt>")
	}

	updated, _ = next.Update(execCmd(cmd))
	next = updated.(model)
	if !transcriptContains(next.transcript, "hello from exec") {
		t.Fatalf("expected exec response in transcript, got %#v", next.transcript)
	}
	if len(provider.requests) == 0 {
		t.Fatal("expected provider request")
	}
	systemPrompt := provider.requests[0].Messages[0].Content
	if strings.Contains(systemPrompt, "Design Conversation agent") {
		t.Fatal("exec path should not use design conversation prompt")
	}
}

func TestDesignAfterExecReturnsToDesignMode(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))
	m.designMode = false
	m.input.SetValue("/design")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if !next.designMode {
		t.Fatal("expected /design to re-enter design mode")
	}
}

// --- CP3: persistent plan panel ---

func TestLayoutCommandTogglesPersistentPlanPanel(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.designMode = false // start with the toggle off regardless of the CP4 default
	if m.planPanelPersistent {
		t.Fatal("plan panel toggle should default off")
	}
	m.input.SetValue("/layout")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if !next.planPanelPersistent {
		t.Fatal("/layout should turn the persistent plan panel on")
	}
	if !transcriptContains(next.transcript, "Persistent plan panel on.") {
		t.Fatalf("missing on-notice, transcript: %#v", next.transcript)
	}
	// Toggle back off.
	next.input.SetValue("/layout")
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if next.planPanelPersistent {
		t.Fatal("second /layout should turn the persistent plan panel off")
	}
	if !transcriptContains(next.transcript, "Persistent plan panel off.") {
		t.Fatalf("missing off-notice, transcript: %#v", next.transcript)
	}
}

func TestPersistentPlanHeaderRendersWhenToggledOn(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.designMode = true
	m.planPanelPersistent = true
	m.pendingPlan = &schemas.DesignPlan{Epic: "add user auth", Requirements: []string{"login flow", "session token"}}
	header := m.persistentPlanHeader(80)
	if header == "" {
		t.Fatal("expected a rendered plan header")
	}
	if !strings.Contains(header, "add user auth") {
		t.Fatalf("plan header missing epic: %q", header)
	}
	if !strings.Contains(header, "login flow") || !strings.Contains(header, "session token") {
		t.Fatalf("plan header missing requirements: %q", header)
	}
}

func TestPersistentPlanHeaderInertOutsideDesignMode(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.designMode = false // not in design mode
	m.planPanelPersistent = true
	m.pendingPlan = &schemas.DesignPlan{Epic: "should not render"}
	if header := m.persistentPlanHeader(80); header != "" {
		t.Fatalf("plan header should be empty outside design mode, got %q", header)
	}
}

func TestPersistentPlanHeaderInertWithoutPlan(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.designMode = true
	m.planPanelPersistent = true
	m.pendingPlan = nil // no crystallized plan
	if header := m.persistentPlanHeader(80); header != "" {
		t.Fatalf("plan header should be empty without a pending plan, got %q", header)
	}
}

func TestPersistentPlanHeaderInertWhenToggleOff(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.designMode = true
	m.planPanelPersistent = false // toggle off
	m.pendingPlan = &schemas.DesignPlan{Epic: "should not render"}
	if header := m.persistentPlanHeader(80); header != "" {
		t.Fatalf("plan header should be empty when toggle off, got %q", header)
	}
}
func TestPersistentPlanHeaderRendersTaskGraph(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.designMode = true
	m.planPanelPersistent = true
	m.pendingPlan = &schemas.DesignPlan{
		Epic: "add diagrams",
		Tasks: []schemas.Task{
			{ID: "d1", Title: "Renderer", Intent: "build the renderer"},
			{ID: "d2", Title: "Seam", Intent: "wire the panel", DependsOn: []string{"d1"}},
		},
	}
	header := m.persistentPlanHeader(80)
	if !strings.Contains(header, "Task graph") {
		t.Fatalf("plan header missing task graph section: %q", header)
	}
	if !strings.Contains(header, "Renderer") || !strings.Contains(header, "Seam") || !strings.Contains(header, "▼") {
		t.Fatalf("plan header missing rendered task graph: %q", header)
	}
	narrow := m.persistentPlanHeader(40)
	if !strings.Contains(narrow, "- Renderer") || strings.Contains(narrow, "▼") {
		t.Fatalf("narrow plan header should use the list fallback: %q", narrow)
	}
}
