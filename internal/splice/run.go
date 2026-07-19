package splice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
)

const (
	defaultMaxIterations  = 5
	defaultMaxWallSeconds = 600
)

// Run executes the Splice deterministic pipeline for a user prompt.
// It mirrors agent.Run's signature so it can be swapped in at the TUI/CLI seams.
func Run(ctx context.Context, prompt string, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery) (agent.Result, error) {
	runID := options.SessionID
	if runID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		runID = "run-" + hex.EncodeToString(b)
	}

	plan, err := BuildExecutionPlan(prompt)
	if err != nil {
		return agent.Result{}, fmt.Errorf("build plan: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return agent.Result{}, fmt.Errorf("validate plan: %w", err)
	}

	result, err := runExecutionPlan(ctx, runID, plan, provider, options, mem, rec)
	if err != nil {
		return agent.Result{}, err
	}

	finalAnswer, _ := json.MarshalIndent(result, "", "  ")
	emitText(options, completionSummary(result))
	return agent.Result{
		FinalAnswer:      string(finalAnswer),
		Incomplete:       result.Status != "completed",
		IncompleteReason: abortReason(result),
	}, nil
}

func runExecutionPlan(ctx context.Context, runID string, plan schemas.ExecutionPlan, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery) (schemas.PipelineResult, error) {
	workDir := options.Cwd
	if workDir == "" {
		workDir = "."
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("resolve work dir: %w", err)
	}

	runner := newAgentToolRunner(options, absWorkDir)

	registry, err := buildStageRegistry(provider, options, absWorkDir, runner)
	if err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("build stage registry: %w", err)
	}

	if mem != nil {
		obs := buildConfigObservation(runID, absWorkDir, plan)
		persistObservation(ctx, mem, obs, func(msg string) {
			emitProgress(options, fmt.Sprintf("[orchestrator] %s", msg))
		})
	}

	result, err := runIterationLoop(ctx, runID, plan, registry, provider, options, absWorkDir, runner, mem, rec)
	if err != nil {
		return schemas.PipelineResult{}, err
	}
	if err := result.Validate(); err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("validate pipeline result: %w", err)
	}
	return result, nil
}

