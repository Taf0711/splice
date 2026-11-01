// keybinding_help.go renders the `?` keyboard-shortcut overlay. Splice has a
// rich set of chord bindings (Ctrl+T effort, Ctrl+P plan, drill-in subchat,
// Shift+Tab permission mode, …) that are otherwise invisible — only learnable
// by reading the source. A single-key `?` overlay (opened on an empty composer)
// lists them grouped, so the keymap is discoverable the way the reference TUIs
// do it. The list is declarative and hand-curated to match the real handlers in
// model.go's Update switch; keep them in sync when a binding changes.
//
// Configurable bindings (toggleDetailed, toggleMouse, cycleReasoning,
// togglePlan, toggleSidebar) pull their key label from m.keyBindings so a user
// who remaps them in config.json sees the actual chords, not the defaults.
package tui

import "strings"

// keybinding is one row in the help overlay: the key chord and what it does.
type keybinding struct {
	keys string
	desc string
}

// keybindingGroup is a titled cluster of related bindings.
type keybindingGroup struct {
	title    string
	bindings []keybinding
}

// buildKeybindingGroups returns the full, grouped shortcut list shown by the
// `?` overlay. Configurable bindings use the user's remapped labels (via
// m.keyBindings) when present; unconfigured bindings get their built-in labels.
// Sourced from the real key cases in model.go (Update) — not invented. When a
// binding is added/changed there, update this method too.
func (m model) buildKeybindingGroups() []keybindingGroup {
	// Dual-purpose chords say what they do right now, not in general.
	effortKey := "reasoning effort (not supported by this model)"
	if ring := m.reasoningEffortRing(); len(ring) > 0 {
		levels := make([]string, 0, len(ring))
		for _, effort := range ring {
			levels = append(levels, string(effort))
		}
		effortKey = "cycle reasoning effort (auto \u2192 " + strings.Join(levels, " \u2192 ") + ")"
	}
	if m.pipeline.active {
		effortKey = "show / hide the trajectory (during a run; effort otherwise)"
	}
	sidebarKey := "hide / show the right context sidebar"
	if m.compactWidth() {
		sidebarKey = "open the context sheet (the sidebar needs 120 columns)"
	}
	// In the Ctrl+G pane with a graph, the arrows select a node instead of
	// scrolling, so the row says what they do there.
	graph, note := m.detailGraph()
	paneGraph := m.detailView.active && note == "" && len(graph.Nodes) > 0
	arrows := "scroll, or move within a popup / multi-line input"
	if paneGraph {
		arrows = "select a pipeline node (empty input); PgUp / PgDn scroll the pane"
	}
	navigation := []keybinding{
		{"PgUp / PgDn", "scroll the transcript by a page"},
		{"\u2191 / \u2193", arrows},
		{labelOr(m.keyBindings.toggleDetailed, "Ctrl+O"), "toggle the detailed (full-screen) transcript"},
		{labelOr(m.keyBindings.toggleSidebar, "Ctrl+B"), sidebarKey},
		{"Ctrl+G", "open the run evidence pane (Esc closes)"},
	}
	// The graph keys work only inside the Ctrl+G pane, so they show only there.
	if paneGraph {
		navigation = append(navigation, keybinding{"Enter / Esc", "open / close the node details"})
	}
	navigation = append(navigation,
		keybinding{labelOr(m.keyBindings.toggleNarration, "Ctrl+N"), "cycle transcript verbosity (quiet / normal / detailed)"},
		keybinding{labelOr(m.keyBindings.toggleMouse, "Ctrl+E"), "release the mouse to drag-select & copy text"},
		keybinding{"Tab", "accept the autocomplete / picker selection"},
	)
	return []keybindingGroup{
		{
			title: "Chat",
			bindings: []keybinding{
				{"Enter", "send the message"},
				{"Shift+Enter / Alt+Enter", "insert a newline (multi-line compose)"},
				{"Esc (\u00d72)", "cancel the run / dismiss a popup / clear the input"},
				{"Ctrl+C", "cancel the run, then quit"},
				{"?", "show this help (on an empty input)"},
			},
		},
		{
			title: "Model & run controls",
			bindings: []keybinding{
				{labelOr(m.keyBindings.cycleReasoning, "Ctrl+T"), effortKey},
				{"Shift+Tab", "cycle permission mode (auto \u2194 ask)"},
				{labelOr(m.keyBindings.togglePlan, "Ctrl+P"), "expand / collapse the plan panel"},
			},
		},
		{
			title:    "Navigation & scrollback",
			bindings: navigation,
		},
		{
			title: "Mouse & pickers",
			bindings: []keybinding{
				{"Click a specialist card", "drill into its sub-session"},
				{"\u2191 / Esc (in a sub-session)", "return to the main chat"},
				{"Ctrl+F (in /model)", "toggle the highlighted model as a favorite"},
				{"Click a tool card", "expand / collapse its output"},
				{"Right-click", "paste the clipboard"},
			},
		},
	}
}

