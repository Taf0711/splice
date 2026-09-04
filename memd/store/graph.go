package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cognition graph kinds, edge kinds, and node statuses. All wire values are
// lowercase snake_case and stable: they cross the sidecar HTTP boundary and
// must never change spelling.
const (
	NodeKindFact       = "fact"
	NodeKindConclusion = "conclusion"
	NodeKindDecision   = "decision"
	NodeKindProcedure  = "procedure"
	NodeKindFailure    = "failure"
	NodeKindEvidence   = "evidence"

	EdgeKindImplementedBy = "implemented_by"
	EdgeKindDefinedIn     = "defined_in"
	EdgeKindTestedBy      = "tested_by"
	EdgeKindSupportedBy   = "supported_by"
	EdgeKindDependsOn     = "depends_on"
	EdgeKindContradicts   = "contradicts"
	EdgeKindSupersedes    = "supersedes"
	EdgeKindDerivedFrom   = "derived_from"
	EdgeKindRelatedTo     = "related_to"

	NodeStatusActive       = "active"
	NodeStatusSuperseded   = "superseded"
	NodeStatusStale        = "stale"
	NodeStatusContradicted = "contradicted"
	NodeStatusArchived     = "archived"
	NodeStatusEphemeral    = "ephemeral"
)

// nodeKinds is the closed set of valid node kinds.
var nodeKinds = map[string]bool{
	NodeKindFact:       true,
	NodeKindConclusion: true,
	NodeKindDecision:   true,
	NodeKindProcedure:  true,
	NodeKindFailure:    true,
	NodeKindEvidence:   true,
}

// edgeKinds is the closed set of valid edge kinds.
var edgeKinds = map[string]bool{
	EdgeKindImplementedBy: true,
	EdgeKindDefinedIn:     true,
	EdgeKindTestedBy:      true,
	EdgeKindSupportedBy:   true,
	EdgeKindDependsOn:     true,
	EdgeKindContradicts:   true,
	EdgeKindSupersedes:    true,
	EdgeKindDerivedFrom:   true,
	EdgeKindRelatedTo:     true,
}

// nodeStatuses is the closed set of valid node statuses.
var nodeStatuses = map[string]bool{
	NodeStatusActive:       true,
	NodeStatusSuperseded:   true,
	NodeStatusStale:        true,
	NodeStatusContradicted: true,
	NodeStatusArchived:     true,
	NodeStatusEphemeral:    true,
}

// anchorKinds is the open set of anchor kinds used by /graph/exact. The store
// does not close this set: file, symbol, package, test, and revision are the
// expected kinds, but the exact index is a generic (kind, value) index and a
// caller may anchor to any stable name.
var anchorKinds = map[string]bool{
	"file":     true,
	"symbol":   true,
	"package":  true,
	"test":     true,
	"revision": true,
}

// ValidNodeKind reports whether kind is a known node kind.
func ValidNodeKind(kind string) bool { return nodeKinds[kind] }

// ValidEdgeKind reports whether kind is a known edge kind.
func ValidEdgeKind(kind string) bool { return edgeKinds[kind] }

// ValidNodeStatus reports whether status is a known node status.
func ValidNodeStatus(status string) bool { return nodeStatuses[status] }

// ValidAnchorKind reports whether kind is a known anchor kind. Unknown anchor
// kinds are rejected at the store boundary so a typo like "sybmol" fails loud
// instead of silently creating an unmatchable index entry.
func ValidAnchorKind(kind string) bool { return anchorKinds[kind] }

// Node is one typed cognition graph node.
type Node struct {
	ID               int64
	Kind             string
	Claim            string
	Scope            string
	ProjectPath      sql.NullString
	Status           string
	Confidence       sql.NullFloat64
	SourceRunID      sql.NullString
	CreatedRevision  sql.NullString
	VerifiedRevision sql.NullString
	CreatedAt        int64
	VerifiedAt       sql.NullInt64
	ClaimHash        string
	MetadataJSON     sql.NullString
}

// Edge is one directed typed edge between two nodes.
type Edge struct {
	ID        int64
	SrcID     int64
	DstID     int64
	Kind      string
	CreatedAt int64
}

// Anchor is one typed anchor on a node.
type Anchor struct {
	NodeID int64
	Kind   string
	Value  string
}

// Evidence is one piece of evidence attached to a node.
type Evidence struct {
	NodeID    int64
	Kind      string
	Ref       sql.NullString
	Detail    sql.NullString
	CreatedAt int64
}

