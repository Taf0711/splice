package splice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// traceMemoryStore implements both MemoryStore and TraceStore so a run can be
// exercised end-to-end with a fake sidecar and the written trace captured.
type traceMemoryStore struct {
	gotTrace *schemas.RunOutcome
}

func (s *traceMemoryStore) Search(context.Context, schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	return schemas.MemoryBundle{}, nil
}

func (s *traceMemoryStore) Upsert(context.Context, schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	return schemas.MemoryObservation{}, nil
}

func (s *traceMemoryStore) UpsertTrace(_ context.Context, trace schemas.RunOutcome) error {
	copy := trace
	s.gotTrace = &copy
	return nil
}

func (s *traceMemoryStore) UpsertVerdict(context.Context, schemas.VerdictRecord) error { return nil }

// recordingTraceStore records every trace write in order, so partial and final
// writes can be asserted separately.
type recordingTraceStore struct {
	traces []schemas.RunOutcome
}

func (s *recordingTraceStore) UpsertTrace(_ context.Context, trace schemas.RunOutcome) error {
	s.traces = append(s.traces, trace)
	return nil
}

func (s *recordingTraceStore) UpsertVerdict(context.Context, schemas.VerdictRecord) error { return nil }

// failingTraceStore fails every trace write, exercising the degraded path.
type failingTraceStore struct{}

func (s *failingTraceStore) UpsertTrace(context.Context, schemas.RunOutcome) error {
	return errors.New("trace write failed")
}

func (s *failingTraceStore) UpsertVerdict(context.Context, schemas.VerdictRecord) error { return nil }

// TestPersistPartialWritesRunningTrace pins the incremental write: after a
// stage completion, a partial trace with status "running" is written and
// carries the stage completed so far.
func TestPersistPartialWritesRunningTrace(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "add a Hello function",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 100, OutputMax: 100}}},
		TokenBudget:   schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"},
	}
	store := &recordingTraceStore{}
	tr := newRunTraceAccumulator(store, "run-1", "sess-1", "/repo", plan, "active")
	tr.recordStageCompletion(schemas.StageRecord{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1})
	tr.persistPartial(context.Background())

	if len(store.traces) != 1 {
		t.Fatalf("partial writes = %d, want 1", len(store.traces))
	}
	partial := store.traces[0]
	if partial.Outcome.Status != "running" {
		t.Fatalf("partial status = %q, want running", partial.Outcome.Status)
	}
	if len(partial.Stages) != 1 || partial.Stages[0].Name != "code_writer" {
		t.Fatalf("partial stages = %#v, want code_writer", partial.Stages)
	}
	if partial.EventsStatus != schemas.TraceEventsComplete {
		t.Fatalf("partial events_status = %q, want complete", partial.EventsStatus)
	}
	if err := partial.Validate(); err != nil {
		t.Fatalf("partial trace invalid: %v", err)
	}
}

// TestPersistPartialDegradesOnWriteFailure pins the degraded contract: a
// mid-run write failure never aborts the run, and the final trace records
// EventsStatus partial so consumers know events were lost.
func TestPersistPartialDegradesOnWriteFailure(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "add a Hello function",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 100, OutputMax: 100}}},
		TokenBudget:   schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"},
	}
	tr := newRunTraceAccumulator(&failingTraceStore{}, "run-1", "sess-1", "/repo", plan, "active")
	tr.recordStageCompletion(schemas.StageRecord{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1})
	tr.persistPartial(context.Background())
	if !tr.eventsPartial {
		t.Fatal("eventsPartial = false, want true after a partial write failure")
	}

	result := schemas.PipelineResult{
		RunID:  "run-1",
		Status: "completed",
		Tier:   plan.Tier,
		Stages: []schemas.StageRecord{{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1}},
	}
	trace, err := tr.buildRunOutcome(result)
	if err != nil {
		t.Fatalf("buildRunOutcome: %v", err)
	}
	if trace.EventsStatus != schemas.TraceEventsPartial {
		t.Fatalf("final events_status = %q, want partial", trace.EventsStatus)
	}
}

