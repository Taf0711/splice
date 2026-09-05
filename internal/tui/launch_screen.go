package tui

// launch_screen.go (P12, frame kAYHl — owner-revised "LAUNCH COCKPIT"):
// the empty-transcript body becomes an information cockpit, replacing the
// centered braid splash. Same chrome as every other state; the transcript
// body carries the wordmark, the facts block, the resume card (when a
// resumable session exists), START actions, and the honest-state line.
//
// Every number projects in-tree data: tool registry, MCP view state, skills
// loader, git branch, design-state ledger. Nothing is invented — an
// unknown value omits its row rather than showing a padded zero.

import (
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
)

// launchFact is one key/value row of the facts block.
type launchFact struct {
	key   string
	value string
	warn  bool // amber value (a state the user likely wants to fix)
}

// launchWordmark renders the identity row: lowercase "splice" in the brand
// lime + "| design mode" beside it (frame kAYHl).
func (m model) launchWordmark() string {
	return zeroTheme.accent.Render("splice") + " " +
		zeroTheme.muted.Render("| design mode")
}

// launchFacts assembles the facts block. Rows omit rather than pad: an
// unknown value never renders as a fake number (tests is the honest case —
// there is no last-run source yet, so the value says so).
func (m model) launchFacts() []launchFact {
	facts := []launchFact{
		{key: "repo", value: shortenPath(m.cwd)},
		{key: "model", value: displayValue(strings.TrimSpace(m.modelName), "none")},
		{key: "effort", value: displayValue(strings.TrimSpace(string(m.reasoningEffort)), "auto")},
		{key: "tools", value: fmt.Sprintf("%d · %d sources", len(m.registeredTools()), m.launchSourceCount())},
	}
	if degraded := m.launchDegradedServers(); degraded > 0 {
		facts = append(facts, launchFact{
			key:   "mcp",
			value: fmt.Sprintf("%d server · %d degraded", len(m.mcpViewState().Servers), degraded),
			warn:  true,
		})
	} else if n := len(m.mcpViewState().Servers); n > 0 {
		facts = append(facts, launchFact{key: "mcp", value: fmt.Sprintf("%d servers", n)})
	}
	if skills := m.installedSkills(); len(skills) > 0 {
		facts = append(facts, launchFact{key: "skills", value: fmt.Sprintf("%d installed", len(skills))})
	}
	facts = append(facts, launchFact{key: "tests", value: "not run this session"})
	return facts
}

// launchSourceCount counts distinct tool sources (builtin + per-MCP server)
// using the same attribution the /tools card renders.
func (m model) launchSourceCount() int {
	return len(toolsGroupBySource(m.registeredTools(), m.mcpViewState(), m.agentOptions.DeferThreshold))
}

// launchDegradedServers counts MCP servers whose state is not connected.
func (m model) launchDegradedServers() int {
	degraded := 0
	for _, server := range m.mcpViewState().Servers {
		if !strings.EqualFold(strings.TrimSpace(server.State), "connected") {
			degraded++
		}
	}
	return degraded
}

// launchResumeCard builds the resume block from the latest resumable
// session's reconstructed design state: settled decision count and the
// session identity. Returns nil when there is no resumable session or no
// session store — the launch screen then simply has no resume card (honest
// absence, never a placeholder).
func (m model) launchResumeCard() []string {
	if m.sessionStore == nil {
		return nil
	}
	metas, err := m.sessionStore.ListResumable()
	if err != nil || len(metas) == 0 {
		return nil
	}
	// Workspace-scoped and content-checked, mirroring newSessionPicker's
	// filters so the launch card offers THIS repo's last resumable session.
	var latest *sessions.Metadata
	for i := range metas {
		meta := metas[i]
		if !sessionWorkspaceMatch(meta, m.cwd) || meta.EventCount == 0 {
			continue
		}
		latest = &metas[i]
		break
	}
	if latest == nil {
		return nil
	}
	events, readErr := m.sessionStore.ReadEvents(latest.SessionID)
	if readErr != nil {
		return nil // fail-open, same as the picker: no state, no card
	}
	state, stateErr := splicerun.ReconstructDesignState(events)
	if stateErr != nil {
		return nil
	}
	// Pins are settled anchors by definition (§7.1); the frame's open row
	// renders only from a real open-question source, which the ledger does
	// not carry yet — omit rather than invent.
	settled := len(state.Decisions)
	when := sessionWhen(latest.UpdatedAt, m.now())
	head := zeroTheme.muted.Bold(true).Render("LAST SESSION") + "  " +
		zeroTheme.faint.Render(strings.TrimSpace(when))
	lines := []string{head}
	if settled > 0 {
		lines = append(lines, "  "+zeroTheme.green.Render("[+]")+" "+
			zeroTheme.ink.Render(fmt.Sprintf("%d decisions settled", settled)))
	}
	lines = append(lines, "  "+zeroTheme.accent.Render("/resume")+" "+
		zeroTheme.muted.Render("restore decisions, evidence and open questions"))
	return lines
}

