package memoryrank

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func obs(id int64, title, content string) schemas.MemoryObservation {
	return schemas.MemoryObservation{ID: id, Title: title, Content: content}
}

// The reranker promotes an exact topic-key match above a generic match with
// the same other metadata.
func TestRankExactTopicBeatsGeneric(t *testing.T) {
	key := "file:internal/auth/session.go"
	ctx := Context{TopicKeys: []string{key}, Intent: "harden the session flow", NowUnix: 1000}

	onTopic := obs(1, "Session flow", "how sessions are invalidated")
	onTopic.TopicKey = &key
	offTopic := obs(2, "Build notes", "build flags for release")

	got := Rank(Candidates{Observations: []schemas.MemoryObservation{offTopic, onTopic}}, ctx)
	if got[0].Observation.ID != 1 {
		t.Fatalf("exact-topic observation ranked %d, want first: %+v", got[0].Observation.ID, got)
	}
	if got[0].Features.ExactTopic != 1 {
		t.Fatalf("exact-topic feature = %v", got[0].Features.ExactTopic)
	}
}

// Identifier-aware tokenization: an observation containing the CamelCase
// components of an intent identifier outranks one that does not, without any
// exact topic key.
func TestRankIdentifierOverlap(t *testing.T) {
	ctx := Context{Intent: "fix InvalidateSession in the session store", NowUnix: 1000}
	match := obs(1, "Session store conventions", "InvalidateSession must clear the cache and the cookie")
	noise := obs(2, "Release process", "tag the release and publish artifacts")

	got := Rank(Candidates{Observations: []schemas.MemoryObservation{match, noise}}, ctx)
	if got[0].Observation.ID != 1 {
		t.Fatalf("identifier-overlapping observation ranked second: %+v", got)
	}
	if got[0].Features.IdentifierOverlap <= got[1].Features.IdentifierOverlap {
		t.Fatalf("overlap feature not separated: %v vs %v",
			got[0].Features.IdentifierOverlap, got[1].Features.IdentifierOverlap)
	}
}

// Determinism: shuffling the input 1000 times always yields the same order
// for the same candidate set.
func TestRankDeterministicUnderShuffle(t *testing.T) {
	ctx := Context{Intent: "session store invalidation", NowUnix: 1000}
	cands := Candidates{Observations: []schemas.MemoryObservation{
		obs(1, "Session store", "invalidate sessions on logout"),
		obs(2, "Cache policy", "the cache drops entries on bump"),
		obs(3, "Retry policy", "retries use exponential backoff"),
		obs(4, "Session routing", "session routing middleware"),
	}}
	want := Rank(cands, ctx)
	wantIDs := ids(want)

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		shuffled := Candidates{Observations: shuffle(rng, cands.Observations)}
		got := Rank(shuffled, ctx)
		gotIDs := ids(got)
		for j := range wantIDs {
			if gotIDs[j] != wantIDs[j] {
				t.Fatalf("shuffle %d changed order: got %v want %v", i, gotIDs, wantIDs)
			}
		}
	}
}

// Equal feature scores break ties by BM25 rank (more negative first).
func TestRankTieBreaksByBM25(t *testing.T) {
	ctx := Context{Intent: "unrelated query words", NowUnix: 1000}
	a := obs(1, "alpha topic", "some content here")
	b := obs(2, "beta topic", "some content here")
	// Identical metadata => identical features => tie. Ranks decide.
	got := Rank(Candidates{
		Observations: []schemas.MemoryObservation{a, b},
		Ranks:        []float64{-1.0, -4.0},
	}, ctx)
	if got[0].Observation.ID != 2 {
		t.Fatalf("BM25 tie-break failed: first id %d, want 2 (rank -4)", got[0].Observation.ID)
	}
}

// Missing ranks keep original order among ties (old sidecar parity).
func TestRankMissingRanksKeepOriginalOrder(t *testing.T) {
	ctx := Context{Intent: "unrelated query words", NowUnix: 1000}
	a := obs(1, "alpha topic", "same shape content")
	b := obs(2, "beta topic", "same shape content")
	got := Rank(Candidates{Observations: []schemas.MemoryObservation{a, b}}, ctx)
	if got[0].Observation.ID != 1 || got[1].Observation.ID != 2 {
		t.Fatalf("tie without ranks must keep original order, got %d then %d",
			got[0].Observation.ID, got[1].Observation.ID)
	}
}

