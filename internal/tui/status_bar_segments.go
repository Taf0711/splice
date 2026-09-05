package tui

// status_bar_segments.go (P1.4 Ideal-Iteration delta, frame esBzN):
// "the left cluster is a projection of presentation state, not a label."
// Segments (every segment optional and event-driven; the bar recomposes on
// each runtime event instead of hardcoding one layout per phase):
//
//	phase chip        color + word driven by lifecycle phase
//	context trail     breadcrumb of phases visited; grows, never rewinds
//	work segment      what the current phase is doing, in its own terms
//	agent telemetry   fan-out counts, only when work is distributed
//
// Session meters (elapsed · tokens · cost) already live on the right side of
// statusLine; this file adds only the left-cluster segments. All values are
// projections of runtime truth — the renderer invents nothing (§ invariant).

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/presentation"
)

// maxPhaseTrail caps the context trail so a long session cannot push the bar
// into drop-everything territory; the OLDEST phases fall off (the newest
// context is the relevant one).
const maxPhaseTrail = 4

// statusPhaseTrail holds the breadcrumb of lifecycle phases visited this
// session. Appended on CHANGE only (frame RRoni: "grows as Splice
// context-switches; never rewinds"), capped at maxPhaseTrail.
type statusPhaseTrail struct {
	phases []presentation.Lifecycle
}

// observe records a lifecycle transition. Same phase = no-op (the bar
// recomposes on events, the trail on phase CHANGES). An empty lifecycle is
// the unstarted sentinel and never enters the trail.
func (t *statusPhaseTrail) observe(phase presentation.Lifecycle) {
	if phase == "" {
		return
	}
	if len(t.phases) > 0 && t.phases[len(t.phases)-1] == phase {
		return
	}
	t.phases = append(t.phases, phase)
	if len(t.phases) > maxPhaseTrail {
		t.phases = t.phases[len(t.phases)-maxPhaseTrail:]
	}
}

// current returns the active phase, or "" when none has been observed.
func (t statusPhaseTrail) current() presentation.Lifecycle {
	if len(t.phases) == 0 {
		return ""
	}
	return t.phases[len(t.phases)-1]
}

// reset clears the trail (session switch: the new session has its own story).
func (t *statusPhaseTrail) reset() {
	t.phases = nil
}

// statusPhaseStyle maps a lifecycle phase to the frame's color grammar:
// design/critique-family lime vs amber, executing lime, verified green,
// regression red, recovering amber. Unmapped phases render muted.
func statusPhaseStyle(phase presentation.Lifecycle) lipgloss.Style {
	switch phase {
	case presentation.LifecycleDesign:
		return zeroTheme.accent // lime: design work
	case presentation.LifecycleCrystallizing, presentation.LifecycleCritique, presentation.LifecycleAwaitingApprove, presentation.LifecyclePlanReady:
		return zeroTheme.amber // amber: transition/review phases
	case presentation.LifecycleExecute, presentation.LifecycleVerifying, presentation.LifecycleMergeBack:
		return zeroTheme.accent // lime: execution work
	case presentation.LifecycleComplete:
		return zeroTheme.green
	default:
		return zeroTheme.muted
	}
}

// statusHealthStyle maps the health dimension onto the same grammar: health
// outranks phase color when it is alerting (a red REGRESSION chip must not
// render lime just because the phase is executing).
func statusHealthStyle(health presentation.Health) (lipgloss.Style, bool) {
	switch health {
	case presentation.HealthRegression:
		return zeroTheme.red, true
	case presentation.HealthRecovering, presentation.HealthBlockedOnUser:
		return zeroTheme.amber, true
	default:
		return lipgloss.Style{}, false
	}
}

// phaseChipSegment renders `● design` / `● regression` — color + word driven
// by lifecycle phase, health alerting overrides BOTH (a red REGRESSION chip
// must not say "executing", frame Jo2Uk's regression(red) grammar). Empty
// when no phase has been observed (the segment is optional).
func (m model) phaseChipSegment() string {
	phase := m.phaseTrail.current()
	if phase == "" {
		return ""
	}
	word := string(phase)
	if health := m.lastState.Health.Effective(); health != presentation.HealthNormal {
		if _, alerting := statusHealthStyle(health); alerting {
			word = string(health)
		}
	}
	style, alerting := statusHealthStyle(m.lastState.Health.Effective())
	if !alerting {
		style = statusPhaseStyle(phase)
	}
	return style.Render("●") + " " + style.Render(word)
}

// contextTrailSegment renders the breadcrumb `design→plan→exec` form: phases
// joined by → (fold-table covered), oldest dropped past the cap. A single
// phase renders as just that phase — no arrow, no self-referential trail.
// Empty when no phase has been observed.
func (m model) contextTrailSegment() string {
	if len(m.phaseTrail.phases) == 0 {
		return ""
	}
	parts := make([]string, len(m.phaseTrail.phases))
	for i, phase := range m.phaseTrail.phases {
		parts[i] = statusPhaseStyle(phase).Render(trailPhaseLabel(phase))
	}
	return zeroTheme.faint.Render(strings.Join(parts, "→"))
}

// trailPhaseLabel shortens a lifecycle to the frame's breadcrumb vocabulary
// (design→plan→exec): plan_ready/awaiting_approval collapse into "plan".
func trailPhaseLabel(phase presentation.Lifecycle) string {
	switch phase {
	case presentation.LifecyclePlanReady, presentation.LifecycleAwaitingApprove:
		return "plan"
	case presentation.LifecycleVerifying, presentation.LifecycleMergeBack:
		return "verify"
	default:
		return string(phase)
	}
}

// workSegment renders what the current phase is doing, in its own terms —
// the segment swaps content per phase (frame UAYbi):
//
//	design:      decisions n
//	execute:     pass/plan/tasks counts from the live presentation state
//	complete:    nothing (receipts own the terminal story)
//
// Empty whenever the phase has no counters to show (optional segment).
func (m model) workSegment() string {
	phase := m.phaseTrail.current()
	switch phase {
	case presentation.LifecycleDesign:
		if decisions, ok := m.designDecisions(); ok && len(decisions) > 0 {
			return zeroTheme.muted.Render(fmt.Sprintf("decisions %d", len(decisions)))
		}
	case presentation.LifecycleExecute, presentation.LifecycleVerifying:
		pipeline := m.pipeline.presentation()
		if pipeline.active && pipeline.total > 0 {
			return zeroTheme.muted.Render("tasks " + formatDoneTotal(pipeline.done, pipeline.total))
		}
	}
	return ""
}

// agentTelemetrySegment renders `N agents · k running` — appears ONLY when
// work is distributed (frame GwrAE: "fan-out/fan-in counts from runtime
// events"). Idle/empty = absent, never a placeholder.
func (m model) agentTelemetrySegment() string {
	agents := m.sidebarSpecialists()
	swarm := m.swarmSpawnedAgents()
	total := len(agents) + len(swarm)
	if total == 0 {
		return ""
	}
	running := 0
	for _, a := range agents {
		if a.status == specialistRunning {
			running++
		}
	}
	for _, a := range swarm {
		if a.state == "running" {
			running++
		}
	}
	return zeroTheme.muted.Render(fmt.Sprintf("%d agents · %d running", total, running))
}
