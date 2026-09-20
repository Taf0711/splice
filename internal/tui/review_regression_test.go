package tui

// review_regression_test.go pins the defects found by the workflow-surfaces
// code review of PR #26 (September 2026). Every probe here FAILED on the
// reviewed head 10e9b67f and passes only against the corrected behavior, so
// each one is a regression net for one finding, not a description of the
// implementation.
//
// The probes came from the reviewer as an overlay suite. They are kept in
// the tree, next to the code under test, so the failure modes cannot come
// back silently: a guard that breaks quietly looks like success, which is
// exactly why these assert desired behavior rather than current behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Taf0711/splice/internal/agent"

	"github.com/Taf0711/splice/internal/modelregistry"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/worktrees"
)

func TestReviewReceiptResumeSchedulesScan(t *testing.T) {
	m := receiptFixture(t, errors.New("failed"))
	_, next, cmd := m.receiptResume()
	if next.(model).sessionScanInFlight && cmd == nil {
		t.Fatal("session scan marked in flight but no command returned; resume never opens")
	}
}

func reviewHandoff(t *testing.T) model {
	t.Helper()
	m := mouseTestModel()
	m.activeWorktree = &worktrees.Result{Name: "review-lane", Path: t.TempDir(), RepoRoot: t.TempDir()}
	m.pendingHandoff = &handoffState{lane: m.activeWorktree.Name, path: m.activeWorktree.Path, preserved: true}
	return m
}

func TestReviewHandoffDefersGitUntilCommand(t *testing.T) {
	m := reviewHandoff(t)
	old := tuiMergeBackWorktree
	defer func() { tuiMergeBackWorktree = old }()
	called := false
	tuiMergeBackWorktree = func(context.Context, worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
		called = true
		return worktrees.MergeBackResult{}, errors.New("refused")
	}
	m.runHandoffMerge()
	if called {
		t.Fatal("Git merge ran synchronously before the returned tea.Cmd was executed")
	}
}

func TestReviewFailedHandoffRemainsActionable(t *testing.T) {
	m := reviewHandoff(t)
	old := tuiMergeBackWorktree
	defer func() { tuiMergeBackWorktree = old }()
	tuiMergeBackWorktree = func(context.Context, worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
		return worktrees.MergeBackResult{}, errors.New("merge conflict")
	}
	_, next, _ := m.runHandoffMerge()
	got := next.(model)
	if got.pendingHandoff == nil || strings.Contains(transcriptText(got.transcript), "Handoff resolved: merged") {
		t.Fatal("failed merge clears the handoff and announces merged")
	}
}

func TestReviewHandoffDoesNotMutateDuringRun(t *testing.T) {
	m := reviewHandoff(t)
	m.pending = true
	old := tuiMergeBackWorktree
	defer func() { tuiMergeBackWorktree = old }()
	called := false
	tuiMergeBackWorktree = func(context.Context, worktrees.MergeBackOptions) (worktrees.MergeBackResult, error) {
		called = true
		return worktrees.MergeBackResult{}, errors.New("refused")
	}
	m.Update(reviewRealShiftKey('M'))
	if called {
		t.Fatal("handoff merge is enabled while a new run is pending")
	}
}

func TestReviewComposerRetainsCapitalLetters(t *testing.T) {
	m := planApprovalFixture(t)
	m.input.SetValue("Please ")
	next, _ := m.Update(reviewRealShiftKey('R'))
	if next.(model).input.Value() == "Revise the plan: " {
		t.Fatal("typing uppercase R replaces an existing user draft with a revision template")
	}
}

func TestReviewReplayRebuildsPipeline(t *testing.T) {
	m := mouseTestModel()
	st := benchNodeState(2)
	m = m.replayPresentationState([]sessions.Event{replayTestEvent(t, st)})
	if m.pipeline.isEmpty() {
		t.Fatal("snapshot restored lastState but did not restore pipeline nodes")
	}
}

func TestReviewSessionResetClearsReceiptTruth(t *testing.T) {
	m := mouseTestModel()
	m.lastState = replayTestState("failed")
	m.lastTerminalReceipt = receiptFailed
	m = m.resetRunInteractionState()
	if m.lastState.Completion != nil || m.lastTerminalReceipt != "" {
		t.Fatal("session reset retains previous session's presentation truth and receipt keys")
	}
}

