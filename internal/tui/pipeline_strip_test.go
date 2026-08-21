package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/agent"
)

// stateWith builds a pipelinePanelState with the given ordered stages.
func stateWith(stages []pipelineStageRow) pipelinePanelState {
	return pipelinePanelState{active: true, stages: stages}
}

func TestPipelineStageLabelAbbreviatesNames(t *testing.T) {
	cases := []struct{ name, want string }{
		{"code_writer", "cw"},
		{"static_analyzer", "sa"},
		{"test_runner", "tr"},
		{"acceptance_verifier", "av"},
		{"planner", "pl"},
		{"x", "x"},
		{"", "?"},
		{"a_b", "ab"},
	}
	for _, tc := range cases {
		if got := pipelineStageLabel(tc.name); got != tc.want {
			t.Fatalf("pipelineStageLabel(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPipelineStripStateDerivation(t *testing.T) {
	cases := []struct {
		name   string
		stages []pipelineStageRow
		want   pipelineStripState
	}{
		{name: "no stages", stages: nil, want: pipelineStripInactive},
		{name: "all pending nothing running", stages: []pipelineStageRow{{name: "code_writer", status: pipelineStagePending}}, want: pipelineStripNoRunning},
		{name: "fresh running", stages: []pipelineStageRow{{name: "test_runner", status: pipelineStageRunning}}, want: pipelineStripRunning},
		{name: "reentry repair", stages: []pipelineStageRow{{name: "test_runner", status: pipelineStageRunning, reentered: true}}, want: pipelineStripRepair},
		{name: "failed", stages: []pipelineStageRow{{name: "code_writer", status: pipelineStageFailed}}, want: pipelineStripFailed},
		{name: "all done", stages: []pipelineStageRow{{name: "code_writer", status: pipelineStageCompleted}}, want: pipelineStripDone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := stateWith(tc.stages).presentation()
			if got := p.stripState(); got != tc.want {
				t.Fatalf("stripState = %d, want %d", got, tc.want)
			}
		})
	}
	if !stateWith(nil).presentation().active {
		t.Fatal("presentation of active empty state should still report active")
	}
}

// TestPipelineStripRepairIsReentryNotInventedStage: a repair is a terminal
// stage re-entering as running (via the reentered flag), never a synthetic
// "repair" stage name. The roster must stay exactly the planned stages.
func TestPipelineStripRepairIsReentryNotInventedStage(t *testing.T) {
	var state pipelinePanelState
	state.applyPlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner", "acceptance_verifier"}})
	// First pass: code_writer completes, test_runner runs then fails.
	state.applyStageEvent(agent.StageEvent{Name: "code_writer", Status: "completed", Progress: 100})
	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "failed", Progress: 100})

	state.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "running", Detail: "repair retry"})
	if len(state.stages) != 3 {
		t.Fatalf("repair invented a stage: stages = %d, want 3", len(state.stages))
	}
	names := make([]string, len(state.stages))
	for i, s := range state.stages {
		names[i] = s.name
	}
	if strings.Join(names, ",") != "code_writer,test_runner,acceptance_verifier" {
		t.Fatalf("roster changed by repair: %v", names)
	}
	p := state.presentation()
	if p.stripState() != pipelineStripRepair {
		t.Fatalf("stripState = %d, want repair", p.stripState())
	}
	if !p.current.reentered {
		t.Fatal("repair stage should be marked reentered")
	}
}

func TestPipelineStripWidthDegradation(t *testing.T) {
	p := stateWith([]pipelineStageRow{
		{name: "code_writer", status: pipelineStageCompleted},
		{name: "test_runner", status: pipelineStageRunning, detail: "jigging", progress: 50},
		{name: "acceptance_verifier", status: pipelineStagePending},
	}).presentation()

	cases := []struct{ width, wantLines int }{
		{width: 100, wantLines: 3}, // full: header + stage run + bar
		{width: 80, wantLines: 3},  // medium: header + stage run + bar
		{width: 66, wantLines: 1},  // narrow: header + current label, one line
		{width: 50, wantLines: 1},  // tiny: header only
		{width: 38, wantLines: 1},  // tiny: header only
	}
	for _, tc := range cases {
		t.Run(string(rune('0'+(tc.width/10)%10))+string(rune('0'+tc.width%10)), func(t *testing.T) {
			lines := p.renderStrip(tc.width, 0)
			joined := strings.Join(lines, "\n")
			if len(lines) != tc.wantLines {
				t.Fatalf("width %d: renderStrip = %d lines, want %d\n%s", tc.width, len(lines), tc.wantLines, joined)
			}
		})
	}
}

