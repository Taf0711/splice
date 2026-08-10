package splice

import (
	"context"
	"fmt"
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
