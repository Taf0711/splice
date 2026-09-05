package store

import (
	"context"
	"testing"
)

func TestReanchorAdvancesVerifiedRevision(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	n, err := st.UpsertNode(ctx, NodeInput{
		Kind:             NodeKindFact,
		Claim:            "internal/session/store.go defines Store.ActiveSessionsFor",
		ProjectPath:      "/repo/warm",
		Status:           NodeStatusActive,
		VerifiedRevision: "1111111111",
		Anchors:          []AnchorInput{{Kind: "file", Value: "internal/session/store.go"}},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	count, err := st.Reanchor(ctx, "/repo/warm", "1111111111", "2222222222")
	if err != nil {
		t.Fatalf("reanchor: %v", err)
	}
	if count != 1 {
		t.Fatalf("reanchored %d nodes, want 1", count)
	}
	got, err := st.GetExact(ctx, map[string][]string{"file": {"internal/session/store.go"}}, GetExactOptions{ProjectPath: "/repo/warm"})
	if err != nil {
		t.Fatalf("exact after reanchor: %v", err)
	}
	if len(got) != 1 || got[0].ID != n.ID || !got[0].VerifiedRevision.Valid || got[0].VerifiedRevision.String != "2222222222" {
		t.Fatalf("node after reanchor = %+v, want verified_revision 2222222222", got)
	}
}

func TestReanchorValidation(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()
	if _, err := st.Reanchor(ctx, "", "a", "b"); err == nil {
		t.Fatal("empty project must fail")
	}
	if _, err := st.Reanchor(ctx, "/p", "", "b"); err == nil {
		t.Fatal("empty from-revision must fail")
	}
	if _, err := st.Reanchor(ctx, "/p", "a", "a"); err == nil {
		t.Fatal("identical revisions must fail")
	}
}
