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

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

const detailViewMaxFindings = 200

// detailViewState manages the detail pane. Mirrors fileViewState: parent
// scroll preserved so closing returns to the same spot.
type detailViewState struct {
	active             bool
	parentScrollOffset int
	// selectedName is the selected graph node (arrow keys). drawerOpen shows
	// that node's drawer (Enter opens, Esc closes).
	selectedName string
	drawerOpen   bool
	drawer       pipelineDrawerState
	// staticTopology is the active topology resolved at open when no run
	// exists, so the pane shows the static graph. staticNote names a resolve
	// failure. Render never reads files.
	staticTopology *schemas.PipelineTopology
	staticNote     string
}

// openDetailView activates the pane. Opening when already open is a no-op
// (the toggle must not bounce scroll position).
func (m model) openDetailView() model {
	if m.detailView.active {
		return m
	}
	m.detailView = detailViewState{active: true, parentScrollOffset: m.chatScrollOffset}
	if _, live, _ := m.livePipelineGraph(); !live {
		topology, _, _, err := splicerun.ResolveTopology(splicerun.TopologySourcesFor(m.cwd, m.trusted))
		if err != nil {
			m.detailView.staticNote = "the active topology did not resolve: " + err.Error()
		} else {
			m.detailView.staticTopology = topology
		}
	}
	m.chatScrollOffset = 0
	m = m.clearHover()
	// Open on the graph: the pane is bottom-pinned, so a long evidence
	// list would otherwise hide the selected node.
	return m.ensureDetailFocusVisible()
}

// exitDetailView deactivates the pane and restores the chat scroll position.
func (m model) exitDetailView() model {
	if !m.detailView.active {
		return m
	}
	m.chatScrollOffset = m.detailView.parentScrollOffset
	m.detailView = detailViewState{}
	m = m.clearHover()
	m.invalidateSettledTranscript()
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
	title := "DETAIL — run evidence"
	if m.compactWidth() {
		title = "CONTEXT — run state and evidence"
	}
	left := zeroTheme.accent.Render(title)
	n := len(m.lastState.Evidence)
	// Hints name only the keys that work now. The longest form that fits
	// wins, so the key hints are never cut mid-word.
	keys := []string{"esc back"}
	if graph, note := m.detailGraph(); note == "" && len(graph.Nodes) > 0 {
		keys = []string{"\u2191\u2193 select · enter details · esc back", "\u2191\u2193 · enter · esc"}
		if m.detailView.drawerOpen {
			keys = []string{"\u2191\u2193 select · esc closes details", "\u2191\u2193 · esc close"}
		}
	}
	var candidates []string
	if n > 0 {
		for _, k := range keys {
			candidates = append(candidates, fmt.Sprintf("%d evidence groups · %s", n, k))
		}
	}
	candidates = append(candidates, keys...)
	right := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if lipgloss.Width(title)+2+lipgloss.Width(candidate) <= width {
			right = candidate
			break
		}
	}
	return fitStyledLine(joinHeaderLine(left, zeroTheme.faint.Render(right), width), width)
}

// renderDetailView projects the evidence projection at the given width.
// Every section renders from m.lastState; an empty state renders an honest
// empty notice, never invented rows.
func (m model) renderDetailView(width int) string {
	if width <= 0 {
		return ""
	}
	lines, _, _ := m.detailViewLines(width)
	return styledBlock(width, lines, zeroTheme.cardRun)
}

// detailViewLines builds the pane body and the line range of the selected
// graph node and its open drawer (-1,-1 when none), so selection can scroll
// the focus into view.
func (m model) detailViewLines(width int) ([]string, int, int) {
	state := m.lastState
	graphLines, focusStart, focusEnd := m.renderDetailGraphFocus(width - 4)
	var evidence []string
	evidence = append(evidence, graphLines...)
	evidence = append(evidence, m.renderDetailEvidence(state, width)...)
	evidence = append(evidence, m.renderDetailInterventions(state, width)...)
	evidence = append(evidence, m.renderDetailReceipt(state, width)...)
	if len(state.Evidence) == 0 && len(state.Interventions) == 0 && state.Completion == nil {
		for _, line := range wrapPlainText("No run evidence yet. Evidence appears here as stages report QA groups, interventions, and the completion receipt.", maxInt(10, width-4)) {
			evidence = append(evidence, zeroTheme.faint.Render(line))
		}
	}
	lines := m.compactContextLines(width - 4)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	if focusStart >= 0 {
		focusStart += len(lines)
		focusEnd += len(lines)
	}
	lines = append(lines, evidence...)
	return lines, focusStart, focusEnd
}

