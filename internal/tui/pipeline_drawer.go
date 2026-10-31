package tui

// pipeline_drawer.go is the node drawer of the Ctrl+G pane: the arrow keys
// select a graph node, Enter opens its drawer, Esc closes it. The
// drawer shows the node's resolved configuration through the same
// resolution `splice config describe` uses (splicerun.DescribeTopology), and
// the run facts of the node from the graph view model.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// pipelineDrawerState is the resolved configuration of the drawer's node.
// It is computed when the drawer opens (file I/O), never in render. The
// shared splicerun.NodeDrawer is the same drawer the web viewer shows.
type pipelineDrawerState = splicerun.NodeDrawer

// detailSelectedIndex returns the index of the selected node. Selection is
// by name, so a graph that changes under the pane keeps the same node; a
// node that is gone selects the first row.
func (m model) detailSelectedIndex(graph presentrun.Graph) int {
	for i, node := range graph.Nodes {
		if node.Name == m.detailView.selectedName {
			return i
		}
	}
	return 0
}

// refreshPipelineDrawer resolves the open drawer again when the graph under
// it changed: a run started, the tier changed, or the node list changed. It
// runs from Update, never from render.
func (m *model) refreshPipelineDrawer() {
	if !m.detailView.active || !m.detailView.drawerOpen {
		return
	}
	graph, note := m.detailGraph()
	if note != "" || len(graph.Nodes) == 0 {
		return
	}
	node := graph.Nodes[m.detailSelectedIndex(graph)]
	if !m.detailView.drawer.Matches(graph, node) {
		m.detailView.selectedName = node.Name
		m.detailView.drawer = m.resolvePipelineDrawer(graph, node)
	}
}

// detailGraphKey reports whether a key drives the pane's graph selection.
// The caller has checked that the pane is open, no modal is up, and the
// composer is empty, so typing is never captured.
func (m model) detailGraphKey(msg tea.KeyMsg) bool {
	graph, note := m.detailGraph()
	if note != "" || len(graph.Nodes) == 0 {
		return false
	}
	switch {
	// Only the arrows select. Letters always type, so a message that starts
	// with j or k is never taken as navigation.
	case keyIs(msg, tea.KeyUp), keyIs(msg, tea.KeyDown):
		return true
	case keyIs(msg, tea.KeyEnter):
		// Enter opens the drawer, or resolves an open drawer again.
		return true
	}
	return false
}

// handleDetailGraphKey moves the selection or opens the drawer. Moving the
// selection while the drawer is open retargets the drawer.
func (m model) handleDetailGraphKey(msg tea.KeyMsg) model {
	graph, _ := m.detailGraph()
	last := len(graph.Nodes) - 1
	selected := m.detailSelectedIndex(graph)
	switch {
	case keyIs(msg, tea.KeyDown):
		selected = min(selected+1, last)
	case keyIs(msg, tea.KeyUp):
		selected = max(selected-1, 0)
	case keyIs(msg, tea.KeyEnter):
		m.detailView.drawerOpen = true
	}
	node := graph.Nodes[selected]
	m.detailView.selectedName = node.Name
	if m.detailView.drawerOpen {
		m.detailView.drawer = m.resolvePipelineDrawer(graph, node)
	}
	return m.ensureDetailFocusVisible()
}

// closePipelineDrawer is the Esc action while the drawer is open.
func (m model) closePipelineDrawer() model {
	m.detailView.drawerOpen = false
	m.detailView.drawer = pipelineDrawerState{}
	return m
}

// drawerTopology returns the topology the graph came from.
func (m model) drawerTopology(graph presentrun.Graph) *schemas.PipelineTopology {
	if graph.Live {
		return m.pipeline.graphTopology
	}
	return m.detailView.staticTopology
}

// resolvePipelineDrawer resolves the node's configuration at the graph's
// tier (the run tier, or the describe default when no run exists).
func (m model) resolvePipelineDrawer(graph presentrun.Graph, node presentrun.GraphNode) pipelineDrawerState {
	return splicerun.ResolveNodeDrawer(graph, node, m.drawerTopology(graph), stageModelConfigPath(m.userConfigPath))
}

// renderPipelineDrawer renders the open drawer for the selected node.
func (m model) renderPipelineDrawer(graph presentrun.Graph, width int) []string {
	if !m.detailView.drawerOpen || len(graph.Nodes) == 0 {
		return nil
	}
	node := graph.Nodes[m.detailSelectedIndex(graph)]
	drawer := m.detailView.drawer
	if !drawer.Matches(graph, node) {
		// Never mix one node's run facts with another node's or another
		// tier's configuration. Update resolves it again on the next event.
		drawer = pipelineDrawerState{Tier: string(splicerun.DrawerTier(graph)), Problem: "out of date; press Enter to refresh"}
	}
	var lines []string
	lines = append(lines, zeroTheme.accent.Bold(true).Render(truncateDisplayWidth("NODE "+node.Name, width)))
	for _, fact := range splicerun.NodeDrawerFields(graph, node, drawer) {
		prefix := fmt.Sprintf("  %-9s ", fact.Key)
		for i, wrapped := range wrapPlainText(pipelineCardSafe(fact.Value), max(8, width-len(prefix))) {
			if i > 0 {
				prefix = strings.Repeat(" ", len(prefix))
			}
			lines = append(lines, zeroTheme.faint.Render(prefix)+zeroTheme.ink.Render(wrapped))
		}
	}
	lines = append(lines, zeroTheme.faint.Render("  esc closes the details · \u2191/\u2193 selects another node"))
	return lines
}
