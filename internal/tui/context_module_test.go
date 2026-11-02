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
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// withPlanAndFile seeds one plan step and one touched file, so PLAN and FILES
// have content to render.
func withPlanAndFile(m model) model {
	m.plan.steps = []planStep{{content: "x", status: "pending"}}
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolResult, tool: "write_file", changedFiles: []string{"main.go"}})
	return m
}

// Sections with nothing to show are absent: an idle model renders no AGENTS,
// PLAN, or FILES placeholder, and no PIPELINE, MEMORY, or ACTIVITY section.
// With a plan and a touched file, PLAN precedes FILES.
func TestContextRegistrySlotOrderAndPresence(t *testing.T) {
	m := mouseTestModel()
	m.designMode = false // design mode shows the RUN module on an otherwise idle model
	if lines := m.renderContextModules(30, 40); len(lines) != 0 {
		t.Fatalf("idle model rendered sections, want none: %q", stripSidebar(lines))
	}
	m = withPlanAndFile(m)
	plain := stripSidebar(m.renderContextModules(30, 40))
	plan := strings.Index(plain, "PLAN")
	files := strings.Index(plain, "FILES")
	if plan < 0 || files < 0 || strings.Contains(plain, "AGENTS") {
		t.Fatalf("expected PLAN and FILES only: %q", plain)
	}
	if !(plan < files) {
		t.Fatalf("slot order wrong: plan@%d files@%d", plan, files)
	}
}

// With a transcript but nothing to show, the sidebar carries one hint line in
// place of the empty sections, and no token readout (there is no figure yet).
func TestSidebarIdleShowsHintNotPlaceholders(t *testing.T) {
	m := mouseTestModel()
	m.designMode = false
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file", detail: "main.go"})
	lines := m.renderContextSidebar(sidebarWidth(m.width), m.height)
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "Run details show here.") {
		t.Fatalf("idle sidebar missing the hint line: %q", plain)
	}
	for _, placeholder := range []string{"no agents spawned", "no active plan", "no files touched", "after next turn", "0 tokens"} {
		if strings.Contains(plain, placeholder) {
			t.Fatalf("idle sidebar still renders %q: %q", placeholder, plain)
		}
	}
	if last := strings.TrimSpace(stripSidebar(lines[len(lines)-1:])); last != "" {
		t.Fatalf("token row = %q, want blank with no figure", last)
	}
}

// A pipeline-carrying model renders PIPELINE between PLAN and FILES, and the
// trajectory surface below it.
func TestContextRegistryPipelineAndTrajectory(t *testing.T) {
	m := withPlanAndFile(mouseTestModel())
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
// section). With a budget that fits only the one-step PLAN, nothing after it renders.
func TestContextRegistryDropsWholeUnderBudget(t *testing.T) {
	m := mouseTestModel()
	m = withPlanAndFile(m)
	m.pipeline.applyState(benchNodeState(4))
	lines := m.renderContextModules(30, 3)
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "PLAN") {
		t.Fatalf("first module must fit: %q", plain)
	}
	if strings.Contains(plain, "PIPELINE") || strings.Contains(plain, "FILES") {
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
	m, _ = m.recordUsageEvent(m.modelName, zeroruntime.Usage{InputTokens: 100, OutputTokens: 20})
	lines := m.renderContextSidebar(sidebarWidth(m.width), m.height)
	if len(lines) != m.height {
		t.Fatalf("sidebar rows = %d, want %d", len(lines), m.height)
	}
	last := stripSidebar([]string{lines[len(lines)-1]})
	if !strings.Contains(last, "tokens") {
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
