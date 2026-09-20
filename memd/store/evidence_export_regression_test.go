package store

import (
	"context"
	"database/sql"
	"testing"
)

// TestEvidenceForRoundTripPersistsRows is the export-path regression pin:
// EvidenceFor must read the rows and columns that AddEvidence writes, and
// the deterministic tie order (created_at, rowid) must preserve insertion
// order when timestamps are equal.
func TestEvidenceForRoundTripPersistsRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	node, err := s.UpsertNode(ctx, NodeInput{
		Kind:        NodeKindFact,
		Claim:       "internal/session/store.go defines Store.InvalidateUserSessions; verified at revision abc123",
		ProjectPath: "/repo",
		SourceRunID: "run-evidence-export",
	})
	if err != nil {
		t.Fatalf("upsert node: %v", err)
	}

	// Force an equal created_at so the test reads the tie order, not the
	// clock. Insert directly because AddEvidence uses the current time.
	for i, ref := range []string{"abc123", "def456"} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO cognition_evidence(node_id, kind, ref, detail, created_at) VALUES(?,?,?,?,?)`,
			node.ID, "git", ref, "detail-"+ref, 42); err != nil {
			t.Fatalf("insert evidence %d: %v", i, err)
		}
	}

	got, err := s.EvidenceFor(ctx, []int64{node.ID})
	if err != nil {
		t.Fatalf("EvidenceFor: %v", err)
	}
	rows := got[node.ID]
	if len(rows) != 2 {
		t.Fatalf("EvidenceFor returned %d rows, want 2", len(rows))
	}
	if !rows[0].Ref.Valid || !rows[1].Ref.Valid {
		t.Fatalf("evidence refs not populated: %+v", rows)
	}
	if rows[0].Ref.String != "abc123" || rows[1].Ref.String != "def456" {
		t.Fatalf("equal-timestamp order = %q, %q; want insertion order", rows[0].Ref.String, rows[1].Ref.String)
	}
	if !rows[0].Detail.Valid || rows[0].Detail.String != "detail-abc123" {
		t.Fatalf("evidence detail not round-tripped: %+v", rows[0])
	}
}

// TestEvidenceForNoRowsKeepsNodeAbsent pins the empty-map behavior that the
// export endpoint relies on when a node has no evidence.
func TestEvidenceForNoRowsKeepsNodeAbsent(t *testing.T) {
	s := newTestStore(t)
	node, err := s.UpsertNode(context.Background(), NodeInput{Kind: NodeKindFact, Claim: "no evidence"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.EvidenceFor(context.Background(), []int64{node.ID})
	if err != nil {
		t.Fatalf("EvidenceFor: %v", err)
	}
	if _, ok := got[node.ID]; ok {
		t.Fatalf("node %d unexpectedly present: %+v", node.ID, got)
	}
}

var _ = sql.NullString{}
