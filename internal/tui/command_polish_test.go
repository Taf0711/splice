package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/tools"
)

func TestHelpCommandRendersGroupedSections(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/help")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /help to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{
		"Commands",
		"Model",
		"Session",
		"Runtime",
		"Tools",
		"Meta",
		"  /model [list|id]",
		"  /permissions",
		"hint: descriptions live in the palette. Type / for command details.",
	} {
		assertContains(t, text, want)
	}
	assertNotContains(t, text, "Commands:\n/provider")
}

func TestProviderAndConfigCommandsUseStableStatusOutput(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			APIKey:       "sk-sensitive",
			Model:        "gpt-4.1",
		},
		AgentOptions:  agent.Options{MaxTurns: 42},
		RecapsEnabled: true,
	})

	m.input.SetValue("/provider status")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /provider to be handled without starting an agent run")
	}
	providerText := transcriptText(next.transcript)
	for _, want := range []string{"Provider", "status: ok", "provider: openai", "model: gpt-4.1", "api key: set"} {
		assertContains(t, providerText, want)
	}
	assertNotContains(t, providerText, "sk-sensitive")

	next.input.SetValue("/config")
	updated, cmd = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected /config to be handled without starting an agent run")
	}
	configText := transcriptText(next.transcript)
	for _, want := range []string{"Config", "status: ok", "runtime: go", "max turns: 42", "permission mode:", "recaps: on"} {
		assertContains(t, configText, want)
	}
	assertNotContains(t, configText, "sk-sensitive")
}

func TestProviderCommandRedactsCredentialBearingBaseURL(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      "https://user:super-secret@proxy.local/v1?api_key=query-secret",
			APIKey:       "query-secret",
			Model:        "gpt-4.1",
		},
	})
	m.input.SetValue("/provider status")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /provider to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, unwanted := range []string{"super-secret", "query-secret", "user:super-secret@"} {
		assertNotContains(t, text, unwanted)
	}
	assertContains(t, text, "base url: https://proxy.local/v1?api_key=[REDACTED]")
}

func TestToolsCommandRendersCommandCard(t *testing.T) {
	m := newModel(context.Background(), Options{
		Registry: tools.NewRegistry(),
	})
	m.input.SetValue("/tools")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /tools to be handled without starting an agent run")
	}
	emptyText := transcriptText(next.transcript)
	for _, want := range []string{
		"Tools",
		"0 registered | no tools available",
		"Registry",
		"registered  0",
		"actions: /mcp manage servers | /permissions manage access",
	} {
		assertContains(t, emptyText, want)
	}
	assertNotContains(t, emptyText, "status: warning")
	assertNotContains(t, emptyText, "registered tools:")

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool("."))
	m = newModel(context.Background(), Options{
		Registry: registry,
	})
	m.input.SetValue("/tools")

	updated, cmd = m.Update(testKey(tea.KeyEnter))
	next = updated.(model)

	if cmd != nil {
		t.Fatal("expected /tools to be handled without starting an agent run")
	}
	toolsText := transcriptText(next.transcript)
	for _, want := range []string{
		"Tools",
		"1 registered",
		"BUILTIN",
		"read_file",
		"hint: every tool here is gated by /permissions — registration is not access",
		"actions: /mcp manage servers | /permissions manage access",
	} {
		assertContains(t, toolsText, want)
	}
	assertNotContains(t, toolsText, "status: ok")
	assertNotContains(t, toolsText, "registered tools:")
}

func TestToolsCommandCardHandlesNilRegistry(t *testing.T) {
	text := model{}.toolsText()

	for _, want := range []string{
		"Tools",
		"0 registered | no tools available",
		"registered  0",
	} {
		assertContains(t, text, want)
	}
}

func TestToolsCommandShowsFullSortedCatalog(t *testing.T) {
	registry := tools.NewRegistry()
	for _, name := range []string{
		"write_file",
		"read_file",
		"grep",
		"glob",
		"edit_file",
		"apply_patch",
		"bash",
		"web_search",
		"web_fetch",
	} {
		registry.Register(commandTestMCPTool{name: name, description: name + " tool"})
	}

	m := newModel(context.Background(), Options{
		Registry: registry,
	})
	m.input.SetValue("/tools")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /tools to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	assertContains(t, text, "9 registered")

	// Every name renders exactly once, in the BUILTIN group, alpha-sorted,
	// four per wrapped line (no truncation, no "... N more").
	previous := -1
	for _, want := range []string{
		"apply_patch",
		"bash",
		"edit_file",
		"glob",
		"grep",
		"read_file",
		"web_fetch",
		"web_search",
		"write_file",
	} {
		current := strings.Index(text, want)
		if current < 0 {
			t.Fatalf("expected tools output to contain %q, got:\n%s", want, text)
		}
		if current <= previous {
			t.Fatalf("expected tools output to keep sorted order at %q, got:\n%s", want, text)
		}
		previous = current
	}
	for _, unwanted := range []string{"... 1 more", "Available"} {
		assertNotContains(t, text, unwanted)
	}
	builtinIndex := strings.Index(text, "\nBUILTIN\n")
	if builtinIndex < 0 {
		t.Fatalf("expected a BUILTIN group header, got:\n%s", text)
	}
	if strings.Count(text, "BUILTIN") != 1 {
		t.Fatalf("expected exactly one BUILTIN group, got:\n%s", text)
	}
}

