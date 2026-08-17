package splice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/learn"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
)

// learningStore implements MemoryStore, TraceStore, and learn.TraceQuerier so a
// run can be exercised end-to-end with a fabricated corpus and the fitted
// budget captured in the written trace.
type learningStore struct {
	corpus   []schemas.RunOutcome
	gotTrace *schemas.RunOutcome
}

func (s *learningStore) Search(context.Context, schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	return schemas.MemoryBundle{}, nil
}

func (s *learningStore) Upsert(context.Context, schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	return schemas.MemoryObservation{}, nil
}

func (s *learningStore) UpsertTrace(_ context.Context, trace schemas.RunOutcome) error {
	copy := trace
	s.gotTrace = &copy
	return nil
}

func (s *learningStore) UpsertVerdict(context.Context, schemas.VerdictRecord) error { return nil }

func (s *learningStore) QueryTraces(_ context.Context, filter schemas.TraceQueryFilter) ([]schemas.TraceQueryResult, error) {
	out := make([]schemas.TraceQueryResult, 0, len(s.corpus))
	for _, t := range s.corpus {
		if filter.RepoRoot != "" && t.RepoRoot != filter.RepoRoot {
			continue
		}
		if filter.Status != "" && t.Outcome.Status != filter.Status {
			continue
		}
		out = append(out, schemas.TraceQueryResult{Trace: t})
	}
	return out, nil
}

func corpusTrace(key learn.BucketKey, in, out int) schemas.RunOutcome {
	model := key.Model
	return schemas.RunOutcome{
		SchemaVersion:   "1",
		RunID:           "run",
		RepoRoot:        key.RepoRoot,
		Intent:          "x",
		Tier:            "light",
		Memory:          schemas.MemoryRecord{Status: "active"},
		ToolFingerprint: key.ToolFingerprint,
		TopologyHash:    key.TopologyHash,
		Outcome:         schemas.OutcomeRecord{Status: "completed"},
		Stages: []schemas.TracedStage{{
			StageRecord: schemas.StageRecord{
				Name: key.Stage, Model: &model, Iteration: 1,
				Status: schemas.StageCompleted, TokensInput: in, TokensOutput: out,
			},
			PromptHash: key.PromptHash,
		}},
	}
}

func runWithCorpus(t *testing.T, n, in, out int) *schemas.RunOutcome {
	t.Helper()
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(workDir) {
		registry.Register(tool)
	}

	plan, err := BuildExecutionPlan("add a Hello function and tests")
	if err != nil {
		t.Fatalf("BuildExecutionPlan: %v", err)
	}
	stageNames := make([]string, len(plan.Stages))
	for i, s := range plan.Stages {
		stageNames[i] = s.Name
	}
	abs, _ := filepath.Abs(workDir)
	model := "model-x"
	key := learn.BucketKey{
		RepoRoot:        abs,
		Stage:           "code_writer",
		PromptHash:      learn.Hash(stages.StagePrompt("code_writer")),
		Model:           model,
		ToolFingerprint: learn.Hash(stages.VerificationToolIdentities()...),
		TopologyHash:    learn.Hash(stageNames...),
	}

	corpus := make([]schemas.RunOutcome, 0, n)
	for i := 0; i < n; i++ {
		corpus = append(corpus, corpusTrace(key, in, out))
	}
	store := &learningStore{corpus: corpus}

	_, err = Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		Model:          model,
	}, store, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if store.gotTrace == nil {
		t.Fatal("no trace written")
	}
	return store.gotTrace
}

func TestRunAppliesLearnedBudgetsAboveFloor(t *testing.T) {
	trace := runWithCorpus(t, 25, 6000, 9000)
	if !strings.Contains(trace.BudgetProvenance["code_writer"], "calibrated") {
		t.Fatalf("budget provenance = %q, want calibrated", trace.BudgetProvenance["code_writer"])
	}
	// The fitted p80 (6000/9000) must be embedded in the plan.
	var fitted schemas.StageBudget
	for _, s := range trace.Plan.Stages {
		if s.Name == "code_writer" {
			fitted = s.Budget
		}
	}
	if fitted.InputMax != 6000 || fitted.OutputMax != 9000 {
		t.Fatalf("embedded plan budget = %d/%d, want 6000/9000", fitted.InputMax, fitted.OutputMax)
	}
	if trace.ToolFingerprint == "" || trace.TopologyHash == "" {
		t.Fatalf("trace key fields empty: fingerprint=%q topology=%q", trace.ToolFingerprint, trace.TopologyHash)
	}
	if len(trace.Stages) == 0 || trace.Stages[0].PromptHash == "" {
		t.Fatalf("traced stage prompt hash empty: %#v", trace.Stages)
	}
}

func TestRunKeepsDefaultsBelowFloor(t *testing.T) {
	trace := runWithCorpus(t, 5, 6000, 9000)
	if !strings.Contains(trace.BudgetProvenance["code_writer"], "not calibrated") {
		t.Fatalf("budget provenance = %q, want not-calibrated", trace.BudgetProvenance["code_writer"])
	}
	var budget schemas.StageBudget
	for _, s := range trace.Plan.Stages {
		if s.Name == "code_writer" {
			budget = s.Budget
		}
	}
	if budget.InputMax != 4000 || budget.OutputMax != 8192 {
		t.Fatalf("embedded plan budget = %d/%d, want static default 4000/8192", budget.InputMax, budget.OutputMax)
	}
}
