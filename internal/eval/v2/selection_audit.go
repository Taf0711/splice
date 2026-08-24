package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// SelectionAuditEntry records the sealed selector outcome for one task.
type SelectionAuditEntry struct {
	TaskID                    string   `json:"task_id"`
	ExpectedSelectedIDs       []string `json:"expected_selected_ids"`
	ExpectedPostCompactionIDs []string `json:"expected_post_compaction_ids"`
	RetrievalMiss             bool     `json:"retrieval_miss"`
}

// SelectionAudit is immutable selection evidence consumed by selection
// verification and manifest locking at the corpus checkpoint.
type SelectionAudit struct {
	Tasks []SelectionAuditEntry `json:"tasks"`
}

// Validate checks duplicate task IDs and retrieval-miss invariants.
func (a SelectionAudit) Validate() error {
	seen := make(map[string]bool, len(a.Tasks))
	for index, entry := range a.Tasks {
		if entry.TaskID == "" {
			return fmt.Errorf("tasks[%d]: task_id is required", index)
		}
		if seen[entry.TaskID] {
			return fmt.Errorf("duplicate selection audit task_id %q", entry.TaskID)
		}
		seen[entry.TaskID] = true
		if err := validateIDs(entry.ExpectedSelectedIDs, "expected_selected_ids"); err != nil {
			return fmt.Errorf("task %s: %w", entry.TaskID, err)
		}
		if err := validateIDs(entry.ExpectedPostCompactionIDs, "expected_post_compaction_ids"); err != nil {
			return fmt.Errorf("task %s: %w", entry.TaskID, err)
		}
		if entry.RetrievalMiss && (len(entry.ExpectedSelectedIDs) != 0 || len(entry.ExpectedPostCompactionIDs) != 0) {
			return fmt.Errorf("task %s: retrieval miss must have empty expected selections", entry.TaskID)
		}
	}
	return nil
}

// ValidateFor checks audit task IDs against the locked manifest task set.
func (a SelectionAudit) ValidateFor(tasks []TaskSpec) error {
	if err := a.Validate(); err != nil {
		return err
	}
	known := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		known[task.ID] = true
	}
	for _, entry := range a.Tasks {
		if !known[entry.TaskID] {
			return fmt.Errorf("selection audit references unknown task %q", entry.TaskID)
		}
	}
	return nil
}

// AuditSHA256 returns the SHA-256 hash over canonical audit JSON.
func AuditSHA256(a SelectionAudit) string {
	data, err := json.Marshal(a.canonical())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Encode returns canonical immutable audit JSON.
func (a SelectionAudit) Encode() ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(a.canonical())
}

func (a SelectionAudit) canonical() SelectionAudit {
	copy := a
	copy.Tasks = append([]SelectionAuditEntry(nil), a.Tasks...)
	sort.Slice(copy.Tasks, func(i, j int) bool { return copy.Tasks[i].TaskID < copy.Tasks[j].TaskID })
	for i := range copy.Tasks {
		copy.Tasks[i].ExpectedSelectedIDs = sortedUnique(copy.Tasks[i].ExpectedSelectedIDs)
		copy.Tasks[i].ExpectedPostCompactionIDs = sortedUnique(copy.Tasks[i].ExpectedPostCompactionIDs)
	}
	return copy
}

func validateIDs(ids []string, field string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("%s contains an empty ID", field)
		}
		if seen[id] {
			return fmt.Errorf("%s contains duplicate ID %q", field, id)
		}
		seen[id] = true
	}
	return nil
}

func sortedUnique(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}
