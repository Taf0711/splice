package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestFormatMemoryStats(t *testing.T) {
	stats := memd.MemoryStats{
		Total:       5,
		ByType:      map[string]int{"decision": 3, "test_command": 2},
		DBSizeBytes: 4096,
	}
	out := formatMemoryStats(stats)
	for _, want := range []string{"5", "decision", "3", "test_command", "2", "KB"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestFormatMemoryList(t *testing.T) {
	obs := []schemas.MemoryObservation{
		{Title: "First decision", Content: "Do this", MemoryType: "decision"},
		{Title: "Second note", Content: "Remember that", MemoryType: "note"},
	}
	out := formatMemoryList("🧵 Memory Search: \"query\" — 2 result(s)", "No memories found.", obs)
	for _, want := range []string{"query", "2 result", "First decision", "Second note", "decision", "note"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestFormatMemoryListEmpty(t *testing.T) {
	out := formatMemoryList("🧵 Memory Search: \"query\" — 0 result(s)", "No memories found.", nil)
	if !strings.Contains(out, "No memories found") {
		t.Fatalf("expected empty result text, got:\n%s", out)
	}
}

func TestFormatMemoryListRecent(t *testing.T) {
	obs := []schemas.MemoryObservation{
		{Title: "Alpha", Content: "One", MemoryType: "decision"},
		{Title: "Beta", Content: "Two", MemoryType: "test_command"},
		{Title: "Gamma", Content: "Three", MemoryType: "note"},
	}
	out := formatMemoryList("🧵 Recent Memories — 3", "No memories yet.", obs)
	if !strings.Contains(out, "3") {
		t.Fatalf("expected count 3 in output, got:\n%s", out)
	}
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		if !strings.Contains(out, title) {
			t.Fatalf("expected output to contain %q, got:\n%s", title, out)
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