func runIterationLoop(
	ctx context.Context,
	runID string,
	plan schemas.ExecutionPlan,
	registry stageRegistry,
	provider agent.Provider,
	options agent.Options,
	workDir string,
	runner ToolRunner,
	mem MemoryStore,
	rec WorkspaceRecovery,
) (schemas.PipelineResult, error) {
	maxWallSeconds := defaultMaxWallSeconds
	tokenBudget := plan.TokenBudget.TotalInputBudget + plan.TokenBudget.TotalOutputBudget

	history := []schemas.IterationState{}
	allRecords := []schemas.StageRecord{}
	wallStart := time.Now()
	var revisionContext *string

	// escalated tracks whether the escalation model resolver has been called
	// for this run. Escalation fires at most once (AR10c).
	escalated := false

	// agent.Options.MaxTurns bounds model turns in agent.Run. In the deterministic
	// pipeline, one full pipeline pass is the closest equivalent turn, so use it as
	// the iteration cap when the caller supplies it.
	maxIterations := defaultMaxIterations
	if options.MaxTurns > 0 {
		maxIterations = options.MaxTurns
	}

	// snapshots holds references to captured workspace states for rollback.
	// It is always seeded with iteration 0 (captured before the first pass)
	// when recovery is configured.
	snapshots := []snapshot{}
	if rec != nil {
		ref, captureErr := rec.Capture(ctx, runID, 0)
		if captureErr != nil {
			if errors.Is(captureErr, context.Canceled) || ctx.Err() != nil {
				return schemas.PipelineResult{}, context.Canceled
			}
			return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("capture iteration 0: %v", captureErr))
		}
		emitProgress(options, fmt.Sprintf("[recovery] captured iteration 0 snapshot %s\n", ref))
		snapshots = append(snapshots, snapshot{ref: ref, iter: 0, score: 0})
	}

	emitProgress(options, fmt.Sprintf("Starting pipeline run %s (tier %s)\n", runID, plan.Tier))

	for i := 1; i <= maxIterations; i++ {
		if time.Since(wallStart).Seconds() > float64(maxWallSeconds) {
			return finishWithReason(runID, plan, allRecords, "aborted", "wall time exceeded")
		}

		emitProgress(options, fmt.Sprintf("Starting pipeline iteration %d\n", i))
		passRecords, passOutputs, completed, err := runPass(ctx, runID, i, plan, registry, provider, options, workDir, runner, revisionContext, mem)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return schemas.PipelineResult{}, context.Canceled
			}
			return finishWithReason(runID, plan, allRecords, "failed", err.Error())
		}
		allRecords = append(allRecords, passRecords...)

		if !completed {
			if i < maxIterations {
				failed := findFailed(passRecords)
				rc := buildRevisionContext(plan.RequestIntent, history, passRecords, fmt.Sprintf("Recovery: stage failure in iteration %d: %s", i, DerefString(failed.OutputSummary)))
				revisionContext = &rc
				continue
			}
			return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("stage failed in iteration %d", i))
		}

		changeSummary := summarizeWorkspaceChanges(ctx, workDir)
		state, err := ComputeIterationState(i, passOutputs, passRecords, changeSummary, nil)
		if err != nil {
			return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("compute iteration state: %v", err))
		}
		history = append(history, state)

		// Capture the workspace state after each completed iteration so
		// rollback has a valid snapshot to restore. Errors (including
		// cancellation) stop the pipeline without retry.
		if rec != nil {
			ref, captureErr := rec.Capture(ctx, runID, i)
			if captureErr != nil {
				if errors.Is(captureErr, context.Canceled) || ctx.Err() != nil {
					return schemas.PipelineResult{}, context.Canceled
				}
				return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("capture iteration %d: %v", i, captureErr))
			}
			score := ComputeScore(state)
			emitProgress(options, fmt.Sprintf("[recovery] captured iteration %d snapshot %s (score=%.1f)\n", i, ref, score))
			snapshots = append(snapshots, snapshot{ref: ref, iter: i, score: score})
		}

		if passSucceeded(passRecords, state) {
			return finishCompleted(runID, plan, allRecords)
		}

		decision := EvaluateTrajectory(history, maxIterations, &tokenBudget)
		if decision.Action == schemas.ActionContinue {
			rc := buildRevisionContext(plan.RequestIntent, history, passRecords, "")
			revisionContext = &rc
			continue
		}
		if decision.Action == schemas.ActionRollback {
			if rec == nil {
				return finishWithReason(runID, plan, allRecords, "aborted", fmt.Sprintf("rollback requires an isolated --worktree: %s", decision.Reason))
			}
			target, ok := selectBestSnapshot(snapshots, i)
			if !ok {
				return finishWithReason(runID, plan, allRecords, "failed", "rollback requested but no workspace snapshot is available")
			}
			current := snapshots[len(snapshots)-1]
			if restoreErr := rec.Restore(ctx, current.ref, target.ref); restoreErr != nil {
				if errors.Is(restoreErr, context.Canceled) || ctx.Err() != nil {
					return schemas.PipelineResult{}, context.Canceled
				}
				return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("restore to iteration %d: %v", target.iter, restoreErr))
			}
			emitProgress(options, fmt.Sprintf("[recovery] rejected iteration %d (score=%.1f), restored iteration %d (score=%.1f)\n", i, current.score, target.iter, target.score))
			rc := buildRevisionContext(plan.RequestIntent, history, passRecords, fmt.Sprintf("Rollback: restored iteration %d at score %.1f. %s", target.iter, target.score, decision.Reason))
			revisionContext = &rc
			continue
		}
		if decision.Action == schemas.ActionStepBack {
			report := buildStepBackReport(plan.RequestIntent, history, passOutputs, decision)
			stageOpts := stageOptions("step_back", options, workDir, runner)
			analysis, sbErr := stages.StepBack(ctx, provider, stageOpts, report)
			if sbErr != nil {
				if errors.Is(sbErr, context.Canceled) || ctx.Err() != nil {
					return schemas.PipelineResult{}, context.Canceled
				}
				return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("step-back analysis: %v", sbErr))
			}
			rc := fmt.Sprintf("Step-back analysis: %s. Recommended approach: %s.", analysis.HypothesizedRootCause, analysis.RecommendedApproach)
			revisionContext = &rc
			emitProgress(options, fmt.Sprintf("[step-back] root cause: %s", truncateString(analysis.HypothesizedRootCause, 100)))
			continue
		}
		if decision.Action == schemas.ActionEscalateCycleDetected || decision.Action == schemas.ActionEscalateOscillation {
			if !escalated {
				if options.EscalationModelResolver != nil {
					escalated = true // do not retry: escalation fires at most once per run
					escProvider, escModel, escEffort, escErr := options.EscalationModelResolver()
					if escErr != nil {
						emitProgress(options, fmt.Sprintf("[escalation] resolver error: %v (continuing without escalation)\n", escErr))
					} else if escProvider == nil {
						emitProgress(options, "[escalation] no escalation provider configured (continuing without escalation)\n")
					} else {
						provider = escProvider
						options.Model = escModel
						options.ReasoningEffort = escEffort
						emitProgress(options, fmt.Sprintf("[escalation] switched to model %s for iteration %d\n", escModel, i+1))
					}
				} else {
					escalated = true
					emitProgress(options, "[escalation] no EscalationModelResolver configured (continuing without escalation)\n")
				}
			}
			rc := buildRevisionContext(plan.RequestIntent, history, passRecords, fmt.Sprintf("Recovery: %s — %s", decision.Action, decision.Reason))
			revisionContext = &rc
			continue
		}
		if decision.Action == schemas.ActionSurfaceToUser {
			if options.OnSurfaceToUser == nil {
				return finishWithReason(runID, plan, allRecords, "aborted", fmt.Sprintf("surface_to_user: %s (no interactive callback; aborting)", decision.Reason))
			}

			recentConfidences := make([]float64, 0, 3)
			for _, st := range history[max(0, len(history)-3):] {
				recentConfidences = append(recentConfidences, st.Confidence)
			}

			req := agent.SurfaceToUserRequest{
				RunID:             runID,
				Iteration:         i,
				Reason:            decision.Reason,
				Evidence:          decision.Evidence,
				RecentConfidences: recentConfidences,
				CurrentScore:      decision.CurrentScore,
				InitialScore:      decision.InitialScore,
			}

			userDecision, cbErr := options.OnSurfaceToUser(ctx, req)
			if cbErr != nil {
				if errors.Is(cbErr, context.Canceled) || ctx.Err() != nil {
					return schemas.PipelineResult{}, context.Canceled
				}
				return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("surface_to_user callback: %v", cbErr))
			}

			switch userDecision.Action {
			case agent.SurfaceToUserAbort:
				msg := "user aborted: " + userDecision.Message
				return finishWithReason(runID, plan, allRecords, "aborted", msg)
			case agent.SurfaceToUserContinue:
				rc := userDecision.Message
				revisionContext = &rc
				emitProgress(options, fmt.Sprintf("[surface-to-user] user guidance: %s", userDecision.Message))
				continue
			default:
				return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("unexpected surface_to_user action: %s", userDecision.Action))
			}
		}
		return finishWithReason(runID, plan, allRecords, "aborted", fmt.Sprintf("%s: %s", decision.Action, decision.Reason))
	}

	return finishWithReason(runID, plan, allRecords, "aborted", fmt.Sprintf("reached max iterations (%d) without success", maxIterations))
}

