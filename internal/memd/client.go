// Package memd is the HTTP client for the splice-memd memory sidecar.
//
// The sidecar (built from the memd/ module in this repo) serves six JSON
// endpoints over a Unix domain socket. This package speaks that protocol and
// never imports the sidecar module; the wire contract is the only coupling.
// The structured-memory architecture is documented alongside the sidecar.
package memd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

const (
	requestTimeout = 2 * time.Second
	spawnDeadline  = 3 * time.Second
)

// ErrRecentUnsupported indicates that the running daemon predates the recent
// listing route. Callers can present an update instruction instead of a raw
// HTTP error.
var ErrRecentUnsupported = errors.New("memory daemon does not support recent listings")

type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", e.status, e.body)
}

// Client talks to a splice-memd daemon over its Unix socket.
type Client struct {
	socketPath string
	httpClient *http.Client
}

// NewClient returns a client bound to socketPath. It performs no I/O.
func NewClient(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{
		socketPath: socketPath,
		httpClient: &http.Client{Transport: transport, Timeout: requestTimeout},
	}
}

// SocketPath returns the Unix socket path this client dials.
func (c *Client) SocketPath() string {
	return c.socketPath
}

// Health checks that the daemon is up and answering.
func (c *Client) Health(ctx context.Context) error {
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodGet, "/health", nil, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("memd health: %s", resp.Error)
	}
	return nil
}

// upsertRequest mirrors the sidecar's POST /upsert body: the caller-supplied
// subset of observation fields, without server-owned fields like id,
// normalized_hash, or timestamps.
type upsertRequest struct {
	ProjectPath  *string  `json:"project_path,omitempty"`
	Scope        string   `json:"scope"`
	OwnerAgent   string   `json:"owner_agent"`
	Visibility   string   `json:"visibility"`
	MemoryType   string   `json:"memory_type"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	TopicKey     *string  `json:"topic_key,omitempty"`
	SourceRunID  *string  `json:"source_run_id,omitempty"`
	SourceStage  *string  `json:"source_stage,omitempty"`
	SourceBranch *string  `json:"source_branch,omitempty"`
	SourceCommit *string  `json:"source_commit,omitempty"`
	Pinned       bool     `json:"pinned"`
	Confidence   *float64 `json:"confidence,omitempty"`
}

// Upsert persists one observation and returns the stored row.
func (c *Client) Upsert(ctx context.Context, obs schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	if err := obs.Validate(); err != nil {
		return schemas.MemoryObservation{}, fmt.Errorf("memd upsert: %w", err)
	}
	req := upsertRequest{
		ProjectPath:  obs.ProjectPath,
		Scope:        obs.Scope,
		OwnerAgent:   obs.OwnerAgent,
		Visibility:   obs.Visibility,
		MemoryType:   obs.MemoryType,
		Title:        obs.Title,
		Content:      obs.Content,
		TopicKey:     obs.TopicKey,
		SourceRunID:  obs.SourceRunID,
		SourceStage:  obs.SourceStage,
		SourceBranch: obs.SourceBranch,
		SourceCommit: obs.SourceCommit,
		Pinned:       obs.Pinned,
		Confidence:   obs.Confidence,
	}
	var resp struct {
		OK          bool                      `json:"ok"`
		Observation schemas.MemoryObservation `json:"observation"`
		Error       string                    `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/upsert", req, &resp); err != nil {
		return schemas.MemoryObservation{}, err
	}
	if !resp.OK {
		return schemas.MemoryObservation{}, fmt.Errorf("memd upsert: %s", resp.Error)
	}
	return resp.Observation, nil
}

