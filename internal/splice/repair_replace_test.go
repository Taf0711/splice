package splice

// Adversarial tests for the repair loop's output accounting: the post-repair
// rerun must REPLACE the stale failing test_runner payload in passOutputs (not
// append), or typedPayloads counts the old suite forever and a fully repaired
// pass aborts on budget.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// scriptedTestRunner fails until it has run failTimes+1 times, then passes.
// It records every invocation so tests can prove exactly how many reruns ran.
type scriptedTestRunner struct {
	failTimes int
	runs      int
}

func (s *scriptedTestRunner) Capabilities() stages.Capabilities {
	return stages.Capabilities{ModelFree: true, Description: "running tests"}
}

func (s *scriptedTestRunner) Run(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
	s.runs++
	failed := s.runs <= s.failTimes
	return schemas.HarnessStageOutput{
		Summary:    summaryFor(failed),
		Confidence: 1.0,
		Data:       testResultsPayloadVarying(failed, s.runs),
	}, nil
}

func testResultsPayload(failed bool) map[string]any {
	return testResultsPayloadVarying(failed, 0)
}

// testResultsPayloadVarying varies the failing test name per run so a
// genuinely progressing repair produces new failure evidence (the A3 guard
// requires new evidence to allow the next repair).
func testResultsPayloadVarying(failed bool, run int) map[string]any {
	status := "passed"
	if failed {
		status = "failed"
	}
	name := "TestPasses"
	if failed {
		name = fmt.Sprintf("TestStillFails%d", run)
	}
	return map[string]any{
		"test_results": schemas.TestRunResults{
			Command:  []string{"true"},
			ExitCode: map[bool]int{false: 0, true: 1}[failed],
			Tests: []schemas.TestCaseResult{{
				Name:   name,
				Status: status,
			}},
		},
	}
}

func summaryFor(failed bool) string {
	if failed {
		return "Test command failed with exit code 1."
	}
	return "Test command passed."
}

func TestRepairReplacesStaleFailingOutputSoSuccessIsVisible(t *testing.T) {
	runner := &scriptedTestRunner{failTimes: 1} // first rerun still fails, second passes
	registry := stageRegistry{
		"code_writer": &scriptedWriter{},
		"test_runner": runner,
	}
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}}}

	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	initial := schemas.HarnessStageOutput{
		Summary: "Test command failed with exit code 1.",
		Data:    testResultsPayload(true),
	}

	repaired, interaction, err := attemptLocalRepair(
		context.Background(), "run-replace", 1, plan, registry, nil,
		PipelineRunConfig{}, t.TempDir(), nil, nil, nil,
		time.Now().Add(time.Minute), &records, &outputs,
		&priorSummaries, &priorChangedFiles, initial,
	)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !repaired || interaction == nil || !interaction.Resolved {
		t.Fatalf("repaired = %v interaction = %+v, want resolved", repaired, interaction)
	}
	if runner.runs != 2 {
		t.Fatalf("test_runner runs = %d, want 2 (fail then pass)", runner.runs)
	}

	// The adversarial pin: exactly one test_results payload survives, and it
	// is the passing one. An appended stale suite would double-count failure.
	var payloads []schemas.TestRunResults
	for _, output := range outputs {
		if raw, ok := output.Data["test_results"]; ok {
			payloads = append(payloads, raw.(schemas.TestRunResults))
		}
	}
	if len(payloads) != 1 {
		t.Fatalf("test_results payloads = %d, want exactly 1", len(payloads))
	}
	if payloads[0].Failed() != 0 {
		t.Fatalf("surviving payload must be the passing suite, got %+v", payloads[0])
	}

	state, err := ComputeIterationState(1, outputs, records, schemas.ChangeSummary{}, nil)
	if err != nil {
		t.Fatalf("iteration state: %v", err)
	}
	if state.TestsFailing != 0 || state.TestsErrored != 0 {
		t.Fatalf("state = failing %d errored %d, want a clean pass", state.TestsFailing, state.TestsErrored)
	}
	if !passSucceeded(records, state) {
		t.Fatal("passSucceeded must be true after a successful repair")
	}
}

