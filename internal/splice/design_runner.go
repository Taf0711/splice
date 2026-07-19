package splice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/agent"
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
}

// RunDesignPlan executes each task in a design plan as an independent pipeline run.
func RunDesignPlan(ctx context.Context, plan schemas.DesignPlan, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery) (agent.Result, error) {
	return RunDesignPlanWithResume(ctx, plan, provider, options, mem, rec, RunDesignPlanOptions{
		PlanID: options.SessionID,
	})
}

// RunDesignPlanWithResume executes a design plan with resume support, per-task
// callbacks, acceptance fact propagation, and a unique plan revision ID.
func RunDesignPlanWithResume(ctx context.Context, plan schemas.DesignPlan, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery, runOpts RunDesignPlanOptions) (agent.Result, error) {
	if err := plan.Validate(); err != nil {
		return agent.Result{}, fmt.Errorf("validate plan: %w", err)
	}

	planID := runOpts.PlanID
	if planID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		planID = "plan-" + hex.EncodeToString(b)
	}

	ordered, err := topologicalOrder(plan.Tasks)
	if err != nil {
		result := schemas.DesignPlanResult{
			PlanID:         planID,
			Status:         "failed",
			SkippedTaskIDs: taskIDs(plan.Tasks),
		}
		return designPlanAgentResult(options, result, err.Error())
	}

	completedSet := make(map[string]bool, len(runOpts.CompletedTaskIDs))
	for _, id := range runOpts.CompletedTaskIDs {
		completedSet[id] = true
	}

	completedOutcomes := []schemas.TaskRunOutcome{}
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
			continue
		}

		emitProgress(options, fmt.Sprintf("Starting task %d/%d %s: %s\n", nextTaskNumber, totalTasks, task.ID, task.Title))
		nextTaskNumber++

		taskPlan, acceptanceFacts, err := BuildExecutionPlanForTaskWithFacts(task)
		if err != nil {
			return agent.Result{}, fmt.Errorf("build task plan %s: %w", task.ID, err)
		}
		if err := taskPlan.Validate(); err != nil {
			return agent.Result{}, fmt.Errorf("validate task plan %s: %w", task.ID, err)
		}

		if len(acceptanceFacts) > 0 {
			taskPlan.RequestIntent = task.Intent + "\n\nAcceptance criteria:\n- " + strings.Join(acceptanceFacts, "\n- ")
		}

		pipelineResult, err := runExecutionPlan(ctx, runID, taskPlan, provider, options, mem, rec)
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
		return designPlanAgentResult(options, result, reason)
	}

	result := schemas.DesignPlanResult{
		PlanID:         planID,
		Status:         "completed",
		CompletedTasks: completedOutcomes,
	}
	return designPlanAgentResult(options, result, "")
}

func taskIDs(tasks []schemas.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func designPlanAgentResult(options agent.Options, result schemas.DesignPlanResult, reason string) (agent.Result, error) {
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