// launchHealth reports the launch frame's health dimension: degraded when a
// real MCP server is in a degraded (non-connected, non-disabled) state,
// normal otherwise. The launch screen has no presentation run state, so the
// degraded-server count is the only honest health source.
func (m model) launchHealth() string {
	if m.launchDegradedServers() > 0 {
		return "degraded"
	}
	return "normal"
}

// launchContractBand renders the launch contract dimension:
// "phase idle | health degraded | gate none" (frame kAYHl item 2). The
// launch screen has run nothing, so phase is idle and gate is none; only
// health projects live state.
func (m model) launchContractBand() string {
	health := m.launchHealth()
	healthStyle := zeroTheme.ink
	if health != "normal" {
		healthStyle = zeroTheme.amber
	}
	return zeroTheme.faint.Render("phase ") + zeroTheme.ink.Render("idle") +
		zeroTheme.faint.Render(" | health ") + healthStyle.Render(health) +
		zeroTheme.faint.Render(" | gate ") + zeroTheme.ink.Render("none")
}

// launchStart renders the START block. The /mcp action's text adapts to the
// degraded-server count (frame: "/mcp  reconnect the degraded server").
func (m model) launchStart() []string {
	mcpDesc := "connect tools"
	if degraded := m.launchDegradedServers(); degraded > 0 {
		mcpDesc = "reconnect the degraded server"
	}
	actions := []struct{ cmd, desc string }{
		{"describe a change", "splice settles decisions before it writes"},
		{"/model", "switch model + effort"},
		{"/mcp", mcpDesc},
		{"/init", "write a workspace config for this repo"},
	}
	lines := []string{zeroTheme.muted.Bold(true).Render("START")}
	for _, a := range actions {
		lines = append(lines, "  "+zeroTheme.accent.Render(a.cmd)+"  "+
			zeroTheme.muted.Render(a.desc))
	}
	return lines
}

// launchScreenLines assembles the full launch body: wordmark + tagline,
// facts, resume card (when one exists), START, honest-state line.
func (m model) launchScreenLines() []string {
	// Frame kAYHl, item 2: the contract dimension renders before any run —
	// "phase idle | health degraded | gate none". Phase is idle (nothing has
	// run), gate is none (no gate source exists on launch); health projects
	// the real degraded-server count.
	lines := []string{m.launchContractBand(), "", m.launchWordmark(), "", zeroTheme.muted.Render(emptyStateTagline), ""}
	for _, fact := range m.launchFacts() {
		value := zeroTheme.ink.Render(fact.value)
		if fact.warn {
			value = zeroTheme.amber.Render(fact.value)
		}
		lines = append(lines, "  "+zeroTheme.muted.Render(fact.key+"        ")+" "+
			value)
	}
	lines = append(lines, "")
	if resume := m.launchResumeCard(); len(resume) > 0 {
		lines = append(lines, resume...)
		lines = append(lines, "")
	}
	lines = append(lines, m.launchStart()...)
	lines = append(lines, "", zeroTheme.faint.Render("nothing has run in this session"))
	return lines
}