// isModelFreeStage reports whether a pipeline stage runs deterministically
// without a provider. F14a: static analysis, security audit, and test execution
// are model-free. Every other stage (including unknown/custom stage names) keeps
// the current model-backed default so test and extension seams do not silently
// lose their provider.
func isModelFreeStage(name string) bool {
	switch name {
	case "static_analyzer", "security_auditor", "test_runner":
		return true
	default:
		return false
	}
}

func runPass(
	ctx context.Context,
	runID string,
	iteration int,
	plan schemas.ExecutionPlan,
	registry stageRegistry,
	provider agent.Provider,
	options agent.Options,
	workDir string,
	runner ToolRunner,
	revisionContext *string,
	mem MemoryStore,
) ([]schemas.StageRecord, []schemas.HarnessStageOutput, bool, error) {
	priorSummaries := map[string]string{}
	records := []schemas.StageRecord{}
	outputs := []schemas.HarnessStageOutput{}

	for seq, stage := range plan.Stages {
		stageName := stage.Name
		agentStage, ok := registry[stageName]
		if !ok {
			if stage.Budget.Skippable {
				summary := fmt.Sprintf("Stage skipped: no configured agent for %s", stageName)
				records = append(records, schemas.StageRecord{
					Name:          stageName,
					Status:        schemas.StageSkipped,
					Iteration:     iteration,
					OutputSummary: &summary,
				})
				emitStageEvent(options, stageName, "skipped", summary, 0, nil)
				continue
			}
			summary := fmt.Sprintf("Stage unavailable: %s has no configured agent", stageName)
			records = append(records, schemas.StageRecord{
				Name:          stageName,
				Status:        schemas.StageFailed,
				Iteration:     iteration,
				OutputSummary: &summary,
			})
			return records, outputs, false, nil
		}

		input := schemas.HarnessStageInput{
			RunID:           runID,
			StageName:       stageName,
			Sequence:        seq + 1,
			PlanTier:        plan.Tier,
			RequestIntent:   plan.RequestIntent,
			PriorSummaries:  maps.Clone(priorSummaries),
			RevisionContext: revisionContext,
		}

		if mem != nil {
			bundle, mErr := mem.Search(ctx, newMemoryQuery(stageName, plan.RequestIntent, workDir))
			if mErr != nil {
				emitProgress(options, fmt.Sprintf("[%s] memory retrieval skipped: %v\n", stageName, mErr))
			} else {
				bundle.RequestingAgent = stageName
				input.MemoryBundle = &bundle
			}
		}

		if err := input.Validate(); err != nil {
			return records, outputs, false, fmt.Errorf("stage %s input: %w", stageName, err)
		}

		emitProgress(options, fmt.Sprintf("[%s] stage started\n", stageName))
		emitStageEvent(options, stageName, "running", "", 0, nil)

		// Model-free stages skip provider resolution and attribution.
		modelFree := isModelFreeStage(stageName)
		stageProvider := provider
		stageModel := options.Model
		stageEffort := options.ReasoningEffort
		if options.StageModelResolver != nil && !modelFree {
			resolved, model, effort, rerr := options.StageModelResolver(stageName)
			if rerr != nil {
				emitProgress(options, fmt.Sprintf("[%s] stage model resolution failed: %v\n", stageName, rerr))
			} else if resolved != nil {
				stageProvider = resolved
				stageModel = model
				stageEffort = effort
			}
		}
		if modelFree {
			stageProvider = nil
			stageModel = ""
			stageEffort = ""
		}

		start := time.Now()
		output, err := runStageWithContext(ctx, input, agentStage, stageProvider, stageModel, stageEffort, options, workDir, runner, mem)
		latencyMs := int(time.Since(start).Milliseconds())
		emitProgress(options, fmt.Sprintf("[%s] stage finished\n", stageName))
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return records, outputs, false, context.Canceled
		}

		record := schemas.StageRecord{
			Name:      stageName,
			Iteration: iteration,
			LatencyMs: latencyMs,
		}
		if !modelFree {
			if stageModel != "" {
				record.Model = Ptr(stageModel)
			}
			if options.ProviderName != "" {
				record.Provider = Ptr(options.ProviderName)
			}
		}
		if err != nil {
			record.Status = schemas.StageFailed
			var metered interface{ StageUsage() *schemas.StageUsage }
			if errors.As(err, &metered) {
				applyStageUsage(&record, metered.StageUsage())
			}
			summary := fmt.Sprintf("%T: %v", err, err)
			record.OutputSummary = &summary
			records = append(records, record)
			emitStageEvent(options, stageName, "failed", summary, 0, nil)
			return records, outputs, false, nil
		}
		if output.ContextRequest != nil {
			return records, outputs, false, fmt.Errorf("stage %s requested context twice", stageName)
		}
		if err := output.Validate(); err != nil {
			record.Status = schemas.StageFailed
			record.LatencyMs = latencyMs
			failSummary := fmt.Sprintf("invalid stage output: %v", err)
			record.OutputSummary = &failSummary
			records = append(records, record)
			emitStageEvent(options, stageName, "failed", failSummary, 0, nil)
			return records, outputs, false, nil
		}
		record.Status = schemas.StageCompleted
		if isVerificationIncompleteOutput(output) {
			record.Status = schemas.StageIncomplete
		}
		record.Confidence = &output.Confidence
		applyStageUsage(&record, output.Usage)
		summary := SummarizeStageOutput(stageName, output)
		record.OutputSummary = &summary
		records = append(records, record)
		if record.Status == schemas.StageIncomplete {
			emitStageEvent(options, stageName, "incomplete", summary, 0, nil)
		} else {
			emitStageEvent(options, stageName, "completed", summary, 100, stageChangedFiles(output))
		}
		for _, obs := range extractWriteObservations(stageName, runID, workDir, output) {
			persistObservation(ctx, mem, obs, func(msg string) {
				emitProgress(options, fmt.Sprintf("[%s] %s", stageName, msg))
			})
		}
		priorSummaries[stageName] = *record.OutputSummary
		outputs = append(outputs, output)
	}

	return records, outputs, true, nil
}

