package memd

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func ptr[T any](v T) *T { return &v }

// newTestServer binds an httptest server to a Unix socket (the transport the
// client dials) and returns a client wired to it.
func newTestServer(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	f, err := os.CreateTemp("", "memd-*.sock")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	sock := f.Name()
	f.Close()
	os.Remove(sock)
	t.Cleanup(func() { os.Remove(sock) })
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return NewClient(sock)
}

func validObservation() schemas.MemoryObservation {
	return schemas.MemoryObservation{
		OwnerAgent: "agent-1",
		Title:      "title",
		Content:    "content",
		MemoryType: "decision",
		Scope:      "project",
		Visibility: "private",
	}
}

func TestHealth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		if err := c.Health(context.Background()); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "boom"})
		}))
		err := c.Health(context.Background())
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected error containing boom, got %v", err)
		}
	})

	t.Run("no listener", func(t *testing.T) {
		c := NewClient(filepath.Join(t.TempDir(), "nope.sock"))
		if err := c.Health(context.Background()); err == nil {
			t.Fatal("expected non-nil error when no listener")
		}
	})
}

func TestUpsert(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		var gotBody map[string]json.RawMessage
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/upsert" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode request: %v", err)
			}
			echo := validObservation()
			echo.ID = 42
			echo.CreatedAt = 100
			echo.UpdatedAt = 200
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "observation": echo})
		}))
		got, err := c.Upsert(context.Background(), validObservation())
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if got.ID != 42 || got.CreatedAt != 100 || got.UpdatedAt != 200 {
			t.Fatalf("unexpected server-filled fields: %+v", got)
		}
		if got.OwnerAgent != "agent-1" || got.Title != "title" || got.Content != "content" {
			t.Fatalf("echoed observation mismatch: %+v", got)
		}
		// The client's request struct omits server-owned fields.
		if _, ok := gotBody["id"]; ok {
			t.Errorf("request unexpectedly contained id")
		}
		if _, ok := gotBody["normalized_hash"]; ok {
			t.Errorf("request unexpectedly contained normalized_hash")
		}
	})

	t.Run("invalid not sent", func(t *testing.T) {
		hit := false
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		bad := validObservation()
		bad.OwnerAgent = ""
		if _, err := c.Upsert(context.Background(), bad); err == nil {
			t.Fatal("expected validation error")
		}
		if hit {
			t.Fatal("handler should not have been hit for invalid observation")
		}
	})

	t.Run("server error", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "dup"})
		}))
		_, err := c.Upsert(context.Background(), validObservation())
		if err == nil || !strings.Contains(err.Error(), "dup") {
			t.Fatalf("expected error containing dup, got %v", err)
		}
	})
}

func TestSearch(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/search" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			obs := validObservation()
			obs.ID = 7
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":           true,
				"observations": []schemas.MemoryObservation{obs},
				"truncated":    true,
			})
		}))
		bundle, err := c.Search(context.Background(), schemas.MemoryQuery{RequestingAgent: "a", Query: "q", Limit: 5})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if bundle.RequestingAgent != "a" {
			t.Fatalf("requesting agent = %q, want a", bundle.RequestingAgent)
		}
		if len(bundle.Observations) != 1 {
			t.Fatalf("observations len = %d, want 1", len(bundle.Observations))
		}
		if !bundle.Truncated {
			t.Fatal("expected truncated=true")
		}
	})

	t.Run("invalid not sent", func(t *testing.T) {
		hit := false
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		if _, err := c.Search(context.Background(), schemas.MemoryQuery{RequestingAgent: "a", Query: "", Limit: 5}); err == nil {
			t.Fatal("expected validation error")
		}
		if hit {
			t.Fatal("handler should not have been hit for invalid query")
		}
	})

	// Regression for CHANGE 1: nil pointers must transmit as absent (server
	// defaults to true); an explicit false must transmit as a non-nil pointer.
	t.Run("include flags nil vs explicit false", func(t *testing.T) {
		type seen struct {
			IncludePrivate   *bool `json:"include_private"`
			IncludeShareable *bool `json:"include_shareable"`
		}
		var got seen
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = seen{}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "observations": []schemas.MemoryObservation{}, "truncated": false})
		}))

		if _, err := c.Search(context.Background(), schemas.MemoryQuery{RequestingAgent: "a", Query: "q", Limit: 5}); err != nil {
			t.Fatalf("search: %v", err)
		}
		if got.IncludePrivate != nil {
			t.Fatalf("expected include_private nil (absent), got %v", *got.IncludePrivate)
		}
		if got.IncludeShareable != nil {
			t.Fatalf("expected include_shareable nil (absent), got %v", *got.IncludeShareable)
		}

		if _, err := c.Search(context.Background(), schemas.MemoryQuery{RequestingAgent: "a", Query: "q", Limit: 5, IncludePrivate: ptr(false)}); err != nil {
			t.Fatalf("search: %v", err)
		}
		if got.IncludePrivate == nil {
			t.Fatal("expected include_private non-nil pointer")
		}
		if *got.IncludePrivate {
			t.Fatalf("expected include_private=false, got true")
		}
	})
}

func TestMarkReviewed(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/mark_reviewed" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		if err := c.MarkReviewed(context.Background(), 1); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "not found"})
		}))
		err := c.MarkReviewed(context.Background(), 1)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected error containing 'not found', got %v", err)
		}
	})
}

