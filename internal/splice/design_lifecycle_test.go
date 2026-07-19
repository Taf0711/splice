package splice

import (
	"encoding/json"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func designEvent(seq int, typ sessions.EventType, payload any) sessions.Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return sessions.Event{
		ID:        "test",
		SessionID: "test",
		Sequence:  seq,
		Type:      typ,
		CreatedAt: "2026-07-13T00:00:00Z",
		Payload:   raw,
	}
}

func validPlan() schemas.DesignPlan {
	return schemas.DesignPlan{
		Epic:         "test epic",
		Requirements: []string{"req1"},
		InScope:      []string{"in"},
		OutOfScope:   []string{"out"},
		SystemDesign: "design",
		Source:       "conversation",
		Tasks: []schemas.Task{
			{ID: "t1", Title: "Task 1", Intent: "do thing 1"},
			{ID: "t2", Title: "Task 2", Intent: "do thing 2"},
		},
	}
}

func TestReconstructEmptyEvents(t *testing.T) {
	state, err := ReconstructDesignState(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseNone {
		t.Fatalf("phase = %q, want %q", state.Phase, schemas.DesignPhaseNone)
	}
	if state.Plan != nil {
		t.Fatalf("plan should be nil")
	}
}

func TestReconstructConversationPhase(t *testing.T) {
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseConversation {
		t.Fatalf("phase = %q, want %q", state.Phase, schemas.DesignPhaseConversation)
	}
	if state.Plan != nil {
		t.Fatalf("plan should be nil in conversation phase")
	}
}

func TestReconstructReviewPhase(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		designEvent(2, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID:   "plan-1",
			Revision: 1,
			Plan:     planJSON,
		}),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseReview {
		t.Fatalf("phase = %q, want %q", state.Phase, schemas.DesignPhaseReview)
	}
	if state.Plan == nil {
		t.Fatalf("plan should be set")
	}
	if state.Plan.Epic != "test epic" {
		t.Fatalf("epic = %q, want %q", state.Plan.Epic, "test epic")
	}
	if state.Revision.PlanID != "plan-1" || state.Revision.Revision != 1 {
		t.Fatalf("revision = %+v, want {plan-1 1}", state.Revision)
	}
	if state.Critique != nil {
		t.Fatalf("critique should be nil before critique_recorded")
	}
}

func TestReconstructCritiqueRecorded(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	critique := schemas.PlanCritique{
		OverallAssessment: "looks ok",
		Critiques: []schemas.Critique{{
			Category: "security",
			Severity: schemas.SeverityLow,
			Issue:    "minor",
		}},
	}
	critiqueJSON, _ := json.Marshal(critique)
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		designEvent(2, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID:   "plan-1",
			Revision: 1,
			Plan:     planJSON,
		}),
		designEvent(3, sessions.EventCritiqueRecorded, CritiqueRecordedPayload{
			PlanID:   "plan-1",
			Revision: 1,
			Critique: critiqueJSON,
		}),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Critique == nil {
		t.Fatalf("critique should be set")
	}
	if state.Critique.OverallAssessment != "looks ok" {
		t.Fatalf("assessment = %q, want %q", state.Critique.OverallAssessment, "looks ok")
	}
}

func TestReconstructExecutingPhase(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		designEvent(2, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID:   "plan-1",
			Revision: 1,
			Plan:     planJSON,
		}),
		designEvent(3, sessions.EventPlanApproved, PlanApprovedPayload{PlanID: "plan-1"}),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseExecuting {
		t.Fatalf("phase = %q, want %q", state.Phase, schemas.DesignPhaseExecuting)
	}
}

func TestReconstructAllTasksCompleted(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		designEvent(2, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID: "plan-1", Revision: 1, Plan: planJSON,
		}),
		designEvent(3, sessions.EventPlanApproved, PlanApprovedPayload{PlanID: "plan-1"}),
		designEvent(4, sessions.EventTaskStarted, TaskStartedPayload{TaskID: "t1", RunID: "r1"}),
		designEvent(5, sessions.EventTaskCompleted, TaskCompletedPayload{TaskID: "t1", RunID: "r1"}),
		designEvent(6, sessions.EventTaskStarted, TaskStartedPayload{TaskID: "t2", RunID: "r2"}),
		designEvent(7, sessions.EventTaskCompleted, TaskCompletedPayload{TaskID: "t2", RunID: "r2"}),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseCompleted {
		t.Fatalf("phase = %q, want %q", state.Phase, schemas.DesignPhaseCompleted)
	}
}

