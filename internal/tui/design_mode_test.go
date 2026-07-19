package tui

import (
	"context"
	"encoding/json"
	"fmt"
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
	filtered := designConversationRegistry(registry)

	for _, name := range []string{"read_file", "list_directory", "grep", "ask_user"} {
		if _, ok := filtered.Get(name); !ok {
			t.Fatalf("expected %s to be in design conversation registry", name)
		}
	}
	for _, name := range []string{"write_file", "edit_file", "bash"} {
		if _, ok := filtered.Get(name); ok {
			t.Fatalf("expected %s to be excluded from design conversation registry", name)
		}
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
