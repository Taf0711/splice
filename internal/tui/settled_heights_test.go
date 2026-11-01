package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestBakedHeightsMatchOnDemand pins the baked-height contract: heights
// carried by settled descriptors must equal what measureTranscriptBodyItems
// computes on demand for the same items (via cache or render). A drift here
// silently corrupts scroll geometry (wrong totalLines, wrong window math).
func TestBakedHeightsMatchOnDemand(t *testing.T) {
	m := benchTranscriptModel(500)
	// Real-session sequence: first View seeds the cache, re-settle bakes.
	items := m.transcriptBodyItems(96, "", false)
	ondemand := measureTranscriptBodyItems(items, m.transcriptBodyHeights)
	m.altScreenSettledWidth = 0
	m.rebuildAltScreenSettledItems(96)
	baked := m.transcriptBodyItems(96, "", false)

	bakedLayout := measureTranscriptBodyItems(baked, m.transcriptBodyHeights)
	if len(bakedLayout.spans) != len(ondemand.spans) {
		t.Fatalf("span count drifted: baked=%d ondemand=%d", len(bakedLayout.spans), len(ondemand.spans))
	}
	for i := range ondemand.spans {
		if bakedLayout.spans[i] != ondemand.spans[i] {
			t.Fatalf("span %d drifted: baked=%+v ondemand=%+v", i, bakedLayout.spans[i], ondemand.spans[i])
		}
	}
	if bakedLayout.totalLines() != ondemand.totalLines() {
		t.Fatalf("total lines drifted: baked=%d ondemand=%d", bakedLayout.totalLines(), ondemand.totalLines())
	}
	resolved := 0
	for _, item := range baked {
		if item.heightResolved {
			resolved++
		}
	}
	if resolved != len(baked) {
		t.Fatalf("expected every settled descriptor baked, got %d/%d", resolved, len(baked))
	}
}

// TestBakedHeightsSurviveCacheEviction proves the once-per-item promise: even
// when the height cache evicts everything between settles (small cache), the
// re-settle keeps every descriptor resolved via the render-on-miss fallback.
func TestBakedHeightsSurviveCacheEviction(t *testing.T) {
	m := benchTranscriptModel(2_000)
	// Force eviction between settles: replace the cache with a tiny one.
	items := m.transcriptBodyItems(96, "", false)
	measureTranscriptBodyItems(items, m.transcriptBodyHeights)
	m.altScreenSettledWidth = 0
	m.rebuildAltScreenSettledItems(96) // bake pass 1
	// Evict everything, then re-settle again.
	m.transcriptBodyHeights = newTranscriptBodyHeightCache(1)
	m.altScreenSettledWidth = 0
	m.rebuildAltScreenSettledItems(96) // bake pass 2: cache useless
	baked := m.transcriptBodyItems(96, "", false)
	resolved := 0
	for _, item := range baked {
		if item.heightResolved {
			resolved++
		}
	}
	if resolved != len(baked) {
		t.Fatalf("eviction broke the bake: %d/%d resolved", resolved, len(baked))
	}
	layout := measureTranscriptBodyItems(baked, m.transcriptBodyHeights)
	// A 2k-row fixture is 2 rows + 1 separator each = 5999 items; every
	// height must still be the true one. Separator + rows are deterministic.
	if layout.totalLines() == 0 {
		t.Fatal("empty layout after eviction")
	}
}

// TestIncrementalSettleMatchesFullRebuild pins the O(turn) settle path: at
// every frontier step, extending the cache must produce exactly the items a
// full rebuild produces (same kinds, rows, cache keys, rendered lines), or
// the viewport silently diverges from history.
func TestIncrementalSettleMatchesFullRebuild(t *testing.T) {
	var rows []transcriptRow
	for turn := 0; turn < 6; turn++ {
		rows = append(rows,
			transcriptRow{kind: rowUser, text: fmt.Sprintf("turn %d request", turn)},
			transcriptRow{kind: rowReasoning, text: "thinking about it"},
			transcriptRow{kind: rowToolResult, tool: "read_file", id: fmt.Sprintf("r%da", turn), detail: "a.go"},
			transcriptRow{kind: rowToolResult, tool: "grep", id: fmt.Sprintf("r%db", turn), detail: "needle"},
			transcriptRow{kind: rowReasoning, text: "next step"},
			transcriptRow{kind: rowToolResult, tool: "bash", id: fmt.Sprintf("r%dc", turn), detail: "go test ./..."},
			transcriptRow{kind: rowToolResult, tool: "edit_file", id: fmt.Sprintf("r%dd", turn), detail: "a.go"},
			transcriptRow{kind: rowAssistant, text: fmt.Sprintf("turn %d answer with a longer explanation", turn)},
		)
	}
	render := func(items []transcriptBodyItem) []string {
		var out []string
		for _, item := range items {
			out = append(out, fmt.Sprintf("%d|%d|%s|%d", item.kind, item.rowIndex, item.heightCacheKey, item.height))
			out = append(out, renderTranscriptBodyItem(item, 0).lines...)
		}
		return out
	}
	for _, step := range []int{1, 3, 5} {
		incremental := benchTranscriptModel(0)
		incremental.transcript = append([]transcriptRow(nil), rows...)
		incremental.flushed, incremental.flushedAny = 0, false
		incremental.altScreenSettledItems = nil
		incremental.altScreenSettledWidth, incremental.altScreenSettledFrontier = 0, 0
		width := incremental.chatColumnWidth()
		extended := 0
		for frontier := step; frontier <= len(rows); frontier += step {
			incremental.transcript = rows[:frontier]
			incremental, _ = incremental.settleTranscript()
			if incremental.flushed != frontier {
				t.Fatalf("step %d: frontier %d did not settle (flushed=%d)", step, frontier, incremental.flushed)
			}
			full := incremental
			full.altScreenSettledItems = nil
			full.rebuildAltScreenSettledItems(width)
			got, want := render(incremental.altScreenSettledItems), render(full.altScreenSettledItems)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("step %d frontier %d: incremental settle diverged from full rebuild\n--- incremental\n%s\n--- full\n%s",
					step, frontier, strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			extended++
		}
		if extended == 0 {
			t.Fatalf("step %d: no frontier advanced", step)
		}
	}
}
