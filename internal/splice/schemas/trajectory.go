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
	ActionEscalateCycleDetected TrajectoryAction = "escalate_cycle_detected"
	ActionEscalateOscillation   TrajectoryAction = "escalate_oscillation"
	ActionRollback              TrajectoryAction = "rollback"
	ActionStepBack              TrajectoryAction = "step_back"
	ActionSurfaceToUser         TrajectoryAction = "surface_to_user"
)

// IterationState is a deterministic snapshot of where the code stands after one pipeline pass.
type IterationState struct {
	Iteration                int              `json:"iteration"`
	Timestamp                float64          `json:"timestamp"`
	TestsPassing             int              `json:"tests_passing"`
	TestsFailing             int              `json:"tests_failing"`
	TestsErrored             int              `json:"tests_errored"`
	LintIssuesBySeverity     map[Severity]int `json:"lint_issues_by_severity,omitempty"`
	SecurityIssuesBySeverity map[Severity]int `json:"security_issues_by_severity,omitempty"`
	TypeErrors               int              `json:"type_errors"`
	CodeSizeBytes            int              `json:"code_size_bytes"`
	VerificationIncomplete   int              `json:"verification_incomplete"`
	StateHash                string           `json:"state_hash"`
	Confidence               float64          `json:"confidence"`
	TokensConsumed           int              `json:"tokens_consumed"`
	FilesChanged             []string         `json:"files_changed,omitempty"`
	LinesAdded               int              `json:"lines_added"`
	LinesRemoved             int              `json:"lines_removed"`
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
	if i.TypeErrors < 0 || i.CodeSizeBytes < 0 {
		return errors.New("type_errors and code_size_bytes must be non-negative")
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
	case ActionContinue, ActionAbortHardLimit, ActionAbortBudget, ActionAbortWallTime,
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
