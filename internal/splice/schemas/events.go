package schemas

import "fmt"

// ChangedFile describes one changed file in a working-tree summary.
type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // created, modified, deleted
}

// ChangeSummary is a deterministic summary of working-tree changes.
type ChangeSummary struct {
	IsRepo       bool          `json:"is_repo"`
	ChangedFiles []ChangedFile `json:"changed_files,omitempty"`
	DiffText     string        `json:"diff_text"`
	Truncated    bool          `json:"truncated"`
	// DegradedReason names why the Git read could not run when the directory
	// is a repository and the summary came from the filesystem walk instead.
	// It is empty when the Git read succeeded or the directory is not a
	// repository.
	DegradedReason string `json:"degraded_reason,omitempty"`
}

// Validate checks the change summary.
func (c ChangeSummary) Validate() error {
	for i, f := range c.ChangedFiles {
		if f.Path == "" {
			return fmt.Errorf("changed_files[%d]: path is required", i)
		}
	}
	if c.DegradedReason != "" && !c.IsRepo {
		return fmt.Errorf("degraded_reason is set while is_repo is false")
	}
	return nil
}
