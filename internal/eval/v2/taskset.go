package v2

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// TaskSpec is one eval task with its sealing metadata. An unsealed task is a
// candidate; a sealed task carries the full hash and approval record and can
// enter a locked manifest.
type TaskSpec struct {
	ID     string `json:"id"`
	Sealed bool   `json:"sealed"`

	// Representation metadata (protocol section 6.2).
	RepositoryClass  string `json:"repository_class"`
	Language         string `json:"language"`
	Family           string `json:"family"`
	Tier             string `json:"tier"`
	Difficulty       string `json:"difficulty"`
	MemoryCompetency string `json:"memory_competency"`

	// Integrity hashes. All are required once sealed.
	PromptSHA256            string `json:"prompt_sha256"`
	FixtureArchiveSHA256    string `json:"fixture_archive_sha256"`
	BaselineCommandSHA256   string `json:"baseline_command_sha256"`
	SetupSHA256             string `json:"setup_sha256"`
	CheckSHA256             string `json:"check_sha256"`
	ReferenceSolutionSHA256 string `json:"reference_solution_sha256"`

	// Change expectations, reusing the AGENT_EVALS vocabulary.
	ExpectedChangedFiles  []string `json:"expected_changed_files"`
	ForbiddenChangedFiles []string `json:"forbidden_changed_files"`
	RequiredTraceEvents   []string `json:"required_trace_events"`
	ContextChecks         struct {
		RequiredFiles  []string `json:"required_files,omitempty"`
		ForbiddenFiles []string `json:"forbidden_files,omitempty"`
	} `json:"context_checks"`

	// Policy.
	NetworkPolicy string `json:"network_policy"` // "offline" or "allowlisted"

	// Approval record. Required once sealed.
	Author       string `json:"author"`
	Auditor      string `json:"auditor"`
	ApprovalDate string `json:"approval_date"` // RFC3339 with timezone
}

// ValidNetworkPolicy reports whether policy is known.
func ValidNetworkPolicy(policy string) bool {
	return policy == "offline" || policy == "allowlisted"
}

// Validate checks one task. Sealing tightens every rule: hashes, approvers,
// and dates become mandatory.
func (t TaskSpec) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("id is required")
	}
	for _, field := range []struct{ name, value string }{
		{"repository_class", t.RepositoryClass},
		{"language", t.Language},
		{"family", t.Family},
		{"tier", t.Tier},
		{"difficulty", t.Difficulty},
		{"memory_competency", t.MemoryCompetency},
		{"network_policy", t.NetworkPolicy},
	} {
		if field.value == "" {
			return fmt.Errorf("task %s: %s is required", t.ID, field.name)
		}
	}
	if !ValidNetworkPolicy(t.NetworkPolicy) {
		return fmt.Errorf("task %s: invalid network_policy %q", t.ID, t.NetworkPolicy)
	}
	hashes := []struct{ name, value string }{
		{"prompt_sha256", t.PromptSHA256},
		{"fixture_archive_sha256", t.FixtureArchiveSHA256},
		{"baseline_command_sha256", t.BaselineCommandSHA256},
		{"setup_sha256", t.SetupSHA256},
		{"check_sha256", t.CheckSHA256},
		{"reference_solution_sha256", t.ReferenceSolutionSHA256},
	}
	for _, h := range hashes {
		if !validHash(h.value) {
			return fmt.Errorf("task %s: %s must be a sha256 hex digest, got %q", t.ID, h.name, h.value)
		}
	}
	if err := validateFileList(t.ID, "expected_changed_files", t.ExpectedChangedFiles, true); err != nil {
		return err
	}
	if err := validateFileList(t.ID, "forbidden_changed_files", t.ForbiddenChangedFiles, false); err != nil {
		return err
	}
	if err := validateFileList(t.ID, "context_checks.required_files", t.ContextChecks.RequiredFiles, false); err != nil {
		return err
	}
	if err := validateFileList(t.ID, "context_checks.forbidden_files", t.ContextChecks.ForbiddenFiles, false); err != nil {
		return err
	}
	if err := validateStringList(t.ID, "required_trace_events", t.RequiredTraceEvents); err != nil {
		return err
	}
	if overlap := sharedFile(t.ExpectedChangedFiles, t.ForbiddenChangedFiles); overlap != "" {
		return fmt.Errorf("task %s: file %q is both expected and forbidden", t.ID, overlap)
	}
	if overlap := sharedFile(t.ContextChecks.RequiredFiles, t.ContextChecks.ForbiddenFiles); overlap != "" {
		return fmt.Errorf("task %s: context file %q is both required and forbidden", t.ID, overlap)
	}
	if t.Sealed {
		if t.Author == "" || t.Auditor == "" || t.ApprovalDate == "" {
			return fmt.Errorf("task %s: sealed tasks need author, auditor, and approval_date", t.ID)
		}
		if t.Auditor == t.Author {
			return fmt.Errorf("task %s: auditor must differ from author", t.ID)
		}
		if _, err := time.Parse(time.RFC3339, t.ApprovalDate); err != nil {
			return fmt.Errorf("task %s: approval_date must be RFC3339: %w", t.ID, err)
		}
	}
	return nil
}

