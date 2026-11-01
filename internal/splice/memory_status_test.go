package splice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// erroringSearchStore resolves (implements TraceStore) but every memory Search
// fails, simulating a warm run whose retrieval died mid-run.
type erroringSearchStore struct {
	gotTrace *schemas.RunOutcome
}

func (s *erroringSearchStore) Search(context.Context, schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	return schemas.MemoryBundle{}, errors.New("memd: connection refused")
}

func (s *erroringSearchStore) Upsert(context.Context, schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	return schemas.MemoryObservation{}, nil
}

func (s *erroringSearchStore) UpsertTrace(_ context.Context, trace schemas.RunOutcome) error {
	copy := trace
	s.gotTrace = &copy
	return nil
}

func (s *erroringSearchStore) UpsertVerdict(context.Context, schemas.VerdictRecord) error { return nil }

func statusAccumulator(status string) *runTraceAccumulator {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "add a Hello function",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 100, OutputMax: 100}}},
		TokenBudget:   schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"},
	}
	tr := newRunTraceAccumulator(&traceMemoryStore{}, "run-1", "sess-1", "/repo", plan, status, nil)
	tr.recordHistory(schemas.IterationState{Iteration: 1, StateHash: "h", Confidence: 0.9, TestsPassing: 1})
	return tr
}

func statusResult(tr *runTraceAccumulator) schemas.RunOutcome {
	result := schemas.PipelineResult{
		RunID:  "run-1",
		Status: "completed",
		Tier:   tr.plan.Tier,
		Stages: []schemas.StageRecord{{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1}},
	}
	trace, err := tr.buildRunOutcome(result)
	if err != nil {
		panic(err)
	}
	return trace
}

func TestMemoryStatusMapping(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "active", want: "active"},
		{in: "off", want: "off"},
		{in: "unavailable", want: "unavailable"},
		{in: "", want: "off"}, // unset derives to off
	}
	for _, tc := range cases {
		if got := statusResult(statusAccumulator(tc.in)).Memory.Status; got != tc.want {
			t.Fatalf("status %q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMemoryStatusDegradesOnSearchFailure(t *testing.T) {
	tr := statusAccumulator("active")
	tr.noteMemorySearchFailed()
	if got := statusResult(tr).Memory.Status; got != "unavailable" {
		t.Fatalf("status after search failure = %q, want unavailable", got)
	}

	// A deliberately-disabled run stays off: degradation must not relabel it.
	off := statusAccumulator("off")
	off.noteMemorySearchFailed()
	if got := statusResult(off).Memory.Status; got != "off" {
		t.Fatalf("disabled run degraded to %q, want off", got)
	}
}

func runStatusIntegration(t *testing.T, store MemoryStore, memStatus string) {
	t.Helper()
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(workDir) {
		registry.Register(tool)
	}
	if _, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		MemoryStatus:   memStatus,
	}, store, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunWritesUnavailableOnSearchError(t *testing.T) {
	store := &erroringSearchStore{}
	runStatusIntegration(t, store, "")
	if store.gotTrace == nil {
		t.Fatal("no trace written")
	}
	if store.gotTrace.Memory.Status != "unavailable" {
		t.Fatalf("Memory.Status = %q, want unavailable after search failure", store.gotTrace.Memory.Status)
	}
}

func TestRunWritesUnavailableOnResolveFailure(t *testing.T) {
	// A resolved trace store whose memory resolution reported unavailable.
	store := &traceMemoryStore{}
	runStatusIntegration(t, store, "unavailable")
	if store.gotTrace == nil {
		t.Fatal("no trace written")
	}
	if store.gotTrace.Memory.Status != "unavailable" {
		t.Fatalf("Memory.Status = %q, want unavailable (resolve failure)", store.gotTrace.Memory.Status)
	}
}

func TestRunWritesActiveWhenMemoryResolved(t *testing.T) {
	store := &traceMemoryStore{}
	runStatusIntegration(t, store, "active")
	if store.gotTrace == nil {
		t.Fatal("no trace written")
	}
	if store.gotTrace.Memory.Status != "active" {
		t.Fatalf("Memory.Status = %q, want active", store.gotTrace.Memory.Status)
	}
}
