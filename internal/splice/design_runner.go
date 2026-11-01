package splice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/projectdocs"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// topologicalOrder returns tasks in dependency order with task-list FIFO tie breaks.
func topologicalOrder(tasks []schemas.Task) ([]schemas.Task, error) {
	byID := make(map[string]schemas.Task, len(tasks))
	inDegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
		inDegree[task.ID] = len(task.DependsOn)
		dependents[task.ID] = nil
	}
	for _, task := range tasks {
		for _, dep := range task.DependsOn {
			if _, ok := dependents[dep]; !ok {
				return nil, fmt.Errorf("task %s depends on unknown task id %s", task.ID, dep)
			}
			dependents[dep] = append(dependents[dep], task.ID)
		}
	}

	ready := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if inDegree[task.ID] == 0 {
			ready = append(ready, task.ID)
		}
	}

	ordered := make([]schemas.Task, 0, len(tasks))
	for len(ready) > 0 {
		taskID := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byID[taskID])
		for _, dependentID := range dependents[taskID] {
			inDegree[dependentID]--
			if inDegree[dependentID] == 0 {
				ready = append(ready, dependentID)
			}
		}
	}
	if len(ordered) != len(tasks) {
		unresolved := make([]string, 0, len(tasks)-len(ordered))
		for _, task := range tasks {
			if inDegree[task.ID] > 0 {
				unresolved = append(unresolved, task.ID)
			}
		}
		return nil, fmt.Errorf("dependency cycle among task ids: %s", strings.Join(unresolved, ", "))
	}
	return ordered, nil
}

// RunDesignPlanOptions configures a resumable design plan run.
type RunDesignPlanOptions struct {
	// PlanID is the unique plan revision identifier. If empty, one is generated.
	PlanID string
	// CompletedTaskIDs are tasks already completed in a prior run. They are
	// skipped without re-execution. Their dependencies are still respected.
	CompletedTaskIDs []string
	// OnTaskLifecycle is invoked after each task completes or fails, before
	// the next task starts. May be nil.
	OnTaskLifecycle schemas.TaskLifecycleCallback
	// OnTaskStart is invoked immediately before a task's pipeline run
	// dispatches. May be nil. Not invoked for tasks in CompletedTaskIDs.
	OnTaskStart schemas.TaskStartCallback
}

// RunDesignPlan executes each task in a design plan as an independent pipeline run.
func RunDesignPlan(ctx context.Context, plan schemas.DesignPlan, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery) (agent.Result, error) {
	if _, err := ResolveExpansionRounds(); err != nil {
		return agent.Result{}, runInputError{err}
	}
	cfg := PipelineConfigFromAgentOptions(options)
	return RunDesignPlanWithResume(ctx, plan, provider, options, mem, rec, RunDesignPlanOptions{
		PlanID: cfg.SessionID,
	})
}

// RunDesignPlanWithResume executes a design plan with resume support, per-task
// callbacks, acceptance fact propagation, and a unique plan revision ID.
func RunDesignPlanWithResume(ctx context.Context, plan schemas.DesignPlan, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery, runOpts RunDesignPlanOptions) (agent.Result, error) {
	return bracketRun(options, func() (agent.Result, error) {
		return runDesignPlanWithResume(ctx, plan, provider, options, mem, rec, runOpts)
	})
}

