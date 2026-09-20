package store

// Tests for the transactional all-or-nothing ReanchorByIDs and the
// producer-run-qualified CaptureSetIDs (finding F9).

import (
	"context"
	"strings"
	"testing"
)

// upsertReanchorNode is a small helper that persists one active node
// anchored at the given revision in the given project.
func upsertReanchorNode(t *testing.T, st *Store, project, claim, revision, sourceRunID string) Node {
	t.Helper()
	n, err := st.UpsertNode(context.Background(), NodeInput{
		Kind:             NodeKindFact,
		Claim:            claim,
		ProjectPath:      project,
		Status:           NodeStatusActive,
		VerifiedRevision: revision,
		SourceRunID:      sourceRunID,
		Anchors:          []AnchorInput{{Kind: "file", Value: claim + ".go"}},
	})
	if err != nil {
		t.Fatalf("upsert node %q: %v", claim, err)
	}
	return n
}

// nodeRevisionAt returns the verified_revision currently stored for the
// node behind the given anchor value.
func nodeRevisionAt(t *testing.T, st *Store, project, anchor string) string {
	t.Helper()
	nodes, err := st.GetExact(context.Background(), map[string][]string{"file": {anchor}}, GetExactOptions{ProjectPath: project})
	if err != nil || len(nodes) != 1 {
		t.Fatalf("exact %q: %v (%d nodes)", anchor, err, len(nodes))
	}
	return nodes[0].VerifiedRevision.String
}

func TestReanchorByIDsAllOrNothing(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	first := upsertReanchorNode(t, st, "/repo/warm", "valid first node", "1111111111", "run-a")
	second := upsertReanchorNode(t, st, "/repo/warm", "stale second node", "9999999999", "run-a")

	// The first ID is valid, the second is not anchored at fromRevision.
	_, err := st.ReanchorByIDs(ctx, "/repo/warm", []int64{first.ID, second.ID}, "1111111111", "2222222222")
	if err == nil {
		t.Fatal("reanchor with an invalid second id must fail")
	}
	if !strings.Contains(err.Error(), "9") {
		t.Fatalf("error must name the offending id, got %q", err.Error())
	}

	// The whole operation is all-or-nothing: NEITHER record advanced.
	if got := nodeRevisionAt(t, st, "/repo/warm", "valid first node.go"); got != "1111111111" {
		t.Fatalf("first node must stay anchored at 1111111111 after the failed transaction, got %s", got)
	}
	if got := nodeRevisionAt(t, st, "/repo/warm", "stale second node.go"); got != "9999999999" {
		t.Fatalf("second node must stay anchored at 9999999999, got %s", got)
	}
}

func TestReanchorByIDsValidationBeforeUpdate(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	a := upsertReanchorNode(t, st, "/repo/warm", "node a", "1111111111", "run-a")
	b := upsertReanchorNode(t, st, "/repo/warm", "node b", "1111111111", "run-a")
	stale := upsertReanchorNode(t, st, "/repo/warm", "stale node", "9999999999", "run-a")
	inactive := upsertReanchorNode(t, st, "/repo/warm", "inactive node", "1111111111", "run-a")
	other := upsertReanchorNode(t, st, "/repo/other", "other project node", "1111111111", "run-a")
	if err := st.SetStatus(ctx, inactive.ID, NodeStatusArchived); err != nil {
		t.Fatalf("archive node: %v", err)
	}

	cases := []struct {
		name    string
		ids     []int64
		project string
		wantIn  string
	}{
		{"duplicate ids", []int64{a.ID, a.ID}, "/repo/warm", "duplicate"},
		{"unknown id", []int64{a.ID, 424242}, "/repo/warm", "unknown id"},
		{"project mismatch", []int64{a.ID, other.ID}, "/repo/warm", "project mismatch"},
		{"inactive status", []int64{a.ID, inactive.ID}, "/repo/warm", "not active"},
		{"revision mismatch", []int64{a.ID, stale.ID}, "/repo/warm", "not anchored"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Node a is valid in every case; the named-input error must
			// come from the OTHER id, before ANY update runs.
			_, err := st.ReanchorByIDs(ctx, tc.project, tc.ids, "1111111111", "2222222222")
			if err == nil {
				t.Fatalf("%s must be rejected", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("%s error %q must contain %q", tc.name, err.Error(), tc.wantIn)
			}
		})
	}

	// After every rejected call, both still-valid nodes stay anchored at
	// the source revision: no partial advance ever happened.
	for _, anchor := range []string{"node a.go", "node b.go"} {
		if got := nodeRevisionAt(t, st, "/repo/warm", anchor); got != "1111111111" {
			t.Fatalf("%s advanced to %s after rejected reanchor calls", anchor, got)
		}
	}
	if got := nodeRevisionAt(t, st, "/repo/other", "other project node.go"); got != "1111111111" {
		t.Fatalf("other project node advanced to %s after rejected reanchor calls", got)
	}

	// The valid set still reanchors once every invalid input is gone.
	n, err := st.ReanchorByIDs(ctx, "/repo/warm", []int64{a.ID, b.ID}, "1111111111", "2222222222")
	if err != nil {
		t.Fatalf("valid reanchor: %v", err)
	}
	if n != 2 {
		t.Fatalf("advanced %d nodes, want 2", n)
	}
}

func TestCaptureSetIDsBySourceRun(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	// Two producer runs verified the SAME tree (project+revision) and
	// each persisted its own nodes.
	runA := upsertReanchorNode(t, st, "/repo/warm", "run a node", "1111111111", "run-a")
	runB := upsertReanchorNode(t, st, "/repo/warm", "run b node", "1111111111", "run-b")

	// With the producer-run filter, each run gets only its own set.
	idsA, err := st.CaptureSetIDs(ctx, "/repo/warm", "1111111111", "run-a")
	if err != nil {
		t.Fatalf("capture set run-a: %v", err)
	}
	if len(idsA) != 1 || idsA[0] != runA.ID {
		t.Fatalf("run-a capture set = %v, want [%d]", idsA, runA.ID)
	}
	idsB, err := st.CaptureSetIDs(ctx, "/repo/warm", "1111111111", "run-b")
	if err != nil {
		t.Fatalf("capture set run-b: %v", err)
	}
	if len(idsB) != 1 || idsB[0] != runB.ID {
		t.Fatalf("run-b capture set = %v, want [%d]", idsB, runB.ID)
	}

	// Unknown producer run: an empty set, never another run's nodes.
	idsNone, err := st.CaptureSetIDs(ctx, "/repo/warm", "1111111111", "run-zzz")
	if err != nil {
		t.Fatalf("capture set unknown run: %v", err)
	}
	if len(idsNone) != 0 {
		t.Fatalf("unknown run capture set = %v, want empty", idsNone)
	}

	// Omitting the filter preserves the old conflated behavior: both
	// runs' nodes come back as one set.
	idsAll, err := st.CaptureSetIDs(ctx, "/repo/warm", "1111111111", "")
	if err != nil {
		t.Fatalf("capture set unfiltered: %v", err)
	}
	if len(idsAll) != 2 {
		t.Fatalf("unfiltered capture set = %v, want both ids %d and %d", idsAll, runA.ID, runB.ID)
	}
	seen := map[int64]bool{runA.ID: false, runB.ID: false}
	for _, id := range idsAll {
		if _, ok := seen[id]; !ok {
			t.Fatalf("unexpected id %d in unfiltered set %v", id, idsAll)
		}
		seen[id] = true
	}
	for id, found := range seen {
		if !found {
			t.Fatalf("expected id %d missing from unfiltered set %v", id, idsAll)
		}
	}
}
