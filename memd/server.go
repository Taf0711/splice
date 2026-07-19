package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/Taf0711/splice/memd/store"
)

var errAlreadyRunning = errors.New("splice-memd already running")

// server holds the HTTP server, the store, and the Unix socket path.
type server struct {
	store      *store.Store
	socketPath string
	httpServer *http.Server
}

func newServer(store *store.Store, socketPath string) *server {
	s := &server{
		store:      store,
		socketPath: socketPath,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/upsert", s.handleUpsert)
	mux.HandleFunc("/search", s.handleSearch)
	mux.HandleFunc("/mark_reviewed", s.handleMarkReviewed)
	mux.HandleFunc("/stats", s.handleStats)
	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
	return s
}

func (s *server) listenAndServe(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("mkdir socket dir: %w", err)
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		if !errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("listen unix: %w", err)
		}
		conn, dialErr := net.DialTimeout("unix", s.socketPath, time.Second)
		if dialErr == nil {
			conn.Close() //nolint:errcheck
			return errAlreadyRunning
		}
		if removeErr := os.Remove(s.socketPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove stale socket: %w", removeErr)
		}
		listener, err = net.Listen("unix", s.socketPath)
		if err != nil {
			return fmt.Errorf("listen unix after stale socket cleanup: %w", err)
		}
	}
	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		listener.Close()        //nolint:errcheck
		os.Remove(s.socketPath) //nolint:errcheck
		return fmt.Errorf("chmod socket: %w", err)
	}

	// Graceful shutdown on SIGTERM/SIGINT.
	go func() {
		<-ctx.Done()
		s.httpServer.Shutdown(context.Background()) //nolint:errcheck
	}()

	err = s.httpServer.Serve(listener)
	// http.ErrServerClosed is expected on graceful shutdown.
	if err == http.ErrServerClosed {
		err = nil
	}
	// Clean up socket on exit.
	os.Remove(s.socketPath) //nolint:errcheck
	return err
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": version,
	})
}

func (s *server) handleUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req upsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}

	obs := &store.Observation{
		Scope:      req.Scope,
		OwnerAgent: req.OwnerAgent,
		Visibility: req.Visibility,
		MemoryType: req.MemoryType,
		Title:      req.Title,
		Content:    req.Content,
		Pinned:     req.Pinned,
	}
	if req.ProjectPath != nil {
		obs.ProjectPath = sql.NullString{String: *req.ProjectPath, Valid: true}
	}
	if req.TopicKey != nil {
		obs.TopicKey = sql.NullString{String: *req.TopicKey, Valid: true}
	}
	if req.SourceRunID != nil {
		obs.SourceRunID = sql.NullString{String: *req.SourceRunID, Valid: true}
	}
	if req.SourceStage != nil {
		obs.SourceStage = sql.NullString{String: *req.SourceStage, Valid: true}
	}
	if req.SourceBranch != nil {
		obs.SourceBranch = sql.NullString{String: *req.SourceBranch, Valid: true}
	}
	if req.SourceCommit != nil {
		obs.SourceCommit = sql.NullString{String: *req.SourceCommit, Valid: true}
	}
	if req.Confidence != nil {
		obs.Confidence = sql.NullFloat64{Float64: *req.Confidence, Valid: true}
	}

	result, err := s.store.UpsertObservation(r.Context(), obs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"observation": toProtocol(result),
	})
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}

	if req.Limit <= 0 {
		req.Limit = 8
	}
	includePrivate := true
	if req.IncludePrivate != nil {
		includePrivate = *req.IncludePrivate
	}
	includeShareable := true
	if req.IncludeShareable != nil {
		includeShareable = *req.IncludeShareable
	}

	q := &store.Query{
		ProjectPath:      req.ProjectPath,
		RequestingAgent:  req.RequestingAgent,
		QueryText:        req.Query,
		Scopes:           req.Scopes,
		IncludePrivate:   includePrivate,
		IncludeShareable: includeShareable,
		MemoryTypes:      req.MemoryTypes,
		Limit:            req.Limit,
	}
	results, truncated, err := s.store.Search(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Non-nil so splice hits marshal as [] rather than null (the Python client
	// iterates the field directly).
	protocol := make([]protocolObservation, 0, len(results))
	for _, obs := range results {
		protocol = append(protocol, toProtocol(obs))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"observations": protocol,
		"truncated":    truncated,
	})
}

func (s *server) handleMarkReviewed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req markReviewedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	if err := s.store.MarkReviewed(r.Context(), req.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, genericResponse{OK: true})
}

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rows, err := s.store.DB().QueryContext(r.Context(),
		`SELECT memory_type, COUNT(*) FROM observations WHERE deleted_at IS NULL GROUP BY memory_type`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	byType := make(map[string]int)
	total := 0
	for rows.Next() {
		var mt string
		var count int
		if err := rows.Scan(&mt, &count); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		byType[mt] = count
		total += count
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var pageCount, pageSize int64
	if err := s.store.DB().QueryRowContext(r.Context(), "PRAGMA page_count").Scan(&pageCount); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.DB().QueryRowContext(r.Context(), "PRAGMA page_size").Scan(&pageSize); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, statsResponse{
		OK:          true,
		Total:       total,
		ByType:      byType,
		DBSizeBytes: pageCount * pageSize,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, genericResponse{OK: false, Error: msg})
}

// dataDirFor returns the mutable Splice data directory for a platform.
func dataDirFor(goos string, home string, xdg string) string {
	if home == "" {
		return filepath.Join(os.TempDir(), "splice")
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "splice")
	}
	if xdg != "" {
		return filepath.Join(xdg, "splice")
	}
	return filepath.Join(home, ".local", "share", "splice")
}

// defaultSocketPath returns the Unix socket path.
func defaultSocketPath() string {
	if env := os.Getenv("SPLICE_MEMD_SOCKET"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(dataDirFor(runtime.GOOS, home, os.Getenv("XDG_DATA_HOME")), "mem.sock")
}

// defaultDBPath returns the SQLite database path.
func defaultDBPath() string {
	if env := os.Getenv("SPLICE_MEMD_DB"); env != "" {
		return env
	}
	sock := defaultSocketPath()
	return filepath.Join(filepath.Dir(sock), "memory.db")
}

// runServer is the entrypoint for `splice-memd --serve`.
func runServer() {
	socketPath := defaultSocketPath()
	dbPath := defaultDBPath()

	log.Printf("splice-memd %s starting on %s (db: %s)", version, socketPath, dbPath)

	if err := ensurePrivateDir(filepath.Dir(dbPath)); err != nil {
		log.Fatalf("private data dir: %v", err)
	}

	restore := setPrivateUmask()
	defer restore()

	st, err := store.New(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := tightenDBPermissions(dbPath); err != nil {
		log.Printf("warning: tighten db permissions: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	srv := newServer(st, socketPath)

	if err := srv.listenAndServe(ctx); err != nil {
		if errors.Is(err, errAlreadyRunning) {
			log.Printf("another instance is serving %s; exiting", socketPath)
			return
		}
		log.Fatalf("server: %v", err)
	}
}
