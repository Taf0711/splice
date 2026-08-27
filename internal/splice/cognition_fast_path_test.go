package splice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// cognitionLookupStore is a fake MemoryStore + TopicLookupStore + TraceQuerier
// for the C0.5 test matrix. It embeds stubStore so Search falls through to
// the parent's bundle/err, and adds LookupTopic over a topic-key map.
type cognitionLookupStore struct {
	stubStore
	topics    map[string]schemas.MemoryBundle
	lookups   []schemas.MemoryTopicQuery
	lookupErr error
	queried   bool
}

func (s *cognitionLookupStore) LookupTopic(_ context.Context, q schemas.MemoryTopicQuery) (schemas.MemoryBundle, error) {
	s.lookups = append(s.lookups, q)
	if s.lookupErr != nil {
		return schemas.MemoryBundle{}, s.lookupErr
	}
	return s.topics[q.TopicKey], nil
}

func (s *cognitionLookupStore) QueryTraces(_ context.Context, _ schemas.TraceQueryFilter) ([]schemas.TraceQueryResult, error) {
	s.queried = true
	return nil, nil
}

// ptr helper for test fixtures.
func ptr[T any](v T) *T { return &v }

// cognitionFixtureRepo creates a real git repo with one file committed,
// returns (repoRoot, commitSHA).
func cognitionFixtureRepo(t *testing.T, path, content string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	mustGit(t, root, "init", "-b", "main")
	mustGit(t, root, "config", "user.name", "splice-test")
	mustGit(t, root, "config", "user.email", "splice-test@local")
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-m", "base")
	commit := mustGit(t, root, "rev-parse", "HEAD")
	return root, strings.TrimSpace(commit)
}

// mustGit runs git and fails the test on error.
var mustGit = func(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", command...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

// cognitionInput builds a minimal HarnessStageInput for the given stage.
func cognitionInput(stage, intent string) schemas.HarnessStageInput {
	return schemas.HarnessStageInput{
		RunID:         "c0-test",
		StageName:     stage,
		Sequence:      1,
		PlanTier:      schemas.TierLight,
		RequestIntent: intent,
	}
}

// cognitionPlan builds a minimal plan for the given stage.
func cognitionPlan(stage string) schemas.ExecutionPlan {
	return schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "test",
		Stages:        []schemas.ExecutionStage{{Name: stage}},
	}
}

// Test A: exact fresh hit — direct used, Search NOT called, exemplars NOT queried.
func TestCognitionFastPath_A_FreshHit(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	obs := obsWithID(1, root, "session manager must use context.Background")
	obs.SourceCommit = &commit
	obs.TopicKey = ptr("file:internal/auth/session.go")
	store := &cognitionLookupStore{topics: map[string]schemas.MemoryBundle{
		"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs}},
	}}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix session invalidation in internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 {
		t.Fatalf("expected 1 observation in direct hit, got %v", prepared.MemoryBundle)
	}
	if len(store.queries) != 0 {
		t.Fatalf("Search called %d times, want 0 (direct hit should skip Search)", len(store.queries))
	}
	if store.queried {
		t.Fatal("TraceQuerier called on direct hit, want exemplars skipped")
	}
	meta := tr.stages[stageKey{"code_writer", 1}]
	if meta.MemoryLookupMode != "direct" {
		t.Fatalf("memory_lookup_mode = %q, want direct", meta.MemoryLookupMode)
	}
	if meta.DirectHits != 1 {
		t.Fatalf("direct_hits = %d, want 1", meta.DirectHits)
	}
}

// Test B: miss — no matching topic key; Search must be called, exemplars
// retrieved, admission note present.
func TestCognitionFastPath_B_MissFallback(t *testing.T) {
	root := t.TempDir()
	store := &cognitionLookupStore{
		topics: map[string]schemas.MemoryBundle{},
		stubStore: stubStore{bundle: schemas.MemoryBundle{
			RequestingAgent: "code_writer",
			Observations:    []schemas.MemoryObservation{obsWithID(1, root, "search result")},
		}},
	}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 {
		t.Fatalf("expected 1 observation from Search fallback, got %v", prepared.MemoryBundle)
	}
	if len(store.queries) != 1 {
		t.Fatalf("Search called %d times, want 1", len(store.queries))
	}
	if !store.queried {
		t.Fatal("TraceQuerier not called on Search path, exemplars should be attempted")
	}
	meta := tr.stages[stageKey{"code_writer", 1}]
	if meta.MemoryLookupMode != "search" {
		t.Fatalf("memory_lookup_mode = %q, want search", meta.MemoryLookupMode)
	}
}

