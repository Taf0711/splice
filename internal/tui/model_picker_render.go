package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Rendering for the /model picker's inline controls. The layout is a stack:
// search line, scroll affordance, model rows (label · badge · effort ring ·
// effort label), scroll affordance, a contextual toggle line, the price readout,
// the badge legend, and a keybind hint bar that names only the keys the
// highlighted row actually responds to.

const (
	// effortSegmentFilled and effortSegmentEmpty draw the ring. They are distinct
	// glyphs, not one glyph in two brightnesses: brightness alone disappears under
	// NO_COLOR and in low-contrast terminals, which would leave the ring encoding
	// nothing at all. Shape carries the level; color reinforces it.
	effortSegmentFilled = "■"
	effortSegmentEmpty  = "□"
	// modelPickerEffortColumn is the x where every row's effort ring starts. A
	// shared column is what makes the differing ring lengths comparable down the
	// list — ragged rings following variable-length labels would encode nothing.
	// Labels longer than the column push their ring right rather than truncate.
	modelPickerEffortColumn = 30
	// costRailWidth is the price rail's length in cells. Wide enough to separate
	// models a few-fold apart on the log scale, short enough to leave the three
	// rate columns beneath it on a narrow overlay.
	costRailWidth = 34
)

// renderEffortRing draws "← ■■□□ → high" for the highlighted row and a dimmed,
// arrow-less "■■□□ high" for the rest. Only the highlighted row shows arrows
// because only it responds to ←/→; showing them on every row would advertise a
// control that does nothing where the cursor is not.
//
// Returns "" for models with no effort ring, leaving those rows as a bare label.
func renderEffortRing(item pickerItem, selected bool, surface func(lipgloss.Style) lipgloss.Style) string {
	if len(item.Efforts) == 0 {
		return ""
	}
	filled := item.EffortIndex + 1 // effortAuto (-1) fills nothing
	segments := make([]string, 0, len(item.Efforts))
	for index := range item.Efforts {
		if index < filled {
			style := zeroTheme.muted
			if selected {
				style = zeroTheme.accent
			}
			segments = append(segments, surface(style).Render(effortSegmentFilled))
			continue
		}
		segments = append(segments, surface(zeroTheme.faintest).Render(effortSegmentEmpty))
	}
	ring := strings.Join(segments, "")
	label := item.effortLabel()
	if !selected {
		// Reserve the arrow gutter even without arrows, so an unselected row's ring
		// sits at the same x as the selected row's. Otherwise the whole column
		// shifts by two cells as the cursor moves, and the ring lengths — the thing
		// the column exists to make comparable — stop lining up.
		gutter := surface(zeroTheme.faintest).Render(strings.Repeat(" ", lipgloss.Width("← ")))
		return gutter + ring + surface(zeroTheme.faint).Render(strings.Repeat(" ", lipgloss.Width(" → "))+label)
	}
	arrow := surface(zeroTheme.faint)
	left, right := arrow.Render("← "), arrow.Render(" → ")
	if item.EffortIndex <= effortAuto {
		// At an end of the ring the arrow is inert; dim it rather than remove it, so
		// the row's columns stay aligned as the cursor moves down the list.
		left = surface(zeroTheme.faintest).Render("← ")
	}
	if item.EffortIndex >= len(item.Efforts)-1 {
		right = surface(zeroTheme.faintest).Render(" → ")
	}
	return left + ring + right + surface(zeroTheme.ink).Render(label)
}

// renderPickerBadge returns the row's status asterisk, colored to match its
// legend entry. Empty for unbadged rows.
func renderPickerBadge(badge pickerBadge, surface func(lipgloss.Style) lipgloss.Style) string {
	switch badge {
	case badgeBeta:
		return surface(zeroTheme.blue).Render("*")
	case badgeDeprecated:
		return surface(zeroTheme.amber).Render("*")
	}
	return ""
}

// renderBadgeLegend decodes the row asterisks. The entry matching the
// highlighted row is lit and the others stay faint, so the legend answers "what
// is the mark on THIS row" at a glance instead of being a static key the eye has
// to scan. Returns "" when no row in view carries a badge.
func renderBadgeLegend(visible []pickerItem, active pickerBadge) string {
	present := map[pickerBadge]bool{}
	for _, item := range visible {
		if item.Badge != badgeNone {
			present[item.Badge] = true
		}
	}
	if len(present) == 0 {
		return ""
	}
	parts := []string{}
	for _, badge := range []pickerBadge{badgeBeta, badgeDeprecated} {
		if !present[badge] {
			continue
		}
		mark := renderPickerBadge(badge, transparentSurface)
		style := zeroTheme.faint
		if badge == active {
			style = zeroTheme.ink
		}
		parts = append(parts, mark+" "+style.Render(badge.label()))
	}
	return strings.Join(parts, "   ")
}

