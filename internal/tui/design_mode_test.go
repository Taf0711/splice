package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	spinner "charm.land/bubbles/v2/spinner"
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

func designToolCall(id, name, args string) []zeroruntime.StreamEvent {
	return []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: id, ToolName: name},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: id, ArgumentsFragment: args},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: id},
	}
}

func designTextAnswer(text string) []zeroruntime.StreamEvent {
	return []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: text},
		{Type: zeroruntime.StreamEventDone},
	}
}

func tuiDesignPlan() schemas.DesignPlan {
	return schemas.DesignPlan{
		Source:       "conversation",
		Epic:         "Build the feature",
		Requirements: []string{"must work"},
		InScope:      []string{"core"},
		OutOfScope:   []string{"enterprise"},
		SystemDesign: "use go structs",
		Tasks:        []schemas.Task{{ID: "t1", Title: "Build core", Intent: "Build the core flow"}},
	}
}

func tuiCleanCritique() schemas.PlanCritique {
	return schemas.PlanCritique{OverallAssessment: "ready", MustFixBeforeExecution: false}
}

// disableAutoTitle stops the agent-response title generation from making an
// extra provider call that would consume a staged scripted-provider turn.
func disableAutoTitle(m model) model {
	if m.activeSession.SessionID == "" {
		return m
	}
	if m.titledSessions == nil {
		m.titledSessions = map[string]bool{}
	}
	m.titledSessions[m.activeSession.SessionID] = true
	return m
}

// collectRunMsgs runs a possibly-batched command to completion and returns
// every substantive message it produced, excluding spinner ticks.
func collectRunMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	var out []tea.Msg
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, sub := range msg {
			out = append(out, collectRunMsgs(sub)...)
		}
	case spinner.TickMsg:
	default:
		if msg != nil {
			out = append(out, msg)
		}
	}
	return out
}

func TestDesignTransitionToolExposure(t *testing.T) {
	if _, ok := designConversationRegistry(nil).Get(string(splicerun.DesignTransitionApprove)); ok {
		t.Fatal("base registry unexpectedly exposes approve_design")
	}
	reg := tools.NewRegistry()
	designTransitionRegistry(reg, false, splicerun.NewDesignTransitionRecorder())
	if _, ok := reg.Get(string(splicerun.DesignTransitionCrystallize)); !ok {
		t.Fatal("crystallize_design not exposed")
	}
	if _, ok := reg.Get(string(splicerun.DesignTransitionApprove)); ok {
		t.Fatal("approve_design exposed without a plan or with a must-fix critique")
	}
	regOK := tools.NewRegistry()
	designTransitionRegistry(regOK, true, splicerun.NewDesignTransitionRecorder())
	if _, ok := regOK.Get(string(splicerun.DesignTransitionApprove)); !ok {
		t.Fatal("approve_design not exposed with a ready plan")
	}
}

