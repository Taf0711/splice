package schemas

import (
	"errors"
	"fmt"
)

// AcceptanceFact is one machine-checkable acceptance criterion for a task.
type AcceptanceFact struct {
	Statement                        string  `json:"statement"`
	AutomatedVerification            bool    `json:"automated_verification"`
	VerificationCommand              *string `json:"verification_command,omitempty"`
	RecommendedAutomatedVerification bool    `json:"recommended_automated_verification"`
}

// Validate checks the acceptance fact.
func (a AcceptanceFact) Validate() error {
	if a.Statement == "" {
		return errors.New("statement is required")
	}
	if a.AutomatedVerification && (a.VerificationCommand == nil || *a.VerificationCommand == "") {
		return errors.New("automated_verification true requires verification_command")
	}
	return nil
}

// Task is one unit of work.
type Task struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Intent          string           `json:"intent"`
	AcceptanceFacts []AcceptanceFact `json:"acceptance_facts,omitempty"`
	TargetPaths     []string         `json:"target_paths,omitempty"`
	DependsOn       []string         `json:"depends_on,omitempty"`
	EstimatedTier   *PipelineTier    `json:"estimated_tier,omitempty"`
}

// Validate checks the task.
func (t Task) Validate() error {
	if t.ID == "" {
		return errors.New("id is required")
	}
	if t.Title == "" {
		return errors.New("title is required")
	}
	if t.Intent == "" {
		return errors.New("intent is required")
	}
	if len(t.Intent) > 320 {
		return errors.New("intent must be <= 320 chars")
	}
	if t.EstimatedTier != nil {
		switch *t.EstimatedTier {
		case TierTrivial, TierLight, TierStandard, TierSubstantial, TierArchitectural:
		default:
			return fmt.Errorf("invalid estimated_tier %q", *t.EstimatedTier)
		}
	}
	for i, f := range t.AcceptanceFacts {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("acceptance_facts[%d]: %w", i, err)
		}
	}
	return nil
}

// Critique is one issue raised by the adversarial plan critic.
type Critique struct {
	Category            string   `json:"category"`
	Severity            Severity `json:"severity"`
	Issue               string   `json:"issue"`
	SuggestedMitigation string   `json:"suggested_mitigation,omitempty"`
}

// CritiqueCategories returns the category values a critique may carry. It is
// the single source of truth shared by Critique.Validate and the plan_critic
// tool schema, so the machine contract given to the model cannot drift from
// the runtime validator.
func CritiqueCategories() []string {
	return []string{"scalability", "security", "maintainability", "complexity", "operability", "correctness"}
}

// Validate checks the critique.
func (c Critique) Validate() error {
	if c.Issue == "" {
		return errors.New("issue is required")
	}
	switch c.Severity {
	case SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
	default:
		return fmt.Errorf("invalid severity %q", c.Severity)
	}
	categoryValid := false
	for _, category := range CritiqueCategories() {
		if c.Category == category {
			categoryValid = true
			break
		}
	}
	if !categoryValid {
		return fmt.Errorf("invalid category %q", c.Category)
	}
	return nil
}

// PlanCritique is the review output of a concrete DesignPlan.
type PlanCritique struct {
	Critiques            []Critique `json:"critiques,omitempty"`
	CrossCuttingConcerns []string   `json:"cross_cutting_concerns,omitempty"`
	// MustFixBeforeExecution must be true exactly when a critique has high or
	// critical severity. Medium, low, info, and empty critique sets cannot block.
	MustFixBeforeExecution bool   `json:"must_fix_before_execution"`
	OverallAssessment      string `json:"overall_assessment"`
}

// Validate checks the plan critique.
func (p PlanCritique) Validate() error {
	if p.OverallAssessment == "" {
		return errors.New("overall_assessment is required")
	}
	if err := p.ValidateStructure(); err != nil {
		return err
	}
	qualifies := false
	for _, c := range p.Critiques {
		if c.Severity == SeverityHigh || c.Severity == SeverityCritical {
			qualifies = true
			break
		}
	}
	if p.MustFixBeforeExecution != qualifies {
		return fmt.Errorf("must_fix_before_execution=%t does not match highest critique severity; high or critical severity requires true and lower or empty severity requires false", p.MustFixBeforeExecution)
	}
	return nil
}