// Test C: stale — file changed after source_commit; direct not injected, Search called.
func TestCognitionFastPath_C_StaleFallback(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	// Change the file after the observed commit.
	if err := os.WriteFile(filepath.Join(root, "internal/auth/session.go"), []byte("package auth\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	obs := obsWithID(1, root, "stale observation")
	obs.SourceCommit = &commit
	obs.TopicKey = ptr("file:internal/auth/session.go")
	store := &cognitionLookupStore{
		topics: map[string]schemas.MemoryBundle{
			"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs}},
		},
		stubStore: stubStore{bundle: schemas.MemoryBundle{
			RequestingAgent: "code_writer",
			Observations:    []schemas.MemoryObservation{obsWithID(2, root, "fresh search result")},
		}},
	}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 {
		t.Fatalf("expected 1 observation from Search fallback, got %v", prepared.MemoryBundle)
	}
	if prepared.MemoryBundle.Observations[0].ID != 2 {
		t.Fatalf("expected Search result (id=2), got id=%d (stale direct observation was incorrectly injected)", prepared.MemoryBundle.Observations[0].ID)
	}
	if len(store.queries) != 1 {
		t.Fatalf("Search called %d times, want 1 (stale must fall back)", len(store.queries))
	}
}

// Test D: unknown freshness — empty source_commit falls back to Search.
func TestCognitionFastPath_D_UnknownFreshness(t *testing.T) {
	root := t.TempDir()
	obs := obsWithID(1, root, "unknown freshness")
	obs.TopicKey = ptr("file:internal/auth/session.go")
	// SourceCommit is left nil.
	store := &cognitionLookupStore{
		topics: map[string]schemas.MemoryBundle{
			"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs}},
		},
		stubStore: stubStore{bundle: schemas.MemoryBundle{
			RequestingAgent: "code_writer",
			Observations:    []schemas.MemoryObservation{obsWithID(2, root, "search result")},
		}},
	}
	plan := cognitionPlan("code_writer")
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 || prepared.MemoryBundle.Observations[0].ID != 2 {
		t.Fatalf("expected Search fallback result (id=2), got %v", prepared.MemoryBundle)
	}
	if len(store.queries) != 1 {
		t.Fatalf("Search called %d times, want 1", len(store.queries))
	}
}

// Test E: wrong-project isolation — Admit rejects a fresh observation from a
// different project root; fallback to Search.
func TestCognitionFastPath_E_WrongProject(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	obs := obsWithID(1, "/other/repo", "wrong project")
	obs.SourceCommit = &commit
	obs.TopicKey = ptr("file:internal/auth/session.go")
	store := &cognitionLookupStore{
		topics: map[string]schemas.MemoryBundle{
			"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs}},
		},
		stubStore: stubStore{bundle: schemas.MemoryBundle{
			RequestingAgent: "code_writer",
			Observations:    []schemas.MemoryObservation{obsWithID(2, root, "correct project result")},
		}},
	}
	plan := cognitionPlan("code_writer")
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil {
		t.Fatal("expected a bundle from Search fallback")
	}
	if len(prepared.MemoryBundle.Observations) != 1 || prepared.MemoryBundle.Observations[0].ID != 2 {
		t.Fatalf("expected Search fallback result (id=2), got %v", prepared.MemoryBundle)
	}
	if len(store.queries) != 1 {
		t.Fatalf("Search called %d times, want 1 (wrong-project direct must fall back)", len(store.queries))
	}
}

// Test F: review-due — Admit rejects a review-due observation; fallback to Search.
func TestCognitionFastPath_F_ReviewDue(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	obs := obsWithID(1, root, "review-due observation")
	obs.SourceCommit = &commit
	obs.TopicKey = ptr("file:internal/auth/session.go")
	obs.ReviewAfter = ptr(int64(1)) // past timestamp
	store := &cognitionLookupStore{
		topics: map[string]schemas.MemoryBundle{
			"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs}},
		},
		stubStore: stubStore{bundle: schemas.MemoryBundle{
			RequestingAgent: "code_writer",
			Observations:    []schemas.MemoryObservation{obsWithID(2, root, "fresh search result")},
		}},
	}
	plan := cognitionPlan("code_writer")
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		NowUnix:   10, // past the review_after=1
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 || prepared.MemoryBundle.Observations[0].ID != 2 {
		t.Fatalf("expected Search fallback result (id=2), got %v", prepared.MemoryBundle)
	}
	if len(store.queries) != 1 {
		t.Fatalf("Search called %d times, want 1", len(store.queries))
	}
}

// Test G: duplicate direct records — Admit deduplicates on StableID; the
// second observation with the same ID is rejected, direct hit still used.
func TestCognitionFastPath_G_DuplicateDedupe(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	obs1 := obsWithID(1, root, "unique content")
	obs1.SourceCommit = &commit
	obs1.TopicKey = ptr("file:internal/auth/session.go")
	obs2 := obsWithID(1, root, "different content but same ID")
	obs2.SourceCommit = &commit
	obs2.TopicKey = ptr("file:internal/auth/session.go")
	store := &cognitionLookupStore{topics: map[string]schemas.MemoryBundle{
		"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs1, obs2}},
	}}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 {
		t.Fatalf("expected 1 deduplicated observation, got %v", prepared.MemoryBundle)
	}
	if len(store.queries) != 0 {
		t.Fatalf("Search called %d times, want 0 (direct hit should be used)", len(store.queries))
	}
	meta := tr.stages[stageKey{"code_writer", 1}]
	// Both observations were classified fresh pre-admission; Admit's StableID
	// dedupe then kept one. DirectHits counts the pre-admission fresh set.
	if meta.DirectHits != 2 {
		t.Fatalf("direct_hits = %d, want 2 (both were fresh before admission)", meta.DirectHits)
	}
	if meta.MemoryLookupMode != "direct" {
		t.Fatalf("memory_lookup_mode = %q, want direct", meta.MemoryLookupMode)
	}
}

// Test H: budget compaction — a direct bundle that overflows the stage's
// input allowance must be compacted through the existing path; delivered count
// < direct fresh count.
func TestCognitionFastPath_H_BudgetCompaction(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	var observations []schemas.MemoryObservation
	for i := int64(1); i <= 5; i++ {
		obs := obsWithID(i, root, strings.Repeat("0123456789", int(40*i+10)))
		obs.SourceCommit = &commit
		obs.TopicKey = ptr("file:internal/auth/session.go")
		observations = append(observations, obs)
	}
	store := &cognitionLookupStore{topics: map[string]schemas.MemoryBundle{
		"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: observations},
	}}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run", "session", root, plan, "active", nil)
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil {
		t.Fatal("expected a bundle (compaction drops some, not all)")
	}
	delivered := len(prepared.MemoryBundle.Observations)
	if delivered == 0 || delivered >= 5 {
		t.Fatalf("compaction must drop some observations: delivered=%d, want 1..4", delivered)
	}
	meta := tr.stages[stageKey{"code_writer", 1}]
	if meta.MemoryItems != delivered {
		t.Fatalf("trace MemoryItems = %d, want post-compaction delivered %d", meta.MemoryItems, delivered)
	}
	if meta.MemoryLookupMode != "direct" {
		t.Fatalf("memory_lookup_mode = %q, want direct", meta.MemoryLookupMode)
	}
}

// Test I: no lookup capability — a plain stubStore (no TopicLookupStore)
// must work unchanged: Search called, bundle from Search delivered.
func TestCognitionFastPath_I_NoCapability(t *testing.T) {
	root := t.TempDir()
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(1, root, "plain search")},
	}}
	plan := cognitionPlan("code_writer")
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "fix internal/auth/session.go"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	if prepared.MemoryBundle == nil || len(prepared.MemoryBundle.Observations) != 1 {
		t.Fatalf("expected 1 observation from Search, got %v", prepared.MemoryBundle)
	}
	if len(store.queries) != 1 {
		t.Fatalf("Search called %d times, want 1", len(store.queries))
	}
}

