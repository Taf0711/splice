// Package presentrun adapts the runtime's existing pipeline events into the
// presentation contract and accumulates presentation.State snapshots. It is
// observability, not control flow: a malformed event is counted and logged
// through the caller's warning path, never allowed to abort a pipeline run.
package presentrun

import (
	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// stageKindMap maps pipeline stage names to presentation node kinds. The
// kinds mirror the stage registry's roles (internal/splice/registry.go):
// code_writer writes code, static_analyzer analyzes, and so on. Unknown
// stage names fall back to CUSTOM so custom topologies need no code change.
var stageKindMap = map[string]presentation.NodeKind{
	"code_writer":         presentation.NodeKindWrite,
	"test_generator":      presentation.NodeKindWrite,
	"static_analyzer":     presentation.NodeKindAnalyze,
	"security_auditor":    presentation.NodeKindSecurity,
	"test_runner":         presentation.NodeKindTest,
	"acceptance_verifier": presentation.NodeKindVerify,
}

// NodeKindForStage resolves the presentation node kind for a stage name.
func NodeKindForStage(name string) presentation.NodeKind {
	if kind, ok := stageKindMap[name]; ok {
		return kind
	}
	return presentation.NodeKindCustom
}

// AdaptStageEvent maps a runtime stage event onto the presentation contract.
// Repair-loop statuses "message" and "repaired" are interventions, not stage
// transitions: a revision request proposes a rollback, and a resolved
// revision continues the run. Progress arrives as an integer 0-100 and stays
// an integer on the event; the reducer divides by 100 when it stores the
// node's float64 progress. This conversion is documented here once.
func AdaptStageEvent(event agent.StageEvent) presentation.StreamEventLike {
	switch event.Status {
	case "message":
		return presentation.InterventionEvent{
			Kind:   presentation.InterventionRollback,
			NodeID: event.Name,
			Status: string(presentation.InterventionProposed),
			Reason: event.Detail,
		}
	case "repaired":
		return presentation.InterventionEvent{
			Kind:   presentation.InterventionContinue,
			NodeID: event.Name,
			Status: string(presentation.InterventionApplied),
			Reason: event.Detail,
		}
	}
	return presentation.StageEvent{
		ID:       event.Name,
		Kind:     NodeKindForStage(event.Name),
		Status:   event.Status,
		Detail:   event.Detail,
		Progress: event.Progress,
	}
}

// AdaptPlanEvent maps the announced stage roster onto a plan event. The
// title comes from the plan identity available at emission time (the request
// intent for now; the full plan projection arrives in P1.4).
func AdaptPlanEvent(title string, stages []string) presentation.StreamEventLike {
	return presentation.PlanEvent{Title: title, StageNames: append([]string(nil), stages...)}
}

// AdaptRunEvent maps the terminal run outcome onto a run event.
func AdaptRunEvent(status, detail string) presentation.StreamEventLike {
	return presentation.RunEvent{Status: status, Detail: detail}
}

// IsTrajectoryIntervention reports whether a trajectory action represents an
// intervention worth presenting. Continue is the normal loop and abort is a
// terminal outcome (covered by the run event); neither is an intervention.
func IsTrajectoryIntervention(action schemas.TrajectoryAction) bool {
	switch action {
	case schemas.ActionRollback, schemas.ActionStepBack,
		schemas.ActionEscalateCycleDetected, schemas.ActionEscalateOscillation,
		schemas.ActionSurfaceToUser:
		return true
	}
	return false
}

// AdaptTrajectoryDecision maps a trajectory intervention onto the
// presentation contract. It returns ok=false when no target node is
// available, so the caller counts it as skipped rather than applied.
func AdaptTrajectoryDecision(action schemas.TrajectoryAction, reason, target string) (presentation.StreamEventLike, bool) {
	if target == "" {
		return nil, false
	}
	switch action {
	case schemas.ActionRollback:
		return presentation.InterventionEvent{
			Kind:   presentation.InterventionRollback,
			NodeID: target,
			Status: string(presentation.InterventionApplied),
			Reason: reason,
		}, true
	case schemas.ActionStepBack:
		return presentation.InterventionEvent{
			Kind:   presentation.InterventionStepBack,
			NodeID: target,
			Status: string(presentation.InterventionApplied),
			Reason: reason,
		}, true
	case schemas.ActionEscalateCycleDetected, schemas.ActionEscalateOscillation:
		return presentation.InterventionEvent{
			Kind:   presentation.InterventionEscalate,
			NodeID: target,
			Status: string(presentation.InterventionApplied),
			Reason: reason,
		}, true
	case schemas.ActionSurfaceToUser:
		return presentation.InterventionEvent{
			Kind:   presentation.InterventionAskUser,
			NodeID: target,
			Status: string(presentation.InterventionApplied),
			Reason: reason,
		}, true
	}
	return nil, false
}
