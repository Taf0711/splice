package swebenchpro

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Row is one dataset instance needed to drive a run. Fields mirror the
// columns the official evaluator's CSV expects plus the issue text used as
// the Splice prompt.
type Row struct {
	InstanceID string `json:"instance_id"`
	RepoURL    string `json:"repo_url"`
	BaseCommit string `json:"base_commit"`
	IssueText  string `json:"issue_text"`
}

// LoadRows reads a dataset rows JSON file (array of Row) and returns the
// rows for the wanted instance ids, in manifest order. It fails loudly on a
// missing id: running a subset silently missing members would misstate scope.
func LoadRows(path string, wanted []string) ([]Row, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("swebenchpro.LoadRows: read %s: %w", path, err)
	}
	var all []Row
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("swebenchpro.LoadRows: parse %s: %w", path, err)
	}
	byID := make(map[string]Row, len(all))
	for _, r := range all {
		if r.InstanceID == "" {
			return nil, fmt.Errorf("swebenchpro.LoadRows: %s contains a row with empty instance_id", path)
		}
		byID[r.InstanceID] = r
	}
	rows := make([]Row, 0, len(wanted))
	var missing []string
	for _, id := range wanted {
		r, ok := byID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		rows = append(rows, r)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("swebenchpro.LoadRows: %s is missing wanted instance ids: %v", path, missing)
	}
	return rows, nil
}
