package tui

import (
	"context"
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

	m = model{memoryStatus: "unavailable"}
	if got := m.memoryStatusSegment(); got != "🧵 unavailable" {
		t.Fatalf("got %q, want 🧵 unavailable", got)
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

// TestMemoryTransitionNoticeIntoUnavailable: a transition from active into
// unavailable emits its own system row and must not reuse the off wording.
func TestMemoryTransitionNoticeIntoUnavailable(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.activeRunID = 1
	m.pending = true
	updated, _ := m.Update(agentResponseMsg{runID: 1, memoryStatus: "active", memoryCount: 3})
	next := updated.(model)

	next.activeRunID = 2
	next.pending = true
	updated2, _ := next.Update(agentResponseMsg{runID: 2, memoryStatus: "unavailable"})
	next2 := updated2.(model)

	text := transcriptText(next2.transcript)
	if !strings.Contains(text, "Memory retrieval failing; running without memory injection.") {
		t.Fatalf("transition into unavailable should emit its own row:\n%s", text)
	}
	if strings.Contains(text, "Memory sidecar unavailable; running without memory injection.") {
		t.Fatalf("unavailable must not reuse the off wording:\n%s", text)
	}
}

// TestMemoryTransitionNoticeIntoOffKeepsWording: a transition from active into
// off keeps the existing off wording.
func TestMemoryTransitionNoticeIntoOffKeepsWording(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.activeRunID = 1
	m.pending = true
	updated, _ := m.Update(agentResponseMsg{runID: 1, memoryStatus: "active", memoryCount: 3})
	next := updated.(model)

	next.activeRunID = 2
	next.pending = true
	updated2, _ := next.Update(agentResponseMsg{runID: 2, memoryStatus: "off"})
	next2 := updated2.(model)

	text := transcriptText(next2.transcript)
	if !strings.Contains(text, "Memory sidecar unavailable; running without memory injection.") {
		t.Fatalf("off transition keeps its wording:\n%s", text)
	}
}
