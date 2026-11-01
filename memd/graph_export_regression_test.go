package main

import (
	"net/http"
	"testing"
)

// TestGraphExportCaptureSetCarriesEvidenceThroughServer pins the export
// endpoint contract that the natural-capture path depends on: a node stored
// with evidence is exported with all of its evidence records and fields.
func TestGraphExportCaptureSetCarriesEvidenceThroughServer(t *testing.T) {
	srv := newTestServer(t)

	body := map[string]any{
		"kind":              "fact",
		"claim":             "internal/audit/retention.go defines Apply",
		"scope":             "project",
		"project_path":      "/repo",
		"source_run_id":     "run-A",
		"verified_revision": "rev-A",
		"anchors": []map[string]string{
			{"kind": "file", "value": "internal/audit/retention.go"},
			{"kind": "symbol", "value": "internal/audit/retention.go#Apply"},
		},
		"evidence": []map[string]string{
			{"kind": "git", "ref": "rev-A", "detail": "verified Task A"},
			{"kind": "test", "ref": "go test ./internal/audit/...", "detail": "passed"},
		},
	}
	w, _ := graphPost(t, srv, "/graph/upsert", body)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w, resp := graphPost(t, srv, "/graph/export_capture_set", map[string]any{
		"project_path":  "/repo",
		"revision":      "rev-A",
		"source_run_id": "run-A",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("export: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	nodes, ok := resp["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("export nodes = %#v, want one node", resp["nodes"])
	}
	node := nodes[0].(map[string]any)
	ev, ok := node["evidence"].([]any)
	if !ok || len(ev) != 2 {
		t.Fatalf("export evidence = %#v, want two records", node["evidence"])
	}
	first := ev[0].(map[string]any)
	second := ev[1].(map[string]any)
	if first["kind"] != "git" || first["ref"] != "rev-A" || first["detail"] != "verified Task A" {
		t.Fatalf("first evidence = %#v", first)
	}
	if second["kind"] != "test" || second["ref"] != "go test ./internal/audit/..." {
		t.Fatalf("second evidence = %#v", second)
	}
	anchors, ok := node["anchors"].([]any)
	if !ok || len(anchors) != 2 {
		t.Fatalf("export anchors = %#v, want two anchors", node["anchors"])
	}
}
