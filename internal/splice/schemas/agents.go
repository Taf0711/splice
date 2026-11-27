package schemas

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
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
	// BaseDigest is the sha256 of the file bytes the change was composed on.
	// Empty for full-content changes with no recorded base.
	BaseDigest string `json:"base_digest,omitempty"`
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

// Memory reasoning contract. The model must consider every delivered memory
// item and return an enum-only apply/reject declaration per stable ID.
// Disposition bookkeeping is observability: it is parsed and reconciled
// separately from core file/test validity so malformed bookkeeping can never
// discard substantive output or trigger a provider retry.
const (
	MemoryActionApplied    = "applied"
	MemoryActionRejected   = "rejected"
	MemoryActionUnreported = "unreported" // orchestrator-synthesized only

	MemoryReasonRelevant            = "relevant"
	MemoryReasonIrrelevant          = "irrelevant"
	MemoryReasonStaleOrIncompatible = "stale_or_incompatible"
	MemoryReasonContradicted        = "contradicted"
	MemoryReasonMissing             = "missing" // synthesized only
)

// MemoryDisposition is one model claim (or one normalized orchestrator entry)
// about a single delivered memory item.
type MemoryDisposition struct {
	MemoryID string `json:"memory_id"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
}

// Validate checks the action/reason pairing. unreported/missing entries are
// synthesized by the orchestrator; the model-facing tool schema never offers
// that combination.
func (m MemoryDisposition) Validate() error {
	if m.MemoryID == "" {
		return errors.New("memory_id is required")
	}
	switch m.Action {
	case MemoryActionApplied:
		if m.Reason != MemoryReasonRelevant {
			return fmt.Errorf("action applied requires reason %q, got %q", MemoryReasonRelevant, m.Reason)
		}
	case MemoryActionRejected:
		switch m.Reason {
		case MemoryReasonIrrelevant, MemoryReasonStaleOrIncompatible, MemoryReasonContradicted:
		default:
			return fmt.Errorf("action rejected requires reason irrelevant, stale_or_incompatible, or contradicted, got %q", m.Reason)
		}
	case MemoryActionUnreported:
		if m.Reason != MemoryReasonMissing {
			return fmt.Errorf("action unreported requires reason %q, got %q", MemoryReasonMissing, m.Reason)
		}
	default:
		return fmt.Errorf("invalid action %q", m.Action)
	}
	return nil
}

// MemoryReview is the complete normalized review for exactly one stage
// invocation. Items appear in delivered input order. InvalidClaims counts
// malformed, unknown-ID, and duplicate model claims plus parse issues.
type MemoryReview struct {
	Items         []MemoryDisposition `json:"items"`
	InvalidClaims int                 `json:"invalid_claims"`
}

// Validate enforces uniqueness, item validity, and non-emptiness: a non-nil
// review with zero items would falsely record that a review happened.
func (r MemoryReview) Validate() error {
	if len(r.Items) == 0 {
		return errors.New("review with zero items is invalid; use a nil review when no memory was delivered")
	}
	if r.InvalidClaims < 0 {
		return errors.New("invalid_claims must be non-negative")
	}
	seen := make(map[string]bool, len(r.Items))
	for i, item := range r.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
		if seen[item.MemoryID] {
			return fmt.Errorf("items[%d]: duplicate memory id %q", i, item.MemoryID)
		}
		seen[item.MemoryID] = true
	}
	return nil
}

// SelectedMemory is one bounded observation injected into a stage input.
// RunID is set only for exemplar items so an audit can trace one back to its
// source run; ordinary memory observations leave it empty.
type SelectedMemory struct {
	// ID is the stable audit key the model must echo in its disposition:
	// "observation:<positive decimal id>" or "exemplar:<run id>".
	ID           string `json:"id"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	MemoryType   string `json:"memory_type"`
	Scope        string `json:"scope"`
	RunID        string `json:"run_id,omitempty"`
	SourceStage  string `json:"source_stage,omitempty"`
	SourceCommit string `json:"source_commit,omitempty"`
}

// SelectedMemory scope values. The first four come from memory observations;
// exemplars use their own dedicated scope.
const (
	MemoryScopeProject  = "project"
	MemoryScopeGlobal   = "global"
	MemoryScopePersonal = "personal"
	MemoryScopeExemplar = "exemplar"
)

