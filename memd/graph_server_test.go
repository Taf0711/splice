package main

// Cognition graph endpoint tests (/graph/*). These exercise the full HTTP
// handler stack against a temp-database store, mirroring the main_test.go
// conventions.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func graphPost(t *testing.T, srv *server, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response %s: %v", path, err)
	}
	return w, resp
}

func TestGraphUpsertAndExactRoundTrip(t *testing.T) {
	srv := newTestServer(t)

	// Upsert a node with anchors and an edge.
	upsertBody := map[string]any{
		"kind":          "decision",
		"claim":         "session storage uses NewStore",
		"scope":         "project",
		"project_path":  "/repo",
		"source_run_id": "run-1",
		"anchors": []map[string]string{
			{"kind": "file", "value": "internal/sessions/store.go"},
			{"kind": "symbol", "value": "NewStore"},
		},
	}
	w, resp := graphPost(t, srv, "/graph/upsert", upsertBody)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp["ok"] != true {
		t.Fatalf("upsert: expected ok=true, got %v", resp)
	}
	node := resp["node"].(map[string]any)
	nodeID := int64(node["id"].(float64))
	if node["status"] != "active" {
		t.Errorf("expected status active, got %v", node["status"])
	}

	// Re-upsert the same claim: dedupe returns the same id.
	w, resp = graphPost(t, srv, "/graph/upsert", upsertBody)
	if w.Code != http.StatusOK {
		t.Fatalf("re-upsert: expected 200, got %d", w.Code)
	}
	node2 := resp["node"].(map[string]any)
	if int64(node2["id"].(float64)) != nodeID {
		t.Errorf("expected dedupe to return id %d, got %v", nodeID, node2["id"])
	}

	// Exact retrieval by anchor.
	exactBody := map[string]any{
		"anchors":      map[string][]string{"file": {"internal/sessions/store.go"}},
		"project_path": "/repo",
	}
	w, resp = graphPost(t, srv, "/graph/exact", exactBody)
	if w.Code != http.StatusOK {
		t.Fatalf("exact: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	nodes := resp["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 exact hit, got %d", len(nodes))
	}
	if int64(nodes[0].(map[string]any)["id"].(float64)) != nodeID {
		t.Errorf("expected node %d from exact query, got %v", nodeID, nodes[0])
	}

	// Unknown anchor kind fails loud as a validation error.
	exactBad := map[string]any{"anchors": map[string][]string{"sybmol": {"x"}}}
	w, _ = graphPost(t, srv, "/graph/exact", exactBad)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown anchor kind, got %d", w.Code)
	}
}

