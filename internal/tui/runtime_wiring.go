package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/tools"
)

// runtimeWiring is the single seam every TUI-initiated agent run passes
// through. Both the /exec path (runAgentWithOptions) and the /approve path
// (startApprovalConfirmed) build one and call decorate. The decorator owns
// shared UI transport and interactive fallbacks. Path-specific callbacks keep
// their nil semantics. TestRunPathsFollowCallbackPolicies fails when a callback
// is added without an explicit policy.
type runtimeWiring struct {
	runID      int
	send       func(tea.Msg)
	beforeText func(string)
}

// decorate wraps the callbacks that have shared TUI behavior. Each wrapper
// calls the prior value when non-nil. Callbacks without shared behavior remain
// unchanged, including intentional nil values that disable optional work.
func (w runtimeWiring) decorate(options agent.Options) agent.Options {
	priorText := options.OnText
	options.OnText = func(delta string) {
		if w.beforeText != nil {
			w.beforeText(delta)
		}
		if w.send != nil {
			w.send(agentTextMsg{runID: w.runID, delta: delta})
		}
		if priorText != nil {
			priorText(delta)
		}
	}

	priorReasoning := options.OnReasoning
	options.OnReasoning = func(delta string) {
		if strings.HasPrefix(delta, "\x00STAGE") {
			return
		}
		if w.send != nil {
			w.send(agentReasoningMsg{runID: w.runID, delta: delta})
		}
		if priorReasoning != nil {
			priorReasoning(delta)
		}
	}

	priorToolCallStart := options.OnToolCallStart
	options.OnToolCallStart = func(id, name string) {
		if w.send != nil {
			w.send(toolCallStreamStartMsg{runID: w.runID, id: id, name: name})
		}
		if priorToolCallStart != nil {
			priorToolCallStart(id, name)
		}
	}
	priorToolCallDelta := options.OnToolCallDelta
	options.OnToolCallDelta = func(id, fragment string) {
		if w.send != nil {
			w.send(toolCallStreamDeltaMsg{runID: w.runID, id: id, fragment: fragment})
		}
		if priorToolCallDelta != nil {
			priorToolCallDelta(id, fragment)
		}
	}

	priorPermissionRequest := options.OnPermissionRequest
	options.OnPermissionRequest = func(ctx context.Context, request agent.PermissionRequest) (agent.PermissionDecision, error) {
		if priorPermissionRequest != nil {
			return priorPermissionRequest(ctx, request)
		}
		if w.send == nil {
			return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "permission prompt unavailable"}, nil
		}
		decisionCh := make(chan agent.PermissionDecision, 1)
		w.send(permissionRequestMsg{runID: w.runID, request: request, decide: func(decision agent.PermissionDecision) {
			select {
			case decisionCh <- decision:
			default:
			}
		}})
		select {
		case decision := <-decisionCh:
			if strings.TrimSpace(decision.Reason) == "" {
				decision.Reason = permissionDecisionReason(permissionDecision(decision.Action))
			}
			return decision, nil
		case <-ctx.Done():
			return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: ctx.Err().Error()}, ctx.Err()
		}
	}

	priorAskUser := options.OnAskUser
	options.OnAskUser = func(ctx context.Context, request agent.AskUserRequest) (agent.AskUserResponse, error) {
		if priorAskUser != nil {
			return priorAskUser(ctx, request)
		}
		if w.send == nil {
			return agent.AskUserResponse{}, fmt.Errorf("ask_user prompt unavailable")
		}
		answerCh := make(chan []string, 1)
		w.send(askUserRequestMsg{runID: w.runID, request: request, answer: func(answers []string) {
			select {
			case answerCh <- answers:
			default:
			}
		}})
		select {
		case answers := <-answerCh:
			return agent.AskUserResponse{Answers: answers}, nil
		case <-ctx.Done():
			return agent.AskUserResponse{}, ctx.Err()
		}
	}

	priorToolOutput := options.OnToolOutput
	options.OnToolOutput = func(snapshot tools.OutputSnapshot) {
		if priorToolOutput == nil && w.send != nil {
			w.send(toolOutputSnapshotMsg{runID: w.runID, id: snapshot.ToolCallID, snapshot: snapshot.Output})
		}
		if priorToolOutput != nil {
			priorToolOutput(snapshot)
		}
	}
	priorSurfaceToUser := options.OnSurfaceToUser
	options.OnSurfaceToUser = func(ctx context.Context, request agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error) {
		if priorSurfaceToUser != nil {
			return priorSurfaceToUser(ctx, request)
		}
		if w.send == nil {
			return agent.SurfaceToUserDecision{Action: agent.SurfaceToUserAbort, Message: "interactive surface unavailable"}, nil
		}
		answerCh := make(chan []string, 1)
		askRequest := surfaceToUserAskRequest(request)
		w.send(askUserRequestMsg{runID: w.runID, request: askRequest, answer: func(answers []string) {
			select {
			case answerCh <- answers:
			default:
			}
		}})
		select {
		case answers := <-answerCh:
			return surfaceToUserFromAnswers(answers), nil
		case <-ctx.Done():
			return agent.SurfaceToUserDecision{Action: agent.SurfaceToUserAbort, Message: ctx.Err().Error()}, ctx.Err()
		}
	}
	priorPipelinePlan := options.OnPipelinePlan
	options.OnPipelinePlan = func(event agent.PipelinePlanEvent) {
		if w.send != nil {
			copied := agent.PipelinePlanEvent{Stages: append([]string(nil), event.Stages...)}
			w.send(pipelinePlanMsg{runID: w.runID, event: copied})
		}
		if priorPipelinePlan != nil {
			priorPipelinePlan(event)
		}
	}
	priorStageEvent := options.OnStageEvent
	options.OnStageEvent = func(event agent.StageEvent) {
		if w.send != nil {
			w.send(pipelineStageEventMsg{runID: w.runID, event: event})
		}
		if priorStageEvent != nil {
			priorStageEvent(event)
		}
	}
	// OnPresentationState stays nil on the TUI path until P1.2, when the TUI
	// renders from presentation state snapshots. The wiring test pins this
	// as path-specific so the flip is a deliberate, reviewed change.
	return options
}
