package tui

// context_module_test.go (P3 GAP-K slice 1): the registry composes the
// sidebar sections in slot order, drops modules that do not fit whole, and
// preserves the geometry contract (token floor, blank separators). Probes
// use the real model renderers — the registry is a composition change, not
// a behavior change.

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/presentation"
)

// The registry composes every always-present module in slot order: AGENTS,
// PLAN, FILES render placeholders when empty; PIPELINE/TRAJECTORY/MEMORY/
// ACTIVITY are absent when they have nothing.
func TestContextRegistrySlotOrderAndPresence(t *testing.T) {
	m := mouseTestModel()
	lines := m.renderContextModules(30, 40)
	plain := stripSidebar(lines)
	// Always-present sections, in order.
	agents := strings.Index(plain, "AGENTS")
	plan := strings.Index(plain, "PLAN")
	files := strings.Index(plain, "FILES")
	if agents < 0 || plan < 0 || files < 0 {
		t.Fatalf("registry missing a base section: %q", plain)
	}
	if !(agents < plan && plan < files) {
		t.Fatalf("slot order wrong: agents@%d plan@%d files@%d", agents, plan, files)
	}
	// Conditional sections absent on an idle model.
	if strings.Contains(plain, "PIPELINE") {
		t.Fatalf("idle model rendered the PIPELINE section: %q", plain)
	}
	if strings.Contains(plain, "Memory") {
		t.Fatalf("idle model rendered the MEMORY section: %q", plain)
	}
	if strings.Contains(plain, "ACTIVITY") {
		t.Fatalf("idle model rendered the ACTIVITY section: %q", plain)
	}
}

// A pipeline-carrying model renders PIPELINE between PLAN and FILES, and the
// trajectory surface below it.
func TestContextRegistryPipelineAndTrajectory(t *testing.T) {
	m := mouseTestModel()
	m.pipeline.applyState(benchNodeState(4))
	lines := m.renderContextModules(40, 60)
	plain := stripSidebar(lines)
	plan := strings.Index(plain, "PLAN")
	pipeline := strings.Index(plain, "PIPELINE")
	files := strings.Index(plain, "FILES")
	if pipeline < 0 {
		t.Fatalf("pipeline-carrying model missing the PIPELINE section: %q", plain)
	}
	if !(plan < pipeline && pipeline < files) {
		t.Fatalf("pipeline slot wrong: plan@%d pipeline@%d files@%d", plan, pipeline, files)
	}
}

// Budget: a module that no longer fits is dropped WHOLE (no truncated
// section). With a budget that fits only AGENTS, nothing after it renders.
func TestContextRegistryDropsWholeUnderBudget(t *testing.T) {
	m := mouseTestModel()
	m.pipeline.applyState(benchNodeState(4))
	lines := m.renderContextModules(30, 3)
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "AGENTS") {
		t.Fatalf("first module must fit: %q", plain)
	}
	if strings.Contains(plain, "PLAN") || strings.Contains(plain, "PIPELINE") || strings.Contains(plain, "FILES") {
		t.Fatalf("sections past the budget rendered partially: %q", plain)
	}
}

// The sidebar geometry contract holds through the registry: the token line is
// pinned at the floor and every row is normalized to width.
func TestContextSidebarRegistryPreservesGeometry(t *testing.T) {
	m := mouseTestModel()
	m.width = 120
	m.height = 30
	m.altScreen = true
	m.pipeline.applyState(benchNodeState(4))
	lines := m.renderContextSidebar(sidebarWidth(m.width), m.height)
	if len(lines) != m.height {
		t.Fatalf("sidebar rows = %d, want %d", len(lines), m.height)
	}
	last := stripSidebar([]string{lines[len(lines)-1]})
	if !strings.Contains(last, "tokens") && !strings.Contains(last, "0 tokens") {
		t.Fatalf("token line not pinned at the floor: %q", last)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != sidebarWidth(m.width) {
			t.Fatalf("row %d width = %d, want %d", i, w, sidebarWidth(m.width))
		}
	}
}

// Wire-as-you-go pairing: the pipeline module reads the same presentation
// state the reducer produces (never TUI-invented data). The node renders as
// `<glyph> <id> <workspace> <label>`; the ID is the unambiguous marker.
func TestContextRegistryPipelineReadsPresentationState(t *testing.T) {
	m := mouseTestModel()
	st := presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Nodes:         []presentation.ExecutionNode{{ID: "stage_w", Label: "write files", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusRunning, Progress: 0.5, Workspace: "isolated"}},
	}
	m.pipeline.applyState(st)
	lines := m.renderContextModules(60, 60)
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "stage_w") {
		t.Fatalf("pipeline module did not project the reducer's node: %q", plain)
	}
	if !strings.Contains(plain, "50%") {
		t.Fatalf("pipeline module did not project the node's progress: %q", plain)
	}
}
