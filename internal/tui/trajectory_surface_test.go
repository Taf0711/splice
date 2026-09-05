package tui

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/presentation"
)

func trajectoryState(health presentation.Health, scores []float64, restores []string) presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Health:        health,
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusRunning, Progress: 0.4},
		},
		Trajectory: presentation.Trajectory{
			PassScores:     scores,
			RestoreMarkers: []string{"test_runner pass 2"},
		},
	}
}

// TestTrajectoryHiddenByDefault pins DoD 11: trajectory renders only its
// hidden notice until the user (or a regression) enables it.
func TestTrajectoryHiddenByDefault(t *testing.T) {
	m := mouseTestModel()
	plain := stripANSI(m.renderTrajectorySurface(midRunProjectionState(), 100))
	if !strings.Contains(plain, "trajectory hidden") || !strings.Contains(plain, "ctrl+t to enable") {
		t.Fatalf("hidden trajectory missing the notice:\n%s", plain)
	}
}

// TestTrajectoryToggleOnCtrlT pins the toggle contract: Ctrl+T during an
// active run reveals the surface; Ctrl+T again hides it.
func TestTrajectoryToggle(t *testing.T) {
	m := mouseTestModel()
	m.pipeline.applyState(midRunProjectionState())

	updated, _ := m.Update(testKeyCtrl('t'))
	next := updated.(model)
	if !next.trajectoryVisible {
		t.Fatal("ctrl+t did not reveal trajectory during a run")
	}
	updated, _ = next.Update(testKeyCtrl('t'))
	next = updated.(model)
	if next.trajectoryVisible {
		t.Fatal("ctrl+t did not hide trajectory")
	}
}

// TestRegressionAutoRevealsTrajectory pins §10's auto-reveal: a snapshot
// reporting regression reveals trajectory AND marks it auto-revealed, so
// the header says "auto-revealed on regression" instead of implying the
// user opened it.
func TestRegressionAutoRevealsTrajectory(t *testing.T) {
	m := mouseTestModel()
	msg := presentationStateMsg{runID: m.activeRunID, state: presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Health:        presentation.HealthRegression,
		Nodes: []presentation.ExecutionNode{
			{ID: "test_runner", Label: "test_runner", Kind: presentation.NodeKindTest, Status: presentation.NodeStatusFailed},
		},
	}}
	updated, _ := m.Update(msg)
	next := updated.(model)
	if !next.trajectoryVisible {
		t.Fatal("regression did not auto-reveal trajectory")
	}
	if !next.trajectoryAutoRevealed {
		t.Fatal("auto-reveal did not record itself; the UI cannot say why trajectory opened")
	}
	if got := stripANSI(next.renderTrajectorySurface(next.lastState, 100)); !strings.Contains(got, "auto-revealed on regression") {
		t.Fatalf("auto-reveal notice missing:\n%s", got)
	}
}

// TestTrajectorySurfaceRenders pins the visible content: the P1.4 trail form
// (`100 ▔▔▔▔ 86 ▔▔▔` — score + scaled bars per pass, one line), restore
// markers, and the auto-reveal wording.
func TestTrajectorySurfaceRenders(t *testing.T) {
	m := mouseTestModel()
	m.trajectoryVisible = true
	m.trajectoryAutoRevealed = true
	state := presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Trajectory: presentation.Trajectory{
			PassScores:     []float64{1.0, 0.86},
			RestoreMarkers: []string{"snapshot @ test_runner pass 2"},
		},
	}
	plain := stripANSI(m.renderTrajectorySurface(state, 100))
	for _, want := range []string{"TRAJECTORY", "auto-revealed on regression", "100 ▔▔▔▔", "86 ▔▔▔", "restore"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("trajectory surface missing %q:\n%s", want, plain)
		}
	}
}

// No passes yet: the surface says so instead of rendering an empty trail.
func TestTrajectorySurfaceEmptyTrail(t *testing.T) {
	m := mouseTestModel()
	m.trajectoryVisible = true
	state := presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
	}
	plain := stripANSI(m.renderTrajectorySurface(state, 100))
	if !strings.Contains(plain, "no passes scored yet") {
		t.Fatalf("empty trajectory missing the honest empty state:\n%s", plain)
	}
}
