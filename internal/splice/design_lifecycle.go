package splice

import (
	"encoding/json"
	"fmt"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Design lifecycle event payloads. These are the JSON shapes stored in
// sessions.Event.Payload for the design lifecycle event types. They live in
// the splice package so the sessions package never imports splice schemas;
// the plan and critique are stored as json.RawMessage and decoded here.
//
// See plans/design-phase-tui-wiring-2026-07-13.md checkpoint D0 for the
// full contract.

// PlanCrystallizedPayload records one crystallized plan revision.
type PlanCrystallizedPayload struct {
	PlanID   string          `json:"plan_id"`
	Revision int             `json:"revision"`
	Plan     json.RawMessage `json:"plan"` // encoded schemas.DesignPlan
}

// CritiqueRecordedPayload records a critique against a plan revision.
type CritiqueRecordedPayload struct {
	PlanID   string          `json:"plan_id"`
	Revision int             `json:"revision"`
	Critique json.RawMessage `json:"critique"` // encoded schemas.PlanCritique
}

// PlanApprovedPayload marks the transition from review to execution.
type PlanApprovedPayload struct {
	PlanID string `json:"plan_id"`
}

// TaskStartedPayload records that a task has begun executing.
type TaskStartedPayload struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
}

// TaskCompletedPayload records that a task succeeded.
type TaskCompletedPayload struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
}

// TaskFailedPayload records that a task failed.
type TaskFailedPayload struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
	Reason string `json:"reason,omitempty"`
}

// DesignState is the reconstructed design workflow state derived from session
// lifecycle events. It is the authoritative view; no global file backs it.
// Transient busy states (crystallizing, critic running) are not represented
// here because they are in-memory only.
type DesignState struct {
	Phase        schemas.DesignPhase
	Revision     schemas.PlanRevision
	Plan         *schemas.DesignPlan
	Critique     *schemas.PlanCritique
	TaskOutcomes map[string]schemas.TaskRunOutcome // task_id -> outcome
}

