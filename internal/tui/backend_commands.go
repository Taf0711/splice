package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/redaction"
)

// backend_commands.go shows backend state the CLI already owns (hooks,
// plugins, specialists, usage, git changes, verification) inside the TUI.
// The CLI renders it; the TUI runs the read-only form in-process through
// Options.BackendCommand, off the UI goroutine, and shows the output as a
// card that replaces its own "running" row.

// BackendCommandResult is the captured output of one allowlisted CLI command.
type BackendCommandResult struct {
	Output   string
	Error    string
	ExitCode int
}

type backendCommandResultMsg struct {
	title  string
	rowID  string
	result BackendCommandResult
}

const backendCardPrefix = "\x00backend-card\x00"

// backendCommandArgs maps a slash command to the CLI args it runs. User
// arguments are passed through only where the CLI allowlist accepts flags.
func backendCommandArgs(kind commandKind, text string) (title string, args []string) {
	extra := strings.Fields(text)
	switch kind {
	case commandHooks:
		return "hooks", []string{"hooks", "list"}
	case commandPlugins:
		return "plugins", []string{"plugins", "list"}
	case commandSpecialists:
		return "specialists", []string{"specialist", "list"}
	case commandUsage:
		return "usage", append([]string{"usage", "report"}, extra...)
	case commandChanges:
		return "changes", append([]string{"changes", "inspect"}, extra...)
	case commandVerify:
		return "verify", append([]string{"verify"}, extra...)
	}
	return "", nil
}

func (m model) startBackendCommand(kind commandKind, text string) (model, tea.Cmd) {
	title, args := backendCommandArgs(kind, text)
	rowID := "backend/" + title
	if m.backendCommand == nil {
		return m.setBackendRow(rowID, backendBlockedCard(title, "not available in this session")), nil
	}
	// Verification runs the project's own check commands (npm scripts,
	// make targets). In an untrusted workspace that is running untrusted
	// code, so it waits for /trust like every other project-defined action.
	if kind == commandVerify && !m.trusted {
		return m.setBackendRow(rowID, backendBlockedCard(title, "verification runs this project's own commands; trust the workspace first with /trust")), nil
	}
	m = m.setBackendRow(rowID, "Running splice "+strings.Join(args, " ")+" …")
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	runner := m.backendCommand
	return m, func() tea.Msg {
		return backendCommandResultMsg{title: title, rowID: rowID, result: runner(ctx, args)}
	}
}

// setBackendRow writes the command's single transcript row in place, so a
// re-run replaces the old card instead of stacking copies.
func (m model) setBackendRow(rowID, text string) model {
	row := transcriptRow{kind: rowSystem, id: rowID, tool: "backend", text: text}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].id == rowID {
			m.transcript[i] = row
			if i < m.flushed {
				m.invalidateSettledTranscript()
			}
			return m
		}
	}
	m.transcript = appendTranscriptRow(m.transcript, row)
	return m
}

// backendBlockedCard reports a command that did not start. Nothing ran, so the
// card shows "not run" rather than "failed".
func backendBlockedCard(title, reason string) string {
	return backendCardPrefix + title + "\x00blocked\x00" + redaction.RedactString(reason, redaction.Options{})
}

func backendCard(title string, result BackendCommandResult) string {
	status := "ok"
	if result.ExitCode != 0 {
		status = "failed"
	}
	body := result.Output
	if result.Error != "" {
		if body != "" {
			body += "\n"
		}
		body += result.Error
	}
	if strings.TrimSpace(body) == "" {
		body = "nothing to report"
	}
	return backendCardPrefix + title + "\x00" + status + "\x00" + redaction.RedactString(body, redaction.Options{})
}

func parseBackendCard(text string) (title, status, body string, ok bool) {
	rest, found := strings.CutPrefix(text, backendCardPrefix)
	if !found {
		return "", "", "", false
	}
	parts := strings.SplitN(rest, "\x00", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// renderBackendCard keeps the CLI's own column alignment: lines are fit to
// the card, never reflowed, so tables stay tables.
func renderBackendCard(title, status, body string, width int) string {
	glyph, head := zeroTheme.green.Render("•"), zeroTheme.green.Bold(true).Render(title)
	switch status {
	case "ok":
	case "blocked":
		glyph, head = zeroTheme.muted.Render("•"), zeroTheme.muted.Bold(true).Render(title+" not run")
	default:
		glyph, head = zeroTheme.red.Render("•"), zeroTheme.red.Bold(true).Render(title+" failed")
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i, line := range lines {
		lines[i] = zeroTheme.muted.Render(strings.ReplaceAll(line, "\t", "    "))
	}
	return toolCard(head, glyph, lines, "", zeroTheme.line, width)
}