func TestConfigureSpawn(t *testing.T) {
	cmd := exec.Command("echo", "ok")
	configureSpawn(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("configureSpawn left SysProcAttr nil")
	}
}

func TestResolveBinary(t *testing.T) {
	t.Run("env override", func(t *testing.T) {
		getenv := func(k string) string {
			if k == "SPLICE_MEMD_BIN" {
				return "/custom/splice-memd"
			}
			return ""
		}
		lookPath := func(string) (string, error) { return "", os.ErrNotExist }
		if got := resolveBinary(getenv, lookPath); got != "/custom/splice-memd" {
			t.Fatalf("got %q, want /custom/splice-memd", got)
		}
	})

	t.Run("path lookup", func(t *testing.T) {
		getenv := func(string) string { return "" }
		lookPath := func(string) (string, error) { return "/usr/bin/splice-memd", nil }
		if got := resolveBinary(getenv, lookPath); got != "/usr/bin/splice-memd" {
			t.Fatalf("got %q, want /usr/bin/splice-memd", got)
		}
	})

	t.Run("env precedence over path", func(t *testing.T) {
		getenv := func(k string) string {
			if k == "SPLICE_MEMD_BIN" {
				return "/env/splice-memd"
			}
			return ""
		}
		lookPath := func(string) (string, error) { return "/usr/bin/splice-memd", nil }
		if got := resolveBinary(getenv, lookPath); got != "/env/splice-memd" {
			t.Fatalf("got %q, want /env/splice-memd", got)
		}
	})

	t.Run("sibling binary", func(t *testing.T) {
		getenv := func(string) string { return "" }
		lookPath := func(string) (string, error) { return "", os.ErrNotExist }
		// Create a fake splice-memd next to the test binary.
		exe, err := os.Executable()
		if err != nil {
			t.Skipf("os.Executable: %v", err)
		}
		sibling := filepath.Join(filepath.Dir(exe), "splice-memd")
		if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write sibling: %v", err)
		}
		t.Cleanup(func() { os.Remove(sibling) })
		if got := resolveBinary(getenv, lookPath); got != sibling {
			t.Fatalf("got %q, want %q", got, sibling)
		}
	})

	t.Run("sibling binary not executable", func(t *testing.T) {
		getenv := func(string) string { return "" }
		lookPath := func(string) (string, error) { return "", os.ErrNotExist }
		exe, err := os.Executable()
		if err != nil {
			t.Skipf("os.Executable: %v", err)
		}
		sibling := filepath.Join(filepath.Dir(exe), "splice-memd")
		if err := os.WriteFile(sibling, []byte("not executable"), 0o644); err != nil {
			t.Fatalf("write sibling: %v", err)
		}
		t.Cleanup(func() { os.Remove(sibling) })
		if got := resolveBinary(getenv, lookPath); got != "" {
			t.Fatalf("got %q, want empty (non-executable sibling ignored)", got)
		}
	})

	t.Run("cwd binary ignored", func(t *testing.T) {
		getenv := func(string) string { return "" }
		lookPath := func(string) (string, error) { return "", os.ErrNotExist }
		workDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(workDir, "memd"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		dev := filepath.Join(workDir, "memd", "splice-memd")
		if err := os.WriteFile(dev, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
		// Make the temp directory the current working directory so a
		// cwd-relative fallback would pick up the malicious repo binary.
		t.Chdir(workDir)
		if got := resolveBinary(getenv, lookPath); got != "" {
			t.Fatalf("got %q, want empty (cwd fallback removed)", got)
		}
		if got := resolveBinary(getenv, lookPath); got == dev {
			t.Fatalf("resolution unexpectedly returned malicious repo path %q", got)
		}
	})

	t.Run("no binary", func(t *testing.T) {
		getenv := func(string) string { return "" }
		lookPath := func(string) (string, error) { return "", os.ErrNotExist }
		if got := resolveBinary(getenv, lookPath); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

func TestStats(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/stats" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":            true,
				"total":         42,
				"by_type":       map[string]int{"decision": 10, "test_command": 32},
				"db_size_bytes": int64(4096),
			})
		}))
		stats, err := c.Stats(context.Background())
		if err != nil {
			t.Fatalf("stats: %v", err)
		}
		if stats.Total != 42 {
			t.Fatalf("total = %d, want 42", stats.Total)
		}
		if stats.ByType["decision"] != 10 {
			t.Fatalf("by_type[decision] = %d, want 10", stats.ByType["decision"])
		}
		if stats.DBSizeBytes != 4096 {
			t.Fatalf("db_size = %d, want 4096", stats.DBSizeBytes)
		}
	})

	t.Run("server error", func(t *testing.T) {
		c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "db locked"})
		}))
		_, err := c.Stats(context.Background())
		if err == nil || !strings.Contains(err.Error(), "db locked") {
			t.Fatalf("expected error containing 'db locked', got %v", err)
		}
	})
}

// TestDoNon2xx verifies the client returns a meaningful error when the server
// responds with a non-2xx status code, even without valid JSON.
func TestDoNon2xx(t *testing.T) {
	c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error")) //nolint:errcheck
	}))
	err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected non-nil error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error should mention status code 500, got: %v", err)
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("error should include body text, got: %v", err)
	}
}
