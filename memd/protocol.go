package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Taf0711/splice/memd/store"
)

// protocolObservation is the JSON wire format for an observation.  Fields that
// are nullable in the Go store (sql.NullString, sql.NullFloat64, sql.NullInt64)
// are represented as plain Go pointer types so json.Marshal emits null instead
// of the default {"String":"","Valid":false} structs.
type protocolObservation struct {
	ID             int64    `json:"id"`
	ProjectPath    *string  `json:"project_path"`
	Scope          string   `json:"scope"`
	OwnerAgent     string   `json:"owner_agent"`
	Visibility     string   `json:"visibility"`
	MemoryType     string   `json:"memory_type"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	TopicKey       *string  `json:"topic_key"`
	NormalizedHash *string  `json:"normalized_hash"`
	SourceRunID    *string  `json:"source_run_id"`
	SourceStage    *string  `json:"source_stage"`
	SourceBranch   *string  `json:"source_branch"`
	SourceCommit   *string  `json:"source_commit"`
	Pinned         bool     `json:"pinned"`
	Confidence     *float64 `json:"confidence"`
	RevisionCount  int      `json:"revision_count"`
	DuplicateCount int      `json:"duplicate_count"`
	ReviewAfter    *int64   `json:"review_after"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
	DeletedAt      *int64   `json:"deleted_at"`
}

func toProtocol(obs *store.Observation) protocolObservation {
	var projectPath *string
	if obs.ProjectPath.Valid {
		v := obs.ProjectPath.String
		projectPath = &v
	}
	var topicKey *string
	if obs.TopicKey.Valid {
		v := obs.TopicKey.String
		topicKey = &v
	}
	var normalizedHash *string
	if obs.NormalizedHash.Valid {
		v := obs.NormalizedHash.String
		normalizedHash = &v
	}
	var sourceRunID *string
	if obs.SourceRunID.Valid {
		v := obs.SourceRunID.String
		sourceRunID = &v
	}
	var sourceStage *string
	if obs.SourceStage.Valid {
		v := obs.SourceStage.String
		sourceStage = &v
	}
	var sourceBranch *string
	if obs.SourceBranch.Valid {
		v := obs.SourceBranch.String
		sourceBranch = &v
	}
	var sourceCommit *string
	if obs.SourceCommit.Valid {
		v := obs.SourceCommit.String
		sourceCommit = &v
	}
	var confidence *float64
	if obs.Confidence.Valid {
		v := obs.Confidence.Float64
		confidence = &v
	}
	var reviewAfter *int64
	if obs.ReviewAfter.Valid {
		v := obs.ReviewAfter.Int64
		reviewAfter = &v
	}
	var deletedAt *int64
	if obs.DeletedAt.Valid {
		v := obs.DeletedAt.Int64
		deletedAt = &v
	}

	return protocolObservation{
		ID:             obs.ID,
		ProjectPath:    projectPath,
		Scope:          obs.Scope,
		OwnerAgent:     obs.OwnerAgent,
		Visibility:     obs.Visibility,
		MemoryType:     obs.MemoryType,
		Title:          obs.Title,
		Content:        obs.Content,
		TopicKey:       topicKey,
		NormalizedHash: normalizedHash,
		SourceRunID:    sourceRunID,
		SourceStage:    sourceStage,
		SourceBranch:   sourceBranch,
		SourceCommit:   sourceCommit,
		Pinned:         obs.Pinned,
		Confidence:     confidence,
		RevisionCount:  obs.RevisionCount,
		DuplicateCount: obs.DuplicateCount,
		ReviewAfter:    reviewAfter,
		CreatedAt:      obs.CreatedAt,
		UpdatedAt:      obs.UpdatedAt,
		DeletedAt:      deletedAt,
	}
}

