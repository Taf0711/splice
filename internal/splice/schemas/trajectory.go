package schemas

import (
	"errors"
	"fmt"
)

// TrajectoryAction is the report-only action recommended by the trajectory monitor.
type TrajectoryAction string

const (
	ActionContinue              TrajectoryAction = "continue"
	ActionAbortHardLimit        TrajectoryAction = "abort_hard_limit"
	ActionAbortBudget           TrajectoryAction = "abort_budget"
	ActionAbortWallTime         TrajectoryAction = "abort_wall_time"
	ActionAbortNoProgress       TrajectoryAction = "abort_no_progress"
	ActionEscalateCycleDetected TrajectoryAction = "escalate_cycle_detected"
	ActionEscalateOscillation   TrajectoryAction = "escalate_oscillation"
	ActionRollback              TrajectoryAction = "rollback"
	ActionStepBack              TrajectoryAction = "step_back"
	ActionSurfaceToUser         TrajectoryAction = "surface_to_user"
)

// TestCounts splits test results into authored (written this run by the
// test generator) and preexisting (everything else). The pass/fail formula
// uses the aggregate counts; the split is additive and feeds LN3's
// authored-test discount.
type TestCounts struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Errored int `json:"errored"`
}

// Validate checks the test counts.
func (t TestCounts) Validate() error {
	if t.Pass < 0 || t.Fail < 0 || t.Errored < 0 {
		return errors.New("test counts must be non-negative")
	}
	return nil
}

// IterationState is a deterministic snapshot of where the code stands after one pipeline pass.
type IterationState struct {
	Iteration                int              `json:"iteration"`
	Timestamp                float64          `json:"timestamp"`
	TestsPassing             int              `json:"tests_passing"`
	TestsFailing             int              `json:"tests_failing"`
	TestsErrored             int              `json:"tests_errored"`
	Preexisting              TestCounts       `json:"preexisting,omitempty"`
	Authored                 TestCounts       `json:"authored,omitempty"`
	AcceptanceFactsPassing   int              `json:"acceptance_facts_passing"`
	AcceptanceFactsFailing   int              `json:"acceptance_facts_failing"`
	LintIssuesBySeverity     map[Severity]int `json:"lint_issues_by_severity,omitempty"`
	SecurityIssuesBySeverity map[Severity]int `json:"security_issues_by_severity,omitempty"`
	CodeSizeBytes            int              `json:"code_size_bytes"`
	VerificationIncomplete   int              `json:"verification_incomplete"`
	StateHash                string           `json:"state_hash"`
	Confidence               float64          `json:"confidence"`
	TokensConsumed           int              `json:"tokens_consumed"`
	// TokensGenerated is the output-side spend (completion + reasoning) for
	// the iteration. Trajectory budget gating keys on this field: input
	// volume is bounded per call by stage-input compaction, not by abort.
	TokensGenerated int      `json:"tokens_generated"`
	FilesChanged    []string `json:"files_changed,omitempty"`
	LinesAdded      int      `json:"lines_added"`
	LinesRemoved    int      `json:"lines_removed"`
	// PassChanged reports whether the workspace content changed since the
	// previous judged pass. FilesChanged and the line counts compare with
	// HEAD, so an earlier pass's edits counted as progress on every later
	// pass (review F7). Nil means unknown (older traces).
	PassChanged *bool `json:"pass_changed,omitempty"`
}

// Validate checks the iteration state.
func (i IterationState) Validate() error {
	if i.Iteration < 0 {
		return errors.New("iteration must be >= 0")
	}
	if i.StateHash == "" {
		return errors.New("state_hash is required")
	}
	if err := validateConfidence(i.Confidence); err != nil {
		return err
	}
	if i.TestsPassing < 0 || i.TestsFailing < 0 || i.TestsErrored < 0 {
		return errors.New("test counts must be non-negative")
	}
	if err := i.Preexisting.Validate(); err != nil {
		return fmt.Errorf("preexisting: %w", err)
	}
	if err := i.Authored.Validate(); err != nil {
		return fmt.Errorf("authored: %w", err)
	}
	if i.AcceptanceFactsPassing < 0 || i.AcceptanceFactsFailing < 0 {
		return errors.New("acceptance fact counts must be non-negative")
	}
	if i.CodeSizeBytes < 0 {
		return errors.New("code_size_bytes must be non-negative")
	}
	if i.VerificationIncomplete < 0 {
		return errors.New("verification_incomplete must be non-negative")
	}
	if i.TokensConsumed < 0 {
		return errors.New("tokens_consumed must be non-negative")
	}
	if i.LinesAdded < 0 || i.LinesRemoved < 0 {
		return errors.New("line counts must be non-negative")
	}
	for sev := range i.LintIssuesBySeverity {
		switch sev {
		case SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		default:
			return fmt.Errorf("invalid lint severity %q", sev)
		}
	}
	for sev := range i.SecurityIssuesBySeverity {
		switch sev {
		case SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		default:
			return fmt.Errorf("invalid security severity %q", sev)
		}
	}
	return nil
}

