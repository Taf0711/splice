package splice

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/tools"
)

// DesignTransitionAction identifies a design-phase transition that a caller
// can request. It names the action, not a slash command: the TUI dispatches it
// through the same typed controller the manual commands use.
type DesignTransitionAction string

const (
	// DesignTransitionCrystallize crystallizes the design conversation into a
	// typed plan and runs the adversarial critic.
	DesignTransitionCrystallize DesignTransitionAction = "crystallize_design"
	// DesignTransitionApprove approves the current plan and schedules execution.
	DesignTransitionApprove DesignTransitionAction = "approve_design"
)

// DesignTransitionSource records who requested a design transition. It is
// persisted on lifecycle events so the audit can tell a manual request from
// one initiated by the design agent.
type DesignTransitionSource string

const (
	// DesignTransitionSourceManual marks a request typed by the user.
	DesignTransitionSourceManual DesignTransitionSource = "manual"
	// DesignTransitionSourceAgent marks a request made by the design agent at
	// the user's explicit direction.
	DesignTransitionSourceAgent DesignTransitionSource = "agent"
)

// DesignTransitionRequest is one queued design-phase transition. The design
// agent tools produce it; the TUI consumes it after the agent turn finishes so
// the transition never runs inside a nested provider call.
type DesignTransitionRequest struct {
	Action         DesignTransitionAction
	Source         DesignTransitionSource
	ApproveIfReady bool
}

// Validate checks the transition source.
func (s DesignTransitionSource) Validate() error {
	switch s {
	case DesignTransitionSourceManual, DesignTransitionSourceAgent:
		return nil
	default:
		return fmt.Errorf("invalid design transition source %q", s)
	}
}

// Validate checks the transition request. Malformed values return a named
// error; nothing silently defaults.
func (r DesignTransitionRequest) Validate() error {
	switch r.Action {
	case DesignTransitionCrystallize:
	case DesignTransitionApprove:
		if r.ApproveIfReady {
			return fmt.Errorf("approve_if_ready is only valid for %s", DesignTransitionCrystallize)
		}
	default:
		return fmt.Errorf("invalid design transition action %q", r.Action)
	}
	return r.Source.Validate()
}

// DesignTransitionRecorder records at most one queued transition per model
// turn. The design tools write to it; the TUI run goroutine reads it once
// after agent.Run returns. A second call in the same turn is rejected so a
// model cannot stack or silently replace transitions.
type DesignTransitionRecorder struct {
	mu  sync.Mutex
	req *DesignTransitionRequest
}

// NewDesignTransitionRecorder creates an empty recorder for one design turn.
func NewDesignTransitionRecorder() *DesignTransitionRecorder {
	return &DesignTransitionRecorder{}
}

// Record stores the transition. It returns an error when the request is
// invalid or when a transition was already recorded for this turn.
func (r *DesignTransitionRecorder) Record(req DesignTransitionRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.req != nil {
		return fmt.Errorf("%s ignored: a design transition was already requested this turn (%s)", req.Action, r.req.Action)
	}
	r.req = &req
	return nil
}

// Take returns and clears the recorded transition, or nil when none is
// recorded. It is called exactly once, by the TUI run goroutine, after the
// agent turn finishes.
func (r *DesignTransitionRecorder) Take() *DesignTransitionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	req := r.req
	r.req = nil
	return req
}

// designTransitionTool is the shared implementation of the two design
// transition tools. It only queues a host transition through a recorder; it
// never runs the crystallizer, critic, or executor itself, so its permission
// can be Allow.
type designTransitionTool struct {
	name        string
	description string
	parameters  tools.Schema
	recorder    *DesignTransitionRecorder
	action      DesignTransitionAction
}

func (t *designTransitionTool) Name() string             { return t.name }
func (t *designTransitionTool) Description() string      { return t.description }
func (t *designTransitionTool) Parameters() tools.Schema { return t.parameters }
func (t *designTransitionTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectLocalControl,
		Permission: tools.PermissionAllow,
		Reason:     "Queues a design-phase transition the host runs after the turn.",
	}
}

