package splice

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Design lifecycle event payloads. These are the JSON shapes stored in
// sessions.Event.Payload for the design lifecycle event types. They live in
// the splice package so the sessions package never imports splice schemas;
// the plan and critique are stored as json.RawMessage and decoded here.
//
// The full lifecycle contract is documented with ReconstructDesignState.

// PlanCrystallizedPayload records one crystallized plan revision. Source
// records whether the user or the design agent requested it.
type PlanCrystallizedPayload struct {
	PlanID   string                 `json:"plan_id"`
	Revision int                    `json:"revision"`
	Plan     json.RawMessage        `json:"plan"` // encoded schemas.DesignPlan
	Source   DesignTransitionSource `json:"source,omitempty"`
}

// CritiqueRecordedPayload records a critique against a plan revision. Source
// records whether the user or the design agent requested the crystallization.
type CritiqueRecordedPayload struct {
	PlanID   string                 `json:"plan_id"`
	Revision int                    `json:"revision"`
	Critique json.RawMessage        `json:"critique"` // encoded schemas.PlanCritique
	Source   DesignTransitionSource `json:"source,omitempty"`
}

// PlanApprovedPayload marks the transition from review to execution. Source
// records whether the user or the design agent requested approval.
type PlanApprovedPayload struct {
	PlanID string                 `json:"plan_id"`
	Source DesignTransitionSource `json:"source,omitempty"`
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

// DecisionPinnedPayload records one settled design decision (§7.1, P4 E1).
// Decisions are first-class runtime data: the ledger card projects them,
// /crystallize completeness is reasoned over them, and the audit trail
// survives compaction in the raw event log. Revised marks a decision that
// supersedes an earlier pinned decision (history is never silently
// rewritten — the card renders the revision).
type DecisionPinnedPayload struct {
	Statement string `json:"statement"`
	Detail    string `json:"detail,omitempty"`
	Revised   bool   `json:"revised,omitempty"`
	Epic      string `json:"epic,omitempty"`
	Timestamp string `json:"ts,omitempty"`
}

// OpenQuestionPayload records one open design question (§7.1). A question is
// raised in conversation when the design depends on an unanswered decision.
// It is first-class runtime data like the pinned decisions: the resume card
// and the launch DECISIONS module project it, and it survives compaction in
// the raw event log. Resolution removes it from the open set — the raised
// events stay in the log (append-only audit), but only UNRESOLVED questions
// reconstruct.
type OpenQuestionPayload struct {
	Question string `json:"question"`
	Detail   string `json:"detail,omitempty"`
	// Sequence is stamped at reconstruction from the raising event's sequence
	// number; a raise and its resolve pair on identical Question text.
	Sequence int `json:"seq,omitempty"`
}

// OpenQuestionResolvedPayload marks one open question settled or withdrawn.
// The Question must match a currently-open question's text exactly; a resolve
// with no matching open question is a reconstruction error (fail closed, G2).
type OpenQuestionResolvedPayload struct {
	Question string `json:"question"`
	// Resolution names the outcome: "settled" (a decision pinned) or
	// "withdrawn" (no longer relevant). Empty is invalid.
	Resolution string `json:"resolution"`
}

func (p OpenQuestionResolvedPayload) Validate() error {
	if strings.TrimSpace(p.Question) == "" {
		return fmt.Errorf("open_question_resolved: question is required")
	}
	switch p.Resolution {
	case "settled", "withdrawn":
		return nil
	default:
		return fmt.Errorf("open_question_resolved: resolution must be settled or withdrawn, got %q", p.Resolution)
	}
}

func (p OpenQuestionPayload) Validate() error {
	if strings.TrimSpace(p.Question) == "" {
		return fmt.Errorf("open_question payload: question is required")
	}
	return nil
}

// OpenQuestionRaisedAppender returns the session-append input for one raised
// open question.
func OpenQuestionRaisedAppender(question, detail string) (sessions.AppendEventInput, error) {
	payload := OpenQuestionPayload{Question: question, Detail: detail}
	if err := payload.Validate(); err != nil {
		return sessions.AppendEventInput{}, err
	}
	raw, _ := json.Marshal(payload)
	return sessions.AppendEventInput{Type: sessions.EventOpenQuestionRaised, Payload: json.RawMessage(raw)}, nil
}

// OpenQuestionResolvedAppender returns the session-append input for one
// resolved open question.
func OpenQuestionResolvedAppender(question, resolution string) (sessions.AppendEventInput, error) {
	payload := OpenQuestionResolvedPayload{Question: question, Resolution: resolution}
	if err := payload.Validate(); err != nil {
		return sessions.AppendEventInput{}, err
	}
	raw, _ := json.Marshal(payload)
	return sessions.AppendEventInput{Type: sessions.EventOpenQuestionResolved, Payload: json.RawMessage(raw)}, nil
}

// DecisionPinnedAppender returns the session-append input for one pinned
// decision, shared by every emitter so the payload shape has one writer.
func DecisionPinnedAppender(statement, detail, epic string) sessions.AppendEventInput {
	payload, _ := json.Marshal(DecisionPinnedPayload{
		Statement: statement,
		Detail:    detail,
		Epic:      epic,
	})
	return sessions.AppendEventInput{Type: sessions.EventDecisionPinned, Payload: json.RawMessage(payload)}
}

// DesignState is the reconstructed design workflow state derived from session
// lifecycle events. It is the authoritative view; no global file backs it.
// Transient busy states (crystallizing, critic running) are not represented
// here because they are in-memory only.
type DesignState struct {
	Phase    schemas.DesignPhase
	Revision schemas.PlanRevision
	Plan     *schemas.DesignPlan
	Critique *schemas.PlanCritique
	// Decisions is the pinned-decision ledger in pin order (§7.1). A REVISED
	// decision keeps its predecessor: the ledger shows both with the
	// revision marker, never a silent rewrite.
	Decisions []DecisionPinnedPayload
	// OpenQuestions is the currently-open question set in raise order. A
	// resolved question leaves the set (the raise event stays in the log);
	// only a new design-mode epoch clears the audit trail itself.
	OpenQuestions []OpenQuestionPayload
	// TaskOutcomes is task_id -> status, including in-flight "running" for a
	// task_started event with no terminal event after it yet (e.g. a run
	// interrupted mid-task). schemas.TaskRunOutcome is strictly terminal and
	// cannot represent that state; TaskRunStatus can.
	TaskOutcomes map[string]schemas.TaskRunStatus
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
	state := DesignState{TaskOutcomes: map[string]schemas.TaskRunStatus{}}
	for _, event := range events {
		switch event.Type {
		case sessions.EventDesignModeEntered:
			state = DesignState{
				Phase:        schemas.DesignPhaseConversation,
				TaskOutcomes: map[string]schemas.TaskRunStatus{},
			}
		case sessions.EventDecisionPinned:
			var d DecisionPinnedPayload
			if err := json.Unmarshal(event.Payload, &d); err != nil {
				return DesignState{}, fmt.Errorf("design_mode decision_pinned seq %d: %w", event.Sequence, err)
			}
			if strings.TrimSpace(d.Statement) == "" {
				return DesignState{}, fmt.Errorf("design_mode decision_pinned seq %d: statement is required", event.Sequence)
			}
			// The ledger is append-only: a revised decision keeps its
			// predecessor so history is never silently rewritten (§7.1).
			state.Decisions = append(state.Decisions, d)
		case sessions.EventOpenQuestionRaised:
			var q OpenQuestionPayload
			if err := json.Unmarshal(event.Payload, &q); err != nil {
				return DesignState{}, fmt.Errorf("design_mode open_question_raised seq %d: %w", event.Sequence, err)
			}
			if err := q.Validate(); err != nil {
				return DesignState{}, fmt.Errorf("design_mode open_question_raised seq %d: %w", event.Sequence, err)
			}
			// The open set is keyed by question text: re-raising an identical
			// question replaces nothing — the first raise stays authoritative
			// (append-only set, no silent rewrite).
			for _, existing := range state.OpenQuestions {
				if existing.Question == q.Question {
					return DesignState{}, fmt.Errorf("design_mode open_question_raised seq %d: question %q is already open", event.Sequence, q.Question)
				}
			}
			q.Sequence = event.Sequence
			state.OpenQuestions = append(state.OpenQuestions, q)
		case sessions.EventOpenQuestionResolved:
			var r OpenQuestionResolvedPayload
			if err := json.Unmarshal(event.Payload, &r); err != nil {
				return DesignState{}, fmt.Errorf("design_mode open_question_resolved seq %d: %w", event.Sequence, err)
			}
			if err := r.Validate(); err != nil {
				return DesignState{}, fmt.Errorf("design_mode open_question_resolved seq %d: %w", event.Sequence, err)
			}
			idx := -1
			for i, existing := range state.OpenQuestions {
				if existing.Question == r.Question {
					idx = i
					break
				}
			}
			if idx < 0 {
				return DesignState{}, fmt.Errorf("design_mode open_question_resolved seq %d: no open question %q", event.Sequence, r.Question)
			}
			state.OpenQuestions = append(state.OpenQuestions[:idx], state.OpenQuestions[idx+1:]...)
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
			if p.Source != "" {
				if err := p.Source.Validate(); err != nil {
					return DesignState{}, fmt.Errorf("design_mode plan_crystallized seq %d: %w", event.Sequence, err)
				}
			}
			var plan schemas.DesignPlan
			if err := json.Unmarshal(p.Plan, &plan); err != nil {
				return DesignState{}, fmt.Errorf("design_mode plan_crystallized seq %d: decode plan: %w", event.Sequence, err)
			}
			state.Phase = schemas.DesignPhaseReview
			state.Revision = schemas.PlanRevision{PlanID: p.PlanID, Revision: p.Revision}
			state.Plan = &plan
			state.Critique = nil
			state.TaskOutcomes = map[string]schemas.TaskRunStatus{}
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
			if c.Source != "" {
				if err := c.Source.Validate(); err != nil {
					return DesignState{}, fmt.Errorf("design_mode critique_recorded seq %d: %w", event.Sequence, err)
				}
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
			if a.Source != "" {
				if err := a.Source.Validate(); err != nil {
					return DesignState{}, fmt.Errorf("design_mode plan_approved seq %d: %w", event.Sequence, err)
				}
			}
			state.Phase = schemas.DesignPhaseExecuting
			state.TaskOutcomes = map[string]schemas.TaskRunStatus{}
		case sessions.EventTaskStarted:
			var t TaskStartedPayload
			if err := json.Unmarshal(event.Payload, &t); err != nil {
				return DesignState{}, fmt.Errorf("design_mode task_started seq %d: %w", event.Sequence, err)
			}
			if t.TaskID == "" || t.RunID == "" {
				return DesignState{}, fmt.Errorf("design_mode task_started seq %d: task_id and run_id are required", event.Sequence)
			}
			state.TaskOutcomes[t.TaskID] = schemas.TaskRunStatus{
				TaskID: t.TaskID,
				RunID:  &t.RunID,
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
			state.TaskOutcomes[t.TaskID] = schemas.TaskRunStatus{
				TaskID: t.TaskID,
				RunID:  &t.RunID,
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
			state.TaskOutcomes[t.TaskID] = schemas.TaskRunStatus{
				TaskID: t.TaskID,
				RunID:  &t.RunID,
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
