package memd

// Tests for the cognition graph client methods. Each test asserts the wire
// body the client sends and the response it decodes, using a stub handler.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestUpsertGraphNode(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/graph/upsert" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["kind"] != "fact" || req["claim"] != "claim text" {
				t.Errorf("unexpected body %v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"node": map[string]any{
					"id": 7, "kind": "fact", "claim": "claim text",
					"scope": "project", "status": "active", "created_at": 1,
					"claim_hash": "abc",
				},
			})
		}))
		node, err := c.UpsertGraphNode(context.Background(), GraphUpsertInput{
			Kind:  "fact",
			Claim: "claim text",
			Anchors: []GraphAnchor{
				{Kind: "file", Value: "a.go"},
			},
		})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if node.ID != 7 {
			t.Errorf("expected node id 7, got %d", node.ID)
		}
		if node.Status != "active" {
			t.Errorf("expected active, got %q", node.Status)
		}
	})

	t.Run("validation", func(t *testing.T) {
		c := newTestServer(t, http.NotFoundHandler())
		if _, err := c.UpsertGraphNode(context.Background(), GraphUpsertInput{Kind: "fact"}); err == nil {
			t.Error("expected missing claim to fail")
		}
		if _, err := c.UpsertGraphNode(context.Background(), GraphUpsertInput{Claim: "x"}); err == nil {
			t.Error("expected missing kind to fail")
		}
	})

	t.Run("server error", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "validation: bad"})
		}))
		if _, err := c.UpsertGraphNode(context.Background(), GraphUpsertInput{Kind: "fact", Claim: "x"}); err == nil || !stringsContains(err.Error(), "bad") {
			t.Fatalf("expected validation error, got %v", err)
		}
	})
}

func TestGetExactNodes(t *testing.T) {
	c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		anchors, _ := req["anchors"].(map[string]any)
		if _, ok := anchors["file"]; !ok {
			t.Errorf("expected file anchor in request, got %v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"nodes": []map[string]any{{"id": 3, "kind": "fact", "claim": "c", "scope": "project", "status": "active", "created_at": 1, "claim_hash": "h"}},
		})
	}))
	nodes, err := c.GetExactNodes(context.Background(), map[string][]string{"file": {"a.go"}}, "/repo", 0)
	if err != nil {
		t.Fatalf("exact: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != 3 {
		t.Fatalf("expected 1 node id 3, got %+v", nodes)
	}
	if _, err := c.GetExactNodes(context.Background(), nil, "", 0); err == nil {
		t.Error("expected empty anchors to fail")
	}
}

func TestGetNeighborsClient(t *testing.T) {
	c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"nodes": []map[string]any{{"id": 2, "kind": "fact", "claim": "b", "scope": "project", "status": "active", "created_at": 1, "claim_hash": "h"}},
			"edges": []map[string]any{{"src_id": 2, "dst_id": 1, "kind": "related_to"}},
		})
	}))
	nodes, edges, err := c.GetNeighbors(context.Background(), 1, []string{"related_to"}, 2, 10)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != 2 {
		t.Fatalf("expected node 2, got %+v", nodes)
	}
	if len(edges) != 1 || edges[0].Kind != "related_to" {
		t.Fatalf("expected one related_to edge, got %+v", edges)
	}
	if _, _, err := c.GetNeighbors(context.Background(), 0, nil, 1, 1); err == nil {
		t.Error("expected node_id 0 to fail")
	}
}

func TestSetGraphNodeStatusClient(t *testing.T) {
	c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	if err := c.SetGraphNodeStatus(context.Background(), 1, "stale"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := c.SetGraphNodeStatus(context.Background(), 0, "stale"); err == nil {
		t.Error("expected node_id 0 to fail")
	}
	if err := c.SetGraphNodeStatus(context.Background(), 1, ""); err == nil {
		t.Error("expected empty status to fail")
	}
}

func TestContradictGraphNodeClient(t *testing.T) {
	c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	if err := c.ContradictGraphNode(context.Background(), 1, 2, "falsified_approach", "ref", "detail"); err != nil {
		t.Fatalf("contradict: %v", err)
	}
	if err := c.ContradictGraphNode(context.Background(), 1, 2, "", "", ""); err == nil {
		t.Error("expected empty evidence kind to fail")
	}
}

func TestSearchGraphSemanticallyClient(t *testing.T) {
	c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"hits": []map[string]any{{"node_id": 5, "score": 0.75}},
		})
	}))
	hits, err := c.SearchGraphSemantically(context.Background(), "session storage", 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].NodeID != 5 || hits[0].Score != 0.75 {
		t.Fatalf("unexpected hits %+v", hits)
	}
	if _, err := c.SearchGraphSemantically(context.Background(), "", 3); err == nil {
		t.Error("expected empty text to fail")
	}
}

func TestCompactAndCollectGraphClient(t *testing.T) {
	c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graph/compact":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"report": map[string]any{"duplicate_groups": 1, "duplicates_merged": 2, "edges_retargeted": 1, "duration_ms": 3},
			})
		case "/graph/collect":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "collected": 4})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	report, err := c.CompactGraph(context.Background())
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if report.DuplicatesMerged != 2 || report.DuplicateGroups != 1 {
		t.Fatalf("unexpected report %+v", report)
	}
	collected, err := c.CollectGraph(context.Background(), 60)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if collected != 4 {
		t.Fatalf("expected 4 collected, got %d", collected)
	}
}

func stringsContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
