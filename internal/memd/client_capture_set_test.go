package memd

// Test that the capture-set client methods round-trip the optional
// source_run_id filter over the Unix-socket protocol (finding F9).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestCaptureSetIDsForRunRoundTrip(t *testing.T) {
	t.Run("with filter", func(t *testing.T) {
		var gotSourceRunID any
		var gotRevision, gotProjectPath string
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/graph/capture_set" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode body: %v", err)
			}
			gotProjectPath, _ = req["project_path"].(string)
			gotRevision, _ = req["revision"].(string)
			gotSourceRunID = req["source_run_id"]
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":  true,
				"ids": []int64{3, 7},
			})
		}))
		ids, err := c.CaptureSetIDsForRun(context.Background(), "/repo/warm", "1111111111", "run-a")
		if err != nil {
			t.Fatalf("capture set for run: %v", err)
		}
		if len(ids) != 2 || ids[0] != 3 || ids[1] != 7 {
			t.Fatalf("ids = %v, want [3 7]", ids)
		}
		if gotProjectPath != "/repo/warm" || gotRevision != "1111111111" {
			t.Fatalf("request body = project %q revision %q", gotProjectPath, gotRevision)
		}
		if gotSourceRunID != "run-a" {
			t.Fatalf("source_run_id wire value = %v, want run-a", gotSourceRunID)
		}
	})

	t.Run("two-arg form omits the filter", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if _, present := req["source_run_id"]; present {
				t.Errorf("two-arg form must not send source_run_id, got body %v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":  true,
				"ids": []int64{},
			})
		}))
		ids, err := c.CaptureSetIDs(context.Background(), "/repo/warm", "1111111111")
		if err != nil {
			t.Fatalf("capture set: %v", err)
		}
		if len(ids) != 0 {
			t.Fatalf("ids = %v, want empty", ids)
		}
	})

	t.Run("validation", func(t *testing.T) {
		c := newTestServer(t, http.NotFoundHandler())
		if _, err := c.CaptureSetIDsForRun(context.Background(), "", "1111111111", "run-a"); err == nil {
			t.Error("expected empty project to fail")
		}
		if _, err := c.CaptureSetIDsForRun(context.Background(), "/repo/warm", "", "run-a"); err == nil {
			t.Error("expected empty revision to fail")
		}
	})

	t.Run("server error", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "validation: bad"})
		}))
		if _, err := c.CaptureSetIDsForRun(context.Background(), "/repo/warm", "1111111111", "run-a"); err == nil || !stringsContains(err.Error(), "bad") {
			t.Fatalf("expected server error, got %v", err)
		}
	})
}
