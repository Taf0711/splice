package tui

// receipt_keys_test.go: the advertised receipt/lifecycle-card action keys
// dispatch through the real Update path with real terminal key shapes
// (shift+letter as a terminal emits it), and plain lowercase letters still
// reach the composer. Fail-on-old-code proof: the audit probe
// (TestAuditReceiptKeysDispatch / TestAuditPlanApproveKeyDispatch,
// 2026-09-05) showed every advertised key typed its letter into the
// composer on pre-fix code; these tests pin the fixed dispatch.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/worktrees"
)

// planApprovalFixture arms a design model with a persisted plan revision
// and a pending plan card (the state the IMPLEMENTATION PLAN card's [A]
// approve key acts on).
func planApprovalFixture(t *testing.T) model {
	t.Helper()
	store := testSessionStore(t)
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, store)
	var err error
	m, err = m.ensureActiveSession("design")
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	m, _ = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	planJSON, _ := json.Marshal(tuiDesignPlan())
	payload := splicerun.PlanCrystallizedPayload{PlanID: "p1", Revision: 1, Plan: planJSON}
	if m, err = m.appendSessionEvent(sessions.EventPlanCrystallized, payload); err != nil {
		t.Fatalf("append plan_crystallized: %v", err)
	}
	m = disableAutoTitle(m)
	m.activeRunID = 1
	m.width, m.height, m.altScreen = 100, 40, true
	updated, _ := m.Update(crystallizeResultMsg{runID: 1, plan: tuiDesignPlan(), critique: tuiCleanCritique()})
	next := updated.(model)
	if next.pendingPlan == nil {
		t.Fatal("fixture: plan card did not arm pendingPlan")
	}
	return next
}

// receiptFixture drives a failed (or cancelled, err=context.Canceled) plan
// execution through the real message path and returns the model after the
// worktree review picker resolved.
func receiptFixture(t *testing.T, err error) model {
	t.Helper()
	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 42
	updated, _ := m.Update(planExecutionResultMsg{runID: 42, err: err})
	next := updated.(model)
	if next.lastTerminalReceipt == "" {
		t.Fatal("fixture: terminal receipt not recorded")
	}
	if next.pendingAskUser != nil {
		updated, _ = next.Update(testKey(tea.KeyEnter)) // keep
		next = updated.(model)
	}
	return next
}

// [A] on the plan card starts approval through the real path.
func TestReceiptPlanCardApproveKeyStartsApproval(t *testing.T) {
	next := planApprovalFixture(t)
	if view := plainRender(t, next.View()); !strings.Contains(view, "[A] approve") {
		t.Fatalf("fixture: approve row not visible: %s", view)
	}
	updated, _ := next.Update(reviewRealShiftKey('A'))
	approved := updated.(model)
	if !approved.pending {
		t.Fatal("receipt key: [A] approve did not start the approval run")
	}
}

// [R] on the plan card prefills the composer for a revision; it must not
// type a bare "R".
func TestReceiptPlanCardReviseKeyPrefillsComposer(t *testing.T) {
	next := planApprovalFixture(t)
	updated, _ := next.Update(reviewRealShiftKey('R'))
	revised := updated.(model)
	if !strings.HasPrefix(revised.input.Value(), "Revise the plan:") {
		t.Fatalf("receipt key: [R] revise = %q, want a revise prompt prefill", revised.input.Value())
	}
}

// [F] on a blocked critique card prefills the fold prompt.
func TestReceiptCritiqueFoldKeyPrefillsComposer(t *testing.T) {
	next := planApprovalFixture(t)
	updated, _ := next.Update(crystallizeResultMsg{runID: 1, plan: tuiDesignPlan(), critique: testCritiqueBlocking()})
	next = updated.(model)
	updated, _ = next.Update(reviewRealShiftKey('F'))
	folded := updated.(model)
	if !strings.HasPrefix(folded.input.Value(), "Fold required fixes:") {
		t.Fatalf("receipt key: [F] fold = %q, want the fold prompt prefill", folded.input.Value())
	}
}

