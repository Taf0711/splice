package tui

// P12 launch-screen probes (frame kAYHl, owner-revised): the empty
// transcript renders the information cockpit — wordmark, facts, resume
// card (when a resumable session exists), START, honest state — replacing
// the centered braid splash. Probes drive the real render path.

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// stripANSIStrings strips ANSI from a []string of rendered lines.
func stripANSIStrings(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = ansi.Strip(line)
	}
	return out
}

func launchTestModel(t *testing.T) model {
	t.Helper()
	m := newModel(context.Background(), Options{ProviderName: "anthropic", ModelName: "claude-sonnet-4.5"})
	m.width, m.height = 100, 30
	return m
}

// The launch body is the cockpit, not the braid: wordmark + tagline + facts
// + START, with no braid pixels and no stale example line.
func TestLaunchScreenIsCockpitNotSplash(t *testing.T) {
	m := launchTestModel(t)
	view := plainRender(t, m.View())
	assertContains(t, view, "splice | design mode")
	assertContains(t, view, emptyStateTagline)
	assertContains(t, view, "repo")
	assertContains(t, view, "not run this session")
	assertContains(t, view, "START")
	assertContains(t, view, "nothing has run in this session")
	// The braid half-block pixels are gone from the launch surface.
	assertNotContains(t, view, "▀")
}

// The START block carries the frame's pointers.
func TestLaunchScreenStartBlock(t *testing.T) {
	m := launchTestModel(t)
	view := plainRender(t, m.View())
	for _, want := range []string{"/resume", "/model", "/mcp", "/init", "describe a change"} {
		assertContains(t, view, want)
	}
}

// The /mcp action text adapts: with a degraded MCP server it says
// "reconnect the degraded server"; connected-only says "connect tools".
func TestLaunchMCPActionAdapts(t *testing.T) {
	m := launchTestModel(t)
	joined := strings.Join(stripANSIStrings(m.launchStart()), "\n")
	assertNotContains(t, joined, "reconnect the degraded server")
	assertContains(t, joined, "connect tools")

	degraded := launchTestModel(t)
	degraded.mcpViewStateCache = MCPViewState{Servers: []MCPServerView{{Name: "firecrawl", State: "degraded"}}}
	degraded.mcpViewStateReady = true
	joinedDegraded := strings.Join(stripANSIStrings(degraded.launchStart()), "\n")
	assertContains(t, joinedDegraded, "reconnect the degraded server")
}

// The facts block projects real in-tree counts: an empty registry renders
// the honest 0 · 0 sources, tests shows the honest default, and no invented
// test-run figure appears anywhere.
func TestLaunchFactsProjectRegistry(t *testing.T) {
	m := launchTestModel(t)
	found := map[string]string{}
	for _, f := range m.launchFacts() {
		found[f.key] = f.value
	}
	if got, ok := found["tools"]; !ok || !strings.Contains(got, "0 · 0 sources") {
		t.Fatalf("tools fact = %q (empty registry is honest)", got)
	}
	if got, ok := found["tests"]; !ok || got != "not run this session" {
		t.Fatalf("tests fact = %q, want the honest default", got)
	}
	for _, f := range m.launchFacts() {
		if strings.Contains(f.value, "last run: never") {
			t.Fatalf("facts invented a test-run figure: %+v", f)
		}
	}
}

// The resume card is honest: an isolated store with no resumable sessions →
// no LAST SESSION rows.
func TestLaunchResumeCardAbsentWithoutSessions(t *testing.T) {
	m := launchTestModel(t)
	m.sessionStore = testSessionStore(t) // isolated: zero sessions
	if card := m.launchResumeCard(); len(card) != 0 {
		t.Fatalf("empty store rendered a resume card: %v", card)
	}
	view := plainRender(t, m.View())
	assertNotContains(t, view, "LAST SESSION")
}