// renderCostRail draws the shared price axis with two knobs: a hollow one for
// the model currently in use and a filled one for the model under the cursor.
// Seeing both on one axis is the point — it turns "is this cheaper than what I'm
// on?" into a glance rather than mental arithmetic on two sets of rates.
//
// The rail runs green (cheap) through amber to red (expensive), so position and
// color agree and the comparison survives a monochrome terminal via position
// alone.
func renderCostRail(width int, hoverPos, activePos float64, hasActive bool) string {
	width = maxInt(8, width)
	hoverCell := clampInt(int(hoverPos*float64(width-1)+0.5), 0, width-1)
	activeCell := clampInt(int(activePos*float64(width-1)+0.5), 0, width-1)
	cells := make([]string, width)
	for index := 0; index < width; index++ {
		style := zeroTheme.green
		switch fraction := float64(index) / float64(width-1); {
		case fraction > 0.66:
			style = zeroTheme.red
		case fraction > 0.33:
			style = zeroTheme.amber
		}
		cells[index] = style.Render("─")
	}
	if hasActive && activeCell != hoverCell {
		cells[activeCell] = zeroTheme.faint.Render("○")
	}
	cells[hoverCell] = zeroTheme.ink.Bold(true).Render("●")
	return strings.Join(cells, "")
}

// renderPriceColumns prints the three rates a user compares. A model whose rates
// are all zero collapses to "Free" — three "$0 / 1M" columns would be noise.
func renderPriceColumns(cost pickerCost) string {
	if cost.free() {
		return zeroTheme.green.Render("Free")
	}
	column := func(label string, rate float64) string {
		return zeroTheme.faint.Render(label+"  ") + zeroTheme.ink.Render(formatRatePerMillion(rate))
	}
	return column("Input", cost.InputPerMillion) + "   " +
		column("Cached", cost.CachedInputPerMillion) + "   " +
		column("Output", cost.OutputPerMillion)
}

// formatRatePerMillion prints a per-million rate without trailing-zero noise:
// $12 / 1M rather than $12.00 / 1M, but $0.26 / 1M keeps the cents that matter
// at the cheap end.
func formatRatePerMillion(rate float64) string {
	switch {
	case rate == 0:
		return "$0 / 1M"
	case rate < 0.01:
		return fmt.Sprintf("$%.3f / 1M", rate)
	case rate == float64(int(rate)):
		return fmt.Sprintf("$%d / 1M", int(rate))
	case rate*10 == float64(int(rate*10)):
		return fmt.Sprintf("$%.1f / 1M", rate)
	}
	return fmt.Sprintf("$%.2f / 1M", rate)
}

// renderContextToggle is the "Long context  Off  tab" line shown only for rows
// that advertise long context. It sits above the price readout because enabling
// it changes which pricing tier applies on several providers.
//
// The key is named on this line rather than in the hint bar at the bottom: the
// binding is row-conditional, so it belongs next to the state it flips, where it
// is visible exactly when it works. That also keeps it off the hint bar, which
// has to shed entries to fit the overlay width.
func renderContextToggle(item pickerItem) string {
	if !item.LongContext {
		return ""
	}
	state := zeroTheme.faint.Render("Off")
	if item.LongContextOn {
		state = zeroTheme.accent.Render("On")
	}
	return zeroTheme.muted.Render("Long context  ") + state + zeroTheme.faintest.Render("   tab")
}

// modelPickerHintBar names only the keys the highlighted row responds to, so the
// bar is an accurate contract rather than a fixed list with dead entries. ←/→
// appears only on rows with an effort ring; Tab only on long-context rows.
//
// width caps the bar. The full list overflows the overlay's 76-cell ceiling on a
// long-context reasoning row, and a truncated bar is worse than a shorter one —
// it cuts the tail, which is where Esc lives. Instead the bar sheds hints from
// the least essential end until it fits, so whatever renders is complete.
func modelPickerHintBar(item pickerItem, hasItem bool, width int) string {
	// Ordered least- to most-essential. Favorite goes first: it is a convenience
	// on a surface whose job is choosing a model. The long-context key is absent
	// by design — renderContextToggle names it on the toggle line itself.
	optional := []string{"ctrl+f favorite"}
	if hasItem && len(item.Efforts) > 0 {
		// Effort is the reason this picker changed shape, so it is dropped only
		// just before the core keys.
		optional = append(optional, "←→ effort")
	}
	core := []string{"↑↓ select", "⏎ confirm", "esc cancel"}
	for drop := 0; drop <= len(optional); drop++ {
		parts := append([]string{"↑↓ select"}, optional[drop:]...)
		parts = append(parts, core[1:]...)
		bar := strings.Join(parts, "  ·  ")
		if width <= 0 || lipgloss.Width(bar) <= width {
			return bar
		}
	}
	return strings.Join(core, "  ·  ")
}

// modelPickerScrollHint renders the "↑ more above" / "↓ more below" affordances.
// The list is a fixed-height window over a catalog that is usually longer, and
// without these the window's edge is indistinguishable from the end of the list.
func modelPickerScrollHint(text string, show bool, innerWidth int) (string, bool) {
	if !show {
		return "", false
	}
	return fillPaletteLine(zeroTheme.faintest.Render("  "+text), innerWidth, transparentSurface), true
}

// modelPickerRowWidth measures the widest row so the overlay can size itself
// around the label + badge + ring + effort-label columns.
func modelPickerRowWidth(item pickerItem) int {
	width := lipgloss.Width("❯ ") + lipgloss.Width(item.Label)
	if item.Favorite {
		width += lipgloss.Width("* ")
	}
	if item.Badge != badgeNone {
		width += lipgloss.Width(" *")
	}
	if len(item.Efforts) > 0 {
		width += lipgloss.Width("  ← ") + len(item.Efforts) + lipgloss.Width(" → ") + lipgloss.Width(item.effortLabel())
	}
	return width
}
