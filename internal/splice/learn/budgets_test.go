package learn

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// fakeQuerier returns fabricated corpora keyed by status, with the RepoRoot
// filter applied the way the sidecar applies it.
type fakeQuerier struct {
	completed []schemas.RunOutcome
	aborted   []schemas.RunOutcome
	err       error
}

func (f *fakeQuerier) QueryTraces(_ context.Context, filter schemas.TraceQueryFilter) ([]schemas.TraceQueryResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	var pool []schemas.RunOutcome
	switch filter.Status {
	case "completed":
		pool = f.completed
	case "aborted":
		pool = f.aborted
	}
	out := make([]schemas.TraceQueryResult, 0, len(pool))
	for _, t := range pool {
		if filter.RepoRoot != "" && t.RepoRoot != filter.RepoRoot {
			continue
		}
		out = append(out, schemas.TraceQueryResult{Trace: t})
	}
	return out, nil
}

func testKey() BucketKey {
	return BucketKey{
		RepoRoot:        "/repo",
		Stage:           "code_writer",
		PromptHash:      "prompt-1",
		Model:           "model-x",
		ToolFingerprint: "tool-1",
		TopologyHash:    "topo-1",
	}
}

func trace(key BucketKey, memoryStatus string, in, out int, status string) schemas.RunOutcome {
	model := key.Model
	return schemas.RunOutcome{
		SchemaVersion:   "1",
		RunID:           "run",
		RepoRoot:        key.RepoRoot,
		Intent:          "x",
		Tier:            "light",
		Memory:          schemas.MemoryRecord{Status: memoryStatus},
		ToolFingerprint: key.ToolFingerprint,
		TopologyHash:    key.TopologyHash,
		Outcome:         schemas.OutcomeRecord{Status: status},
		Stages: []schemas.TracedStage{{
			StageRecord: schemas.StageRecord{
				Name: key.Stage, Model: &model, Iteration: 1,
				Status: schemas.StageCompleted, TokensInput: in, TokensOutput: out,
			},
			PromptHash: key.PromptHash,
		}},
	}
}

func corpus(key BucketKey, memoryStatus string, in, out, n int, status string) []schemas.RunOutcome {
	rows := make([]schemas.RunOutcome, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, trace(key, memoryStatus, in, out, status))
	}
	return rows
}

func staticDefault() schemas.StageBudget {
	return schemas.StageBudget{InputMax: 4000, OutputMax: 8192}
}

func TestFitBudgetBelowFloorRefusesLoudly(t *testing.T) {
	key := testKey()
	q := &fakeQuerier{completed: corpus(key, "active", 4000, 8000, Floor-1, "completed")}
	fit, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("FitBudget: %v", err)
	}
	if fit.Calibrated {
		t.Fatalf("calibrated = true, want below-floor refusal")
	}
	want := "budget not calibrated: 19/20 for this key"
	if fit.Provenance != want {
		t.Fatalf("provenance = %q, want %q", fit.Provenance, want)
	}
	if fit.InputMax != 4000 || fit.OutputMax != 8192 {
		t.Fatalf("defaults = %d/%d, want 4000/8192", fit.InputMax, fit.OutputMax)
	}
}

func TestFitBudgetAtFloorFits(t *testing.T) {
	key := testKey()
	q := &fakeQuerier{completed: corpus(key, "active", 5000, 8000, Floor, "completed")}
	fit, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("FitBudget: %v", err)
	}
	if !fit.Calibrated {
		t.Fatalf("calibrated = false, want fit at floor")
	}
	if fit.InputMax != 5000 || fit.OutputMax != 8000 {
		t.Fatalf("fit = %d/%d, want 5000/8000", fit.InputMax, fit.OutputMax)
	}
	if fit.Provenance != "calibrated from 20 runs, p80" {
		t.Fatalf("provenance = %q", fit.Provenance)
	}
}

func TestFitBudgetPercentileIgnoresOutlier(t *testing.T) {
	key := testKey()
	rows := corpus(key, "active", 4000, 8000, Floor-1, "completed")
	rows = append(rows, trace(key, "active", 100000, 100000, "completed")) // one outlier
	q := &fakeQuerier{completed: rows}
	fit, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("FitBudget: %v", err)
	}
	// p80 must be 4000/8000, not the mean (~8800) and not the outlier.
	if fit.InputMax != 4000 || fit.OutputMax != 8000 {
		t.Fatalf("fit = %d/%d, want 4000/8000 (p80, not mean)", fit.InputMax, fit.OutputMax)
	}
}

