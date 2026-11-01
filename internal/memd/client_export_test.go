package memd

// A4 wire pairing test: ExportCaptureSet and ImportCaptureSet speak the
// same wire shapes as the sidecar's /graph/export_capture_set and
// /graph/import_capture_set. The client never imports the sidecar module;
// the JSON contract is the coupling, so this test pins it from the client
// side with a stand-in server that decodes with the same shapes the
// sidecar's protocol declares.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExportImportCaptureSetWirePairing(t *testing.T) {
	// The stand-in decodes the request and encodes the response exactly
	// as the sidecar handlers do (same field names, same nesting).
	// The client dials a Unix socket; macOS caps sun_path at ~104 bytes,
	// so the socket lives directly in the system temp dir with a short
	// name, not inside the long per-test directory.
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("memd-wt-%d.sock", time.Now().UnixNano()))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(sockPath) })
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graph/export_capture_set":
			var req struct {
				ProjectPath string  `json:"project_path"`
				Revision    string  `json:"revision"`
				SourceRunID *string `json:"source_run_id,omitempty"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("export request undecodable: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if req.ProjectPath == "" || req.Revision == "" {
				t.Error("export request missing project_path/revision")
			}
			_, _ = w.Write([]byte(`{"ok":true,"nodes":[{"node":{"id":7,"kind":"fact","claim":"a.go defines Retention","source_run_id":"run-a","verified_revision":"rev1","claim_hash":"h1"},"anchors":[{"kind":"file","value":"a.go"}],"evidence":[{"kind":"git","ref":"rev1","detail":"verified run changed this file"}],"source_id":7,"claim_hash":"h1"}]}`))
		case "/graph/import_capture_set":
			var req struct {
				ProjectPath string                `json:"project_path"`
				Nodes       []ExportedCaptureNode `json:"nodes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("import request undecodable: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if req.ProjectPath == "" {
				t.Error("import request missing project_path")
			}
			if len(req.Nodes) != 1 || req.Nodes[0].Node.Claim != "a.go defines Retention" {
				t.Errorf("import nodes payload drifted: %+v", req.Nodes)
			}
			_, _ = w.Write([]byte(`{"ok":true,"imported":1}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	server.Listener = ln
	server.Start()
	defer server.Close()

	client := NewClient(sockPath)

	nodes, err := client.ExportCaptureSet(context.Background(), "/proj", "rev1", "run-a")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("export returned %d nodes, want 1", len(nodes))
	}
	if nodes[0].Node.SourceRunID == nil || *nodes[0].Node.SourceRunID != "run-a" {
		t.Fatal("producer run id lost in export decode")
	}
	if len(nodes[0].Anchors) != 1 || nodes[0].Anchors[0].Kind != "file" {
		t.Fatal("anchors lost in export decode")
	}
	if len(nodes[0].Evidence) != 1 || nodes[0].Evidence[0].Detail != "verified run changed this file" {
		t.Fatal("evidence lost in export decode")
	}

	imported, err := client.ImportCaptureSet(context.Background(), "/target", nodes)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}
}
