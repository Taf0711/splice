package cli

import (
	"strings"
	"testing"
)

// The TUI may only run read-only CLI forms. A mutating subcommand, an
// unknown flag, or a flag smuggled without its value must be refused here,
// on the side that executes.
func TestTUIBackendArgsAllowlist(t *testing.T) {
	allowed := [][]string{
		{"hooks", "list"},
		{"plugins", "list"},
		{"specialist", "list"},
		{"usage", "report"},
		{"usage", "report", "--days", "7"},
		{"changes", "inspect", "--base", "main"},
		{"verify"},
		{"verify", "--only", "go-test"},
	}
	for _, args := range allowed {
		if _, err := tuiBackendArgs(args); err != nil {
			t.Fatalf("tuiBackendArgs(%q) refused: %v", args, err)
		}
	}
	refused := [][]string{
		{},
		{"hooks"},
		{"hooks", "add", "x"},
		{"hooks", "remove", "x"},
		{"plugins", "add", "https://example.com/p.git"},
		{"changes", "commit", "-m", "x"},
		{"changes", "push"},
		{"changes", "inspect", "--force"},
		{"usage", "report", "--days"},
		{"usage", "report", "--days", "--json"},
		{"verify", "--attempts", "5"},
		{"mcp", "remove", "x"},
		{"update", "--apply"},
	}
	for _, args := range refused {
		if _, err := tuiBackendArgs(args); err == nil {
			t.Fatalf("tuiBackendArgs(%q) accepted a form the TUI must not run", args)
		}
	}
}

func TestTUIBackendCommandCapturesRefusal(t *testing.T) {
	result := newTUIBackendCommand(appDeps{})(nil, []string{"changes", "push"})
	if result.ExitCode == 0 || !strings.Contains(result.Error, "from the TUI") {
		t.Fatalf("refusal = %+v", result)
	}
}
