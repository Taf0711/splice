package tui

// evidence_band_test.go (P3 GAP-K slice 2): the in-flow evidence band,
// probed through the real View path. Contract per the owner's Tension-3
// decision and the E2E frames: execution-only, wide-only (>= 120), absent
// when the sidebar hosts the same sections, absent when the body is swapped,
// evidence-only (never furniture), and built from the module registry.

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/worktrees"
)

// bandProbeModel: wide single-column alt-screen frame, mid-run.
func bandProbeModel() model {
	m := mouseTestModel()
	m.width = 140
	m.height = 34
	m.altScreen = true
	m.pending = true
	m.activeRunID = 9
	m.pipeline.applyState(benchNodeState(4))
	return m
}

// During a run on a wide single-column frame, the band renders the pipeline
// evidence in the live View, between rules, at full transcript width. Stage
// labels are the compact abbreviations (pipelineStageLabel: s0/cw/tr…).
func TestEvidenceBandRendersInLiveViewDuringRun(t *testing.T) {
	m := bandProbeModel()
	plain := plainRender(t, m.View())
	if !strings.Contains(plain, "PIPELINE 2/4") {
		t.Fatalf("evidence band missing the pipeline section during a run:\n%s", plain[:minInt(2200, len(plain))])
	}
	if !strings.Contains(plain, "[########") {
		t.Fatal("evidence band does not project the run's progress bar")
	}
}

// Idle frames carry no band: the rail is execution-only ("here and nowhere
// else").
func TestEvidenceBandAbsentWhenIdle(t *testing.T) {
	m := bandProbeModel()
	m.pending = false
	m.activeRunID = 0
	if m.evidenceBandVisible() {
		t.Fatal("evidence band visible on an idle frame")
	}
	plain := plainRender(t, m.View())
	if strings.Contains(plain, "stage_0") && !strings.Contains(plain, "PIPELINE ") {
		t.Fatal("idle view unexpectedly carries band content")
	}
}

// Narrow terminals fold the band back (below 120 columns).
func TestEvidenceBandFoldsBackNarrow(t *testing.T) {
	m := bandProbeModel()
	m.width = 110
	if m.evidenceBandVisible() {
		t.Fatal("evidence band visible below the 120-col boundary")
	}
	plain := plainRender(t, m.View())
	if strings.Contains(plain, "────────────") && strings.Contains(plain, "stage_0") {
		t.Fatal("band content leaked into the narrow frame")
	}
}

// When the two-column sidebar is active it already hosts the same sections;
// the band must not render both.
func TestEvidenceBandAbsentWhenSidebarActive(t *testing.T) {
	m := bandProbeModel()
	m.width = 160
	m.transcript = append(m.transcript, transcriptRow{kind: rowUser, text: "hello"})
	m.flushed = len(m.transcript)
	m.flushedAny = true
	m.rebuildAltScreenSettledItems(m.chatColumnWidth())
	if !m.sidebarActive() {
		t.Fatal("fixture: expected the two-column sidebar to be active")
	}
	if m.evidenceBandVisible() {
		t.Fatal("evidence band visible while the sidebar hosts the same sections")
	}
}

// Drill-in views (file view / diff view) swap the body wholesale; the band
// must not render into them.
func TestEvidenceBandAbsentWhenBodySwapped(t *testing.T) {
	m := bandProbeModel()
	text := benchDiffText(2)
	m.diffView = diffViewState{active: true, wt: worktrees.Result{Name: "wt-band", Path: "/tmp/wt-band", RepoRoot: "/tmp"}, base: "main", text: text, files: diffFileStats(text)}
	if m.evidenceBandVisible() {
		t.Fatal("evidence band visible while the diff view owns the body")
	}
}

// The band is evidence-only: an idle pipeline with nothing to show renders
// no band at all (no empty rules as furniture).
func TestEvidenceBandAbsentWithoutEvidence(t *testing.T) {
	m := bandProbeModel()
	m.pipeline = pipelinePanelState{} // no run state at all
	if m.hasEvidenceContent() {
		t.Fatal("fixture: no-evidence model reports content")
	}
	if m.evidenceBandVisible() {
		t.Fatal("evidence band visible with no evidence")
	}
	if got := m.evidenceBandBlock(chatWidth(m.width)); got != "" {
		t.Fatalf("evidence band rendered furniture: %q", got)
	}
}

// The band is bounded: a tall run state clips at the cap instead of pushing
// the transcript off screen.
func TestEvidenceBandBoundedHeight(t *testing.T) {
	m := bandProbeModel()
	m.pipeline.applyState(benchNodeState(60))
	height := m.evidenceBandHeight(chatWidth(m.width))
	if height == 0 {
		t.Fatal("expected a band with a 60-node run")
	}
	if height > evidenceBandMaxLines+2 { // +2 rules
		t.Fatalf("band height %d exceeds the cap %d", height, evidenceBandMaxLines+2)
	}
	lines := m.renderEvidenceBand(chatWidth(m.width))
	for i, line := range lines {
		if w := lipgloss.Width(line); w != chatWidth(m.width) {
			t.Fatalf("band row %d width = %d, want %d", i, w, chatWidth(m.width))
		}
	}
}