// IterationRecord is a compact summary of one iteration.
type IterationRecord struct {
	Iteration int    `json:"iteration"`
	Summary   string `json:"summary"`
	TestDelta int    `json:"test_delta"`
	Outcome   string `json:"outcome"` // improvement, regression, no_change
}

// Validate checks the iteration record.
func (i IterationRecord) Validate() error {
	if i.Iteration < 1 {
		return errors.New("iteration must be >= 1")
	}
	if i.Summary == "" {
		return errors.New("summary is required")
	}
	switch i.Outcome {
	case "improvement", "regression", "no_change":
	default:
		return fmt.Errorf("invalid outcome %q", i.Outcome)
	}
	return nil
}

// FailureReport is structured output when the iteration loop aborts.
type FailureReport struct {
	OriginalIntent string            `json:"original_intent"`
	Attempts       []IterationRecord `json:"attempts"`
	Hypothesis     string            `json:"hypothesis"`
	Options        []string          `json:"options"`
}

// Validate checks the failure report.
func (f FailureReport) Validate() error {
	if f.OriginalIntent == "" {
		return errors.New("original_intent is required")
	}
	if f.Hypothesis == "" {
		return errors.New("hypothesis is required")
	}
	if len(f.Options) == 0 {
		return errors.New("at least one option is required")
	}
	for i, opt := range f.Options {
		if opt == "" {
			return fmt.Errorf("options[%d] must not be empty", i)
		}
	}
	for i, attempt := range f.Attempts {
		if err := attempt.Validate(); err != nil {
			return fmt.Errorf("attempts[%d]: %w", i, err)
		}
	}
	return nil
}

// TrajectoryDecision is a pure report from evaluating an iteration-state history.
type TrajectoryDecision struct {
	Action         TrajectoryAction `json:"action"`
	Reason         string           `json:"reason"`
	IterationCount int              `json:"iteration_count"`
	CurrentScore   *float64         `json:"current_score,omitempty"`
	InitialScore   *float64         `json:"initial_score,omitempty"`
	Evidence       []string         `json:"evidence,omitempty"`
}

// Validate checks the trajectory decision.
func (t TrajectoryDecision) Validate() error {
	switch t.Action {
	case ActionContinue, ActionAbortHardLimit, ActionAbortBudget, ActionAbortWallTime, ActionAbortNoProgress,
		ActionEscalateCycleDetected, ActionEscalateOscillation, ActionRollback, ActionStepBack, ActionSurfaceToUser:
	default:
		return fmt.Errorf("invalid action %q", t.Action)
	}
	if t.Reason == "" {
		return errors.New("reason is required")
	}
	if t.IterationCount < 0 {
		return errors.New("iteration_count must be >= 0")
	}
	return nil
}

// TrajectoryStep contains a bounded change summary and failure text.
type TrajectoryStep struct {
	Change  string
	Failure string
	// FailureKind is the typed failure classification carried from the stage
	// records. Empty means legacy evidence that must be classified from text.
	FailureKind string `json:"FailureKind,omitempty"`
}

// TrajectoryFailure records the failure at one boundary.
type TrajectoryFailure struct {
	Iteration   int    `json:"iteration"`
	Repair      int    `json:"repair"`
	Kind        string `json:"kind"`
	Signature   string `json:"signature"`
	SignatureID int    `json:"signature_id"`
	IsNew       bool   `json:"is_new"`
}