func applyStageUsage(record *schemas.StageRecord, usage *schemas.StageUsage) {
	if record == nil || usage == nil {
		return
	}
	record.TokensInput = usage.InputTokens
	record.TokensOutput = usage.OutputTokens
	record.TokensCached = usage.CachedInputTokens
	record.TokensCacheWrite = usage.CacheWriteTokens
	record.CostUSD = usage.CostUSD
}

func runStageWithContext(
	ctx context.Context,
	input schemas.HarnessStageInput,
	stage stages.Stage,
	provider agent.Provider,
	modelOverride string,
	reasoningEffort string,
	options agent.Options,
	workDir string,
	runner ToolRunner,
	mem MemoryStore,
) (schemas.HarnessStageOutput, error) {
	stageOpts := stageOptions(input.StageName, options, workDir, runner)
	stageOpts.ModelOverride = modelOverride
	stageOpts.ReasoningEffort = reasoningEffort
	output, err := stage.Run(ctx, input, provider, stageOpts)
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	if output.ContextRequest == nil {
		return output, nil
	}

	bundle, err := FulfillContextRequest(ctx, *output.ContextRequest, runner)
	if err != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("fulfill context: %w", err)
	}
	input.Context = &bundle
	if mem != nil {
		for _, obs := range extractDegradationObservations(input.StageName, input.RunID, workDir, bundle) {
			persistObservation(ctx, mem, obs, func(msg string) {
				emitProgress(options, fmt.Sprintf("[%s] %s", input.StageName, msg))
			})
		}
	}
	finalOutput, err := stage.Run(ctx, input, provider, stageOpts)
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	if finalOutput.ContextRequest != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("stage requested context more than once")
	}
	finalOutput.Usage = mergeStageUsage(output.Usage, finalOutput.Usage)
	return finalOutput, nil
}

func passSucceeded(records []schemas.StageRecord, state schemas.IterationState) bool {
	for _, r := range records {
		if r.Status == schemas.StageFailed {
			return false
		}
	}
	if state.TestsFailing > 0 || state.TestsErrored > 0 {
		return false
	}
	if state.LintIssuesBySeverity[schemas.SeverityCritical] > 0 || state.LintIssuesBySeverity[schemas.SeverityHigh] > 0 {
		return false
	}
	if state.SecurityIssuesBySeverity[schemas.SeverityCritical] > 0 || state.SecurityIssuesBySeverity[schemas.SeverityHigh] > 0 {
		return false
	}
	return true
}

// isVerificationIncompleteOutput checks whether a stage output carries a
// VerificationReport with incomplete status. This lets the orchestrator
// record StageIncomplete instead of StageCompleted for deterministic stages
// whose required checks could not run.
func isVerificationIncompleteOutput(output schemas.HarnessStageOutput) bool {
	for _, key := range []string{"static_analyzer_output", "security_auditor_output"} {
		if report, ok := output.Data[key].(schemas.VerificationReport); ok {
			if report.Status == schemas.VerificationIncomplete {
				return true
			}
		}
	}
	return false
}

