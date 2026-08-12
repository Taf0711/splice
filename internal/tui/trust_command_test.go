package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/sessions"
)

// selectTrustItem highlights the picker row whose Value matches and returns the
// model ready to be chosen via choosePicker.
func selectTrustItem(t *testing.T, m model, value string) model {
	t.Helper()
	if m.picker == nil || m.picker.kind != pickerTrust {
		t.Fatalf("trust picker not open: %#v", m.picker)
	}
	for i, it := range m.picker.items {
		if it.Value == value {
			m.picker.selected = i
			return m
		}
	}
	t.Fatalf("trust picker has no item with value %q: %#v", value, m.picker.items)
	return m
}

func newTrustStore(t *testing.T) (*config.TrustStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trust.json")
	store, err := config.LoadTrustStore(path)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	return store, path
}

func TestTrustPickerMenuShowsStateAndActions(t *testing.T) {
	workspace := t.TempDir()
	m := newModel(context.Background(), Options{Cwd: workspace, Trusted: false})
	m.designMode = false
	m.input.SetValue("/trust")
	updated, _ := m.handleSubmit()
	next := updated.(model)
	if next.picker == nil || next.picker.kind != pickerTrust {
		t.Fatalf("/trust did not open trust menu: %#v", next.picker)
	}
	got := plainRender(t, next.pickerOverlay(100))
	for _, want := range []string{"Trust this folder", "Trust parent folder", "Do not trust this folder", "untrusted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("trust menu missing %q: %s", want, got)
		}
	}
}

// The first-run menu, opened while the workspace trust is undecided, must not
// be dismissible with Esc: the user is required to pick trust, parent trust, or
// decline before the TUI proceeds to launch.
func TestStartupTrustPromptIsRequiredAndNonDismissible(t *testing.T) {
	workspace := t.TempDir()
	store, _ := newTrustStore(t)
	m := newModel(context.Background(), Options{Cwd: workspace, Trusted: false, TrustStore: store, TrustPrompt: true})
	m.designMode = false
	next := m.openTrustPromptIfRequired()
	if next.picker == nil || next.picker.kind != pickerTrust {
		t.Fatalf("startup did not open required trust menu: %#v", next.picker)
	}
	afterEsc, _ := next.Update(testKey(tea.KeyEsc))
	if afterEsc.(model).picker == nil {
		t.Fatal("Esc dismissed the required first-run trust menu")
	}
	afterCtrlC, _ := afterEsc.(model).Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if afterCtrlC.(model).picker == nil {
		t.Fatal("Ctrl+C dismissed the required first-run trust menu")
	}
	if item, ok := afterCtrlC.(model).picker.current(); !ok || item.Value != trustActionDecline {
		t.Fatalf("default trust choice = %#v, want decline", item)
	}
}

// After a first-run choice the TUI must continue normal launch, including the
// session-resume picker.
func TestStartupTrustChoiceContinuesToLaunchSessionPicker(t *testing.T) {
	workspace := t.TempDir()
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "Workspace plan", Cwd: workspace})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	appendPickerPlan(t, store, session.SessionID, "workspace-plan")
	trustStore, _ := newTrustStore(t)

	m := newModel(context.Background(), Options{
		Cwd:          workspace,
		SessionStore: store,
		TrustStore:   trustStore,
		Trusted:      false,
		TrustPrompt:  true,
	})
	m.designMode = false
	next := m.openTrustPromptIfRequired()
	next = selectTrustItem(t, next, trustActionCurrent)
	chosen, _ := next.choosePicker()
	got := chosen.(model)
	if got.picker == nil || got.picker.kind != pickerSession {
		t.Fatalf("trust choice did not continue to launch session picker: %#v", got.picker)
	}
	if got.trustPromptRequired {
		t.Fatal("trustPromptRequired not cleared after first-run choice")
	}
	if !got.trusted {
		t.Fatal("trusted not set after trusting current folder")
	}
}

func TestNewModelCopiesWorkspaceTrustToAgentOptions(t *testing.T) {
	m := newModel(context.Background(), Options{Trusted: true, AgentOptions: agent.Options{}})
	if !m.agentOptions.TrustedWorkspace {
		t.Fatal("trusted TUI did not enable trusted workspace permissions")
	}
}