func TestToolsCommandGroupsMCPToolsUnderServerName(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"github": {Type: "http", URL: "https://github.example/mcp"},
	}}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool("."))
	registry.Register(commandTestMCPTool{name: "mcp_github_create_pr", serverName: "github", description: "Create a PR"})
	registry.Register(commandTestMCPTool{name: "mcp_github_list_issues", serverName: "github", description: "List issues"})
	registry.Register(commandTestMCPTool{name: "mcp_orphan_ping", description: "No owning server"})

	m := newModel(context.Background(), Options{
		Registry:  registry,
		MCPConfig: cfg,
	})
	m.input.SetValue("/tools")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /tools to be handled without starting an agent run")
	}
	if got := countTranscriptRows(next.transcript, rowSystem); got != 1 {
		t.Fatalf("expected /tools to render as a single command card, got %d system rows: %#v", got, next.transcript)
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{
		"4 registered · 3 sources",
		"BUILTIN",
		"github",
		"connected",
		"mcp_github_create_pr mcp_github_list_issues",
		"(unknown source)",
		"mcp_orphan_ping",
		"hint: every tool here is gated by /permissions — registration is not access",
		"actions: /mcp manage servers | /permissions manage access",
	} {
		assertContains(t, text, want)
	}
	// The card's NUL routing tag is a transcript transport marker: renderRow
	// consumes it, so the marker must never survive into rendered body text.
	if !strings.Contains(text, commandCardTranscriptPrefix) {
		t.Fatalf("expected a tagged command card in the transcript, got:\n%s", text)
	}
}

func TestToolsCommandMarksDegradedServerWithHiddenTools(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"postgres": {Type: "stdio", Command: "pg-mcp"},
	}}
	registry := tools.NewRegistry()
	registry.Register(commandTestMCPTool{name: "mcp_postgres_query", serverName: "postgres", description: "Query"})
	registry.Register(commandTestMCPTool{
		name:        "mcp_postgres_explain",
		serverName:  "postgres",
		description: "Explain",
		safety:      tools.Safety{SideEffect: tools.SideEffectNetwork, Permission: tools.PermissionPrompt},
	})
	// Make the fake deferred-eligible: agent deferral hides eligible tools once
	// the eligible count reaches DeferThreshold (1).
	registry.Register(deferralEligibleTestTool{commandTestMCPTool{
		name:        "mcp_postgres_analyze",
		serverName:  "postgres",
		description: "Analyze",
	}})

	m := newModel(context.Background(), Options{
		Registry:  registry,
		MCPConfig: cfg,
		AgentOptions: agent.Options{
			DeferThreshold: 1,
		},
	})
	m.input.SetValue("/tools")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	text := transcriptText(next.transcript)
	assertContains(t, text, "3 registered")
	assertContains(t, text, "postgres")
	assertContains(t, text, "degraded — 1 tools hidden")
	assertContains(t, text, "mcp_postgres_explain mcp_postgres_query")
	assertNotContains(t, text, "mcp_postgres_analyze")
}

// deferralEligibleTestTool marks a test MCP tool as deferral-eligible so the
// /tools card exercises the hidden-tool ("degraded") path.
type deferralEligibleTestTool struct {
	commandTestMCPTool
}

func (tool deferralEligibleTestTool) Deferred() bool {
	return true
}

// toolVisibility
func TestToolsCommandHiddenToolsStayRegisteredButUngrouped(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"solo": {Type: "stdio", Command: "solo-mcp"},
	}}
	registry := tools.NewRegistry()
	registry.Register(deferralEligibleTestTool{commandTestMCPTool{
		name:        "mcp_solo_only",
		serverName:  "solo",
		description: "The only tool",
	}})
	registry.Register(deferralEligibleTestTool{commandTestMCPTool{
		name:        "mcp_solo_extra",
		serverName:  "solo",
		description: "The second tool",
	}})

	m := newModel(context.Background(), Options{
		Registry:     registry,
		MCPConfig:    cfg,
		AgentOptions: agent.Options{DeferThreshold: 1},
	})
	m.input.SetValue("/tools")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	text := transcriptText(next.transcript)
	// Both tools stay registered in the count; both are hidden behind
	// tool_search; the server group still renders with the hidden count.
	assertContains(t, text, "2 registered")
	assertContains(t, text, "solo")
	assertContains(t, text, "degraded — 2 tools hidden")
	assertNotContains(t, text, "mcp_solo_only")
	assertNotContains(t, text, "mcp_solo_extra")
}