// TestSplitTestCounts pins the Q2 test split: a test declared in a file the
// test generator wrote this run is authored; everything else is preexisting.
// The aggregate totals are unchanged by the split.
func TestReplaceStageRecord(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "x",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 1, OutputMax: 1}}},
		TokenBudget:   schemas.TokenBudget{TotalInputBudget: 1, TotalOutputBudget: 1, OverflowPolicy: "abort"},
	}
	tr := newRunTraceAccumulator(&traceMemoryStore{}, "run-1", "sess-1", "/repo", plan, "active")
	tr.recordStageCompletion(schemas.StageRecord{Name: "code_writer", Iteration: 1, Status: schemas.StageCompleted})
	tr.recordStageCompletion(schemas.StageRecord{Name: "test_runner", Iteration: 1, Status: schemas.StageCompleted})

	// Replace on match, preserving order.
	tr.replaceStageRecord(schemas.StageRecord{Name: "code_writer", Iteration: 1, Status: schemas.StageCompleted, TokensInput: 200})
	if len(tr.completedStages) != 2 {
		t.Fatalf("completedStages = %d, want 2 (replaced, not appended)", len(tr.completedStages))
	}
	if tr.completedStages[0].TokensInput != 200 || tr.completedStages[0].Name != "code_writer" {
		t.Fatalf("code_writer record not replaced in place: %#v", tr.completedStages)
	}
	if tr.completedStages[1].Name != "test_runner" {
		t.Fatalf("order not preserved: %#v", tr.completedStages)
	}

	// Append on absence.
	tr.replaceStageRecord(schemas.StageRecord{Name: "static_analyzer", Iteration: 1, Status: schemas.StageCompleted})
	if len(tr.completedStages) != 3 || tr.completedStages[2].Name != "static_analyzer" {
		t.Fatalf("append on absence failed: %#v", tr.completedStages)
	}
}

func TestSplitTestCounts(t *testing.T) {
	authored := []schemas.FileChange{
		{Path: "add_test.go", ChangeType: "create", Content: "package add\n\nfunc TestAdd(t *testing.T) {}\n"},
	}
	results := []schemas.TestRunResults{{
		Command: []string{"go", "test"},
		Tests: []schemas.TestCaseResult{
			{Name: "TestAdd", Status: "passed"},
			{Name: "TestAdd/negative", Status: "failed"},
			{Name: "TestExisting", Status: "failed"},
			{Name: "TestErrored", Status: "errored"},
		},
	}}

	preexisting, authoredCounts := splitTestCounts(results, authored)

	if preexisting != (schemas.TestCounts{Fail: 1, Errored: 1}) {
		t.Fatalf("preexisting = %#v, want {Fail:1 Errored:1}", preexisting)
	}
	if authoredCounts != (schemas.TestCounts{Pass: 1, Fail: 1}) {
		t.Fatalf("authored = %#v, want {Pass:1 Fail:1}", authoredCounts)
	}
	// The aggregate is unchanged: the split is additive.
	total := schemas.TestCounts{
		Pass:    preexisting.Pass + authoredCounts.Pass,
		Fail:    preexisting.Fail + authoredCounts.Fail,
		Errored: preexisting.Errored + authoredCounts.Errored,
	}
	if total != (schemas.TestCounts{Pass: 1, Fail: 2, Errored: 1}) {
		t.Fatalf("aggregate = %#v, want {Pass:1 Fail:2 Errored:1}", total)
	}
}

// TestComputeIterationStateSplitsTests drives the split through
// ComputeIterationState with fabricated stage outputs.
func TestComputeIterationStateSplitsTests(t *testing.T) {
	outputs := []schemas.HarnessStageOutput{
		{
			Summary:    "generated tests",
			Confidence: 1,
			Data: map[string]any{
				"test_generator_output": schemas.TestGeneratorOutput{
					Files: []schemas.FileChange{
						{Path: "add_test.go", ChangeType: "create", Content: "func TestAdd(t *testing.T) {}"},
					},
					Language:   "go",
					Intent:     "add tests",
					Confidence: 0.9,
				},
			},
		},
		{
			Summary:    "ran tests",
			Confidence: 1,
			Data: map[string]any{
				"test_results": schemas.TestRunResults{
					Command: []string{"go", "test"},
					Tests: []schemas.TestCaseResult{
						{Name: "TestAdd", Status: "passed"},
						{Name: "TestOld", Status: "failed"},
					},
				},
			},
		},
	}
	state, err := ComputeIterationState(1, outputs, nil, schemas.ChangeSummary{}, nil)
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if state.TestsPassing != 1 || state.TestsFailing != 1 {
		t.Fatalf("aggregate = pass %d fail %d, want 1/1", state.TestsPassing, state.TestsFailing)
	}
	if state.Authored != (schemas.TestCounts{Pass: 1}) {
		t.Fatalf("authored = %#v, want {Pass:1}", state.Authored)
	}
	if state.Preexisting != (schemas.TestCounts{Fail: 1}) {
		t.Fatalf("preexisting = %#v, want {Fail:1}", state.Preexisting)
	}
}