func TestDesignAgentCrystallizeSchedulesAndPersistsSourceAgent(t *testing.T) {
	store := testSessionStore(t)
	planArgs, _ := json.Marshal(tuiDesignPlan())
	critiqueArgs, _ := json.Marshal(tuiCleanCritique())
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		designToolCall("call-cry", "crystallize_design", `{}`),
		designTextAnswer("crystallizing now"),
		designToolCall("call-plan", "submit_design_plan", string(planArgs)),
		designToolCall("call-crit", "submit_critique", string(critiqueArgs)),
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	var err error
	m, err = m.ensureActiveSession("design")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m.designNoticeShown = true
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{"role": "user", "content": "Design the feature"})
	m = disableAutoTitle(m)

	m.input.SetValue("crystallize the design, please")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected design turn")
	}
	respMsg := execCmd(cmd)
	updated, transitionCmd := next.Update(respMsg)
	next = updated.(model)
	if transitionCmd == nil {
		t.Fatal("expected the design turn to schedule the crystallization transition")
	}
	var crystallize *crystallizeResultMsg
	for _, msg := range collectRunMsgs(transitionCmd) {
		if c, ok := msg.(crystallizeResultMsg); ok {
			crystallize = &c
			break
		}
	}
	if crystallize == nil {
		t.Fatal("no crystallizeResultMsg in transition")
	}
	if crystallize.err != nil {
		t.Fatalf("crystallize: %v", crystallize.err)
	}
	if crystallize.source != splicerun.DesignTransitionSourceAgent {
		t.Fatalf("crystallize source = %q, want agent", crystallize.source)
	}
	if !transcriptContains(next.transcript, "The design agent requested crystallization.") {
		t.Fatalf("expected agent request label, got %#v", next.transcript)
	}
	events, err := store.ReadEvents(m.activeSession.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var planSource, critSource splicerun.DesignTransitionSource
	for _, ev := range events {
		switch ev.Type {
		case sessions.EventPlanCrystallized:
			var p splicerun.PlanCrystallizedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("unmarshal plan_crystallized: %v", err)
			}
			planSource = p.Source
		case sessions.EventCritiqueRecorded:
			var c splicerun.CritiqueRecordedPayload
			if err := json.Unmarshal(ev.Payload, &c); err != nil {
				t.Fatalf("unmarshal critique_recorded: %v", err)
			}
			critSource = c.Source
		}
	}
	if planSource != splicerun.DesignTransitionSourceAgent || critSource != splicerun.DesignTransitionSourceAgent {
		t.Fatalf("persisted source = plan:%q critique:%q, want agent/agent", planSource, critSource)
	}
}

func TestDesignAgentApproveRequestsUserConfirmation(t *testing.T) {
	store := testSessionStore(t)
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		designToolCall("call-ap", "approve_design", `{}`),
		designTextAnswer("approving the plan"),
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	var err error
	m, err = m.ensureActiveSession("design")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m.designNoticeShown = true
	m.width = 100
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{"role": "user", "content": "Design the feature"})
	plan := tuiDesignPlan()
	planJSON, _ := json.Marshal(plan)
	critiqueJSON, _ := json.Marshal(tuiCleanCritique())
	m, _ = m.appendSessionEvent(sessions.EventPlanCrystallized, splicerun.PlanCrystallizedPayload{PlanID: "plan-1", Revision: 1, Plan: planJSON})
	m, _ = m.appendSessionEvent(sessions.EventCritiqueRecorded, splicerun.CritiqueRecordedPayload{PlanID: "plan-1", Revision: 1, Critique: critiqueJSON})
	m = m.reconstructDesignState()
	m.pendingPlan = &plan
	m = disableAutoTitle(m)

	m.input.SetValue("approve the plan")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected design turn")
	}
	respMsg := execCmd(cmd)
	updated, transitionCmd := next.Update(respMsg)
	next = updated.(model)
	if transitionCmd != nil || next.pending {
		t.Fatal("agent-requested approval started execution before user confirmation")
	}
	if next.pendingPermission == nil || next.pendingPermission.request.ToolName != "approve_design" {
		t.Fatal("agent-requested approval did not show the confirmation prompt")
	}
	if !next.sidebarActive() {
		t.Fatal("approval confirmation made the design-plan sidebar disappear")
	}
	if plain := stripSidebar(next.sidebarPlanLines(sidebarWidth(next.width))); !strings.Contains(plain, "Build core") {
		t.Fatalf("design task missing from sidebar during confirmation:\n%s", plain)
	}

	deniedModel, deniedCmd := next.resolvePermission(permissionDecisionDeny)
	denied := deniedModel.(model)
	if deniedCmd != nil || denied.pending {
		t.Fatal("denying agent-requested approval started plan execution")
	}
	if eventTypesContain(denied.sessionEvents, sessions.EventPlanApproved) {
		t.Fatal("denied agent-requested approval persisted plan_approved")
	}
}