func findFailed(records []schemas.StageRecord) schemas.StageRecord {
	for _, r := range records {
		if r.Status == schemas.StageFailed {
			return r
		}
	}
	return records[len(records)-1]
}

func buildRevisionContext(intent string, history []schemas.IterationState, records []schemas.StageRecord, note string) string {
	lines := []string{fmt.Sprintf("Original intent: %s", intent), "", "Iteration history:"}
	for _, state := range history {
		lines = append(lines, fmt.Sprintf("  iter %d: tests_passing=%d tests_failing=%d tests_errored=%d score=%.1f",
			state.Iteration, state.TestsPassing, state.TestsFailing, state.TestsErrored, ComputeScore(state)))
	}
	failed := []schemas.StageRecord{}
	for _, r := range records {
		if r.Status == schemas.StageFailed {
			failed = append(failed, r)
		}
	}
	if len(failed) > 0 {
		lines = append(lines, "", "Last-pass failures:")
		for _, r := range failed {
			lines = append(lines, fmt.Sprintf("  %s: %s", r.Name, DerefString(r.OutputSummary)))
		}
	}
	if note != "" {
		lines = append(lines, "", note)
	}
	return strings.Join(lines, "\n")
}

func buildStepBackReport(intent string, history []schemas.IterationState, passOutputs []schemas.HarnessStageOutput, decision schemas.TrajectoryDecision) stages.StepBackReport {
	report := stages.StepBackReport{
		Intent:       intent,
		RecentScores: make([]float64, 0, 3),
		Reason:       decision.Reason,
	}
	// Take the last 3 scores.
	start := 0
	if len(history) > 3 {
		start = len(history) - 3
	}
	for _, st := range history[start:] {
		report.RecentScores = append(report.RecentScores, ComputeScore(st))
		report.ChangedFiles = append(report.ChangedFiles, st.FilesChanged...)
	}
	// Also grab failing test names from the last pass output.
	for _, out := range passOutputs {
		if results, ok := out.Data["test_results"]; ok {
			if tr, ok := results.(schemas.TestRunResults); ok {
				for _, tc := range tr.Tests {
					if tc.Status == "failed" || tc.Status == "errored" {
						report.FailingTests = append(report.FailingTests, tc.Name)
					}
				}
			}
		}
		// Get changed files from code_writer_output if present.
		if cw, ok := out.Data["code_writer_output"]; ok {
			if cwo, ok := cw.(schemas.CodeWriterOutput); ok {
				for _, f := range cwo.Files {
					report.ChangedFiles = append(report.ChangedFiles, f.Path)
				}
			}
		}
	}
	// Deduplicate.
	seen := map[string]bool{}
	uniq := make([]string, 0, len(report.FailingTests))
	for _, s := range report.FailingTests {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	report.FailingTests = uniq
	seen2 := map[string]bool{}
	uniq2 := make([]string, 0, len(report.ChangedFiles))
	for _, s := range report.ChangedFiles {
		if !seen2[s] {
			seen2[s] = true
			uniq2 = append(uniq2, s)
		}
	}
	report.ChangedFiles = uniq2
	return report
}

func truncateString(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

const (
	defaultMaxSummaryFiles = 200
	defaultMaxFileBytes    = 64 * 1024
	defaultMaxDiffBytes    = 256 * 1024
)

var skipSummaryDirs = map[string]bool{
	".git":         true,
	".splice":      true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"__pycache__":  true,
}

// summarizeWorkspaceChanges returns a bounded ChangeSummary. If workDir is a git
// repository it prefers git status/diff; otherwise it falls back to a bounded
// filesystem walk. The diff text and changed-file list are capped so a very
// large workspace cannot produce an unbounded trajectory input.
func summarizeWorkspaceChanges(ctx context.Context, workDir string) schemas.ChangeSummary {
	if summary, ok := gitChangeSummary(ctx, workDir); ok {
		return summary
	}
	return walkChangeSummary(workDir)
}

func gitChangeSummary(ctx context.Context, workDir string) (schemas.ChangeSummary, bool) {
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return schemas.ChangeSummary{}, false
	}
	if _, err := exec.LookPath("git"); err != nil {
		return schemas.ChangeSummary{}, false
	}

	statusOut, err := exec.CommandContext(ctx, "git", "-C", workDir, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		return schemas.ChangeSummary{}, false
	}

	diffOut, err := exec.CommandContext(ctx, "git", "-C", workDir, "diff", "HEAD", "--no-color").Output()
	if err != nil {
		return schemas.ChangeSummary{}, false
	}

	truncated := false
	var files []schemas.ChangedFile
	var created []string
	for _, line := range strings.Split(string(statusOut), "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 4 {
			continue
		}
		statusCode := line[:2]
		path := strings.TrimSpace(line[2:])
		if path == "" {
			continue
		}
		status := gitStatusToChangeStatus(statusCode)
		files = append(files, schemas.ChangedFile{Path: path, Status: status})
		if status == "created" {
			created = append(created, path)
		}
		if len(files) >= defaultMaxSummaryFiles {
			truncated = true
			break
		}
	}

	diff := &strings.Builder{}
	diff.Write(diffOut)
	total := diff.Len()
	for _, path := range created {
		relPath := path
		if filepath.IsAbs(path) {
			relPath, _ = filepath.Rel(workDir, path)
		}
		full := filepath.Join(workDir, path)
		f, err := os.Open(full)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(defaultMaxFileBytes)))
		_ = f.Close()
		if err != nil {
			continue
		}
		if len(data) == defaultMaxFileBytes {
			truncated = true
		}
		header := fmt.Sprintf("\n# untracked file: %s\n", relPath)
		if total+len(header) > defaultMaxDiffBytes {
			truncated = true
			break
		}
		diff.WriteString(header)
		total += len(header)
		for _, line := range strings.Split(string(data), "\n") {
			out := "+" + line + "\n"
			if total+len(out) > defaultMaxDiffBytes {
				truncated = true
				break
			}
			diff.WriteString(out)
			total += len(out)
		}
	}

	diffText := diff.String()
	if len(diffText) > defaultMaxDiffBytes {
		diffText = diffText[:defaultMaxDiffBytes]
		truncated = true
	}

	return schemas.ChangeSummary{
		IsRepo:       true,
		ChangedFiles: files,
		DiffText:     diffText,
		Truncated:    truncated,
	}, true
}