// NewCrystallizeDesignTool builds the crystallize_design tool. It is callable
// only when the current user explicitly asked the agent to crystallize the
// plan. approve_if_ready schedules approval only after a clean successful
// critique with no must-fix issue; it never bypasses that barrier.
func NewCrystallizeDesignTool(recorder *DesignTransitionRecorder) tools.Tool {
	return &designTransitionTool{
		name: string(DesignTransitionCrystallize),
		description: "Crystallize the design conversation into a typed plan and run the plan critic. " +
			"Call this only when the current user explicitly asked you to crystallize the plan. " +
			"Set approve_if_ready to true only when the user also explicitly asked you to approve the plan and start execution when the critique is clean.",
		parameters: tools.Schema{
			Type: "object",
			Properties: map[string]tools.PropertySchema{
				"approve_if_ready": {
					Type:        "boolean",
					Description: "When true, start execution after the plan is crystallized and no critique requires a fix. Defaults to false.",
				},
			},
			AdditionalProperties: false,
		},
		recorder: recorder,
		action:   DesignTransitionCrystallize,
	}
}

// NewApproveDesignTool builds the approve_design tool. It is exposed only when
// a current plan exists and no must-fix critique blocks execution. It is
// callable only when the current user explicitly asked the agent to approve
// the plan.
func NewApproveDesignTool(recorder *DesignTransitionRecorder) tools.Tool {
	return &designTransitionTool{
		name: string(DesignTransitionApprove),
		description: "Approve the current crystallized plan and schedule its execution. " +
			"Call this only when the current user explicitly asked you to approve the plan and start execution.",
		parameters: tools.Schema{Type: "object", AdditionalProperties: false},
		recorder:   recorder,
		action:     DesignTransitionApprove,
	}
}

func (t *designTransitionTool) Run(_ context.Context, args map[string]any) tools.Result {
	req := DesignTransitionRequest{
		Action: t.action,
		Source: DesignTransitionSourceAgent,
	}
	if t.action == DesignTransitionCrystallize {
		if raw, ok := args["approve_if_ready"]; ok {
			flag, ok := raw.(bool)
			if !ok {
				return tools.Result{
					Status: tools.StatusError,
					Output: "Error: Invalid arguments for " + t.name + ": approve_if_ready must be a boolean.",
				}
			}
			req.ApproveIfReady = flag
		}
	}
	if err := t.recorder.Record(req); err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	detail := "The transition will start after this turn finishes."
	if req.ApproveIfReady {
		detail = "Execution will start after the critique, if the critique does not require a fix."
	}
	return tools.Result{
		Status: tools.StatusOK,
		Output: t.name + " queued. " + detail,
	}
}

// DecisionRecorder accumulates design decisions pinned by the design agent
// during one turn (§7.1, P4 E1). The TUI drains it after the turn and
// appends one decision_pinned session event per decision, so the ledger
// survives compaction in the raw event log and ReconstructDesignState
// rebuilds it.
type DecisionRecorder struct {
	mu        sync.Mutex
	decisions []DecisionPinnedPayload
}

// NewDecisionRecorder creates an empty decision ledger for one design turn.
func NewDecisionRecorder() *DecisionRecorder {
	return &DecisionRecorder{}
}

// Pin records one settled decision. A statement is required; malformed
// calls return a named error and never silently default.
func (r *DecisionRecorder) Record(statement, detail string) error {
	if strings.TrimSpace(statement) == "" {
		return fmt.Errorf("pin_design_decision ignored: statement is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.decisions = append(r.decisions, DecisionPinnedPayload{Statement: statement, Detail: detail})
	return nil
}

// Take returns and clears the recorded decisions. It is called exactly once,
// by the TUI run goroutine, after the agent turn finishes.
func (r *DecisionRecorder) Take() []DecisionPinnedPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.decisions
	r.decisions = nil
	return out
}

// OpenQuestionRecorder accumulates open questions raised by the design agent
// during one turn (§7.1). It mirrors DecisionRecorder: the TUI drains it
// after the turn and appends one open_question_raised session event per
// question, so the open set survives compaction and reconstructs.
type OpenQuestionRecorder struct {
	mu        sync.Mutex
	questions []OpenQuestionPayload
}

// NewOpenQuestionRecorder creates an empty open-question ledger for one
// design turn.
func NewOpenQuestionRecorder() *OpenQuestionRecorder {
	return &OpenQuestionRecorder{}
}

// Raise records one open question. A question is required; a duplicate of a
// question already queued this turn returns a named error and queues nothing.
func (r *OpenQuestionRecorder) Raise(question, detail string) error {
	if strings.TrimSpace(question) == "" {
		return fmt.Errorf("raise_open_question ignored: question is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.questions {
		if existing.Question == question {
			return fmt.Errorf("raise_open_question ignored: %q is already open", question)
		}
	}
	r.questions = append(r.questions, OpenQuestionPayload{Question: question, Detail: detail})
	return nil
}

// Take returns and clears the recorded questions. It is called exactly once,
// by the TUI run goroutine, after the agent turn finishes.
func (r *OpenQuestionRecorder) Take() []OpenQuestionPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.questions
	r.questions = nil
	return out
}

// raiseOpenQuestionTool lets the design agent raise an open question as
// first-class runtime data. Permission is Allow: it queues a ledger entry
// the host persists after the turn, like the pin tool.
type raiseOpenQuestionTool struct {
	recorder *OpenQuestionRecorder
}

func (t *raiseOpenQuestionTool) Name() string { return "raise_open_question" }

func (t *raiseOpenQuestionTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"question": {
				Type:        "string",
				Description: "The open question in one sentence, e.g. 'are streamed bodies idempotent?'.",
			},
			"detail": {
				Type:        "string",
				Description: "Optional context: why the answer blocks the design.",
			},
		},
		Required:             []string{"question"},
		AdditionalProperties: false,
	}
}

