package splice

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// exemplarQuerier returns a fixed, pre-ranked result set (already verdict- and
// repo-filtered), so the retrieval unit tests exercise only the score gate,
// caps, and distillation.
type exemplarQuerier struct {
	results []schemas.TraceQueryResult
	err     error
}

func (q *exemplarQuerier) QueryTraces(context.Context, schemas.TraceQueryFilter) ([]schemas.TraceQueryResult, error) {
	if q.err != nil {
		return nil, q.err
	}
	return q.results, nil
}

func exemplarTrace(runID, intent string, in, out int, files []string) schemas.RunOutcome {
	return schemas.RunOutcome{
		SchemaVersion: "1",
		RunID:         runID,
		RepoRoot:      "/repo",
		Intent:        intent,
		Tier:          "light",
		Plan: &schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: intent,
			Stages: []schemas.ExecutionStage{
				{Name: "code_writer", Budget: schemas.StageBudget{InputMax: 100, OutputMax: 100}},
				{Name: "test_runner", Budget: schemas.StageBudget{InputMax: 0, OutputMax: 0}},
			},
			TokenBudget: schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"},
		},
		Iterations: []schemas.IterationState{{Iteration: 1, StateHash: "h", Confidence: 0.9}},
		Stages: []schemas.TracedStage{{
			StageRecord: schemas.StageRecord{Name: "code_writer", Iteration: 1, Status: schemas.StageCompleted, TokensInput: in, TokensOutput: out},
		}},
		Outcome: schemas.OutcomeRecord{Status: "completed", ChangedFiles: files},
		Memory:  schemas.MemoryRecord{Status: "active"},
	}
}

func ranked(runID, intent string, rank float64) schemas.TraceQueryResult {
	return schemas.TraceQueryResult{Trace: exemplarTrace(runID, intent, 1000, 2000, []string{"a.go", "b.go"}), Rank: rank}
}

func TestRetrieveExemplarsScoreGate(t *testing.T) {
	q := &exemplarQuerier{results: []schemas.TraceQueryResult{
		ranked("weak-1", "unrelated", -0.2),
		ranked("weak-2", "unrelated", 0.0),
	}}
	got, err := retrieveExemplars(context.Background(), q, "/repo", "add a Hello function")
	if err != nil {
		t.Fatalf("retrieveExemplars: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("exemplars = %d, want 0 (junk similarity below the gate)", len(got))
	}
}

func TestRetrieveExemplarsCapsAtThree(t *testing.T) {
	q := &exemplarQuerier{results: []schemas.TraceQueryResult{
		ranked("r1", "intent", -8),
		ranked("r2", "intent", -7),
		ranked("r3", "intent", -6),
		ranked("r4", "intent", -5),
		ranked("r5", "intent", -4),
	}}
	got, err := retrieveExemplars(context.Background(), q, "/repo", "intent")
	if err != nil {
		t.Fatalf("retrieveExemplars: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("exemplars = %d, want exactly 3", len(got))
	}
}

func TestRetrieveExemplarsTotalCap(t *testing.T) {
	big := strings.Repeat("x", 5000)
	q := &exemplarQuerier{results: []schemas.TraceQueryResult{
		{Trace: exemplarTrace("r1", big, 1000, 2000, nil), Rank: -8},
		{Trace: exemplarTrace("r2", big, 1000, 2000, nil), Rank: -7},
		{Trace: exemplarTrace("r3", big, 1000, 2000, nil), Rank: -6},
	}}
	got, err := retrieveExemplars(context.Background(), q, "/repo", "intent")
	if err != nil {
		t.Fatalf("retrieveExemplars: %v", err)
	}
	total := 0
	for _, e := range got {
		total += len(e.Content)
		if len(e.Content) > exemplarMaxChars {
			t.Fatalf("exemplar content %d chars exceeds per-exemplar cap %d", len(e.Content), exemplarMaxChars)
		}
	}
	if total > exemplarTotalChars {
		t.Fatalf("total exemplar chars = %d, want <= %d", total, exemplarTotalChars)
	}
}

func TestDistillExemplarSafety(t *testing.T) {
	trace := exemplarTrace("run-1", "add a Hello function", 1000, 2000, []string{"main.go", "main_test.go", "x", "y", "z", "w"})
	// A stage output body carrying a raw prompt must never leak into the
	// distillate.
	trace.Stages[0].OutputSummary = strPtr("SECRET_RAW_PROMPT_DO_NOT_LEAK")
	trace.Stages[0].Activity = strPtr("SECRET_RAW_PROMPT_DO_NOT_LEAK")
	trace.Iterations[0].FilesChanged = []string{"SECRET_RAW_PROMPT_DO_NOT_LEAK"}

	content := distillExemplar(trace)
	if strings.Contains(content, "SECRET_RAW_PROMPT_DO_NOT_LEAK") {
		t.Fatalf("distillate leaked raw content: %s", content)
	}
	for _, want := range []string{"intent:", "tier: light", "stages: code_writer, test_runner", "iterations: 1", "changed: main.go", "tokens: 3000"} {
		if !strings.Contains(content, want) {
			t.Fatalf("distillate missing %q: %s", want, content)
		}
	}
}

func TestRetrieveExemplarsDeterministic(t *testing.T) {
	q := &exemplarQuerier{results: []schemas.TraceQueryResult{
		ranked("r1", "intent", -8),
		ranked("r2", "intent", -6),
	}}
	a, err := retrieveExemplars(context.Background(), q, "/repo", "intent")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := retrieveExemplars(context.Background(), q, "/repo", "intent")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("nondeterministic: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("nondeterministic exemplar %d: %#v vs %#v", i, a[i], b[i])
		}
	}
}

func strPtr(s string) *string { return &s }
