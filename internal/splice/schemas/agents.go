package schemas

import (
	"errors"
	"fmt"
)

// Severity levels used by deterministic and LLM stages.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// FileChange is a generated or modified file.
type FileChange struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	ChangeType string `json:"change_type"` // create, modify, delete
}

// Validate checks that the file change is well-formed.
func (f FileChange) Validate() error {
	if f.Path == "" {
		return errors.New("file change path is required")
	}
	switch f.ChangeType {
	case "create", "modify", "delete":
	default:
		return fmt.Errorf("file change type must be create, modify, or delete, got %q", f.ChangeType)
	}
	return nil
}

// AppliedFileChange is one file change safely applied inside a workspace.
type AppliedFileChange struct {
	Path       string `json:"path"`
	ChangeType string `json:"change_type"`
	BytesRead  int    `json:"bytes_read"`
}

// Validate checks constraints on the applied file change.
func (a AppliedFileChange) Validate() error {
	if a.Path == "" {
		return errors.New("applied file change path is required")
	}
	switch a.ChangeType {
	case "create", "modify", "delete":
	default:
		return fmt.Errorf("applied change type must be create, modify, or delete, got %q", a.ChangeType)
	}
	if a.BytesRead < 0 {
		return errors.New("bytes_read must be non-negative")
	}
	return nil
}

// FileChangeApplyResult is the result of applying model-generated file changes.
type FileChangeApplyResult struct {
	Workspace string              `json:"workspace"`
	Applied   []AppliedFileChange `json:"applied"`
}

// Validate checks the apply result.
func (r FileChangeApplyResult) Validate() error {
	if r.Workspace == "" {
		return errors.New("workspace is required")
	}
	for i, a := range r.Applied {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("applied[%d]: %w", i, err)
		}
	}
	return nil
}

// SelectedMemory is one bounded observation injected into a stage input.
type SelectedMemory struct {
	Title      string `json:"title"`
	Content    string `json:"content"`
	MemoryType string `json:"memory_type"`
	Scope      string `json:"scope"`
}

// CodeWriterInput is the minimum input required by the code writer.
type CodeWriterInput struct {
	Intent          string           `json:"intent"`
	Language        string           `json:"language"`
	TargetPaths     []string         `json:"target_paths,omitempty"`
	RelevantContext []string         `json:"relevant_context,omitempty"`
	RevisionContext *string          `json:"revision_context,omitempty"`
	Memory          []SelectedMemory `json:"memory,omitempty"`
}

// Validate checks the code writer input.
func (c CodeWriterInput) Validate() error {
	if c.Intent == "" {
		return errors.New("intent is required")
	}
	if c.Language == "" {
		return errors.New("language is required")
	}
	return nil
}

// CodeWriterOutput is code writer output validated before any downstream stage receives it.
type CodeWriterOutput struct {
	Files            []FileChange `json:"files"`
	Language         string       `json:"language"`
	Intent           string       `json:"intent"`
	Dependencies     []string     `json:"dependencies,omitempty"`
	KnownLimitations []string     `json:"known_limitations,omitempty"`
	Confidence       float64      `json:"confidence"`
}

// Validate checks the code writer output and embedded file changes.
func (c CodeWriterOutput) Validate() error {
	if c.Language == "" {
		return errors.New("language is required")
	}
	if c.Intent == "" {
		return errors.New("intent is required")
	}
	if err := validateConfidence(c.Confidence); err != nil {
		return fmt.Errorf("confidence: %w", err)
	}
	for i, f := range c.Files {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("files[%d]: %w", i, err)
		}
	}
	return nil
}

// StepBackAnalysis is the result of a fresh-context step-back analysis.
// It is orchestrator-level, not a pipeline stage, and replaces the
// verbose iteration-history dump in the next iteration's revision context.
type StepBackAnalysis struct {
	HypothesizedRootCause string   `json:"hypothesized_root_cause"`
	Evidence              []string `json:"evidence,omitempty"`
	RecommendedApproach   string   `json:"recommended_approach"`
	Confidence            float64  `json:"confidence"`
}