func gitStatusToChangeStatus(code string) string {
	switch {
	case strings.Contains(code, "D"):
		return "deleted"
	case strings.Contains(code, "A") || strings.Contains(code, "?"):
		return "created"
	default:
		return "modified"
	}
}

func walkChangeSummary(workDir string) schemas.ChangeSummary {
	files := []schemas.ChangedFile{}
	diff := &strings.Builder{}
	truncated := false
	totalBytes := 0

	_ = filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipSummaryDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(workDir, path)
		if rel == "" || skipSummaryDirComponent(rel) {
			return nil
		}
		if len(files) >= defaultMaxSummaryFiles {
			truncated = true
			return filepath.SkipAll
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(defaultMaxFileBytes)))
		_ = f.Close()
		if err != nil {
			return nil
		}
		if len(data) == defaultMaxFileBytes {
			truncated = true
		}

		header := fmt.Sprintf("# file: %s\n", rel)
		if totalBytes+len(header) > defaultMaxDiffBytes {
			truncated = true
			return filepath.SkipAll
		}
		diff.WriteString(header)
		totalBytes += len(header)

		n := len(data)
		if totalBytes+n > defaultMaxDiffBytes {
			n = defaultMaxDiffBytes - totalBytes
			truncated = true
		}
		if n > 0 {
			diff.Write(data[:n])
			totalBytes += n
			if n < len(data) || data[len(data)-1] != '\n' {
				diff.WriteByte('\n')
				totalBytes++
			}
		}

		files = append(files, schemas.ChangedFile{Path: rel, Status: "modified"})
		return nil
	})

	return schemas.ChangeSummary{
		IsRepo:       false,
		ChangedFiles: files,
		DiffText:     diff.String(),
		Truncated:    truncated,
	}
}

func skipSummaryDirComponent(rel string) bool {
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if skipSummaryDirs[part] {
			return true
		}
	}
	return false
}

