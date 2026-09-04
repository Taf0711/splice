package splice

// Adversarial tests for the repair no-progress guard (A3), the repair
// instruction direction fix (A5 extension), and the focused repair payload.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestRepairProgressStopsOnRepeatedSameFingerprint(t *testing.T) {
	state := newRepairProgressState()
	if repeated, _ := state.observe("fp-1", []string{"ev-1"}, nil); repeated {
		t.Fatal("the baseline observation must not report a repeat")
	}
	// Same fingerprint, same evidence after the baseline: the directive's
	// stop condition after one evidence-informed retry.
	if repeated, _ := state.observe("fp-1", []string{"ev-1"}, nil); !repeated {
		t.Fatal("repeated same fingerprint with no new evidence must be reported")
	}
}

func TestRepairProgressNewEvidencePermitsOneMore(t *testing.T) {
	state := newRepairProgressState()
	state.observe("fp-1", []string{"ev-1"}, nil)
	state.observe("fp-1", []string{"ev-1"}, nil)
	// New evidence arrives: not a repeat, and the stall count resets.
	if repeated, stalled := state.observe("fp-1", []string{"ev-2"}, nil); repeated || stalled != 0 {
		t.Fatalf("new evidence must reset the stall, got repeated=%v stalled=%d", repeated, stalled)
	}
	if repeated, stalled := state.observe("fp-1", []string{"ev-2"}, nil); !repeated || stalled != 1 {
		t.Fatalf("same fingerprint and stale evidence again must be a stall, got repeated=%v stalled=%d", repeated, stalled)
	}
}

func TestRepairProgressFingerprintChangeContinues(t *testing.T) {
	state := newRepairProgressState()
	state.observe("fp-1", []string{"ev-1"}, nil)
	state.observe("fp-1", []string{"ev-1"}, nil)
	if repeated, stalled := state.observe("fp-2", []string{"ev-1"}, nil); repeated || stalled != 0 {
		t.Fatalf("a changed fingerprint must continue the loop, got repeated=%v stalled=%d", repeated, stalled)
	}
}

func TestRepairProgressRepeatWritesCountTowardStall(t *testing.T) {
	state := newRepairProgressState()
	hashes := map[string]string{"main.go": "aaaa"}
	state.observe("fp-1", []string{"ev-1"}, hashes)
	// A byte-identical repeat write is no-progress evidence even when the
	// evidence text changed: it feeds the stall counter.
	if repeated, stalled := state.observe("fp-1", []string{"ev-2"}, map[string]string{"main.go": "aaaa"}); repeated || stalled != 1 {
		t.Fatalf("a byte-identical repeat write must count as one stall, got repeated=%v stalled=%d", repeated, stalled)
	}
	// A changed write with a changed fingerprint resets the stall.
	if _, stalled := state.observe("fp-2", []string{"ev-3"}, map[string]string{"main.go": "bbbb"}); stalled != 0 {
		t.Fatalf("a real change must reset the stall, got %d", stalled)
	}
}

// A full attemptLocalRepair run whose test runner returns the identical
// failure must stop early with repair_no_progress surfaced in the stream, and
// must run fewer than the max repair re-entries.
func TestAttemptLocalRepairNoProgressStopsLoop(t *testing.T) {
	runner := &identicalFailureRunner{}
	registry := stageRegistry{"code_writer": &scriptedWriter{}, "test_runner": runner}
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}}}
	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	initial := runner.current()

	var events []string
	options := PipelineRunConfig{OnStageEvent: func(event agent.StageEvent) {
		events = append(events, event.Status+" "+event.Detail)
	}}

	repaired, interaction, err := attemptLocalRepair(
		context.Background(), "run-noprogress", 1, plan, registry, nil,
		options, t.TempDir(), nil, nil, nil,
		time.Now().Add(time.Minute), &records, &outputs,
		&priorSummaries, &priorChangedFiles, initial,
	)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if repaired || (interaction != nil && interaction.Resolved) {
		t.Fatalf("repaired = %v, want an unresolved no-progress stop", repaired)
	}
	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, repairNoProgressReason) {
		t.Fatalf("events must surface %s, got:\n%s", repairNoProgressReason, joined)
	}
	// The guard stopped the loop before the budget was consumed.
	if runner.runs > maxLocalRepairs {
		t.Fatalf("test_runner runs = %d, want <= %d after the guard fired", runner.runs, maxLocalRepairs)
	}
}

