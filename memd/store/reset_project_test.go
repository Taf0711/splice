package store_test

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/memd/store"
)

// seedForProject inserts one observation bound to the given project path.
// Each call gets a unique topic key so the store's topic-key upsert (which
// updates in place on owner+project+topic match) never merges two seeds.
func seedForProject(t *testing.T, s *store.Store, project, title, content string) {
	t.Helper()
	obs := baseObs("code_writer", title, content)
	obs.ProjectPath = ns(project)
	obs.TopicKey = ns("file:" + title + ".go")
	if _, err := s.UpsertObservation(context.Background(), obs); err != nil {
		t.Fatalf("upsert seed: %v", err)
	}
}

// searchProject runs the warm arm's retrieval path (FTS over one project,
// private rows included) and returns the hits.
func searchProject(t *testing.T, s *store.Store, project, query string) []*store.Observation {
	t.Helper()
	hits, _, err := s.Search(context.Background(), &store.Query{
		ProjectPath:     project,
		RequestingAgent: "code_writer",
		QueryText:       query,
		Scopes:          []string{"project"},
		IncludePrivate:  true,
		Limit:           100,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	return hits
}

// traceRowFor builds a minimal settled trace row for a repo root.
func traceRowFor(runID, repoRoot string) *store.TraceRow {
	return &store.TraceRow{
		RunID:     runID,
		SessionID: runID,
		RepoRoot:  repoRoot,
		Tier:      "light",
		Status:    "completed",
		Intent:    "probe " + runID,
		Payload:   []byte(`{"schema_version":"x","run_id":"` + runID + `"}`),
	}
}

// mustUpsertTrace inserts a trace row or fails the test.
func mustUpsertTrace(t *testing.T, s *store.Store, row *store.TraceRow) {
	t.Helper()
	if _, err := s.UpsertTrace(context.Background(), row); err != nil {
		t.Fatalf("upsert trace: %v", err)
	}
}

// TestResetProject_RemovesOnlyTargetProject pins the isolation primitive's
// blast radius: a reset wipes exactly one project's observations and traces
// and leaves every other project untouched.
func TestResetProject_RemovesOnlyTargetProject(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	seedForProject(t, s, "/arm/warm", "warm seed", "seeded observation")
	seedForProject(t, s, "/arm/warm", "warm learned", "attempt 1 learning")
	seedForProject(t, s, "/other/project", "other seed", "must survive")

	mustUpsertTrace(t, s, traceRowFor("run-warm-1", "/arm/warm"))
	mustUpsertTrace(t, s, traceRowFor("run-other-1", "/other/project"))

	counts, err := s.ResetProject(ctx, "/arm/warm")
	if err != nil {
		t.Fatalf("ResetProject: %v", err)
	}
	if counts.Observations != 2 {
		t.Fatalf("observations removed = %d, want 2", counts.Observations)
	}
	if counts.Traces != 1 {
		t.Fatalf("traces removed = %d, want 1", counts.Traces)
	}
	if got := searchProject(t, s, "/arm/warm", "seeded observation learning"); len(got) != 0 {
		t.Fatalf("target project still returns %d hits after reset", len(got))
	}
	if got := searchProject(t, s, "/other/project", "must survive"); len(got) != 1 {
		t.Fatalf("other project lost observations: has %d hits, want 1", len(got))
	}
	others, err := s.QueryTraces(ctx, store.TraceFilter{RepoRoot: "/other/project"})
	if err != nil {
		t.Fatalf("query other traces: %v", err)
	}
	if len(others) != 1 || others[0].Trace.RunID != "run-other-1" {
		t.Fatalf("other project's traces damaged: %+v", others)
	}
}

// TestResetProject_SearchAndFTSClean pins that a reset removes rows from the
// FTS index too: a full-text query must not resurface deleted cognition.
// FTS is the warm arm's retrieval path, so a stale index row would break the
// isolation invariant even with the base table empty.
func TestResetProject_SearchAndFTSClean(t *testing.T) {
	s := newStore(t)

	seedForProject(t, s, "/arm/warm", "idempotent delete convention", "Delete must be idempotent and return the count")
	if got := searchProject(t, s, "/arm/warm", "idempotent delete"); len(got) != 1 {
		t.Fatalf("precondition: %d hits, want 1", len(got))
	}

	if _, err := s.ResetProject(context.Background(), "/arm/warm"); err != nil {
		t.Fatalf("ResetProject: %v", err)
	}

	hits := searchProject(t, s, "/arm/warm", "idempotent delete convention")
	if len(hits) != 0 {
		t.Fatalf("retrieval still returns %d rows after reset; the index kept deleted cognition", len(hits))
	}
}

// TestResetProject_AttemptSequence models the causal invariant end to end:
// warm attempt 1 runs, leaves learned cognition; the reset-then-reseed
// sequence restores EXACTLY the seeded state, so attempt 2 cannot see
// attempt 1's experience. This is the regression test for the rollout
// independence requirement.
func TestResetProject_AttemptSequence(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	const project = "/arm/warm"

	// Attempt 1: seeded state, then the run learns something new.
	seedForProject(t, s, project, "seed", "seeded convention observation")
	mustUpsertTrace(t, s, traceRowFor("run-1", project))
	seedForProject(t, s, project, "learned-in-attempt-1", "experience from attempt 1")

	// Between attempts: reset + reseed (what the harness does).
	counts, err := s.ResetProject(ctx, project)
	if err != nil {
		t.Fatalf("ResetProject: %v", err)
	}
	if counts.Observations != 2 || counts.Traces != 1 {
		t.Fatalf("counts = %+v, want 2 obs and 1 trace", counts)
	}
	seedForProject(t, s, project, "seed", "seeded convention observation")

	// Attempt 2's retrieval sees ONLY the seed: attempt 1's experience is
	// unretrievable.
	remaining := searchProject(t, s, project, "seeded convention experience attempt")
	if len(remaining) != 1 {
		t.Fatalf("attempt 2 sees %d observations, want exactly 1 (the seed)", len(remaining))
	}
	if remaining[0].Title != "seed" {
		t.Fatalf("surviving observation = %q, want the seed", remaining[0].Title)
	}
	traces, err := s.QueryTraces(ctx, store.TraceFilter{RepoRoot: project})
	if err != nil {
		t.Fatalf("query traces: %v", err)
	}
	if len(traces) != 0 {
		t.Fatalf("attempt 1's trace survived the reset; attempt 2 could join stale telemetry")
	}
}

// TestResetProject_EmptyAndMissing pin the honest-count contract: resetting
// a project that holds nothing succeeds and reports zero (it must not
// error, and must not fabricate nonzero counts).
func TestResetProject_EmptyAndMissing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	counts, err := s.ResetProject(ctx, "/never/seen")
	if err != nil {
		t.Fatalf("reset of unknown project: %v", err)
	}
	if counts.Observations != 0 || counts.Traces != 0 {
		t.Fatalf("counts = %+v, want zeros", counts)
	}

	if _, err := s.ResetProject(ctx, ""); err == nil {
		t.Fatal("empty project path must fail loud")
	}
}

// TestResetProject_HardDelete proves the reset hard-deletes rather than
// tombstoning: a deleted_at-covered row must be physically gone so it can
// never re-enter retrieval through any path.
func TestResetProject_HardDelete(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedForProject(t, s, "/arm/warm", "to be removed", "content")

	if _, err := s.ResetProject(ctx, "/arm/warm"); err != nil {
		t.Fatalf("ResetProject: %v", err)
	}

	var n int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM observations WHERE project_path = ?`, "/arm/warm").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d rows remain in the raw table after reset (tombstones are not isolation)", n)
	}
}
