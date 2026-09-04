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

// ResetCounts reports what one ResetProject removed. Zero counts are
// meaningful: they say the project had nothing to remove.
type ResetCounts struct {
	Observations int64 `json:"observations"`
	Traces       int64 `json:"traces"`
}

// ResetProject hard-deletes every observation and trace stored for the exact
// project path. Eval-harness isolation primitive: it returns one project's
// memory state to empty so a fresh seed is the only cognition that project
// carries. Zero counts are valid and reported, never hidden.
func (c *Client) ResetProject(ctx context.Context, projectPath string) (ResetCounts, error) {
	req := map[string]string{"project_path": projectPath}
	var resp struct {
		OK           bool   `json:"ok"`
		Observations int64  `json:"observations"`
		Traces       int64  `json:"traces"`
		Error        string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/project/reset", req, &resp); err != nil {
		return ResetCounts{}, err
	}
	if !resp.OK {
		return ResetCounts{}, fmt.Errorf("memd project/reset: %s", resp.Error)
	}
	return ResetCounts{Observations: resp.Observations, Traces: resp.Traces}, nil
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

// ---------------------------------------------------------------------------
// Cognition graph methods. Each mirrors one /graph/* endpoint on the sidecar.
// The wire contract is the only coupling: these structs are client-local and
// never import the memd sidecar module.
// ---------------------------------------------------------------------------

// GraphAnchor is one typed anchor on a graph node.
type GraphAnchor struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// GraphEdgeInput is one directed edge from the upserted node.
type GraphEdgeInput struct {
	DstID int64  `json:"dst_id"`
	Kind  string `json:"kind"`
}

// GraphNode is the client-side view of one cognition node.
type GraphNode struct {
	ID               int64         `json:"id"`
	Kind             string        `json:"kind"`
	Claim            string        `json:"claim"`
	Scope            string        `json:"scope"`
	ProjectPath      *string       `json:"project_path"`
	Status           string        `json:"status"`
	Confidence       *float64      `json:"confidence"`
	SourceRunID      *string       `json:"source_run_id"`
	CreatedRevision  *string       `json:"created_revision"`
	VerifiedRevision *string       `json:"verified_revision"`
	CreatedAt        int64         `json:"created_at"`
	VerifiedAt       *int64        `json:"verified_at"`
	ClaimHash        string        `json:"claim_hash"`
	MetadataJSON     *string       `json:"metadata_json"`
	Anchors          []GraphAnchor `json:"anchors,omitempty"`
}

// GraphUpsertInput is the caller-supplied shape for one graph upsert.
type GraphUpsertInput struct {
	Kind             string           `json:"kind"`
	Claim            string           `json:"claim"`
	Scope            string           `json:"scope,omitempty"`
	ProjectPath      string           `json:"project_path,omitempty"`
	Status           string           `json:"status,omitempty"`
	Confidence       *float64         `json:"confidence,omitempty"`
	SourceRunID      string           `json:"source_run_id,omitempty"`
	CreatedRevision  string           `json:"created_revision,omitempty"`
	VerifiedRevision string           `json:"verified_revision,omitempty"`
	Metadata         map[string]any   `json:"metadata,omitempty"`
	Anchors          []GraphAnchor    `json:"anchors,omitempty"`
	Edges            []GraphEdgeInput `json:"edges,omitempty"`
	Evidence         []GraphEvidence  `json:"evidence,omitempty"`
}

// GraphEvidence is one deterministic support record attached to the node at
// upsert time.
type GraphEvidence struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// UpsertGraphNode creates or updates one node with anchors and edges, then
// returns the stored node with its canonical ID.
func (c *Client) UpsertGraphNode(ctx context.Context, in GraphUpsertInput) (GraphNode, error) {
	if in.Kind == "" {
		return GraphNode{}, fmt.Errorf("memd graph upsert: kind is required")
	}
	if in.Claim == "" {
		return GraphNode{}, fmt.Errorf("memd graph upsert: claim is required")
	}
	var resp struct {
		OK    bool      `json:"ok"`
		Node  GraphNode `json:"node"`
		Error string    `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/graph/upsert", in, &resp); err != nil {
		return GraphNode{}, err
	}
	if !resp.OK {
		return GraphNode{}, fmt.Errorf("memd graph upsert: %s", resp.Error)
	}
	return resp.Node, nil
}

// GetExactNodes returns active nodes carrying all requested anchors. The map
// key is the anchor kind (file, symbol, package, test, revision); the values
// are the anchor values to match.
func (c *Client) GetExactNodes(ctx context.Context, anchors map[string][]string, projectPath string, limit int) ([]GraphNode, error) {
	if len(anchors) == 0 {
		return nil, fmt.Errorf("memd graph exact: at least one anchor is required")
	}
	req := map[string]any{
		"anchors":      anchors,
		"project_path": projectPath,
		"limit":        limitDefault(limit),
	}
	var resp struct {
		OK    bool        `json:"ok"`
		Nodes []GraphNode `json:"nodes"`
		Error string      `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/graph/exact", req, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("memd graph exact: %s", resp.Error)
	}
	return resp.Nodes, nil
}

// limitDefault normalizes a non-positive limit to the sidecar default (32).
func limitDefault(limit int) int {
	if limit <= 0 {
		return 32
	}
	return limit
}

// GraphNeighborEdge is one edge returned by the neighbors walk.
type GraphNeighborEdge struct {
	SrcID int64  `json:"src_id"`
	DstID int64  `json:"dst_id"`
	Kind  string `json:"kind"`
}

// GetNeighbors walks a bounded BFS from one node, following only the given
// edge kinds (empty means all kinds), and returns the active nodes and edges
// it reached.
func (c *Client) GetNeighbors(ctx context.Context, nodeID int64, kinds []string, depth, limit int) ([]GraphNode, []GraphNeighborEdge, error) {
	if nodeID < 1 {
		return nil, nil, fmt.Errorf("memd graph neighbors: node_id must be >= 1, got %d", nodeID)
	}
	body := map[string]any{"node_id": nodeID, "kinds": kinds, "depth": depth, "limit": limitDefault(limit)}
	var resp struct {
		OK    bool                `json:"ok"`
		Nodes []GraphNode         `json:"nodes"`
		Edges []GraphNeighborEdge `json:"edges"`
		Error string              `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/graph/neighbors", body, &resp); err != nil {
		return nil, nil, err
	}
	if !resp.OK {
		return nil, nil, fmt.Errorf("memd graph neighbors: %s", resp.Error)
	}
	return resp.Nodes, resp.Edges, nil
}

// SetGraphNodeStatus moves one node to a new status.
func (c *Client) SetGraphNodeStatus(ctx context.Context, nodeID int64, status string) error {
	if nodeID < 1 {
		return fmt.Errorf("memd graph status: node_id must be >= 1, got %d", nodeID)
	}
	if status == "" {
		return fmt.Errorf("memd graph status: status is required")
	}
	req := map[string]any{"node_id": nodeID, "status": status}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/graph/status", req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("memd graph status: %s", resp.Error)
	}
	return nil
}

// ContradictGraphNode marks nodeID contradicted by byNodeID with one evidence
// row. The sidecar adds the contradicts edge (by -> node) and the evidence.
func (c *Client) ContradictGraphNode(ctx context.Context, nodeID, byNodeID int64, evidenceKind, ref, detail string) error {
	if nodeID < 1 {
		return fmt.Errorf("memd graph contradict: node_id must be >= 1, got %d", nodeID)
	}
	if byNodeID < 1 {
		return fmt.Errorf("memd graph contradict: by_node_id must be >= 1, got %d", byNodeID)
	}
	if evidenceKind == "" {
		return fmt.Errorf("memd graph contradict: evidence kind is required")
	}
	req := map[string]any{
		"node_id":    nodeID,
		"by_node_id": byNodeID,
		"kind":       evidenceKind,
		"ref":        ref,
		"detail":     detail,
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/graph/contradict", req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("memd graph contradict: %s", resp.Error)
	}
	return nil
}

// GraphSearchHit pairs a node ID with its semantic similarity score.
type GraphSearchHit struct {
	NodeID int64   `json:"node_id"`
	Score  float64 `json:"score"`
}

// SearchGraphSemantically ranks active nodes by cosine similarity to text and
// returns the top k hits.
func (c *Client) SearchGraphSemantically(ctx context.Context, text string, k int) ([]GraphSearchHit, error) {
	if text == "" {
		return nil, fmt.Errorf("memd graph search_semantic: text is required")
	}
	var resp struct {
		OK    bool             `json:"ok"`
		Hits  []GraphSearchHit `json:"hits"`
		Error string           `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/graph/search_semantic", map[string]any{"text": text, "k": k}, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("memd graph search_semantic: %s", resp.Error)
	}
	return resp.Hits, nil
}

// GraphCompactionReport mirrors the sidecar's compaction summary.
type GraphCompactionReport struct {
	DuplicateGroups  int   `json:"duplicate_groups"`
	DuplicatesMerged int   `json:"duplicates_merged"`
	EdgesRetargeted  int   `json:"edges_retargeted"`
	AnchorsMerged    int   `json:"anchors_merged"`
	EvidenceMerged   int   `json:"evidence_merged"`
	DurationMs       int64 `json:"duration_ms"`
}

// CompactGraph merges duplicate nodes and returns the report.
func (c *Client) CompactGraph(ctx context.Context) (GraphCompactionReport, error) {
	var resp struct {
		OK     bool                  `json:"ok"`
		Report GraphCompactionReport `json:"report"`
		Error  string                `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/graph/compact", map[string]any{}, &resp); err != nil {
		return GraphCompactionReport{}, err
	}
	if !resp.OK {
		return GraphCompactionReport{}, fmt.Errorf("memd graph compact: %s", resp.Error)
	}
	return resp.Report, nil
}

// CollectGraph hard-deletes stale unreferenced ephemeral nodes older than
// olderThanSeconds and returns the count removed.
func (c *Client) CollectGraph(ctx context.Context, olderThanSeconds int64) (int64, error) {
	var resp struct {
		OK        bool   `json:"ok"`
		Collected int64  `json:"collected"`
		Error     string `json:"error,omitempty"`
	}
	if err := c.do(ctx, http.MethodPost, "/graph/collect", map[string]any{"older_than": olderThanSeconds}, &resp); err != nil {
		return 0, err
	}
	if !resp.OK {
		return 0, fmt.Errorf("memd graph collect: %s", resp.Error)
	}
	return resp.Collected, nil
}
