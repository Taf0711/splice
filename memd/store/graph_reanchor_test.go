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

func TestReanchorByIDsScopedToCaptureSet(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	// Two nodes in the same project at the same source revision: one
	// captured by THIS run (in the capture set), one by another run (not).
	captured, err := st.UpsertNode(ctx, NodeInput{
		Kind: NodeKindFact, Claim: "captured by this run",
		ProjectPath: "/repo/warm", Status: NodeStatusActive,
		VerifiedRevision: "1111111111",
		Anchors:          []AnchorInput{{Kind: "file", Value: "captured.go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	other, uerr := st.UpsertNode(ctx, NodeInput{
		Kind: NodeKindFact, Claim: "captured by another run",
		ProjectPath: "/repo/warm", Status: NodeStatusActive,
		VerifiedRevision: "1111111111",
		Anchors:          []AnchorInput{{Kind: "file", Value: "other.go"}},
	})
	if uerr != nil {
		t.Fatal(uerr)
	}

	count, err := st.ReanchorByIDs(ctx, "/repo/warm", []int64{captured.ID}, "1111111111", "2222222222")
	if err != nil {
		t.Fatalf("reanchor by ids: %v", err)
	}
	if count != 1 {
		t.Fatalf("advanced %d nodes, want 1", count)
	}

	// The captured node advanced; the other run's node did not.
	capNodes, err := st.GetExact(ctx, map[string][]string{"file": {"captured.go"}}, GetExactOptions{ProjectPath: "/repo/warm"})
	if err != nil || len(capNodes) != 1 {
		t.Fatalf("captured node exact: %v %d", err, len(capNodes))
	}
	if capNodes[0].VerifiedRevision.String != "2222222222" {
		t.Fatalf("capture set node revision = %s, want 2222222222", capNodes[0].VerifiedRevision.String)
	}
	// The other run's node must stay at the old revision. ReanchorByIDs
	// returns an error if any requested id mismatches, and `other` was not
	// in the request, so verify via a fresh exact query on its anchor.
	otherNodes, err := st.GetExact(ctx, map[string][]string{"file": {"other.go"}}, GetExactOptions{ProjectPath: "/repo/warm"})
	if err != nil || len(otherNodes) != 1 {
		t.Fatalf("other node exact: %v %d", err, len(otherNodes))
	}
	if otherNodes[0].ID != other.ID {
		t.Fatalf("other node id mismatch: %d vs %d", otherNodes[0].ID, other.ID)
	}
	if otherNodes[0].VerifiedRevision.String != "1111111111" {
		t.Fatalf("other run's node must stay at the old revision, got %s", otherNodes[0].VerifiedRevision.String)
	}

	// Wrong-source-revision reanchor fails loud.
	if _, err := st.ReanchorByIDs(ctx, "/repo/warm", []int64{captured.ID}, "1111111111", "3333333333"); err == nil {
		t.Fatal("reanchor from a revision the node is not anchored at must fail loud")
	}
}