// Validate checks one selected memory item, including its stable-ID shape.
func (s SelectedMemory) Validate() error {
	if s.ID == "" {
		return errors.New("id is required")
	}
	if s.Title == "" {
		return errors.New("title is required")
	}
	if s.Content == "" {
		return errors.New("content is required")
	}
	if s.MemoryType == "" {
		return errors.New("memory_type is required")
	}
	switch s.Scope {
	case MemoryScopeProject, MemoryScopeGlobal, MemoryScopePersonal:
		// Identity is source-qualified: observation rows and cognition
		// graph nodes have separate numeric ID sequences, so graph items
		// carry a graph:<id> identity. Both must be positive decimals
		// without leading zeros; anything else fails loud.
		switch {
		case strings.HasPrefix(s.ID, "observation:"):
			num := strings.TrimPrefix(s.ID, "observation:")
			if n, err := strconv.ParseUint(num, 10, 64); err != nil || num == "0" || num == "" || strings.HasPrefix(num, "0") || n == 0 {
				return fmt.Errorf("observation stable id must be a positive decimal without leading zeros, got %q", s.ID)
			}
		case strings.HasPrefix(s.ID, "graph:"):
			num := strings.TrimPrefix(s.ID, "graph:")
			if n, err := strconv.ParseUint(num, 10, 64); err != nil || num == "0" || num == "" || strings.HasPrefix(num, "0") || n == 0 {
				return fmt.Errorf("graph stable id must be a positive decimal without leading zeros, got %q", s.ID)
			}
		default:
			return fmt.Errorf("scope %s requires an observation:<id> or graph:<id> stable id, got %q", s.Scope, s.ID)
		}
	case MemoryScopeExemplar:
		if !strings.HasPrefix(s.ID, "exemplar:") || s.RunID == "" || s.ID != "exemplar:"+s.RunID {
			return fmt.Errorf("scope exemplar requires id exemplar:<non-empty run_id> with matching run_id, got %q", s.ID)
		}
	default:
		return fmt.Errorf("invalid scope %q", s.Scope)
	}
	return nil
}

// CodeWriterInput is the minimum input required by the code writer.
type CodeWriterInput struct {
	Intent          string           `json:"intent"`
	Language        string           `json:"language"`
	TargetPaths     []string         `json:"target_paths,omitempty"`
	RelevantContext []string         `json:"relevant_context,omitempty"`
	RevisionContext *string          `json:"revision_context,omitempty"`
	Memory          []SelectedMemory `json:"memory,omitempty"`
	PipelineStages  []string         `json:"pipeline_stages,omitempty"`
	NextStage       string           `json:"next_stage,omitempty"`
}

// Validate checks the code writer input.
func (c CodeWriterInput) Validate() error {
	if c.Intent == "" {
		return errors.New("intent is required")
	}
	if c.Language == "" {
		return errors.New("language is required")
	}
	if err := validateSelectedMemoryItems(c.Memory); err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	return nil
}

// validateSelectedMemoryItems checks every delivered item and rejects
// duplicate stable IDs loudly before a provider call: a model cannot give a
// meaningful per-item disposition when two items share one id.
func validateSelectedMemoryItems(items []SelectedMemory) error {
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
		if seen[item.ID] {
			return fmt.Errorf("items[%d]: duplicate memory id %q", i, item.ID)
		}
		seen[item.ID] = true
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
	// MemoryDisposition carries raw model claims. It is decoded separately
	// from core output and is intentionally NOT validated here: malformed
	// bookkeeping must never invalidate files.
	MemoryDisposition []MemoryDisposition `json:"memory_disposition,omitempty"`
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
	Intent             string           `json:"intent"`
	Language           string           `json:"language"`
	TargetPaths        []string         `json:"target_paths,omitempty"`
	WriterChangedPaths []string         `json:"writer_changed_paths,omitempty"`
	RelevantContext    []string         `json:"relevant_context,omitempty"`
	RevisionContext    *string          `json:"revision_context,omitempty"`
	Memory             []SelectedMemory `json:"memory,omitempty"`
	PipelineStages     []string         `json:"pipeline_stages,omitempty"`
	NextStage          string           `json:"next_stage,omitempty"`
}