// keybindingHelpFooter is the dismiss hint shown at the bottom of the overlay.
const keybindingHelpFooter = "? or Esc to close \u00b7 /help for slash commands"

// renderKeybindingHelpLines builds the overlay body lines (group titles +
// aligned key/description rows + footer), wrapped to the inner width. Exposed
// separately from the framed renderer so tests can assert on content without
// the border chrome.
func (m model) renderKeybindingHelpLines(innerWidth int) []string {
	groups := m.buildKeybindingGroups()
	keyColumn := keybindingKeyColumnWidth(groups)
	lines := make([]string, 0, 64)
	for index, group := range groups {
		if index > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, zeroTheme.accent.Render(group.title))
		for _, binding := range group.bindings {
			lines = append(lines, formatKeybindingLines(binding, keyColumn, innerWidth)...)
		}
	}
	lines = append(lines, "")
	lines = append(lines, zeroTheme.faint.Render(keybindingHelpFooter))
	return lines
}

// keybindingKeyColumnWidth returns the width of the key column: the widest key
// chord across all groups, so the descriptions align in a clean second column.
func keybindingKeyColumnWidth(groups []keybindingGroup) int {
	widest := 0
	for _, group := range groups {
		for _, binding := range group.bindings {
			if n := len([]rune(binding.keys)); n > widest {
				widest = n
			}
		}
	}
	return widest
}

// formatKeybindingLines renders one "  <keys>  <desc>" row: the chord in
// ink, padded to keyColumn, then the muted description. A description that
// does not fit wraps under its own column rather than truncating, so no
// shortcut's meaning is cut off.
func formatKeybindingLines(binding keybinding, keyColumn int, innerWidth int) []string {
	keys := binding.keys
	pad := keyColumn - len([]rune(keys))
	if pad < 0 {
		pad = 0
	}
	keyCell := zeroTheme.ink.Render(keys) + strings.Repeat(" ", pad)
	// Indent(2) + keyCell + gap(2) consumed before the description.
	descBudget := innerWidth - 2 - keyColumn - 2
	if descBudget < 12 {
		descBudget = 12
	}
	wrapped := wrapPlainText(binding.desc, descBudget)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	lines := []string{"  " + keyCell + "  " + zeroTheme.muted.Render(wrapped[0])}
	indent := strings.Repeat(" ", 2+keyColumn+2)
	for _, part := range wrapped[1:] {
		lines = append(lines, indent+zeroTheme.muted.Render(part))
	}
	return lines
}

// renderKeybindingHelpOverlay frames the `?` overlay, sized to the given
// terminal width. Vertical centering is handled by the caller's overlay
// compositing pipeline (overlayViewportLines in transcriptView).
func (m model) renderKeybindingHelpOverlay(width int) string {
	overlayWidth := keybindingHelpOverlayWidth(width)
	lines := m.renderKeybindingHelpLines(overlayWidth - 4)
	block := styledBlockFillTitle(overlayWidth, "Keyboard Shortcuts", lines, zeroTheme.line, zeroTheme.panel)
	return centerRenderedBlock(block, width)
}

// keybindingHelpOverlayWidth picks the overlay width: wide enough that the
// descriptions don't truncate next to the key column, capped, and never wider
// than the terminal.
func keybindingHelpOverlayWidth(terminalWidth int) int {
	width := 96
	if terminalWidth-4 < width {
		width = terminalWidth - 4
	}
	if width < 24 {
		width = 24
	}
	return width
}
