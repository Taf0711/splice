package splice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// ---------------------------------------------------------------------------
// Fake WorkspaceRecovery for integration tests
// ---------------------------------------------------------------------------

type recoveryCall struct {
	method      string // "Capture" or "Restore"
	runID       string
	iteration   int
	expectedRef string
	targetRef   string
}

type fakeRecovery struct {
	calls     []recoveryCall
	captureFn func(ctx context.Context, runID string, iteration int) (string, error)
	restoreFn func(ctx context.Context, expectedCurrentRef, targetRef string) error
}

func newFakeRecovery() *fakeRecovery {
	return &fakeRecovery{}
}

func (r *fakeRecovery) Capture(ctx context.Context, runID string, iteration int) (string, error) {
	if r.captureFn != nil {
		return r.captureFn(ctx, runID, iteration)
	}
	ref := fmt.Sprintf("refs/fake/%s/%d", runID, iteration)
	r.calls = append(r.calls, recoveryCall{method: "Capture", runID: runID, iteration: iteration, targetRef: ref})
	return ref, nil
}

func (r *fakeRecovery) Restore(ctx context.Context, expectedCurrentRef, targetRef string) error {
	r.calls = append(r.calls, recoveryCall{method: "Restore", expectedRef: expectedCurrentRef, targetRef: targetRef})
	if r.restoreFn != nil {
		return r.restoreFn(ctx, expectedCurrentRef, targetRef)
	}
	return nil
}

// failCapture returns a fakeRecovery whose Capture always fails.
func failCapture(err error) *fakeRecovery {
	return &fakeRecovery{
		captureFn: func(ctx context.Context, runID string, iteration int) (string, error) {
			return "", err
		},
	}
}

// captureCount returns the number of Capture calls.
func (r *fakeRecovery) captureCount() int {
	n := 0
	for _, c := range r.calls {
		if c.method == "Capture" {
			n++
		}
	}
	return n
}

func (r *fakeRecovery) restored() bool {
	for _, call := range r.calls {
		if call.method == "Restore" {
			return true
		}
	}
	return false
}

type trajectoryRecoveryStage struct {
	calls          int
	recovery       *fakeRecovery
	restoreWasLate bool
}

func (s *trajectoryRecoveryStage) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.calls++
	if s.calls == 4 && (s.recovery == nil || !s.recovery.restored()) {
		s.restoreWasLate = true
	}
	passing, failing := 0, 0
	switch s.calls {
	case 1:
		passing, failing = 10, 1
	case 2:
		passing, failing = 11, 1
	case 3:
		failing = 2
	default:
		passing = 1
	}
	tests := make([]schemas.TestCaseResult, 0, passing+failing)
	for i := 0; i < passing; i++ {
		tests = append(tests, schemas.TestCaseResult{Name: fmt.Sprintf("pass-%d", i), Status: "passed"})
	}
	for i := 0; i < failing; i++ {
		tests = append(tests, schemas.TestCaseResult{Name: fmt.Sprintf("fail-%d", i), Status: "failed"})
	}
	return schemas.HarnessStageOutput{
		Summary:    fmt.Sprintf("trajectory iteration %d", s.calls),
		Confidence: 0.9,
		Data: map[string]any{
			"test_results": schemas.TestRunResults{Command: []string{"test"}, Tests: tests},
			"code_writer_output": schemas.CodeWriterOutput{Files: []schemas.FileChange{{
				Path: "state.txt", ChangeType: "modify", Content: fmt.Sprintf("state-%d", s.calls),
			}}},
		},
	}, nil
}

func trajectoryRecoveryPlan() schemas.ExecutionPlan {
	return schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "exercise trajectory rollback",
		Stages:        []schemas.ExecutionStage{{Name: "trajectory_stage"}},
		TokenBudget: schemas.TokenBudget{
			TotalInputBudget:  1000,
			TotalOutputBudget: 1000,
		},
	}
}

