package presentation

import "testing"

func getenv(mapVars map[string]string) func(string) string {
	return func(key string) string { return mapVars[key] }
}

// The tier matrix: SPLICE_GLYPH_TIER overrides, UTF-8 locale upgrades to
// the Pen rich set, NO_COLOR always forces ASCII (markers are the only
// state channel when color is off).
func TestSelectGlyphTier(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want GlyphTier
	}{
		{"utf-8 locale upgrades", map[string]string{"LANG": "en_US.UTF-8"}, GlyphTierRichUnicode},
		{"utf8 no-dash upgrades", map[string]string{"LANG": "C.utf8"}, GlyphTierRichUnicode},
		{"LC_ALL wins when LANG empty", map[string]string{"LC_ALL": "en_GB.UTF-8"}, GlyphTierRichUnicode},
		{"C locale stays ascii", map[string]string{"LANG": "C"}, GlyphTierASCII},
		{"no locale stays ascii", map[string]string{}, GlyphTierASCII},
		{"NO_COLOR forces ascii over utf-8", map[string]string{"LANG": "en_US.UTF-8", "NO_COLOR": "1"}, GlyphTierASCII},
		{"override ascii", map[string]string{"LANG": "en_US.UTF-8", "SPLICE_GLYPH_TIER": "ascii"}, GlyphTierASCII},
		{"override safe", map[string]string{"SPLICE_GLYPH_TIER": "safe"}, GlyphTierSafeUnicode},
		{"override rich", map[string]string{"SPLICE_GLYPH_TIER": "rich"}, GlyphTierRichUnicode},
		{"override rich beats NO_COLOR", map[string]string{"NO_COLOR": "1", "SPLICE_GLYPH_TIER": "rich"}, GlyphTierASCII},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelectGlyphTier(getenv(tc.env)); got != tc.want {
				t.Fatalf("SelectGlyphTier(%v) = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}

// The rich tier IS the Pen contract set (contract 9.1 / turn-4 table):
// ◉ running, ✓ complete, ✗ failed, ▲ degraded, ○ pending, ◆ blocked.
func TestRichMarkerSetMatchesPen(t *testing.T) {
	want := map[NodeStatus]string{
		NodeStatusRunning:  "◉",
		NodeStatusComplete: "✓",
		NodeStatusFailed:   "✗",
		NodeStatusDegraded: "▲",
		NodeStatusPending:  "○",
	}
	for status, glyph := range want {
		got := []rune(StatusMarker(status, GlyphTierRichUnicode).Glyph)
		if len(got) == 0 || string(got[0]) != glyph {
			t.Fatalf("rich marker for %s = %q, want leading %q", status, string(got), glyph)
		}
	}
	if bm := BlockedMarker(GlyphTierRichUnicode); !func() bool { r := []rune(bm.Glyph); return len(r) > 0 && string(r[0]) == "◆" }() || bm.Word != "NEEDS YOU" {
		t.Fatalf("rich blocked marker = %+v, want ◆ NEEDS YOU", bm)
	}
}

// RichProgressBar: eighth-block runs, single-cell exact at every fraction
// (the mock's "▊▊▊▊▋  54%" language).
func TestRichProgressBarWidthExact(t *testing.T) {
	for _, pct := range []int{0, 1, 12, 34, 54, 78, 99, 100} {
		for _, w := range []int{4, 8, 16, 24} {
			bar := RichProgressBar(float64(pct)/100, w)
			if len([]rune(bar)) != w {
				t.Fatalf("RichProgressBar(%d%%, %d) = %q (%d cells), want %d", pct, w, bar, len([]rune(bar)), w)
			}
		}
	}
	if got := RichProgressBar(0.5, 4); got != "██  " {
		t.Fatalf("RichProgressBar(0.5, 4) = %q, want ██  ", got)
	}
}