func TestPipelineStripTruthfulHeaderCount(t *testing.T) {
	p := stateWith([]pipelineStageRow{
		{name: "code_writer", status: pipelineStageCompleted},
		{name: "test_runner", status: pipelineStageRunning, progress: 50},
		{name: "acceptance_verifier", status: pipelineStagePending},
	}).presentation()
	plain := plainRender(t, strings.Join(p.renderStrip(50, 0), "\n"))
	if !strings.Contains(plain, "1/3") {
		t.Fatalf("strip header missing truthful done/total: %q", plain)
	}
	// LLM-stage progress must not leak a fake aggregate: with one running at 40
	// progress over 3 stages the aggregate is 40/3 = 13, shown only in the bar.
	if strings.Contains(plain, "100%") {
		t.Fatalf("strip over-reporting progress: %q", plain)
	}
}

func TestPipelineStripHeaderColorByState(t *testing.T) {
	// Failed roster -> red header (the full header line is colored).
	failed := stateWith([]pipelineStageRow{{name: "code_writer", status: pipelineStageFailed}}).presentation()
	got := failed.renderStrip(50, 0)[0]
	want := zeroTheme.red.Render("PIPELINE 1/1")
	if got != want {
		t.Fatalf("failed strip header = %q, want red %q", got, want)
	}
	// Done roster -> green header.
	done := stateWith([]pipelineStageRow{{name: "code_writer", status: pipelineStageCompleted}}).presentation()
	got = done.renderStrip(50, 0)[0]
	want = zeroTheme.green.Render("PIPELINE 1/1")
	if got != want {
		t.Fatalf("done strip header = %q, want green %q", got, want)
	}
	// A running roster stays amber.
	running := stateWith([]pipelineStageRow{{name: "code_writer", status: pipelineStageRunning}}).presentation()
	got = running.renderStrip(50, 0)[0]
	want = zeroTheme.amber.Render("PIPELINE 0/1")
	if got != want {
		t.Fatalf("running strip header = %q, want amber %q", got, want)
	}
}

// pipelineStripModel returns a narrow single-column model (no sidebar geometry)
// with an active pipeline roster, ready for footer rendering.
func pipelineStripModel(t *testing.T) model {
	t.Helper()
	m := newModel(t.Context(), Options{})
	m.width = 66
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true
	m.pipeline.applyPlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner", "acceptance_verifier"}})
	m.pipeline.applyStageEvent(agent.StageEvent{Name: "code_writer", Status: "completed", Progress: 100})
	m.pipeline.applyStageEvent(agent.StageEvent{Name: "test_runner", Status: "running", Detail: "writing tests", Progress: 50})
	return m
}

// TestPipelineStripShowsWhenSidebarCannotHost: on a narrow single-column
// terminal the strip renders in the footer; it must never render alongside the
// sidebar PIPELINE section (would duplicate the roster).
func TestPipelineStripShowsWhenSidebarCannotHost(t *testing.T) {
	m := pipelineStripModel(t)
	if m.sidebarAvailable() {
		t.Skip("precondition: narrow model should have no sidebar")
	}
	footer := plainRender(t, m.footerView(m.chatColumnWidth()))
	if !strings.Contains(footer, "PIPELINE") {
		t.Fatal("strip missing from narrow footer")
	}
}

