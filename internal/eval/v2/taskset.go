package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	// Integrity hashes. The base hashes are required for every task. The
	// three solution hashes are required once sealed; an unsealed candidate
	// may omit all three together.
	PromptSHA256            string `json:"prompt_sha256"`
	FixtureArchiveSHA256    string `json:"fixture_archive_sha256"`
	BaselineCommandSHA256   string `json:"baseline_command_sha256"`
	SetupSHA256             string `json:"setup_sha256"`
	CheckSHA256             string `json:"check_sha256"`
	ReferenceSolutionSHA256 string `json:"reference_solution_sha256"`
	// IndependentSolutionSHA256 is a second, independently authored solution
	// proving the check is solvable another way. It must differ from the
	// reference solution: a check satisfied by one canonical solution only is
	// brittle and does not prove general solvability.
	IndependentSolutionSHA256 string `json:"independent_solution_sha256"`
	// MutationProbeSHA256 hashes the mutation probe artifact for the task.
	MutationProbeSHA256 string `json:"mutation_probe_sha256"`

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
	// Base hashes: required for every task (sealed or unsealed).
	baseHashes := []struct{ name, value string }{
		{"prompt_sha256", t.PromptSHA256},
		{"fixture_archive_sha256", t.FixtureArchiveSHA256},
		{"baseline_command_sha256", t.BaselineCommandSHA256},
		{"setup_sha256", t.SetupSHA256},
		{"check_sha256", t.CheckSHA256},
	}
	for _, h := range baseHashes {
		if !validHash(h.value) {
			return fmt.Errorf("task %s: %s must be a sha256 hex digest, got %q", t.ID, h.name, h.value)
		}
	}
	// Solution hashes: reference, independent, mutation probe. A sealed task
	// must carry all three. An unsealed candidate may omit all three together,
	// but partial presence is inconsistent.
	solutionHashes := []struct {
		name  string
		value string
	}{
		{"reference_solution_sha256", t.ReferenceSolutionSHA256},
		{"independent_solution_sha256", t.IndependentSolutionSHA256},
		{"mutation_probe_sha256", t.MutationProbeSHA256},
	}
	present := 0
	for _, h := range solutionHashes {
		if h.value != "" {
			present++
		}
	}
	if t.Sealed && present != 3 {
		missing := make([]string, 0, 3)
		for _, h := range solutionHashes {
			if h.value == "" {
				missing = append(missing, h.name)
			}
		}
		return fmt.Errorf("task %s: sealed tasks need all three solution hashes (reference, independent, mutation probe); missing: %s", t.ID, strings.Join(missing, ", "))
	}
	if !t.Sealed && present > 0 && present < 3 {
		return fmt.Errorf("task %s: solution hashes must be all present or all omitted, got %d of 3", t.ID, present)
	}
	for _, h := range solutionHashes {
		if h.value != "" && !validHash(h.value) {
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
		// DF1: sealed tasks require complete QA evidence collections.
		if len(t.ForbiddenChangedFiles) == 0 {
			return fmt.Errorf("task %s: sealed tasks need a non-empty forbidden_changed_files set", t.ID)
		}
		if len(t.ContextChecks.RequiredFiles) == 0 {
			return fmt.Errorf("task %s: sealed tasks need a non-empty context_checks.required_files set", t.ID)
		}
		// The authority spec requires independent and reference hashes to differ.
		if t.IndependentSolutionSHA256 != "" && t.IndependentSolutionSHA256 == t.ReferenceSolutionSHA256 {
			return fmt.Errorf("task %s: independent and reference solution hashes must differ", t.ID)
		}
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

// CanonicalTaskHash returns the SHA-256 of the canonical JSON encoding of a
// task spec. The canonical form is the JSON encoding with map keys sorted, so
// field order and whitespace never affect task identity. It is the single
// content-identity function for candidate registration, acceptance, and
// manifest locking: every consumer that needs a task's content hash uses this
// helper, never a private re-implementation.
func CanonicalTaskHash(task TaskSpec) (string, error) {
	encoded, err := json.Marshal(task)
	if err != nil {
		return "", err
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return "", err
	}
	// json.Marshal sorts map keys, so this re-encode is the canonical form.
	canonical, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