func runDesignPlanWithResume(ctx context.Context, plan schemas.DesignPlan, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery, runOpts RunDesignPlanOptions) (agent.Result, error) {
	cfg := PipelineConfigFromAgentOptions(options)
	instructionDir := cfg.Cwd
	if instructionDir == "" {
		instructionDir = cfg.ProjectRoot
	}
	cfg.ProjectInstructions = projectdocs.Load(instructionDir)
	// Workspace isolation (DoD 26): a run whose Cwd is a worktree path
	// distinct from the stable repo root is an isolated lane; the stage
	// events stamp that so the sidebar can badge the lane honestly.
	if cfg.ProjectRoot != "" && cfg.Cwd != cfg.ProjectRoot {
		cfg.IsolatedWorktree = cfg.Cwd
	}
	if err := plan.Validate(); err != nil {
		return agent.Result{}, fmt.Errorf("validate plan: %w", err)
	}

	planID := runOpts.PlanID
	if planID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		planID = "plan-" + hex.EncodeToString(b)
	}

	// The advisor config is checked once, before any task runs, and the
	// classifier state is reported once for the plan. Each task builds its
	// own advisor below, so budgets and records stay per task, as in a run.
	if err := resolveRunAdvisor(options, &cfg, false); err != nil {
		return agent.Result{}, err
	}
	classifierReported := false
	// Design task plans come from the default tier planner, so they run the
	// default topology. Say so when another pipeline is active. A topology
	// that does not resolve is not used here, so it does not stop the plan.
	if topology, source, _, err := resolveRunTopology(options); err == nil && source != TopologySourceDefault {
		emitNotice(cfg, sanitizeNoticeLine("[topology] Design plans run the default topology; the active pipeline "+topology.Name+" is not used."))
	}

	ordered, err := topologicalOrder(plan.Tasks)
	if err != nil {
		result := schemas.DesignPlanResult{
			PlanID:         planID,
			Status:         "failed",
			SkippedTaskIDs: taskIDs(plan.Tasks),
		}
		return designPlanAgentResult(cfg, result, err.Error())
	}

	completedSet := make(map[string]bool, len(runOpts.CompletedTaskIDs))
	for _, id := range runOpts.CompletedTaskIDs {
		completedSet[id] = true
	}

	// Check the node pins and stage routes of every task plan before the
	// first task runs, as Run does, so a model that cannot resolve stops the
	// plan before any provider request. A task plan that does not build is
	// reported by the task loop below.
	for _, task := range ordered {
		if completedSet[task.ID] {
			continue
		}
		taskPlan, _, err := BuildExecutionPlanForTaskWithFacts(task, cfg.Flags)
		if err != nil {
			continue
		}
		if err := preflightNodePins(taskPlan, cfg); err != nil {
			return agent.Result{}, preflightRunError(fmt.Errorf("task %s: %w", task.ID, err))
		}
		if err := preflightStageRoutes(taskPlan, cfg); err != nil {
			return agent.Result{}, preflightRunError(fmt.Errorf("task %s: %w", task.ID, err))
		}
	}

	completedOutcomes := []schemas.TaskRunOutcome{}
	// The plan is verified only when every completed task verified. The first
	// unverified task names the reason.
	unverifiedReason := ""
	noteUnverified := func(taskID, reason string) {
		if unverifiedReason != "" {
			return
		}
		if strings.TrimSpace(reason) == "" {
			reason = "completed without a passing verification check"
		}
		unverifiedReason = "task " + taskID + ": " + reason
	}
	nextTaskNumber := 1
	totalTasks := len(ordered)

	for index, task := range ordered {
		runID := planID + "-" + task.ID

		if completedSet[task.ID] {
			outcome := schemas.TaskRunOutcome{
				TaskID: task.ID,
				RunID:  runID,
				Status: "completed",
			}
			completedOutcomes = append(completedOutcomes, outcome)
			noteUnverified(task.ID, "completed in an earlier run; this run did not verify it")
			continue
		}

		emitProgress(cfg, fmt.Sprintf("Starting task %d/%d %s: %s\n", nextTaskNumber, totalTasks, task.ID, task.Title))
		nextTaskNumber++

		if runOpts.OnTaskStart != nil {
			runOpts.OnTaskStart(task, runID)
		}

		taskPlan, acceptanceFacts, err := BuildExecutionPlanForTaskWithFacts(task, cfg.Flags)
		if err != nil {
			return agent.Result{}, fmt.Errorf("build task plan %s: %w", task.ID, err)
		}
		if err := taskPlan.Validate(); err != nil {
			return agent.Result{}, fmt.Errorf("validate task plan %s: %w", task.ID, err)
		}

		if len(acceptanceFacts) > 0 {
			taskPlan.RequestIntent = task.Intent + "\n\nAcceptance criteria:\n- " + strings.Join(acceptanceFacts, "\n- ")
		}

		// Design task plans come from the default tier planner, so the default
		// topology is the one whose nodes must be resolvable here.
		taskCfg := cfg
		if err := resolveRunAdvisor(options, &taskCfg, !classifierReported); err != nil {
			return agent.Result{}, fmt.Errorf("task %s: %w", task.ID, err)
		}
		classifierReported = true
		pipelineResult, err := runExecutionPlan(ctx, runID, taskPlan, defaultTopology(), provider, taskCfg, mem, rec)
		if dir := os.Getenv(DecisionRecordsDirEnvVar); dir != "" && taskCfg.Advisor != nil {
			if werr := writeDecisionRecords(dir, runID, taskCfg.Advisor.Records()); werr != nil {
				emitProgress(taskCfg, fmt.Sprintf("[decision advisor] warning: cannot write decision records: %v\n", werr))
			}
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return agent.Result{}, context.Canceled
			}
			// Preserve the underlying error even when cancellation raced it, so
			// infrastructure failures are not masked as interrupts. The CLI's
			// interrupt handling still recognizes cancellation via ctx.Err().
			return agent.Result{}, fmt.Errorf("task %s: %w", task.ID, err)
		}

		outcome := schemas.TaskRunOutcome{
			TaskID: task.ID,
			RunID:  runID,
			Status: pipelineResult.Status,
		}
		if runOpts.OnTaskLifecycle != nil {
			runOpts.OnTaskLifecycle(task, runID, pipelineResult)
		}

		if pipelineResult.Status == "completed" {
			completedOutcomes = append(completedOutcomes, outcome)
			if !pipelineResult.Verified {
				noteUnverified(task.ID, pipelineResult.UnverifiedReason)
			}
			continue
		}
		result := schemas.DesignPlanResult{
			PlanID:         planID,
			Status:         "failed",
			CompletedTasks: completedOutcomes,
			FailedTask:     &outcome,
			SkippedTaskIDs: taskIDs(ordered[index+1:]),
		}
		reason := fmt.Sprintf("task %s stopped with status %s", task.ID, pipelineResult.Status)
		if pipelineResult.AbortReason != nil && strings.TrimSpace(*pipelineResult.AbortReason) != "" {
			reason += ": " + strings.TrimSpace(*pipelineResult.AbortReason)
		}
		return designPlanAgentResult(cfg, result, reason)
	}

	result := schemas.DesignPlanResult{
		PlanID:         planID,
		Status:         "completed",
		CompletedTasks: completedOutcomes,
	}
	if len(completedOutcomes) == 0 {
		unverifiedReason = "the plan has no task"
	}
	agentResult, err := designPlanAgentResult(cfg, result, "")
	agentResult.Verified = unverifiedReason == ""
	agentResult.UnverifiedReason = unverifiedReason
	return agentResult, err
}

func taskIDs(tasks []schemas.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func designPlanAgentResult(options PipelineRunConfig, result schemas.DesignPlanResult, reason string) (agent.Result, error) {
	if err := result.Validate(); err != nil {
		return agent.Result{}, fmt.Errorf("validate design plan result: %w", err)
	}
	finalAnswer, _ := json.MarshalIndent(result, "", "  ")
	emitText(options, designPlanCompletionSummary(result, reason))
	return agent.Result{
		FinalAnswer:      string(finalAnswer),
		Incomplete:       result.Status != "completed",
		IncompleteReason: reason,
	}, nil
}

func designPlanCompletionSummary(result schemas.DesignPlanResult, reason string) string {
	summary := fmt.Sprintf("Design plan %s after %d completed task(s).", result.Status, len(result.CompletedTasks))
	if strings.TrimSpace(reason) != "" {
		summary += " " + strings.TrimSpace(reason) + "."
	}
	return summary + "\n"
}