// TestRunWritesCompleteTrace is the integration test: a mocked-provider run
// writes a complete, valid trace through the TraceStore seam.
func TestRunWritesCompleteTrace(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(workDir) {
		registry.Register(tool)
	}

	store := &traceMemoryStore{}
	result, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
	}, store, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("expected completed run, got incomplete: %s", result.IncompleteReason)
	}
	if store.gotTrace == nil {
		t.Fatal("no trace was written")
	}
	trace := *store.gotTrace
	if err := trace.Validate(); err != nil {
		t.Fatalf("written trace is invalid: %v", err)
	}
	if trace.Plan == nil || trace.Plan.RequestIntent != "add a Hello function and tests" {
		t.Fatalf("trace plan = %#v", trace.Plan)
	}
	if len(trace.Stages) == 0 || len(trace.Iterations) == 0 {
		t.Fatalf("trace stages=%d iterations=%d, want non-empty", len(trace.Stages), len(trace.Iterations))
	}
	if trace.Outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", trace.Outcome.Status)
	}
	abs, _ := filepath.Abs(workDir)
	if trace.RepoRoot != abs {
		t.Fatalf("repo root = %q, want %q", trace.RepoRoot, abs)
	}
	if trace.Memory.Status != "active" {
		t.Fatalf("memory status = %q, want active (memory store was provided)", trace.Memory.Status)
	}
}

// TestBuildRunOutcomeCoversEveryField is the producer-consumer pairing test: a
// synthetic run that exercises every dimension produces a RunOutcome with no
// zero-valued field. A new RunOutcome field that the writer forgets to
// populate fails this test, so CI fails on drift.
func TestBuildRunOutcomeCoversEveryField(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "add a Hello function",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 100, OutputMax: 100}}},
		TokenBudget:   schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"},
	}

	tr := newRunTraceAccumulator(&traceMemoryStore{}, "run-1", "sess-1", "/repo", plan, "active")
	tr.recordHistory(schemas.IterationState{
		Iteration: 1, Timestamp: 1, TestsPassing: 1, StateHash: "abc",
		Confidence: 0.9, Preexisting: schemas.TestCounts{Pass: 1},
	})
	tr.recordMemory("code_writer", 1, schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{{OwnerAgent: "orchestrator", Title: "t", Content: "c", MemoryType: "run_config", Scope: "project", Visibility: "shareable"}},
		Exemplars:       []schemas.Exemplar{{RunID: "run-ex", Content: "exemplar"}},
	})
	tr.recordContext("code_writer", 1, schemas.ContextBundle{
		Request: schemas.ContextRequest{Reason: "inspect", Queries: []schemas.ContextQuery{{QueryType: schemas.ContextListFiles, MaxResults: 1, MaxChars: 100}}},
		Items:   []schemas.ContextItem{{Query: schemas.ContextQuery{QueryType: schemas.ContextListFiles, MaxResults: 1, MaxChars: 100}, Summary: "one file"}},
	})
	tr.recordEdge("code_writer", 1, 42)
	tr.interventions = append(tr.interventions, schemas.InterventionRecord{
		Type: schemas.InterventionPermissionTap, Weight: 1, Stage: "code_writer",
		Iteration: 1, Summary: "bash: run tests", Choice: "allow",
	})
	// LN2 bucket-key identities and provenance.
	tr.toolFingerprint = "toolfp"
	tr.topologyHash = "topohash"
	tr.stagePromptHash = map[string]string{"code_writer": "prompthash"}
	tr.budgetProvenance = map[string]string{"code_writer": "calibrated from 25 runs, p80"}

	result := schemas.PipelineResult{
		RunID:  "run-1",
		Status: "completed",
		Tier:   plan.Tier,
		Stages: []schemas.StageRecord{{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1}},
	}

	trace, err := tr.buildRunOutcome(result)
	if err != nil {
		t.Fatalf("buildRunOutcome: %v", err)
	}

	v := reflect.ValueOf(trace)
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.IsZero() {
			t.Errorf("RunOutcome.%s is zero; the trace writer does not populate it", v.Type().Field(i).Name)
		}
	}
	// The per-stage prompt hash must also be written, not just the run-level
	// key fields. (Cache is legitimately zero in v1, so TracedStage is not
	// reflected field-by-field the way RunOutcome is.)
	if len(trace.Stages) == 0 {
		t.Fatal("expected at least one traced stage")
	}
	if trace.Stages[0].PromptHash != "prompthash" {
		t.Errorf("TracedStage.PromptHash = %q, want prompthash", trace.Stages[0].PromptHash)
	}
	if trace.Stages[0].InputMeta.ExemplarItems != 1 {
		t.Errorf("TracedStage.InputMeta.ExemplarItems = %d, want 1", trace.Stages[0].InputMeta.ExemplarItems)
	}
}
