package tui

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// trust_card.go renders the UNTRUSTED PROJECT CONFIG card (GAP-I, §15.1/§16,
// Pen frame I1). The card is a projection: it shows what the project config
// WOULD change, states that nothing loaded, and names the next action. The
// trust decision itself belongs to the existing trust picker (newTrustPicker
// / applyTrustPickerChoice); the card never decides.

// trustConfigTranscriptMarker tags a system row whose payload is a trust
// config card (same NUL-tag pattern as the handoff/receipt cards).
const trustConfigTranscriptMarker = "\x00trust-config\x00"

// trustActionView is the trust picker row value for "show what this config
// would change". applyTrustPickerChoice handles it explicitly (before its
// unknown-choice guard) — it is an inspection action, not a trust decision.
const trustActionView = "view-config"

// trustConfigTranscriptPayload serializes the config summary into the row
// text. Fields join with NUL: path, parseError, summary lines joined by \x1f.
func trustConfigTranscriptPayload(c projectTrustConfig) string {
	var b strings.Builder
	b.WriteString(trustConfigTranscriptMarker)
	b.WriteString(c.Path)
	b.WriteByte(0)
	b.WriteString(c.ParseError)
	b.WriteByte(0)
	b.WriteString(strings.Join(trustConfigCardLines(c, 0), "\x1f"))
	return b.String()
}

// parseTrustConfigTranscriptPayload decodes a trust config payload row.
func parseTrustConfigTranscriptPayload(text string) (projectTrustConfig, []string, bool) {
	if !strings.HasPrefix(text, trustConfigTranscriptMarker) {
		return projectTrustConfig{}, nil, false
	}
	parts := strings.Split(strings.TrimPrefix(text, trustConfigTranscriptMarker), "\x00")
	if len(parts) != 3 {
		return projectTrustConfig{}, nil, false
	}
	return projectTrustConfig{Path: parts[0], ParseError: parts[1]},
		strings.Split(parts[2], "\x1f"), true
}

// renderTrustConfigCard renders the card at the given width. Form per I1:
// amber [~] header (a caution, not a failure), the path, the change
// classes, the executable-config warning, and the not-loaded statement.
// Keys are named where they work: the trust decision happens in the trust
// picker, so the card's [T]/[D] line says to use the trust menu rather
// than competing with it for input.
func renderTrustConfigCard(c projectTrustConfig, summary []string, width int) string {
	if width <= 0 {
		return ""
	}
	header := zeroTheme.amber.Render("[~] UNTRUSTED PROJECT CONFIG — not loaded")
	lines := []string{header, ""}
	lines = append(lines, "  config: "+c.Path)
	if len(summary) > 0 {
		lines = append(lines, "")
		lines = append(lines, summary...)
	}
	lines = append(lines, "")
	lines = append(lines,
		"  "+zeroTheme.faint.Render("this config can run commands on your machine."),
		"  "+zeroTheme.faint.Render("review before trusting. it is NOT active in this session."),
	)
	lines = append(lines, "")
	lines = append(lines,
		zeroTheme.muted.Render("[V]")+" "+zeroTheme.ink.Render("view config file")+"  "+
			zeroTheme.muted.Render("[T]")+" "+zeroTheme.ink.Render("trust (menu open)")+"  "+
			zeroTheme.muted.Render("[D]")+" "+zeroTheme.ink.Render("decline = defaults"))
	return styledBlock(width, lines, zeroTheme.cardRun)
}

// trustConfigNotice appends the card to the transcript as a NUL-tagged
// system row. Used at launch when the workspace is untrusted (or undecided)
// and an executable project config exists. Best-effort: a card failure
// never blocks launch.
func (m *model) trustConfigNotice(c projectTrustConfig) {
	if c.Empty() && c.ParseError == "" {
		return
	}
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowSystem,
		text: trustConfigTranscriptPayload(c),
	})
}

// openTrustConfigInViewer shows the raw config file in $EDITOR (the same
// hand-over as diff [O]; read-only enforcement is the editor's concern).
// Honest failures: a vanished file and a missing $EDITOR both say so.
func (m model) openTrustConfigInViewer(path string) (tea.Model, tea.Cmd) {
	if strings.TrimSpace(path) == "" {
		return m, nil
	}
	if _, err := os.Stat(path); err != nil {
		return m.appendSystemNotice("Config file no longer exists: " + path), nil
	}
	editor := strings.TrimSpace(osEditor())
	if editor == "" {
		return m.appendSystemNotice("No $EDITOR set — read it manually: cat " + path), nil
	}
	c := execEditor(editor, path)
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return diffEditorMsg{err: err}
	})
}
