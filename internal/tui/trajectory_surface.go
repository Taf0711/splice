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

// renderTrajectorySurface renders the trajectory pane. P1.4 delta (frame
// aPZTh, S4): the pass history renders as the frame's inline score trail —
// `61 ▔▔▔▔ 67 ▔▔ 72` — with restore markers summarized beneath, instead of
// one line per pass. ASCII tier: the trail glyph is ▔ (fold-table covered);
// scores render as integers 0-100.
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

	if trail := renderTrajectoryTrail(state.Trajectory, bodyBudget); trail != "" {
		lines = append(lines, "  "+zeroTheme.ink.Render(trail))
	} else {
		lines = append(lines, "  "+zeroTheme.faint.Render("no passes scored yet"))
	}
	for _, restore := range state.Trajectory.RestoreMarkers {
		lines = append(lines, "  "+zeroTheme.muted.Render(restoreLabel(restore)))
	}
	return styledBlock(width, lines, zeroTheme.cardRun)
}

// renderTrajectoryTrail composes the inline score trail: each pass renders
// as `<score> ▔*n` where n scales the score to the segment width, joined by
// spaces — the frame's `61 ▔▔▔▔ 67 ▔▔ 72`. Empty when no pass has scored.
func renderTrajectoryTrail(t presentation.Trajectory, budget int) string {
	if len(t.PassScores) == 0 {
		return ""
	}
	segment := 4 // bars per pass at full score; scales down below
	if len(t.PassScores) > 4 {
		segment = 2 // long histories compress so the trail stays one line
	}
	var parts []string
	used := 0
	for i, score := range t.PassScores {
		text := fmt.Sprintf("%.0f", score*100)
		bars := int(score*float64(segment) + 0.5)
		part := text + " " + strings.Repeat("▔", maxInt(1, bars))
		if i > 0 && used+1+len(part) > budget {
			break
		}
		if i > 0 {
			used++
		}
		used += len([]rune(part))
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// restoreLabel renders one restore marker line.
func restoreLabel(marker string) string {
	return "[~] restore  " + marker
}
