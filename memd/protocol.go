package main

import (
	"fmt"

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
