package store

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func newSemanticTestStore(t *testing.T) (*Store, *SemanticIndex) {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "semantic.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, NewSemanticIndex(st)
}

func TestSemanticTokenize(t *testing.T) {
	toks := semanticTokenize("The NewStore handles Session-Storage for users")
	if len(toks) == 0 {
		t.Fatal("expected tokens")
	}
	for _, tok := range toks {
		if len(tok) < 3 {
			t.Errorf("token %q shorter than 3 chars", tok)
		}
		lower := strings.ToLower(tok)
		if tok != lower {
			t.Errorf("token %q not lowercased", tok)
		}
	}
	// Stopwords dropped.
	for _, sw := range []string{"the", "for"} {
		for _, tok := range toks {
			if tok == sw {
				t.Errorf("stopword %q not dropped", sw)
			}
		}
	}
	// All tokens present.
	for _, want := range []string{"newstore", "handles", "session", "storage", "users"} {
		found := false
		for _, tok := range toks {
			if tok == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected token %q in %v", want, toks)
		}
	}
}

func TestSemanticEmbedL2Normalized(t *testing.T) {
	vec := semanticEmbed("retry the failed test run")
	var sum float64
	for _, v := range vec {
		sum += v * v
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("expected L2 norm 1, got %f", sum)
	}
	// Deterministic.
	vec2 := semanticEmbed("retry the failed test run")
	for i := range vec {
		if vec[i] != vec2[i] {
			t.Fatalf("embedding not deterministic at bucket %d", i)
		}
	}
	// Empty text: zero vector, no panic.
	zero := semanticEmbed("the and for")
	norm := 0.0
	for _, v := range zero {
		norm += v * v
	}
	if norm != 0 {
		t.Errorf("expected zero vector for stopword-only text, got norm %f", norm)
	}
}

func TestSemanticRoundTripEncodeDecode(t *testing.T) {
	original := semanticEmbed("compact graph nodes deterministically")
	blob := encodeVector(original)
	decoded, err := decodeVector(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(decoded), len(original))
	}
	for i := range original {
		// float32 precision loses about 7 digits.
		if math.Abs(original[i]-decoded[i]) > 1e-6 {
			t.Errorf("bucket %d: %f vs %f", i, original[i], decoded[i])
		}
	}
}

func TestSemanticSearchFindsRelatedNode(t *testing.T) {
	st, idx := newSemanticTestStore(t)
	ctx := context.Background()

	sessionNode, err := st.UpsertNode(ctx, NodeInput{
		Kind:  NodeKindDecision,
		Claim: "session storage opens one SQLite store per project",
	})
	if err != nil {
		t.Fatalf("session node: %v", err)
	}
	embedNode, err := st.UpsertNode(ctx, NodeInput{
		Kind:  NodeKindFact,
		Claim: "semantic vectors are hashed fnv buckets, l2 normalized",
	})
	if err != nil {
		t.Fatalf("embed node: %v", err)
	}
	releaseNode, err := st.UpsertNode(ctx, NodeInput{
		Kind:  NodeKindProcedure,
		Claim: "run the release build through make and sign the binary",
	})
	if err != nil {
		t.Fatalf("release node: %v", err)
	}
	for _, n := range []struct {
		id    int64
		claim string
	}{
		{sessionNode.ID, sessionNode.Claim},
		{embedNode.ID, embedNode.Claim},
		{releaseNode.ID, releaseNode.Claim},
	} {
		if err := idx.IndexNode(ctx, n.id, n.claim); err != nil {
			t.Fatalf("index node %d: %v", n.id, err)
		}
	}

	hits, err := idx.Search(ctx, "how does session storage work", 2, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	if hits[0].NodeID != sessionNode.ID {
		t.Errorf("expected session node %d in top hit, got %d", sessionNode.ID, hits[0].NodeID)
	}
	// Scores are cosine similarities in [-1, 1]; a positive match expected.
	if hits[0].Score <= 0 {
		t.Errorf("expected positive similarity, got %f", hits[0].Score)
	}

	// Irrelevant query ranks the unrelated node lower, not first.
	hits, err = idx.Search(ctx, "sign the release binary", 3, "")
	if err != nil {
		t.Fatalf("search 2: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits for second query")
	}
	if hits[0].NodeID == sessionNode.ID && len(hits) > 1 {
		t.Errorf("expected release-procedure node to outrank session node for this query")
	}
}

func TestSemanticSearchEmptyIndexFallback(t *testing.T) {
	st, idx := newSemanticTestStore(t)
	ctx := context.Background()

	hits, err := idx.Search(ctx, "anything at all", 8, "")
	if err != nil {
		t.Fatalf("empty index search returned error: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected empty result on empty index, got %d", len(hits))
	}

	// An indexed node that is later superseded drops out of search.
	node, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: "temporary claim about builds"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := idx.IndexNode(ctx, node.ID, node.Claim); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := st.SetStatus(ctx, node.ID, NodeStatusSuperseded); err != nil {
		t.Fatalf("set status: %v", err)
	}
	hits, err = idx.Search(ctx, "builds", 8, "")
	if err != nil {
		t.Fatalf("search after supersede: %v", err)
	}
	for _, h := range hits {
		if h.NodeID == node.ID {
			t.Errorf("expected superseded node to be excluded from search")
		}
	}

	// Empty search text fails loud.
	if _, err := idx.Search(ctx, "  ", 8, ""); err == nil {
		t.Error("expected empty search text to fail")
	}
}

func TestSemanticIndexReindexAll(t *testing.T) {
	st, idx := newSemanticTestStore(t)
	ctx := context.Background()
	for _, claim := range []string{"first claim about parsers", "second claim about storage"} {
		if _, err := st.UpsertNode(ctx, NodeInput{Kind: NodeKindFact, Claim: claim}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	n, err := idx.ReindexAll(ctx)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 nodes indexed, got %d", n)
	}
	hits, err := idx.Search(ctx, "storage", 1, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected a hit after reindex")
	}
}

func TestSemanticSearchProjectScoped(t *testing.T) {
	st := newGraphTestStore(t)
	idx := NewSemanticIndex(st)
	ctx := context.Background()

	n1, err := st.UpsertNode(ctx, NodeInput{
		Kind: NodeKindFact, Claim: "session storage keeps tokens in memory", ProjectPath: "/repo/one",
	})
	if err != nil {
		t.Fatalf("upsert one: %v", err)
	}
	n2, err := st.UpsertNode(ctx, NodeInput{
		Kind: NodeKindFact, Claim: "session storage keeps tokens in memory", ProjectPath: "/repo/two",
	})
	if err != nil {
		t.Fatalf("upsert two: %v", err)
	}
	if err := idx.IndexNode(ctx, n1.ID, n1.Claim); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexNode(ctx, n2.ID, n2.Claim); err != nil {
		t.Fatal(err)
	}

	hits, err := idx.Search(ctx, "session storage", 8, "/repo/two")
	if err != nil {
		t.Fatalf("scoped search: %v", err)
	}
	if len(hits) != 1 || hits[0].NodeID != n2.ID {
		t.Fatalf("scoped search returned %+v, want only project two's node", hits)
	}
}