func TestGraphUpsertValidation(t *testing.T) {
	srv := newTestServer(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing kind", map[string]any{"claim": "x"}},
		{"missing claim", map[string]any{"kind": "fact"}},
		{"bad anchor", map[string]any{"kind": "fact", "claim": "x", "anchors": []map[string]string{{"kind": "file"}}}},
		{"bad edge", map[string]any{"kind": "fact", "claim": "x", "edges": []map[string]any{{"dst_id": 0, "kind": "related_to"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := graphPost(t, srv, "/graph/upsert", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
	// Method check.
	req := httptest.NewRequest(http.MethodGet, "/graph/upsert", nil)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", w.Code)
	}
}

func TestGraphNeighborsAndStatus(t *testing.T) {
	srv := newTestServer(t)

	// Create a -> b.
	w, resp := graphPost(t, srv, "/graph/upsert", map[string]any{"kind": "fact", "claim": "node a"})
	a := int64(resp["node"].(map[string]any)["id"].(float64))
	w, resp = graphPost(t, srv, "/graph/upsert", map[string]any{
		"kind":  "fact",
		"claim": "node b",
		"edges": []map[string]any{{"dst_id": a, "kind": "related_to"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("upsert b: %d: %s", w.Code, w.Body.String())
	}
	b := int64(resp["node"].(map[string]any)["id"].(float64))

	// Neighbors of a: b is reachable (edge a<-b is traversed both ways).
	w, resp = graphPost(t, srv, "/graph/neighbors", map[string]any{"node_id": a, "depth": 1, "limit": 10})
	if w.Code != http.StatusOK {
		t.Fatalf("neighbors: %d: %s", w.Code, w.Body.String())
	}
	nodes := resp["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 neighbor, got %d", len(nodes))
	}
	if int64(nodes[0].(map[string]any)["id"].(float64)) != b {
		t.Errorf("expected neighbor %d, got %v", b, nodes[0])
	}

	// Bad node_id fails validation.
	w, _ = graphPost(t, srv, "/graph/neighbors", map[string]any{"node_id": 0})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for node_id 0, got %d", w.Code)
	}

	// Status transition.
	w, _ = graphPost(t, srv, "/graph/status", map[string]any{"node_id": b, "status": "stale"})
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d: %s", w.Code, w.Body.String())
	}
	// b is no longer active, so it drops out of the neighbors walk.
	w, resp = graphPost(t, srv, "/graph/neighbors", map[string]any{"node_id": a, "depth": 1, "limit": 10})
	nodes = resp["nodes"].([]any)
	if len(nodes) != 0 {
		t.Errorf("expected stale neighbor to be filtered, got %d", len(nodes))
	}
	// Unknown status fails loud.
	w, _ = graphPost(t, srv, "/graph/status", map[string]any{"node_id": b, "status": "zombie"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown status, got %d", w.Code)
	}
	// Missing node 404s.
	w, _ = graphPost(t, srv, "/graph/status", map[string]any{"node_id": 424242, "status": "stale"})
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing node, got %d", w.Code)
	}
}

func TestGraphContradictEndpoint(t *testing.T) {
	srv := newTestServer(t)

	w, resp := graphPost(t, srv, "/graph/upsert", map[string]any{"kind": "fact", "claim": "the fixture API is newSessionStore"})
	fact := int64(resp["node"].(map[string]any)["id"].(float64))
	w, resp = graphPost(t, srv, "/graph/upsert", map[string]any{
		"kind":    "failure",
		"claim":   "newSessionStore does not exist; the fixture API is NewStore + newSessionMux",
		"anchors": []map[string]string{{"kind": "test", "value": "TestFixtureAPI"}},
	})
	failure := int64(resp["node"].(map[string]any)["id"].(float64))

	w, _ = graphPost(t, srv, "/graph/contradict", map[string]any{
		"node_id":    fact,
		"by_node_id": failure,
		"kind":       "falsified_approach",
		"detail":     "compile error: undefined: newSessionStore",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("contradict: %d: %s", w.Code, w.Body.String())
	}

	// The contradicted fact no longer appears in exact retrieval.
	w, resp = graphPost(t, srv, "/graph/exact", map[string]any{
		"anchors": map[string][]string{"test": {"TestFixtureAPI"}},
	})
	// The failure node itself still matches.
	nodes := resp["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("expected only the failure node, got %d", len(nodes))
	}

	// Validation errors.
	w, _ = graphPost(t, srv, "/graph/contradict", map[string]any{"node_id": fact, "by_node_id": 0, "kind": "x"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing by_node_id, got %d", w.Code)
	}
	w, _ = graphPost(t, srv, "/graph/contradict", map[string]any{"node_id": 424242, "by_node_id": failure, "kind": "x"})
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing node, got %d", w.Code)
	}
}

func TestGraphSearchSemanticEndpoint(t *testing.T) {
	srv := newTestServer(t)

	w, resp := graphPost(t, srv, "/graph/upsert", map[string]any{"kind": "decision", "claim": "session storage opens one SQLite store per project"})
	if w.Code != http.StatusOK {
		t.Fatalf("upsert: %d: %s", w.Code, w.Body.String())
	}
	sessionID := int64(resp["node"].(map[string]any)["id"].(float64))
	w, resp = graphPost(t, srv, "/graph/upsert", map[string]any{"kind": "fact", "claim": "release builds are signed with the release key"})
	if w.Code != http.StatusOK {
		t.Fatalf("upsert 2: %d: %s", w.Code, w.Body.String())
	}

	w, resp = graphPost(t, srv, "/graph/search_semantic", map[string]any{"text": "how does session storage work", "k": 1})
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d: %s", w.Code, w.Body.String())
	}
	hits := resp["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	hit := hits[0].(map[string]any)
	if int64(hit["node_id"].(float64)) != sessionID {
		t.Errorf("expected session node %d, got %v", sessionID, hit["node_id"])
	}

	// k=0 defaults to a sane value server-side; empty text fails loud.
	w, _ = graphPost(t, srv, "/graph/search_semantic", map[string]any{"text": "", "k": 3})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty text, got %d", w.Code)
	}

	// Empty index (fresh store) returns empty, not error.
	srv2 := newTestServer(t)
	w, resp = graphPost(t, srv2, "/graph/search_semantic", map[string]any{"text": "anything", "k": 5})
	if w.Code != http.StatusOK {
		t.Fatalf("empty index search: %d: %s", w.Code, w.Body.String())
	}
	if len(resp["hits"].([]any)) != 0 {
		t.Error("expected no hits on empty index")
	}
}

func TestGraphCompactAndCollectEndpoints(t *testing.T) {
	srv := newTestServer(t)

	// Compact on an empty graph is a zero report, not an error.
	w, resp := graphPost(t, srv, "/graph/compact", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("compact: %d: %s", w.Code, w.Body.String())
	}
	report := resp["report"].(map[string]any)
	if report["duplicates_merged"].(float64) != 0 {
		t.Errorf("expected 0 merged, got %v", report["duplicates_merged"])
	}

	// Collect with default age collects nothing on an empty graph.
	w, resp = graphPost(t, srv, "/graph/collect", map[string]any{"older_than": 3600})
	if w.Code != http.StatusOK {
		t.Fatalf("collect: %d: %s", w.Code, w.Body.String())
	}
	if resp["collected"].(float64) != 0 {
		t.Errorf("expected 0 collected, got %v", resp["collected"])
	}

	// Negative older_than fails validation.
	w, _ = graphPost(t, srv, "/graph/collect", map[string]any{"older_than": -1})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative older_than, got %d", w.Code)
	}

	// Method check on every graph route.
	for _, path := range []string{
		"/graph/upsert", "/graph/exact", "/graph/neighbors", "/graph/status",
		"/graph/contradict", "/graph/search_semantic", "/graph/compact", "/graph/collect",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405 for GET, got %d", path, w.Code)
		}
	}
}
