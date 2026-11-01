package presentation

import (
	"strings"
	"testing"
)

func TestGlyphTierValidation(t *testing.T) {
	for _, tier := range []GlyphTier{GlyphTierASCII, GlyphTierSafeUnicode, GlyphTierRichUnicode} {
		if err := tier.Validate(); err != nil {
			t.Fatalf("tier %q rejected: %v", tier, err)
		}
	}
	if err := GlyphTier("bogus").Validate(); err == nil {
		t.Fatal("bogus tier accepted")
	}
	if DefaultGlyphTier != GlyphTierASCII {
		t.Fatalf("default tier = %q, want ascii (DoD 24)", DefaultGlyphTier)
	}
}

// TestASCIIMarkersAreExactlyThreeCells pins the P3 width contract: every
// default-tier status marker is exactly 3 cells, so alignment never needs
// terminal probing.
func TestASCIIMarkersAreExactlyThreeCells(t *testing.T) {
	for _, status := range []NodeStatus{NodeStatusRunning, NodeStatusComplete, NodeStatusFailed, NodeStatusDegraded, NodeStatusPending} {
		m := StatusMarker(status, GlyphTierASCII)
		if w := len([]rune(m.Glyph)); w != 3 {
			t.Fatalf("ascii marker %q for %s is %d cells, want 3", m.Glyph, status, w)
		}
		if m.Word == "" {
			t.Fatalf("ascii marker for %s missing required word (no state may rely on glyph alone)", status)
		}
	}
	blocked := BlockedMarker(GlyphTierASCII)
	if blocked.Glyph != "[?]" || blocked.Word != "NEEDS YOU" {
		t.Fatalf("blocked marker = %+v", blocked)
	}
}

// TestMarkerTiersKeepRequiredWords pins the second-channel rule: the word
// survives tier changes, only the glyph changes.
func TestMarkerTiersKeepRequiredWords(t *testing.T) {
	for _, status := range []NodeStatus{NodeStatusRunning, NodeStatusComplete, NodeStatusFailed, NodeStatusDegraded, NodeStatusPending} {
		ascii := StatusMarker(status, GlyphTierASCII)
		for _, tier := range []GlyphTier{GlyphTierSafeUnicode, GlyphTierRichUnicode} {
			rich := StatusMarker(status, tier)
			if rich.Word != ascii.Word {
				t.Fatalf("%s: tier %s word %q != ascii word %q", status, tier, rich.Word, ascii.Word)
			}
		}
	}
}

// TestSafeUnicodeTierExcludesAmbiguousGlyphs pins the P3 audit rule: the
// safe tier allows only check/cross; ▲ ○ ◆ █ never appear below the rich
// tier.
func TestSafeUnicodeTierExcludesAmbiguousGlyphs(t *testing.T) {
	for _, status := range []NodeStatus{NodeStatusRunning, NodeStatusComplete, NodeStatusFailed, NodeStatusDegraded, NodeStatusPending} {
		m := StatusMarker(status, GlyphTierSafeUnicode)
		for _, banned := range []rune{'▲', '○', '◆', '◉', '█'} {
			if strings.ContainsRune(m.Glyph, banned) {
				t.Fatalf("safe-unicode marker %q for %s contains ambiguous %q", m.Glyph, status, banned)
			}
		}
	}
}

// TestProgressBarWidthExact pins the ASCII progress contract: the bar is
// exactly width+2 cells for any fraction, ends are brackets, cells are
// '#' and '-' only (P3: "[########--------] 54%").
func TestProgressBarWidthExact(t *testing.T) {
	cases := []struct {
		fraction float64
		wantFill int
	}{
		{0, 0},
		{0.54, 9}, // 0.54*16 = 8.64 -> rounds to 9
		{0.5, 8},  // 0.5*16 = 8
		{1, 16},   // full
		{-0.5, 0}, // clamped
		{1.5, 16}, // clamped
	}
	for _, tc := range cases {
		bar := ProgressBar(tc.fraction, 16)
		if got := len([]rune(bar)); got != 18 {
			t.Fatalf("bar for %.2f is %d cells, want 18 (16+2 brackets): %q", tc.fraction, got, bar)
		}
		if bar[0] != '[' || bar[17] != ']' {
			t.Fatalf("bar %q missing brackets", bar)
		}
		inner := bar[1:17]
		filled := strings.Count(inner, "#")
		if filled != tc.wantFill {
			t.Fatalf("bar for %.2f has %d filled cells, want %d: %q", tc.fraction, filled, tc.wantFill, bar)
		}
		if strings.ContainsAny(inner, "█░▏▎▍▌▋▊▉") {
			t.Fatalf("bar %q contains ambiguous block glyphs; ASCII tier is the default", bar)
		}
	}
	if got := ProgressBar(0.5, 0); got != "" {
		t.Fatalf("zero-width bar = %q, want empty", got)
	}
}
