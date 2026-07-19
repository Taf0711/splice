package schemas

import (
	"errors"
	"fmt"
)

// MemoryObservation is one persisted memory entry.
type MemoryObservation struct {
	ID             int64    `json:"id"`
	ProjectPath    *string  `json:"project_path,omitempty"`
	Scope          string   `json:"scope"` // project, global, personal
	OwnerAgent     string   `json:"owner_agent"`
	Visibility     string   `json:"visibility"` // private, shareable
	MemoryType     string   `json:"memory_type"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	TopicKey       *string  `json:"topic_key,omitempty"`
	NormalizedHash *string  `json:"normalized_hash,omitempty"`
	SourceRunID    *string  `json:"source_run_id,omitempty"`
	SourceStage    *string  `json:"source_stage,omitempty"`
	SourceBranch   *string  `json:"source_branch,omitempty"`
	SourceCommit   *string  `json:"source_commit,omitempty"`
	Pinned         bool     `json:"pinned"`
	Confidence     *float64 `json:"confidence,omitempty"`
	RevisionCount  int      `json:"revision_count"`
	DuplicateCount int      `json:"duplicate_count"`
	ReviewAfter    *int64   `json:"review_after,omitempty"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
	DeletedAt      *int64   `json:"deleted_at,omitempty"`
}

// Validate checks the memory observation.
func (m MemoryObservation) Validate() error {
	if m.OwnerAgent == "" {
		return errors.New("owner_agent is required")
	}
	if m.Title == "" {
		return errors.New("title is required")
	}
	if m.Content == "" {
		return errors.New("content is required")
	}
	if m.MemoryType == "" {
		return errors.New("memory_type is required")
	}
	switch m.Scope {
	case "project", "global", "personal":
	default:
		return errors.New("scope must be project, global, or personal")
	}
	switch m.Visibility {
	case "private", "shareable":
	default:
		return errors.New("visibility must be private or shareable")
	}
	if m.Confidence != nil {
		if err := validateConfidence(*m.Confidence); err != nil {
			return err
		}
	}
	return nil
}

// MemoryQuery is a search request sent to the memory sidecar.
type MemoryQuery struct {
	ProjectPath      *string  `json:"project_path,omitempty"`
	RequestingAgent  string   `json:"requesting_agent"`
	Query            string   `json:"query"`
	Scopes           []string `json:"scopes,omitempty"`
	IncludePrivate   *bool    `json:"include_private,omitempty"`
	IncludeShareable *bool    `json:"include_shareable,omitempty"`
	MemoryTypes      []string `json:"memory_types,omitempty"`
	Limit            int      `json:"limit"`
}

// Validate checks the memory query.
func (m MemoryQuery) Validate() error {
	if m.RequestingAgent == "" {
		return errors.New("requesting_agent is required")
	}
	if m.Query == "" {
		return errors.New("query is required")
	}
	if m.Limit <= 0 {
		m.Limit = 8
	}
	for _, scope := range m.Scopes {
		switch scope {
		case "project", "global", "personal":
		default:
			return errors.New("scopes must be project, global, or personal")
		}
	}
	return nil
}

// MemoryBundle is bounded memory context injected into a stage's HarnessStageInput.
type MemoryBundle struct {
	RequestingAgent string              `json:"requesting_agent"`
	Observations    []MemoryObservation `json:"observations,omitempty"`
	Truncated       bool                `json:"truncated"`
}

// Validate checks the memory bundle.
func (m MemoryBundle) Validate() error {
	if m.RequestingAgent == "" {
		return errors.New("requesting_agent is required")
	}
	for i, obs := range m.Observations {
		if err := obs.Validate(); err != nil {
			return fmt.Errorf("observations[%d]: %w", i, err)
		}
	}
	return nil
}