func TestDesignAgentApproveIfReadyRequestsUserConfirmation(t *testing.T) {
	store := testSessionStore(t)
	planArgs, _ := json.Marshal(tuiDesignPlan())
	critiqueArgs, _ := json.Marshal(tuiCleanCritique())
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		designToolCall("call-cry", "crystallize_design", `{"approve_if_ready":true}`),
		designTextAnswer("crystallizing now"),
		designToolCall("call-plan", "submit_design_plan", string(planArgs)),
		designToolCall("call-crit", "submit_critique", string(critiqueArgs)),
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	var err error
	m, err = m.ensureActiveSession("design")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m.designNoticeShown = true
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{"role": "user", "content": "Design the feature"})
	m = disableAutoTitle(m)

	m.input.SetValue("crystallize and approve when ready")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	respMsg := execCmd(cmd)
	updated, transitionCmd := next.Update(respMsg)
	next = updated.(model)
	var crystallize *crystallizeResultMsg
	for _, msg := range collectRunMsgs(transitionCmd) {
		if c, ok := msg.(crystallizeResultMsg); ok {
			crystallize = &c
			break
		}
	}
	if crystallize == nil {
		t.Fatal("no crystallizeResultMsg in transition")
	}
	if crystallize.err != nil {
		t.Fatalf("crystallize: %v", crystallize.err)
	}
	if !crystallize.approveIfReady {
		t.Fatal("approve_if_ready not carried on the crystallize result")
	}
	updated, approveCmd := next.Update(*crystallize)
	next = updated.(model)
	if approveCmd != nil || next.pending {
		t.Fatal("approve_if_ready started execution before user confirmation")
	}
	if next.pendingPermission == nil {
		t.Fatal("approve_if_ready did not show the confirmation prompt")
	}

	approvedModel, runCmd := next.resolvePermission(permissionDecisionAllow)
	approved := approvedModel.(model)
	if runCmd == nil || !approved.pending {
		t.Fatal("explicit user confirmation did not start plan execution")
	}
	var payload splicerun.PlanApprovedPayload
	for _, event := range approved.sessionEvents {
		if event.Type == sessions.EventPlanApproved {
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal plan_approved: %v", err)
			}
		}
	}
	if payload.PlanID == "" || payload.Source != splicerun.DesignTransitionSourceAgent {
		t.Fatalf("plan_approved = %#v, want agent source", payload)
	}
}

func TestDesignAgentApproveIfReadyStaysIdleOnMustFix(t *testing.T) {
	store := testSessionStore(t)
	planArgs, _ := json.Marshal(tuiDesignPlan())
	blocking := schemas.PlanCritique{
		OverallAssessment: "needs work",
		Critiques: []schemas.Critique{{
			Category: "correctness", Severity: schemas.SeverityHigh, Issue: "unsafe",
		}},
		MustFixBeforeExecution: true,
	}
	critiqueArgs, _ := json.Marshal(blocking)
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		designToolCall("call-cry", "crystallize_design", `{"approve_if_ready":true}`),
		designTextAnswer("crystallizing now"),
		designToolCall("call-plan", "submit_design_plan", string(planArgs)),
		designToolCall("call-crit", "submit_critique", string(critiqueArgs)),
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	var err error
	m, err = m.ensureActiveSession("design")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m.designNoticeShown = true
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{"role": "user", "content": "Design the feature"})
	m = disableAutoTitle(m)

	m.input.SetValue("crystallize and approve when ready")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	respMsg := execCmd(cmd)
	updated, transitionCmd := next.Update(respMsg)
	next = updated.(model)
	var crystallize *crystallizeResultMsg
	for _, msg := range collectRunMsgs(transitionCmd) {
		if c, ok := msg.(crystallizeResultMsg); ok {
			crystallize = &c
			break
		}
	}
	if crystallize == nil || crystallize.err != nil {
		t.Fatalf("crystallize failed: %#v", crystallize)
	}
	updated, approveCmd := next.Update(*crystallize)
	next = updated.(model)
	if approveCmd != nil {
		t.Fatal("must-fix critique must not auto-approve")
	}
	if next.pending {
		t.Fatal("must-fix critique must not start an approval run")
	}
}

