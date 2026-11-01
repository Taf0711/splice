package v2

import (
	"fmt"
	"sort"
)

// SelectedDelivery carries observed selected IDs before and after compaction.
type SelectedDelivery struct {
	SelectedIDs       []string `json:"selected_ids"`
	PostCompactionIDs []string `json:"post_compaction_ids"`
}

// VerifySelection compares observed delivery with the sealed audit as exact
// sets. A mismatch is an infrastructure invalidation, never a tolerated miss.
func VerifySelection(actual SelectedDelivery, audit SelectionAudit, taskID string) error {
	if err := audit.Validate(); err != nil {
		return fmt.Errorf("selection audit: %w", err)
	}
	var expected *SelectionAuditEntry
	for index := range audit.Tasks {
		if audit.Tasks[index].TaskID == taskID {
			entry := audit.Tasks[index]
			expected = &entry
			break
		}
	}
	if expected == nil {
		return fmt.Errorf("selection verification rule=invalidation unknown task_id=%q", taskID)
	}
	selectedMissing, selectedExtra := symmetricDiff(expected.ExpectedSelectedIDs, actual.SelectedIDs)
	postMissing, postExtra := symmetricDiff(expected.ExpectedPostCompactionIDs, actual.PostCompactionIDs)
	if len(selectedMissing) == 0 && len(selectedExtra) == 0 && len(postMissing) == 0 && len(postExtra) == 0 {
		return nil
	}
	return fmt.Errorf("selection verification rule=invalidation task_id=%q selected_missing=%v selected_extra=%v post_compaction_missing=%v post_compaction_extra=%v", taskID, selectedMissing, selectedExtra, postMissing, postExtra)
}

func symmetricDiff(expected, actual []string) ([]string, []string) {
	expectedSet := make(map[string]bool, len(expected))
	actualSet := make(map[string]bool, len(actual))
	for _, id := range expected {
		expectedSet[id] = true
	}
	for _, id := range actual {
		actualSet[id] = true
	}
	missing := make([]string, 0)
	extra := make([]string, 0)
	for id := range expectedSet {
		if !actualSet[id] {
			missing = append(missing, id)
		}
	}
	for id := range actualSet {
		if !expectedSet[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