// Search runs a bounded FTS query and returns the matching observations as a
// bundle attributed to the requesting agent.
func (c *Client) Search(ctx context.Context, query schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	if err := query.Validate(); err != nil {
		return schemas.MemoryBundle{}, fmt.Errorf("memd search: %w", err)
	}
	var resp struct {
		OK           bool                        `json:"ok"`
		Observations []schemas.MemoryObservation `json:"observations"`
		Truncated    bool                        `json:"truncated"`
		Error        string                      `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/search", query, &resp); err != nil {
		return schemas.MemoryBundle{}, err
	}
	if !resp.OK {
		return schemas.MemoryBundle{}, fmt.Errorf("memd search: %s", resp.Error)
	}
	return schemas.MemoryBundle{
		RequestingAgent: query.RequestingAgent,
		Observations:    resp.Observations,
		Truncated:       resp.Truncated,
	}, nil
}

// LookupTopic runs a deterministic exact topic-key lookup and returns the
// matched observations as a bundle. RequestingAgent is set by the caller
// (the consuming stage), never by the sidecar: the store only filters by
// visibility. The bundle carries no exemplars.
func (c *Client) LookupTopic(ctx context.Context, query schemas.MemoryTopicQuery) (schemas.MemoryBundle, error) {
	if err := query.Validate(); err != nil {
		return schemas.MemoryBundle{}, fmt.Errorf("memd lookup_topic: %w", err)
	}
	var resp struct {
		OK           bool                        `json:"ok"`
		Observations []schemas.MemoryObservation `json:"observations"`
		Truncated    bool                        `json:"truncated"`
		Error        string                      `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/lookup_topic", query, &resp); err != nil {
		return schemas.MemoryBundle{}, err
	}
	if !resp.OK {
		return schemas.MemoryBundle{}, fmt.Errorf("memd lookup_topic: %s", resp.Error)
	}
	return schemas.MemoryBundle{
		RequestingAgent: query.RequestingAgent,
		Observations:    resp.Observations,
		Truncated:       resp.Truncated,
	}, nil
}

// SearchRanked runs the same bounded FTS query as Search against a sidecar
// that exposes /search_ranked, and returns candidates with their BM25 ranks.
// The rank lets the orchestrator rerank deterministically (report section 28)
// without re-deriving BM25. An old sidecar without the endpoint surfaces as
// an error; callers treat that as an ordinary retrieval failure and fall
// back to Search.
func (c *Client) SearchRanked(ctx context.Context, query schemas.MemoryQuery) ([]schemas.MemoryRanked, bool, error) {
	if err := query.Validate(); err != nil {
		return nil, false, fmt.Errorf("memd search_ranked: %w", err)
	}
	var resp struct {
		OK           bool `json:"ok"`
		Observations []struct {
			Observation schemas.MemoryObservation `json:"observation"`
			Rank        float64                   `json:"rank"`
		} `json:"observations"`
		Truncated bool   `json:"truncated"`
		Error     string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/search_ranked", query, &resp); err != nil {
		return nil, false, err
	}
	if !resp.OK {
		return nil, false, fmt.Errorf("memd search_ranked: %s", resp.Error)
	}
	ranked := make([]schemas.MemoryRanked, 0, len(resp.Observations))
	for _, ro := range resp.Observations {
		ranked = append(ranked, schemas.MemoryRanked{Observation: ro.Observation, Rank: ro.Rank})
	}
	return ranked, resp.Truncated, nil
}

