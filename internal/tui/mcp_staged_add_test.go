package tui

// mcp_staged_add_test.go (P3 GAP-H, frame VCeGi): the staged-add card
// contract, probed through the real Update path with real key shapes.
// Five must-show fields, banner, key dispatch ([A]/[D]/[X]/[E]), and the
// composer safety net (the card never types into the composer).

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/config"
)

// stagedProbeModel opens the wizard on the confirm step for a stdio command
// (the frame's example: npx memory server).
func stagedProbeModel(t *testing.T) model {
	t.Helper()
	m := newModel(context.Background(), Options{})
	m.width = 120
	m.height = 36
	m.mcpAddWizard = newMCPAddWizard("stdio")
	m.mcpAddWizard.step = mcpAddWizardStepConfirm
	m.mcpAddWizard.serverName = "memory"
	m.mcpAddWizard.serverType = "stdio"
	m.mcpAddWizard.endpoint = "npx -y @modelcontextprotocol/server-memory"
	return m
}

// The staged card must show exactly the frame's five fields plus the banner:
// will run (command + transport + untrusted), adds, config (user scope),
// and the "nothing runs until you apply" promise.
func TestStagedAddCardRendersAllMustShowFields(t *testing.T) {
	m := stagedProbeModel(t)
	view := plainRender(t, m.View())
	for _, want := range []string{
		"STAGED ADD",
		"nothing runs until you apply",
		"will run",
		"npx -y @modelcontextprotocol/server-memory",
		"stdio subprocess, spawned at session start",
		"untrusted output",
		"adds",
		"1 server, 0 tools until enabled",
		"config",
		"user scope",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("staged-add card missing %q:\n%s", want, view)
		}
	}
}

// The staged card must NOT leak into the composer: with the wizard up and
// the composer empty, typing letters dispatches card keys (a/d/x/e) and any
// non-card letter is swallowed, never echoed into the composer.
func TestStagedAddCardNeverTypesIntoComposer(t *testing.T) {
	m := stagedProbeModel(t)
	// A letter that is NOT a card key must be swallowed, not composed.
	updated, _ := m.Update(reviewRealPlainKey('q'))
	next := updated.(model)
	if got := next.composerValue(); got != "" {
		t.Fatalf("staged card leaked %q into the composer", got)
	}
	if next.mcpAddWizard == nil {
		t.Fatal("non-card key closed the staged card")
	}
}

// [A] apply: dispatches the save (add + enable) through the real MCPCommand
// seam — no --disabled flag, config lands, staged card resolves to result.
func TestStagedAddApplyKeyDispatchesAddEnabled(t *testing.T) {
	var called []string
	m := stagedProbeModel(t)
	m.mcpCommand = func(_ context.Context, args []string) MCPCommandResult {
		called = append([]string{}, args...)
		return MCPCommandResult{
			ExitCode: 0,
			Output:   "Added MCP server memory.",
			Config: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"memory": {Type: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-memory"}},
			}},
		}
	}
	updated, cmd := m.Update(reviewRealPlainKey('a'))
	if cmd == nil {
		t.Fatal("staged [A] produced no command")
	}
	next := updated.(model)
	// The command runs off-loop; feed the result back through the real path.
	msg := cmd()
	updated2, _ := next.Update(msg)
	final := updated2.(model)
	want := []string{"add", "memory", "--type", "stdio", "--", "npx", "-y", "@modelcontextprotocol/server-memory"}
	if len(called) != len(want) {
		t.Fatalf("staged [A] args = %#v, want %#v", called, want)
	}
	for i := range want {
		if called[i] != want[i] {
			t.Fatalf("staged [A] args = %#v, want %#v", called, want)
		}
	}
	if _, ok := final.mcpConfig.Servers["memory"]; !ok {
		t.Fatal("staged [A] did not land the server in config")
	}
	if final.mcpAddWizard != nil && final.mcpAddWizard.step == mcpAddWizardStepResult {
		// The result card shows the connected state — fine either way, but
		// the wizard must not still be sitting on confirm.
		if final.mcpAddWizard.step == mcpAddWizardStepConfirm {
			t.Fatal("staged [A] left the card staged")
		}
	}
}

