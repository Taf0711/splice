package tui

// launch_modules.go (P12, frame kAYHl): the launch-cockpit sidebar modules.
// On an empty transcript the sidebar hosts the launch readout — NEXT, the
// DECISIONS/AGENTS/TOOLS/CONTEXT summaries, and the contract band — all
// projected from in-tree data, never invented:
//
//	NEXT
//	  resume, or describe
//	  a change
//	DECISIONS
//	  3 settled
//	  1 open
//	AGENTS
//	  none running
//	TOOLS
//	  46 registered
//	  1 mcp degraded
//	CONTEXT
//	  0%

import (
	"fmt"
)

// launchSidebarModules renders the P12 launch module set for the sidebar.
// The frame's module order: NEXT, DECISIONS, AGENTS, TOOLS, CONTEXT. Each
// section is drop-whole under the sidebar budget like every other module.
// has() gates on the empty transcript: these modules REPLACE the run-time
// modules on the launch screen (run/pipeline/trajectory have nothing to
// project yet, and files/activity are empty).
func (m model) launchSidebarModules(width, budget int) []string {
	if width <= 0 || budget <= 0 {
		return nil
	}
	var lines []string
	used := 0
	appendSection := func(body []string) bool {
		if len(body) == 0 {
			return true
		}
		if len(lines) > 0 {
			if used+1 > budget {
				return false
			}
			lines = append(lines, "")
			used++
		}
		if used+len(body) > budget {
			return false
		}
		lines = append(lines, body...)
		used += len(body)
		return true
	}

	// NEXT — the single next action (frame: "resume, or describe a change").
	if !appendSection(m.launchNextModule(width)) {
		return lines
	}
	// DECISIONS — settled/open counts from the reconstructed ledger.
	if !appendSection(m.launchDecisionsModule(width)) {
		return lines
	}
	// AGENTS — honest "none running" on launch.
	if !appendSection(m.launchAgentsModule(width)) {
		return lines
	}
	// TOOLS — registered count + degraded-server warning.
	if !appendSection(m.launchToolsModule(width)) {
		return lines
	}
	// CONTEXT — the context-fill readout (0% on launch).
	if !appendSection(m.launchContextModule(width)) {
		return lines
	}
	return lines
}

// launchNextModule renders NEXT: "resume, or describe a change" when a
// resumable session exists, "describe a change" when not.
func (m model) launchNextModule(width int) []string {
	next := "describe a change"
	if m.launchHasResumable() {
		next = "resume, or describe a change"
	}
	lines := []string{sidebarHeader("NEXT", width)}
	for _, wrapped := range wrapPlainText(next, width-2) {
		lines = append(lines, "  "+zeroTheme.ink.Render(wrapped))
	}
	return lines
}

// launchDecisionsModule renders the DECISIONS summary from the reconstructed
// ledger: the settled count and, when open questions exist, the amber open
// count (frame kAYHl: "3 settled" / "1 open").
func (m model) launchDecisionsModule(width int) []string {
	decisions, decisionsOK := m.designDecisions()
	open, openOK := m.designOpenQuestions()
	if (!decisionsOK || len(decisions) == 0) && (!openOK || len(open) == 0) {
		return nil
	}
	lines := []string{sidebarHeader("DECISIONS", width)}
	if decisionsOK && len(decisions) > 0 {
		lines = append(lines, "  "+zeroTheme.ink.Render(fmt.Sprintf("%d settled", len(decisions))))
	}
	if openOK && len(open) > 0 {
		lines = append(lines, "  "+zeroTheme.amber.Render(fmt.Sprintf("%d open", len(open))))
	}
	return lines
}

// launchAgentsModule renders AGENTS: "none running" on launch (the frame's
// honest empty state).
func (m model) launchAgentsModule(width int) []string {
	lines := []string{sidebarHeader("AGENTS", width)}
	lines = append(lines, "  "+zeroTheme.faint.Render("none running"))
	return lines
}

// launchToolsModule renders TOOLS: the registered count and, when an MCP
// server is degraded, the amber "N mcp degraded" line (frame kAYHl).
func (m model) launchToolsModule(width int) []string {
	registered := len(m.registeredTools())
	if registered == 0 {
		return nil
	}
	lines := []string{sidebarHeader("TOOLS", width)}
	lines = append(lines, "  "+zeroTheme.ink.Render(fmt.Sprintf("%d registered", registered)))
	if degraded := m.launchDegradedServers(); degraded > 0 {
		lines = append(lines, "  "+zeroTheme.amber.Render(fmt.Sprintf("%d mcp degraded", degraded)))
	}
	return lines
}

// launchContextModule renders CONTEXT: the context-fill percent, shared with
// the status-line gauge so both read the same figure.
func (m model) launchContextModule(width int) []string {
	pct, _, _, style, ok := m.contextFillPercent()
	if !ok {
		// 0% is the honest launch figure (nothing sent yet).
		lines := []string{sidebarHeader("CONTEXT", width)}
		lines = append(lines, "  "+zeroTheme.ink.Render("0%"))
		return lines
	}
	lines := []string{sidebarHeader("CONTEXT", width)}
	lines = append(lines, "  "+style.Render(fmt.Sprintf("%d%%", pct)))
	return lines
}

// launchHasResumable reports whether a workspace-scoped resumable session
// exists — the cheap check NEXT uses (no event reads; the resume CARD does
// the full reconstruction lazily).
func (m model) launchHasResumable() bool {
	if m.sessionStore == nil {
		return false
	}
	metas, err := m.sessionStore.ListResumable()
	if err != nil {
		return false
	}
	for _, meta := range metas {
		if sessionWorkspaceMatch(meta, m.cwd) && meta.EventCount > 0 {
			return true
		}
	}
	return false
}
