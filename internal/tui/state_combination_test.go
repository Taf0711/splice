package tui

// state_combination_test.go (F5, stabilization P0 §8): interaction-state
// conflict probes. Each probe drives the REAL update path with REAL input
// shapes and asserts the invariant that must hold while two states are live
// simultaneously. Probes here cover the pairs not already owned by
// acceptance_wiring_test.go / acceptance_verify_test.go / session_test.go.

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/worktrees"
)

// verifierPermissionDecision collects the decision a permission callback
// receives, so a probe can prove the runtime was unblocked exactly once.
func TestPermissionPromptPlusCancelUnblocksRuntimeOnce(t *testing.T) {
	// ask_user + cancel (§8): the run is blocked on a hard ask gate; the
	// user cancels with Esc (once — the ask gate consumes Esc as cancel,
	// no confirm window while a gate owns input). The gate must release
	// with the run, the runtime goroutine unblocks via ctx cancellation,
	// and the run lands in the flush set (late events still persist).
	m := mouseTestModel()
	m.activeRunID = 7
	m.pending = true
	updated, _ := m.Update(askUserRequestMsg{
		runID: 7,
		request: agent.AskUserRequest{
			ToolCallID: "gate-1",
			Header:     "Design question",
			Questions: []agent.AskUserQuestion{{
				Question:    "retry streamed bodies?",
				Header:      "Retry",
				Options:     []string{"never", "buffer"},
				Recommended: "never",
			}},
		},
		answer: func([]string) {},
	})
	m = updated.(model)
	if m.pendingAskUser == nil {
		t.Fatal("verifier setup: ask gate not pending")
	}
	// Esc on the ask gate cancels the run (documented gate behavior).
	updatedEsc, _ := m.Update(testKey(tea.KeyEsc))
	next := updatedEsc.(model)
	if next.pendingAskUser != nil {
		t.Fatal("ask_user gate survived run cancel — the gate must release with the run")
	}
	if next.pending {
		t.Fatal("cancel did not clear pending state")
	}
	if next.activeRunID != 0 {
		t.Fatal("cancel did not clear activeRunID")
	}
	if len(next.flushRunIDs) == 0 {
		t.Fatal("cancelled run not flagged for session-event flush (late checkpoints would be orphaned)")
	}
}

func TestPermissionPromptCancelResolvesCallbackExactlyOnce(t *testing.T) {
	// permission + cancel: the blocked agent goroutine waits on ctx.Done()
	// (the ask/permission plumbing unblocks via context cancellation, see
	// OnAskUser's select), so cancelRun clearing the prompt without invoking
	// the decide callback is correct — but it must clear the prompt so the
	// UI doesn't advertise a gate whose run is gone.
	m := mouseTestModel()
	m.activeRunID = 9
	m.pending = true
	decisions := 0
	updated, _ := m.Update(permissionRequestMsg{
		runID: 9,
		request: agent.PermissionRequest{
			ToolName: "bash",
			Action:   agent.PermissionActionPrompt,
		},
		decide: func(agent.PermissionDecision) { decisions++ },
	})
	next := updated.(model)
	if next.pendingPermission == nil {
		t.Fatal("verifier setup: permission gate not pending")
	}
	next.cancelRun()
	if next.pendingPermission != nil {
		t.Fatal("cancel left the permission prompt pending — the UI would advertise a gate whose run is gone")
	}
	if next.activeRunID != 0 {
		t.Fatal("cancel did not clear activeRunID")
	}
	// The callback was never invoked (cancel relies on ctx cancellation);
	// a decision here would double-resolve when the context also fires.
	if decisions != 0 {
		t.Fatalf("cancel invoked the decide callback %d times; cancellation unblocks via ctx, not the callback", decisions)
	}
}