func TestReconstructTaskFailedStaysExecuting(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		designEvent(2, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID: "plan-1", Revision: 1, Plan: planJSON,
		}),
		designEvent(3, sessions.EventPlanApproved, PlanApprovedPayload{PlanID: "plan-1"}),
		designEvent(4, sessions.EventTaskStarted, TaskStartedPayload{TaskID: "t1", RunID: "r1"}),
		designEvent(5, sessions.EventTaskFailed, TaskFailedPayload{TaskID: "t1", RunID: "r1", Reason: "boom"}),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseExecuting {
		t.Fatalf("phase = %q, want %q (failed task does not complete plan)", state.Phase, schemas.DesignPhaseExecuting)
	}
	if outcome := state.TaskOutcomes["t1"]; outcome.Status != "failed" {
		t.Fatalf("t1 outcome = %q, want %q", outcome.Status, "failed")
	}
}

func TestReconstructSecondDesignModeResets(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		designEvent(2, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID: "plan-1", Revision: 1, Plan: planJSON,
		}),
		designEvent(3, sessions.EventDesignModeEntered, nil),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseConversation {
		t.Fatalf("phase = %q, want %q (second entry resets)", state.Phase, schemas.DesignPhaseConversation)
	}
	if state.Plan != nil {
		t.Fatalf("plan should be nil after reset")
	}
	if len(state.TaskOutcomes) != 0 {
		t.Fatalf("task outcomes should be empty after reset")
	}
}

func TestReconstructRecrystallizationClearsOldState(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	critique := schemas.PlanCritique{OverallAssessment: "v1 critique"}
	critiqueJSON, _ := json.Marshal(critique)
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		designEvent(2, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID: "plan-1", Revision: 1, Plan: planJSON,
		}),
		designEvent(3, sessions.EventCritiqueRecorded, CritiqueRecordedPayload{
			PlanID: "plan-1", Revision: 1, Critique: critiqueJSON,
		}),
		designEvent(4, sessions.EventPlanApproved, PlanApprovedPayload{PlanID: "plan-1"}),
		designEvent(5, sessions.EventTaskStarted, TaskStartedPayload{TaskID: "t1", RunID: "r1"}),
		designEvent(6, sessions.EventTaskCompleted, TaskCompletedPayload{TaskID: "t1", RunID: "r1"}),
		designEvent(7, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID: "plan-1", Revision: 2, Plan: planJSON,
		}),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseReview {
		t.Fatalf("phase = %q, want %q", state.Phase, schemas.DesignPhaseReview)
	}
	if state.Revision.Revision != 2 {
		t.Fatalf("revision = %d, want 2", state.Revision.Revision)
	}
	if state.Critique != nil {
		t.Fatalf("critique should be cleared by re-crystallization")
	}
	if len(state.TaskOutcomes) != 0 {
		t.Fatalf("task outcomes should be cleared by re-crystallization")
	}
}

func TestReconstructRewindScenario(t *testing.T) {
	// Simulate a rewind that truncated events before plan_crystallized:
	// only design_mode_entered survives.
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseConversation {
		t.Fatalf("phase = %q, want %q (rewind removed crystallization)", state.Phase, schemas.DesignPhaseConversation)
	}
	if state.Plan != nil {
		t.Fatalf("plan should be nil after rewind removed crystallization")
	}
}

func TestReconstructForkScenario(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		designEvent(2, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID: "plan-1", Revision: 1, Plan: planJSON,
		}),
	}
	// Fork copies all non-usage events; reconstruction is identical.
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != schemas.DesignPhaseReview {
		t.Fatalf("phase = %q, want %q", state.Phase, schemas.DesignPhaseReview)
	}
	if state.Plan == nil || state.Plan.Epic != "test epic" {
		t.Fatalf("plan not reconstructed correctly in fork scenario")
	}
}

func TestReconstructMalformedPlanPayload(t *testing.T) {
	events := []sessions.Event{
		designEvent(1, sessions.EventDesignModeEntered, nil),
		{
			ID:        "test",
			SessionID: "test",
			Sequence:  2,
			Type:      sessions.EventPlanCrystallized,
			CreatedAt: "2026-07-13T00:00:00Z",
			Payload:   json.RawMessage(`{"plan_id":"plan-1","revision":1,"plan":{bad json}`),
		},
	}
	_, err := ReconstructDesignState(events)
	if err == nil {
		t.Fatalf("expected error for malformed plan payload")
	}
}

func TestReconstructMissingPlanID(t *testing.T) {
	plan := validPlan()
	planJSON, _ := json.Marshal(plan)
	events := []sessions.Event{
		designEvent(1, sessions.EventPlanCrystallized, PlanCrystallizedPayload{
			PlanID:   "",
			Revision: 1,
			Plan:     planJSON,
		}),
	}
	_, err := ReconstructDesignState(events)
	if err == nil {
		t.Fatalf("expected error for missing plan_id")
	}
}

func TestReconstructMissingTaskID(t *testing.T) {
	events := []sessions.Event{
		designEvent(1, sessions.EventTaskStarted, TaskStartedPayload{
			TaskID: "",
			RunID:  "r1",
		}),
	}
	_, err := ReconstructDesignState(events)
	if err == nil {
		t.Fatalf("expected error for missing task_id")
	}
}
