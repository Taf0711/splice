package splice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/learn"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// TestBudgetFitFreezePinsProvenance pins the M1 evaluation rule: with
// SPLICE_BUDGET_FIT=off, learned fitting never replaces the static budgets,
// and the trace says so per stage. Warm/cold evaluation arms set this so
// learned fitting cannot give one arm different effective budgets than the
// other. The unfrozen contrast run proves the provenance comes from the
// freeze, not from an empty corpus.
func TestBudgetFitFreezePinsProvenance(t *testing.T) {
	// Enough completed runs that fitting, if enabled, would calibrate the
	// code_writer bucket instead of reporting a below-floor miss.
	corpus := make([]schemas.TraceQueryResult, 0, learn.Floor+5)
	for i := 0; i < learn.Floor+5; i++ {
		corpus = append(corpus, schemas.TraceQueryResult{Trace: exemplarTrace("fit-1", "add a Hello function and tests", 4000, 8000, nil), Rank: -1})
	}

	runOnce := func(set flags.Set) *exemplarStore {
		t.Helper()
		workDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		registry := tools.NewRegistry()
		for _, tool := range tools.CoreTools(workDir) {
			registry.Register(tool)
		}
		store := &exemplarStore{corpus: corpus}
		if _, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
			Cwd:            workDir,
			Registry:       registry,
			PermissionMode: agent.PermissionModeAuto,
			Flags:          set,
		}, store, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if store.gotTrace == nil {
			t.Fatal("run must persist its trace to the store")
		}
		return store
	}

	t.Run("frozen via pipeline.budget_fit=false", func(t *testing.T) {
		store := runOnce(flags.Set{flags.PipelineBudgetFit: false})
		if len(store.gotTrace.BudgetProvenance) == 0 {
			t.Fatal("trace must carry budget provenance")
		}
		for stage, prov := range store.gotTrace.BudgetProvenance {
			if !strings.Contains(prov, "frozen") {
				t.Fatalf("%s provenance = %q, want the frozen marker", stage, prov)
			}
		}
	})

	t.Run("unfrozen contrast never says frozen", func(t *testing.T) {
		store := runOnce(nil)
		for stage, prov := range store.gotTrace.BudgetProvenance {
			if strings.Contains(prov, "frozen") {
				t.Fatalf("%s provenance = %q, want a fit or miss result when fitting is enabled", stage, prov)
			}
		}
	})
}