// Nil and empty inputs never panic and return nil.
func TestRankNilInputs(t *testing.T) {
	if got := Rank(Candidates{}, Context{}); got != nil {
		t.Fatal("empty candidates must return nil")
	}
	if got := Rank(Candidates{Observations: nil, Ranks: []float64{1}}, Context{}); got != nil {
		t.Fatal("nil observations must return nil")
	}
}

// All features stay within [0,1] for hostile metadata.
func TestRankFeatureBounds(t *testing.T) {
	ctx := Context{Intent: "fix auth", NowUnix: 1000}
	huge := 1e18
	negative := -5.0
	o := obs(1, stringsRepe("internal/auth/session.go", 500), stringsRepe("InvalidateSession", 2000))
	o.Confidence = &huge
	o2 := obs(2, "x", "y")
	o2.Confidence = &negative
	reviewed := int64(50)
	o2.ReviewAfter = &reviewed
	got := Rank(Candidates{Observations: []schemas.MemoryObservation{o, o2}}, ctx)
	for _, s := range got {
		checkBound(t, "ExactTopic", s.Features.ExactTopic)
		checkBound(t, "ExactPath", s.Features.ExactPath)
		checkBound(t, "IdentifierOverlap", s.Features.IdentifierOverlap)
		checkBound(t, "SameStage", s.Features.SameStage)
		checkBound(t, "Provenance", s.Features.Provenance)
		checkBound(t, "Confidence", s.Features.Confidence)
		checkBound(t, "Recency", s.Features.Recency)
		if s.Score < 0 || s.Score > 1 {
			t.Fatalf("score out of [0,1]: %v", s.Score)
		}
	}
}

// Recency: fresher wins, absent timestamps decay to the floor.
func TestRecencyOrdering(t *testing.T) {
	now := int64(1_800_000_000)
	fresh := recency(now, now)
	monthOld := recency(now-30*86400, now)
	ancient := recency(0, now)
	if !(fresh > monthOld && monthOld > ancient) {
		t.Fatalf("recency not ordered: fresh=%v month=%v ancient=%v", fresh, monthOld, ancient)
	}
}

// Tokenizer contract from report section 27: identifiers split into
// components, originals retained.
func TestTokenizeIdentifierAware(t *testing.T) {
	got := toSet(Tokenize("InvalidateSession"))
	for _, want := range []string{"invalidatesession", "invalidate", "session"} {
		if !got[want] {
			t.Fatalf("tokenize missing %q: %v", want, got)
		}
	}
	got = toSet(Tokenize("session_store"))
	for _, want := range []string{"session_store", "session", "store"} {
		if !got[want] {
			t.Fatalf("tokenize missing %q: %v", want, got)
		}
	}
	got = toSet(Tokenize("internal/auth/session/store.go"))
	for _, want := range []string{"internal", "auth", "session", "store", "store.go"} {
		if !got[want] {
			t.Fatalf("tokenize missing %q: %v", want, got)
		}
	}
	// Digits-only tokens are dropped; single letters are dropped.
	got = toSet(Tokenize("http2 v2 a 42"))
	if got["42"] || got["a"] {
		t.Fatalf("noise tokens leaked: %v", got)
	}
}

// Same-stage provenance fires only on the requesting stage.
func TestSameStageFeature(t *testing.T) {
	ctx := Context{StageName: "code_writer", Intent: "words", NowUnix: 1000}
	stage := "code_writer"
	other := "test_runner"
	matching := obs(1, "t", "c")
	matching.SourceStage = &stage
	different := obs(2, "t", "c")
	different.SourceStage = &other

	got := Rank(Candidates{Observations: []schemas.MemoryObservation{matching, different}}, ctx)
	if got[0].Features.SameStage != 1 || got[1].Features.SameStage != 0 {
		t.Fatalf("same-stage features wrong: %v", got)
	}
}

func checkBound(t *testing.T, name string, v float64) {
	t.Helper()
	if v < 0 || v > 1 {
		t.Fatalf("feature %s out of [0,1]: %v", name, v)
	}
}

func ids(scored []Scored) []int64 {
	out := make([]int64, len(scored))
	for i, s := range scored {
		out[i] = s.Observation.ID
	}
	return out
}

func toSet(tokens []string) map[string]bool {
	out := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		out[tok] = true
	}
	return out
}

func stringsRepe(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s + " "
	}
	return fmt.Sprint(out)
}

func shuffle(rng *rand.Rand, in []schemas.MemoryObservation) []schemas.MemoryObservation {
	out := append([]schemas.MemoryObservation(nil), in...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
