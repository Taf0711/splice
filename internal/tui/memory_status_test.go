package tui

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
)

func TestMemoryStatusSegment(t *testing.T) {
	m := model{memoryStatus: "active", memoryCount: 42}
	if got := m.memoryStatusSegment(); got != "🧵 42" {
		t.Fatalf("got %q, want 🧵 42", got)
	}

	m = model{memoryStatus: "off"}
	if got := m.memoryStatusSegment(); got != "🧵 off" {
		t.Fatalf("got %q, want 🧵 off", got)
	}

	m = model{memoryStatus: ""}
	if got := m.memoryStatusSegment(); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestStatusLineMemoryActive(t *testing.T) {
	m := minimalStatusModel()
	m.memoryStatus = "active"
	m.memoryCount = 5
	out := m.statusLine(120)
	if !strings.Contains(out, "🧵 5") {
		t.Fatalf("status line missing 🧵 5: %s", out)
	}
}

func TestStatusLineMemoryOff(t *testing.T) {
	m := minimalStatusModel()
	m.memoryStatus = "off"
	out := m.statusLine(120)
	if !strings.Contains(out, "🧵 off") {
		t.Fatalf("status line missing 🧵 off: %s", out)
	}
}

func TestStatusLineMemoryUnknown(t *testing.T) {
	m := minimalStatusModel()
	m.memoryStatus = ""
	out := m.statusLine(120)
	if strings.Contains(out, "🧵") {
		t.Fatalf("status line should not contain 🧵 when unknown: %s", out)
	}
}

func TestStatusLineMemoryOmittedOnTiny(t *testing.T) {
	m := minimalStatusModel()
	m.memoryStatus = "active"
	m.memoryCount = 99
	out := m.statusLine(40)
	if strings.Contains(out, "🧵") {
		t.Fatalf("status line should omit 🧵 on tierTiny: %s", out)
	}
}

func TestMemorySidebarLinesActive(t *testing.T) {
	m := model{memoryStatus: "active", memoryCount: 42, memoryByType: map[string]int{"decision": 10, "test_command": 32}}
	lines := m.memorySidebarLines(30)
	if len(lines) == 0 {
		t.Fatal("expected lines for active memory")
	}
	if !strings.Contains(lines[0], "42 observations") {
		t.Fatalf("first line = %q, want 42 observations", lines[0])
	}
	if !strings.Contains(lines[1], "test_command") || !strings.Contains(lines[1], "32") {
		t.Fatalf("second line = %q, want test_command: 32", lines[1])
	}
}

func TestMemorySidebarLinesOff(t *testing.T) {
	m := model{memoryStatus: "off", memoryCount: 0}
	lines := m.memorySidebarLines(30)
	if lines != nil {
		t.Fatalf("expected nil for off memory, got %v", lines)
	}
}

func TestMemorySidebarLinesNoByType(t *testing.T) {
	m := model{memoryStatus: "active", memoryCount: 5, memoryByType: nil}
	lines := m.memorySidebarLines(30)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (count only), got %d", len(lines))
	}
	if !strings.Contains(lines[0], "5 observations") {
		t.Fatalf("line = %q, want 5 observations", lines[0])
	}
}

// minimalStatusModel builds a model with just enough state for statusLine to
// render without panicking.
func minimalStatusModel() model {
	m := model{}
	m.width = 120
	m.permissionMode = agent.PermissionModeAuto
	m.reasoningEffort = ""
	return m
}