// upsertRequest is the JSON body for POST /upsert.
type upsertRequest struct {
	ProjectPath  *string  `json:"project_path"`
	Scope        string   `json:"scope"`
	OwnerAgent   string   `json:"owner_agent"`
	Visibility   string   `json:"visibility"`
	MemoryType   string   `json:"memory_type"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	TopicKey     *string  `json:"topic_key"`
	SourceRunID  *string  `json:"source_run_id"`
	SourceStage  *string  `json:"source_stage"`
	SourceBranch *string  `json:"source_branch"`
	SourceCommit *string  `json:"source_commit"`
	Pinned       bool     `json:"pinned"`
	Confidence   *float64 `json:"confidence"`
}

// searchRequest is the JSON body for POST /search. The include flags are
// pointers so an omitted field defaults to true, matching the Python
// MemoryQuery schema defaults.
type searchRequest struct {
	ProjectPath      string   `json:"project_path"`
	RequestingAgent  string   `json:"requesting_agent"`
	Query            string   `json:"query"`
	Scopes           []string `json:"scopes"`
	IncludePrivate   *bool    `json:"include_private"`
	IncludeShareable *bool    `json:"include_shareable"`
	MemoryTypes      []string `json:"memory_types"`
	Limit            int      `json:"limit"`
}

func (r *upsertRequest) Validate() error {
	if r.OwnerAgent == "" {
		return fmt.Errorf("owner_agent is required")
	}
	if r.Visibility != "private" && r.Visibility != "shareable" {
		return fmt.Errorf("visibility must be 'private' or 'shareable', got %q", r.Visibility)
	}
	if r.MemoryType == "" {
		return fmt.Errorf("memory_type is required")
	}
	if r.Title == "" {
		return fmt.Errorf("title is required")
	}
	if r.Content == "" {
		return fmt.Errorf("content is required")
	}
	if r.Scope == "" {
		r.Scope = "project"
	} else if r.Scope != "project" && r.Scope != "global" {
		return fmt.Errorf("scope must be 'project' or 'global', got %q", r.Scope)
	}
	if r.Confidence != nil && (*r.Confidence < 0 || *r.Confidence > 1) {
		return fmt.Errorf("confidence must be in [0, 1], got %f", *r.Confidence)
	}
	return nil
}

func (r *searchRequest) Validate() error {
	if r.Query == "" {
		return fmt.Errorf("query is required")
	}
	for _, ch := range r.Query {
		if ch < 0x20 && ch != '\t' && ch != '\n' {
			return fmt.Errorf("query contains a control character")
		}
	}
	return r.validateCommon()
}

func (r *searchRequest) ValidateRecent() error {
	return r.validateCommon()
}

func (r *searchRequest) validateCommon() error {
	if r.RequestingAgent == "" {
		return fmt.Errorf("requesting_agent is required")
	}
	if r.Limit > 100 {
		r.Limit = 100
	}
	return nil
}

func (r *markReviewedRequest) Validate() error {
	if r.ID < 1 {
		return fmt.Errorf("id must be >= 1, got %d", r.ID)
	}
	return nil
}

// markReviewedRequest is the JSON body for POST /mark_reviewed.
type markReviewedRequest struct {
	ID int64 `json:"id"`
}

// statsResponse is the JSON body for GET /stats.
type statsResponse struct {
	OK          bool           `json:"ok"`
	Total       int            `json:"total"`
	ByType      map[string]int `json:"by_type"`
	DBSizeBytes int64          `json:"db_size_bytes"`
	Error       string         `json:"error,omitempty"`
}

// genericResponse wraps success/error responses.
type genericResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// traceUpsertRequest is the JSON body for POST /trace/upsert. Only the indexed
// columns are decoded; the full body is stored verbatim as the payload so the
// sidecar never drops unknown fields from a newer schema.
type traceUpsertRequest struct {
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	RepoRoot  string `json:"repo_root"`
	Tier      string `json:"tier"`
	Intent    string `json:"intent"`
	Outcome   struct {
		Status string `json:"status"`
	} `json:"outcome"`
}

func (r *traceUpsertRequest) Validate() error {
	if r.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if r.RepoRoot == "" {
		return fmt.Errorf("repo_root is required")
	}
	if r.Tier == "" {
		return fmt.Errorf("tier is required")
	}
	switch r.Outcome.Status {
	case "running", "completed", "aborted", "failed":
	default:
		return fmt.Errorf("outcome.status must be running, completed, aborted, or failed, got %q", r.Outcome.Status)
	}
	return nil
}

// traceUpsertResponse is the JSON body for POST /trace/upsert. inserted is
// false when a "running" partial write was rejected by the settled-row guard
// (the existing row already reached completed/aborted/failed).
type traceUpsertResponse struct {
	OK       bool `json:"ok"`
	Inserted bool `json:"inserted"`
}

// verdictUpsertRequest is the JSON body for POST /trace/verdict. decided_at is
// the RFC3339 timestamp from VerdictRecord; the store column is unix seconds.
type verdictUpsertRequest struct {
	RunID     string    `json:"run_id"`
	Verdict   string    `json:"verdict"`
	Reason    string    `json:"reject_reason"`
	DecidedAt time.Time `json:"decided_at"`
}

func (r *verdictUpsertRequest) Validate() error {
	if r.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if r.Verdict != "kept" && r.Verdict != "rejected" {
		return fmt.Errorf("verdict must be kept or rejected, got %q", r.Verdict)
	}
	if r.DecidedAt.IsZero() {
		return fmt.Errorf("decided_at is required")
	}
	return nil
}

// traceQueryRequest is the JSON body for POST /trace/query. Empty fields are
// ignored; since is unix seconds.
type traceQueryRequest struct {
	RepoRoot string `json:"repo_root"`
	Tier     string `json:"tier"`
	Status   string `json:"status"`
	Verdict  string `json:"verdict"`
	Query    string `json:"query"`
	Since    int64  `json:"since"`
	Limit    int    `json:"limit"`
}

// verdictResponse is the latest verdict joined into a trace query result.
type verdictResponse struct {
	DecidedAt int64           `json:"decided_at"`
	Verdict   string          `json:"verdict"`
	Reason    string          `json:"reason,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

// traceResponse is one row returned by POST /trace/query.
type traceResponse struct {
	RunID     string           `json:"run_id"`
	SessionID string           `json:"session_id,omitempty"`
	RepoRoot  string           `json:"repo_root"`
	Tier      string           `json:"tier"`
	Status    string           `json:"status"`
	CreatedAt int64            `json:"created_at"`
	Rank      float64          `json:"rank,omitempty"`
	Payload   json.RawMessage  `json:"payload"`
	Verdict   *verdictResponse `json:"verdict,omitempty"`
}

// traceQueryResponse is the JSON body returned by POST /trace/query.
type traceQueryResponse struct {
	OK     bool            `json:"ok"`
	Traces []traceResponse `json:"traces"`
}
