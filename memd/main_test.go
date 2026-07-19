package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Taf0711/splice/memd/store"
)

// newTestServer creates a server backed by a temp database.
func newTestServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sockPath := filepath.Join(dir, "test.sock")
	return newServer(st, sockPath)
}

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp["ok"])
	}
	if resp["version"] != version {
		t.Fatalf("expected version=%q, got %q", version, resp["version"])
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestUpsertAndSearchRoundTrip(t *testing.T) {
	srv := newTestServer(t)

	// Upsert an observation.
	upsertBody := upsertRequest{
		Scope:      "project",
		OwnerAgent: "code_writer",
		Visibility: "private",
		MemoryType: "discovery",
		Title:      "test_command",
		Content:    "pytest tests/",
		TopicKey:   strPtr("test_command"),
	}
	body, _ := json.Marshal(upsertBody)
	req := httptest.NewRequest(http.MethodPost, "/upsert", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleUpsert(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var upsertResp map[string]any
	json.NewDecoder(w.Body).Decode(&upsertResp)
	if upsertResp["ok"] != true {
		t.Fatalf("upsert: expected ok=true, got %v", upsertResp)
	}

	obs := upsertResp["observation"].(map[string]any)
	if obs["memory_type"] != "discovery" {
		t.Fatalf("expected memory_type=discovery, got %v", obs["memory_type"])
	}
	if obs["topic_key"] != "test_command" {
		t.Fatalf("expected topic_key=test_command, got %v", obs["topic_key"])
	}
	if obs["owner_agent"] != "code_writer" {
		t.Fatalf("expected owner_agent=code_writer, got %v", obs["owner_agent"])
	}

	// Search for it.
	searchBody := searchRequest{
		RequestingAgent: "code_writer",
		Query:           "test_command",
		Scopes:          []string{"project"},
		Limit:           10,
	}
	body, _ = json.Marshal(searchBody)
	req = httptest.NewRequest(http.MethodPost, "/search", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handleSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var searchResp map[string]any
	json.NewDecoder(w.Body).Decode(&searchResp)
	if searchResp["ok"] != true {
		t.Fatalf("search: expected ok=true, got %v", searchResp)
	}
	obsList := searchResp["observations"].([]any)
	if len(obsList) == 0 {
		t.Fatal("search: expected at least 1 observation, got 0")
	}
}

func TestUpsertInvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/upsert", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	srv.handleUpsert(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestNullFieldSerialization(t *testing.T) {
	srv := newTestServer(t)

	// Upsert with no optional fields set.
	upsertBody := upsertRequest{
		Scope:      "project",
		OwnerAgent: "test_agent",
		Visibility: "private",
		MemoryType: "config",
		Title:      "test",
		Content:    "test content",
	}
	body, _ := json.Marshal(upsertBody)
	req := httptest.NewRequest(http.MethodPost, "/upsert", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleUpsert(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Check that null fields are serialized as null, not as {"String":"","Valid":false}.
	raw := w.Body.String()
	if bytes.Contains([]byte(raw), []byte(`"Valid"`)) {
		t.Fatalf("response contains Go sql.NullString wire format (Valid field), expected plain null: %s", raw)
	}
	if bytes.Contains([]byte(raw), []byte(`"String"`)) {
		t.Fatalf("response contains Go sql.NullString wire format (String field), expected plain null: %s", raw)
	}
	// project_path should be null.
	var resp map[string]any
	json.NewDecoder(bytes.NewReader([]byte(raw))).Decode(&resp)
	obs := resp["observation"].(map[string]any)
	if obs["project_path"] != nil {
		t.Fatalf("expected project_path=null, got %v", obs["project_path"])
	}
	if obs["confidence"] != nil {
		t.Fatalf("expected confidence=null, got %v", obs["confidence"])
	}
}

func TestStats(t *testing.T) {
	srv := newTestServer(t)

	// Insert two observations of different types.
	for _, mt := range []string{"discovery", "config"} {
		upsertBody := upsertRequest{
			Scope:      "project",
			OwnerAgent: "orchestrator",
			Visibility: "shareable",
			MemoryType: mt,
			Title:      mt + "_title",
			Content:    mt + "_content",
		}
		body, _ := json.Marshal(upsertBody)
		req := httptest.NewRequest(http.MethodPost, "/upsert", bytes.NewReader(body))
		w := httptest.NewRecorder()
		srv.handleUpsert(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("upsert %s: expected 200, got %d", mt, w.Code)
		}
	}

	// Get stats.
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	w := httptest.NewRecorder()
	srv.handleStats(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("stats: expected 200, got %d", w.Code)
	}
	var stats statsResponse
	json.NewDecoder(w.Body).Decode(&stats)
	if stats.Total != 2 {
		t.Fatalf("expected total=2, got %d", stats.Total)
	}
	if stats.ByType["discovery"] != 1 {
		t.Fatalf("expected discovery=1, got %d", stats.ByType["discovery"])
	}
	if stats.ByType["config"] != 1 {
		t.Fatalf("expected config=1, got %d", stats.ByType["config"])
	}
}

func TestMarkReviewed(t *testing.T) {
	srv := newTestServer(t)

	// Insert and get the ID.
	upsertBody := upsertRequest{
		Scope:      "project",
		OwnerAgent: "test",
		Visibility: "private",
		MemoryType: "discovery",
		Title:      "x",
		Content:    "y",
	}
	body, _ := json.Marshal(upsertBody)
	req := httptest.NewRequest(http.MethodPost, "/upsert", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleUpsert(w, req)
	var upsertResp map[string]any
	json.NewDecoder(w.Body).Decode(&upsertResp)
	obs := upsertResp["observation"].(map[string]any)
	id := int64(obs["id"].(float64))

	// Mark reviewed.
	markBody := markReviewedRequest{ID: id}
	body, _ = json.Marshal(markBody)
	req = httptest.NewRequest(http.MethodPost, "/mark_reviewed", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handleMarkReviewed(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mark_reviewed: expected 200, got %d", w.Code)
	}
}

func TestDefaultSocketPath(t *testing.T) {
	path := defaultSocketPath()
	if path == "" {
		t.Fatal("defaultSocketPath returned empty string")
	}
	if filepath.Ext(path) != ".sock" {
		t.Fatalf("expected .sock extension, got %q", path)
	}
}

func shortSocketTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "splice-memd-*")
	if err != nil {
		t.Fatalf("create short temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestListenAndServeReplacesStaleSocketAndSetsPrivateMode(t *testing.T) {
	dir := shortSocketTestDir(t)
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sockPath := filepath.Join(dir, "test.sock")
	if err := os.WriteFile(sockPath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale socket: %v", err)
	}

	srv := newServer(st, sockPath)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.listenAndServe(ctx)
	}()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			return net.DialTimeout("unix", sockPath, time.Second)
		},
	}
	client := &http.Client{Transport: transport}
	t.Cleanup(transport.CloseIdleConnections)

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := client.Get("http://unix/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close() //nolint:errcheck
			break
		}
		if resp != nil {
			resp.Body.Close() //nolint:errcheck
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not become healthy before deadline: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %v, want 0600", got)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("listenAndServe returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listenAndServe did not shut down after cancellation")
	}
}

func TestListenAndServeReportsAlreadyRunning(t *testing.T) {
	dir := shortSocketTestDir(t)
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sockPath := filepath.Join(dir, "test.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen existing socket: %v", err)
	}
	defer listener.Close() //nolint:errcheck

	srv := newServer(st, sockPath)
	err = srv.listenAndServe(context.Background())
	if !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("listenAndServe error = %v, want errAlreadyRunning", err)
	}
}

func TestDataDirForMatchesPythonClient(t *testing.T) {
	tests := []struct {
		name string
		goos string
		home string
		xdg  string
		want string
	}{
		{
			name: "darwin ignores xdg",
			goos: "darwin",
			home: "/Users/tester",
			xdg:  "/xdg",
			want: filepath.Join("/Users/tester", "Library", "Application Support", "splice"),
		},
		{
			name: "linux xdg adds splice subdirectory",
			goos: "linux",
			home: "/home/tester",
			xdg:  "/xdg",
			want: filepath.Join("/xdg", "splice"),
		},
		{
			name: "linux default data dir",
			goos: "linux",
			home: "/home/tester",
			xdg:  "",
			want: filepath.Join("/home/tester", ".local", "share", "splice"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dataDirFor(tt.goos, tt.home, tt.xdg)
			if got != tt.want {
				t.Fatalf("dataDirFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultDBPath(t *testing.T) {
	path := defaultDBPath()
	if path == "" {
		t.Fatal("defaultDBPath returned empty string")
	}
	if filepath.Ext(path) != ".db" {
		t.Fatalf("expected .db extension, got %q", path)
	}
}

func strPtr(s string) *string { return &s }

func TestEnsurePrivateDirCreatesParentOnUnix(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "does", "not", "exist")

	if err := ensurePrivateDir(dir); err != nil {
		t.Fatalf("ensurePrivateDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat created dir: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("created dir mode = %v, want 0700", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirTightensExistingDirOnUnix(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod 0755: %v", err)
	}

	if err := ensurePrivateDir(dir); err != nil {
		t.Fatalf("ensurePrivateDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", info.Mode().Perm())
	}
}

func TestTightenDBPermissionsAfterWriteOnUnix(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatalf("ensurePrivateDir: %v", err)
	}

	restore := setPrivateUmask()
	defer restore()

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertObservation(context.Background(), &store.Observation{
		Scope:      "project",
		OwnerAgent: "orchestrator",
		Visibility: "private",
		MemoryType: "config",
		Title:      "x",
		Content:    "y",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := tightenDBPermissions(dbPath); err != nil {
		t.Fatalf("tightenDBPermissions: %v", err)
	}

	if runtime.GOOS == "windows" {
		return
	}

	type check struct {
		path string
		want os.FileMode
	}
	checks := []check{{path: dbPath, want: 0o600}}
	for _, suffix := range []string{"-wal", "-shm"} {
		p := dbPath + suffix
		if _, err := os.Stat(p); err == nil {
			checks = append(checks, check{path: p, want: 0o600})
		}
	}
	for _, c := range checks {
		info, err := os.Stat(c.path)
		if err != nil {
			t.Fatalf("stat %s: %v", c.path, err)
		}
		if info.Mode().Perm() != c.want {
			t.Fatalf("%s mode = %v, want %v", c.path, info.Mode().Perm(), c.want)
		}
	}
}

func TestStoreReopenAfterClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatalf("ensurePrivateDir: %v", err)
	}

	st1, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	st1.Close()

	st2, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	st2.Close()
}

func TestMain(m *testing.M) {
	// Set a temp socket path so tests don't touch the real one.
	dir, _ := os.MkdirTemp("", "memd-test-*")
	os.Setenv("SPLICE_MEMD_SOCKET", filepath.Join(dir, "test.sock"))
	os.Setenv("SPLICE_MEMD_DB", filepath.Join(dir, "test.db"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// TestSearchEmptyResultIsArray verifies splice hits serialize as
// "observations": [] rather than null (the Python client iterates directly).
func TestSearchEmptyResultIsArray(t *testing.T) {
	srv := newTestServer(t)

	searchBody := searchRequest{
		RequestingAgent: "code_writer",
		Query:           "nothing matches this",
		Scopes:          []string{"project"},
		Limit:           5,
	}
	body, _ := json.Marshal(searchBody)
	req := httptest.NewRequest(http.MethodPost, "/search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"observations":[]`)) {
		t.Fatalf("splice-hit search must serialize observations as [], got: %s", w.Body.String())
	}
}

// TestMarkReviewedNotFound verifies a nonexistent id returns 404.
func TestMarkReviewedNotFound(t *testing.T) {
	srv := newTestServer(t)

	body, _ := json.Marshal(markReviewedRequest{ID: 424242})
	req := httptest.NewRequest(http.MethodPost, "/mark_reviewed", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleMarkReviewed(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing id, got %d: %s", w.Code, w.Body.String())
	}
}

// TestStatsIncludesDBSize verifies /stats reports a positive db_size_bytes.
func TestStatsIncludesDBSize(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	w := httptest.NewRecorder()
	srv.handleStats(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("stats: expected 200, got %d", w.Code)
	}
	var stats statsResponse
	json.NewDecoder(w.Body).Decode(&stats)
	if stats.DBSizeBytes <= 0 {
		t.Fatalf("db_size_bytes = %d, want > 0", stats.DBSizeBytes)
	}
}

// TestUpsertValidationRejection verifies the server returns 400 for a
// request with an empty owner_agent.
func TestUpsertValidationRejection(t *testing.T) {
	srv := newTestServer(t)
	body, _ := json.Marshal(upsertRequest{
		Scope:      "project",
		OwnerAgent: "", // invalid
		Visibility: "private",
		MemoryType: "decision",
		Title:      "title",
		Content:    "content",
	})
	req := httptest.NewRequest(http.MethodPost, "/upsert", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleUpsert(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("owner_agent")) {
		t.Fatalf("response should mention owner_agent, got: %s", w.Body.String())
	}
}

// TestSearchValidationRejection verifies the server returns 400 for a
// request with an empty query.
func TestSearchValidationRejection(t *testing.T) {
	srv := newTestServer(t)
	body, _ := json.Marshal(searchRequest{
		Query:           "",
		RequestingAgent: "agent-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("query")) {
		t.Fatalf("response should mention query, got: %s", w.Body.String())
	}
}

// TestMarkReviewedValidationRejection verifies the server returns 400 for a
// request with id=0.
func TestMarkReviewedValidationRejection(t *testing.T) {
	srv := newTestServer(t)
	body, _ := json.Marshal(markReviewedRequest{ID: 0})
	req := httptest.NewRequest(http.MethodPost, "/mark_reviewed", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleMarkReviewed(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpsertOversizedBody verifies the server returns a 4xx status for a
// body exceeding the 1 MiB limit.
func TestUpsertOversizedBody(t *testing.T) {
	srv := newTestServer(t)
	// Build a body just over 1 MiB.
	big := make([]byte, 1<<20+1024)
	// Fill with valid-looking JSON structure.
	copy(big, []byte(`{"scope":"project","owner_agent":"x","visibility":"private","memory_type":"m","title":"`))
	for i := 0; i < len(big); i++ {
		if big[i] == 0 {
			big[i] = 'x'
		}
	}
	// Tacking on closing braces won't help because it's over limit, but that's fine.
	req := httptest.NewRequest(http.MethodPost, "/upsert", bytes.NewReader(big))
	w := httptest.NewRecorder()
	srv.handleUpsert(w, req)
	// MaxBytesReader can cause http.ErrBodyReadAfterClose or similar;
	// accept any 4xx status.
	if w.Code < 400 || w.Code >= 500 {
		t.Fatalf("expected 4xx for oversized body, got %d: %s", w.Code, w.Body.String())
	}
}