func TestTrustCurrentPersistsAndSetsImmediateTrusted(t *testing.T) {
	workspace := t.TempDir()
	store, storePath := newTrustStore(t)
	m := newModel(context.Background(), Options{Cwd: workspace, Trusted: false, TrustStore: store})
	m.designMode = false
	m.input.SetValue("/trust")
	updated, _ := m.handleSubmit()
	next := updated.(model)
	next = selectTrustItem(t, next, trustActionCurrent)
	chosen, _ := next.choosePicker()
	got := chosen.(model)
	if !got.trusted || !got.agentOptions.TrustedWorkspace {
		t.Fatal("trusted permission state not set immediately after trusting current")
	}
	reloaded, err := config.LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("reload trust store: %v", err)
	}
	if d := reloaded.IsTrusted(workspace); d != config.TrustTrusted {
		t.Fatalf("stored decision = %v, want trusted", d)
	}
}

func TestTrustParentPersists(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "child")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	store, storePath := newTrustStore(t)
	if err := store.SetTrusted(workspace, false); err != nil {
		t.Fatalf("record prior decline: %v", err)
	}
	m := newModel(context.Background(), Options{Cwd: workspace, Trusted: false, TrustStore: store})
	m.designMode = false
	m.input.SetValue("/trust")
	updated, _ := m.handleSubmit()
	next := updated.(model)
	next = selectTrustItem(t, next, trustActionParent)
	chosen, _ := next.choosePicker()
	got := chosen.(model)
	if !got.trusted {
		t.Fatal("trusted not set after trusting parent folder")
	}
	reloaded, err := config.LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("reload trust store: %v", err)
	}
	if d := reloaded.IsTrusted(workspace); d != config.TrustTrusted {
		t.Fatalf("child decision via parent = %v, want trusted", d)
	}
	if d := reloaded.IsTrusted(parent); d != config.TrustTrusted {
		t.Fatalf("parent decision = %v, want trusted", d)
	}
}

func TestTrustDeclinePersistsUntrusted(t *testing.T) {
	workspace := t.TempDir()
	store, storePath := newTrustStore(t)
	m := newModel(context.Background(), Options{Cwd: workspace, Trusted: false, TrustStore: store})
	m.designMode = false
	m.input.SetValue("/trust")
	updated, _ := m.handleSubmit()
	next := updated.(model)
	next = selectTrustItem(t, next, trustActionDecline)
	chosen, _ := next.choosePicker()
	got := chosen.(model)
	if got.trusted || got.agentOptions.TrustedWorkspace {
		t.Fatal("trusted permission state stayed true after decline")
	}
	reloaded, err := config.LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("reload trust store: %v", err)
	}
	if d := reloaded.IsTrusted(workspace); d != config.TrustDeclined {
		t.Fatalf("stored decision = %v, want declined", d)
	}
}

// A later /trust invocation is cancellable: Esc dismisses the menu without a
// change and without persisting anything.
func TestTrustMenuLaterInvocationIsCancellable(t *testing.T) {
	workspace := t.TempDir()
	store, storePath := newTrustStore(t)
	m := newModel(context.Background(), Options{Cwd: workspace, Trusted: false, TrustStore: store})
	m.designMode = false
	m.input.SetValue("/trust")
	updated, _ := m.handleSubmit()
	next := updated.(model)
	afterEsc, _ := next.Update(testKey(tea.KeyEsc))
	got := afterEsc.(model)
	if got.picker != nil {
		t.Fatalf("Esc did not cancel the trust menu: %#v", got.picker)
	}
	reloaded, err := config.LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("reload trust store: %v", err)
	}
	if d := reloaded.IsTrusted(workspace); d != config.TrustUndecided {
		t.Fatalf("cancel wrote a decision = %v, want undecided", d)
	}
}

// A persistence failure keeps the active decision unchanged.
func TestTrustPickerPersistenceFailureReportsError(t *testing.T) {
	workspace := t.TempDir()
	store, storePath := newTrustStore(t)
	// Sabotage persistence: turn the store path into a directory so Save's
	// final rename onto it fails after the trust decision is recorded in memory.
	if err := os.MkdirAll(storePath, 0o700); err != nil {
		t.Fatalf("mkdir over trust store: %v", err)
	}
	m := newModel(context.Background(), Options{Cwd: workspace, Trusted: false, TrustStore: store})
	m.designMode = false
	m.input.SetValue("/trust")
	updated, _ := m.handleSubmit()
	next := updated.(model)
	next.trustPromptRequired = true
	next = selectTrustItem(t, next, trustActionCurrent)
	chosen, _ := next.choosePicker()
	got := chosen.(model)
	if got.trusted {
		t.Fatal("trusted changed after persistence failure")
	}
	if !got.trustPromptRequired || got.picker == nil || got.picker.kind != pickerTrust {
		t.Fatal("required trust menu closed after persistence failure")
	}
	note := transcriptText(got.transcript)
	if !strings.Contains(note, "Failed to save") {
		t.Fatalf("trust menu did not report persistence failure: %q", note)
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