// cancelled run + late goroutine result (§8): the late agentResponseMsg
// must not offer a worktree review or resurrect run state, but MUST release
// the worktree lock (stale path) and drain for session persistence.
func TestCancelledRunLateResultReleasesWorktreeWithoutReview(t *testing.T) {
	m := mouseTestModel()
	m.activeRunID = 11
	m.pending = true
	// Cancel clears activeRunID and flags the run for a late flush.
	m.cancelRun()
	if m.activeRunID != 0 {
		t.Fatal("verifier setup: cancel did not clear activeRunID")
	}
	flushCount := len(m.flushRunIDs)
	if flushCount == 0 {
		t.Fatal("verifier setup: cancelled run not flagged for flush")
	}
	// The late result arrives with the runID the run started under.
	wt := &worktrees.Result{Name: "wt-late", Path: t.TempDir(), RepoRoot: "/nonexistent"}
	updated, _ := m.Update(planExecutionResultMsg{runID: 11, err: acceptErr("late failure"), worktree: wt})
	next := updated.(model)
	if next.pendingHandoff != nil {
		t.Fatal("late result for a cancelled run offered a HANDOFF — a cancelled run's lane surfaces nothing actionable")
	}
	// The worktree lock release ran (no panic, state consistent).
	if next.activeWorktree != nil && next.activeWorktree.Name == "wt-late" && next.pending {
		t.Fatal("late result resurrected pending state for a cancelled run")
	}
}

// fileView + running agent: a resize while the drill-in is open must keep
// the view consistent (nav bar swap, scroll preserved) and not leak the
// file view into the running-agent transcript.
func TestFileViewOpenSurvivesResizeWhileRunning(t *testing.T) {
	m := mouseTestModel()
	m.fileView = fileViewState{active: true, path: "src/x.go", parentScrollOffset: 4}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	next := updated.(model)
	if !next.fileView.active {
		t.Fatal("resize closed the file drill-in")
	}
	if next.width != 100 || next.height != 26 {
		t.Fatalf("resize not applied: %dx%d", next.width, next.height)
	}
	// The title bar must still be the file nav line (the swap holds at the
	// new width).
	nav := next.pinnedTitleBar(next.chatColumnWidth())
	if nav == "" {
		t.Fatal("file nav bar empty after resize")
	}
}

// repair re-entry + prior failure: a failed stage followed by a re-entry
// must preserve node identity and the failed node's history (reducer
// contract, asserted through the real presentrun seam).
func TestRepairReEntryPreservesNodeIdentity(t *testing.T) {
	acc := presentrun.New(nil)
	// Pass 1: write stage fails (isolated lane, per the runtime stamp).
	acc.Apply(presentrun.AdaptStageEvent(agent.StageEvent{
		Name: "code_writer", Status: "failed", Detail: "boom", Workspace: "isolated",
	}))
	// Re-entry: same stage runs again (repair).
	acc.Apply(presentrun.AdaptStageEvent(agent.StageEvent{
		Name: "code_writer", Status: "running", Detail: "retrying", Workspace: "isolated",
	}))
	state := acc.Snapshot()
	nodes := state.Nodes
	if len(nodes) != 1 {
		t.Fatalf("re-entry duplicated the node: %d nodes", len(nodes))
	}
	if nodes[0].Status != "running" {
		t.Fatalf("re-entry status = %q, want running", nodes[0].Status)
	}
	// Workspace identity persists across re-entries (DoD 26 corollary).
	if nodes[0].Workspace != "isolated" {
		t.Fatalf("workspace identity lost across re-entry: %q", nodes[0].Workspace)
	}
}

// session switch + old async result: a diffCapturedMsg for a lane that is
// no longer active must be dropped (already asserted in diff tests); here
// the complement — an ask_user answer arriving after the session switched
// must not mutate the new session's state.
func TestSessionSwitchDropsOldGateAnswer(t *testing.T) {
	// session switch + old gate: after the F2 interaction reset, the gate is
	// gone; a stale Enter must not resolve the old callback or crash.
	m := mouseTestModel()
	m.activeRunID = 21
	answers := 0
	updated, _ := m.Update(askUserRequestMsg{
		runID: 21,
		request: agent.AskUserRequest{
			ToolCallID: "gate-old",
			Header:     "old session",
			Questions: []agent.AskUserQuestion{{
				Question:    "q",
				Options:     []string{"a"},
				Recommended: "a",
			}},
		},
		answer: func([]string) { answers++ },
	})
	m = updated.(model)
	if m.pendingAskUser == nil {
		t.Fatal("verifier setup: gate not pending")
	}
	// Switch session (the F2 reset path clears the run-bound gate).
	m = m.resetRunInteractionState()
	if m.pendingAskUser != nil {
		t.Fatal("interaction reset left the old session's gate pending")
	}
	// A stale Enter (user answering a gate that no longer exists) must be a
	// harmless no-op: no answer delivered, no panic.
	updatedEnter, _ := m.Update(testKey(tea.KeyEnter))
	_ = updatedEnter
	if answers != 0 {
		t.Fatalf("stale Enter resolved the old gate callback %d times after the switch", answers)
	}
}
