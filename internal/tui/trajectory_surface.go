package tui

import (
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/presentation"
)

// Trajectory inspection surface (GAP-G, §10, DoD 11). Hidden by default;
// toggled by Ctrl+T during a run; auto-revealed (with an explicit notice)
// when the runtime reports a regression. The renderer projects the
// snapshot's trajectory data; it never invents progress or policy.

// renderTrajectorySurface renders the trajectory pane.
func (m model) renderTrajectorySurface(state presentation.State, width int) string {
	if width <= 0 {
		return ""
	}
	if !m.trajectoryVisible {
		return " " + zeroTheme.faint.Render("trajectory hidden · ctrl+t to enable")
	}

	bodyBudget := width - 6
	if bodyBudget < 12 {
		bodyBudget = 12
	}

	header := zeroTheme.amber.Bold(true).Render("TRAJECTORY")
	if m.trajectoryAutoRevealed {
		header += "  " + zeroTheme.amber.Render("auto-revealed on regression · ctrl+t to hide")
	}
	lines := []string{header, ""}

	for i, score := range state.Trajectory.PassScores {
		if score >= 0.999 {
			glyph := presentation.StatusMarker(presentation.NodeStatusComplete, presentation.GlyphTierASCII).Glyph
			lines = append(lines, fmt.Sprintf("  pass %d  %s %.0f%%", i+1, glyph, score*100))
			continue
		}
		degradedGlyph := presentation.StatusMarker(presentation.NodeStatusDegraded, presentation.GlyphTierASCII).Glyph
		lines = append(lines, fmt.Sprintf("  [%s] pass %d  %.0f%%",
			strings.Trim(degradedGlyph, "[]"), i+1, score*100))
	}
	for _, restore := range state.Trajectory.RestoreMarkers {
		lines = append(lines, "  "+zeroTheme.muted.Render(restoreLabel(restore)))
	}
	return styledBlock(width, lines, zeroTheme.cardRun)
}

// restoreLabel renders one restore marker line.
func restoreLabel(marker string) string {
	return "[~] restore  " + marker
}
