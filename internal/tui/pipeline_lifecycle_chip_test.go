package tui

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/presentation"
)

// phaseChipState builds a snapshot in the given lifecycle/health/gate with
// one running node so the panel is active.
func phaseChipState(lifecycle presentation.Lifecycle, health presentation.Health, gate *presentation.GateView) presentation.State {
	return presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     lifecycle,
		Health:        health,
		Gate:          gate,
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Label: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusRunning, Progress: 0.4},
		},
	}
}

// TestPipelineHeaderProjectsPhaseHealthGate pins the P8 state-chip contract:
// the pipeline header reads `phase | health | gate` from the presentation
// snapshot, and normal/absent segments disappear instead of printing noise.
func TestPipelineHeaderProjectsPhaseHealthGate(t *testing.T) {
	cases := []struct {
		name     string
		state    presentation.State
		contains []string
		absent   []string
	}{
		{
			name:     "executing normal no gate shows phase only",
			state:    phaseChipState(presentation.LifecycleExecute, presentation.HealthNormal, nil),
			contains: []string{"executing"},
			absent:   []string{"normal", "gate"},
		},
		{
			name:     "regression shows health",
			state:    phaseChipState(presentation.LifecycleExecute, presentation.HealthRegression, nil),
			contains: []string{"executing", "regression"},
		},
		{
			name: "blocked on user with gate shows both",
			state: phaseChipState(presentation.LifecycleDesign, presentation.HealthBlockedOnUser,
				&presentation.GateView{Kind: presentation.GateAskUser, Prompt: "buffer bodies?", Blocking: true}),
			contains: []string{"design", "blocked_on_user", "gate ask_user"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var panel pipelinePanelState
			panel.applyState(tc.state)
			// A generous budget: these assertions cover segment composition,
			// not width-pressure dropping (covered below).
			chip := panel.lifecycleChip(120)
			for _, want := range tc.contains {
				if !strings.Contains(chip, want) {
					t.Fatalf("chip %q missing %q", chip, want)
				}
			}
			for _, gone := range tc.absent {
				if strings.Contains(chip, gone) {
					t.Fatalf("chip %q should not contain %q", chip, gone)
				}
			}
		})
	}
}

// TestLifecycleChipDropsSegmentsByPriority pins DoD 18: under width pressure
// segments drop whole (never ellipsis-truncated mid-word), gate preempts
// health, and the phase is the mandatory base.
func TestLifecycleChipDropsSegmentsByPriority(t *testing.T) {
	var panel pipelinePanelState
	panel.applyState(phaseChipState(presentation.LifecycleCritique, presentation.HealthBlockedOnUser,
		&presentation.GateView{Kind: presentation.GateCritiqueBlk, Prompt: "2 required", Blocking: true}))

	// Everything fits.
	full := panel.lifecycleChip(120)
	if full != "critique | blocked_on_user | gate critique_block" {
		t.Fatalf("full chip = %q", full)
	}
	// Tight: gate preempts health, both words remain whole.
	tight := panel.lifecycleChip(len("critique | gate critique_block"))
	if tight != "critique | gate critique_block" {
		t.Fatalf("tight chip = %q", tight)
	}
	if strings.Contains(tight, "…") {
		t.Fatalf("chip truncated mid-word under pressure: %q", tight)
	}
	// Tighter still: the gate is never sacrificed (DoD 22), so the gate
	// survives even when it overflows; it renders whole rather than
	// truncating. Only health ever drops.
	tighter := panel.lifecycleChip(len("critique"))
	if tighter != "critique | gate critique_block" {
		t.Fatalf("tighter chip = %q", tighter)
	}
	// Without a gate, health drops whole at tight widths.
	var panel2 pipelinePanelState
	panel2.applyState(phaseChipState(presentation.LifecycleCritique, presentation.HealthBlockedOnUser, nil))
	healthDrops := panel2.lifecycleChip(len("critique"))
	if healthDrops != "critique" {
		t.Fatalf("no-gate tight chip = %q, want phase only", healthDrops)
	}
	if strings.Contains(healthDrops, "…") {
		t.Fatalf("chip truncated mid-word under pressure: %q", healthDrops)
	}
}

// TestSidebarRendersLifecycleChip proves the chip is WIRED: the sidebar's
// pipeline section header carries the phase/health/gate readout end-to-end
// from the presentation snapshot.
func TestSidebarRendersLifecycleChip(t *testing.T) {
	m := sidebarTestModel()
	// Width 140 gives a 40-cell sidebar (the max), so the full
	// `executing | regression` chip fits the header budget without
	// dropping segments. Width-pressure dropping is unit-tested in
	// TestLifecycleChipDropsSegmentsByPriority.
	m.width = 140
	m.pipeline.applyState(phaseChipState(presentation.LifecycleExecute, presentation.HealthRegression, nil))
	plain := stripSidebar(m.renderContextSidebar(sidebarWidth(m.width), m.height))
	// Header labels render upper-cased (sidebarHeaderWithCount).
	if !strings.Contains(plain, "EXECUTING") || !strings.Contains(plain, "REGRESSION") {
		t.Fatalf("sidebar pipeline header missing phase/health chip, got:\n%s", plain)
	}
}