// NodeInput is the caller-supplied shape for UpsertNode. Only Kind and Claim
// are required.
type NodeInput struct {
	Kind             string
	Claim            string
	Scope            string
	ProjectPath      string
	Status           string
	Confidence       float64
	ConfidenceValid  bool
	SourceRunID      string
	CreatedRevision  string
	VerifiedRevision string
	Metadata         map[string]any
	Anchors          []AnchorInput
	Edges            []EdgeInput
	Evidence         []EvidenceInput
}

// AnchorInput is one anchor attached to a node at upsert time.
type AnchorInput struct {
	Kind  string
	Value string
}

// EdgeInput is one edge attached to a node at upsert time (or via UpsertEdge).
type EdgeInput struct {
	DstID int64
	Kind  string
}

// EvidenceInput is one piece of evidence attached via AddEvidence or
// Contradict.
type EvidenceInput struct {
	Kind   string
	Ref    string
	Detail string
}

// ErrNotFound is returned by graph reads that target a missing node ID. It is
// the same sentinel the observations store uses.
// (ErrNotFound is declared in store.go; graph code reuses it.)

// graphCounters are package-level lifetime counters for graph writes. The
// store is a process-wide singleton behind one SQLite file, so package-level
// counters match the lifetime of the data they describe.
var (
	graphMu        sync.Mutex
	graphCreated   int64
	graphUpdated   int64
	graphCompacted int64
	graphCollected int64
)

// GraphCounters reports lifetime counters for graph writes.
type GraphCounters struct {
	Created   int64 `json:"created"`
	Updated   int64 `json:"updated"`
	Compacted int64 `json:"compacted"`
	Collected int64 `json:"collected"`
}

// Validate fails loud on any empty required field or unknown enum value. It
// follows the AGENTS.md errors-are-values convention: the error names the
// offending field and value.
func (in *NodeInput) Validate() error {
	if !ValidNodeKind(in.Kind) {
		return fmt.Errorf("graph: node kind %q is not one of fact|conclusion|decision|procedure|failure|evidence", in.Kind)
	}
	if strings.TrimSpace(in.Claim) == "" {
		return fmt.Errorf("graph: claim is required")
	}
	if in.Scope == "" {
		in.Scope = "project"
	}
	if in.Scope != "project" && in.Scope != "global" {
		return fmt.Errorf("graph: scope must be 'project' or 'global', got %q", in.Scope)
	}
	if in.Status == "" {
		in.Status = NodeStatusActive
	}
	if !ValidNodeStatus(in.Status) {
		return fmt.Errorf("graph: status %q is not one of active|superseded|stale|contradicted|archived|ephemeral", in.Status)
	}
	if in.ConfidenceValid && (in.Confidence < 0 || in.Confidence > 1) {
		return fmt.Errorf("graph: confidence must be in [0, 1], got %f", in.Confidence)
	}
	for _, a := range in.Anchors {
		if !ValidAnchorKind(a.Kind) {
			return fmt.Errorf("graph: anchor kind %q is not one of file|symbol|package|test|revision", a.Kind)
		}
		if strings.TrimSpace(a.Value) == "" {
			return fmt.Errorf("graph: anchor kind %q has an empty value", a.Kind)
		}
	}
	return nil
}

// Validate fails loud on unknown edge kind, empty dst id, or empty src id.
func (e *EdgeInput) Validate(srcID int64) error {
	if srcID <= 0 {
		return fmt.Errorf("graph: edge src_id must be >= 1, got %d", srcID)
	}
	if e.DstID <= 0 {
		return fmt.Errorf("graph: edge dst_id must be >= 1, got %d", e.DstID)
	}
	if !ValidEdgeKind(e.Kind) {
		return fmt.Errorf("graph: edge kind %q is not one of implemented_by|defined_in|tested_by|supported_by|depends_on|contradicts|supersedes|derived_from|related_to", e.Kind)
	}
	return nil
}

// Validate fails loud on an unknown evidence kind. Evidence kind is an open
// vocabulary (failing_test, compile_output, run_log, and so on) but it must
// not be empty.
func (e *EvidenceInput) Validate() error {
	if strings.TrimSpace(e.Kind) == "" {
		return fmt.Errorf("graph: evidence kind is required")
	}
	return nil
}

// claimHash computes a deterministic sha256 over the claim with whitespace
// runs collapsed to single spaces. Case is preserved: "Foo" and "foo" are
// different claims.
func claimHash(claim string) string {
	collapsed := strings.Join(strings.Fields(claim), " ")
	sum := sha256.Sum256([]byte(collapsed))
	return hex.EncodeToString(sum[:])
}

