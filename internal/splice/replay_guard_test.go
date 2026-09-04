package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// prepareCognition runs one prepareStageInput invocation against the given
// store with an explicit plan and returns the prepared input.
func prepareCognition(t *testing.T, store MemoryStore, plan schemas.ExecutionPlan, stage, intent, root string, iteration int, tr *runTraceAccumulator) schemas.HarnessStageInput {
	t.Helper()
	prepared, err := prepareStageInput(context.Background(), stageInputPreparation{
		Input:     cognitionInput(stage, intent),
		Stage:     &capturingStage{caps: stages.Capabilities{ConsumesMemory: true}},
		Budget:    stageBudgetByName(plan, stage),
		Tier:      plan.Tier,
		Iteration: iteration,
		WorkDir:   root,
		Options:   PipelineConfigFromAgentOptions(agent.Options{}),
		Memory:    store,
		Trace:     tr,
	})
	if err != nil {
		t.Fatalf("prepareStageInput: %v", err)
	}
	return prepared
}

// TestReplayGuard_SameStageDeliveredOnce (Test A): a stage that already
// consumed an observation must not receive it again on later invocations of
// the same run.
func TestReplayGuard_SameStageDeliveredOnce(t *testing.T) {
	root := t.TempDir()
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(42, root, "convention A")},
	}}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run-a", "session", root, plan, "active", nil)

	first := prepareCognition(t, store, plan, "code_writer", "task", root, 1, tr)
	if len(first.MemoryBundle.Observations) != 1 {
		t.Fatalf("first invocation observations = %d, want 1", len(first.MemoryBundle.Observations))
	}
	second := prepareCognition(t, store, plan, "code_writer", "task", root, 2, tr)
	third := prepareCognition(t, store, plan, "code_writer", "task", root, 3, tr)
	if len(second.MemoryBundle.Observations) != 0 || len(third.MemoryBundle.Observations) != 0 {
		t.Fatalf("re-invocations must suppress consumed items: second=%v third=%v", second.MemoryBundle, third.MemoryBundle)
	}
	if got := tr.replaySuppressedCount(); got != 2 {
		t.Fatalf("replay suppressed = %d, want 2", got)
	}
	// Retrieval stayed real: Search ran for every invocation.
	if len(store.queries) != 3 {
		t.Fatalf("Search calls = %d, want 3 (suppression is delivery-level)", len(store.queries))
	}
}

// TestReplayGuard_PerStageIndependence (Test B): consuming an item does not
// mark it consumed for other memory-consuming stages.
func TestReplayGuard_PerStageIndependence(t *testing.T) {
	root := t.TempDir()
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(42, root, "convention A")},
	}}
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "test",
		Stages: []schemas.ExecutionStage{
			{Name: "code_writer"},
			{Name: "test_generator"},
		},
	}
	tr := newRunTraceAccumulator(nil, "run-b", "session", root, plan, "active", nil)

	// code_writer consumes observation 42.
	cw := prepareCognition(t, store, plan, "code_writer", "task", root, 1, tr)
	if len(cw.MemoryBundle.Observations) != 1 {
		t.Fatalf("code_writer first delivery = %d, want 1", len(cw.MemoryBundle.Observations))
	}
	// test_generator (a different consuming stage) receives it once.
	tg := prepareCognition(t, store, plan, "test_generator", "task", root, 1, tr)
	if len(tg.MemoryBundle.Observations) != 1 {
		t.Fatalf("test_generator delivery = %d, want 1 (per-stage independence)", len(tg.MemoryBundle.Observations))
	}
	// Repairs: each stage's own re-entry is suppressed.
	cw2 := prepareCognition(t, store, plan, "code_writer", "task", root, 2, tr)
	tg2 := prepareCognition(t, store, plan, "test_generator", "task", root, 2, tr)
	if len(cw2.MemoryBundle.Observations) != 0 || len(tg2.MemoryBundle.Observations) != 0 {
		t.Fatalf("repairs must suppress: cw2=%v tg2=%v", cw2.MemoryBundle, tg2.MemoryBundle)
	}
}