func TestContextAndPermissionsCommandsRenderProductState(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool("."))

	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	if _, err := store.Grant(sandbox.GrantInput{
		ToolName: "bash",
		Decision: sandbox.GrantAllow,
		Reason:   "sk-proj-sensitive approved shell",
	}); err != nil {
		t.Fatalf("Grant returned error: %v", err)
	}

	m := newModel(context.Background(), Options{
		Cwd:            `D:\codings\Opensource\Splice`,
		ProviderName:   "openai",
		ModelName:      "gpt-4.1",
		Registry:       registry,
		SandboxStore:   store,
		PermissionMode: agent.PermissionModeAsk,
	})

	m.input.SetValue("/context")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /context to be handled without starting an agent run")
	}
	contextText := transcriptText(next.transcript)
	for _, want := range []string{
		"Context",
		"go runtime | ask permissions | 1 tool",
		"Runtime",
		"cwd        D:\\codings\\Opensource\\Splice",
		"provider   openai",
		"model      gpt-4.1",
		"Session",
		"active      none",
		"compaction  idle",
		"Tools",
		"registered  1",
		"actions: /permissions manage access | /tools inspect catalog",
	} {
		assertContains(t, contextText, want)
	}
	assertNotContains(t, contextText, "status: ok")
	assertNotContains(t, contextText, "permission mode:")

	next.input.SetValue("/permissions")
	updated, cmd = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	if cmd != nil {
		t.Fatal("expected /permissions to be handled without starting an agent run")
	}
	permissionText := transcriptText(next.transcript)
	for _, want := range []string{
		"Permissions",
		"ask permissions",
		"1 persistent grant",
		"State",
		"mode  ask",
		"Grants",
		"bash [allow]",
		"[REDACTED]",
	} {
		assertContains(t, permissionText, want)
	}
	assertNotContains(t, permissionText, "sk-proj-sensitive")
	assertNotContains(t, permissionText, "status: ok")
	assertNotContains(t, permissionText, "Permission mode:")
}

// stubGrantStore is a minimal test double for sandbox.GrantStore. It only
// implements List, which is enough to exercise the error path of
// permissionsText().
type stubGrantStore struct {
	listErr error
}

func (store *stubGrantStore) List() ([]sandbox.Grant, error) {
	return nil, store.listErr
}

func TestPermissionsCommandCardHandlesNilStoreAndEmptyGrants(t *testing.T) {
	nilStore := model{permissionMode: agent.PermissionModeAuto}.permissionsText()
	for _, want := range []string{
		"Permissions",
		"auto permissions",
		"grants unavailable",
		"mode  auto",
		"persistent grants: unavailable",
	} {
		assertContains(t, nilStore, want)
	}
	assertNotContains(t, nilStore, "status: warning")

	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "empty-grants.json")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	emptyText := model{permissionMode: agent.PermissionModeAsk, sandboxStore: store}.permissionsText()
	for _, want := range []string{
		"Permissions",
		"ask permissions",
		"no persistent grants",
		"mode  ask",
		"none",
	} {
		assertContains(t, emptyText, want)
	}
	assertNotContains(t, emptyText, "status: info")

	errStore := &stubGrantStore{listErr: errors.New("storage failure")}
	errText := model{permissionMode: agent.PermissionModeAsk}.permissionsTextWithStore(errStore)
	for _, want := range []string{
		"Permissions",
		"ask permissions",
		"grants error",
		"mode  ask",
		"error: storage failure",
	} {
		assertContains(t, errText, want)
	}
	assertNotContains(t, errText, "status: blocked")
}

func TestContextCommandCardHandlesNilRegistryAndStableStyle(t *testing.T) {
	text := model{}.contextText()

	for _, want := range []string{
		"Context",
		"0 tools",
		"style      concise",
		"root        unknown",
	} {
		assertContains(t, text, want)
	}
}

func TestCompactCommandAvoidsShellOnlyPlaceholder(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/compact")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /compact to be handled without starting an agent run")
	}
	text := transcriptText(next.transcript)
	for _, want := range []string{"Compact", "status: warning", "requested: yes", "visible transcript rows:"} {
		assertContains(t, text, want)
	}
	if strings.Contains(text, "not wired") || strings.Contains(text, "future compaction backend") {
		t.Fatalf("compact output should describe product state, got %q", text)
	}
}
