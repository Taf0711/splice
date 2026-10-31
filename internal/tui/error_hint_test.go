package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// A hinted error row renders the raw error and the faint one-line next step
// below it; an unhinted row renders just the error.
func TestRenderErrorRowShowsHint(t *testing.T) {
	withHint := renderErrorRow(transcriptRow{
		kind: rowError,
		text: "auth error: your API key is missing or invalid",
		hint: "API key rejected — run /provider to re-check your credentials",
	}, 80)
	if !strings.Contains(withHint, "auth error:") {
		t.Fatalf("expected raw error text in output, got:\n%s", withHint)
	}
	if !strings.Contains(withHint, "/provider") {
		t.Fatalf("expected hint referencing /provider, got:\n%s", withHint)
	}

	noHint := renderErrorRow(transcriptRow{kind: rowError, text: "provider error: mystery"}, 80)
	if strings.Contains(noHint, "→") {
		t.Fatalf("unhinted error must not render a hint arrow, got:\n%s", noHint)
	}
}

// An error row at 80 columns with the sidebar open must show the whole cause.
// The row wraps inside the chat column instead of cutting the text at its edge.
func TestRenderErrorRowWrapsFullCause(t *testing.T) {
	width := 80 - sidebarWidth(80) - 3
	cause := "Crystallization failed: stage design_crystallize model: stage routing config invalid: " +
		"parse /Users/me/.config/splice/stage-models.json: unexpected end of JSON input"
	out := stripANSI(renderErrorRow(transcriptRow{kind: rowError, text: cause}, width))
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("error row overflows the chat column: %d > %d: %q", w, width, line)
		}
	}
	flat := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if strings.ContainsRune("│╭╮╰╯─", r) {
			return ' '
		}
		return r
	}, out)), " ")
	for _, want := range []string{"stage routing config invalid", "stage-models.json", "unexpected end of JSON input"} {
		if !strings.Contains(flat, want) {
			t.Fatalf("error row cut off %q at width %d:\n%s", want, width, out)
		}
	}
}

// A trust notice at 80 and 120 columns must show its whole clause. The
// notice wraps to the row instead of cutting the last clause off.
func TestRenderSystemNoteWrapsTrustNotice(t *testing.T) {
	const notice = "Workspace trust saved. The pipeline topology applies from the next run. The project config, hooks, plugins, and MCP servers load on the next launch."
	for _, width := range []int{80, 120} {
		out := stripANSI(renderSystemNote(notice, width))
		for _, line := range strings.Split(out, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d: notice line overflows: %d cells: %q", width, w, line)
			}
		}
		flat := strings.Join(strings.Fields(strings.ReplaceAll(out, "·", " ")), " ")
		if !strings.Contains(flat, notice) {
			t.Fatalf("width %d: notice cut off, got:\n%s", width, out)
		}
	}
}

// A short notice keeps the one-line ack grammar at any width.
func TestRenderSystemNoteKeepsShortAckOneLine(t *testing.T) {
	out := stripANSI(renderSystemNote("Trust saved.", 80))
	if strings.Contains(out, "\n") {
		t.Fatalf("short notice wrapped: %q", out)
	}
	if !strings.HasPrefix(out, "· Trust saved.") {
		t.Fatalf("short notice lost its marker: %q", out)
	}
}