// [A] on the cancelled receipt runs the merge-back seam (apply staged)
// through the same runtime path the worktree review uses.
func TestReceiptCancelledApplyKeyMergesBack(t *testing.T) {
	origMerge := tuiMergeBackWorktree
	defer func() { tuiMergeBackWorktree = origMerge }()
	merged := false
	tuiMergeBackWorktree = func(_ context.Context, _ worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
		merged = true
		return worktrees.MergeBackResult{Status: worktrees.MergeBackMerged, Message: "merged"}, nil
	}
	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 42
	wt := &worktrees.Result{Name: "wt-receipt-a", Path: "/nonexistent/wt-receipt-a", RepoRoot: "/nonexistent"}
	updated, _ := m.Update(planExecutionResultMsg{runID: 42, err: context.Canceled, worktree: wt})
	next := updated.(model)
	updated, _ = next.Update(testKey(tea.KeyEnter)) // keep
	next = updated.(model)
	updated, _ = next.Update(reviewRealShiftKey('A'))
	applied := updated.(model)
	if !merged {
		t.Fatal("receipt key: cancelled [A] apply staged did not run the merge-back seam")
	}
	joined := transcriptText(applied.transcript)
	if !strings.Contains(joined, "Receipt action queued") {
		t.Fatal("receipt key: [A] produced no queued-action notice")
	}
}

// [D] on the cancelled receipt preserves then removes the worktree.
func TestReceiptCancelledDiscardKeyPreservesThenRemoves(t *testing.T) {
	origPreserve := tuiPreserveWorktree
	origRemove := tuiRemoveWorktree
	defer func() {
		tuiPreserveWorktree = origPreserve
		tuiRemoveWorktree = origRemove
	}()
	preserved, removed := false, false
	tuiPreserveWorktree = func(_ context.Context, _ worktrees.MergeBackOptions) (string, error) {
		preserved = true
		return "splice/wt-receipt-d", nil
	}
	tuiRemoveWorktree = func(_ context.Context, _ worktrees.RemoveOptions) error {
		removed = true
		return nil
	}
	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 42
	wt := &worktrees.Result{Name: "wt-receipt-d", Path: "/nonexistent/wt-receipt-d", RepoRoot: "/nonexistent"}
	updated, _ := m.Update(planExecutionResultMsg{runID: 42, err: context.Canceled, worktree: wt})
	next := updated.(model)
	updated, _ = next.Update(testKey(tea.KeyEnter))
	next = updated.(model)
	updated, _ = next.Update(reviewRealShiftKey('D'))
	if !preserved || !removed {
		t.Fatalf("receipt key: cancelled [D] (preserved=%v removed=%v) did not discard via the review seams", preserved, removed)
	}
}

// With no worktree, [A]/[D] say so instead of silently doing nothing.
func TestReceiptCancelledWorktreeKeysWithoutWorktreeSaySo(t *testing.T) {
	for _, key := range []rune{'A', 'D'} {
		next := receiptFixture(t, context.Canceled)
		updated, _ := next.Update(reviewRealShiftKey(key))
		got := updated.(model)
		if !strings.Contains(transcriptText(got.transcript), "No staged work") {
			t.Fatalf("receipt key: cancelled [%c] without a worktree produced no honest notice", key)
		}
	}
}

// [R] on a terminal receipt opens the resume surface (picker or the text
// fallback) — never a typed "R".
func TestReceiptResumeKeyOpensResume(t *testing.T) {
	for _, err := range []error{context.Canceled, acceptErr("verification failed")} {
		next := receiptFixture(t, err)
		updated, _ := next.Update(reviewRealShiftKey('R'))
		got := updated.(model)
		if got.picker == nil && got.input.Value() == "R" {
			t.Fatalf("receipt key: [%v] [R] typed R into the composer instead of resuming", err)
		}
	}
}