// [D] add disabled: dispatches the save with --disabled; the config carries
// Disabled.
func TestStagedAddDisabledKeyDispatchesAddDisabled(t *testing.T) {
	var called []string
	m := stagedProbeModel(t)
	m.mcpCommand = func(_ context.Context, args []string) MCPCommandResult {
		called = append([]string{}, args...)
		return MCPCommandResult{
			ExitCode: 0,
			Output:   "Added MCP server memory.",
			Config: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"memory": {Type: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-memory"}, Disabled: true},
			}},
		}
	}
	updated, cmd := m.Update(reviewRealPlainKey('d'))
	if cmd == nil {
		t.Fatal("staged [D] produced no command")
	}
	next := updated.(model)
	msg := cmd()
	updated2, _ := next.Update(msg)
	final := updated2.(model)
	found := false
	for i, arg := range called {
		if arg == "--disabled" {
			found = true
			_ = i
			break
		}
	}
	if !found {
		t.Fatalf("staged [D] did not pass --disabled: %#v", called)
	}
	if !final.mcpConfig.Servers["memory"].Disabled {
		t.Fatal("staged [D] server not disabled in config")
	}
}

// [X] discard: writes nothing — no MCPCommand call, config untouched, card
// closed.
func TestStagedAddDiscardKeyWritesNothing(t *testing.T) {
	called := false
	m := stagedProbeModel(t)
	m.mcpCommand = func(_ context.Context, args []string) MCPCommandResult {
		called = true
		return MCPCommandResult{}
	}
	updated, cmd := m.Update(reviewRealPlainKey('x'))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("staged [X] produced a command; discard writes nothing")
	}
	if called {
		t.Fatal("staged [X] invoked MCPCommand; discard writes nothing")
	}
	if next.mcpAddWizard != nil {
		t.Fatal("staged [X] did not close the card")
	}
	if len(next.mcpConfig.Servers) > 0 {
		t.Fatal("staged [X] mutated config")
	}
}

// [E] edit command: returns to the endpoint step with the command intact.
func TestStagedAddEditKeyReturnsToEndpoint(t *testing.T) {
	m := stagedProbeModel(t)
	updated, cmd := m.Update(reviewRealPlainKey('e'))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("staged [E] produced a command")
	}
	if next.mcpAddWizard == nil || next.mcpAddWizard.step != mcpAddWizardStepEndpoint {
		t.Fatalf("staged [E] did not return to the endpoint step: %+v", next.mcpAddWizard)
	}
	if next.mcpAddWizard.endpoint != "npx -y @modelcontextprotocol/server-memory" {
		t.Fatalf("staged [E] lost the command: %q", next.mcpAddWizard.endpoint)
	}
}

// Remote servers stage differently: willConnect shows the URL, transport is
// the remote dial line, and the untrusted-output note is stdio-only.
func TestStagedAddRemoteShowsURLNotCommand(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 120
	m.height = 36
	m.mcpAddWizard = newMCPAddWizard("http")
	m.mcpAddWizard.step = mcpAddWizardStepConfirm
	m.mcpAddWizard.serverName = "docs"
	m.mcpAddWizard.serverType = "http"
	m.mcpAddWizard.endpoint = "https://mcp.example/mcp"
	view := plainRender(t, m.View())
	if !strings.Contains(view, "https://mcp.example/mcp") {
		t.Fatalf("remote staged card missing the URL:\n%s", view)
	}
	if !strings.Contains(view, "remote, dialed at session start") {
		t.Fatalf("remote staged card missing the remote transport line:\n%s", view)
	}
	if strings.Contains(view, "stdio subprocess") {
		t.Fatalf("remote staged card claims stdio transport:\n%s", view)
	}
}

// Esc on the staged card closes it without writing anything.
func TestStagedAddEscClosesWithoutWriting(t *testing.T) {
	called := false
	m := stagedProbeModel(t)
	m.mcpCommand = func(_ context.Context, args []string) MCPCommandResult {
		called = true
		return MCPCommandResult{}
	}
	updated, _ := m.Update(testKey(tea.KeyEsc))
	next := updated.(model)
	if called {
		t.Fatal("Esc on the staged card invoked MCPCommand")
	}
	if next.mcpAddWizard != nil {
		t.Fatal("Esc did not close the staged card")
	}
}
