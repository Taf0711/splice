package tui

// pipeline_graph.go renders the pipeline graph view model
// (presentrun.BuildGraph) for the terminal: the /pipeline card when idle, and
// the sidebar PIPELINE section and the Ctrl+G pane during a run. The view
// model decides nothing here; this file only lays it out by width.

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/redaction"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// pipelineGraphLayeredMinWidth is the narrowest content width for the
// layered view (rows grouped under depth headers, dependencies on the row).
// Below it the graph is a flat ordered list with dependencies on their own
// line: the 60-column terminal, the sidebar, and the compact pane.
const pipelineGraphLayeredMinWidth = 64

// skippedMarker marks a node the run tier skipped. It is 3 cells, like the
// status markers, and the "skipped at" words carry the meaning.
const skippedMarker = "[-]"

// renderPipelineGraph lays the graph out at width. selected is the index of
// the selected node, or -1 when nothing is selectable.
func renderPipelineGraph(graph presentrun.Graph, width int, tier presentation.GlyphTier, selected int) []string {
	lines, _, _ := renderPipelineGraphFocus(graph, width, tier, selected)
	return lines
}

// renderPipelineGraphFocus is renderPipelineGraph plus the line range of the
// selected node, so a scrolling host can keep it in view. The range is -1,-1
// when nothing is selected.
func renderPipelineGraphFocus(graph presentrun.Graph, width int, tier presentation.GlyphTier, selected int) ([]string, int, int) {
	focusStart, focusEnd := -1, -1
	if width <= 0 || len(graph.Nodes) == 0 {
		return nil, focusStart, focusEnd
	}
	layered := width >= pipelineGraphLayeredMinWidth
	nameWidth := 0
	for _, node := range graph.Nodes {
		nameWidth = max(nameWidth, lipgloss.Width(node.Name))
	}
	lines := make([]string, 0, len(graph.Nodes)*2)
	for _, note := range graph.Notes {
		for _, wrapped := range wrapPlainText(note, width) {
			lines = append(lines, zeroTheme.amber.Render(wrapped))
		}
	}
	depth := -1
	for i, node := range graph.Nodes {
		if layered && node.Depth != depth {
			depth = node.Depth
			lines = append(lines, zeroTheme.faint.Render(fmt.Sprintf("depth %d", depth)))
		}
		rows := renderPipelineGraphNode(graph, node, i == selected, layered, nameWidth, width, tier)
		if i == selected {
			focusStart, focusEnd = len(lines), len(lines)+len(rows)-1
		}
		lines = append(lines, rows...)
	}
	return lines, focusStart, focusEnd
}

// renderPipelineGraphNode renders one node: cursor, status marker, name,
// metadata, and dependencies. The layered view keeps the dependencies on the
// row when they fit; otherwise, and always in the flat list, they move to an
// indented line of their own, so they are never cut off.
func renderPipelineGraphNode(graph presentrun.Graph, node presentrun.GraphNode, selected, layered bool, nameWidth, width int, tier presentation.GlyphTier) []string {
	skipped := graph.Live && (len(node.SkippedAt) > 0 || node.Removed != "" || node.NotRun || node.Interrupted)
	nameStyle, metaStyle := zeroTheme.ink, zeroTheme.faint
	if skipped {
		nameStyle = zeroTheme.faint
	}
	cursor := " "
	if selected {
		cursor = zeroTheme.accent.Render(">")
		nameStyle = nameStyle.Bold(true)
	}
	head := cursor
	if layered {
		head += " "
	}
	if marker, style, ok := pipelineGraphMarker(graph.Live, skipped, node.Status, tier); ok {
		head += style.Render(marker) + " "
	}
	indent := strings.Repeat(" ", lipgloss.Width(head))
	name := node.Name
	if layered {
		name += strings.Repeat(" ", max(0, nameWidth-lipgloss.Width(name)))
	}
	metaParts := pipelineGraphMeta(graph, node, tier)
	meta := strings.Join(metaParts, "  ")
	deps := ""
	if len(node.Dependencies) > 0 {
		deps = "<- " + strings.Join(node.Dependencies, ", ")
	}

	first := head + nameStyle.Render(name)
	if meta != "" {
		first += "  " + metaStyle.Render(meta)
	}
	if layered && deps != "" && lipgloss.Width(first)+2+lipgloss.Width(deps) <= width {
		return []string{first + "  " + zeroTheme.muted.Render(deps)}
	}
	// A row too wide for the width keeps the name on the first line and
	// wraps the facts below it, so a "skipped at" or a cost is never cut.
	lines := []string{fitStyledLine(first, width)}
	if meta != "" && lipgloss.Width(first) > width {
		lines = []string{fitStyledLine(head+nameStyle.Render(name), width)}
		for _, wrapped := range packPipelineFacts(metaParts, max(8, width-lipgloss.Width(indent)-2)) {
			lines = append(lines, indent+"  "+metaStyle.Render(wrapped))
		}
	}
	if deps != "" {
		for _, wrapped := range wrapPlainText(deps, max(8, width-lipgloss.Width(indent)-2)) {
			lines = append(lines, indent+"  "+zeroTheme.muted.Render(wrapped))
		}
	}
	return lines
}

