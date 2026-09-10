package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Tests in this file pin the MVP causal-proof storage contracts:
//
//  1. graph nodes persist across a full store close/reopen cycle (persistence
//     means a LATER INDEPENDENT process can retrieve, not that a method was
//     called);
//  2. ResetProject clears a project's graph rows (per-attempt eval isolation)
//     and cascades to anchors, edges, evidence, and embeddings;
//  3. GetByIDs + AnchorsFor back the semantic-hit enrichment on the wire.

func TestGraphPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "persist.db")
	ctx := context.Background()

	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	node, err := st.UpsertNode(ctx, NodeInput{
		Kind:             NodeKindFact,
		Claim:            "internal/session/store.go defines Store.InvalidateUserSessions; verified at revision abc123",
		ProjectPath:      "/repo",
		SourceRunID:      "run-42",
		VerifiedRevision: "abc123",
		Anchors: []AnchorInput{
			{Kind: "file", Value: "internal/session/store.go"},
			{Kind: "symbol", Value: "internal/session/store.go#Store.InvalidateUserSessions"},
		},
		Evidence: []EvidenceInput{{Kind: "git", Ref: "abc123", Detail: "verified run changed this file"}},
	})
	if err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	if node.ID < 1 {
		t.Fatalf("node id = %d, want >= 1", node.ID)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Reopen: a brand-new Store over the same file is the persistence claim.
	reopened, err := New(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close() //nolint:errcheck

	got, err := reopened.GetExact(ctx, map[string][]string{
		"file": {"internal/session/store.go"},
	}, GetExactOptions{ProjectPath: "/repo"})
	if err != nil {
		t.Fatalf("exact retrieval after reopen: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("exact retrieval after reopen returned %d nodes, want 1", len(got))
	}
	if got[0].ID != node.ID || got[0].Kind != NodeKindFact || got[0].SourceRunID.String != "run-42" {
		t.Errorf("retrieved node mismatch: id=%d kind=%s run=%s", got[0].ID, got[0].Kind, got[0].SourceRunID.String)
	}
}

func TestResetProjectClearsGraphRows(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	for _, project := range []string{"/repo/warm", "/repo/other"} {
		if _, err := st.UpsertNode(ctx, NodeInput{
			Kind:        NodeKindFact,
			Claim:       "fact for " + project,
			ProjectPath: project,
			Anchors:     []AnchorInput{{Kind: "file", Value: "a.go"}},
		}); err != nil {
			t.Fatalf("upsert node for %s: %v", project, err)
		}
	}

	counts, err := st.ResetProject(ctx, "/repo/warm")
	if err != nil {
		t.Fatalf("reset project: %v", err)
	}
	if counts.GraphNodes != 1 {
		t.Fatalf("ResetProject graph_nodes = %d, want 1", counts.GraphNodes)
	}

	remaining, err := st.GetExact(ctx, map[string][]string{"file": {"a.go"}}, GetExactOptions{})
	if err != nil {
		t.Fatalf("exact after reset: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ProjectPath.String != "/repo/other" {
		t.Fatalf("expected only /repo/other's node to survive, got %d node(s)", len(remaining))
	}
}

func TestGetByIDsAndAnchorsFor(t *testing.T) {
	st := newGraphTestStore(t)
	ctx := context.Background()

	kept, err := st.UpsertNode(ctx, NodeInput{
		Kind:        NodeKindProcedure,
		Claim:       "verification passes with: go test ./...",
		ProjectPath: "/repo",
		Anchors:     []AnchorInput{{Kind: "test", Value: "go test ./..."}},
	})
	if err != nil {
		t.Fatalf("upsert kept: %v", err)
	}
	dropped, err := st.UpsertNode(ctx, NodeInput{
		Kind:        NodeKindFact,
		Claim:       "soon superseded",
		ProjectPath: "/repo",
		Anchors:     []AnchorInput{{Kind: "file", Value: "b.go"}},
	})
	if err != nil {
		t.Fatalf("upsert dropped: %v", err)
	}
	if err := st.SetStatus(ctx, dropped.ID, NodeStatusSuperseded); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	nodes, err := st.GetByIDs(ctx, []int64{kept.ID, dropped.ID, 9999}, "/repo")
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != kept.ID {
		t.Fatalf("GetByIDs returned %d node(s), want only the active one (id %d)", len(nodes), kept.ID)
	}

	anchors, err := st.AnchorsFor(ctx, []int64{kept.ID})
	if err != nil {
		t.Fatalf("AnchorsFor: %v", err)
	}
	got := anchors[kept.ID]
	if len(got) != 1 || got[0].Kind != "test" || got[0].Value != "go test ./..." {
		t.Fatalf("anchors for kept node = %+v, want one test anchor", got)
	}
}