// [I] intervenes (self-correct full) and [L] opens the logs (detail pane).
func TestReceiptFailedInterveneAndLogsKeys(t *testing.T) {
	next := receiptFixture(t, acceptErr("verification gate rejected the build"))
	updated, _ := next.Update(reviewRealShiftKey('I'))
	intervened := updated.(model)
	if !intervened.selfCorrectTests {
		t.Fatal("receipt key: failed [I] intervene did not enable full self-correct")
	}
	next = receiptFixture(t, acceptErr("verification gate rejected the build"))
	updated, _ = next.Update(reviewRealShiftKey('L'))
	logs := updated.(model)
	if !logs.detailView.active {
		t.Fatal("receipt key: failed [L] logs did not open the detail pane")
	}
}

// Plain lowercase letters never dispatch: they belong to the composer.
func TestReceiptPlainLettersDoNotDispatch(t *testing.T) {
	fired := false
	origMerge := tuiMergeBackWorktree
	tuiMergeBackWorktree = func(_ context.Context, _ worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
		fired = true
		return worktrees.MergeBackResult{}, nil
	}
	defer func() { tuiMergeBackWorktree = origMerge }()
	for _, letter := range []rune{'a', 'd', 'r', 'i', 'l', 'f'} {
		next := planApprovalFixture(t)
		updated, _ := next.Update(reviewRealPlainKey(letter))
		got := updated.(model)
		if !strings.Contains(got.input.Value(), string(letter)) {
			t.Fatalf("receipt key: plain %q did not reach the composer", letter)
		}
		if got.pending {
			t.Fatalf("receipt key: plain %q dispatched an action", letter)
		}
	}
	_ = fired
	next := receiptFixture(t, context.Canceled)
	updated, _ := next.Update(reviewRealPlainKey('a'))
	if fired {
		t.Fatal("receipt key: plain 'a' on a cancelled receipt dispatched the merge")
	}
	if got := updated.(model); got.input.Value() != "a" {
		t.Fatalf("receipt key: plain 'a' composer = %q", got.input.Value())
	}
}

// An armed preserved handoff owns its keys: [D] opens the diff (the
// handoff meaning), and the receipt discard must not fire behind it.
func TestReceiptKeysInertBehindArmedHandoff(t *testing.T) {
	origRemove := tuiRemoveWorktree
	defer func() { tuiRemoveWorktree = origRemove }()
	removed := false
	tuiRemoveWorktree = func(_ context.Context, _ worktrees.RemoveOptions) error {
		removed = true
		return nil
	}
	next := reviewArmedHandoffModel(t, "wt-receipt-handoff")
	next.lastTerminalReceipt = receiptCancelled
	updated, _ := next.Update(reviewRealShiftKey('D'))
	got := updated.(model)
	if removed {
		t.Fatal("receipt key: [D] discarded behind an armed handoff")
	}
	if !got.diffView.active {
		t.Fatal("receipt key: armed handoff [D] no longer opens the diff review")
	}
}

// verifiedRunFixture drives a SUCCESSFUL plan execution through the real
// message path with a completed presentation state, the way the runtime
// emits it (finishPresentation stamps Completion on the snapshot).
func verifiedRunFixture(t *testing.T) model {
	t.Helper()
	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 7
	m.width, m.height, m.altScreen = 100, 40, true
	m.turnStartedAt = m.now().Add(-90 * time.Second)
	m.lastState = presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleComplete,
		Usage:         presentation.UsageSummary{InputTokens: 12345, OutputTokens: 678},
		Completion:    &presentation.CompletionReceipt{Status: "completed", Detail: "design plan completed"},
	}
	result := agent.Result{
		FinalAnswer: `{"plan_id":"plan-abc","status":"completed","completed_tasks":[{"task_id":"t1","run_id":"plan-abc-t1","status":"completed"},{"task_id":"t2","run_id":"plan-abc-t2","status":"completed"}]}`,
	}
	updated, _ := m.Update(planExecutionResultMsg{runID: 7, result: result})
	return updated.(model)
}

