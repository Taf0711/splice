package tui

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
)

// PromptWorkspaceTrust asks whether the current workspace is trusted. It uses
// the normal terminal screen so the main TUI can start its own alt-screen later.
// Ctrl+C is an explicit decline because the terminal is in raw mode.
func PromptWorkspaceTrust(ctx context.Context, input io.Reader, output io.Writer) (trusted bool, decided bool, err error) {
	model := &trustPromptModel{cursor: 1}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output))
	final, err := program.Run()
	if err != nil {
		return false, false, err
	}
	result, ok := final.(*trustPromptModel)
	if !ok {
		return false, false, fmt.Errorf("trust prompt returned %T", final)
	}
	return result.trusted, result.decided, nil
}

// trustPromptOptions is the number of selectable rows. Deriving the cursor
// arithmetic from it keeps up/down correct if a third option (for example
// trusting the parent folder) is ever added.
const trustPromptOptions = 2

type trustPromptModel struct {
	cursor  int
	trusted bool
	decided bool
}

func (m *trustPromptModel) Init() tea.Cmd { return nil }

func (m *trustPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.InterruptMsg:
		return m.choose(false)
	case tea.KeyMsg:
		if _, release := msg.(tea.KeyReleaseMsg); release {
			return m, nil
		}
		if keyCtrl(msg, 'c') {
			return m.choose(false)
		}
		switch {
		case keyIs(msg, tea.KeyUp) || keyText(msg) == "k":
			m.cursor = (m.cursor + trustPromptOptions - 1) % trustPromptOptions
		case keyIs(msg, tea.KeyDown) || keyText(msg) == "j":
			m.cursor = (m.cursor + 1) % trustPromptOptions
		case keyIs(msg, tea.KeyEnter):
			return m.choose(m.cursor == 0)
		case keyText(msg) == "t":
			return m.choose(true)
		case keyText(msg) == "d" || keyText(msg) == "n":
			return m.choose(false)
		}
	}
	return m, nil
}

func (m *trustPromptModel) choose(trusted bool) (tea.Model, tea.Cmd) {
	m.trusted = trusted
	m.decided = true
	return m, tea.Quit
}

func (m *trustPromptModel) View() tea.View {
	trustMark, declineMark := "  ", "  "
	if m.cursor == 0 {
		trustMark = "> "
	} else {
		declineMark = "> "
	}
	// ASCII only. This is the first screen a new user sees, and it must stay
	// legible on a console that renders UTF-8 as a legacy code page (a real
	// report showed box-drawing and arrow glyphs arriving as CP437 mojibake).
	// A trust decision with unreadable instructions is worse than no prompt.
	return tea.NewView(fmt.Sprintf("Trust this workspace?\n\n%s trust this folder  (t)\n%s do not trust      (d)\n\nUse up/down arrows or j/k, then Enter. Ctrl+C declines.\n", trustMark, declineMark))
}