// Validate checks the failure kind and counters.
func (f TrajectoryFailure) Validate() error {
	if f.Iteration < 0 || f.Repair < 0 || f.SignatureID < 0 {
		return fmt.Errorf("trajectory failure: negative counter: iteration=%d repair=%d signature_id=%d", f.Iteration, f.Repair, f.SignatureID)
	}
	switch f.Kind {
	case "stage_error", "test_failure", "none":
		return nil
	default:
		return fmt.Errorf("trajectory failure: unknown kind %q", f.Kind)
	}
}

// TrajectoryBoundary contains the evidence for one host decision.
type TrajectoryBoundary struct {
	RunID, ProjectID, Intent string
	Iteration, Repair        int
	History                  []IterationState
	Previous, Latest         *TrajectoryStep
	Rules                    TrajectoryDecision
	StepBackUsed, Escalated  bool
	RollbackTarget           bool
	IsolatedWorktree         bool `json:"-"`
	// WriterStage is the plan name of the code writer, or "pass" when the plan
	// has no code writer. Empty means the builtin name. It labels the snapshot
	// and is never serialized.
	WriterStage string `json:"-"`
	// EvidenceShadow is the other builder's pass evidence for each History
	// entry. It is never serialized: the classifier input must not change.
	EvidenceShadow            []PassEvidence      `json:"-"`
	FailureHistory            []TrajectoryFailure `json:"failure_history"`
	BoundariesSinceNewFailure int                 `json:"boundaries_since_new_failure"`
	DistinctFailures          int                 `json:"distinct_failures"`
}

// Validate checks the boundary counters and failure history.
func (b TrajectoryBoundary) Validate() error {
	if b.Iteration < 0 || b.Repair < 0 {
		return fmt.Errorf("trajectory boundary: negative iteration or repair")
	}
	if b.BoundariesSinceNewFailure < 0 || b.DistinctFailures < 0 || len(b.FailureHistory) > 8 {
		return fmt.Errorf("trajectory boundary: invalid failure history: boundaries_since_new_failure=%d distinct_failures=%d entries=%d", b.BoundariesSinceNewFailure, b.DistinctFailures, len(b.FailureHistory))
	}
	for _, failure := range b.FailureHistory {
		if err := failure.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// TrajectorySnapshot adds computed scores and hash repeats to a boundary.
type TrajectorySnapshot struct {
	Boundary    TrajectoryBoundary `json:"boundary"`
	Scores      []float64          `json:"scores"`
	HashRepeats map[string]int     `json:"hash_repeats"`
}

// Validate checks the boundary counters.
func (s TrajectorySnapshot) Validate() error { return s.Boundary.Validate() }

// PassEvidence is the workspace change one pipeline pass produced, as one
// builder measured it. Builder is "head" (change since HEAD) or "pass"
// (change since the end of the previous pass). It is evidence beside the
// IterationState, never inside it, so classifier input does not change.
type PassEvidence struct {
	Iteration      int      `json:"iteration"`
	Builder        string   `json:"builder"`
	PassChanged    *bool    `json:"pass_changed,omitempty"`
	FilesChanged   []string `json:"files_changed,omitempty"`
	LinesAdded     int      `json:"lines_added"`
	LinesRemoved   int      `json:"lines_removed"`
	Truncated      bool     `json:"truncated,omitempty"`
	DegradedReason string   `json:"degraded_reason,omitempty"`
	// Nodes is the per-node attribution of this pass, in run order (P2.2).
	Nodes []NodeEvidence `json:"nodes,omitempty"`
	// Unclaimed lists the files the pass builder saw that no node reported.
	Unclaimed []string `json:"unclaimed,omitempty"`
	// ClaimedUnchanged lists the files a node reported that the pass builder
	// did not see change.
	ClaimedUnchanged []string `json:"claimed_unchanged,omitempty"`
}

// Closed failure kinds of a node outcome.
const (
	NodeFailureStageError  = "stage_error"
	NodeFailureTestFailure = "test_failure"
	NodeFailureNone        = "none"
)

// NodeOutcome is a node's result in one pass: its stage status and the closed
// failure kind.
type NodeOutcome struct {
	Status  StageStatus `json:"status"`
	Failure string      `json:"failure"`
}

// NodeEvidence is one stage node's view of one pass: the files it reported
// changed and its outcome. It is evidence beside the IterationState, never
// inside it.
type NodeEvidence struct {
	Node         string      `json:"node"`
	Iteration    int         `json:"iteration"`
	ChangedFiles []string    `json:"changed_files,omitempty"`
	Outcome      NodeOutcome `json:"outcome"`
}
