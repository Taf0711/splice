package tui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

func TestUseAltScreenForInteractiveChat(t *testing.T) {
	if !useAltScreen(Options{}) {
		t.Fatal("normal chat should use the alternate screen")
	}
	if !useAltScreen(Options{Setup: SetupOptions{Visible: true}}) {
		t.Fatal("setup takeover should also use the alternate screen")
	}
}

func TestProgramDecodesInputRendersAndQuits(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var output bytes.Buffer
	program := tea.NewProgram(
		newModel(ctx, Options{AltScreen: true, Version: "test"}),
		tea.WithContext(ctx),
		tea.WithInput(strings.NewReader("/exit\r")),
		tea.WithOutput(&output),
		tea.WithWindowSize(80, 24),
		tea.WithColorProfile(colorprofile.Ascii),
		tea.WithoutSignals(),
	)

	final, err := program.Run()
	if err != nil {
		t.Fatalf("run Bubble Tea program: %v", err)
	}
	model, ok := final.(model)
	if !ok {
		t.Fatalf("final model type = %T, want tui.model", final)
	}
	if !model.exiting {
		t.Fatal("/exit input did not reach the main TUI model")
	}
	if !strings.Contains(output.String(), emptyStateTagline) {
		t.Fatalf("rendered output does not contain %q: %q", emptyStateTagline, output.String())
	}
}

// TestRunRejectsNonTTYStdin pins that the interactive shell fails fast with a
// non-splice code when stdin is not a terminal, instead of blocking forever in the
// Bubble Tea event loop (e.g. `echo "" | splice`). The guard runs before any model
// construction, so empty Options are fine.
func TestRunRejectsNonTTYStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	w.Close() // a pipe read-end is not a character device

	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()

	done := make(chan int, 1)
	go func() { done <- Run(context.Background(), Options{}) }()

	select {
	case code := <-done:
		if code != 2 {
			t.Fatalf("Run with non-TTY stdin returned %d; want exit code 2 from the stdin TTY guard", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run blocked on non-TTY stdin instead of failing fast")
	}
}
