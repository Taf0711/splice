package store_test

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/memd/store"
)

// The ranked search must return ranks aligned with results and ordered by
// BM25 (more negative = more relevant). Seeded corpus: one observation that
// clearly matches the query and one unrelated control.
func TestSearchRanked_RanksAlignAndOrder(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	obsA, err := s.UpsertObservation(ctx, baseObs("agent-1", "InvalidateSession hardening",
		"always invalidate the session store after a password change"))
	if err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	obsB, err := s.UpsertObservation(ctx, baseObs("agent-1", "Storage notes",
		"the store room needs new shelving"))
	if err != nil {
		t.Fatalf("upsert B: %v", err)
	}

	q := &store.Query{
		RequestingAgent: "agent-1",
		QueryText:       "invalidate session store",
		Scopes:          []string{"project"},
		IncludePrivate:  true,
		Limit:           8,
	}
	results, truncated, ranks, err := s.SearchRanked(ctx, q)
	if err != nil {
		t.Fatalf("search ranked: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(results) != 2 || len(ranks) != 2 {
		t.Fatalf("got %d results, %d ranks; want 2 and 2", len(results), len(ranks))
	}
	if results[0].ID != obsA.ID {
		t.Fatalf("ranked first = id %d, want the matching observation %d", results[0].ID, obsA.ID)
	}
	if results[1].ID != obsB.ID {
		t.Fatalf("ranked second = id %d, want %d", results[1].ID, obsB.ID)
	}
	// BM25 is negative; more negative = more relevant.
	if !(ranks[0] < ranks[1]) {
		t.Fatalf("ranks not BM25-ordered: %v", ranks)
	}
	if ranks[0] > 0 || ranks[1] > 0 {
		t.Fatalf("BM25 ranks must be non-positive, got %v", ranks)
	}
}

// A query with no matches returns empty, not an error.
func TestSearchRanked_EmptyOnNoMatch(t *testing.T) {
	s := newStore(t)
	results, truncated, ranks, err := s.SearchRanked(context.Background(), &store.Query{
		RequestingAgent: "agent-1",
		QueryText:       "zzzunmatchable",
		Scopes:          []string{"project"},
		IncludePrivate:  true,
	})
	if err != nil {
		t.Fatalf("search ranked: %v", err)
	}
	if truncated || len(results) != 0 || len(ranks) != 0 {
		t.Fatalf("want empty (results=%d ranks=%d truncated=%v)", len(results), len(ranks), truncated)
	}
}

// Search and SearchRanked agree on ordering for the same query: the wrapper
// must not change the contract, only add ranks.
func TestSearchRanked_SearchOrderAgreement(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.UpsertObservation(ctx, baseObs("agent-1", "cache invalidation policy",
		"the freshness cache drops entries on generation bump")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := s.UpsertObservation(ctx, baseObs("agent-1", "weather log",
		"rain expected tuesday")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	q := &store.Query{
		RequestingAgent: "agent-1",
		QueryText:       "cache invalidation freshness",
		Scopes:          []string{"project"},
		IncludePrivate:  true,
	}
	a, _, err := s.Search(ctx, q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	b, _, _, err := s.SearchRanked(ctx, q)
	if err != nil {
		t.Fatalf("search ranked: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("order mismatch at %d: %d vs %d", i, a[i].ID, b[i].ID)
		}
	}
}
