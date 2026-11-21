package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/streamjson"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// execDesignRun is one --design run. It crystallizes the request, critiques
// the plan, runs one revision when the critic blocks, and then runs the plan
// the same way --plan does.
type execDesignRun struct {
	// now seeds the plan revision identity.
	now           time.Time
	store         *sessions.Store
	sessionID     string
	workspaceRoot string
	images        []zeroruntime.ImageBlock
	provider      agent.Provider
	stageResolver agent.StageModelResolver
	// primary is the default route. A design stage without a stage-models.json
	// route uses it.
	primaryName   string
	primaryModel  string
	primaryEffort string
	// options is the plan run's configuration. Its OnAttributedUsage records
	// the design stages' usage with their stage names, as a pipeline stage does.
	options   agent.Options
	memClient splicerun.MemoryStore
	recovery  splicerun.WorkspaceRecovery
	writer    *execEventWriter
	// record persists one session event, in order. The run uses its session
	// recorder, so approval and task events land in the same session as the
	// design conversation.
	record func(sessions.EventType, any)
}

// runExecDesign returns the result of the plan run. An error stops the design
// phase before any task starts. A plan that cannot run returns an incomplete
// result.
func runExecDesign(ctx context.Context, run execDesignRun) (agent.Result, error) {
	// Every route is resolved before the first request, so a bad route stops the
	// run with no provider cost. A tier fallback is reported, not refused.
	fallbacks, err := splicerun.PreflightDesignRoutes(run.options, run.stageResolver, []string{"design_crystallize", "plan_critic"})
	if err != nil {
		return agent.Result{}, err
	}
	for _, fallback := range fallbacks {
		run.writer.warning("design stage uses the run model (no stage route): " + fallback)
	}
	planID := "plan-" + strconv.FormatInt(run.now.UnixNano(), 16)
	// The request is a human action, so it uses the manual source. The design
	// agent's source applies only to transitions the agent requests.
	wf := splicerun.NewDesignWorkflow(run.store, run.sessionID, planID).
		WithPrimarySelection(run.primaryName, run.primaryModel, run.primaryEffort).
		WithSource(splicerun.DesignTransitionSourceManual)
	reporter := &designStageReporter{writer: run.writer, usage: splicerun.DesignUsageRecorder(run.options)}

	plan, critique, err := run.crystallize(ctx, wf, reporter)
	if err != nil {
		return agent.Result{}, err
	}
	revised := false
	if critique.MustFixBeforeExecution {
		// One revision. The stored critique is part of the next crystallization
		// input, so the crystallizer sees what the critic objected to.
		revised = true
		plan, critique, err = run.crystallize(ctx, wf, reporter)
		if err != nil {
			return agent.Result{}, fmt.Errorf("design revision: %w", err)
		}
	}
	resolved := !critique.MustFixBeforeExecution

	issues := len(critique.Critiques)
	tasks := make([]string, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		tasks = append(tasks, task.Title)
	}
	run.writer.designPlan(execDesignPlanSummary{
		planID:   planID,
		tasks:    tasks,
		revised:  revised,
		resolved: resolved,
		issues:   issues,
		summary:  critique.OverallAssessment,
	})

	if len(plan.Tasks) == 0 {
		result := schemas.DesignPlanResult{PlanID: planID, Status: "failed"}
		final, _ := json.MarshalIndent(result, "", "  ")
		return agent.Result{
			FinalAnswer:      string(final),
			Incomplete:       true,
			IncompleteReason: "the design plan has no task to run",
		}, nil
	}
	if !resolved {
		run.writer.warning("design critique not resolved: the plan critic still requires a fix after one revision. The plan runs anyway.")
	}
	// Approval is recorded before any task starts, as the TUI records it. The
	// lifecycle hooks then record each task in execution order, so a later
	// /resume reads the plan as executed.
	run.record(sessions.EventPlanApproved, splicerun.PlanApprovedPayload{PlanID: planID, Source: splicerun.DesignTransitionSourceManual})
	onStart := func(task schemas.Task, taskRunID string) {
		run.record(sessions.EventTaskStarted, splicerun.TaskStartedPayload{TaskID: task.ID, RunID: taskRunID})
	}
	onLifecycle := func(task schemas.Task, taskRunID string, result schemas.PipelineResult) {
		if result.Status == "completed" {
			run.record(sessions.EventTaskCompleted, splicerun.TaskCompletedPayload{TaskID: task.ID, RunID: taskRunID})
			return
		}
		run.record(sessions.EventTaskFailed, splicerun.TaskFailedPayload{TaskID: task.ID, RunID: taskRunID})
	}
	return splicerun.RunDesignPlanWithLifecycle(ctx, plan, run.provider, run.options, run.memClient, run.recovery, onStart, onLifecycle)
}