func TestFitBudgetSurvivorshipRaises(t *testing.T) {
	key := testKey()
	q := &fakeQuerier{
		completed: corpus(key, "active", 4000, 4000, Floor, "completed"),
		aborted: []schemas.RunOutcome{{
			SchemaVersion: "1", RunID: "abort", RepoRoot: key.RepoRoot, Intent: "x", Tier: "light",
			Memory:          schemas.MemoryRecord{Status: "active"},
			ToolFingerprint: key.ToolFingerprint,
			TopologyHash:    key.TopologyHash,
			Outcome:         schemas.OutcomeRecord{Status: "aborted", AbortReason: "abort_budget: Token budget reached."},
			Stages: []schemas.TracedStage{{
				StageRecord: schemas.StageRecord{Name: key.Stage, Model: strPtr(key.Model), Iteration: 1, Status: schemas.StageFailed, TokensInput: 6000, TokensOutput: 6000},
				PromptHash:  key.PromptHash,
			}},
		}},
	}
	fit, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("FitBudget: %v", err)
	}
	if fit.InputMax != 6000 || fit.OutputMax != 6000 {
		t.Fatalf("fit = %d/%d, want 6000/6000 (survivorship raise)", fit.InputMax, fit.OutputMax)
	}
	if !strings.Contains(fit.Provenance, "raised for 1 budget aborts") {
		t.Fatalf("provenance = %q, want survivorship note", fit.Provenance)
	}
}

func TestFitBudgetMemoryStatusSplit(t *testing.T) {
	key := testKey()
	// A warm corpus (active) must not calibrate a cold run (off).
	q := &fakeQuerier{completed: corpus(key, "active", 5000, 8000, Floor, "completed")}
	fit, err := FitBudget(context.Background(), q, key, "off", staticDefault())
	if err != nil {
		t.Fatalf("FitBudget: %v", err)
	}
	if fit.Calibrated {
		t.Fatalf("calibrated = true, want cold refusal against warm corpus")
	}
	if !strings.Contains(fit.Provenance, "0/20") {
		t.Fatalf("provenance = %q, want 0/20 refusal", fit.Provenance)
	}
}

func TestFitBudgetUnavailableDoesNotMatchActive(t *testing.T) {
	key := testKey()
	// A degraded (unavailable) corpus must not calibrate an active-status fit:
	// the warm arm is not polluted by runs whose retrieval failed.
	q := &fakeQuerier{completed: corpus(key, "unavailable", 5000, 8000, Floor, "completed")}
	fit, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("FitBudget: %v", err)
	}
	if fit.Calibrated {
		t.Fatalf("calibrated = true, want active fit to reject an unavailable corpus")
	}
	if !strings.Contains(fit.Provenance, "0/20") {
		t.Fatalf("provenance = %q, want 0/20 refusal", fit.Provenance)
	}
}

func TestFitBudgetLegacyBucketNeverFits(t *testing.T) {
	key := testKey()
	// Legacy traces have empty key fields: they must never match a full key.
	legacy := testKey()
	legacy.PromptHash = ""
	legacy.ToolFingerprint = ""
	legacy.TopologyHash = ""
	q := &fakeQuerier{completed: corpus(legacy, "active", 5000, 8000, Floor, "completed")}
	fit, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("FitBudget: %v", err)
	}
	if fit.Calibrated {
		t.Fatalf("calibrated = true, want legacy-bucket refusal")
	}
}

func TestFitBudgetClampFires(t *testing.T) {
	key := testKey()
	q := &fakeQuerier{completed: corpus(key, "active", 10000, 20000, Floor, "completed")}
	fit, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("FitBudget: %v", err)
	}
	if fit.InputMax != 8000 || fit.OutputMax != 16384 {
		t.Fatalf("fit = %d/%d, want clamp to 8000/16384", fit.InputMax, fit.OutputMax)
	}
	if !strings.Contains(fit.Provenance, "clamped") {
		t.Fatalf("provenance = %q, want clamp note", fit.Provenance)
	}
}

func TestFitBudgetDeterministic(t *testing.T) {
	key := testKey()
	q := &fakeQuerier{completed: corpus(key, "active", 5000, 8000, Floor, "completed")}
	a, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := FitBudget(context.Background(), q, key, "active", staticDefault())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a != b {
		t.Fatalf("nondeterministic fit: %#v vs %#v", a, b)
	}
}

func TestRefusalProvenanceNamesCountAndFloor(t *testing.T) {
	key := testKey()
	for _, n := range []int{0, 1, Floor - 1} {
		q := &fakeQuerier{completed: corpus(key, "active", 4000, 8000, n, "completed")}
		fit, err := FitBudget(context.Background(), q, key, "active", staticDefault())
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		want := strings.Replace("budget not calibrated: N/20 for this key", "N", itoa(n), 1)
		if fit.Provenance != want {
			t.Fatalf("n=%d provenance = %q, want %q", n, fit.Provenance, want)
		}
	}
}

func TestHashPromptEditChangesHash(t *testing.T) {
	if Hash("prompt A") == Hash("prompt B") {
		t.Fatal("different prompt contents must produce different hashes")
	}
}

func TestTopologyHashStableAcrossCodeEdit(t *testing.T) {
	// Topology is the stage/edge structure only; a code edit that does not
	// change the plan shape keeps the topology hash stable.
	a := Hash("code_writer", "test_runner")
	if a != Hash("code_writer", "test_runner") {
		t.Fatal("topology hash must be deterministic")
	}
	// A changed stage roster changes the hash; unrelated content does not.
	if a == Hash("code_writer", "test_generator") {
		t.Fatal("a topology change must change the hash")
	}
}

func strPtr(s string) *string { return &s }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
