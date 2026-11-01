package tui

// p14_status_bar_test.go (P1.4 Ideal-Iteration delta, frame esBzN): the
// status bar's left cluster is a projection of presentation state. Probes:
// phase chip color+word from lifecycle, health alert override, context trail
// grows-never-rewinds and resets on session switch, work segment swaps per
// phase, agent telemetry appears only when work is distributed.

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/sessions"
)

// p14PhaseState builds a minimal legal presentation state at a phase.
func p14PhaseState(phase presentation.Lifecycle, health presentation.Health) presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     phase,
		Health:        health,
	}
}

// The phase chip renders the lifecycle word, and the context trail joins the
// visited phases in order. Delivered through the REAL status line.
func TestStatusBarPhaseChipAndTrail(t *testing.T) {
	m := mouseTestModel()
	m.phaseTrail.observe(presentation.LifecycleDesign)
	m.phaseTrail.observe(presentation.LifecycleExecute)
	m.lastState = p14PhaseState(presentation.LifecycleExecute, presentation.HealthNormal)

	plain := stripANSI(m.statusLine(160))
	if !strings.Contains(plain, "executing") {
		t.Fatalf("phase chip missing the lifecycle word:\n%s", plain)
	}
	if !strings.Contains(plain, "design→exec") {
		t.Fatalf("context trail missing the breadcrumb:\n%s", plain)
	}
}

// Health outranks phase color: a regression during executing reads
// "regression", not "executing" (a red chip must not lie lime).
func TestStatusBarHealthOverridesPhaseWord(t *testing.T) {
	m := mouseTestModel()
	m.phaseTrail.observe(presentation.LifecycleExecute)
	m.lastState = p14PhaseState(presentation.LifecycleExecute, presentation.HealthRegression)

	plain := stripANSI(m.statusLine(160))
	if !strings.Contains(plain, "regression") {
		t.Fatalf("health alert did not override the phase word:\n%s", plain)
	}
}

// The trail never rewinds and never duplicates: observing the same phase
// twice is a no-op, and the cap drops the OLDEST phase.
func TestPhaseTrailAppendOnChangeOnly(t *testing.T) {
	var trail statusPhaseTrail
	trail.observe(presentation.LifecycleDesign)
	trail.observe(presentation.LifecycleDesign) // no-op
	trail.observe(presentation.LifecycleExecute)
	trail.observe(presentation.LifecycleVerifying)
	if len(trail.phases) != 3 {
		t.Fatalf("trail = %v, want 3 phases (no duplicates)", trail.phases)
	}
	// Cap: oldest falls off.
	for i := 0; i < maxPhaseTrail+2; i++ {
		trail.observe(presentation.LifecycleExecute)
	}
	if len(trail.phases) > maxPhaseTrail {
		t.Fatalf("trail grew past the cap: %v", trail.phases)
	}
	// Reset clears.
	trail.reset()
	if len(trail.phases) != 0 {
		t.Fatalf("reset left phases behind: %v", trail.phases)
	}
}

// Wire-as-you-go pairing: the trail is observed by the SAME presentation
// state update path that feeds the pipeline — sending a state through
// m.Update grows the trail.
func TestPhaseTrailObservedOnPresentationState(t *testing.T) {
	m := mouseTestModel()
	msg := presentationStateMsg{runID: m.activeRunID, state: p14PhaseState(presentation.LifecycleDesign, presentation.HealthNormal)}
	updated, _ := m.Update(msg)
	next := updated.(model)
	if got := next.phaseTrail.current(); got != presentation.LifecycleDesign {
		t.Fatalf("trail did not observe the design phase: %v", next.phaseTrail.phases)
	}
}

// Work segment swaps per phase: design shows the decision count, executing
// shows task counts. Idle phases show nothing (optional segment).
func TestStatusBarWorkSegmentSwapsPerPhase(t *testing.T) {
	// Design phase with pinned decisions.
	m := mouseTestModel()
	m.phaseTrail.observe(presentation.LifecycleDesign)
	m.lastState = p14PhaseState(presentation.LifecycleDesign, presentation.HealthNormal)
	m.sessionEvents = []sessions.Event{
		{Type: sessions.EventDecisionPinned, Payload: p14DecisionPin(t, "pin one", false)},
		{Type: sessions.EventDecisionPinned, Payload: p14DecisionPin(t, "pin two", false)},
	}
	plain := stripANSI(m.statusLine(160))
	if !strings.Contains(plain, "decisions 2") {
		t.Fatalf("design work segment missing the decision count:\n%s", plain)
	}

	// Executing phase with a live pipeline.
	m2 := mouseTestModel()
	m2.phaseTrail.observe(presentation.LifecycleExecute)
	m2.lastState = p14PhaseState(presentation.LifecycleExecute, presentation.HealthNormal)
	m2.pipeline.applyState(benchNodeState(4))
	plain2 := stripANSI(m2.statusLine(160))
	if !strings.Contains(plain2, "tasks 2/4") {
		t.Fatalf("execute work segment missing the task count:\n%s", plain2)
	}
}

// Agent telemetry appears only when work is distributed.
func TestStatusBarAgentTelemetryConditional(t *testing.T) {
	m := mouseTestModel()
	if seg := m.agentTelemetrySegment(); seg != "" {
		t.Fatalf("idle model rendered agent telemetry: %q", seg)
	}
	m.specialists.start("worker", "runs the task", "child-1", m.now())
	if seg := m.agentTelemetrySegment(); !strings.Contains(seg, "1 agents · 1 running") {
		t.Fatalf("distributed work missing telemetry: %q", seg)
	}
}