// crystallize reads the session, then crystallizes and critiques the design
// conversation. The read happens on every call, so a revision sees the plan
// and critique that the previous call persisted.
func (run execDesignRun) crystallize(ctx context.Context, wf *splicerun.DesignWorkflow, reporter *designStageReporter) (schemas.DesignPlan, schemas.PlanCritique, error) {
	events, err := run.store.ReadEvents(run.sessionID)
	if err != nil {
		return schemas.DesignPlan{}, schemas.PlanCritique{}, fmt.Errorf("read design session: %w", err)
	}
	plan, critique, err := wf.CrystallizeAndCritique(ctx, events, run.provider, run.stageResolver, reporter.streamFactory, run.workspaceRoot, run.images)
	if err != nil {
		return schemas.DesignPlan{}, schemas.PlanCritique{}, fmt.Errorf("design phase: %w", err)
	}
	return plan, critique, nil
}

// designStageReporter reports the design stages in the run output. A stage
// reports running when it starts, and completed or failed when its stream
// ends. Each request's usage goes through the run's usage path under the
// stage name.
type designStageReporter struct {
	writer *execEventWriter
	usage  func(agent.AttributedUsage)
}

func (reporter *designStageReporter) streamFactory(stageName string, selection agent.ModelSelection) zeroruntime.CollectOptions {
	reporter.writer.stage(agent.StageEvent{Name: stageName, Status: "running"})
	return zeroruntime.CollectOptions{
		OnUsageResult: func(usage zeroruntime.Usage, reported bool, reportedCostUSD *float64, requestID string, failed bool) {
			status := "completed"
			if failed {
				status = "failed"
			}
			reporter.writer.stage(agent.StageEvent{Name: stageName, Status: status})
			reporter.record(agent.AttributedUsage{
				Usage:           usage,
				UsageReported:   reported,
				ProviderName:    selection.ProviderName,
				Model:           selection.Model,
				Stage:           stageName,
				ReportedCostUSD: reportedCostUSD,
				RequestID:       requestID,
			})
		},
		OnUsageError: func(reason string) {
			reporter.writer.stage(agent.StageEvent{Name: stageName, Status: "failed", Detail: reason})
			reporter.record(agent.AttributedUsage{
				UsageReported: true,
				UsageError:    reason,
				ProviderName:  selection.ProviderName,
				Model:         selection.Model,
				Stage:         stageName,
			})
		},
	}
}

// record hands one design request to the run's usage path. A nil hook drops
// the record, because the run then has no usage sink to attribute it to.
func (reporter *designStageReporter) record(au agent.AttributedUsage) {
	if reporter.usage != nil {
		reporter.usage(au)
	}
}

// execDesignPlanSummary is what the run reports about the design phase.
type execDesignPlanSummary struct {
	planID   string
	tasks    []string
	revised  bool
	resolved bool
	issues   int
	summary  string
}

// designPlan reports the design phase: the task titles, whether one revision
// ran, and whether the critique was resolved. It is one event per run.
func (writer *execEventWriter) designPlan(summary execDesignPlanSummary) {
	switch writer.format {
	case execOutputJSON:
		writer.writeJSON(map[string]any{
			"type":              "design_plan",
			"plan_id":           summary.planID,
			"tasks":             summary.tasks,
			"revised":           summary.revised,
			"critique_resolved": summary.resolved,
			"critique_issues":   summary.issues,
			"critique_summary":  summary.summary,
		})
	case execOutputStreamJSON:
		revised, resolved, issues := summary.revised, summary.resolved, summary.issues
		writer.writeStreamJSON(streamjson.Event{
			Type:             streamjson.EventDesignPlan,
			RunID:            writer.runID,
			PlanID:           summary.planID,
			Tasks:            append([]string(nil), summary.tasks...),
			Revised:          &revised,
			CritiqueResolved: &resolved,
			CritiqueIssues:   &issues,
			CritiqueSummary:  summary.summary,
		})
	default:
		writer.writeStderr(fmt.Sprintf("[splice] design: %d task(s), revised=%t, critique resolved=%t\n", len(summary.tasks), summary.revised, summary.resolved))
		for index, title := range summary.tasks {
			writer.writeStderr(fmt.Sprintf("[splice] design task %d: %s\n", index+1, title))
		}
		if summary.summary != "" {
			writer.writeStderr("[splice] design critique: " + summary.summary + "\n")
		}
	}
}