func newAgentToolRunner(options agent.Options, cwd string) ToolRunner {
	if options.Registry == nil {
		return ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			call := toolCallFor(name, args)
			emitToolCall(options, call)
			res := ToolResult{
				OK:     false,
				Output: "no tool registry available",
				Meta:   map[string]string{},
				Status: tools.StatusError,
			}
			emitToolResult(options, call, res)
			return res, nil
		})
	}
	return ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		call := toolCallFor(name, args)
		emitToolCall(options, call)
		tool, ok := options.Registry.Get(name)
		if !ok {
			res := ToolResult{
				OK:     false,
				Output: errToolNotFound{tool: name}.Error(),
				Meta:   map[string]string{},
				Status: tools.StatusError,
			}
			emitToolResult(options, call, res)
			return res, nil
		}
		permission := tool.Safety().Permission
		if permissioner, ok := tool.(tools.ArgsPermissioner); ok {
			permission = permissioner.PermissionForArgs(args)
		}
		permissionGranted := false
		if permission == tools.PermissionPrompt {
			request := agent.PermissionRequest{
				ToolCallID:         call.ID,
				ToolName:           name,
				Action:             agent.PermissionActionPrompt,
				Permission:         string(permission),
				PermissionMode:     options.PermissionMode,
				Autonomy:           options.Autonomy,
				SideEffect:         string(tool.Safety().SideEffect),
				Reason:             tool.Safety().Reason,
				Risk:               sandboxRisk(tool.Safety().SideEffect),
				Args:               args,
				AvailableDecisions: []agent.PermissionDecisionAction{agent.PermissionDecisionAllow, agent.PermissionDecisionDeny},
			}
			switch options.PermissionMode {
			case agent.PermissionModeAsk:
				if options.OnPermissionRequest == nil {
					res := ToolResult{OK: false, Output: "permission request not handled", Meta: map[string]string{}, Status: tools.StatusError}
					emitToolResult(options, call, res)
					return res, nil
				}
				emitPermissionPrompt(options, request)
				decision, err := options.OnPermissionRequest(ctx, request)
				if err != nil || decision.Action != agent.PermissionDecisionAllow {
					reason := strings.TrimSpace(decision.Reason)
					if err != nil {
						reason = err.Error()
					}
					if reason == "" {
						reason = "permission denied"
					}
					emitPermissionDecision(options, request, agent.PermissionDecisionDeny, reason, false)
					res := ToolResult{OK: false, Output: "permission denied", Meta: map[string]string{"permission_action": string(agent.PermissionActionDeny)}, Status: tools.StatusError}
					emitToolResult(options, call, res)
					return res, nil
				}
				emitPermissionDecision(options, request, agent.PermissionDecisionAllow, strings.TrimSpace(decision.Reason), true)
				permissionGranted = true
			case agent.PermissionModeUnsafe:
				permissionGranted = true
				emitPermissionDecision(options, request, agent.PermissionDecisionAllow, "unsafe permissions mode allowed prompt-gated tool", true)
			default:
				// auto and spec-draft grant mutating tools automatically.
				emitPermissionDecision(options, request, agent.PermissionDecisionAllow, "permission mode allowed prompt-gated tool", false)
				permissionGranted = true
			}
		}
		res := options.Registry.RunWithOptions(ctx, name, args, tools.RunOptions{
			Sandbox:           options.Sandbox,
			PermissionMode:    string(options.PermissionMode),
			Autonomy:          options.Autonomy,
			FileTracker:       options.FileTracker,
			Cwd:               cwd,
			ToolCallID:        call.ID,
			SessionID:         options.SessionID,
			Model:             options.Model,
			PermissionGranted: permissionGranted,
		})
		meta := res.Meta
		if meta == nil {
			meta = map[string]string{}
		}
		toolResult := ToolResult{
			OK:           res.Status == tools.StatusOK,
			Output:       res.Output,
			Truncated:    res.Truncated || meta["truncated"] == "true",
			Meta:         meta,
			Status:       res.Status,
			Redacted:     res.Redacted,
			ChangedFiles: res.ChangedFiles,
			Display:      res.Display,
		}
		emitToolResult(options, call, toolResult)
		return toolResult, nil
	})
}

func sandboxRisk(sideEffect tools.SideEffect) sandbox.Risk {
	level := sandbox.RiskLow
	switch sideEffect {
	case tools.SideEffectWrite, tools.SideEffectShell:
		level = sandbox.RiskMedium
	case tools.SideEffectOutOfWorkspace, tools.SideEffectNetwork:
		level = sandbox.RiskHigh
	}
	return sandbox.Risk{Level: level}
}

func emitProgress(options agent.Options, text string) {
	if options.OnReasoning != nil {
		options.OnReasoning(text)
	}
}

// stageEventMarkerBegin and stageEventMarkerEnd delimit a structured stage event
// embedded in the OnReasoning stream. The TUI detects and parses these markers
// to drive its PIPELINE sidebar without a dedicated agent.Options callback
// (which is upstream Zero code we do not modify). The payload between the
// markers is a compact JSON object.
const (
	stageEventMarkerBegin = "\x00STAGE"
	stageEventMarkerEnd   = "\x00"
)

// emitStageEvent sends a structured stage lifecycle event through the
// OnReasoning callback as a null-delimited marker. The TUI parses it to update
// its PIPELINE sidebar; headless consumers ignore it (it looks like a short
// binary-prefixed line). status is one of: started, running, completed,
// failed, skipped, retry.
func emitStageEvent(options agent.Options, stageName, status, detail string, progress int, changedFiles []string) {
	if options.OnReasoning == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"name":         stageName,
		"status":       status,
		"detail":       detail,
		"progress":     progress,
		"changedFiles": changedFiles,
	})
	if err != nil {
		return
	}
	options.OnReasoning(stageEventMarkerBegin + string(payload) + stageEventMarkerEnd)
}

// stageChangedFiles extracts the changed file paths from a completed stage
// output. It checks code_writer_output and test_generator_output — the two
// stage types that produce FileChange slices. Returns nil when neither is
// present, so non-completed callers and stages without files pass nil cleanly.
func stageChangedFiles(output schemas.HarnessStageOutput) []string {
	var files []string
	if cw, ok := output.Data["code_writer_output"]; ok {
		if cwo, ok := cw.(schemas.CodeWriterOutput); ok {
			for _, f := range cwo.Files {
				files = append(files, f.Path)
			}
		}
	}
	if tg, ok := output.Data["test_generator_output"]; ok {
		if tgo, ok := tg.(schemas.TestGeneratorOutput); ok {
			for _, f := range tgo.Files {
				files = append(files, f.Path)
			}
		}
	}
	// Deduplicate while preserving order.
	seen := map[string]bool{}
	uniq := make([]string, 0, len(files))
	for _, f := range files {
		if !seen[f] {
			seen[f] = true
			uniq = append(uniq, f)
		}
	}
	return uniq
}

func emitText(options agent.Options, text string) {
	if options.OnText != nil {
		options.OnText(text)
	}
}