// TestReplayGuard_NewItemSurvives (Test C): suppression is a consumed-set,
// not a memory-off switch. New observations stay fully deliverable.
func TestReplayGuard_NewItemSurvives(t *testing.T) {
	root := t.TempDir()
	first := schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(42, root, "convention A")},
	}
	store := &stubStore{bundle: first}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run-c", "session", root, plan, "active", nil)

	got := prepareCognition(t, store, plan, "code_writer", "task", root, 1, tr)
	if len(got.MemoryBundle.Observations) != 1 {
		t.Fatalf("first delivery = %d, want 1", len(got.MemoryBundle.Observations))
	}
	// The repair retrieval now also surfaces a NEW observation 77.
	store.bundle = schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations: []schemas.MemoryObservation{
			obsWithID(42, root, "convention A"),
			obsWithID(77, root, "convention B"),
		},
	}
	got2 := prepareCognition(t, store, plan, "code_writer", "task", root, 2, tr)
	if len(got2.MemoryBundle.Observations) != 1 {
		t.Fatalf("second delivery = %d, want exactly 1 (the new item)", len(got2.MemoryBundle.Observations))
	}
	if got2.MemoryBundle.Observations[0].ID != 77 {
		t.Fatalf("delivered item = %d, want 77 (42 suppressed, 77 new)", got2.MemoryBundle.Observations[0].ID)
	}
}

// TestReplayGuard_DirectPathNoFallback (Test D): a direct hit whose items
// are all already consumed must NOT push the stage into the broad search
// path; "already consumed" is not a retrieval miss.
func TestReplayGuard_DirectPathNoFallback(t *testing.T) {
	root, commit := cognitionFixtureRepo(t, "internal/auth/session.go", "package auth\n")
	obs := obsWithID(42, root, "session invalidation rule")
	obs.SourceCommit = &commit
	obs.TopicKey = ptr("file:internal/auth/session.go")
	store := &cognitionLookupStore{topics: map[string]schemas.MemoryBundle{
		"file:internal/auth/session.go": {RequestingAgent: "code_writer", Observations: []schemas.MemoryObservation{obs}},
	}}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run-d", "session", root, plan, "active", nil)

	first := prepareCognition(t, store, plan, "code_writer", "fix session invalidation in internal/auth/session.go#ResetPassword", root, 1, tr)
	if len(first.MemoryBundle.Observations) != 1 {
		t.Fatalf("first direct delivery = %d, want 1", len(first.MemoryBundle.Observations))
	}
	// Re-entry: the direct hit fires again (freshness memoized), but the
	// consumed item is suppressed. The search fallback must NOT run.
	second := prepareCognition(t, store, plan, "code_writer", "fix session invalidation in internal/auth/session.go", root, 2, tr)
	if len(second.MemoryBundle.Observations) != 0 {
		t.Fatalf("re-entry delivery = %d, want 0 (suppressed)", len(second.MemoryBundle.Observations))
	}
	if len(store.queries) != 0 {
		t.Fatalf("Search called %d times after direct suppression, want 0 (suppression is not a retrieval miss)", len(store.queries))
	}
	// Direct-path telemetry stays honest: the hit was still recorded.
	meta := tr.stages[stageKey{"code_writer", 2}]
	if meta.MemoryLookupMode != "direct" || meta.DirectHits != 1 {
		t.Fatalf("re-entry lookup meta = mode %q hits %d, want direct/1", meta.MemoryLookupMode, meta.DirectHits)
	}
}

// TestReplayGuard_SearchPathSuppression (Test E): the search path obeys the
// same invariant: retrieval can occur, delivery does not repeat.
func TestReplayGuard_SearchPathSuppression(t *testing.T) {
	root := t.TempDir()
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(42, root, "search-path convention")},
	}}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run-e", "session", root, plan, "active", nil)

	first := prepareCognition(t, store, plan, "code_writer", "task", root, 1, tr)
	if len(first.MemoryBundle.Observations) != 1 {
		t.Fatalf("first search delivery = %d, want 1", len(first.MemoryBundle.Observations))
	}
	second := prepareCognition(t, store, plan, "code_writer", "task", root, 2, tr)
	if len(second.MemoryBundle.Observations) != 0 {
		t.Fatalf("search re-delivery = %d, want 0", len(second.MemoryBundle.Observations))
	}
	if len(store.queries) != 2 {
		t.Fatalf("Search calls = %d, want 2", len(store.queries))
	}
}