// Recent lists recent observations without using the full-text index.
func (c *Client) Recent(ctx context.Context, query schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	if err := query.ValidateRecent(); err != nil {
		return schemas.MemoryBundle{}, fmt.Errorf("memd recent: %w", err)
	}
	var resp struct {
		OK           bool                        `json:"ok"`
		Observations []schemas.MemoryObservation `json:"observations"`
		Truncated    bool                        `json:"truncated"`
		Error        string                      `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/recent", query, &resp); err != nil {
		var statusErr *statusError
		if errors.As(err, &statusErr) && (statusErr.status == http.StatusNotFound || statusErr.status == http.StatusMethodNotAllowed || statusErr.status == http.StatusNotImplemented) {
			return schemas.MemoryBundle{}, fmt.Errorf("%w: update splice-memd", ErrRecentUnsupported)
		}
		return schemas.MemoryBundle{}, err
	}
	if !resp.OK {
		return schemas.MemoryBundle{}, fmt.Errorf("memd recent: %s", resp.Error)
	}
	return schemas.MemoryBundle{
		RequestingAgent: query.RequestingAgent,
		Observations:    resp.Observations,
		Truncated:       resp.Truncated,
	}, nil
}

// MarkReviewed marks one observation reviewed by ID.
func (c *Client) MarkReviewed(ctx context.Context, id int64) error {
	req := map[string]int64{"id": id}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/mark_reviewed", req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("memd mark_reviewed: %s", resp.Error)
	}
	return nil
}

// MemoryStats is the client-side view of the sidecar's GET /stats response.
// It carries the counts the TUI status line and /memory command render.
type MemoryStats struct {
	Total       int            `json:"total"`
	ByType      map[string]int `json:"by_type"`
	DBSizeBytes int64          `json:"db_size_bytes"`
}

// Stats fetches aggregate memory statistics from the sidecar.
func (c *Client) Stats(ctx context.Context) (MemoryStats, error) {
	var resp struct {
		OK          bool           `json:"ok"`
		Total       int            `json:"total"`
		ByType      map[string]int `json:"by_type"`
		DBSizeBytes int64          `json:"db_size_bytes"`
		Error       string         `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodGet, "/stats", nil, &resp); err != nil {
		return MemoryStats{}, err
	}
	if !resp.OK {
		return MemoryStats{}, fmt.Errorf("memd stats: %s", resp.Error)
	}
	return MemoryStats{
		Total:       resp.Total,
		ByType:      resp.ByType,
		DBSizeBytes: resp.DBSizeBytes,
	}, nil
}

// UpsertTrace stores a run outcome trace. run_traces is write-once: a duplicate
// run_id is an idempotent no-op on the sidecar, never an update.
func (c *Client) UpsertTrace(ctx context.Context, trace schemas.RunOutcome) error {
	if err := trace.Validate(); err != nil {
		return fmt.Errorf("memd trace upsert: %w", err)
	}
	var resp struct {
		OK       bool   `json:"ok"`
		Inserted bool   `json:"inserted"`
		Error    string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/trace/upsert", trace, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("memd trace upsert: %s", resp.Error)
	}
	return nil
}

// UpsertVerdict appends a verdict for a run. Verdicts are append-only; the
// latest row wins at query time.
func (c *Client) UpsertVerdict(ctx context.Context, verdict schemas.VerdictRecord) error {
	if err := verdict.Validate(); err != nil {
		return fmt.Errorf("memd verdict upsert: %w", err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/trace/verdict", verdict, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("memd verdict upsert: %s", resp.Error)
	}
	return nil
}

// QueryTraces returns traces matching the filter, each joined with its latest
// verdict, ordered newest first.
func (c *Client) QueryTraces(ctx context.Context, filter schemas.TraceQueryFilter) ([]schemas.TraceQueryResult, error) {
	var resp struct {
		OK     bool `json:"ok"`
		Traces []struct {
			RunID     string          `json:"run_id"`
			SessionID string          `json:"session_id"`
			RepoRoot  string          `json:"repo_root"`
			Tier      string          `json:"tier"`
			Status    string          `json:"status"`
			CreatedAt int64           `json:"created_at"`
			Rank      float64         `json:"rank"`
			Payload   json.RawMessage `json:"payload"`
			Verdict   *struct {
				DecidedAt int64           `json:"decided_at"`
				Verdict   string          `json:"verdict"`
				Reason    string          `json:"reason"`
				Payload   json.RawMessage `json:"payload"`
			} `json:"verdict"`
		} `json:"traces"`
		Error string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/trace/query", map[string]any{
		"repo_root": filter.RepoRoot,
		"tier":      filter.Tier,
		"status":    filter.Status,
		"verdict":   filter.Verdict,
		"query":     filter.Query,
		"since":     filter.Since,
		"limit":     filter.Limit,
	}, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("memd trace query: %s", resp.Error)
	}
	out := make([]schemas.TraceQueryResult, 0, len(resp.Traces))
	for _, tr := range resp.Traces {
		var trace schemas.RunOutcome
		if err := json.Unmarshal(tr.Payload, &trace); err != nil {
			return nil, fmt.Errorf("memd trace query: decode trace %s: %w", tr.RunID, err)
		}
		result := schemas.TraceQueryResult{Trace: trace, Rank: tr.Rank}
		if tr.Verdict != nil {
			var verdict schemas.VerdictRecord
			if err := json.Unmarshal(tr.Verdict.Payload, &verdict); err != nil {
				return nil, fmt.Errorf("memd trace query: decode verdict %s: %w", tr.RunID, err)
			}
			result.Verdict = &verdict
		}
		out = append(out, result)
	}
	return out, nil
}