// identicalFailureRunner returns the byte-identical failing payload every
// run, modeling the fam-07 thrash: same failure, no new evidence.
type identicalFailureRunner struct{ runs int }

func (r *identicalFailureRunner) Capabilities() stages.Capabilities {
	return stages.Capabilities{ModelFree: true, Description: "running tests"}
}

func (r *identicalFailureRunner) Run(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
	r.runs++
	return r.current(), nil
}

func (r *identicalFailureRunner) current() schemas.HarnessStageOutput {
	return schemas.HarnessStageOutput{
		Summary: "tests failed",
		Data: map[string]any{"test_results": schemas.TestRunResults{
			Command:  []string{"go", "test", "./..."},
			ExitCode: 1,
			Tests: []schemas.TestCaseResult{{
				Name:    "TestStillFails",
				Status:  "failed",
				Message: "identical assertion text",
			}},
		}},
	}
}

// The guard cannot be exceeded: even with fresh evidence each round, total
// repairs stay <= maxLocalRepairs.
func TestAttemptLocalRepairBudgetCannotBeExceeded(t *testing.T) {
	writer := &approachWriter{}
	runner := &freshFailureRunner{}
	registry := stageRegistry{"code_writer": writer, "test_runner": runner}
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}}}
	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	initial := runner.current()

	_, interaction, err := attemptLocalRepair(
		context.Background(), "run-budget", 1, plan, registry, nil,
		PipelineRunConfig{}, t.TempDir(), nil, nil, nil,
		time.Now().Add(time.Minute), &records, &outputs,
		&priorSummaries, &priorChangedFiles, initial,
	)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if interaction == nil {
		t.Fatal("interaction = nil, want an exhausted record")
	}
	if interaction.Repairs > maxLocalRepairs {
		t.Fatalf("repairs = %d, want <= %d", interaction.Repairs, maxLocalRepairs)
	}
	if writer.calls > maxLocalRepairs {
		t.Fatalf("writer calls = %d, want <= %d", writer.calls, maxLocalRepairs)
	}
}

// Instruction direction: failing tests declared in writer-authored test
// files flip the instruction to the test-file directive; failures elsewhere
// keep the implementation directive.
func TestRepairInstructionDirectionFollowsAuthorship(t *testing.T) {
	writerOutput := schemas.HarnessStageOutput{
		Data: map[string]any{"code_writer_output": schemas.CodeWriterOutput{
			Files: []schemas.FileChange{
				{Path: "lookup_table_test.go", Content: "package main\n\nfunc TestLookup(t *testing.T) {}\n", ChangeType: "create"},
			},
		}},
	}

	got := repairInstructionDirection([]string{"TestLookup"}, "--- FAIL: TestLookup (0.00s)", writerOutput)
	if got != repairTestFileInstruction {
		t.Fatalf("instruction = %q, want the test-file directive", got)
	}

	got = repairInstructionDirection([]string{"TestPreexisting"}, "--- FAIL: TestPreexisting (0.00s)", writerOutput)
	if got != repairInstruction {
		t.Fatalf("instruction = %q, want the implementation directive for non-authored failures", got)
	}

	// No authored test files: generic instruction.
	got = repairInstructionDirection([]string{"TestLookup"}, "--- FAIL: TestLookup", schemas.HarnessStageOutput{})
	if got != repairInstruction {
		t.Fatalf("instruction = %q, want the generic directive with no authored tests", got)
	}

	// Compile errors that name a writer-authored file point at the test file.
	got = repairInstructionDirection(nil, "./lookup_table_test.go:45:13: undefined: newSessionStore", writerOutput)
	if got != repairTestFileInstruction {
		t.Fatalf("instruction = %q, want the test-file directive for authored-file compile errors", got)
	}

	// Compile errors in files the writer did not author keep the generic one.
	got = repairInstructionDirection(nil, "./session.go:9:2: undefined: NewStore", writerOutput)
	if got != repairInstruction {
		t.Fatalf("instruction = %q, want the implementation directive for non-authored compile errors", got)
	}
}

// approachWriter produces a distinct summary each call, so every repair
// attempt registers a fresh approach.
type approachWriter struct{ calls int }