func TestDesignConversationContextIncludesExecutionState(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	var err error
	m, err = m.ensureActiveSession("design")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	plan := tuiDesignPlan()
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	for _, event := range []struct {
		typ     sessions.EventType
		payload any
	}{
		{sessions.EventDesignModeEntered, nil},
		{sessions.EventPlanCrystallized, splicerun.PlanCrystallizedPayload{PlanID: "plan-1", Revision: 1, Plan: planJSON}},
		{sessions.EventPlanApproved, splicerun.PlanApprovedPayload{PlanID: "plan-1", Source: splicerun.DesignTransitionSourceAgent}},
		{sessions.EventTaskStarted, splicerun.TaskStartedPayload{TaskID: "t1", RunID: "run-1"}},
		{sessions.EventTaskCompleted, splicerun.TaskCompletedPayload{TaskID: "t1", RunID: "run-1"}},
	} {
		m, err = m.appendSessionEvent(event.typ, event.payload)
		if err != nil {
			t.Fatalf("append %s: %v", event.typ, err)
		}
	}

	_, state, err := designConversationContext(m.sessionEvents, nil, nil)
	if err != nil {
		t.Fatalf("designConversationContext: %v", err)
	}
	for _, want := range []string{`"phase": "completed"`, `"task_outcomes"`, `"t1"`, `"status": "completed"`} {
		if !strings.Contains(state, want) {
			t.Fatalf("design execution state missing %q:\n%s", want, state)
		}
	}
}

func TestManualCrystallizeReusesPlanIDIncrementsRevisionSourceManual(t *testing.T) {
	store := testSessionStore(t)
	planArgs, _ := json.Marshal(tuiDesignPlan())
	critiqueArgs, _ := json.Marshal(tuiCleanCritique())
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		designToolCall("call-plan", "submit_design_plan", string(planArgs)),
		designToolCall("call-crit", "submit_critique", string(critiqueArgs)),
	}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	var err error
	m, err = m.ensureActiveSession("design")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m.designNoticeShown = true
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{"role": "user", "content": "Design the feature"})
	priorPlanJSON, _ := json.Marshal(tuiDesignPlan())
	m, _ = m.appendSessionEvent(sessions.EventPlanCrystallized, splicerun.PlanCrystallizedPayload{PlanID: "plan-1", Revision: 1, Plan: priorPlanJSON})
	if got, err := m.currentPlanID(); err != nil || got != "plan-1" {
		t.Fatalf("currentPlanID = %q, %v, want plan-1", got, err)
	}
	_, cmd := m.handleCrystallizeCommand()
	msg := execCmd(cmd)
	crystallize, ok := msg.(crystallizeResultMsg)
	if !ok {
		t.Fatalf("expected crystallizeResultMsg, got %T", msg)
	}
	if crystallize.err != nil {
		t.Fatalf("crystallize: %v", crystallize.err)
	}
	if crystallize.source != splicerun.DesignTransitionSourceManual {
		t.Fatalf("source = %q, want manual", crystallize.source)
	}
	events, err := store.ReadEvents(m.activeSession.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var lastPlanID string
	var lastRevision int
	var lastSource splicerun.DesignTransitionSource
	for _, ev := range events {
		if ev.Type == sessions.EventPlanCrystallized {
			var p splicerun.PlanCrystallizedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("unmarshal plan_crystallized: %v", err)
			}
			lastPlanID = p.PlanID
			lastRevision = p.Revision
			lastSource = p.Source
		}
	}
	if lastPlanID != "plan-1" {
		t.Fatalf("PlanID = %q, want plan-1 reused across crystallizations", lastPlanID)
	}
	if lastRevision != 2 {
		t.Fatalf("Revision = %d, want 2 (incremented)", lastRevision)
	}
	if lastSource != splicerun.DesignTransitionSourceManual {
		t.Fatalf("source = %q, want manual", lastSource)
	}
}