// Validate checks the step-back analysis.
func (s StepBackAnalysis) Validate() error {
	if s.HypothesizedRootCause == "" {
		return errors.New("hypothesized_root_cause is required")
	}
	if s.RecommendedApproach == "" {
		return errors.New("recommended_approach is required")
	}
	if err := validateConfidence(s.Confidence); err != nil {
		return fmt.Errorf("confidence: %w", err)
	}
	return nil
}

// TestGeneratorInput is the minimum input required by the test generator.
type TestGeneratorInput struct {
	Intent          string           `json:"intent"`
	Language        string           `json:"language"`
	TargetPaths     []string         `json:"target_paths,omitempty"`
	RelevantContext []string         `json:"relevant_context,omitempty"`
	RevisionContext *string          `json:"revision_context,omitempty"`
	Memory          []SelectedMemory `json:"memory,omitempty"`
}

// Validate checks the test generator input.
func (t TestGeneratorInput) Validate() error {
	if t.Intent == "" {
		return errors.New("intent is required")
	}
	if t.Language == "" {
		return errors.New("language is required")
	}
	return nil
}

// TestGeneratorOutput is test generator output validated before any downstream stage receives it.
type TestGeneratorOutput struct {
	Files            []FileChange `json:"files"`
	Language         string       `json:"language"`
	Intent           string       `json:"intent"`
	KnownLimitations []string     `json:"known_limitations,omitempty"`
	Confidence       float64      `json:"confidence"`
}

// Validate checks the test generator output.
func (t TestGeneratorOutput) Validate() error {
	if t.Language == "" {
		return errors.New("language is required")
	}
	if t.Intent == "" {
		return errors.New("intent is required")
	}
	if err := validateConfidence(t.Confidence); err != nil {
		return fmt.Errorf("confidence: %w", err)
	}
	for i, f := range t.Files {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("files[%d]: %w", i, err)
		}
	}
	return nil
}

// StaticAnalyzerInput is minimum input for static analysis interpretation.
// TestRunnerInput is input for the deterministic test runner.
type TestRunnerInput struct {
	Command        []string `json:"command"`
	Cwd            *string  `json:"cwd,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// Validate checks the test runner input.
func (t TestRunnerInput) Validate() error {
	if len(t.Command) == 0 {
		return errors.New("command is required")
	}
	if t.TimeoutSeconds <= 0 {
		return errors.New("timeout_seconds must be > 0")
	}
	return nil
}

// TestCaseResult is the result for a single deterministic test case.
type TestCaseResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"` // passed, failed, errored, skipped
	DurationMs int    `json:"duration_ms"`
	Message    string `json:"message,omitempty"`
}

// Validate checks the test case result.
func (t TestCaseResult) Validate() error {
	if t.Name == "" {
		return errors.New("name is required")
	}
	switch t.Status {
	case "passed", "failed", "errored", "skipped":
	default:
		return fmt.Errorf("invalid test status %q", t.Status)
	}
	if t.DurationMs < 0 {
		return errors.New("duration_ms must be non-negative")
	}
	return nil
}

// TestRunResults is deterministic test runner output.
type TestRunResults struct {
	Command    []string         `json:"command"`
	ExitCode   int              `json:"exit_code"`
	Tests      []TestCaseResult `json:"tests"`
	Stdout     string           `json:"stdout"`
	Stderr     string           `json:"stderr"`
	DurationMs int              `json:"duration_ms"`
}

// Validate checks the test run results.
func (t TestRunResults) Validate() error {
	if len(t.Command) == 0 {
		return errors.New("command is required")
	}
	if t.DurationMs < 0 {
		return errors.New("duration_ms must be non-negative")
	}
	for i, tc := range t.Tests {
		if err := tc.Validate(); err != nil {
			return fmt.Errorf("tests[%d]: %w", i, err)
		}
	}
	return nil
}

// Passed returns the number of passing tests.
func (t TestRunResults) Passed() int {
	count := 0
	for _, tc := range t.Tests {
		if tc.Status == "passed" {
			count++
		}
	}
	return count
}

// Failed returns the number of failed or errored tests.
func (t TestRunResults) Failed() int {
	count := 0
	for _, tc := range t.Tests {
		if tc.Status == "failed" || tc.Status == "errored" {
			count++
		}
	}
	return count
}

func validateConfidence(c float64) error {
	if c < 0 || c > 1 {
		return fmt.Errorf("confidence %v must be between 0.0 and 1.0", c)
	}
	return nil
}