func TestExhaustedRepairsKeepExactlyOneFailingPayload(t *testing.T) {
	runner := &scriptedTestRunner{failTimes: maxLocalRepairs + 10} // never passes
	registry := stageRegistry{
		"code_writer": &scriptedWriter{},
		"test_runner": runner,
	}
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}}}

	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	initial := schemas.HarnessStageOutput{
		Summary: "Test command failed with exit code 1.",
		Data:    testResultsPayload(true),
	}

	repaired, interaction, err := attemptLocalRepair(
		context.Background(), "run-exhausted", 1, plan, registry, nil,
		PipelineRunConfig{}, t.TempDir(), nil, nil, nil,
		time.Now().Add(time.Minute), &records, &outputs,
		&priorSummaries, &priorChangedFiles, initial,
	)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if repaired || interaction.Resolved {
		t.Fatalf("repaired = %v resolved = %v, want exhausted unresolved", repaired, interaction.Resolved)
	}
	// The scripted runner varies the failing test name per run, so each
	// retry sees new evidence and the full repair budget is consumed. The
	// no-progress stop is pinned separately in repair_progress_test.go.
	if interaction.Repairs != maxLocalRepairs {
		t.Fatalf("repairs = %d, want %d", interaction.Repairs, maxLocalRepairs)
	}

	var failing int
	for _, output := range outputs {
		if raw, ok := output.Data["test_results"]; ok {
			if raw.(schemas.TestRunResults).Failed() > 0 {
				failing++
			}
		}
	}
	if failing != 1 {
		t.Fatalf("failing payloads = %d, want exactly 1 (replace semantics hold when exhausted too)", failing)
	}
	state, err := ComputeIterationState(1, outputs, records, schemas.ChangeSummary{}, nil)
	if err != nil {
		t.Fatalf("iteration state: %v", err)
	}
	if state.TestsFailing == 0 {
		t.Fatal("exhausted repairs must keep TestsFailing above zero")
	}
}

func TestRepairEmitsLabeledRerunAndExhaustionEvents(t *testing.T) {
	runner := &scriptedTestRunner{failTimes: maxLocalRepairs + 10}
	registry := stageRegistry{"code_writer": &scriptedWriter{}, "test_runner": runner}
	plan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}}}
	var events []string
	options := PipelineRunConfig{OnStageEvent: func(event agent.StageEvent) {
		events = append(events, event.Status+" "+event.Detail)
	}}

	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	initial := schemas.HarnessStageOutput{Summary: "fail", Data: testResultsPayload(true)}

	_, _, _ = attemptLocalRepair(
		context.Background(), "run-events", 1, plan, registry, nil,
		options, t.TempDir(), nil, nil, nil,
		time.Now().Add(time.Minute), &records, &outputs,
		&priorSummaries, &priorChangedFiles, initial,
	)

	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, "repair re-entry") {
		t.Fatalf("events must label the rerun, got:\n%s", joined)
	}
	// The varied-failure runner consumes the full budget: exhaustion must
	// stay distinguishable from a no-progress stop in streams.
	if !strings.Contains(joined, "repair_exhausted") {
		t.Fatalf("events must distinguish exhaustion, got:\n%s", joined)
	}
	if strings.Contains(joined, repairNoProgressReason) {
		t.Fatalf("varying evidence must not trigger the no-progress stop, got:\n%s", joined)
	}
}

// scriptedWriter stands in for the code_writer re-entry; it produces a valid
// completed output without touching files.
type scriptedWriter struct{}

func (s *scriptedWriter) Run(_ context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, _ stages.StageOptions) (schemas.HarnessStageOutput, error) {
	return schemas.HarnessStageOutput{
		Summary:    "revision applied",
		Confidence: 0.9,
	}, nil
}

func (s *scriptedWriter) Capabilities() stages.Capabilities {
	return stages.Capabilities{ModelFree: false, Description: "writing code changes"}
}