// TestReplayGuard_ExemplarSuppressedPerStage (Test E-exemplar): exemplars
// obey the same run-local replay rule keyed exemplar:<run_id>.
func TestReplayGuard_ExemplarSuppressedPerStage(t *testing.T) {
	root := t.TempDir()
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Exemplars: []schemas.Exemplar{
			{RunID: "run_abc", Content: "prior kept run"},
		},
	}}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run-ex", "session", root, plan, "active", nil)

	first := prepareCognition(t, store, plan, "code_writer", "task", root, 1, tr)
	if len(first.MemoryBundle.Exemplars) != 1 {
		t.Fatalf("first exemplar delivery = %d, want 1", len(first.MemoryBundle.Exemplars))
	}
	second := prepareCognition(t, store, plan, "code_writer", "task", root, 2, tr)
	if len(second.MemoryBundle.Exemplars) != 0 {
		t.Fatalf("exemplar re-delivery = %d, want 0", len(second.MemoryBundle.Exemplars))
	}
	if got := tr.replaySuppressedCount(); got != 1 {
		t.Fatalf("replay suppressed = %d, want 1", got)
	}
}

// TestReplayGuard_RetrieveNoPromptNotConsumed (Test G): retrieve-no-prompt
// retrieves and records but never delivers, so nothing becomes consumed.
func TestReplayGuard_RetrieveNoPromptNotConsumed(t *testing.T) {
	t.Setenv("SPLICE_EXEMPLAR_MODE", "retrieve-no-prompt")
	root := t.TempDir()
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(42, root, "never delivered")},
	}}
	plan := cognitionPlan("code_writer")
	tr := newRunTraceAccumulator(nil, "run-g", "session", root, plan, "active", nil)

	first := prepareCognition(t, store, plan, "code_writer", "task", root, 1, tr)
	if first.MemoryBundle == nil || len(first.MemoryBundle.Observations) != 0 {
		t.Fatalf("retrieve-no-prompt must deliver nothing, got %v", first.MemoryBundle)
	}
	second := prepareCognition(t, store, plan, "code_writer", "task", root, 2, tr)
	if len(second.MemoryBundle.Observations) != 0 {
		t.Fatalf("retrieve-no-prompt must never deliver, got %v", second.MemoryBundle.Observations)
	}
	if got := tr.replaySuppressedCount(); got != 0 {
		t.Fatalf("replay suppressed = %d, want 0 (nothing was ever delivered, so nothing is consumed)", got)
	}
}

// TestReplayGuard_RunBoundaryReset (Test H): consumed state never leaks
// between runs; a fresh accumulator starts empty.
func TestReplayGuard_RunBoundaryReset(t *testing.T) {
	root := t.TempDir()
	store := &stubStore{bundle: schemas.MemoryBundle{
		RequestingAgent: "code_writer",
		Observations:    []schemas.MemoryObservation{obsWithID(42, root, "cross-run convention")},
	}}
	plan := cognitionPlan("code_writer")

	runOne := newRunTraceAccumulator(nil, "run-1", "session-1", root, plan, "active", nil)
	first := prepareCognition(t, store, plan, "code_writer", "task", root, 1, runOne)
	if len(first.MemoryBundle.Observations) != 1 {
		t.Fatalf("run 1 delivery = %d, want 1", len(first.MemoryBundle.Observations))
	}
	replay := prepareCognition(t, store, plan, "code_writer", "task", root, 2, runOne)
	if len(replay.MemoryBundle.Observations) != 0 {
		t.Fatalf("run 1 replay = %d, want 0", len(replay.MemoryBundle.Observations))
	}

	runTwo := newRunTraceAccumulator(nil, "run-2", "session-2", root, plan, "active", nil)
	second := prepareCognition(t, store, plan, "code_writer", "task", root, 1, runTwo)
	if len(second.MemoryBundle.Observations) != 1 {
		t.Fatalf("run 2 delivery = %d, want 1 (consumed state must not leak across runs)", len(second.MemoryBundle.Observations))
	}
}