// ValidateStructure checks persisted critique fields without applying the
// current severity-to-blocking rule. It keeps older session history readable.
func (p PlanCritique) ValidateStructure() error {
	for i, c := range p.Critiques {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("critiques[%d]: %w", i, err)
		}
	}
	return nil
}

// TaskRunStatus is per-task execution status persisted alongside a DesignPlan,
// including in-flight states. It backs DesignState.TaskOutcomes, which a
// reconstructed design session reads to tell a task that started but never
// reached a terminal event apart from one that never started at all.
type TaskRunStatus struct {
	TaskID string  `json:"task_id"`
	Status string  `json:"status"` // pending, running, completed, failed, aborted
	RunID  *string `json:"run_id,omitempty"`
}

// Validate checks the task run status.
func (t TaskRunStatus) Validate() error {
	if t.TaskID == "" {
		return errors.New("task_id is required")
	}
	switch t.Status {
	case "pending", "running", "completed", "failed", "aborted":
	default:
		return fmt.Errorf("invalid status %q", t.Status)
	}
	return nil
}

// TaskRunOutcome is one task's final result within a plan-level run.
type TaskRunOutcome struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
	Status string `json:"status"` // completed, failed, aborted
}

// Validate checks the task outcome.
func (t TaskRunOutcome) Validate() error {
	if t.TaskID == "" {
		return errors.New("task_id is required")
	}
	if t.RunID == "" {
		return errors.New("run_id is required")
	}
	switch t.Status {
	case "completed", "failed", "aborted":
	default:
		return fmt.Errorf("invalid status %q", t.Status)
	}
	return nil
}

// TaskLifecycleCallback is invoked after each task completes or fails. The
// TUI uses it for live progress; the runner uses it before starting the next
// task. pipelineResult is the full result of the task's pipeline run.
type TaskLifecycleCallback func(task Task, runID string, pipelineResult PipelineResult)

// TaskStartCallback is invoked immediately before a task's pipeline run
// dispatches, before that task's TaskLifecycleCallback can fire. Not invoked
// for tasks skipped as already complete on resume.
type TaskStartCallback func(task Task, runID string)

// DesignPlanResult is the plan-level result returned by run_design_plan.
type DesignPlanResult struct {
	PlanID         string           `json:"plan_id"`
	Status         string           `json:"status"` // completed, failed
	CompletedTasks []TaskRunOutcome `json:"completed_tasks,omitempty"`
	FailedTask     *TaskRunOutcome  `json:"failed_task,omitempty"`
	SkippedTaskIDs []string         `json:"skipped_task_ids,omitempty"`
	MergeStatus    *string          `json:"merge_status,omitempty"` // not_needed, merged, skipped, conflict, error
	MergeMessage   *string          `json:"merge_message,omitempty"`
}

