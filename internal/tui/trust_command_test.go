package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
)

func TestTrustCommandPersistsForNextRun(t *testing.T) {
	workspace := t.TempDir()
	storePath := t.TempDir() + "/trust.json"
	store, err := config.LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}

	m := newModel(context.Background(), Options{
		Cwd:        workspace,
		Trusted:    false,
		TrustStore: store,
	})
	m.designMode = false
	m.input.SetValue("/trust")
	updated, _ := m.handleSubmit()
	next := updated.(model)

	reloaded, err := config.LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("LoadTrustStore() after /trust error = %v", err)
	}
	if got := reloaded.IsTrusted(workspace); got != config.TrustTrusted {
		t.Fatalf("trust decision after /trust = %v, want trusted", got)
	}
	message := transcriptText(next.transcript)
	if !strings.Contains(message, "Restart Splice for the change to take effect") {
		t.Fatalf("/trust message = %q, want restart wording", message)
	}
	if !strings.Contains(message, "stays untrusted") {
		t.Fatalf("/trust message = %q, want current session honesty", message)
	}
	if strings.Contains(strings.ToLower(message), "now trusted") || strings.Contains(strings.ToLower(message), "access gained") {
		t.Fatalf("/trust message claims current session gained access: %q", message)
	}
}

func TestTrustCommandReportsAlreadyTrusted(t *testing.T) {
	workspace := t.TempDir()
	storePath := t.TempDir() + "/trust.json"
	store, err := config.LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("LoadTrustStore() error = %v", err)
	}
	if err := store.SetTrusted(workspace, true); err != nil {
		t.Fatalf("SetTrusted() error = %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	m := newModel(context.Background(), Options{Cwd: workspace, Trusted: true, TrustStore: store})
	m.designMode = false
	m.input.SetValue("/trust")
	updated, _ := m.handleSubmit()
	message := transcriptText(updated.(model).transcript)
	if !strings.Contains(message, "Workspace is already trusted. No changes made.") {
		t.Fatalf("/trust message = %q, want already-trusted report", message)
	}
}

func TestStatusLineShowsTrustIndicatorOnlyWhenUntrusted(t *testing.T) {
	untrusted := newModel(context.Background(), Options{Trusted: false})
	untrusted.designMode = false
	if got := plainRender(t, untrusted.statusLine(120)); !strings.Contains(got, "! untrusted") {
		t.Fatalf("untrusted status line = %q, want trust indicator", got)
	}

	trusted := newModel(context.Background(), Options{Trusted: true})
	trusted.designMode = false
	if got := plainRender(t, trusted.statusLine(120)); strings.Contains(got, "untrusted") {
		t.Fatalf("trusted status line = %q, must not show trust indicator", got)
	}
}
