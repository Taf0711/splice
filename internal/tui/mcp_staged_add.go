package tui

// P3 GAP-H: the MCP staged-add contract (frame VCeGi). The confirm step
// becomes a STAGED ADD card: it shows exactly what will run, what the
// config change adds, where the config lands, the scope, and a banner that
// nothing runs until the user applies. Keys per the frame:
// [A] apply · add + enable, [D] add disabled, [X] discard, [E] edit command
// (back to the endpoint step). The staged card never dumps a command into
// the composer — the marketplace row that used to prefill /mcp add opens
// this wizard instead.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// stagedMCPAdd carries the facts the staged card renders. Derived once from
// the wizard state so the renderer stays a pure projection.
type stagedMCPAdd struct {
	serverName string
	// willRun is the exact command line the stdio transport spawns at session
	// start (empty for remote servers, whose target goes in willConnect).
	willRun string
	// willConnect is the remote URL the session will dial at startup.
	willConnect string
	transport   string
	untrusted   bool
	// adds describes the config delta in one line.
	adds     string
	config   string
	scope    string
	disabled bool
}

// stagedAddFromWizard derives the staged facts from the wizard's inputs. The
// config destination is the USER config file (m.mcpConfig user scope): the
// CLI add path (`splice mcp add`) writes there too, so the card never claims
// a destination the apply would not use.
func stagedAddFromWizard(m model, wizard *mcpAddWizardState) stagedMCPAdd {
	staged := stagedMCPAdd{
		serverName: wizard.serverName,
		scope:      "user",
		config:     displayValue(strings.TrimSpace(m.userConfigPath), "user config"),
		disabled:   false,
	}
	if wizard.isRemote() {
		staged.willConnect = redactMCPWizardDisplayValue(wizard.endpoint)
		staged.transport = "HTTP remote"
	} else {
		staged.willRun = redactMCPWizardCommand(wizard.endpoint)
		staged.transport = "stdio subprocess, spawned at session start"
		staged.untrusted = true
	}
	staged.adds = fmt.Sprintf("1 server, 0 tools until enabled")
	return staged
}

// renderStagedAddLines projects the staged facts into the card body. Banner
// first (the frame's "nothing runs until you apply"), then the five
// must-show fields.
func (s stagedMCPAdd) renderLines(width int) []string {
	lines := []string{
		zeroTheme.accent.Bold(true).Render("◆ STAGED ADD — nothing runs until you apply"),
		"",
		zeroTheme.accent.Render("will run"),
	}
	if s.willRun != "" {
		lines = append(lines, fitStyledLine("  "+zeroTheme.ink.Render(s.willRun), width))
		lines = append(lines, fitStyledLine(zeroTheme.faint.Render("  "+s.transport+" · untrusted output"), width))
	} else {
		lines = append(lines, fitStyledLine("  "+zeroTheme.ink.Render(s.willConnect), width))
		lines = append(lines, fitStyledLine(zeroTheme.faint.Render("  "+s.transport+" · remote, dialed at session start"), width))
	}
	lines = append(lines,
		zeroTheme.accent.Render("adds"),
		"  "+zeroTheme.ink.Render(s.adds),
		zeroTheme.accent.Render("config"),
		fitStyledLine("  "+zeroTheme.ink.Render(s.config)+" "+zeroTheme.faint.Render("("+s.scope+" scope)"), width),
	)
	return lines
}

// stagedAddFooter is the card's key line. Drop-whole under width pressure
// via fitStyledLine's caller: the core keys are [A]/[D]/[X]; [E] follows.
func stagedAddFooter() string {
	return "[A] apply · add + enable   [D] add disabled   [X] discard   [E] edit command   Esc close"
}

// handleStagedAddKey dispatches the staged card's keys. Runs only on the
// confirm step; other steps keep the wizard's own handling. Returns handled.
// No noBlockingModal() guard here: the wizard IS the active modal at this
// point (the caller routes keys here only when mcpAddWizard != nil), and
// noBlockingModal() is false whenever this wizard is up.
func (m model) handleStagedAddKey(msg tea.KeyMsg) (bool, model, tea.Cmd) {
	if m.mcpAddWizard == nil || m.mcpAddWizard.step != mcpAddWizardStepConfirm {
		return false, m, nil
	}
	if keyIs(msg, tea.KeyEsc) {
		m.mcpAddWizard = nil
		return true, m, nil
	}
	switch strings.ToLower(keyText(msg)) {
	case "a":
		next, cmd := m.saveMCPAddWizard(false)
		return true, next, cmd
	case "d":
		next, cmd := m.saveMCPAddWizard(true)
		return true, next, cmd
	case "x":
		m.mcpAddWizard = nil
		return true, m, nil
	case "e":
		m.mcpAddWizard.step = mcpAddWizardStepEndpoint
		m.mcpAddWizard.err = ""
		return true, m, nil
	}
	return false, m, nil
}
