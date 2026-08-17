package store

import (
	"context"
	"testing"
)

// TestTraceQueryVerdictFilter pins that the verdict filter uses the latest
// verdict join: kept runs are returned for Verdict=kept, rejected runs are
// not, and runs with no verdict (unknown) never match.
func TestTraceQueryVerdictFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"kept-1", "rejected-1", "unknown-1"} {
		if _, err := s.UpsertTrace(ctx, &TraceRow{
			RunID: id, RepoRoot: "/repo", Tier: "light", Status: "completed",
			Intent: "add a Hello function", CreatedAt: 1000, Payload: tracePayload(id, "/repo"),
		}); err != nil {
			t.Fatalf("UpsertTrace %s: %v", id, err)
		}
	}
	if err := s.UpsertVerdict(ctx, &VerdictRow{RunID: "kept-1", DecidedAt: 1000, Verdict: "kept", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("verdict kept: %v", err)
	}
	if err := s.UpsertVerdict(ctx, &VerdictRow{RunID: "rejected-1", DecidedAt: 1000, Verdict: "rejected", Reason: "wrong_approach", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("verdict rejected: %v", err)
	}

	kept, err := s.QueryTraces(ctx, TraceFilter{RepoRoot: "/repo", Verdict: "kept"})
	if err != nil {
		t.Fatalf("kept query: %v", err)
	}
	if len(kept) != 1 || kept[0].Trace.RunID != "kept-1" {
		t.Fatalf("kept query = %d rows, want only kept-1", len(kept))
	}

	rejected, err := s.QueryTraces(ctx, TraceFilter{RepoRoot: "/repo", Verdict: "rejected"})
	if err != nil {
		t.Fatalf("rejected query: %v", err)
	}
	if len(rejected) != 1 || rejected[0].Trace.RunID != "rejected-1" {
		t.Fatalf("rejected query = %d rows, want only rejected-1", len(rejected))
	}
}

// TestTraceQueryFTSMatch pins the intent FTS: a matching intent is returned
// with a negative rank, an unrelated query returns nothing, and a NULL-intent
// legacy row never matches.
func TestTraceQueryFTSMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.UpsertTrace(ctx, &TraceRow{
		RunID: "match-1", RepoRoot: "/repo", Tier: "light", Status: "completed",
		Intent: "add a Hello function and tests", CreatedAt: 1000, Payload: tracePayload("match-1", "/repo"),
	}); err != nil {
		t.Fatalf("upsert match: %v", err)
	}
	if err := s.UpsertVerdict(ctx, &VerdictRow{RunID: "match-1", DecidedAt: 1000, Verdict: "kept", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	// A legacy row with NULL intent must never match any FTS query.
	if _, err := s.UpsertTrace(ctx, &TraceRow{
		RunID: "legacy-1", RepoRoot: "/repo", Tier: "light", Status: "completed",
		Intent: "", CreatedAt: 1000, Payload: tracePayload("legacy-1", "/repo"),
	}); err != nil {
		t.Fatalf("upsert legacy: %v", err)
	}
	if err := s.UpsertVerdict(ctx, &VerdictRow{RunID: "legacy-1", DecidedAt: 1000, Verdict: "kept", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("legacy verdict: %v", err)
	}

	matched, err := s.QueryTraces(ctx, TraceFilter{RepoRoot: "/repo", Verdict: "kept", Query: "add a Hello function"})
	if err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if len(matched) != 1 || matched[0].Trace.RunID != "match-1" {
		t.Fatalf("fts match = %d rows, want match-1 only", len(matched))
	}
	if matched[0].Rank >= 0 {
		t.Fatalf("rank = %f, want a negative bm25 score for a real match", matched[0].Rank)
	}

	unrelated, err := s.QueryTraces(ctx, TraceFilter{RepoRoot: "/repo", Verdict: "kept", Query: "quantum blockchain consensus"})
	if err != nil {
		t.Fatalf("unrelated query: %v", err)
	}
	if len(unrelated) != 0 {
		t.Fatalf("unrelated query = %d rows, want 0 (legacy NULL-intent must never match)", len(unrelated))
	}
}
