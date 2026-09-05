package presentation

import "strings"

// GlyphTier is the terminal-safety tier for markers and progress bars
// (v0.5 §9.1, DoD 24). Production default is ASCII: every marker is
// exactly 3 cells wide and progress cells are single-width by
// construction, so alignment never depends on terminal font coverage
// or East-Asian-Width ambiguity. Richer tiers are opt-in after
// capability detection.
type GlyphTier string

const (
	// GlyphTierASCII is the DEFAULT. Pure ASCII; widths known without
	// probing.
	GlyphTierASCII GlyphTier = "ascii"
	// GlyphTierSafeUnicode allows only known single-cell, non-ambiguous
	// glyphs (e.g. check/cross marks) after capability detection.
	GlyphTierSafeUnicode GlyphTier = "safe_unicode"
	// GlyphTierRichUnicode is the full visual design glyph set; allowed
	// only when width/font behavior is verified for the terminal.
	GlyphTierRichUnicode GlyphTier = "rich_unicode"
)

// Validate checks the closed tier set.
func (t GlyphTier) Validate() error {
	switch t {
	case GlyphTierASCII, GlyphTierSafeUnicode, GlyphTierRichUnicode:
		return nil
	}
	return ErrUnknownGlyphTier
}

// DefaultGlyphTier is the production baseline.
const DefaultGlyphTier = GlyphTierASCII

// ErrUnknownGlyphTier is returned by GlyphTier.Validate for values
// outside the closed set.
var ErrUnknownGlyphTier = glyphErr("unknown glyph tier")

type glyphErr string

func (e glyphErr) Error() string { return string(e) }

// Marker is one rendered status marker: a 3-cell glyph plus its required
// word. The word is the second state channel; color is the optional third
// (renderer concern, not carried here).
type Marker struct {
	Glyph string
	Word  string
}

// StatusMarker returns the marker for a node status in the given tier.
// Every ASCII marker is exactly 3 cells; the safe-unicode tier swaps in
// the two widely-present marks (check/cross, U+2713/U+2717) and keeps the
// rest ASCII; the rich tier is the original Pen glyph set.
func StatusMarker(status NodeStatus, tier GlyphTier) Marker {
	switch tier {
	case GlyphTierRichUnicode:
		return richMarker(status)
	case GlyphTierSafeUnicode:
		return safeMarker(status)
	default:
		return asciiMarker(status)
	}
}

// asciiMarker is the default tier. Markers are exactly 3 cells:
// running, passed, failed, degraded, pending, blocked.
func asciiMarker(status NodeStatus) Marker {
	switch status {
	case NodeStatusRunning:
		return Marker{Glyph: "[>]", Word: "running"}
	case NodeStatusComplete:
		return Marker{Glyph: "[+]", Word: "passed"}
	case NodeStatusFailed:
		return Marker{Glyph: "[!]", Word: "failed"}
	case NodeStatusDegraded:
		return Marker{Glyph: "[~]", Word: "degraded"}
	case NodeStatusPending:
		return Marker{Glyph: "[ ]", Word: "pending"}
	}
	return Marker{Glyph: "[ ]", Word: ""}
}

// safeMarker upgrades only the two marks with near-universal font
// coverage; ambiguous-width glyphs stay ASCII even here.
func safeMarker(status NodeStatus) Marker {
	switch status {
	case NodeStatusComplete:
		return Marker{Glyph: "✓  ", Word: "passed"}
	case NodeStatusFailed:
		return Marker{Glyph: "✗  ", Word: "failed"}
	}
	return asciiMarker(status)
}

// richMarker is the original Pen visual set. ▲ ○ ◆ are East-Asian-Width
// ambiguous (render 2 cells on CJK terminals) — the P3 audit demoted
// them; this tier requires verified width/font behavior.
func richMarker(status NodeStatus) Marker {
	switch status {
	case NodeStatusRunning:
		return Marker{Glyph: "◉  ", Word: "running"}
	case NodeStatusComplete:
		return Marker{Glyph: "✓  ", Word: "passed"}
	case NodeStatusFailed:
		return Marker{Glyph: "✗  ", Word: "failed"}
	case NodeStatusDegraded:
		return Marker{Glyph: "▲  ", Word: "degraded"}
	case NodeStatusPending:
		return Marker{Glyph: "○  ", Word: "pending"}
	}
	return Marker{Glyph: "◆  ", Word: ""}
}

// BlockedMarker returns the NEEDS YOU gate marker for the tier. The
// blocked state has a unique visual signature; the word is mandatory in
// every tier (color is never the only channel).
func BlockedMarker(tier GlyphTier) Marker {
	switch tier {
	case GlyphTierRichUnicode:
		return Marker{Glyph: "◆  ", Word: "NEEDS YOU"}
	default:
		return Marker{Glyph: "[?]", Word: "NEEDS YOU"}
	}
}

// ProgressBar renders a fraction [0,1] as a bracketed ASCII bar of the
// given cell width, e.g. 0.54 at width 16 -> "[########--------] 54%".
// Cells are single-width '#' and '-' by construction, so the bar is
// width-exact in every tier; the rich tier may pass styled runes at the
// renderer level, never by changing this width contract.
func ProgressBar(fraction float64, width int) string {
	if width <= 0 {
		return ""
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	var b strings.Builder
	b.WriteByte('[')
	b.WriteString(strings.Repeat("#", filled))
	b.WriteString(strings.Repeat("-", width-filled))
	b.WriteByte(']')
	return b.String()
}