func TestReviewQuietKeepsPlanAndCritique(t *testing.T) {
	for _, marker := range []string{planCardMarker, critiqueCardMarker, handoffTranscriptMarker} {
		if !narrationVisible(transcriptRow{kind: rowSystem, text: marker + "important state"}, verbosityQuiet) {
			t.Errorf("quiet hides workflow card %q", marker)
		}
	}
}

func TestReviewEmptyDiffIsLoadedState(t *testing.T) {
	m := reviewHandoff(t)
	m.diffView = diffViewState{active: true, wt: *m.activeWorktree, base: "main"}
	m = m.handleDiffCaptured(diffCapturedMsg{lane: m.activeWorktree.Name, res: ""})
	if strings.Contains(m.renderDiffReview(100), "Capturing") {
		t.Fatal("successful empty diff is shown as capturing forever")
	}
}

func TestReviewDiffIncludesUncommittedWrites(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "review@example.test")
	git("config", "user.name", "Review")
	file := filepath.Join(dir, "file.txt")
	os.WriteFile(file, []byte("before\n"), 0600)
	git("add", "file.txt")
	git("commit", "-m", "initial")
	os.WriteFile(file, []byte("after\n"), 0600)
	patch, err := tuiDiffCapture(context.Background(), worktrees.Result{Name: "lane", Path: dir, SourceBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "+after") {
		t.Fatal("review diff omits uncommitted tracked writes that merge-back will commit")
	}
}

func TestReviewCancelAcceptsTerminalPlanResult(t *testing.T) {
	m := mouseTestModel()
	m.activeRunID = 77
	m.pending = true
	m.runCancel = func() {}
	m.cancelRun()
	next, _ := m.Update(planExecutionResultMsg{runID: 77, err: context.Canceled})
	got := next.(model)
	if got.lastTerminalReceipt != receiptCancelled || len(got.flushRunIDs) != 0 {
		t.Fatalf("cancelled receipt=%q; undrained run IDs=%d", got.lastTerminalReceipt, len(got.flushRunIDs))
	}
}

func TestReviewTrustCountsActualHookSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	os.WriteFile(path, []byte(`{"enabled":true,"hooks":[{"id":"hook","event":"sessionStart","command":"example","enabled":true}]}`), 0600)
	if n := countHookEntries(path); n != 1 {
		t.Fatalf("actual hooks.Config has one executable hook; trust surface counts %d", n)
	}
}

func TestReviewStageWizardDetectsExistingOverrideEdit(t *testing.T) {
	cfg := schemas.StageModelConfigFile{Default: schemas.StageModelConfig{ProviderProfile: "test", Model: "model"}, Stages: map[string]schemas.StageModelConfig{"code_writer": {ProviderProfile: "test", Model: "model"}}}
	w := stageModelWizardState{config: cfg, initialConfig: cfg}
	stage := w.config.Stages["code_writer"]
	stage.ReasoningEffort = "high"
	w.config.Stages["code_writer"] = stage
	if !w.isDirty() {
		t.Fatal("editing an existing override mutates the shallow initialConfig snapshot; dirty check stays false")
	}
}

func TestReviewDesignPhaseReplacesCompletedPhase(t *testing.T) {
	m := planApprovalFixture(t)
	m.lastState = replayTestState("completed")
	m.phaseTrail.observe(presentation.LifecycleComplete)
	m = m.enterDesignMode("")
	if m.phaseTrail.current() == presentation.LifecycleComplete {
		t.Fatal("entering design mode retains the previous complete phase chip")
	}
}

func TestReviewFailedStatusChipNamesFailure(t *testing.T) {
	m := mouseTestModel()
	m.lastState = replayTestState("failed")
	m.phaseTrail.observe(presentation.LifecycleComplete)
	if text := stripANSI(m.phaseChipSegment()); !strings.Contains(text, "failed") {
		t.Fatalf("failed run's status chip says %q", text)
	}
}