// ---------------------------------------------------------------------------
// selectBestSnapshot unit tests
// ---------------------------------------------------------------------------

func TestSelectBestSnapshotReturnsHighestScore(t *testing.T) {
	snapshots := []snapshot{
		{ref: "iter0", iter: 0, score: 0},
		{ref: "iter1", iter: 1, score: 50},
		{ref: "iter2", iter: 2, score: 80},
	}
	best, ok := selectBestSnapshot(snapshots, 3)
	if !ok || best.ref != "iter2" || best.score != 80 {
		t.Fatalf("best = %+v, ok = %v, want {iter2, 80}", best, ok)
	}
}

func TestSelectBestSnapshotTiePicksLatest(t *testing.T) {
	snapshots := []snapshot{
		{ref: "iter0", iter: 0, score: 50},
		{ref: "iter1", iter: 1, score: 50},
		{ref: "iter2", iter: 2, score: 50},
	}
	best, ok := selectBestSnapshot(snapshots, 3)
	if !ok || best.ref != "iter2" || best.iter != 2 {
		t.Fatalf("best = %+v, ok = %v, want latest among ties {iter2, 50}", best, ok)
	}
}

func TestSelectBestSnapshotExcludesCurrentIteration(t *testing.T) {
	snapshots := []snapshot{
		{ref: "iter0", iter: 0, score: 10},
		{ref: "iter1", iter: 1, score: 100},
	}
	best, ok := selectBestSnapshot(snapshots, 1)
	if !ok || best.ref != "iter0" || best.score != 10 {
		t.Fatalf("best = %+v, ok = %v, want {iter0, 10} (iter1 excluded)", best, ok)
	}
}

func TestSelectBestSnapshotFallsBackToIter0(t *testing.T) {
	snapshots := []snapshot{
		{ref: "iter0", iter: 0, score: 0},
	}
	best, ok := selectBestSnapshot(snapshots, 1)
	if !ok || best.ref != "iter0" || best.iter != 0 {
		t.Fatalf("best = %+v, ok = %v, want iter0", best, ok)
	}
}

func TestSelectBestSnapshotPrefersNegativeCompletedScoreOverBaseline(t *testing.T) {
	snapshots := []snapshot{
		{ref: "iter0", iter: 0, score: 0},
		{ref: "iter1", iter: 1, score: -100},
		{ref: "iter2", iter: 2, score: -50},
	}
	best, ok := selectBestSnapshot(snapshots, 3)
	if !ok || best.ref != "iter2" {
		t.Fatalf("best = %+v, ok = %v, want iter2", best, ok)
	}
}

func TestRunIterationLoopRestoresBestSnapshotBeforeNextPass(t *testing.T) {
	recovery := newFakeRecovery()
	stage := &trajectoryRecoveryStage{recovery: recovery}
	result, err := runIterationLoop(
		context.Background(),
		"rollback-run",
		trajectoryRecoveryPlan(),
		stageRegistry{"trajectory_stage": stage},
		runFakeProvider{},
		agent.Options{Cwd: t.TempDir(), MaxTurns: 4},
		t.TempDir(),
		nil,
		nil,
		recovery,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q, abort reason = %v", result.Status, result.AbortReason)
	}
	if stage.calls != 4 || stage.restoreWasLate {
		t.Fatalf("stage calls = %d, restoreWasLate = %v", stage.calls, stage.restoreWasLate)
	}
	var restore recoveryCall
	for _, call := range recovery.calls {
		if call.method == "Restore" {
			restore = call
			break
		}
	}
	if restore.expectedRef != "refs/fake/rollback-run/3" || restore.targetRef != "refs/fake/rollback-run/2" {
		t.Fatalf("restore call = %+v, want iteration 3 to iteration 2", restore)
	}
}

