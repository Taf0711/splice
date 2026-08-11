package tui

import (
	"context"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

// Run starts the Splice Bubble Tea shell and returns a process-style exit code.
func Run(ctx context.Context, options Options) int {
	// The interactive shell needs a real terminal on stdin: with piped or
	// redirected input Bubble Tea blocks forever waiting for events that never
	// arrive (e.g. `echo "" | splice`). Fail fast with guidance toward the headless
	// path instead of hanging. term.IsTerminal is a true TTY check (it rejects
	// pipes, regular files, and non-terminal char devices like /dev/null) and
	// fails closed — anything that is not a verified terminal blocks the shell.
	if !term.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(os.Stderr, "splice: the interactive shell needs a terminal (stdin is not a TTY). For non-interactive use, run: splice exec \"<prompt>\"")
		return 2
	}
	restoreConsole := setupConsoleUTF8()
	defer restoreConsole()

	reporter := newHerdrReporter(os.Getenv, nil, nil)
	if reporter != nil {
		reporter.Report(herdrIdle)
		defer reporter.Close()
	}

	externalSink := options.RuntimeMessageSink
	var program *tea.Program
	forward := func(msg tea.Msg) {
		if externalSink != nil {
			externalSink(msg)
		}
		if program != nil {
			program.Send(msg)
		}
	}
	// Coalesce streamed assistant-text deltas to ~one frame each so a fast provider
	// can't drive a full Update→View per token; every other message flushes pending
	// text first, keeping order intact.
	options.RuntimeMessageSink = newTextCoalescer(forward).send
	options.AltScreen = useAltScreen(options)

	programOpts := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithFilter(mouseEventFilter()),
	}
	// Honor the no-color.org spec ourselves: NO_COLOR set to ANY non-empty value
	// disables color. bubbletea/colorprofile only respects strconv.ParseBool-style
	// values, so NO_COLOR=yes / NO_COLOR=foo would otherwise leave the UI in full
	// color. Force the Ascii (no-color, bold-still-allowed) profile. (AUDIT-M3)
	if noColorRequested(os.Getenv) {
		programOpts = append(programOpts, tea.WithColorProfile(colorprofile.Ascii))
	}
	initialModel := newModel(ctx, options)
	initialModel.herdr = reporter
	initialModel = initialModel.openLaunchSessionPicker()
	if initialModel.wantsMouseCapture() {
		initialModel.mouseCapture = true
	}
	program = tea.NewProgram(initialModel, programOpts...)

	final, err := program.Run()
	if err != nil {
		// Surface the failure: exiting 1 with splice diagnostics left users
		// guessing why the default chat surface died.
		fmt.Fprintln(os.Stderr, "splice: tui error:", err)
		return 1
	}
	// Alternate-screen mode clears the whole screen on exit, so the chat the
	// user saw during the session would otherwise be lost. After a normal exit,
	// redraw the final completed transcript into native terminal scrollback so
	// it stays readable, selectable, and copyable. Only chat surfaces with real
	// content dump anything; startup/setup/empty transcripts are skipped by
	// writeTranscriptScrollback.
	if finalModel, ok := final.(model); ok && finalModel.altScreen {
		finalModel.writeTranscriptScrollback(os.Stdout)
	}
	return 0
}

// writeTranscriptScrollback renders the final completed transcript into normal
// terminal scrollback. Bubble Tea's alt-screen mode clears the screen on exit,
// so the settled chat history shown during the session must be printed back out
// to remain visible. It reuses the live/scrollback row rendering (renderRowMode)
// so the dump matches what was on screen, and returns whether anything was
// written (false for an empty or welcome-only transcript, letting callers skip).
func (m model) writeTranscriptScrollback(w io.Writer) bool {
	if m.width <= 0 || len(m.transcript) == 0 {
		return false
	}
	rc := buildRowContext(m.transcript)
	width := chatWidth(m.width)
	wroteAny := false
	previousKind := rowWelcome
	for _, row := range m.transcript {
		if row.kind == rowWelcome || rc.skip(row) {
			continue
		}
		rendered := m.renderRowMode(row, width, rc, true)
		if rendered == "" {
			continue
		}
		// Mirror the flush-frontier spacing: a blank line opens each turn and
		// separates a user row from a reasoning row that immediately follows.
		if wroteAny && startsTurn(row.kind) {
			fmt.Fprintln(w)
		}
		if wroteAny && previousKind == rowUser && row.kind == rowReasoning {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, rendered)
		wroteAny = true
		previousKind = row.kind
	}
	return wroteAny
}

func useAltScreen(_ Options) bool {
	return true
}

// noColorRequested reports whether the no-color.org spec is in effect: NO_COLOR set
// to any non-empty value. Checked here rather than via the colorprofile dependency,
// whose strconv.ParseBool gate ignores common values like NO_COLOR=yes. (AUDIT-M3)
func noColorRequested(getenv func(string) string) bool {
	return getenv("NO_COLOR") != ""
}