func (w *approachWriter) Capabilities() stages.Capabilities {
	return stages.Capabilities{ModelFree: false, Description: "writing code changes"}
}

func (w *approachWriter) Run(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
	w.calls++
	return schemas.HarnessStageOutput{
		Summary:    "attempt " + time.Now().Format("150405.000000000"),
		Confidence: 0.9,
	}, nil
}

// freshFailureRunner returns a DIFFERENT failing test name each run, so the
// fingerprint and evidence always change and only the budget can stop the
// loop.
type freshFailureRunner struct{ runs int }

func (r *freshFailureRunner) Capabilities() stages.Capabilities {
	return stages.Capabilities{ModelFree: true, Description: "running tests"}
}

func (r *freshFailureRunner) Run(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
	r.runs++
	status := "failed"
	if r.runs == 1 {
		// The initial payload comes from current() so the first Run call is
		// the post-repair rerun.
		status = "failed"
	}
	return schemas.HarnessStageOutput{
		Summary: "tests failed",
		Data: map[string]any{"test_results": schemas.TestRunResults{
			Command:  []string{"go", "test", "./..."},
			ExitCode: 1,
			Tests: []schemas.TestCaseResult{{
				Name:    "TestFreshFailure" + time.Now().Format("150405.000000000"),
				Status:  status,
				Message: "fresh assertion text",
			}},
		}},
	}, nil
}

func (r *freshFailureRunner) current() schemas.HarnessStageOutput {
	return schemas.HarnessStageOutput{
		Summary: "tests failed",
		Data: map[string]any{"test_results": schemas.TestRunResults{
			Command:  []string{"go", "test", "./..."},
			ExitCode: 1,
			Tests: []schemas.TestCaseResult{{
				Name:    "TestFreshFailureInitial",
				Status:  "failed",
				Message: "fresh assertion text",
			}},
		}},
	}
}

// The second repair invocation must receive the resolver output in its
// revision context: symbols, lookups, and the failure fingerprint.
func TestAttemptLocalRepairSecondAttemptReceivesResolverEvidence(t *testing.T) {
	workspace := workspaceFixture(t)
	var writerInputs []schemas.HarnessStageInput
	registry := stageRegistry{
		"code_writer": stageFunc(func(_ context.Context, input schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
			writerInputs = append(writerInputs, input)
			return schemas.HarnessStageOutput{Summary: "revision applied", Confidence: 0.9}, nil
		}),
		"test_runner": stageFunc(func(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
			return schemas.HarnessStageOutput{
				Summary: "tests failed",
				Data: map[string]any{"test_results": schemas.TestRunResults{
					Command:  []string{"go", "test", "./..."},
					ExitCode: 1,
					Tests: []schemas.TestCaseResult{{
						Name:    "TestUsesStore",
						Status:  "failed",
						Message: "./main.go:5: store.Commit undefined (type *Store has no field or method Commit)",
					}},
				}},
			}, nil
		}),
	}
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}}}
	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	initial := schemas.HarnessStageOutput{
		Summary: "tests failed",
		Data: map[string]any{"test_results": schemas.TestRunResults{
			Command:  []string{"go", "test", "./..."},
			ExitCode: 1,
			Tests: []schemas.TestCaseResult{{
				Name:    "TestUsesStore",
				Status:  "failed",
				Message: "./main.go:5: store.Commit undefined (type *Store has no field or method Commit)",
			}},
		}},
	}

	_, _, err := attemptLocalRepair(
		context.Background(), "run-resolver", 1, plan, registry, nil,
		PipelineRunConfig{}, workspace, nil, nil, nil,
		time.Now().Add(time.Minute), &records, &outputs,
		&priorSummaries, &priorChangedFiles, initial,
	)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(writerInputs) == 0 {
		t.Fatal("no writer re-entry captured")
	}
	rev := writerInputs[len(writerInputs)-1].RevisionContext
	if rev == nil {
		t.Fatal("revision context = nil, want the focused payload")
	}
	for _, want := range []string{
		"Failure fingerprint:",
		"Resolved symbols:",
		"Commit",
		"methods: Load, Save",
		"Exact failure:",
		"TestUsesStore",
	} {
		if !strings.Contains(*rev, want) {
			t.Fatalf("revision context missing %q:\n%s", want, *rev)
		}
	}
}