func TestReviewModelAutoClearsPreviousEffort(t *testing.T) {
	m := limeTestModel()
	m.modelName = "claude-sonnet-4.5"
	m.reasoningEffort = modelregistry.ReasoningEffortHigh
	item := pickerItem{Value: m.modelName, Efforts: m.modelCatalog.ReasoningEfforts(m.modelName), EffortIndex: effortAuto}
	m = m.applyPickedModelEffort(item)
	if m.reasoningEffort != "" {
		t.Fatalf("auto selection retained %q effort", m.reasoningEffort)
	}
}

func TestReviewDiffTailRemainsReachable(t *testing.T) {
	m := reviewHandoff(t)
	m.height = 40
	patch := "diff --git a/a b/a\n@@ -1 +1 @@\n" + strings.Repeat("+line\n", 4100) + "+TAIL_SENTINEL"
	m.diffView = diffViewState{active: true, wt: *m.activeWorktree, text: patch, hunkTop: 4102, base: "main"}
	if !strings.Contains(m.renderDiffReview(100), "TAIL_SENTINEL") {
		t.Fatal("scroll position beyond line 4000 is clamped during render; diff tail can never be inspected")
	}
}

func TestReviewApprovePersistsEmittedPresentation(t *testing.T) {
	m := planApprovalFixture(t)
	m.provider = &fakeProvider{events: approvePlanEvents()}
	emitted := 0
	m.runtimeMessageSink = func(msg tea.Msg) {
		if _, ok := msg.(presentationStateMsg); ok {
			emitted++
		}
		if prompt, ok := msg.(permissionRequestMsg); ok {
			prompt.decide(agent.PermissionDecision{Action: agent.PermissionDecisionAllow})
		}
	}
	started, cmd := m.handleApproveCommand()
	if cmd == nil {
		t.Fatal("fixture did not start approval")
	}
	msg := execCmd(cmd)
	if _, ok := msg.(planExecutionResultMsg); !ok {
		t.Fatalf("unexpected result %T", msg)
	}
	if emitted == 0 {
		t.Fatal("fixture emitted no presentation states")
	}
	events, err := started.sessionStore.ReadEvents(started.activeSession.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		var payload map[string]json.RawMessage
		if json.Unmarshal(event.Payload, &payload) == nil && len(payload["presentation_state"]) > 0 {
			return
		}
	}
	t.Fatalf("approval emitted %d live snapshots but persisted zero presentation_state payloads", emitted)
}

func BenchmarkReviewWarmedView(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			m := benchTranscriptModel(n)
			m.narrationSettledGeneration = m.narrationVerbosityLevel
			width := m.chatColumnWidth()
			m.rebuildAltScreenSettledItems(width)
			measureTranscriptBodyItems(m.transcriptBodyItems(width, "", false), m.transcriptBodyHeights)
			m.altScreenSettledWidth = 0
			m.rebuildAltScreenSettledItems(width)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = m.View()
			}
		})
	}
}

func BenchmarkReviewWarmedKeypress(b *testing.B) {
	m := benchTranscriptModel(1000)
	m.pending = true
	m.pipeline.applyState(benchNodeState(10))
	key := tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	for i := 0; i < 3; i++ {
		next, _ := m.Update(key)
		m = next.(model)
		_ = m.View()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		next, _ := m.Update(key)
		m = next.(model)
		_ = m.View()
	}
}

func TestReviewClosingDiffRestoresTranscriptAfterNotice(t *testing.T) {
	m := mouseTestModel()
	m.width = 100
	m.height = 40
	m.altScreen = true
	m.transcript = []transcriptRow{{kind: rowUser, text: "ORIGINAL_CHAT_CONTENT"}, {kind: rowAssistant, text: "Original answer"}}
	settled, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = settled.(model)
	m.diffView = diffViewState{active: true, wt: worktrees.Result{Name: "review-lane", Path: t.TempDir()}, base: "main", text: "diff --git a/a b/a\n@@ -1 +1 @@\n+UNIQUE_PATCH_CONTENT"}
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	m = next.(model)
	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = next.(model)
	view := plainRender(t, m.View())
	if strings.Contains(view, "UNIQUE_PATCH_CONTENT") || !strings.Contains(view, "ORIGINAL_CHAT_CONTENT") {
		t.Fatal("after rejecting a hunk then closing diff, settled cache still displays the diff instead of the transcript")
	}
}
