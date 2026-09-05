// detail_view.go is the Ctrl+G detail/evidence pane (GAP-G rest, §17, frame
// aWaAU): a drill-in that swaps the chat column's body to the current run's
// evidence projection — the same proven mechanism the file drill-in and diff
// review use (swap at the single source every consumer reads). Content is
// runtime truth only, re-projected from presentation.State on every render:
//
//	evidence groups   — label, status marker, pass/fail/incomplete counts,
//	                    findings, duration (§ evidence contract)
//	interventions     — rollback/continue with target node + status
//	receipt           — completion status + staged/applied accounting
//
// Projection only: the pane decides nothing, mutates nothing. Ctrl+O
// toggles; Esc closes and restores the chat scroll position.
package tui

import (
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/presentation"
)

const detailViewMaxFindings = 200

// detailViewState manages the detail pane. Mirrors fileViewState: parent
// scroll preserved so closing returns to the same spot.
type detailViewState struct {
	active             bool
	parentScrollOffset int
}

// openDetailView activates the pane. Opening when already open is a no-op
// (the toggle must not bounce scroll position).
func (m model) openDetailView() model {
	if m.detailView.active {
		return m
	}
	m.detailView = detailViewState{active: true, parentScrollOffset: m.chatScrollOffset}
	m.chatScrollOffset = 0
	m = m.clearHover()
	return m
}

// exitDetailView deactivates the pane and restores the chat scroll position.
func (m model) exitDetailView() model {
	if !m.detailView.active {
		return m
	}
	m.chatScrollOffset = m.detailView.parentScrollOffset
	m.detailView = detailViewState{}
	m = m.clearHover()
	return m
}

// toggleDetailView is the Ctrl+O action.
func (m model) toggleDetailView() model {
	if m.detailView.active {
		return m.exitDetailView()
	}
	return m.openDetailView()
}

// detailViewBodyItems builds the body items while active — one pre-rendered
// block, so scrolling and height accounting flow the same path as chat rows.
func (m model) detailViewBodyItems(width int) []transcriptBodyItem {
	return []transcriptBodyItem{transcriptBlockBodyItem(transcriptBodyItemRow, -1, m.renderDetailView(width))}
}

// detailViewNavBar is the one-line nav bar while active (the same swap
// fileViewNavBar / diffViewNavBar use, routed through pinnedTitleBar).
func (m model) detailViewNavBar(width int) string {
	left := zeroTheme.accent.Render("DETAIL — run evidence")
	n := len(m.lastState.Evidence)
	right := zeroTheme.faint.Render(fmt.Sprintf("%d evidence groups · ctrl+g / esc back", n))
	return fitStyledLine(joinHeaderLine(left, right, width), width)
}

// renderDetailView projects the evidence projection at the given width.
// Every section renders from m.lastState; an empty state renders an honest
// empty notice, never invented rows.
func (m model) renderDetailView(width int) string {
	if width <= 0 {
		return ""
	}
	state := m.lastState
	lines := []string{}

	lines = append(lines, m.renderDetailEvidence(state, width)...)
	lines = append(lines, m.renderDetailInterventions(state, width)...)
	lines = append(lines, m.renderDetailReceipt(state, width)...)

	if len(lines) == 0 {
		lines = []string{
			zeroTheme.faint.Render("No run evidence yet. Evidence appears here as stages report QA groups, interventions, and the completion receipt."),
		}
	}
	return styledBlock(width, lines, zeroTheme.cardRun)
}

// renderDetailEvidence renders the QA evidence groups: status marker, counts,
// findings, duration. Findings are capped so a runaway suite cannot freeze
// the pane; the cap notice names what was dropped.
func (m model) renderDetailEvidence(state presentation.State, width int) []string {
	if len(state.Evidence) == 0 {
		return nil
	}
	lines := []string{zeroTheme.amber.Bold(true).Render("EVIDENCE")}
	for _, group := range state.Evidence {
		glyph := presentation.StatusMarker(evidenceStatusToNode(group.Status), presentation.GlyphTierASCII).Glyph
		head := fmt.Sprintf("  %s %s", glyph, group.Label)
		counts := fmt.Sprintf("%d pass · %d fail · %d inc · %.1fs", group.Passed, group.Failed, group.Incomplete, group.Duration)
		lines = append(lines, head+"  "+zeroTheme.faint.Render(counts))
		shown := group.Findings
		if len(shown) > detailViewMaxFindings {
			shown = shown[:detailViewMaxFindings]
			lines = append(lines, "      "+zeroTheme.faint.Render(fmt.Sprintf("… %d more findings not shown", len(group.Findings)-len(shown))))
		}
		for _, finding := range shown {
			for _, wrapped := range wrapPlainText(finding, width-10) {
				lines = append(lines, "      "+zeroTheme.muted.Render(wrapped))
			}
		}
	}
	lines = append(lines, "")
	return lines
}

// renderDetailInterventions renders the intervention list: kind, target
// node, status, reason — the repair story the roster only summarizes.
func (m model) renderDetailInterventions(state presentation.State, width int) []string {
	if len(state.Interventions) == 0 {
		return nil
	}
	lines := []string{zeroTheme.amber.Bold(true).Render("INTERVENTIONS")}
	for _, iv := range state.Interventions {
		marker := presentation.StatusMarker(presentation.NodeStatusDegraded, presentation.GlyphTierASCII).Glyph
		if iv.Status == presentation.InterventionApplied {
			marker = presentation.StatusMarker(presentation.NodeStatusComplete, presentation.GlyphTierASCII).Glyph
		}
		head := fmt.Sprintf("  %s %s → %s", marker, iv.Kind, iv.TargetNodeID)
		if iv.Status == presentation.InterventionProposed {
			head += zeroTheme.faint.Render("  (proposed)")
		}
		lines = append(lines, head)
		for _, wrapped := range wrapPlainText(iv.Reason, width-10) {
			lines = append(lines, "      "+zeroTheme.muted.Render(wrapped))
		}
	}
	lines = append(lines, "")
	return lines
}

// renderDetailReceipt renders the completion receipt when present: status,
// detail, staged-vs-applied accounting (cancelled keeps staged distinct —
// cancelled is not failed, and proposed work is not applied work).
func (m model) renderDetailReceipt(state presentation.State, width int) []string {
	if state.Completion == nil {
		return nil
	}
	c := state.Completion
	lines := []string{zeroTheme.amber.Bold(true).Render("RECEIPT")}
	head := "  " + string(c.Status)
	lines = append(lines, head)
	if strings.TrimSpace(c.Detail) != "" {
		for _, wrapped := range wrapPlainText(c.Detail, width-6) {
			lines = append(lines, "      "+zeroTheme.muted.Render(wrapped))
		}
	}
	if c.Staged > 0 || c.Applied > 0 {
		lines = append(lines, zeroTheme.faint.Render(fmt.Sprintf("  staged %d · applied %d", c.Staged, c.Applied)))
	}
	return lines
}

// evidenceStatusToNode maps an evidence status onto the closest node status
// so the shared ASCII marker table stays the single glyph source.
func evidenceStatusToNode(status presentation.EvidenceStatus) presentation.NodeStatus {
	switch status {
	case presentation.EvidencePassed:
		return presentation.NodeStatusComplete
	case presentation.EvidenceFailed:
		return presentation.NodeStatusFailed
	case presentation.EvidenceIncomplete:
		return presentation.NodeStatusDegraded
	default:
		return presentation.NodeStatusPending
	}
}
