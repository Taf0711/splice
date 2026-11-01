package store

// retrieval_eval_test.go — EVAL-V0 deliverable E: a labeled retrieval corpus
// over the real FTS5/BM25 store. Every case seeds observations with known
// IDs, runs the real SearchRanked path, and asserts a LABELED expectation:
// which observation ID must rank first (hit) or that no irrelevant
// observation surfaces at all (miss). Precision and recall are measured at
// the layer the pipeline actually calls, not at the tokenizer.

import (
	"context"
	"testing"
)

// corpusCase is one labeled retrieval query.
type corpusCase struct {
	name     string
	query    string
	seed     []seedObs
	topTitle string // expected first-ranked observation title; empty = no assertion
	noMatch  bool   // expect an empty result set (irrelevant query)
}

type seedObs struct {
	title   string
	content string
}

func seedObservations(t *testing.T, store *Store, seeds []seedObs) {
	t.Helper()
	for _, s := range seeds {
		created, err := store.UpsertObservation(context.Background(), &Observation{
			Scope:      "project",
			OwnerAgent: "splice",
			Title:      s.title,
			Content:    s.content,
			MemoryType: "note",
			Visibility: "shareable",
		})
		if err != nil {
			t.Fatalf("seed %q: %v", s.title, err)
		}
		_ = created
	}
}

func searchCorpus(t *testing.T, store *Store, query string) []*Observation {
	t.Helper()
	obs, _, _, err := store.SearchRanked(context.Background(), &Query{
		QueryText:        query,
		Scopes:           []string{"project"},
		IncludeShareable: true,
		Limit:            5,
	})
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	return obs
}

func runCorpusCase(t *testing.T, tc corpusCase) {
	t.Helper()
	store := newTestStore(t)
	seedObservations(t, store, tc.seed)
	obs := searchCorpus(t, store, tc.query)
	if tc.noMatch {
		if len(obs) != 0 {
			t.Fatalf("query %q: expected no results, got %d (top: %q)", tc.query, len(obs), obs[0].Title)
		}
		return
	}
	if len(obs) == 0 {
		t.Fatalf("query %q: expected a hit, got none", tc.query)
	}
	if tc.topTitle != "" && obs[0].Title != tc.topTitle {
		t.Fatalf("query %q: top hit title = %q, want %q", tc.query, obs[0].Title, tc.topTitle)
	}
}