// A completed run projects the VERIFIED card with runtime evidence and
// arms its keys — the emitter the GAP-E contract required but never had.
func TestVerifiedReceiptEmittedOnCompletedRun(t *testing.T) {
	next := verifiedRunFixture(t)
	if next.lastTerminalReceipt != receiptVerified {
		t.Fatalf("verified receipt: lastTerminalReceipt = %q, want verified", next.lastTerminalReceipt)
	}
	view := plainRender(t, next.View())
	for _, want := range []string{"VERIFIED", "design plan plan-abc completed", "2 task(s) completed", "t1, t2", "[O] open diff"} {
		if !strings.Contains(view, want) {
			t.Fatalf("verified receipt missing %q in live View:\n%s", want, view)
		}
	}
}

// [O] opens the diff review when the run ran in a worktree.
func TestVerifiedReceiptOpenDiffKey(t *testing.T) {
	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 8
	m.lastState = presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleComplete,
		Completion:    &presentation.CompletionReceipt{Status: "completed"},
	}
	updated, _ := m.Update(planExecutionResultMsg{
		runID:    8,
		result:   agent.Result{FinalAnswer: `{"plan_id":"p","status":"completed","completed_tasks":[{"task_id":"t1","run_id":"r1","status":"completed"}]}`},
		worktree: &worktrees.Result{Name: "wt-verified", Path: "/nonexistent/wt-verified", RepoRoot: "/nonexistent"},
	})
	next := updated.(model)
	updated, _ = next.Update(testKey(tea.KeyEnter)) // resolve the review picker (keep)
	next = updated.(model)
	updated, cmd := next.Update(reviewRealShiftKey('O'))
	opened := updated.(model)
	if !opened.diffView.active {
		t.Fatal("verified receipt: [O] did not open the diff review")
	}
	if cmd == nil {
		t.Fatal("verified receipt: [O] produced no diff capture command")
	}
}

// [E] exports and [R] resumes on the VERIFIED card; the no-worktree [O]
// path says so honestly.
func TestVerifiedReceiptExportResumeAndNoWorktreeOpen(t *testing.T) {
	next := verifiedRunFixture(t)
	updated, _ := next.Update(reviewRealShiftKey('E'))
	exported := updated.(model)
	if !strings.Contains(transcriptText(exported.transcript), "export") {
		t.Fatal("verified receipt: [E] produced no export ack")
	}
	updated, _ = exported.Update(reviewRealShiftKey('R'))
	resumed := updated.(model)
	if resumed.picker == nil && resumed.input.Value() == "R" {
		t.Fatal("verified receipt: [R] typed R into the composer instead of resuming")
	}
	fresh := verifiedRunFixture(t)
	updated, _ = fresh.Update(reviewRealShiftKey('O'))
	opened := updated.(model)
	if opened.diffView.active {
		t.Fatal("verified receipt: [O] opened a diff with no worktree")
	}
	if !strings.Contains(transcriptText(opened.transcript), "No worktree diff") {
		t.Fatal("verified receipt: [O] without a worktree produced no honest notice")
	}
}

// The DoD-13 gate: no deterministic completion in the presentation state,
// no VERIFIED card — a success-looking result must not fabricate one.
func TestVerifiedReceiptGatedOnRuntimeCompletion(t *testing.T) {
	m := mouseTestModel()
	m.sessionStore = testSessionStore(t)
	m.activeRunID = 9
	m.lastState = presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleExecute,
	}
	updated, _ := m.Update(planExecutionResultMsg{
		runID:  9,
		result: agent.Result{FinalAnswer: `{"plan_id":"p","status":"completed","completed_tasks":[{"task_id":"t1","run_id":"r1","status":"completed"}]}`},
	})
	next := updated.(model)
	if next.lastTerminalReceipt != "" {
		t.Fatalf("verified receipt: gated run armed keys with %q", next.lastTerminalReceipt)
	}
	if view := plainRender(t, next.View()); strings.Contains(view, "VERIFIED") {
		t.Fatal("verified receipt: card rendered without a runtime completion receipt (DoD 13)")
	}
}