func TestDesignConversationSeesMustFixCritique(t *testing.T) {
	for _, tt := range []struct {
		name              string
		persistedCritique bool
	}{
		{name: "persisted lifecycle state", persistedCritique: true},
		{name: "live overlay after persistence failure"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := testSessionStore(t)
			provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{{
				{Type: zeroruntime.StreamEventText, Content: "revising"},
				{Type: zeroruntime.StreamEventDone},
			}}}
			m := newDesignModeTestModel(t.TempDir(), provider, store)
			var err error
			m, err = m.ensureActiveSession("design")
			if err != nil {
				t.Fatal(err)
			}
			m.designNoticeShown = true
			m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
			m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{"role": "user", "content": "Design the deployment"})
			m, _ = m.appendSessionEvent(sessions.EventMessage, map[string]any{"role": "assistant", "content": "Here is the design."})

			plan := schemas.DesignPlan{
				Source: "conversation", Epic: "Deploy safely", Requirements: []string{"safe rollback"},
				InScope: []string{"deployment"}, Tasks: []schemas.Task{{
					ID: "t1", Title: "Deploy", Intent: "deploy safely",
					AcceptanceFacts: []schemas.AcceptanceFact{
						{Statement: "first fact"}, {Statement: "second fact"},
						{Statement: "third fact"}, {Statement: "FOURTH ACCEPTANCE FACT"},
					},
				}},
			}
			critique := schemas.PlanCritique{
				OverallAssessment: "Needs revision",
				Critiques: []schemas.Critique{{
					Category: "correctness", Severity: schemas.SeverityHigh,
					Issue: "MISSING ROLLBACK STRATEGY", SuggestedMitigation: "ADD CANARY ROLLBACK",
				}},
				MustFixBeforeExecution: true,
			}
			planJSON, _ := json.Marshal(plan)
			m, _ = m.appendSessionEvent(sessions.EventPlanCrystallized, splicerun.PlanCrystallizedPayload{PlanID: "plan-1", Revision: 1, Plan: planJSON})
			if tt.persistedCritique {
				critiqueJSON, _ := json.Marshal(critique)
				m, _ = m.appendSessionEvent(sessions.EventCritiqueRecorded, splicerun.CritiqueRecordedPayload{PlanID: "plan-1", Revision: 1, Critique: critiqueJSON})
			}
			m = m.reconstructDesignState()
			if !tt.persistedCritique {
				m.pendingCritique = &critique
			}

			m.input.SetValue("Revise the plan using the critique")
			updated, cmd := m.Update(testKey(tea.KeyEnter))
			next := updated.(model)
			if cmd == nil {
				t.Fatal("expected design run after prompt")
			}
			_, _ = next.Update(execCmd(cmd))
			if len(provider.requests) != 1 {
				t.Fatalf("requests = %d, want 1", len(provider.requests))
			}
			messages := provider.requests[0].Messages
			if len(messages) == 0 || messages[0].Role != zeroruntime.MessageRoleSystem {
				t.Fatalf("first message = %#v, want system context", messages)
			}
			system := messages[0].Content
			for _, want := range []string{"MISSING ROLLBACK STRATEGY", "ADD CANARY ROLLBACK", "FOURTH ACCEPTANCE FACT"} {
				if !strings.Contains(system, want) {
					t.Fatalf("design system context lost %q:\n%s", want, system)
				}
			}
			for _, message := range messages[1:] {
				if strings.Contains(message.Content, "MISSING ROLLBACK STRATEGY") {
					t.Fatalf("workflow state leaked into %s message", message.Role)
				}
			}
		})
	}
}