// Validate checks the design plan result.
func (d DesignPlanResult) Validate() error {
	if d.PlanID == "" {
		return errors.New("plan_id is required")
	}
	switch d.Status {
	case "completed", "failed":
	default:
		return fmt.Errorf("invalid status %q", d.Status)
	}
	if d.MergeStatus != nil {
		switch *d.MergeStatus {
		case "not_needed", "merged", "skipped", "conflict", "error":
		default:
			return fmt.Errorf("invalid merge_status %q", *d.MergeStatus)
		}
	}
	for i, t := range d.CompletedTasks {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("completed_tasks[%d]: %w", i, err)
		}
	}
	if d.FailedTask != nil {
		if err := d.FailedTask.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DesignPhase is the durable phase of the design workflow, derived from
// session lifecycle events. Transient busy states (crystallizing, critic
// running, task running) are in-memory only and never persisted.
type DesignPhase string

const (
	DesignPhaseNone         DesignPhase = ""             // no design mode entered
	DesignPhaseConversation DesignPhase = "conversation" // design_mode_entered, not yet crystallized
	DesignPhaseReview       DesignPhase = "review"       // plan_crystallized, not yet approved
	DesignPhaseExecuting    DesignPhase = "executing"    // plan_approved, tasks in progress
	DesignPhaseCompleted    DesignPhase = "completed"    // all tasks reached a terminal outcome
)

// PlanRevision identifies one crystallized revision of a DesignPlan within
// a session. Re-crystallizing produces a new revision so critiques and task
// outcomes stay tied to the exact plan they reviewed.
type PlanRevision struct {
	PlanID   string `json:"plan_id"`
	Revision int    `json:"revision"`
}

// DesignPlan is the crystallized handoff artifact of the design phase.
type DesignPlan struct {
	Epic         string         `json:"epic"`
	Requirements []string       `json:"requirements"`
	InScope      []string       `json:"in_scope"`
	OutOfScope   []string       `json:"out_of_scope"`
	SystemDesign string         `json:"system_design"`
	Tasks        []Task         `json:"tasks"`
	Source       string         `json:"source"` // authored, conversation, ingested
	AuditHistory []PlanCritique `json:"audit_history,omitempty"`
}

// Validate checks the design plan including task graph integrity.
func (d DesignPlan) Validate() error {
	if d.Epic == "" {
		return errors.New("epic is required")
	}
	if len(d.Requirements) == 0 {
		return errors.New("at least one requirement is required")
	}
	if len(d.InScope) == 0 {
		return errors.New("in_scope is required")
	}
	// OutOfScope and SystemDesign are deliberately optional. A short request
	// establishes no boundaries and needs no design note, and the crystallizer
	// is told not to invent scope the conversation did not cover. Requiring
	// them here forced the model to choose between its instructions and a
	// passing validation. The only reader of both fields already treats them
	// as optional (internal/tui/design_mode.go).
	if len(d.Tasks) == 0 {
		return errors.New("at least one task is required")
	}
	switch d.Source {
	case "authored", "conversation", "ingested":
	default:
		return fmt.Errorf("invalid source %q", d.Source)
	}

	idSet := make(map[string]struct{}, len(d.Tasks))
	for i, task := range d.Tasks {
		if err := task.Validate(); err != nil {
			return fmt.Errorf("tasks[%d]: %w", i, err)
		}
		if _, exists := idSet[task.ID]; exists {
			return fmt.Errorf("duplicate task id %q", task.ID)
		}
		idSet[task.ID] = struct{}{}
	}
	for i, task := range d.Tasks {
		for _, dep := range task.DependsOn {
			if _, exists := idSet[dep]; !exists {
				return fmt.Errorf("tasks[%d] depends_on unknown task id %q", i, dep)
			}
		}
	}
	for i, c := range d.AuditHistory {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("audit_history[%d]: %w", i, err)
		}
	}
	return nil
}

// PlanCriticInput is the input to the adversarial plan critic.
type PlanCriticInput struct {
	Plan             DesignPlan    `json:"plan"`
	PreviousPlan     *DesignPlan   `json:"previous_plan,omitempty"`
	PreviousCritique *PlanCritique `json:"previous_critique,omitempty"`
	RelevantContext  []string      `json:"relevant_context,omitempty"`
	PipelineStages   []string      `json:"pipeline_stages,omitempty"`
	NextStage        string        `json:"next_stage,omitempty"`
}

// Validate checks the plan critic input.
func (p PlanCriticInput) Validate() error {
	if err := p.Plan.Validate(); err != nil {
		return err
	}
	if p.PreviousPlan != nil {
		if err := p.PreviousPlan.Validate(); err != nil {
			return fmt.Errorf("previous_plan: %w", err)
		}
	}
	if p.PreviousCritique != nil {
		if err := p.PreviousCritique.ValidateStructure(); err != nil {
			return fmt.Errorf("previous_critique: %w", err)
		}
	}
	return nil
}

// ConversationMessage is one turn of the free-form design conversation.
type ConversationMessage struct {
	Role    string `json:"role"` // user, assistant
	Content string `json:"content"`
}

// Validate checks the conversation message.
func (c ConversationMessage) Validate() error {
	if c.Content == "" {
		return errors.New("content is required")
	}
	switch c.Role {
	case "user", "assistant":
	default:
		return fmt.Errorf("invalid role %q", c.Role)
	}
	return nil
}

// DesignConversationInput is input to the design conversation agent.
type DesignConversationInput struct {
	History []ConversationMessage `json:"history"`
}

// Validate checks the design conversation input.
func (d DesignConversationInput) Validate() error {
	if len(d.History) == 0 {
		return errors.New("at least one history message is required")
	}
	for i, m := range d.History {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("history[%d]: %w", i, err)
		}
	}
	return nil
}