// TestPipelineStripHiddenWhenSidebarHosts: wide alt-screen terminal with real
// conversation and a plan lets the sidebar host the PIPELINE. The strip must be
// suppressed so the roster is not rendered twice.
func TestPipelineStripHiddenWhenSidebarHosts(t *testing.T) {
	m := mouseTestModel()
	m.width = 110
	m.height = 40
	m.pipeline.applyPlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner", "acceptance_verifier"}})
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file"})
	if !m.sidebarAvailable() {
		t.Fatal("precondition: wide terminal should have sidebar available")
	}
	footer := plainRender(t, m.footerView(m.chatColumnWidth()))
	if strings.Contains(footer, "PIPELINE") {
		t.Fatalf("wide terminal must not double-render PIPELINE:\n%s", footer)
	}
	// The sidebar must host the single pipeline section.
	side := plainRender(t, strings.Join(m.renderContextSidebar(30, 80), "\n"))
	if !strings.Contains(side, "PIPELINE") {
		t.Fatalf("sidebar should host the PIPELINE:\n%s", side)
	}
}

// TestPipelineStripSuppressedOnCtrlB: when the sidebar is available but
// collapsed (Ctrl+B), the strip must not resurrect above the composer - it
// mirrors the eager plan-panel rule.
func TestPipelineStripSuppressedOnCtrlB(t *testing.T) {
	m := mouseTestModel()
	m.height = 40
	m.pipeline.applyPlan(agent.PipelinePlanEvent{Stages: []string{"code_writer", "test_runner"}})
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file"})
	m.sidebarHidden = true
	if !m.sidebarAvailable() {
		t.Fatal("precondition: collapsed sidebar still has the available geometry")
	}
	footer := plainRender(t, m.footerView(m.chatColumnWidth()))
	if strings.Contains(footer, "PIPELINE") {
		t.Fatalf("Ctrl+B must hide the pipeline, not resurrect the strip:\n%s", footer)
	}
}

func TestPipelineStripMouseGeometry(t *testing.T) {
	m := pipelineStripModel(t)
	width := m.chatColumnWidth()
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	if frame.composerRect.height <= 0 {
		t.Fatalf("composer rect missing when strip renders above it, frame=%#v", frame)
	}
	// The strip lives above the composer inside the footer; the composer hit
	// box must still be exact. A wheel at the strip's row must NOT hit the
	// composer box.
	stripTop := frame.footerRect.y
	if m.mouseOverComposer(testMouseWheel(tea.MouseWheelUp, 0, stripTop+1)) {
		t.Fatalf("strip row should not hit the composer box")
	}
	if !m.mouseOverComposer(testMouseWheel(tea.MouseWheelUp, 0, frame.composerRect.y)) {
		t.Fatalf("composer box should still hit exactly")
	}
}

func TestPipelineStripShortHeightCollapses(t *testing.T) {
	m := pipelineStripModel(t)
	// A very short terminal cannot host the strip + plan + chrome + a transcript
	// row. The strip must still not overflow via silent top-clip.
	m.height = 5
	footer := m.footerView(m.chatColumnWidth())
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(m.width), footer)
	if frame.footerClip > 0 && strings.Contains(footer, "PIPELINE") {
		t.Fatalf("height %d: visible strip was top-clipped (%d rows):\n%s", m.height, frame.footerClip, footer)
	}
}

func TestPipelineStripChipRendersAndTruncates(t *testing.T) {
	p := stateWith([]pipelineStageRow{{name: "code_writer", status: pipelineStageRunning}}).presentation()
	// Chip fits on a wide header.
	plain := plainRender(t, strings.Join(p.renderStripWithChip(100, 0, "wt:tui-1"), "\n"))
	if !strings.Contains(plain, "wt:tui-1") {
		t.Fatalf("strip header missing chip: %q", plain)
	}
	// A very narrow header truncates the chip rather than overflowing.
	plain = plainRender(t, strings.Join(p.renderStripWithChip(16, 0, "wt-verylong-worktree-name"), "\n"))
	headerLine := strings.Split(plain, "\n")[0]
	if lipgloss.Width(headerLine) > 16 {
		t.Fatalf("chip header overflows width 16: %q", headerLine)
	}
	// No chip (default) renders the bare label.
	plain = plainRender(t, strings.Join(p.renderStrip(100, 0), "\n"))
	if strings.Contains(plain, "wt-") {
		t.Fatalf("chip must be absent by default: %q", plain)
	}
}