func TestResumedDesignConversationKeepsCritiqueContext(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "resumed design", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	plan := schemas.DesignPlan{
		Source: "conversation", Epic: "Resume safely", Requirements: []string{"keep context"},
		InScope: []string{"resume"}, Tasks: []schemas.Task{{ID: "t1", Title: "Resume", Intent: "keep the critique"}},
	}
	critique := schemas.PlanCritique{
		OverallAssessment: "Needs revision",
		Critiques: []schemas.Critique{{
			Category: "correctness", Severity: schemas.SeverityHigh,
			Issue: "RESUMED CRITIQUE ISSUE", SuggestedMitigation: "RESUMED CRITIQUE MITIGATION",
		}},
		MustFixBeforeExecution: true,
	}
	planJSON, _ := json.Marshal(plan)
	critiqueJSON, _ := json.Marshal(critique)
	for _, event := range []sessions.AppendEventInput{
		{Type: sessions.EventDesignModeEntered},
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "Design resume behavior"}},
		{Type: sessions.EventPlanCrystallized, Payload: splicerun.PlanCrystallizedPayload{PlanID: "plan-1", Revision: 1, Plan: planJSON}},
		{Type: sessions.EventCritiqueRecorded, Payload: splicerun.CritiqueRecordedPayload{PlanID: "plan-1", Revision: 1, Critique: critiqueJSON}},
	} {
		if _, err := store.AppendEvent(session.SessionID, event); err != nil {
			t.Fatal(err)
		}
	}
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "revising"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	m.input.SetValue("/resume " + session.SessionID)
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.pendingPlan == nil || m.pendingCritique == nil {
		t.Fatalf("resume lost design state: plan=%#v critique=%#v", m.pendingPlan, m.pendingCritique)
	}

	m.input.SetValue("Revise the resumed plan")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected resumed design run")
	}
	_, _ = next.Update(execCmd(cmd))
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(provider.requests))
	}
	system := provider.requests[0].Messages[0].Content
	for _, want := range []string{"RESUMED CRITIQUE ISSUE", "RESUMED CRITIQUE MITIGATION"} {
		if !strings.Contains(system, want) {
			t.Fatalf("resumed design context lost %q:\n%s", want, system)
		}
	}
	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := countSessionEvents(events, sessions.EventDesignModeEntered); got != 1 {
		t.Fatalf("first resumed prompt created a new design epoch: count=%d", got)
	}
}

func TestResumedDesignSessionExposesApproveDesignFromReconstructedState(t *testing.T) {
	// A resumed session with a persisted plan and a clean critique must expose
	// approve_design to the design agent on the first prompt after resume, and a
	// resumed must-fix critique must keep it hidden. Availability is derived from
	// the state reconstructed from lifecycle events, not an in-memory value that
	// /resume reset.
	cases := []struct {
		name     string
		critique schemas.PlanCritique
		exposed  bool
	}{
		{name: "clean critique exposes approve", critique: schemas.PlanCritique{OverallAssessment: "ready"}, exposed: true},
		{name: "must-fix critique hides approve", critique: schemas.PlanCritique{OverallAssessment: "needs fixes", MustFixBeforeExecution: true}, exposed: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := testSessionStore(t)
			session, err := store.Create(sessions.CreateInput{Title: "resumed approve", Cwd: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			planJSON, _ := json.Marshal(tuiDesignPlan())
			critiqueJSON, _ := json.Marshal(tc.critique)
			for _, event := range []sessions.AppendEventInput{
				{Type: sessions.EventDesignModeEntered},
				{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "Design the feature"}},
				{Type: sessions.EventPlanCrystallized, Payload: splicerun.PlanCrystallizedPayload{PlanID: "plan-1", Revision: 1, Plan: planJSON}},
				{Type: sessions.EventCritiqueRecorded, Payload: splicerun.CritiqueRecordedPayload{PlanID: "plan-1", Revision: 1, Critique: critiqueJSON}},
			} {
				if _, err := store.AppendEvent(session.SessionID, event); err != nil {
					t.Fatal(err)
				}
			}
			provider := &fakeProvider{events: []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventDone}}}
			m := newDesignModeTestModel(t.TempDir(), provider, store)
			m.input.SetValue("/resume " + session.SessionID)
			updated, _ := m.Update(testKey(tea.KeyEnter))
			m = updated.(model)

			var captured agent.Options
			m.captureRunOptions = func(options agent.Options) { captured = options }
			m.input.SetValue("continue the design")
			updated, cmd := m.Update(testKey(tea.KeyEnter))
			next := updated.(model)
			if cmd == nil {
				t.Fatal("expected resumed design run")
			}
			_, _ = next.Update(execCmd(cmd))
			if _, ok := captured.Registry.Get(string(splicerun.DesignTransitionApprove)); ok != tc.exposed {
				t.Fatalf("approve_design exposed = %v, want %v", ok, tc.exposed)
			}
		})
	}
}

