package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

func TestGenericCardBodySanitizesTerminalOutput(t *testing.T) {
	oldProfile := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.Ascii
	defer func() { lipgloss.Writer.Profile = oldProfile }()

	var opts = cardRenderOptions{}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "clear screen payload is removed",
			// A hostile file's contents could drive the user's terminal.
			in:   "safe\x1b[2Jtail",
			want: "safetail",
		},
		{
			name: "OSC hyperlink is removed but label survives",
			in:   "\x1b]8;;http://evil\aclick\x1b]8;;\a",
			want: "click",
		},
		{
			name: "unterminated OSC stops at line boundary",
			in:   "before\x1b]8;;http://evil\nvisible",
			want: "before\nvisible",
		},
		{
			name: "legitimate UTF8 is unchanged",
			in:   "漢字 🙂 ┌─┐",
			want: "漢字 🙂 ┌─┐",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := genericCardBody(test.in, opts)
			got := plainRender(t, strings.Join(body.lines, "\n"))
			want := test.want
			if got != want {
				t.Fatalf("card body = %q, want %q", got, want)
			}
			if strings.Contains(got, "\x1b") {
				t.Fatalf("card body contains an escape: %q", got)
			}
			if strings.Contains(sanitizeTerminalOutput(test.in, true), "\x1b") {
				t.Fatalf("sanitized card content contains an escape: %q", test.in)
			}
		})
	}
}

func TestTruncateTUIOutputSanitizesRowText(t *testing.T) {
	got := truncateTUIOutput("row\x1b[2Jtext\x1b]8;;http://evil\aclick\x1b]8;;\a", 0)
	if got != "rowtextclick" {
		t.Fatalf("row text = %q, want %q", got, "rowtextclick")
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("row text contains an escape: %q", got)
	}
}

func TestGenericCardBodyCapsDisplayWidth(t *testing.T) {
	body := genericCardBody("123456", cardRenderOptions{width: 4})
	if got := plainRender(t, body.lines[0]); got != "123…" {
		t.Fatalf("card body line = %q, want %q", got, "123…")
	}
}

func TestAssistantMarkdownStillEmitsANSIStyling(t *testing.T) {
	rendered := strings.Join(renderAssistantMarkdownText("**styled**", 80, 80, false), "\n")
	if !strings.Contains(rendered, markdownBoldStart) || !strings.Contains(rendered, markdownBoldEnd) {
		t.Fatalf("markdown styling changed: %q", rendered)
	}
}