// ReconstructDesignState replays session lifecycle events in sequence order
// to derive the current design workflow state. It reads raw events (via
// store.ReadEvents, not the compaction-rehydrated replay stream) so design
// events survive compaction in the raw log.
//
// Fork inherits design state because Store.Fork copies all non-usage events.
// Rewind clears design state because ApplyRewind truncates the event log;
// reconstruction from the truncated log naturally reflects the pre-rewind
// point. Neither needs special handling here.
//
// Malformed payloads return a named error and do not silently default (G2).
func ReconstructDesignState(events []sessions.Event) (DesignState, error) {
	state := DesignState{TaskOutcomes: map[string]schemas.TaskRunOutcome{}}
	for _, event := range events {
		switch event.Type {
		case sessions.EventDesignModeEntered:
			state = DesignState{
				Phase:        schemas.DesignPhaseConversation,
				TaskOutcomes: map[string]schemas.TaskRunOutcome{},
			}
		case sessions.EventPlanCrystallized:
			var p PlanCrystallizedPayload
			if err := json.Unmarshal(event.Payload, &p); err != nil {
				return DesignState{}, fmt.Errorf("design_mode plan_crystallized seq %d: %w", event.Sequence, err)
			}
			if p.PlanID == "" {
				return DesignState{}, fmt.Errorf("design_mode plan_crystallized seq %d: plan_id is required", event.Sequence)
			}
			if len(p.Plan) == 0 {
				return DesignState{}, fmt.Errorf("design_mode plan_crystallized seq %d: plan is required", event.Sequence)
			}
			var plan schemas.DesignPlan
			if err := json.Unmarshal(p.Plan, &plan); err != nil {
				return DesignState{}, fmt.Errorf("design_mode plan_crystallized seq %d: decode plan: %w", event.Sequence, err)
			}
			state.Phase = schemas.DesignPhaseReview
			state.Revision = schemas.PlanRevision{PlanID: p.PlanID, Revision: p.Revision}
			state.Plan = &plan
			state.Critique = nil
			state.TaskOutcomes = map[string]schemas.TaskRunOutcome{}
		case sessions.EventCritiqueRecorded:
			var c CritiqueRecordedPayload
			if err := json.Unmarshal(event.Payload, &c); err != nil {
				return DesignState{}, fmt.Errorf("design_mode critique_recorded seq %d: %w", event.Sequence, err)
			}
			if c.PlanID == "" {
				return DesignState{}, fmt.Errorf("design_mode critique_recorded seq %d: plan_id is required", event.Sequence)
			}
			if len(c.Critique) == 0 {
				return DesignState{}, fmt.Errorf("design_mode critique_recorded seq %d: critique is required", event.Sequence)
			}
			var critique schemas.PlanCritique
			if err := json.Unmarshal(c.Critique, &critique); err != nil {
				return DesignState{}, fmt.Errorf("design_mode critique_recorded seq %d: decode critique: %w", event.Sequence, err)
			}
			state.Critique = &critique
		case sessions.EventPlanApproved:
			var a PlanApprovedPayload
			if err := json.Unmarshal(event.Payload, &a); err != nil {
				return DesignState{}, fmt.Errorf("design_mode plan_approved seq %d: %w", event.Sequence, err)
			}
			if a.PlanID == "" {
				return DesignState{}, fmt.Errorf("design_mode plan_approved seq %d: plan_id is required", event.Sequence)
			}
			state.Phase = schemas.DesignPhaseExecuting
			state.TaskOutcomes = map[string]schemas.TaskRunOutcome{}
		case sessions.EventTaskStarted:
			var t TaskStartedPayload
			if err := json.Unmarshal(event.Payload, &t); err != nil {
				return DesignState{}, fmt.Errorf("design_mode task_started seq %d: %w", event.Sequence, err)
			}
			if t.TaskID == "" || t.RunID == "" {
				return DesignState{}, fmt.Errorf("design_mode task_started seq %d: task_id and run_id are required", event.Sequence)
			}
			state.TaskOutcomes[t.TaskID] = schemas.TaskRunOutcome{
				TaskID: t.TaskID,
				RunID:  t.RunID,
				Status: "running",
			}
		case sessions.EventTaskCompleted:
			var t TaskCompletedPayload
			if err := json.Unmarshal(event.Payload, &t); err != nil {
				return DesignState{}, fmt.Errorf("design_mode task_completed seq %d: %w", event.Sequence, err)
			}
			if t.TaskID == "" || t.RunID == "" {
				return DesignState{}, fmt.Errorf("design_mode task_completed seq %d: task_id and run_id are required", event.Sequence)
			}
			state.TaskOutcomes[t.TaskID] = schemas.TaskRunOutcome{
				TaskID: t.TaskID,
				RunID:  t.RunID,
				Status: "completed",
			}
		case sessions.EventTaskFailed:
			var t TaskFailedPayload
			if err := json.Unmarshal(event.Payload, &t); err != nil {
				return DesignState{}, fmt.Errorf("design_mode task_failed seq %d: %w", event.Sequence, err)
			}
			if t.TaskID == "" || t.RunID == "" {
				return DesignState{}, fmt.Errorf("design_mode task_failed seq %d: task_id and run_id are required", event.Sequence)
			}
			state.TaskOutcomes[t.TaskID] = schemas.TaskRunOutcome{
				TaskID: t.TaskID,
				RunID:  t.RunID,
				Status: "failed",
			}
		}
	}

	// Derive the completed phase: if executing and every task in the plan has
	// a terminal outcome, the plan is complete. A failed task leaves the plan
	// in executing with a failed outcome (fail-fast is a runner concern).
	if state.Phase == schemas.DesignPhaseExecuting && state.Plan != nil && len(state.Plan.Tasks) > 0 {
		allTerminal := true
		for _, task := range state.Plan.Tasks {
			outcome, ok := state.TaskOutcomes[task.ID]
			if !ok || (outcome.Status != "completed" && outcome.Status != "failed") {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			state.Phase = schemas.DesignPhaseCompleted
		}
	}

	return state, nil
}
