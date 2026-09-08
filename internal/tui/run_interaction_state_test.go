package tui

// run_interaction_state_test.go (F2, stabilization P0): session switch +
// stale interaction state. §8's "session switch + old async result"
// combination: a handoff card, diff viewport, or file drill-in bound to the
// previous session's run must not survive a real session change.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/worktrees"
)

// verifierCreateSession makes a resumable session for cwd and returns it.
func verifierCreateSession(t *testing.T, m model, cwd string) sessions.Metadata {
	t.Helper()
	sess, err := m.sessionStore.Create(sessions.CreateInput{
		SessionKind: sessions.SessionKind(""),
		Cwd:         cwd,
	})
	if err != nil {
		t.Fatalf("verifier: create session: %v", err)
	}
	return sess
}

// verifierSessionSwitch builds a model carrying run-bound state (handoff,
// diff view, file view, worktree, auto-revealed trajectory), then switches
// to a different session through the REAL /resume flow.
func verifierSessionSwitch(t *testing.T) (before, after model) {
	t.Helper()
	wsA := t.TempDir()

	// Session A: active, with a handoff + diff view + file view + worktree.
	m := newDesignModeTestModel(wsA, &fakeProvider{}, nil)
	sessA := verifierCreateSession(t, m, wsA)
	m.activeSession = sessA

	wt := &worktrees.Result{Name: "wt-stale", Path: t.TempDir(), RepoRoot: "/nonexistent"}
	m.activeWorktree = wt
	m.pendingHandoff = &handoffState{
		lane:      "wt-stale",
		path:      wt.Path,
		branch:    "splice/wt-stale",
		outcome:   "failed",
		preserved: true,
	}
	// Open the diff view directly (the [D] dispatch would re-open the same
	// state; the state under test is identical).
	m, _ = m.openDiffReview(*wt)
	if !m.diffView.active {
		t.Fatal("verifier setup: diff view did not open")
	}
	m.fileView = fileViewState{active: true, path: "src/auth/session.go"}
	m.trajectoryVisible = true
	m.trajectoryAutoRevealed = true
	before = m

	// Session B: a different resumable session on disk. Switch by explicit
	// id — deterministic, no ordering assumptions.
	sessB := verifierCreateSession(t, m, wsA)
	if sessB.SessionID == sessA.SessionID {
		t.Fatal("verifier setup: both sessions share an id")
	}
	updatedT, _ := before.Update(testKeyText("/resume " + sessB.SessionID))
	mid := updatedT.(model)
	// /resume fills the composer; Enter submits it the way the real flow does.
	updatedT, _ = mid.Update(testKey(tea.KeyEnter))
	after = updatedT.(model)
	return before, after
}

func TestSessionSwitchResetsHandoffCard(t *testing.T) {
	before, after := verifierSessionSwitch(t)
	if before.pendingHandoff == nil {
		t.Fatal("verifier setup: handoff not armed")
	}
	if after.activeSession.SessionID == before.activeSession.SessionID {
		t.Fatal("verifier setup: session did not actually change")
	}
	if after.pendingHandoff != nil {
		t.Fatal("stale handoff card survived the session switch — the previous session's worktree actions stay live in a new conversation")
	}
}

func TestSessionSwitchClosesDiffView(t *testing.T) {
	before, after := verifierSessionSwitch(t)
	if !before.diffView.active {
		t.Fatal("verifier setup: diff view not open")
	}
	if after.diffView.active {
		t.Fatalf("stale diff view survived the session switch (still showing lane %q from the previous session)", after.diffView.wt.Name)
	}
}

func TestSessionSwitchClosesFileView(t *testing.T) {
	before, after := verifierSessionSwitch(t)
	if !before.fileView.active {
		t.Fatal("verifier setup: file view not open")
	}
	if after.fileView.active {
		t.Fatal("stale file drill-in survived the session switch")
	}
}

func TestSessionSwitchClearsWorktreeBindingAndTrajectoryReveal(t *testing.T) {
	before, after := verifierSessionSwitch(t)
	if before.activeWorktree == nil {
		t.Fatal("verifier setup: worktree binding missing")
	}
	if after.activeWorktree != nil {
		t.Fatal("previous session's worktree binding survived the switch")
	}
	if !before.trajectoryVisible {
		t.Fatal("verifier setup: trajectory not auto-revealed")
	}
	if after.trajectoryVisible || after.trajectoryAutoRevealed {
		t.Fatal("previous session's trajectory auto-reveal survived the switch (§10 default is hidden)")
	}
}

// A no-op switch (resuming the session that is already active) must NOT
// disturb open interaction surfaces.
func TestSessionSwitchNoOpKeepsSurfaces(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	sess := verifierCreateSession(t, m, ws)
	m.activeSession = sess
	m.pendingHandoff = &handoffState{lane: "wt-live", path: t.TempDir(), preserved: true}
	m.diffView = diffViewState{active: true, wt: worktrees.Result{Name: "wt-live"}, text: diffReviewTestDiff}

	updated, _ := m.Update(testKeyText("/resume latest"))
	mid := updated.(model)
	updated, _ = mid.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.pendingHandoff == nil {
		t.Fatal("no-op switch cleared the handoff; resuming the active session must not disturb surfaces")
	}
	if !next.diffView.active {
		t.Fatal("no-op switch closed the diff view")
	}
}

// No stale handoff payload row may render in the new session's transcript
// (the transcript is rebuilt from the new session's events; a leaked card
// row would be an actionable action for a foreign lane).
func TestSessionSwitchTranscriptHasNoStaleHandoffCard(t *testing.T) {
	_, after := verifierSessionSwitch(t)
	for _, row := range after.transcript {
		if strings.Contains(row.text, handoffTranscriptMarker) {
			t.Fatal("stale handoff card row rendered in the new session's transcript")
		}
	}
}
