package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// changedDrawerModel is a live run whose writer completed its latest pass with
// six changed files, and whose static_analyzer completed with none.
func changedDrawerModel(t *testing.T) model {
	t.Helper()
	topology := graphDiamondTopology()
	state := graphRunState(t, topology, schemas.TierStandard, "lint")
	events := []presentation.StreamEventLike{
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Iteration: 1, Status: "completed", Detail: "wrote six files", Progress: 100,
			ChangedFiles: []string{"a.go", "b.go", "c.go", "d.go", "e.go", "naïve.go"}}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "running"}),
		presentrun.AdaptStageEvent(agent.StageEvent{Name: "static_analyzer", Status: "completed", Detail: "no findings", Progress: 100}),
	}
	var err error
	for _, event := range events {
		if state, err = presentation.Apply(state, event); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newModel(context.Background(), Options{})
	m.cwd = t.TempDir()
	m.glyphTier = presentation.GlyphTierASCII
	m.userConfigPath = filepath.Join(t.TempDir(), "config.json")
	m.pipeline.applyState(state)
	m.observeRunTopology(state)
	m.pipeline.graphTopology, m.pipeline.graphTopologyNote = topology, ""
	m.lastState = state
	return m
}

// TestPipelineDrawerChangedRow pins the Changed row: up to five paths and a
// count of the rest for a completed writer, "none" for a completed node with
// no changes, and no row for a static view.
func TestPipelineDrawerChangedRow(t *testing.T) {
	m := pressAll(t, changedDrawerModel(t), testKeyCtrl('g'), testKey(tea.KeyEnter))
	plain := ansiPattern.ReplaceAllString(m.renderDetailView(120), "")
	if !strings.Contains(plain, "changed   a.go, b.go, c.go, d.go, e.go, +1 more") {
		t.Fatalf("writer drawer lacks the changed row:\n%s", plain)
	}

	m = pressAll(t, m, testKey(tea.KeyEsc), testKey(tea.KeyDown), testKey(tea.KeyDown), testKey(tea.KeyEnter))
	plain = ansiPattern.ReplaceAllString(m.renderDetailView(120), "")
	if !strings.Contains(plain, "NODE static_analyzer") || !strings.Contains(plain, "changed   none") {
		t.Fatalf("completed node without changes must read none:\n%s", plain)
	}

	static := pressAll(t, newModel(context.Background(), Options{}), testKeyCtrl('g'), testKey(tea.KeyEnter))
	if view := ansiPattern.ReplaceAllString(static.renderDetailView(120), ""); strings.Contains(view, "changed") {
		t.Fatalf("a static view must not show a changed row:\n%s", view)
	}
}

func TestPipelineDrawerChangedGolden(t *testing.T) {
	for _, width := range []int{60, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := pressAll(t, changedDrawerModel(t), testKeyCtrl('g'), testKey(tea.KeyEnter))
			checkTextGolden(t, fmt.Sprintf("pipeline_drawer_changed_w%d", width), m.renderDetailView(width))
		})
	}
}
