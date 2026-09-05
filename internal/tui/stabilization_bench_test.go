package tui

// stabilization_bench_test.go (F6, stabilization §13): benchmarks for the
// render paths the directive names, at the representative loads it lists.
// Baselines live here so a regression in long-session behavior shows up as
// a diff, not a user report.

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/worktrees"
)

// benchTranscriptModel builds a settled alt-screen model with n rows.
func benchTranscriptModel(n int) model {
	m := limeTestModel()
	m.width, m.height = 96, 40
	m.altScreen = true
	for i := 0; i < n; i++ {
		m.transcript = append(m.transcript,
			transcriptRow{kind: rowUser, text: fmt.Sprintf("question %d about the service behavior", i)},
			transcriptRow{kind: rowAssistant, text: fmt.Sprintf("answer %d with a fairly long explanation of what changed and why it matters for the rest of the system design", i)},
		)
	}
	m.flushed = len(m.transcript)
	m.flushedAny = true
	m.transcriptBodyHeights = newTranscriptBodyHeightCache(100_000)
	m.rebuildAltScreenSettledItems(96)
	return m
}

// benchView measures one full View(), the per-frame unit the directive's
// latency targets talk about (View returns tea.View; renderContent
// flattens it the way plainRender does for tests).
func benchView(b *testing.B, m model) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		out := renderContent(m.View())
		if len(out) == 0 {
			b.Fatal("empty view")
		}
	}
}

// BenchmarkViewTranscriptScaling measures a full View() over settled
// transcripts (100/1,000/10,000 rows). A long session must not gradually
// make the UI unusable: per-frame cost should stay flat as history grows
// (the settled-items cache bounds the work to the visible window).
func BenchmarkViewTranscriptScaling(b *testing.B) {
	for _, rows := range []int{100, 1_000, 10_000} {
		m := benchTranscriptModel(rows)
		b.Run(fmt.Sprintf("rows_%d", rows), func(b *testing.B) {
			benchView(b, m)
		})
	}
}

// BenchmarkViewSettledTranscriptScaling measures the steady-state frame: a
// model that has already rendered once (cache seeded) and re-settled (heights
// baked into the settled descriptors, as settleTranscript does after any
// frontier move). This is the number a long session actually pays per frame;
// BenchmarkViewTranscriptScaling above is the one-time cold-build cost.
func BenchmarkViewSettledTranscriptScaling(b *testing.B) {
	for _, rows := range []int{100, 1_000, 10_000} {
		m := benchTranscriptModel(rows)
		// Seed heights + bake them, exactly as a real session does: first
		// View renders every item into the height cache, the re-settle
		// snapshot bakes them into the descriptors.
		items := m.transcriptBodyItems(96, "", false)
		measureTranscriptBodyItems(items, m.transcriptBodyHeights)
		m.altScreenSettledWidth = 0
		m.rebuildAltScreenSettledItems(96)
		b.Run(fmt.Sprintf("rows_%d", rows), func(b *testing.B) {
			benchView(b, m)
		})
	}
}

// benchNodeState builds a valid presentation.State with n execution nodes
// in a mix of statuses (the executing-cockpit shape).
func benchNodeState(n int) presentation.State {
	st := presentation.State{SchemaVersion: presentation.PresentationSchemaVersionV1, Lifecycle: presentation.LifecycleExecute}
	statuses := []presentation.NodeStatus{presentation.NodeStatusRunning, presentation.NodeStatusComplete, presentation.NodeStatusPending, presentation.NodeStatusFailed}
	kinds := []presentation.NodeKind{presentation.NodeKindWrite, presentation.NodeKindAnalyze, presentation.NodeKindVerify}
	for i := 0; i < n; i++ {
		st.Nodes = append(st.Nodes, presentation.ExecutionNode{
			ID:        fmt.Sprintf("stage_%02d", i),
			Label:     fmt.Sprintf("stage %02d", i),
			Kind:      kinds[i%len(kinds)],
			Status:    statuses[i%len(statuses)],
			Progress:  float64(i%101) / 100,
			Workspace: "isolated",
		})
	}
	return st
}

// BenchmarkPipelineNodeRenderScaling measures the pipeline sidebar section
// render as the execution graph grows (10/50/100 nodes).
func BenchmarkPipelineNodeRenderScaling(b *testing.B) {
	for _, n := range []int{10, 50, 100} {
		st := benchNodeState(n)
		m := limeTestModel()
		m.width, m.height = 130, 40
		m.pipeline.applyState(st)
		panel := m.pipeline
		b.Run(fmt.Sprintf("nodes_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out := panel.renderSection(32, 0)
				if len(out) == 0 {
					b.Fatal("empty section")
				}
			}
		})
	}
}

// benchDiffText builds a unified diff with n files and ~25 changed lines
// each (the diff-review viewport's representative load).
func benchDiffText(files int) string {
	body := ""
	for i := 0; i < files; i++ {
		body += fmt.Sprintf("diff --git a/pkg/file%02d.go b/pkg/file%02d.go\n", i, i)
		body += "index 111..222 100644\n"
		body += fmt.Sprintf("@@ -1,20 +1,%d @@ func Handler%d\n", 20+files%10, i)
		for line := 0; line < 25; line++ {
			if line%4 == 0 {
				body += fmt.Sprintf("+context line %d in file %d with a realistic amount of code text\n", line, i)
			} else {
				body += fmt.Sprintf(" unchanged line %d in file %d\n", line, i)
			}
		}
	}
	return body
}

// BenchmarkDiffRenderScaling measures the diff review render (parse + stat
// rows + hunk window) as the diff grows (small/medium/very large).
func BenchmarkDiffRenderScaling(b *testing.B) {
	for _, tc := range []struct {
		name  string
		files int
	}{{"small_3", 3}, {"medium_20", 20}, {"large_100", 100}} {
		text := benchDiffText(tc.files)
		m := limeTestModel()
		m.diffView = diffViewState{
			active: true,
			wt:     worktrees.Result{Name: "wt-bench", Path: "/tmp/wt-bench"},
			base:   "main",
			text:   text,
			files:  diffFileStats(text),
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out := m.renderDiffReview(120)
				if len(out) == 0 {
					b.Fatal("empty render")
				}
			}
		})
	}
}

// BenchmarkDiffCaptureParse isolates the deterministic parser so a future
// regression in diffFileStats (e.g. accidental O(n²)) shows separately from
// the render.
func BenchmarkDiffCaptureParse(b *testing.B) {
	text := benchDiffText(100)
	b.ReportAllocs()
	for b.Loop() {
		files := diffFileStats(text)
		if len(files) == 0 {
			b.Fatal("no files parsed")
		}
	}
}

// BenchmarkKeypressToRenderedResponse measures the keypress → rendered
// response path the directive calls out (p95 ≤ 16ms target): Update with a
// navigation keypress + a full View, on a busy mid-run model with 1,000
// settled transcript rows and a live pipeline.
func BenchmarkKeypressToRenderedResponse(b *testing.B) {
	m := benchTranscriptModel(1_000)
	m.pending = true
	m.pipeline.applyState(benchNodeState(10))
	key := tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	b.ReportAllocs()
	for b.Loop() {
		updated, _ := m.Update(key)
		next := updated.(model)
		out := renderContent(next.View())
		if len(out) == 0 {
			b.Fatal("empty view")
		}
	}
}