// evalCorpus is the labeled corpus: session-service domain observations
// (matching the taskset-v0 fixture world) plus adversarial near-miss pairs.
var evalCorpus = []corpusCase{
	{
		name:  "ttl hits the expiry note, not the sweep note",
		query: "session TTL expiry duration configuration",
		seed: []seedObs{
			{"sessionTTL is the single source of truth", "All expiry decisions read the sessionTTL constant in main.go; no duration literals elsewhere."},
			{"sweep removes expired sessions", "The sweep loop deletes sessions whose LastSeen is older than the TTL."},
		},
		topTitle: "sessionTTL is the single source of truth",
	},
	{
		name:  "invalidation hits the reset note",
		query: "password reset invalidate sessions delete all user sessions",
		seed: []seedObs{
			{"password reset invalidates sessions", "Password reset deletes every live session belonging to the user."},
			{"healthz payload schema", "The healthz payload evolves additively: status ok and sessions count."},
		},
		topTitle: "password reset invalidates sessions",
	},
	{
		name:  "id validation hits the security note",
		query: "session id validation ascii characters boundary",
		seed: []seedObs{
			{"session ids validated at every boundary", "ValidSessionID allows 2 to 64 ASCII letters digits hyphens; Get returns ErrInvalidID."},
			{"body cap returns 413", "Request bodies larger than the cap answer 413."},
		},
		topTitle: "session ids validated at every boundary",
	},
	{
		name:  "invalidation rule beats healthz on signout query",
		query: "force sign out user sessions",
		seed: []seedObs{
			{"password reset invalidates sessions", "Password reset deletes every live session belonging to the user."},
			{"healthz payload schema", "The healthz payload evolves additively: status ok and sessions count."},
		},
		topTitle: "password reset invalidates sessions",
	},
	{
		name:  "irrelevant query returns nothing",
		query: "kubernetes cluster autoscaling pods",
		seed: []seedObs{
			{"json tags convention", "Every wire struct gets explicit JSON field tags."},
			{"delete idempotence", "Deleting an already-deleted session is a silent no-op."},
		},
		noMatch: true,
	},
	{
		// Labeled MISS (known limit, recorded not hidden): FTS5 tokenizes
		// without stemming, so "locking" does not match "Lock" and
		// "concurrent" has no surface overlap with the note. Morphological
		// variants need the miss-path rerank (C1c) or an FTS tokenizer
		// change; neither is in scope for V0.
		name:  "morphological variant query is a labeled miss",
		query: "concurrent map access locking",
		seed: []seedObs{
			{"lock discipline RLock reads Lock mutates", "Read paths take RLock and must not mutate; mutators take Lock."},
			{"graceful shutdown drains requests", "Shutdown waits for in-flight requests then exits."},
		},
		noMatch: true,
	},
	{
		name:  "constant-time compare beats generic auth",
		query: "token compare authentication security",
		seed: []seedObs{
			{"auth token uses constant-time compare", "The shared token check uses a constant-time comparison to stop timing attacks."},
			{"content type enforcement 415", "Requests without the expected content type answer 415."},
		},
		topTitle: "auth token uses constant-time compare",
	},
	{
		name:  "error mapping table beats single status",
		query: "map store errors http status codes handler",
		seed: []seedObs{
			{"mapStoreError is the status table", "Store errors translate to 404 409 400 429 through mapStoreError."},
			{"rate limit per id", "Create is rate limited per user id with exact accepted counts."},
		},
		topTitle: "mapStoreError is the status table",
	},
	{
		name:  "table-driven tests convention",
		query: "table driven tests clock controllable",
		seed: []seedObs{
			{"tests are table driven with controllable clocks", "Store tests use one table and the store.now func field."},
			{"field projection query parameter", "GET session supports fields=user to shrink payloads."},
		},
		topTitle: "tests are table driven with controllable clocks",
	},
	{
		name:  "racing observations: race detector note wins",
		query: "race detector exact accepted counts",
		seed: []seedObs{
			{"race verified counter semantics", "The probe runs under the race detector and asserts exact accepted counts."},
			{"per user session cap", "Users hold at most five sessions; the cap is enforced on create."},
		},
		topTitle: "race verified counter semantics",
	},
	{
		name:  "docs pairing observation",
		query: "readme documents endpoint semantics docs",
		seed: []seedObs{
			{"docs and code pairing rule", "The readme documents the delete endpoint semantics alongside the code."},
			{"uptime field additive", "uptime_seconds extends healthz without retyping sessions."},
		},
		topTitle: "docs and code pairing rule",
	},
}

func TestRetrievalCorpus(t *testing.T) {
	for _, tc := range evalCorpus {
		t.Run(tc.name, func(t *testing.T) {
			runCorpusCase(t, tc)
		})
	}
}

// No-cross-topic-slop sweep: unrelated queries must surface none of the
// seeded observations. Every (query, observation) pair is a labeled miss.
func TestRetrievalCorpusNoCrossTopicSlop(t *testing.T) {
	queries := []string{
		"kubernetes autoscaling",
		"favorite pizza toppings",
		"quarterly revenue forecast",
	}
	seeds := []seedObs{
		{"sessionTTL source of truth", "expiry decisions read the sessionTTL constant"},
		{"lock discipline", "RLock for reads, Lock for mutators"},
		{"id validation", "ValidSessionID bounds and charset"},
	}
	store := newTestStore(t)
	seedObservations(t, store, seeds)
	for _, q := range queries {
		obs := searchCorpus(t, store, q)
		for _, o := range obs {
			t.Errorf("query %q surfaced unrelated observation %d (%q)", q, o.ID, o.Title)
		}
	}
}

// Recall guard: the corpus must retrieve the labeled observation even when
// the query uses different surface words (synonym query).
func TestRetrievalCorpusSynonymRecall(t *testing.T) {
	store := newTestStore(t)
	seedObservations(t, store, []seedObs{
		{"sign out everywhere", "forced sign-out deletes every live session for the user"},
		{"paged listing", "list sessions returns one page at a time with a cursor"},
	})
	obs := searchCorpus(t, store, "force logout user sessions")
	if len(obs) == 0 {
		t.Fatal("synonym query returned nothing; recall broken")
	}
	found := false
	for _, o := range obs {
		if o.Title == "sign out everywhere" {
			found = true
		}
	}
	if !found {
		t.Fatalf("synonym query did not retrieve the sign-out observation; got %d results", len(obs))
	}
}
