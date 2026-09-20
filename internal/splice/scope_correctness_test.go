package splice

import (
	"context"
	"errors"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

type stubToolRunner struct {
	calls int
}

func (s *stubToolRunner) RunTool(_ context.Context, _ string, _ map[string]any) (ToolResult, error) {
	s.calls++
	return ToolResult{OK: true, Output: "inner"}, nil
}

// TestScopeNonInferiorityFailsLoudWhenSuppressionMeetsDecline is the
// adversarial fixture: the model self-reported "resolved", so the scope
// suppressed a call, and the run then lost correctness. The check must fail
// loud and record the omission as necessary.
func TestScopeNonInferiorityFailsLoudWhenSuppressionMeetsDecline(t *testing.T) {
	rec := NewSuppressionRecorder()
	rec.record("list_directory")
	baseline := schemas.IterationState{AcceptanceFactsPassing: 3, TestsPassing: 5}
	observed := schemas.IterationState{AcceptanceFactsPassing: 2, TestsPassing: 5}
	pair, err := CheckScopeNonInferiority(rec, baseline, observed)
	if !errors.Is(err, ErrScopeNonInferiority) {
		t.Fatalf("err = %v, want ErrScopeNonInferiority", err)
	}
	if pair.SuppressedCalls != 1 || pair.NecessaryCallsSuppressed != 1 {
		t.Fatalf("pair = %+v, want 1 suppressed and 1 necessary", pair)
	}
	if pair.Decline == "" {
		t.Fatal("the pairing must name the correctness decline")
	}
}

// TestScopeNonInferiorityPassesWithoutDecline pins the green path: suppression
// with steady correctness records no necessary-suppression.
func TestScopeNonInferiorityPassesWithoutDecline(t *testing.T) {
	rec := NewSuppressionRecorder()
	rec.record("list_directory")
	state := schemas.IterationState{AcceptanceFactsPassing: 3, TestsPassing: 5}
	pair, err := CheckScopeNonInferiority(rec, state, state)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if pair.NecessaryCallsSuppressed != 0 {
		t.Fatalf("necessary = %d, want 0", pair.NecessaryCallsSuppressed)
	}
}

// TestScopeNonInferiorityDeclineWithoutSuppressionIsNotScopeFault pins that a
// correctness decline with no host suppression is not attributed to the scope.
func TestScopeNonInferiorityDeclineWithoutSuppressionIsNotScopeFault(t *testing.T) {
	rec := NewSuppressionRecorder()
	pair, err := CheckScopeNonInferiority(rec, schemas.IterationState{TestsPassing: 5}, schemas.IterationState{TestsPassing: 4})
	if err != nil {
		t.Fatalf("err = %v, want nil (nothing was suppressed)", err)
	}
	if pair.NecessaryCallsSuppressed != 0 {
		t.Fatalf("necessary = %d, want 0", pair.NecessaryCallsSuppressed)
	}
}

// TestScopedToolRunnerRecordsSuppressedPair pins that the runner reports the
// suppression pair and that a runner without a recorder is unaffected.
func TestScopedToolRunnerRecordsSuppressedPair(t *testing.T) {
	inner := &stubToolRunner{}
	rec := NewSuppressionRecorder()
	runner := ScopedToolRunner{Inner: inner, Scope: StageScopePlan{AllowGlobalList: false}, Recorder: rec}
	if _, err := runner.RunTool(context.Background(), "list_directory", nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if inner.calls != 0 {
		t.Fatalf("inner ran %d time(s), want 0 (suppressed)", inner.calls)
	}
	if got := rec.Suppressed(); got != 1 {
		t.Fatalf("suppressed = %d, want 1", got)
	}
	if got := rec.Counts()["list_directory"]; got != 1 {
		t.Fatalf("list_directory count = %d, want 1", got)
	}

	// A runner without a recorder still suppresses and must not panic.
	bare := ScopedToolRunner{Inner: inner, Scope: StageScopePlan{AllowGlobalList: false}}
	if _, err := bare.RunTool(context.Background(), "list_directory", nil); err != nil {
		t.Fatalf("bare run: %v", err)
	}
	if inner.calls != 0 {
		t.Fatalf("inner ran %d time(s), want 0", inner.calls)
	}
}

func TestScopeCorrectnessPairingValidateRejectsImpossiblePair(t *testing.T) {
	bad := ScopeCorrectnessPairing{SuppressedCalls: 1, NecessaryCallsSuppressed: 2}
	if err := bad.Validate(); err == nil {
		t.Fatal("necessary > suppressed must fail validation")
	}
}

// TestRecordNecessarySuppressionsStampsExistingRows pins the W2 write-back: the
// paired necessary-suppression count lands only on invocation rows that already
// exist for that iteration, so it never fabricates a metric.
func TestRecordNecessarySuppressionsStampsExistingRows(t *testing.T) {
	tr := &runTraceAccumulator{stages: map[stageKey]schemas.InputMeta{
		stageKeyFor("code_writer", 1, 0): {GlobalListsSuppressed: 1},
		stageKeyFor("code_writer", 2, 0): {GlobalListsSuppressed: 2},
	}}
	tr.recordNecessarySuppressions(1, 3)
	if got := tr.stages[stageKeyFor("code_writer", 1, 0)].NecessaryCallsSuppressed; got != 3 {
		t.Fatalf("iteration 1 = %d, want 3", got)
	}
	if got := tr.stages[stageKeyFor("code_writer", 2, 0)].NecessaryCallsSuppressed; got != 0 {
		t.Fatalf("iteration 2 = %d, want 0 (untouched)", got)
	}
	if got := tr.stages[stageKeyFor("code_writer", 1, 0)].GlobalListsSuppressed; got != 1 {
		t.Fatalf("existing field clobbered: global lists = %d", got)
	}
	tr.recordNecessarySuppressions(99, 5)
	if len(tr.stages) != 2 {
		t.Fatalf("stamping a missing iteration must not add rows, got %d", len(tr.stages))
	}
}

// TestSuppressionRecorderWindowResets pins that the run-scoped recorder starts
// a fresh window per iteration, so the pairing reads the iteration's own count.
func TestSuppressionRecorderWindowResets(t *testing.T) {
	tr := &runTraceAccumulator{}
	rec := tr.suppressionRecorder()
	rec.record("list_directory")
	if rec.Suppressed() != 1 {
		t.Fatalf("suppressed = %d, want 1", rec.Suppressed())
	}
	tr.resetSuppressionRecorder()
	if got := tr.suppressionRecorder().Suppressed(); got != 0 {
		t.Fatalf("after reset suppressed = %d, want 0", got)
	}
}
