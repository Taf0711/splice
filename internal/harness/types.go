package harness

// This package defines the typed harness seam. A harness is an external
// consumer that uses the same projection, surfaces, approvals, logs, and
// status the TUI uses. The seam is typed. It is not a plugin system.

import (
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/agent"
)

// RunEventKind discriminates the typed run events a harness can render.
type RunEventKind string

const (
	RunEventPlan       RunEventKind = "plan"       // the ordered stage roster
	RunEventStage      RunEventKind = "stage"      // stage lifecycle
	RunEventTool       RunEventKind = "tool"       // one settled tool call
	RunEventPermission RunEventKind = "permission" // a permission request/decision
	RunEventUsage      RunEventKind = "usage"      // usage and cost
	RunEventText       RunEventKind = "text"       // a text delta
	RunEventReasoning  RunEventKind = "reasoning"  // a reasoning delta
	RunEventFinal      RunEventKind = "final"      // the final answer
)

// RunEvent is the typed envelope a harness consumes. The producer is the
// orchestrator (agent.Run), which already emits typed PipelinePlanEvent and
// StageEvent values. The harness adapter forwards those callbacks into this
// envelope; nothing is re-derived here.
type RunEvent struct {
	Kind      RunEventKind
	Plan      *agent.PipelinePlanEvent
	Stage     *agent.StageEvent
	Tool      *agent.ToolCall
	Perm      *agent.PermissionRequest
	Usage     *agent.Usage
	Text      string
	Reasoning string
	Final     string
}

// Validate checks that the payload matches the kind.
func (e RunEvent) Validate() error {
	switch e.Kind {
	case RunEventPlan:
		if e.Plan == nil {
			return fmt.Errorf("plan event requires a plan")
		}
	case RunEventStage:
		if e.Stage == nil {
			return fmt.Errorf("stage event requires a stage")
		}
	case RunEventTool:
		if e.Tool == nil {
			return fmt.Errorf("tool event requires a tool call")
		}
	case RunEventPermission:
		if e.Perm == nil {
			return fmt.Errorf("permission event requires a permission request")
		}
	case RunEventUsage:
		if e.Usage == nil {
			return fmt.Errorf("usage event requires a usage record")
		}
	case RunEventText, RunEventReasoning, RunEventFinal:
		// free-text payload; empty is allowed for a terminal event
	default:
		return fmt.Errorf("unknown run event kind %q", e.Kind)
	}
	return nil
}

// ControlCommandKind selects the typed control command.
type ControlCommandKind string

const (
	CtrlCancelRun   ControlCommandKind = "cancel_run"
	CtrlPauseRun    ControlCommandKind = "pause_run"
	CtrlResumeRun   ControlCommandKind = "resume_run"
	CtrlSetModel    ControlCommandKind = "set_model"
	CtrlGrantPermit ControlCommandKind = "grant_permission"
	CtrlDenyPermit  ControlCommandKind = "deny_permission"
)

// ControlCommand is a typed inbound command from a harness. It is not a
// free-form string; Validate rejects unknown kinds and payload mismatches.
type ControlCommand struct {
	Kind   ControlCommandKind
	Model  string // CtrlSetModel
	PermID string // CtrlGrantPermit / CtrlDenyPermit
}

// Validate checks the command kind and its payload.
func (c ControlCommand) Validate() error {
	switch c.Kind {
	case CtrlSetModel:
		if strings.TrimSpace(c.Model) == "" {
			return fmt.Errorf("set_model requires a model name")
		}
	case CtrlGrantPermit, CtrlDenyPermit:
		if strings.TrimSpace(c.PermID) == "" {
			return fmt.Errorf("%s requires a permission id", c.Kind)
		}
	case CtrlCancelRun, CtrlPauseRun, CtrlResumeRun:
		// these commands carry no payload
		if c.PermID != "" || strings.TrimSpace(c.Model) != "" {
			return fmt.Errorf("%s must not carry a payload", c.Kind)
		}
	default:
		return fmt.Errorf("unknown control command kind %q", c.Kind)
	}
	return nil
}

// CapabilitySet declares what the harness can do. The harness owns this
// value. The adapter routes a ControlCommand only when the set declares the
// capability the command needs. Only the capabilities a command gate reads
// exist here. The contract design records additional surface capabilities
// (plan projection, logs, status, session resume) as future extensions; they
// are not shipped until a real adapter needs them.
type CapabilitySet struct {
	ShowApprovals bool
	SetModel      bool
}

// Requires reports whether the set declares the capability cmd needs.
// This is the guard between capability and command: an undeclared command is
// rejected before routing, so a harness cannot reach a core control it has
// not declared.
func (c CapabilitySet) Requires(cmd ControlCommand) bool {
	switch cmd.Kind {
	case CtrlSetModel:
		return c.SetModel
	case CtrlGrantPermit, CtrlDenyPermit:
		return c.ShowApprovals
	case CtrlCancelRun, CtrlPauseRun, CtrlResumeRun:
		return true // lifecycle control is always allowed; it is not a surface
	default:
		return false
	}
}
