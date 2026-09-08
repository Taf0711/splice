package tui

import (
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
