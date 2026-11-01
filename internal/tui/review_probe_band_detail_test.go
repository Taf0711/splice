package tui

// review_probe_band_detail_test.go (final review, 2026-09-02): the evidence
// band guard predates the Ctrl+G detail pane. When the pane owns the body,
// the band must not also render into the frame — two surfaces claiming the
// same region is the exact "here and nowhere else" violation the evidence
// band contract (owner Tension-3 decision) forbids. Probed through the real
// visible-frame path.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/presentation"
)

func reviewBandModel(t *testing.T) model {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.width = 140
	m.altScreen = true
	m.height = 40
	// P12 made the launch screen two-column; this probe targets the band's
	// own visibility rule, so pin the sidebar collapsed (the Ctrl+B state).
	m.sidebarHidden = true
	// A streaming run with pipeline state: the band's trigger conditions.
	m.pending = true
	m.pipeline.applyState(presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
		Nodes: []presentation.ExecutionNode{
			{ID: "code_writer", Kind: presentation.NodeKindWrite, Status: presentation.NodeStatusRunning, Progress: 0.4},
		},
	})
	return m
}

func TestReviewBandHiddenWhenDetailPaneOwnsBody(t *testing.T) {
	m := reviewBandModel(t)
	// Sanity: with the run streaming and no drill-in, the band is visible.
	if !m.evidenceBandVisible() {
		t.Fatal("sanity: band should be visible during a streaming run")
	}
	// Open the detail pane: the pane owns the body, the band must hide.
	m = m.openDetailView()
	if m.evidenceBandVisible() {
		t.Fatal("evidence band renders while the detail pane owns the body")
	}
	// The rendered View must not contain the band's rule + content alongside
	// the pane block (the two surfaces both drawing = the violation).
	plain := plainRender(t, m.View())
	if strings.Contains(plain, "PIPELINE") && strings.Contains(plain, "DETAIL — run evidence") {
		// Both labels coexisting is ambiguous; the decisive check is the band
		// guard, but assert the pane's own pipeline module is the only one.
		if strings.Count(plain, "TRAJECTORY") > 1 {
			t.Fatal("evidence band and detail pane both rendered")
		}
	}
	_ = tea.KeyEnter // keep the tea import stable if assertions change
}
