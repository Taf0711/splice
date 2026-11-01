package harness

// adapter.go maps the existing agent.Options callbacks into typed RunEvent
// values and routes ControlCommand values to the existing core controls. The
// core does not import this package. An adapter wraps agent.Options and
// forwards; nothing is re-derived here.

import (
	"context"
	"fmt"

	"github.com/Taf0711/splice/internal/agent"
)

// Sink receives typed run events. It is the harness side of the seam.
type Sink interface {
	Send(RunEvent)
}

// Controls are the core controls a harness may drive. They are the existing
// surfaces: the run's cancel function, the permission responder, and the
// model switcher. Nil controls reject their commands; a harness cannot reach
// a control the run does not expose.
type Controls struct {
	CancelRun func()
	PauseRun  func()
	ResumeRun func()
	// DecidePermission answers a permission request the same way the TUI and
	// exec do: through agent.Options.OnPermissionRequest. It is the approval
	// surface of the core.
	DecidePermission func(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error)
	// ResolvePermission applies the harness decision to a pending permission
	// request id. The wiring layer fills this in TP5. It looks up the pending
	// request, calls DecidePermission with the harness-chosen action, and
	// returns the decision the core expects. Nil until a wiring registers it.
	ResolvePermission func(id string, action agent.PermissionDecisionAction) error
	// SetModel is nil unless the run exposes model switching. Command routing
	// rejects set_model when it is nil.
	SetModel func(model string) error
}

// Wire returns a copy of options with the harness callbacks attached. It
// forwards the typed events the orchestrator already emits into sink, and it
// leaves every other callback untouched. Callers pass the returned options to
// agent.Run (or splicerun.Run).
func Wire(options agent.Options, sink Sink) agent.Options {
	wrap := options

	priorPlan := options.OnPipelinePlan
	wrap.OnPipelinePlan = func(event agent.PipelinePlanEvent) {
		if sink != nil {
			copied := event
			copied.Stages = append([]string(nil), event.Stages...)
			sink.Send(RunEvent{Kind: RunEventPlan, Plan: &copied})
		}
		if priorPlan != nil {
			priorPlan(event)
		}
	}

	priorStage := options.OnStageEvent
	wrap.OnStageEvent = func(event agent.StageEvent) {
		if sink != nil {
			copied := event
			copied.ChangedFiles = append([]string(nil), event.ChangedFiles...)
			sink.Send(RunEvent{Kind: RunEventStage, Stage: &copied})
		}
		if priorStage != nil {
			priorStage(event)
		}
	}

	priorTool := options.OnToolCall
	wrap.OnToolCall = func(call agent.ToolCall) {
		if sink != nil {
			copied := call
			sink.Send(RunEvent{Kind: RunEventTool, Tool: &copied})
		}
		if priorTool != nil {
			priorTool(call)
		}
	}

	priorPerm := options.OnPermissionRequest
	wrap.OnPermissionRequest = func(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
		if sink != nil {
			copied := req
			sink.Send(RunEvent{Kind: RunEventPermission, Perm: &copied})
		}
		if priorPerm != nil {
			return priorPerm(ctx, req)
		}
		// No prior responder: deny by default. This matches the fail-closed
		// contract of the permission surface.
		return agent.PermissionDecision{Action: agent.PermissionDecisionDeny}, nil
	}

	priorUsage := options.OnUsage
	wrap.OnUsage = func(usage agent.Usage) {
		if sink != nil {
			sink.Send(RunEvent{Kind: RunEventUsage, Usage: &usage})
		}
		if priorUsage != nil {
			priorUsage(usage)
		}
	}

	priorText := options.OnText
	wrap.OnText = func(delta string) {
		if sink != nil {
			sink.Send(RunEvent{Kind: RunEventText, Text: delta})
		}
		if priorText != nil {
			priorText(delta)
		}
	}

	priorReasoning := options.OnReasoning
	wrap.OnReasoning = func(delta string) {
		if sink != nil {
			sink.Send(RunEvent{Kind: RunEventReasoning, Reasoning: delta})
		}
		if priorReasoning != nil {
			priorReasoning(delta)
		}
	}

	return wrap
}

// Route applies a control command to the given controls. It rejects a command
// that fails validation or that the capability set does not declare. It also
// rejects a command whose control is nil (the run does not expose it).
func Route(cmd ControlCommand, caps CapabilitySet, ctrl Controls) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	if !caps.Requires(cmd) {
		return errCapabilityNotDeclared(cmd)
	}
	switch cmd.Kind {
	case CtrlCancelRun:
		if ctrl.CancelRun == nil {
			return errControlUnavailable(cmd)
		}
		ctrl.CancelRun()
	case CtrlPauseRun:
		if ctrl.PauseRun == nil {
			return errControlUnavailable(cmd)
		}
		ctrl.PauseRun()
	case CtrlResumeRun:
		if ctrl.ResumeRun == nil {
			return errControlUnavailable(cmd)
		}
		ctrl.ResumeRun()
	case CtrlSetModel:
		if ctrl.SetModel == nil {
			return errControlUnavailable(cmd)
		}
		return ctrl.SetModel(cmd.Model)
	case CtrlGrantPermit, CtrlDenyPermit:
		if ctrl.ResolvePermission == nil {
			return errControlUnavailable(cmd)
		}
		action := agent.PermissionDecisionDeny
		if cmd.Kind == CtrlGrantPermit {
			action = agent.PermissionDecisionAllow
		}
		return ctrl.ResolvePermission(cmd.PermID, action)
	}
	return nil
}

func errCapabilityNotDeclared(cmd ControlCommand) error {
	return fmt.Errorf("harness: capability for command %q is not declared", cmd.Kind)
}

func errControlUnavailable(cmd ControlCommand) error {
	return fmt.Errorf("harness: control for command %q is unavailable", cmd.Kind)
}
