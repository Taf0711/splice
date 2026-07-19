// Package memd is the HTTP client for the splice-memd memory sidecar.
//
// The sidecar (built from the memd/ module in this repo) serves five JSON
// endpoints over a Unix domain socket. This package speaks that protocol and
// never imports the sidecar module; the wire contract is the only coupling.
// See docs/flug-design/10-structured-memory.md for the architecture.
package memd

import (
	"bytes"
	"context"
	"encoding/json"
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
		return fmt.Errorf("memd %s: unexpected status %d: %s", path, resp.StatusCode, string(bytes.TrimSpace(bodyBytes)))
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
