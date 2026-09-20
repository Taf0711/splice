package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// memoryCardText renders a memory card payload through the real styled-card
// path and strips ANSI, so assertions run against visible text.
func memoryCardText(t *testing.T, text string) string {
	t.Helper()
	payload, ok := commandCardTranscriptPayload(text)
	if !ok {
		t.Fatalf("memory result must carry the command-card prefix, got %q", text)
	}
	return plainRender(t, renderCommandCardRow(payload, 96))
}

// timeNowUnixDaysAgo returns a Unix timestamp N days before now, for fixtures.
func timeNowUnixDaysAgo(days int) int64 {
	return time.Now().Unix() - int64(days)*86400
}

func TestRenderMemoryStatsCardCarriesCounts(t *testing.T) {
	stats := memd.MemoryStats{
		Total:       5,
		ByType:      map[string]int{"decision": 3, "test_command": 2},
		DBSizeBytes: 4096,
	}
	out := memoryCardText(t, renderMemoryStatsCard(stats))
	// Header counts ride the summary line; the DB size is humanized.
	for _, want := range []string{"Memory", "5 observations", "4.0 KB", "decision", "3", "test_command", "2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats card missing %q, got:\n%s", want, out)
		}
	}
	// The emoji header is gone: P3 makes emoji a non-token, and the frame
	// calls out the old 🧵 header by name.
	if strings.Contains(out, "🧵") {
		t.Fatalf("stats card must not render the emoji header, got:\n%s", out)
	}
	// Footer pointers must appear on the card.
	for _, want := range []string{"/memory", "/memory recent", "/search <query>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats card missing footer pointer %q, got:\n%s", want, out)
		}
	}
}

func TestRenderMemoryListCardRendersTaggedTwoColumnRows(t *testing.T) {
	obs := []schemas.MemoryObservation{
		{Title: "First decision", Content: "Do this", MemoryType: "decision"},
		{Title: "Second note", Content: "Remember that", MemoryType: "note"},
	}
	out := memoryCardText(t, renderMemoryListCard("Search", "2 hits", obs))
	// Each observation is one "[type] title" row: no numbering, no emoji.
	for _, want := range []string{"[decision] First decision", "[note] Second note", "2 hits", "Do this", "Remember that"} {
		if !strings.Contains(out, want) {
			t.Fatalf("memory card missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "1. [") {
		t.Fatalf("memory card must drop the numbered list, got:\n%s", out)
	}
	if strings.Contains(out, "🧵") {
		t.Fatalf("memory card must not render the emoji header, got:\n%s", out)
	}
}

func TestRenderMemoryListCardTagsCarryTypeTokens(t *testing.T) {
	// Styled path: the [decision] tag renders amber, [finding] blue, other
	// types muted. Compare against the theme's token renders directly — a
	// regression here means the tag lost its semantic colour.
	obs := []schemas.MemoryObservation{
		{Title: "One", Content: "c", MemoryType: "decision"},
		{Title: "Two", Content: "c", MemoryType: "finding"},
		{Title: "Three", Content: "c", MemoryType: "note"},
	}
	payload, _ := commandCardTranscriptPayload(renderMemoryListCard("Search", "3 hits", obs))
	styled := renderCommandCardRow(payload, 96)
	for _, tag := range []string{"[decision]", "[finding]", "[note]"} {
		if !strings.Contains(stripANSI(styled), tag) {
			t.Fatalf("styled card missing tag %q, got:\n%s", tag, stripANSI(styled))
		}
	}
	for _, want := range []string{
		zeroTheme.amber.Render("[decision]"),
		zeroTheme.blue.Render("[finding]"),
		zeroTheme.muted.Render("[note]"),
	} {
		if !strings.Contains(styled, want) {
			t.Fatalf("tag token styling missing %q in:\n%s", want, stripANSI(styled))
		}
	}
}

func TestRenderMemoryListCardEmptyStateIsHonest(t *testing.T) {
	out := memoryCardText(t, renderMemoryListCard("Search", "0 hits", nil))
	if !strings.Contains(out, "0 hits") {
		t.Fatalf("empty card must carry the zero-hit count, got:\n%s", out)
	}
	if !strings.Contains(out, "no observations match") {
		t.Fatalf("empty card must render the honest empty line, got:\n%s", out)
	}
}

func TestRenderMemoryListCardDetailCarriesAge(t *testing.T) {
	// One line per observation: title row, then the content continuation with
	// the age suffix from the observation timestamp.
	obs := []schemas.MemoryObservation{
		{Title: "Retry policy", Content: "GET HEAD PUT DELETE, never POST", MemoryType: "decision", UpdatedAt: timeNowUnixDaysAgo(4)},
	}
	out := memoryCardText(t, renderMemoryListCard("Search", "1 hit", obs))
	if !strings.Contains(out, "[decision] Retry policy") {
		t.Fatalf("expected tagged title row, got:\n%s", out)
	}
	if !strings.Contains(out, "4d ago") {
		t.Fatalf("expected age suffix on the detail line, got:\n%s", out)
	}
}

func TestMemoryObservationAgeWindows(t *testing.T) {
	cases := []struct {
		name string
		days int
		want string
	}{
		{"today", 0, "just now"},
		{"recent", 4, "4d ago"},
		{"months", 90, "3mo ago"},
		{"years", 800, "2y ago"},
	}
	for _, tc := range cases {
		obs := schemas.MemoryObservation{UpdatedAt: timeNowUnixDaysAgo(tc.days)}
		if got := memoryObservationAge(obs); got != tc.want {
			t.Fatalf("%s: memoryObservationAge = %q, want %q", tc.name, got, tc.want)
		}
	}
	// No timestamp: no dangling separator.
	if got := memoryObservationAge(schemas.MemoryObservation{}); got != "" {
		t.Fatalf("ageless observation must render no age, got %q", got)
	}
}

func TestMemoryListSummaryCarriesQueryAndTypes(t *testing.T) {
	got := memoryListSummary(3, "retry", map[string]int{"decision": 2, "finding": 1})
	for _, want := range []string{"3 hits", `query "retry"`, "decision 2", "finding 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q, got %q", want, got)
		}
	}
}

func TestHandleMemoryCommandParsesSubcommands(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, testSessionStore(t))

	nm, _ := m.handleMemoryCommand("")
	if !transcriptContains(nm.transcript, "/memory") {
		t.Fatalf("expected stats prompt in transcript, got %#v", nm.transcript)
	}

	nm, _ = m.handleMemoryCommand("search foo")
	if !transcriptContains(nm.transcript, "/memory search foo") {
		t.Fatalf("expected search prompt in transcript, got %#v", nm.transcript)
	}

	nm, _ = m.handleMemoryCommand("recent")
	if !transcriptContains(nm.transcript, "/memory recent") {
		t.Fatalf("expected recent prompt in transcript, got %#v", nm.transcript)
	}

	nm, _ = m.handleMemoryCommand("invalid")
	if !transcriptContains(nm.transcript, "Usage:") {
		t.Fatalf("expected usage error in transcript, got %#v", nm.transcript)
	}
}