// Test J: repair re-entry — a second call to prepareStageInput (simulating
// repair re-entry with Iteration=2) must work identically. A miss store
// falls back to Search, and the fast path does not break repair behavior.
func TestCognitionFastPath_J_RepairReentry(t *testing.T) {
	root := t.TempDir()
	store := &cognitionLookupStore{}
	plan := cognitionPlan("code_writer")

	// Initial pass: no keys derived from request intent (no path tokens),
	// so Search is not called (no memory consumed).
	first, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "no structural tokens here"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 1,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
	})
	if err != nil {
		t.Fatalf("initial pass: %v", err)
	}
	if first.MemoryBundle != nil {
		t.Fatal("expected no memory bundle on first pass (no keys, no search)")
	}

	// Repair re-entry: same store, Iteration=2. The function must handle it
	// without error or panic.
	second, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput("code_writer", "still no tokens"),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, "code_writer"),
		Tier:      plan.Tier,
		Iteration: 2,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
	})
	if err != nil {
		t.Fatalf("repair re-entry: %v", err)
	}
	if second.MemoryBundle != nil {
		t.Fatal("expected no memory bundle on repair re-entry (no keys, no search)")
	}
}

// TestCognitionFastPathMatrix_RunAll runs the full A-J matrix. This test is
// a convenience aggregator; individual tests above already cover each case.
func TestCognitionFastPathMatrix_RunAll(t *testing.T) {
	t.Run("A_fresh_hit", TestCognitionFastPath_A_FreshHit)
	t.Run("B_miss_fallback", TestCognitionFastPath_B_MissFallback)
	t.Run("C_stale_fallback", TestCognitionFastPath_C_StaleFallback)
	t.Run("D_unknown_freshness", TestCognitionFastPath_D_UnknownFreshness)
	t.Run("E_wrong_project", TestCognitionFastPath_E_WrongProject)
	t.Run("F_review_due", TestCognitionFastPath_F_ReviewDue)
	t.Run("G_duplicate_dedupe", TestCognitionFastPath_G_DuplicateDedupe)
	t.Run("H_budget_compaction", TestCognitionFastPath_H_BudgetCompaction)
	t.Run("I_no_capability", TestCognitionFastPath_I_NoCapability)
	t.Run("J_repair_reentry", TestCognitionFastPath_J_RepairReentry)
}