// Validate checks the test generator input.
func (t TestGeneratorInput) Validate() error {
	if t.Intent == "" {
		return errors.New("intent is required")
	}
	if t.Language == "" {
		return errors.New("language is required")
	}
	if err := validateSelectedMemoryItems(t.Memory); err != nil {
		return fmt.Errorf("memory: %w", err)
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
	// See CodeWriterOutput.MemoryDisposition: observability only.
	MemoryDisposition []MemoryDisposition `json:"memory_disposition,omitempty"`
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
	// Name identifies the test. A Go test from `go test -json` has the
	// package-qualified name "<import path>.<TestName>" when the event names
	// its package, so the same test name in two packages stays two entries.
	Name       string `json:"name"`
	Status     string `json:"status"` // passed, failed, errored, skipped
	DurationMs int    `json:"duration_ms"`
	Message    string `json:"message,omitempty"`
	// Package is the Go import path that qualifies Name. Empty for an entry
	// that has no package (non-Go runners, suite and build entries).
	Package string `json:"package,omitempty"`
	// AlreadyFailing marks a failed or errored test that also failed in the
	// pre-run baseline. It is not a new failure of the run.
	AlreadyFailing bool `json:"already_failing,omitempty"`
}

// TestName returns the test name without its package qualifier.
func (t TestCaseResult) TestName() string {
	if t.Package == "" {
		return t.Name
	}
	return strings.TrimPrefix(t.Name, t.Package+".")
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
	if t.AlreadyFailing && !t.failed() {
		return fmt.Errorf("already_failing is valid only for a failed or errored test, got %q", t.Status)
	}
	if t.DurationMs < 0 {
		return errors.New("duration_ms must be non-negative")
	}
	return nil
}

// Test evidence levels. They tell how much a test run proves.
const (
	// TestEvidencePerTest: the runner reported each test (go test -json).
	TestEvidencePerTest = "per_test"
	// TestEvidenceCommandOnly: only the exit code is known. A zero exit
	// means the command succeeded, not that individual tests passed.
	TestEvidenceCommandOnly = "command_only"
	// TestEvidenceNone: no test evidence (no command, no tests ran, or a
	// timeout).
	TestEvidenceNone = "none"
)

// TestRunResults is deterministic test runner output.
type TestRunResults struct {
	Command    []string         `json:"command"`
	ExitCode   int              `json:"exit_code"`
	Tests      []TestCaseResult `json:"tests"`
	Stdout     string           `json:"stdout"`
	Stderr     string           `json:"stderr"`
	DurationMs int              `json:"duration_ms"`
	// Evidence is the evidence level of Tests (TestEvidence*). The empty
	// value is a result written before the field existed: consumers judge it
	// by its entries only, and never treat it as command_only.
	Evidence string `json:"evidence,omitempty"`
}

// Validate checks the test run results.
func (t TestRunResults) Validate() error {
	if len(t.Command) == 0 {
		return errors.New("command is required")
	}
	if t.DurationMs < 0 {
		return errors.New("duration_ms must be non-negative")
	}
	switch t.Evidence {
	case "", TestEvidencePerTest, TestEvidenceCommandOnly, TestEvidenceNone:
	default:
		return fmt.Errorf("invalid test evidence %q", t.Evidence)
	}
	if t.Evidence == TestEvidenceCommandOnly && t.Passed() > 0 {
		return errors.New("command_only evidence must not report passed tests")
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

// Failed returns the number of new failed or errored tests. A test that
// already failed in the pre-run baseline is not counted; see FailedBefore.
func (t TestRunResults) Failed() int {
	count := 0
	for _, tc := range t.Tests {
		if tc.failed() && !tc.AlreadyFailing {
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

// FailedBefore returns the number of failed or errored tests that already
// failed in the pre-run baseline. They are not counted by Failed.
func (t TestRunResults) FailedBefore() int {
	count := 0
	for _, tc := range t.Tests {
		if tc.failed() && tc.AlreadyFailing {
			count++
		}
	}
	return count
}

// Identity names a test across two runs. The package qualifies the name, so
// the same test name in two Go packages is two tests.
func (t TestCaseResult) Identity() string {
	return t.Package + "\x00" + t.Name
}

// failed reports whether the test failed or errored.
func (t TestCaseResult) failed() bool {
	return t.Status == "failed" || t.Status == "errored"
}

// NewFailure reports whether the test failed or errored and the run caused
// it. A failure that was already in the pre-run baseline is not new.
func (t TestCaseResult) NewFailure() bool {
	return t.failed() && !t.AlreadyFailing
}

// MarkAlreadyFailing marks each failed or errored test in results that failed
// with the same status in baseline. It returns the number of tests marked. A
// test that passed in the baseline, is absent from it, or changed its failure
// kind (errored to failed, or failed to errored) stays new.
func MarkAlreadyFailing(results *TestRunResults, baseline TestRunResults) int {
	before := make(map[string]bool, len(baseline.Tests))
	for _, tc := range baseline.Tests {
		if tc.failed() {
			before[tc.Identity()+"\x00"+tc.Status] = true
		}
	}
	marked := 0
	for i := range results.Tests {
		tc := &results.Tests[i]
		if tc.failed() && before[tc.Identity()+"\x00"+tc.Status] {
			tc.AlreadyFailing = true
			marked++
		}
	}
	return marked
}