func (c *Client) do(ctx context.Context, method string, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("memd %s: encode: %w", path, err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, reader)
	if err != nil {
		return fmt.Errorf("memd %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("memd %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("memd %s: %w", path, &statusError{status: resp.StatusCode, body: string(bytes.TrimSpace(bodyBytes))})
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("memd %s: decode (status %d): %w", path, resp.StatusCode, err)
	}
	return nil
}

// DefaultSocketPath mirrors the sidecar's own socket resolution: the
// SPLICE_MEMD_SOCKET env var, else mem.sock in the platform data directory.
func DefaultSocketPath() string {
	if env := os.Getenv("SPLICE_MEMD_SOCKET"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(dataDirFor(runtime.GOOS, home, os.Getenv("XDG_DATA_HOME")), "mem.sock")
}

// dataDirFor mirrors memd/server.go so client and daemon agree on defaults.
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

// resolveBinary returns the splice-memd binary to spawn, or "" when none
// resolves. Resolution order:
//  1. SPLICE_MEMD_BIN env var (trusted explicit user intent, returned as-is)
//  2. splice-memd on PATH
//  3. Sibling binary next to the running executable (covers `go install` and
//     `make build` layouts where splice-memd sits beside splice). The
//     executable's own directory is trusted (it's where the main binary
//     lives), not the working directory — a repo cannot plant a binary there
//     unless it can write to the install directory.
//  4. disabled (empty string)
//
// There is no current-working-directory fallback; opening an arbitrary
// project directory must not auto-execute a repository-provided binary.
func resolveBinary(getenv func(string) string, lookPath func(string) (string, error)) string {
	if env := getenv("SPLICE_MEMD_BIN"); env != "" {
		return env
	}
	if path, err := lookPath("splice-memd"); err == nil {
		return path
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "splice-memd")
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return sibling
		}
	}
	return ""
}

// Resolve returns a healthy client for the default socket, auto-spawning the
// daemon when needed. It returns (nil, nil) when no binary resolves (memory
// is simply off) and (nil, err) when a daemon was expected but could not be
// reached, so the caller can degrade with a single warning.
func Resolve(ctx context.Context) (*Client, error) {
	socketPath := DefaultSocketPath()
	client := NewClient(socketPath)
	if err := client.Health(ctx); err == nil {
		return client, nil
	}

	binary := resolveBinary(os.Getenv, exec.LookPath)
	if binary == "" {
		return nil, nil
	}
	if err := spawnDaemon(binary, socketPath); err != nil {
		return nil, fmt.Errorf("spawn splice-memd: %w", err)
	}

	deadline := time.Now().Add(spawnDeadline)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Health(ctx); err == nil {
			return client, nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("splice-memd did not become healthy: %w", lastErr)
}

// spawnDaemon starts `splice-memd --serve` detached from this process group.
// Concurrent spawns are benign: the daemon exits cleanly when another
// instance already owns the socket.
func spawnDaemon(binary string, socketPath string) error {
	cmd := exec.Command(binary, "--serve")
	cmd.Env = append(os.Environ(), "SPLICE_MEMD_SOCKET="+socketPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	configureSpawn(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the child in the background so a fast exit (for example the
	// already-running case) does not leave a zombie.
	go cmd.Wait() //nolint:errcheck
	return nil
}