// ensureDetailFocusVisible scrolls the pane so the selected node, and its
// drawer when open, are on screen. The pane is one bottom-pinned block, so
// without this a selection near the top can move out of view.
func (m model) ensureDetailFocusVisible() model {
	if !m.detailView.active || !m.altScreen || m.height <= 0 {
		return m
	}
	width := m.chatColumnWidth()
	_, focusStart, focusEnd := m.detailViewLines(width)
	if focusStart < 0 {
		return m
	}
	items := m.detailViewBodyItems(width)
	metrics := measureTranscriptBodyItems(items, m.transcriptBodyHeights)
	if len(metrics.spans) == 0 {
		return m
	}
	// The block draws one border line above the body.
	start := metrics.spans[0].startY + 1 + focusStart
	end := metrics.spans[0].startY + 1 + focusEnd
	// The same header and footer the frame renders with.
	frame := m.scrollableTranscriptFrame(m.normalTranscriptHeader(width), m.footerView(width))
	viewport := transcriptViewportForLayout(metrics, frame, m.chatScrollOffset)
	height := frame.bodyRect.height
	top := viewport.window().start
	switch {
	case end-start+1 >= height || start < top:
		top = start
	case end >= top+height:
		top = end - height + 1
	}
	m.chatScrollOffset = clampInt(metrics.totalLines()-height-top, 0, viewport.maxOffset())
	// The scroll pin (syncChatScroll) shifts a scrolled-up view by the body
	// growth since its baseline. The offset above already counts the new
	// body, so the baseline is the new body size.
	m.chatBodyLines = metrics.totalLines()
	if m.chatScrollOffset == 0 {
		m.chatBodyLines = 0
	}
	return m
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
		glyph := presentation.StatusMarker(evidenceStatusToNode(group.Status), m.glyphTier).Glyph
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
		marker := presentation.StatusMarker(presentation.NodeStatusDegraded, m.glyphTier).Glyph
		if iv.Status == presentation.InterventionApplied {
			marker = presentation.StatusMarker(presentation.NodeStatusComplete, m.glyphTier).Glyph
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

// compactContextLines renders the sidebar's modules for the pane when the
// terminal is too narrow for the sidebar itself (below compactModeMinWidth
// the sidebar is removed, not crushed). Wide terminals already show them.
func (m model) compactContextLines(width int) []string {
	if !m.compactWidth() || width <= 0 {
		return nil
	}
	const budget = 1 << 10 // the pane scrolls; no module needs dropping
	var lines []string
	if m.transcriptEmpty() {
		lines = m.launchSidebarModules(width, budget)
	} else {
		// The pane draws the pipeline graph itself, with selection.
		lines = m.renderContextModulesExcept(width, budget, ContextSlotPipeline)
	}
	if token := m.sidebarTokenLine(width); token != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, token)
	}
	return lines
}

// detailGraph returns the graph the pane shows: the live run graph, or the
// static graph of the active topology when no run exists. note names a
// failure; the pane shows it instead of an empty graph.
func (m model) detailGraph() (graph presentrun.Graph, note string) {
	graph, live, err := m.livePipelineGraph()
	if live {
		if err != nil {
			return presentrun.Graph{}, "the pipeline graph did not build: " + err.Error()
		}
		return graph, ""
	}
	if m.detailView.staticTopology == nil {
		return presentrun.Graph{}, m.detailView.staticNote
	}
	graph, err = presentrun.BuildGraph(m.detailView.staticTopology, presentation.State{})
	if err != nil {
		return presentrun.Graph{}, "the pipeline graph did not build: " + err.Error()
	}
	return graph, ""
}

// renderDetailGraph renders the pane's PIPELINE section: the graph with the
// selected node marked, and the selected node's drawer when it is open.
func (m model) renderDetailGraph(width int) []string {
	lines, _, _ := m.renderDetailGraphFocus(width)
	return lines
}

// renderDetailGraphFocus is renderDetailGraph plus the line range of the
// selected node and its open drawer.
func (m model) renderDetailGraphFocus(width int) ([]string, int, int) {
	graph, note := m.detailGraph()
	if note == "" && len(graph.Nodes) == 0 {
		return nil, -1, -1
	}
	title := "PIPELINE"
	if graph.Topology != "" {
		title += " " + graph.Topology
	}
	if graph.Live && graph.Tier != "" {
		title += " · " + graph.Tier
	} else if !graph.Live {
		title += " · no run"
	}
	lines := []string{zeroTheme.amber.Bold(true).Render(truncateDisplayWidth(title, width))}
	if note != "" {
		for _, wrapped := range wrapPlainText(note, width) {
			lines = append(lines, zeroTheme.red.Render(wrapped))
		}
		return append(lines, ""), -1, -1
	}
	selected := m.detailSelectedIndex(graph)
	graphLines, focusStart, focusEnd := renderPipelineGraphFocus(graph, width, m.glyphTier, selected)
	offset := len(lines)
	lines = append(lines, graphLines...)
	if drawer := m.renderPipelineDrawer(graph, width); len(drawer) > 0 {
		lines = append(lines, "")
		lines = append(lines, drawer...)
		// The drawer is part of the focus: it must be readable after Enter.
		focusEnd = len(lines) - 1 - offset
	}
	if focusStart < 0 {
		return append(lines, ""), -1, -1
	}
	return append(lines, ""), focusStart + offset, focusEnd + offset
}