func (t *raiseOpenQuestionTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectLocalControl,
		Permission: tools.PermissionAllow,
		Reason:     "Queues an open design question the host persists after the turn.",
	}
}

func (t *raiseOpenQuestionTool) Run(_ context.Context, args map[string]any) tools.Result {
	question, _ := args["question"].(string)
	detail, _ := args["detail"].(string)
	if err := t.recorder.Raise(question, detail); err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	return tools.Result{
		Status: tools.StatusOK,
		Output: "open question raised: " + question,
	}
}

// NewRaiseOpenQuestionTool builds the raise_open_question tool. The design
// agent calls it when the design depends on an unanswered decision; the host
// persists it as an open_question_raised event after the turn.
func NewRaiseOpenQuestionTool(recorder *OpenQuestionRecorder) tools.Tool {
	return &raiseOpenQuestionTool{recorder: recorder}
}

func (t *raiseOpenQuestionTool) Description() string {
	return "Raise one open design question as durable runtime data. " +
		"Call this when the design depends on an answer you do not have " +
		"(the user has not decided, or evidence is missing). " +
		"The question joins the open set, renders on the resume card, and " +
		"stays open until a decision settles it or it is withdrawn."
}

// pinDesignDecisionTool lets the design agent pin a settled decision as
// first-class runtime data. Permission is Allow: it queues a ledger entry
// the host persists after the turn, like the transition tools.
type pinDesignDecisionTool struct {
	recorder *DecisionRecorder
}

func (t *pinDesignDecisionTool) Name() string { return "pin_design_decision" }
func (t *pinDesignDecisionTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"statement": {
				Type:        "string",
				Description: "The settled decision in one sentence, e.g. 'retry only idempotent methods'.",
			},
			"detail": {
				Type:        "string",
				Description: "Optional supporting detail (constraints, rejected alternatives).",
			},
		},
		Required:             []string{"statement"},
		AdditionalProperties: false,
	}
}

func (t *pinDesignDecisionTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectLocalControl,
		Permission: tools.PermissionAllow,
		Reason:     "Queues a design decision the host persists after the turn.",
	}
}

func (t *pinDesignDecisionTool) Run(_ context.Context, args map[string]any) tools.Result {
	statement, _ := args["statement"].(string)
	detail, _ := args["detail"].(string)
	if err := t.recorder.Record(statement, detail); err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	return tools.Result{
		Status: tools.StatusOK,
		Output: "decision pinned: " + statement,
	}
}

// NewPinDesignDecisionTool builds the pin_design_decision tool. The design
// agent calls it when a decision settles in conversation; the host persists
// it as a decision_pinned event after the turn.
func NewPinDesignDecisionTool(recorder *DecisionRecorder) tools.Tool {
	return &pinDesignDecisionTool{recorder: recorder}
}

func (t *pinDesignDecisionTool) Description() string {
	return "Pin one settled design decision as durable runtime data. " +
		"Call this when a decision settles in conversation (the user confirmed an approach, " +
		"a constraint was agreed, or an alternative was explicitly rejected). " +
		"The pinned decision joins the design ledger and is reused when crystallizing the plan."
}