// packPipelineFacts puts whole facts on lines of at most width cells, two
// spaces apart, so a fact such as "$0.0123 partial" never breaks inside. A
// fact wider than the line goes through wrapPlainText alone.
func packPipelineFacts(parts []string, width int) []string {
	var lines []string
	line := ""
	for _, part := range parts {
		switch {
		case line == "":
			line = part
		case lipgloss.Width(line)+2+lipgloss.Width(part) <= width:
			line += "  " + part
		default:
			lines = append(lines, line)
			line = part
		}
		if lipgloss.Width(line) > width {
			lines = append(lines, wrapPlainText(line, width)...)
			line = ""
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// pipelineGraphMarker returns the 3-cell marker for a node. The static view
// has no run, so it has no marker. A live node that has not started is
// pending: the run will reach it.
func pipelineGraphMarker(live, skipped bool, status presentation.NodeStatus, tier presentation.GlyphTier) (string, lipgloss.Style, bool) {
	if !live {
		return "", lipgloss.Style{}, false
	}
	if skipped {
		return skippedMarker, zeroTheme.faint, true
	}
	if status == "" {
		status = presentation.NodeStatusPending
	}
	style := zeroTheme.faint
	switch status {
	case presentation.NodeStatusComplete:
		style = zeroTheme.green
	case presentation.NodeStatusRunning, presentation.NodeStatusDegraded:
		style = zeroTheme.amber
	case presentation.NodeStatusFailed:
		style = zeroTheme.red
	}
	return presentation.StatusMarker(status, tier).Glyph, style, true
}

// pipelineGraphMeta lists the facts a row shows after the name, in reading
// order. Each part renders only when it carries information, so a quiet
// node stays a short row.
func pipelineGraphMeta(graph presentrun.Graph, node presentrun.GraphNode, tier presentation.GlyphTier) []string {
	var parts []string
	if node.Type != "" && node.Type != node.Name {
		parts = append(parts, node.Type)
	}
	switch {
	case graph.Live && (node.Status == presentation.NodeStatusFailed || node.Status == presentation.NodeStatusDegraded || node.Status == presentation.NodeStatusSkipped):
		// The marker is a symbol; an alert or a skip also gets its word.
		parts = append(parts, presentation.StatusMarker(node.Status, tier).Word)
	case graph.Live && node.NotRun:
		parts = append(parts, "not run")
	case graph.Live && node.Interrupted:
		parts = append(parts, "stopped")
	}
	if node.Iteration > 0 {
		parts = append(parts, fmt.Sprintf("pass %d", node.Iteration+1))
	}
	if node.Tokens > 0 {
		parts = append(parts, humanCount(int(node.Tokens))+" tok")
	}
	if cost := pipelineGraphCost(node); cost != "" {
		parts = append(parts, cost)
	}
	if len(node.SkippedAt) > 0 {
		parts = append(parts, "skipped at "+strings.Join(node.SkippedAt, ", "))
	}
	if node.Removed != "" {
		parts = append(parts, "removed: "+node.Removed)
	}
	return parts
}

// pipelineGraphCost renders a node's cost through the shared formatter.
func pipelineGraphCost(node presentrun.GraphNode) string {
	return presentrun.CostText(node)
}

// pipelineGraphCardMarker tags a transcript row whose payload is the /pipeline
// graph card. The row stores data, and the transcript renders it at the live
// width, like the receipt and handoff cards.
const pipelineGraphCardMarker = "\x00pipeline-graph\x00"

// pipelineGraphCard is the data of the idle /pipeline card.
type pipelineGraphCard struct {
	Graph    presentrun.Graph `json:"graph"`
	Source   string           `json:"source"`
	Warnings []string         `json:"warnings,omitempty"`
	Commands []string         `json:"commands,omitempty"`
	Hints    []string         `json:"hints,omitempty"`
}

func pipelineGraphCardPayload(card pipelineGraphCard) (string, error) {
	data, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return pipelineGraphCardMarker + string(data), nil
}

func parsePipelineGraphCard(text string) (pipelineGraphCard, bool) {
	payload, ok := strings.CutPrefix(text, pipelineGraphCardMarker)
	if !ok {
		return pipelineGraphCard{}, false
	}
	var card pipelineGraphCard
	if err := json.Unmarshal([]byte(payload), &card); err != nil {
		return pipelineGraphCard{}, false
	}
	return card, true
}

// pipelineCardSafe makes untrusted text (command lines, warnings) safe for
// the transcript: terminal control sequences go, and secrets are redacted.
func pipelineCardSafe(text string) string {
	return redaction.RedactString(sanitizeTerminalOutput(text, false), redaction.Options{})
}

// renderPipelineGraphCardLines builds the card body at the inner width.
func renderPipelineGraphCardLines(card pipelineGraphCard, inner int, tier presentation.GlyphTier) []string {
	section := func(title string) string { return zeroTheme.accent.Bold(true).Render(title) }
	lines := []string{zeroTheme.ink.Render(card.Graph.Topology) + zeroTheme.faint.Render("  source: "+card.Source)}
	if len(card.Warnings) > 0 {
		lines = append(lines, "", section("Warnings"))
		for _, warning := range card.Warnings {
			for _, wrapped := range wrapPlainText(pipelineCardSafe(warning), max(8, inner-2)) {
				lines = append(lines, "  "+zeroTheme.amber.Render(wrapped))
			}
		}
	}
	lines = append(lines, "", section("Graph"))
	graph := card.Graph
	for i := range graph.Nodes {
		graph.Nodes[i].Command = pipelineCardSafe(graph.Nodes[i].Command)
	}
	for _, line := range renderPipelineGraph(graph, inner-2, tier, -1) {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "", section("Command nodes"))
	if len(card.Commands) == 0 {
		lines = append(lines, "  "+zeroTheme.faint.Render("none"))
	}
	for _, command := range card.Commands {
		for _, wrapped := range wrapPlainText(pipelineCardSafe(command), max(8, inner-2)) {
			lines = append(lines, "  "+zeroTheme.ink.Render(wrapped))
		}
	}
	if len(card.Hints) > 0 {
		lines = append(lines, "")
	}
	for _, hint := range card.Hints {
		for _, wrapped := range wrapPlainText("hint: "+hint, inner) {
			lines = append(lines, zeroTheme.faint.Render(wrapped))
		}
	}
	return lines
}

// renderPipelineGraphCard renders the /pipeline card at the transcript width.
func renderPipelineGraphCard(card pipelineGraphCard, width int, tier presentation.GlyphTier) string {
	return styledBlockFillTitle(width, "Pipeline", renderPipelineGraphCardLines(card, width-4, tier), zeroTheme.accent, lipgloss.NewStyle())
}

// buildPipelineGraphCard resolves the active topology into the idle card. It
// does file I/O, so it runs from the command handler, never from render.
func buildPipelineGraphCard(cwd string, trusted bool) (pipelineGraphCard, error) {
	topology, source, loadWarnings, err := splicerun.ResolveTopology(splicerun.TopologySourcesFor(cwd, trusted))
	if err != nil {
		return pipelineGraphCard{}, err
	}
	return pipelineGraphCardFor(topology, source, loadWarnings)
}

// pipelineGraphCardFor builds the idle card from a resolved topology. It is
// pure, so the goldens pin it without files.
func pipelineGraphCardFor(topology *schemas.PipelineTopology, source string, loadWarnings []string) (pipelineGraphCard, error) {
	graph, err := presentrun.BuildGraph(topology, presentation.State{})
	if err != nil {
		return pipelineGraphCard{}, err
	}
	compileWarnings, err := splicerun.TopologyWarnings(topology)
	if err != nil {
		return pipelineGraphCard{}, err
	}
	card := pipelineGraphCard{
		Graph:    graph,
		Source:   source,
		Warnings: append(append([]string(nil), loadWarnings...), compileWarnings...),
		// A trusted project file outranks the library entry that "use" sets,
		// so the hint must point at the file when it is the source.
		Hints: []string{"change it with /pipeline use <name>; this view is read-only", "Ctrl+G shows each node's model, budget, and last result"},
	}
	if source == splicerun.TopologySourceProject {
		card.Hints[0] = "edit .splice/pipeline.json to change it; this view is read-only"
	}
	for _, line := range splicerun.CommandNodeLines(topology) {
		card.Commands = append(card.Commands, strings.TrimSpace(line))
	}
	for _, line := range splicerun.AgentNodeLines(topology) {
		card.Commands = append(card.Commands, strings.TrimSpace(line))
	}
	return card, nil
}

// observeRunTopology resolves the topology a run names, once per name, so
// the live graph can add the nodes the run tier skipped. It runs from Update.
// A topology that does not resolve, or resolves under another name, is not
// used: the graph then shows the run plan only and says why.
func (m *model) observeRunTopology(state presentation.State) {
	m.resolveRunTopology(state)
	// The graph under an open drawer may have changed with this state.
	m.refreshPipelineDrawer()
}

func (m *model) resolveRunTopology(state presentation.State) {
	name := state.Plan.Topology
	if name == "" || name == m.pipeline.graphTopologyName {
		return
	}
	m.pipeline.graphTopologyName = name
	m.pipeline.graphTopology, m.pipeline.graphTopologyNote = splicerun.ResolveRunGraphTopology(splicerun.TopologySourcesFor(m.cwd, m.trusted), name)
}

// livePipelineGraph builds the graph of the current run from the latest
// presentation state. ok is false when no run plan exists. A build error is
// returned, never hidden behind an empty graph.
func (m model) livePipelineGraph() (presentrun.Graph, bool, error) {
	if len(m.lastState.Plan.Stages) == 0 && len(m.lastState.Nodes) == 0 {
		return presentrun.Graph{}, false, nil
	}
	graph, err := presentrun.BuildGraph(m.pipeline.graphTopology, m.lastState)
	if err != nil {
		return presentrun.Graph{}, true, err
	}
	if m.pipeline.graphTopologyNote != "" {
		graph.Notes = append([]string{m.pipeline.graphTopologyNote}, graph.Notes...)
	}
	return graph, true, nil
}

// sidebarPipelineBody renders the sidebar PIPELINE section body: the live
// graph when the run plan is known, else the flat stage roster, then the
// progress bar and the current stage.
func (m model) sidebarPipelineBody(pipeline pipelinePresentation, width int) []string {
	graph, ok, err := m.livePipelineGraph()
	if !ok || err != nil || len(graph.Nodes) == 0 {
		// A graph build error falls back to the roster; the Ctrl+G pane names
		// the error.
		return pipeline.renderSection(width, m.spinnerPhase)
	}
	return append(renderPipelineGraph(graph, width, m.glyphTier, -1), pipeline.renderSectionTail(width)...)
}