type capturedMemoryClient struct {
	searchQuery *schemas.MemoryQuery
	recentQuery *schemas.MemoryQuery
	recentErr   error
}

func (c *capturedMemoryClient) Search(_ context.Context, query schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	c.searchQuery = &query
	return schemas.MemoryBundle{RequestingAgent: query.RequestingAgent}, nil
}

func (c *capturedMemoryClient) Recent(_ context.Context, query schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	c.recentQuery = &query
	return schemas.MemoryBundle{RequestingAgent: query.RequestingAgent}, c.recentErr
}

func (c *capturedMemoryClient) Stats(context.Context) (memd.MemoryStats, error) {
	return memd.MemoryStats{}, nil
}

// Regression for /memory search returning zero results when the TUI omitted
// scopes and the project path from its MemoryQuery.
func TestHandleMemorySearchSendsScopesAndProjectPath(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	client := &capturedMemoryClient{}
	previous := tuiResolveMemoryCommand
	tuiResolveMemoryCommand = func(context.Context) (memoryCommandClient, error) { return client, nil }
	t.Cleanup(func() { tuiResolveMemoryCommand = previous })

	m := model{ctx: context.Background(), cwd: workspace}
	_, cmd := m.handleMemorySearch("needle")
	msg, ok := cmd().(memoryResultMsg)
	if !ok || msg.isError {
		t.Fatalf("unexpected command result: %#v", msg)
	}
	if len(client.searchQuery.Scopes) == 0 {
		t.Fatal("scopes is empty")
	}
	if client.searchQuery.ProjectPath == nil || *client.searchQuery.ProjectPath != workspace {
		t.Fatalf("project path = %#v, want %q", client.searchQuery.ProjectPath, workspace)
	}
}

func TestHandleMemoryRecentSendsListingQuery(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	client := &capturedMemoryClient{}
	previous := tuiResolveMemoryCommand
	tuiResolveMemoryCommand = func(context.Context) (memoryCommandClient, error) { return client, nil }
	t.Cleanup(func() { tuiResolveMemoryCommand = previous })

	m := model{ctx: context.Background(), cwd: workspace}
	_, cmd := m.handleMemoryRecent()
	msg, ok := cmd().(memoryResultMsg)
	if !ok || msg.isError {
		t.Fatalf("unexpected command result: %#v", msg)
	}
	if client.recentQuery.Query == "*" {
		t.Fatal("recent must not send the FTS match-all query \"*\"")
	}
	if len(client.recentQuery.Scopes) == 0 {
		t.Fatal("scopes is empty")
	}
	if client.recentQuery.ProjectPath == nil || *client.recentQuery.ProjectPath != workspace {
		t.Fatalf("project path = %#v, want %q", client.recentQuery.ProjectPath, workspace)
	}
}

func TestHandleMemoryRecentDegradesForOldDaemon(t *testing.T) {
	client := &capturedMemoryClient{recentErr: fmt.Errorf("%w: update splice-memd", memd.ErrRecentUnsupported)}
	previous := tuiResolveMemoryCommand
	tuiResolveMemoryCommand = func(context.Context) (memoryCommandClient, error) { return client, nil }
	t.Cleanup(func() { tuiResolveMemoryCommand = previous })

	m := model{ctx: context.Background(), cwd: t.TempDir()}
	_, cmd := m.handleMemoryRecent()
	msg, ok := cmd().(memoryResultMsg)
	if !ok {
		t.Fatalf("unexpected command message type: %#v", cmd())
	}
	if !msg.isError || !strings.Contains(msg.text, "out of date") {
		t.Fatalf("expected out-of-date degradation, got %#v", msg)
	}
	if strings.Contains(msg.text, "unexpected status") || strings.Contains(msg.text, "404") {
		t.Fatalf("degradation exposed raw HTTP error: %q", msg.text)
	}
}

func TestHumanizeBytes(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{512, "B"},
		{4096, "KB"},
		{1 << 20, "MB"},
	}
	for _, tc := range cases {
		got := humanizeBytes(tc.bytes)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("humanizeBytes(%d) = %q, want containing %q", tc.bytes, got, tc.want)
		}
	}
}