func TestRunIterationLoopStopsWhenRestoreFails(t *testing.T) {
	recovery := newFakeRecovery()
	recovery.restoreFn = func(context.Context, string, string) error {
		return errors.New("synthetic restore failure")
	}
	stage := &trajectoryRecoveryStage{recovery: recovery}
	result, err := runIterationLoop(
		context.Background(),
		"rollback-run",
		trajectoryRecoveryPlan(),
		stageRegistry{"trajectory_stage": stage},
		runFakeProvider{},
		agent.Options{Cwd: t.TempDir(), MaxTurns: 4},
		t.TempDir(),
		nil,
		nil,
		recovery,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "failed" || result.AbortReason == nil || !strings.Contains(*result.AbortReason, "synthetic restore failure") {
		t.Fatalf("result = %+v, want failed restore reason", result)
	}
	if stage.calls != 3 {
		t.Fatalf("stage calls = %d, want 3 with no retry after restore failure", stage.calls)
	}
}

func TestRunIterationLoopAbortsRollbackWithoutRecovery(t *testing.T) {
	stage := &trajectoryRecoveryStage{}
	result, err := runIterationLoop(
		context.Background(),
		"rollback-run",
		trajectoryRecoveryPlan(),
		stageRegistry{"trajectory_stage": stage},
		runFakeProvider{},
		agent.Options{Cwd: t.TempDir(), MaxTurns: 4},
		t.TempDir(),
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "aborted" || result.AbortReason == nil || !strings.Contains(*result.AbortReason, "--worktree") {
		t.Fatalf("result = %+v, want aborted isolated-worktree reason", result)
	}
	if stage.calls != 3 {
		t.Fatalf("stage calls = %d, want 3 with no post-rollback mutation", stage.calls)
	}
}

// ---------------------------------------------------------------------------
// Snapshot ordering: iteration 0 captured before loop, then each completed
// iteration after state computation.
// ---------------------------------------------------------------------------

func TestRecoveryCapturesIter0BeforeLoop(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	rec := newFakeRecovery()

	_, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
	}, nil, rec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rec.captureCount() < 2 {
		t.Fatalf("capture count = %d, want at least 2 (iter0 + iter1)", rec.captureCount())
	}
	if rec.calls[0].method != "Capture" || rec.calls[0].iteration != 0 {
		t.Fatalf("first call = %+v, want Capture(iter=0)", rec.calls[0])
	}
	foundIter1 := false
	for _, c := range rec.calls {
		if c.method == "Capture" && c.iteration == 1 {
			foundIter1 = true
			break
		}
	}
	if !foundIter1 {
		t.Fatalf("expected Capture(iter=1) call, got calls=%+v", rec.calls)
	}
}

// ---------------------------------------------------------------------------
// Nil recovery does not crash the pipeline.
// ---------------------------------------------------------------------------

func TestRecoveryNilRecoveryDoesNotPanic(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	result, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run returned Go error: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("expected completed result, got incomplete: %s", result.IncompleteReason)
	}
}

// ---------------------------------------------------------------------------
// Capture errors propagate in-band via PipelineResult.AbortReason.
// ---------------------------------------------------------------------------

func TestRecoveryCaptureErrorStopsPipeline(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	rec := failCapture(errors.New("disk full"))

	result, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
	}, nil, rec)
	if err != nil {
		t.Fatalf("Run returned Go error: %v (expected in-band failure)", err)
	}

	var pipeline schemas.PipelineResult
	if jErr := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); jErr != nil {
		t.Fatalf("parse final answer: %v", jErr)
	}

	if pipeline.Status == "completed" {
		t.Fatal("expected pipeline to fail from capture error")
	}
	if pipeline.AbortReason == nil || !strings.Contains(*pipeline.AbortReason, "disk full") {
		t.Fatalf("abort reason = %v, want disk full", pipeline.AbortReason)
	}
}

// ---------------------------------------------------------------------------
// Context cancellation during capture propagates as context.Canceled.
// ---------------------------------------------------------------------------

func TestRecoveryCaptureCancellationStopsPipeline(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := newFakeRecovery()
	_, err := Run(ctx, "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MaxTurns:       1,
	}, nil, rec)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