// TaskSet is the ordered set of tasks an experiment uses.
type TaskSet struct {
	Tasks []TaskSpec `json:"tasks"`
}

// Validate checks every task and rejects duplicate identities.
func (ts TaskSet) Validate() error {
	if len(ts.Tasks) == 0 {
		return fmt.Errorf("tasks must not be empty")
	}
	seen := make(map[string]bool, len(ts.Tasks))
	for i, task := range ts.Tasks {
		if err := task.Validate(); err != nil {
			return fmt.Errorf("tasks[%d]: %w", i, err)
		}
		if seen[task.ID] {
			return fmt.Errorf("duplicate task id %q", task.ID)
		}
		seen[task.ID] = true
	}
	return nil
}

func validateFileList(taskID, field string, files []string, required bool) error {
	if required && len(files) == 0 {
		return fmt.Errorf("task %s: %s is required", taskID, field)
	}
	seen := make(map[string]bool, len(files))
	for i, file := range files {
		normalized, ok := normalizePath(file)
		canonical := strings.TrimSpace(file)
		if file != canonical || strings.Contains(file, "\\") || !ok || normalized == "" || normalized != canonical {
			return fmt.Errorf("task %s: %s[%d] must be a canonical relative workspace path", taskID, field, i)
		}
		if seen[normalized] {
			return fmt.Errorf("task %s: %s[%d] duplicates %q", taskID, field, i, normalized)
		}
		seen[normalized] = true
	}
	return nil
}

func validateStringList(taskID, field string, values []string) error {
	seen := make(map[string]bool, len(values))
	for i, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("task %s: %s[%d] must not be empty", taskID, field, i)
		}
		if seen[value] {
			return fmt.Errorf("task %s: %s[%d] duplicates %q", taskID, field, i, value)
		}
		seen[value] = true
	}
	return nil
}

func normalizePath(file string) (string, bool) {
	if strings.IndexByte(file, 0) >= 0 {
		return "", false
	}
	file = strings.TrimSpace(strings.ReplaceAll(file, "\\", "/"))
	if file == "" || strings.HasPrefix(file, "/") || strings.Contains(file, ":") {
		return "", false
	}
	clean := path.Clean(file)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func sharedFile(left, right []string) string {
	seen := make(map[string]bool, len(left))
	for _, file := range left {
		if normalized, ok := normalizePath(file); ok {
			seen[normalized] = true
		}
	}
	for _, file := range right {
		if normalized, ok := normalizePath(file); ok && seen[normalized] {
			return normalized
		}
	}
	return ""
}