func toolCallFor(name string, args map[string]any) agent.ToolCall {
	data, _ := json.Marshal(args)
	return agent.ToolCall{
		ID:        newToolCallID(name),
		Name:      name,
		Arguments: string(data),
	}
}

func newToolCallID(name string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	safeName := strings.NewReplacer(".", "_", "-", "_").Replace(name)
	if safeName == "" {
		safeName = "tool"
	}
	return "call_" + safeName + "_" + hex.EncodeToString(b)
}

func emitToolCall(options agent.Options, call agent.ToolCall) {
	if options.OnToolCall != nil {
		options.OnToolCall(call)
	}
}

func emitToolResult(options agent.Options, call agent.ToolCall, result ToolResult) {
	if options.OnToolResult == nil {
		return
	}
	status := result.Status
	if status == "" {
		if result.OK {
			status = tools.StatusOK
		} else {
			status = tools.StatusError
		}
	}
	options.OnToolResult(agent.ToolResult{
		ToolCallID:   call.ID,
		Name:         call.Name,
		Status:       status,
		Output:       result.Output,
		Meta:         result.Meta,
		Redacted:     result.Redacted,
		ChangedFiles: result.ChangedFiles,
		Display:      result.Display,
	})
}

func emitPermissionPrompt(options agent.Options, request agent.PermissionRequest) {
	if options.OnPermission == nil {
		return
	}
	options.OnPermission(agent.PermissionEvent{
		ToolCallID:     request.ToolCallID,
		ToolName:       request.ToolName,
		Action:         agent.PermissionActionPrompt,
		Permission:     request.Permission,
		PermissionMode: request.PermissionMode,
		Autonomy:       request.Autonomy,
		SideEffect:     request.SideEffect,
		Reason:         request.Reason,
		Scope:          request.Scope,
		Risk:           request.Risk,
		CommandPrefix:  append([]string(nil), request.CommandPrefix...),
	})
}

func emitPermissionDecision(options agent.Options, request agent.PermissionRequest, action agent.PermissionDecisionAction, reason string, granted bool) {
	if options.OnPermission == nil {
		return
	}
	eventAction := agent.PermissionActionDeny
	if action == agent.PermissionDecisionAllow {
		eventAction = agent.PermissionActionAllow
	}
	options.OnPermission(agent.PermissionEvent{
		ToolCallID:        request.ToolCallID,
		ToolName:          request.ToolName,
		Action:            eventAction,
		DecisionAction:    action,
		Permission:        request.Permission,
		PermissionGranted: granted,
		PermissionMode:    request.PermissionMode,
		Autonomy:          request.Autonomy,
		SideEffect:        request.SideEffect,
		Reason:            request.Reason,
		Scope:             request.Scope,
		DecisionReason:    reason,
		Risk:              request.Risk,
		CommandPrefix:     append([]string(nil), request.CommandPrefix...),
	})
}

func finishCompleted(runID string, plan schemas.ExecutionPlan, records []schemas.StageRecord) (schemas.PipelineResult, error) {
	return sumTotals(schemas.PipelineResult{
		RunID:  runID,
		Status: "completed",
		Tier:   plan.Tier,
		Stages: records,
	}), nil
}

func finishWithReason(runID string, plan schemas.ExecutionPlan, records []schemas.StageRecord, status, reason string) (schemas.PipelineResult, error) {
	return sumTotals(schemas.PipelineResult{
		RunID:       runID,
		Status:      status,
		Tier:        plan.Tier,
		Stages:      records,
		AbortReason: &reason,
	}), nil
}

func abortReason(result schemas.PipelineResult) string {
	if result.AbortReason != nil {
		return *result.AbortReason
	}
	return ""
}

// sumTotals populates PipelineResult token/cost totals from the per-stage
// records so the final answer never observes zero after real usage.
func sumTotals(result schemas.PipelineResult) schemas.PipelineResult {
	for _, r := range result.Stages {
		result.TotalTokensInput += r.TokensInput
		result.TotalTokensOutput += r.TokensOutput
		result.TotalCostUSD += r.CostUSD
	}
	return result
}

// mergeStageUsage sums two StageUsage pointers so a context-fulfillment call
// and the final stage call are both accounted. nil inputs are handled; if both
// are nil the result is nil (byte-identical to no usage reported).
func mergeStageUsage(a, b *schemas.StageUsage) *schemas.StageUsage {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &schemas.StageUsage{
		InputTokens:       a.InputTokens + b.InputTokens,
		OutputTokens:      a.OutputTokens + b.OutputTokens,
		CachedInputTokens: a.CachedInputTokens + b.CachedInputTokens,
		CacheWriteTokens:  a.CacheWriteTokens + b.CacheWriteTokens,
		CostUSD:           a.CostUSD + b.CostUSD,
	}
}

func completionSummary(result schemas.PipelineResult) string {
	summary := fmt.Sprintf("Pipeline %s after %d stage record(s).", result.Status, len(result.Stages))
	if result.AbortReason != nil && strings.TrimSpace(*result.AbortReason) != "" {
		summary += " " + strings.TrimSpace(*result.AbortReason) + "."
	}
	return summary + "\n"
}