func TestDesignConversationRejectsMalformedLifecycleState(t *testing.T) {
	store := testSessionStore(t)
	provider := &scriptedProvider{}
	m := newDesignModeTestModel(t.TempDir(), provider, store)
	var err error
	m, err = m.ensureActiveSession("design")
	if err != nil {
		t.Fatal(err)
	}
	m.designMode = true
	m.designNoticeShown = true
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	m, _ = m.appendSessionEvent(sessions.EventPlanCrystallized, map[string]any{"plan_id": "plan-1"})
	m.input.SetValue("continue")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("malformed design state started an agent run")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider requests = %d, want 0", len(provider.requests))
	}
	if !transcriptContains(next.transcript, "Design context error: assemble design context:") {
		t.Fatalf("missing named design context error: %#v", next.transcript)
	}
}

func TestDesignCommandEntersDesignMode(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.pendingPlan = &schemas.DesignPlan{Epic: "stale plan"}
	m.pendingCritique = &schemas.PlanCritique{OverallAssessment: "stale critique"}
	m.input.SetValue("/design")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatalf("expected /design to return immediately, got cmd %v", cmd)
	}
	if !next.designMode {
		t.Fatalf("expected designMode to be true, got false")
	}
	if next.pendingPlan != nil || next.pendingCritique != nil {
		t.Fatalf("new design epoch kept stale state: plan=%#v critique=%#v", next.pendingPlan, next.pendingCritique)
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
		"glob", "lsp_navigate", "request_permissions", "skill", "web_fetch", tools.ToolSearchToolName,
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

func TestDesignConversationRegistryIncludesEveryCoreReadOnlyTool(t *testing.T) {
	// Regression: this list was hand-maintained, so a new read-only tool silently
	// missed design mode until somebody edited design_mode.go.
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(t.TempDir()) {
		registry.Register(tool)
	}
	registry.Register(tools.NewToolSearchTool(registry))
	filtered := designConversationRegistry(registry)

	for _, tool := range tools.CoreReadOnlyToolsScoped(t.TempDir(), nil) {
		if _, ok := filtered.Get(tool.Name()); !ok {
			t.Fatalf("core read-only tool %q missing from design conversation registry", tool.Name())
		}
	}
}

func TestDesignConversationRegistryIncludesNewReadOnlyToolAutomatically(t *testing.T) {
	registry := tools.NewRegistry()
	fake := designRegistryTestTool{name: "future_read_only", safety: tools.Safety{
		SideEffect: tools.SideEffectRead,
		Permission: tools.PermissionAllow,
	}}
	registry.Register(fake)

	filtered := designConversationRegistry(registry)
	if _, ok := filtered.Get(fake.Name()); !ok {
		t.Fatal("new read-only tool was not included in design conversation registry")
	}
}

type designRegistryTestTool struct {
	name   string
	safety tools.Safety
}

func (tool designRegistryTestTool) Name() string             { return tool.name }
func (tool designRegistryTestTool) Description() string      { return "test tool" }
func (tool designRegistryTestTool) Parameters() tools.Schema { return tools.Schema{} }
func (tool designRegistryTestTool) Safety() tools.Safety     { return tool.safety }
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
	m.width = 100
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true

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
	if !next.sidebarActive() {
		t.Fatal("crystallized plan did not activate the context sidebar")
	}
	if plain := stripSidebar(next.sidebarPlanLines(sidebarWidth(next.width))); !strings.Contains(plain, "Implement it") {
		t.Fatalf("crystallized task missing from sidebar:\n%s", plain)
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
			name: "critique persistence failure keeps must-fix critique",
			plan: validPlan,
			critique: schemas.PlanCritique{
				Critiques:              []schemas.Critique{{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "unsafe"}},
				OverallAssessment:      "Needs work",
				MustFixBeforeExecution: true,
			},
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
			Critiques:              []schemas.Critique{{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "unsafe"}},
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
		Critiques:              []schemas.Critique{{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "unsafe"}},
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
		Critiques:              []schemas.Critique{{Category: "correctness", Severity: schemas.SeverityHigh, Issue: "unsafe"}},
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
	m = persistPlanForApproval(t, m)

	_, cmd := m.handleApproveCommand()
	if cmd == nil {
		t.Fatal("expected /approve to return a cmd")
	}
	msg := execCmd(cmd)
	if _, ok := msg.(planExecutionResultMsg); !ok {
		t.Fatalf("expected planExecutionResultMsg, got %T", msg)
	}
	events, err := store.ReadEvents(sess.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var approved splicerun.PlanApprovedPayload
	for _, ev := range events {
		if ev.Type == sessions.EventPlanApproved {
			if err := json.Unmarshal(ev.Payload, &approved); err != nil {
				t.Fatalf("unmarshal plan_approved: %v", err)
			}
		}
	}
	if approved.PlanID != "plan-1" {
		t.Fatalf("plan_approved PlanID = %q, want plan-1 (persisted revision reused)", approved.PlanID)
	}
	if approved.Source != splicerun.DesignTransitionSourceManual {
		t.Fatalf("plan_approved source = %q, want manual", approved.Source)
	}
}

func TestApproveCommandStopsWhenAuditPersistenceFails(t *testing.T) {
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	m.activeSession = sessions.Metadata{SessionID: "missing-session"}
	m.pendingPlan = &schemas.DesignPlan{Epic: "Build it", Source: "authored"}
	planJSON, _ := json.Marshal(*m.pendingPlan)
	payload, _ := json.Marshal(splicerun.PlanCrystallizedPayload{PlanID: "plan-1", Revision: 1, Plan: planJSON})
	m.sessionEvents = []sessions.Event{{Type: sessions.EventPlanCrystallized, Payload: payload}}

	updated, cmd := m.handleApproveCommand()
	if cmd != nil || updated.pending {
		t.Fatal("approval execution started after plan_approved persistence failed")
	}
	if !transcriptContains(updated.transcript, "persist plan_approved") {
		t.Fatalf("missing persistence error: %#v", updated.transcript)
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

// persistPlanForApproval appends a plan_crystallized event to the model's
// session events so approval has a persisted current revision to record on the
// plan_approved event.
func persistPlanForApproval(t *testing.T, m model) model {
	t.Helper()
	planJSON, err := json.Marshal(*m.pendingPlan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	m, err = m.appendSessionEvent(sessions.EventPlanCrystallized, splicerun.PlanCrystallizedPayload{PlanID: "plan-1", Revision: 1, Plan: planJSON})
	if err != nil {
		t.Fatalf("append plan_crystallized: %v", err)
	}
	return m
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
	m = persistPlanForApproval(t, m)

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
	m = persistPlanForApproval(t, m)
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
	m = persistPlanForApproval(t, m)
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
	m = persistPlanForApproval(t, m)
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
