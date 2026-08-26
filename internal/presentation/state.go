package presentation

import (
	"fmt"
	"math"
	"strings"
)

// PresentationSchemaVersionV1 is the schema version of this contract. Every
// serialized state carries it; a state with a different version is not this
// contract.
const PresentationSchemaVersionV1 = 1

// Plan is the lightweight reference to crystallized plan data. The full plan
// projection arrives in P1.4.
type Plan struct {
	Title     string `json:"title,omitempty"`
	TaskCount int    `json:"task_count"`
}

// Validate checks the task count is non-negative. The title may be empty
// until the plan projection exists.
func (p Plan) Validate() error {
	if p.TaskCount < 0 {
		return fmt.Errorf("plan task_count must be non-negative, got %d", p.TaskCount)
	}
	return nil
}

// Trajectory is a placeholder for the trajectory signature. Pass scores and
// restore markers may grow in P1.5.
type Trajectory struct {
	PassScores     []float64 `json:"pass_scores,omitempty"`
	RestoreMarkers []string  `json:"restore_markers,omitempty"`
}

// Validate checks finite in-range pass scores and non-empty markers.
func (t Trajectory) Validate() error {
	for i, score := range t.PassScores {
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return fmt.Errorf("trajectory pass_scores[%d] must be within [0,1], got %v", i, score)
		}
	}
	for i, marker := range t.RestoreMarkers {
		if strings.TrimSpace(marker) == "" {
			return fmt.Errorf("trajectory restore_markers[%d] must not be empty", i)
		}
	}
	return nil
}

// FileChangeSummary summarizes one workspace file change.
type FileChangeSummary struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// Validate checks the path, a non-empty status, and non-negative counts.
func (f FileChangeSummary) Validate() error {
	if strings.TrimSpace(f.Path) == "" {
		return fmt.Errorf("file change path is required")
	}
	if strings.TrimSpace(f.Status) == "" {
		return fmt.Errorf("file change %s: status is required", f.Path)
	}
	if f.Additions < 0 {
		return fmt.Errorf("file change %s: additions must be non-negative, got %d", f.Path, f.Additions)
	}
	if f.Deletions < 0 {
		return fmt.Errorf("file change %s: deletions must be non-negative, got %d", f.Path, f.Deletions)
	}
	return nil
}

// CompletionReceipt records how a run ended. It is nil until the run
// finishes.
type CompletionReceipt struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Validate checks the closed completion status.
func (c CompletionReceipt) Validate() error {
	switch c.Status {
	case "completed", "failed":
		return nil
	}
	return fmt.Errorf("unknown completion status %q", c.Status)
}

// State is the versioned presentation contract the runtime emits and the
// TUI projects. The reducer derives it from stream events; every state
// produced by a legal event sequence passes Validate.
type State struct {
	SchemaVersion int                 `json:"schema_version"`
	Lifecycle     Lifecycle           `json:"lifecycle"`
	Plan          Plan                `json:"plan"`
	Nodes         []ExecutionNode     `json:"nodes"`
	Interventions []Intervention      `json:"interventions"`
	Evidence      []EvidenceGroup     `json:"evidence"`
	Trajectory    Trajectory          `json:"trajectory"`
	Files         []FileChangeSummary `json:"files"`
	Usage         UsageSummary        `json:"usage"`
	Completion    *CompletionReceipt  `json:"completion,omitempty"`
}

// Validate checks the whole state: schema version, lifecycle, and every
// nested value. Duplicate node ids are rejected.
func (s State) Validate() error {
	if s.SchemaVersion != PresentationSchemaVersionV1 {
		return fmt.Errorf("schema_version must be %d, got %d", PresentationSchemaVersionV1, s.SchemaVersion)
	}
	if err := s.Lifecycle.Validate(); err != nil {
		return err
	}
	if err := s.Plan.Validate(); err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	seen := make(map[string]bool, len(s.Nodes))
	for i, node := range s.Nodes {
		if err := node.Validate(); err != nil {
			return fmt.Errorf("nodes[%d]: %w", i, err)
		}
		if seen[node.ID] {
			return fmt.Errorf("duplicate node id %q", node.ID)
		}
		seen[node.ID] = true
	}
	for i, intervention := range s.Interventions {
		if err := intervention.Validate(); err != nil {
			return fmt.Errorf("interventions[%d]: %w", i, err)
		}
	}
	for i, group := range s.Evidence {
		if err := group.Validate(); err != nil {
			return fmt.Errorf("evidence[%d]: %w", i, err)
		}
	}
	if err := s.Trajectory.Validate(); err != nil {
		return err
	}
	for i, file := range s.Files {
		if err := file.Validate(); err != nil {
			return fmt.Errorf("files[%d]: %w", i, err)
		}
	}
	if err := s.Usage.Validate(); err != nil {
		return fmt.Errorf("usage: %w", err)
	}
	if s.Completion != nil {
		if err := s.Completion.Validate(); err != nil {
			return fmt.Errorf("completion: %w", err)
		}
	}
	return nil
}
