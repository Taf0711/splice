package splice

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// exemplarStore implements MemoryStore, TraceStore, and learn.TraceQuerier so
// a run can be exercised end-to-end with a fabricated kept-run corpus and the
// injected exemplar count captured in the written trace.
type exemplarStore struct {
	corpus   []schemas.TraceQueryResult
	gotTrace *schemas.RunOutcome
}

func (s *exemplarStore) Search(context.Context, schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	return schemas.MemoryBundle{}, nil
}

func (s *exemplarStore) Upsert(context.Context, schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	return schemas.MemoryObservation{}, nil
}

func (s *exemplarStore) UpsertTrace(_ context.Context, trace schemas.RunOutcome) error {
	copy := trace
	s.gotTrace = &copy
	return nil
}

func (s *exemplarStore) UpsertVerdict(context.Context, schemas.VerdictRecord) error { return nil }

func (s *exemplarStore) QueryTraces(_ context.Context, filter schemas.TraceQueryFilter) ([]schemas.TraceQueryResult, error) {
	out := make([]schemas.TraceQueryResult, 0, len(s.corpus))
	for _, r := range s.corpus {
		if filter.RepoRoot != "" && r.Trace.RepoRoot != filter.RepoRoot {
			continue
		}
		if filter.Status != "" && r.Trace.Outcome.Status != filter.Status {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func TestRunInjectsExemplars(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(workDir) {
		registry.Register(tool)
	}

	abs, _ := filepath.Abs(workDir)
	corpus := []schemas.TraceQueryResult{
		{Trace: exemplarTrace("kept-1", "add a Hello function and tests", 1000, 2000, []string{"main.go", "main_test.go"}), Rank: -8},
		{Trace: exemplarTrace("kept-2", "add a Hello function and tests", 1000, 2000, []string{"main.go"}), Rank: -7},
		{Trace: exemplarTrace("kept-3", "add a Hello function and tests", 1000, 2000, []string{"main.go"}), Rank: -6},
	}
	for i := range corpus {
		corpus[i].Trace.RepoRoot = abs
	}
	store := &exemplarStore{corpus: corpus}

	result, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		Model:          "model-x",
	}, store, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("run incomplete: %s", result.IncompleteReason)
	}
	if store.gotTrace == nil {
		t.Fatal("no trace written")
	}

	var exemplarItems int
	for _, stage := range store.gotTrace.Stages {
		if stage.Name == "code_writer" {
			exemplarItems = stage.InputMeta.ExemplarItems
		}
	}
	if exemplarItems != 3 {
		t.Fatalf("ExemplarItems = %d, want 3 (three kept exemplars injected)", exemplarItems)
	}
	_ = abs
}