// nodeFromRow scans one cognition_nodes row.
func nodeFromRow(scan func(dest ...any) error) (Node, error) {
	var n Node
	err := scan(
		&n.ID, &n.Kind, &n.Claim, &n.Scope, &n.ProjectPath, &n.Status,
		&n.Confidence, &n.SourceRunID, &n.CreatedRevision, &n.VerifiedRevision,
		&n.CreatedAt, &n.VerifiedAt, &n.ClaimHash, &n.MetadataJSON,
	)
	if err != nil {
		return Node{}, err
	}
	return n, nil
}

const nodeColumns = `id, kind, claim, scope, project_path, status, confidence,
source_run_id, created_revision, verified_revision, created_at, verified_at,
claim_hash, metadata_json`

// UpsertNode dedupes by (kind, claim_hash, project_path). A match updates
// verified_at, verified_revision, and provenance, then returns the existing
// row with its canonical id. No match inserts a new node. Anchors and edges
// ride along in the same transaction.
func (s *Store) UpsertNode(ctx context.Context, in NodeInput) (Node, error) {
	if err := in.Validate(); err != nil {
		return Node{}, err
	}
	now := time.Now().Unix()
	hash := claimHash(in.Claim)

	var metadataJSON sql.NullString
	if len(in.Metadata) > 0 {
		raw, err := json.Marshal(in.Metadata)
		if err != nil {
			return Node{}, fmt.Errorf("graph: marshal metadata: %w", err)
		}
		metadataJSON = sql.NullString{String: string(raw), Valid: true}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, fmt.Errorf("graph: upsert begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var (
		node    Node
		created bool
	)
	err = tx.QueryRowContext(ctx, `
		SELECT `+nodeColumns+`
		FROM cognition_nodes
		WHERE kind = ? AND claim_hash = ? AND ifnull(project_path, '') = ifnull(?, '')
		LIMIT 1
	`, in.Kind, hash, nullString(in.ProjectPath)).Scan(
		&node.ID, &node.Kind, &node.Claim, &node.Scope, &node.ProjectPath,
		&node.Status, &node.Confidence, &node.SourceRunID,
		&node.CreatedRevision, &node.VerifiedRevision, &node.CreatedAt,
		&node.VerifiedAt, &node.ClaimHash, &node.MetadataJSON,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		created = true
		res, execErr := tx.ExecContext(ctx, `
			INSERT INTO cognition_nodes(
				kind, claim, scope, project_path, status, confidence,
				source_run_id, created_revision, verified_revision,
				created_at, verified_at, claim_hash, metadata_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, in.Kind, in.Claim, in.Scope, nullString(in.ProjectPath), in.Status,
			nullFloat(in.Confidence, in.ConfidenceValid), nullString(in.SourceRunID),
			nullString(in.CreatedRevision), nullString(in.VerifiedRevision),
			now, now, hash, metadataJSON)
		if execErr != nil {
			return Node{}, fmt.Errorf("graph: insert node: %w", execErr)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return Node{}, fmt.Errorf("graph: insert node id: %w", err)
		}
		node = Node{
			ID: id, Kind: in.Kind, Claim: in.Claim, Scope: in.Scope,
			ProjectPath: nullString(in.ProjectPath), Status: in.Status,
			Confidence:       nullFloat(in.Confidence, in.ConfidenceValid),
			SourceRunID:      nullString(in.SourceRunID),
			CreatedRevision:  nullString(in.CreatedRevision),
			VerifiedRevision: nullString(in.VerifiedRevision),
			CreatedAt:        now,
			VerifiedAt:       sql.NullInt64{Int64: now, Valid: true},
			ClaimHash:        hash,
			MetadataJSON:     metadataJSON,
		}
	case err != nil:
		return Node{}, fmt.Errorf("graph: dedupe query: %w", err)
	default:
		// Update the existing row: refresh verified_at/revision, merge
		// provenance, and keep the canonical id. Provenance merge appends a
		// new source_run_id into metadata_json.run_ids (deduped, sorted) and
		// keeps the highest confidence seen.
		updatedMeta, metaErr := mergeProvenanceMetadata(node.MetadataJSON, in.Metadata, in.SourceRunID)
		if metaErr != nil {
			return Node{}, metaErr
		}
		newConf := node.Confidence
		if in.ConfidenceValid && (!node.Confidence.Valid || in.Confidence > node.Confidence.Float64) {
			newConf = sql.NullFloat64{Float64: in.Confidence, Valid: true}
		}
		verifiedRev := node.VerifiedRevision
		if in.VerifiedRevision != "" {
			verifiedRev = sql.NullString{String: in.VerifiedRevision, Valid: true}
		}
		if _, execErr := tx.ExecContext(ctx, `
			UPDATE cognition_nodes
			SET verified_at = ?, verified_revision = ?, confidence = ?, metadata_json = ?
			WHERE id = ?
		`, now, verifiedRev, newConf, updatedMeta, node.ID); execErr != nil {
			return Node{}, fmt.Errorf("graph: update node %d: %w", node.ID, execErr)
		}
		node.VerifiedAt = sql.NullInt64{Int64: now, Valid: true}
		node.VerifiedRevision = verifiedRev
		node.Confidence = newConf
		node.MetadataJSON = updatedMeta
	}
	// Anchors: insert-or-ignore. The UNIQUE(node_id, kind, value) index makes
	// re-upserts idempotent, so repeated writes never accumulate duplicate
	// anchor rows.
	for _, a := range in.Anchors {
		if _, execErr := tx.ExecContext(ctx, `
			INSERT INTO cognition_anchors(node_id, kind, value) VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING
		`, node.ID, a.Kind, a.Value); execErr != nil {
			return Node{}, fmt.Errorf("graph: insert anchor %s=%s: %w", a.Kind, a.Value, execErr)
		}
	}

	// Edges from this node. Insert-or-ignore keeps the UNIQUE constraint as
	// the dedupe authority.
	for _, e := range in.Edges {
		if err := e.Validate(node.ID); err != nil {
			return Node{}, err
		}
		if _, execErr := tx.ExecContext(ctx, `
			INSERT INTO cognition_edges(src_id, dst_id, kind, created_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(src_id, dst_id, kind) DO NOTHING
		`, node.ID, e.DstID, e.Kind, now); execErr != nil {
			return Node{}, fmt.Errorf("graph: insert edge %s -> %d: %w", e.Kind, e.DstID, execErr)
		}
	}

	// Deterministic evidence attached at upsert time (same transaction, so a
	// node and its evidence are never observed out of sync).
	for _, ev := range in.Evidence {
		if _, execErr := tx.ExecContext(ctx, `
			INSERT INTO cognition_evidence(node_id, kind, ref, detail, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, node.ID, ev.Kind, ev.Ref, ev.Detail, now); execErr != nil {
			return Node{}, fmt.Errorf("graph: insert evidence %s: %w", ev.Kind, execErr)
		}
	}

	if err := tx.Commit(); err != nil {
		return Node{}, fmt.Errorf("graph: upsert commit: %w", err)
	}
	recordGraphWrite(created)
	return node, nil
}

// mergeProvenanceMetadata merges the incoming metadata map and source run id
// into the existing metadata_json blob. run_ids are merged, deduped, and
// sorted so the result is deterministic.
func mergeProvenanceMetadata(existing sql.NullString, incoming map[string]any, sourceRunID string) (sql.NullString, error) {
	merged := map[string]any{}
	if existing.Valid && existing.String != "" {
		if err := json.Unmarshal([]byte(existing.String), &merged); err != nil {
			return sql.NullString{}, fmt.Errorf("graph: decode metadata %s: %w", existing.String, err)
		}
	}
	for k, v := range incoming {
		merged[k] = v
	}
	if sourceRunID != "" {
		runs := map[string]bool{}
		if raw, ok := merged["run_ids"].([]any); ok {
			for _, r := range raw {
				if s, ok := r.(string); ok {
					runs[s] = true
				}
			}
		}
		runs[sourceRunID] = true
		ids := make([]string, 0, len(runs))
		for id := range runs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		merged["run_ids"] = ids
	}
	if len(merged) == 0 {
		return sql.NullString{}, nil
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("graph: marshal merged metadata: %w", err)
	}
	return sql.NullString{String: string(raw), Valid: true}, nil
}

// UpsertEdge inserts or ignores one directed edge.
func (s *Store) UpsertEdge(ctx context.Context, srcID, dstID int64, kind string) error {
	in := EdgeInput{DstID: dstID, Kind: kind}
	if err := in.Validate(srcID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cognition_edges(src_id, dst_id, kind, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(src_id, dst_id, kind) DO NOTHING
	`, srcID, dstID, kind, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("graph: upsert edge %d -> %d (%s): %w", srcID, dstID, kind, err)
	}
	return nil
}

// AddAnchor attaches one anchor to an existing node. Duplicate (node, kind,
// value) rows are idempotent.
func (s *Store) AddAnchor(ctx context.Context, nodeID int64, kind, value string) error {
	if nodeID <= 0 {
		return fmt.Errorf("graph: node id must be >= 1, got %d", nodeID)
	}
	if !ValidAnchorKind(kind) {
		return fmt.Errorf("graph: anchor kind %q is not one of file|symbol|package|test|revision", kind)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("graph: anchor value is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cognition_anchors(node_id, kind, value) VALUES (?, ?, ?)
		ON CONFLICT DO NOTHING
	`, nodeID, kind, value)
	if err != nil {
		return fmt.Errorf("graph: add anchor %s=%s to %d: %w", kind, value, nodeID, err)
	}
	return nil
}

// AddEvidence attaches one evidence row to an existing node.
func (s *Store) AddEvidence(ctx context.Context, nodeID int64, in EvidenceInput) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if nodeID <= 0 {
		return fmt.Errorf("graph: node id must be >= 1, got %d", nodeID)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cognition_evidence(node_id, kind, ref, detail, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, nodeID, in.Kind, nullString(in.Ref), nullString(in.Detail), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("graph: add evidence %q to %d: %w", in.Kind, nodeID, err)
	}
	return nil
}

// GetExactOptions bounds and scopes an exact anchor query.
type GetExactOptions struct {
	ProjectPath string
	Status      string
	Limit       int
}

// GetExact returns nodes that carry ALL the requested anchors, active by
// default, ordered by id. The SQL is fully index-backed: (kind, value) on
// cognition_anchors, then (status) on cognition_nodes.
func (s *Store) GetExact(ctx context.Context, anchors map[string][]string, opts GetExactOptions) ([]Node, error) {
	if len(anchors) == 0 {
		return nil, fmt.Errorf("graph: at least one anchor is required for exact retrieval")
	}
	status := opts.Status
	if status == "" {
		status = NodeStatusActive
	}
	if !ValidNodeStatus(status) {
		return nil, fmt.Errorf("graph: status %q is not one of active|superseded|stale|contradicted|archived|ephemeral", status)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 32
	}

	// Build the INTERSECT chain over the (kind, value) index. Deterministic
	// order: sorted kinds, sorted values.
	kinds := make([]string, 0, len(anchors))
	for k := range anchors {
		if !ValidAnchorKind(k) {
			return nil, fmt.Errorf("graph: anchor kind %q is not one of file|symbol|package|test|revision", k)
		}
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	var args []any
	selects := make([]string, 0, len(kinds))
	for _, k := range kinds {
		vals := anchors[k]
		if len(vals) == 0 {
			return nil, fmt.Errorf("graph: anchor kind %q has no values", k)
		}
		sorted := append([]string(nil), vals...)
		sort.Strings(sorted)
		placeholders := make([]string, 0, len(sorted))
		for _, v := range sorted {
			args = append(args, k, v)
			placeholders = append(placeholders, "(?, ?)")
		}
		selects = append(selects, fmt.Sprintf(
			`SELECT DISTINCT node_id FROM cognition_anchors WHERE (kind, value) IN (%s)`,
			strings.Join(placeholders, ", ")))
	}

	query := selects[0]
	for _, sel := range selects[1:] {
		query += " INTERSECT " + sel
	}
	query = `
		SELECT ` + nodeColumns + `
		FROM cognition_nodes n
		WHERE n.id IN (` + query + `)
		  AND n.status = ?
		  AND (? = '' OR n.project_path = ?)
		ORDER BY n.id
		LIMIT ?`
	args = append(args, status, opts.ProjectPath, opts.ProjectPath, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: exact query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := []Node{}
	for rows.Next() {
		n, err := nodeFromRow(func(dest ...any) error {
			return rows.Scan(dest...)
		})
		if err != nil {
			return nil, fmt.Errorf("graph: exact scan: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: exact rows: %w", err)
	}
	return out, nil
}

// Neighbors walks a bounded BFS from nodeID, following only the given edge
// kinds (empty = all kinds), collecting only active nodes. depth is clamped
// to [1, 2]; limit bounds the total node budget. Deterministic: edges are
// traversed in (kind, dst) order.
func (s *Store) Neighbors(ctx context.Context, nodeID int64, kinds []EdgeKindFilter, depth, limit int) ([]Node, []Edge, error) {
	if nodeID <= 0 {
		return nil, nil, fmt.Errorf("graph: node id must be >= 1, got %d", nodeID)
	}
	if depth > 2 {
		depth = 2
	}
	if depth < 1 {
		depth = 1
	}
	if limit <= 0 {
		limit = 32
	}
	kindFilter := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		kindFilter[string(k)] = true
	}

	visited := map[int64]bool{nodeID: true}
	frontier := []int64{nodeID}
	var (
		nodes   []Node
		edges   []Edge
		edgeIDs = map[int64]bool{}
	)
	for d := 0; d < depth && len(frontier) > 0 && len(nodes) < limit; d++ {
		var next []int64
		for _, cur := range frontier {
			rows, err := s.db.QueryContext(ctx, `
				SELECT e.id, e.src_id, e.dst_id, e.kind, e.created_at
				FROM cognition_edges e
				WHERE e.src_id = ? OR e.dst_id = ?
				ORDER BY e.kind, e.id
			`, cur, cur)
			if err != nil {
				return nil, nil, fmt.Errorf("graph: neighbors query %d: %w", cur, err)
			}
			for rows.Next() {
				var e Edge
				if err := rows.Scan(&e.ID, &e.SrcID, &e.DstID, &e.Kind, &e.CreatedAt); err != nil {
					rows.Close()
					return nil, nil, fmt.Errorf("graph: neighbors scan: %w", err)
				}
				if len(kindFilter) > 0 && !kindFilter[e.Kind] {
					continue
				}
				if !edgeIDs[e.ID] {
					edgeIDs[e.ID] = true
					edges = append(edges, e)
				}
				other := e.DstID
				if other == cur {
					other = e.SrcID
				}
				if !visited[other] {
					visited[other] = true
					next = append(next, other)
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, nil, fmt.Errorf("graph: neighbors rows: %w", err)
			}
			rows.Close()
		}
		sort.Slice(next, func(i, j int) bool { return next[i] < next[j] })
		for _, id := range next {
			if len(nodes) >= limit {
				break
			}
			n, err := s.getActiveNode(ctx, id)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, n)
		}
		frontier = next
	}
	return nodes, edges, nil
}

// EdgeKindFilter is a typed edge-kind filter value for Neighbors. It exists so
// callers cannot pass arbitrary strings without a cast.
type EdgeKindFilter string

// getActiveNode loads one node by id, requiring active status.
func (s *Store) getActiveNode(ctx context.Context, id int64) (Node, error) {
	var n Node
	err := s.db.QueryRowContext(ctx, `
		SELECT `+nodeColumns+` FROM cognition_nodes WHERE id = ? AND status = ?
	`, id, NodeStatusActive).Scan(
		&n.ID, &n.Kind, &n.Claim, &n.Scope, &n.ProjectPath, &n.Status,
		&n.Confidence, &n.SourceRunID, &n.CreatedRevision, &n.VerifiedRevision,
		&n.CreatedAt, &n.VerifiedAt, &n.ClaimHash, &n.MetadataJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("graph: get node %d: %w", id, err)
	}
	return n, nil
}

// SetStatus moves a node to a new status.
func (s *Store) SetStatus(ctx context.Context, nodeID int64, status string) error {
	if nodeID <= 0 {
		return fmt.Errorf("graph: node id must be >= 1, got %d", nodeID)
	}
	if !ValidNodeStatus(status) {
		return fmt.Errorf("graph: status %q is not one of active|superseded|stale|contradicted|archived|ephemeral", status)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE cognition_nodes SET status = ? WHERE id = ?
	`, status, nodeID)
	if err != nil {
		return fmt.Errorf("graph: set status %q on %d: %w", status, nodeID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("graph: set status rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Contradict marks nodeID contradicted, adds a contradicts edge from the
// contradiction source node to it, and records the evidence row.
func (s *Store) Contradict(ctx context.Context, nodeID int64, byNodeID int64, in EvidenceInput) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if nodeID <= 0 {
		return fmt.Errorf("graph: node id must be >= 1, got %d", nodeID)
	}
	if byNodeID <= 0 {
		return fmt.Errorf("graph: contradicting node id must be >= 1, got %d", byNodeID)
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graph: contradict begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM cognition_nodes WHERE id = ?`, nodeID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("graph: contradict lookup %d: %w", nodeID, err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM cognition_nodes WHERE id = ?`, byNodeID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("graph: contradicting node %d: %w", byNodeID, ErrNotFound)
		}
		return fmt.Errorf("graph: contradicting node lookup %d: %w", byNodeID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE cognition_nodes SET status = ? WHERE id = ?
	`, NodeStatusContradicted, nodeID); err != nil {
		return fmt.Errorf("graph: contradict update %d: %w", nodeID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cognition_edges(src_id, dst_id, kind, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(src_id, dst_id, kind) DO NOTHING
	`, byNodeID, nodeID, EdgeKindContradicts, now); err != nil {
		return fmt.Errorf("graph: contradict edge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cognition_evidence(node_id, kind, ref, detail, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, nodeID, in.Kind, nullString(in.Ref), nullString(in.Detail), now); err != nil {
		return fmt.Errorf("graph: contradict evidence: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graph: contradict commit: %w", err)
	}
	return nil
}

// CompactionReport summarizes one Compact pass.
type CompactionReport struct {
	DuplicateGroups  int   `json:"duplicate_groups"`
	DuplicatesMerged int   `json:"duplicates_merged"`
	EdgesRetargeted  int   `json:"edges_retargeted"`
	AnchorsMerged    int   `json:"anchors_merged"`
	EvidenceMerged   int   `json:"evidence_merged"`
	DurationMs       int64 `json:"duration_ms"`
}

// Compact merges duplicate active nodes grouped by (kind, claim_hash,
// project_path). The lowest id in a group is canonical. Provenance (merged
// run_ids) rides into the canonical node's metadata, edges retarget to the
// canonical id, and duplicates are superseded (never deleted, never merged
// when contradicted). Deterministic: groups are processed in ascending
// canonical id order.
func (s *Store) Compact(ctx context.Context) (CompactionReport, error) {
	start := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompactionReport{}, fmt.Errorf("graph: compact begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.QueryContext(ctx, `
		SELECT kind, claim_hash, ifnull(project_path, ''), COUNT(*), MIN(id)
		FROM cognition_nodes
		WHERE status = ?
		GROUP BY kind, claim_hash, ifnull(project_path, '')
		HAVING COUNT(*) > 1
		ORDER BY MIN(id)
	`, NodeStatusActive)
	if err != nil {
		return CompactionReport{}, fmt.Errorf("graph: compact group query: %w", err)
	}
	type group struct {
		kind      string
		claimHash string
		project   string
		count     int
		canonical int64
	}
	var groups []group
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.kind, &g.claimHash, &g.project, &g.count, &g.canonical); err != nil {
			rows.Close()
			return CompactionReport{}, fmt.Errorf("graph: compact group scan: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CompactionReport{}, fmt.Errorf("graph: compact group rows: %w", err)
	}
	rows.Close()

	var report CompactionReport
	for _, g := range groups {
		// Load the duplicate ids excluding the canonical one, excluding
		// contradicted duplicates (they are kept as their own record).
		dupeRows, err := tx.QueryContext(ctx, `
			SELECT id, source_run_id, metadata_json FROM cognition_nodes
			WHERE kind = ? AND claim_hash = ? AND ifnull(project_path, '') = ?
			  AND id != ? AND status = ? AND status != ?
			ORDER BY id
		`, g.kind, g.claimHash, g.project, g.canonical, NodeStatusActive, NodeStatusContradicted)
		if err != nil {
			return CompactionReport{}, fmt.Errorf("graph: compact dupes %d: %w", g.canonical, err)
		}
		type dupe struct {
			id        int64
			sourceRun sql.NullString
			meta      sql.NullString
		}
		var dupes []dupe
		for dupeRows.Next() {
			var d dupe
			if err := dupeRows.Scan(&d.id, &d.sourceRun, &d.meta); err != nil {
				dupeRows.Close()
				return CompactionReport{}, fmt.Errorf("graph: compact dupe scan: %w", err)
			}
			dupes = append(dupes, d)
		}
		if err := dupeRows.Err(); err != nil {
			dupeRows.Close()
			return CompactionReport{}, fmt.Errorf("graph: compact dupe rows: %w", err)
		}
		dupeRows.Close()

		for _, d := range dupes {
			// Retarget edges pointing at the dupe to the canonical node. The
			// UNIQUE(src, dst, kind) constraint plus ON CONFLICT DO NOTHING
			// keeps retargeting idempotent when the canonical edge exists.
			if _, err := tx.ExecContext(ctx, `
				UPDATE OR IGNORE cognition_edges SET dst_id = ? WHERE dst_id = ?
			`, g.canonical, d.id); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact retarget dst %d: %w", d.id, err)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE OR IGNORE cognition_edges SET src_id = ? WHERE src_id = ?
			`, g.canonical, d.id); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact retarget src %d: %w", d.id, err)
			}
			// Move remaining unique edges then drop collisions.
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM cognition_edges WHERE src_id = ? OR dst_id = ?
			`, d.id, d.id); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact drop residual edges %d: %w", d.id, err)
			}
			// Move anchors and evidence onto the canonical node.
			if _, err := tx.ExecContext(ctx, `
				UPDATE OR IGNORE cognition_anchors SET node_id = ? WHERE node_id = ?
			`, g.canonical, d.id); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact anchors %d: %w", d.id, err)
			}
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM cognition_anchors WHERE node_id = ?
			`, d.id); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact residual anchors %d: %w", d.id, err)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE cognition_evidence SET node_id = ? WHERE node_id = ?
			`, g.canonical, d.id); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact evidence %d: %w", d.id, err)
			}
			// Merge provenance into the canonical node's metadata.
			var canonicalMeta sql.NullString
			if err := tx.QueryRowContext(ctx, `
				SELECT metadata_json FROM cognition_nodes WHERE id = ?
			`, g.canonical).Scan(&canonicalMeta); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact canonical meta %d: %w", g.canonical, err)
			}
			merged, err := mergeProvenanceMetadata(canonicalMeta, nil, d.sourceRun.String)
			if err != nil {
				return CompactionReport{}, err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE cognition_nodes SET metadata_json = ? WHERE id = ?
			`, merged, g.canonical); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact merge meta %d: %w", g.canonical, err)
			}
			// Supersede the duplicate. Contradicted duplicates were already
			// excluded above, so they keep their status.
			if _, err := tx.ExecContext(ctx, `
				UPDATE cognition_nodes SET status = ? WHERE id = ?
			`, NodeStatusSuperseded, d.id); err != nil {
				return CompactionReport{}, fmt.Errorf("graph: compact supersede %d: %w", d.id, err)
			}
			report.DuplicatesMerged++
			report.EdgesRetargeted++ // approximate: one retarget batch per dupe
		}
		if len(dupes) > 0 {
			report.DuplicateGroups++
		}
	}
	if err := tx.Commit(); err != nil {
		return CompactionReport{}, fmt.Errorf("graph: compact commit: %w", err)
	}
	report.DurationMs = time.Since(start).Milliseconds()
	recordCompaction(int64(report.DuplicatesMerged))
	return report, nil
}

// Collect hard-deletes ephemeral nodes that are stale (not verified recently
// relative to now), unreferenced by any edge, and carry no evidence rows.
// Non-ephemeral nodes are never deleted here. Returns the count removed.
func (s *Store) Collect(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM cognition_nodes
		WHERE status = ?
		  AND (verified_at IS NULL OR verified_at < ?)
		  AND id NOT IN (SELECT src_id FROM cognition_edges)
		  AND id NOT IN (SELECT dst_id FROM cognition_edges)
		  AND id NOT IN (SELECT node_id FROM cognition_evidence)
	`, NodeStatusEphemeral, cutoff)
	if err != nil {
		return 0, fmt.Errorf("graph: collect: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("graph: collect rows: %w", err)
	}
	recordCollection(n)
	return n, nil
}

// GraphTelemetry returns lifetime counters for graph writes.
func GraphTelemetry() GraphCounters {
	graphMu.Lock()
	defer graphMu.Unlock()
	return GraphCounters{
		Created:   graphCreated,
		Updated:   graphUpdated,
		Compacted: graphCompacted,
		Collected: graphCollected,
	}
}

// recordGraphWrite bumps created/updated counters. Store methods may be called
// from multiple goroutines (the HTTP server), so the counters are mutex-kept.
func recordGraphWrite(created bool) {
	graphMu.Lock()
	defer graphMu.Unlock()
	if created {
		graphCreated++
	} else {
		graphUpdated++
	}
}

func recordCompaction(merged int64) {
	graphMu.Lock()
	defer graphMu.Unlock()
	graphCompacted += merged
}

func recordCollection(n int64) {
	graphMu.Lock()
	defer graphMu.Unlock()
	graphCollected += n
}

// nullString converts "" to SQL NULL so the sidecar round-trips absent fields
// cleanly.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullFloat converts an unset confidence to SQL NULL.
func nullFloat(f float64, valid bool) sql.NullFloat64 {
	if !valid {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}
