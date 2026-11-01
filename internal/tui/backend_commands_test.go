package tui

import (
	"context"
	"strings"
	"testing"
)

// A backend command writes one row: "running", then its card in place.
// Re-running replaces the card instead of stacking copies.
func TestBackendCommandReplacesItsRunningRow(t *testing.T) {
	m := limeTestModel()
	m.trusted = true
	var gotArgs []string
	m.backendCommand = func(_ context.Context, args []string) BackendCommandResult {
		gotArgs = args
		return BackendCommandResult{Output: "ID       EVENT\nfmt      post_edit"}
	}
	for run := 0; run < 2; run++ {
		next, cmd := m.startBackendCommand(commandHooks, "")
		if cmd == nil {
			t.Fatal("no command scheduled")
		}
		msg := cmd().(backendCommandResultMsg)
		updated, _ := next.Update(msg)
		m = updated.(model)
	}
	if strings.Join(gotArgs, " ") != "hooks list" {
		t.Fatalf("args = %q", gotArgs)
	}
	count := 0
	for _, row := range m.transcript {
		if row.id == "backend/hooks" {
			count++
			if _, _, body, ok := parseBackendCard(row.text); !ok || !strings.Contains(body, "fmt      post_edit") {
				t.Fatalf("row is not the result card with aligned text: %q", row.text)
			}
		}
	}
	if count != 1 {
		t.Fatalf("backend rows = %d, want 1", count)
	}
	rendered := stripANSI(renderBackendCard(parseBackendCardOrFail(t, m)))
	if !strings.Contains(rendered, "fmt      post_edit") {
		t.Fatalf("card reflowed the CLI columns:\n%s", rendered)
	}
}

func parseBackendCardOrFail(t *testing.T, m model) (string, string, string, int) {
	t.Helper()
	for _, row := range m.transcript {
		if title, status, body, ok := parseBackendCard(row.text); ok {
			return title, status, body, 80
		}
	}
	t.Fatal("no backend card")
	return "", "", "", 0
}

// /verify runs the project's own commands, so an untrusted workspace is
// refused before anything runs.
func TestVerifyRefusedInUntrustedWorkspace(t *testing.T) {
	m := limeTestModel()
	m.trusted = false
	called := false
	m.backendCommand = func(context.Context, []string) BackendCommandResult {
		called = true
		return BackendCommandResult{}
	}
	next, cmd := m.startBackendCommand(commandVerify, "")
	if cmd != nil || called {
		t.Fatal("verify ran in an untrusted workspace")
	}
	last := next.transcript[len(next.transcript)-1]
	if _, status, body, ok := parseBackendCard(last.text); !ok || status != "blocked" || !strings.Contains(body, "/trust") {
		t.Fatalf("refusal card = %q", last.text)
	}
}

func TestBlockedBackendCardHeadlineSaysNotRun(t *testing.T) {
	plain := ansiStrip(renderBackendCard("verify", "blocked", "trust the workspace first", 80))
	if !strings.Contains(plain, "verify not run") {
		t.Fatalf("blocked card headline should say not run, got:\n%s", plain)
	}
	if strings.Contains(plain, "failed") {
		t.Fatalf("blocked card must not claim failure, got:\n%s", plain)
	}
}

// Secrets in CLI output (a hook command with a token) are redacted.
func TestBackendCardRedacts(t *testing.T) {
	card := backendCard("hooks", BackendCommandResult{Output: "curl -H 'Authorization: Bearer sk-live-abcdefghijklmnopqrstuvwxyz0123456789'"})
	if strings.Contains(card, "sk-live-abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatalf("secret leaked into the card: %q", card)
	}
}
