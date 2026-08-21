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
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/learn"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const (
	defaultMaxIterations  = 5
	defaultMaxWallSeconds = 600
)

var errWallTimeExceeded = errors.New("wall time exceeded")

type requestLedger struct {
	records []schemas.PipelineUsageRecord
}

func newRequestLedger() *requestLedger { return &requestLedger{} }

func (ledger *requestLedger) append(record schemas.PipelineUsageRecord) {
	record.Sequence = len(ledger.records) + 1
	ledger.records = append(ledger.records, record)
}

func (ledger *requestLedger) recordingOptions(options PipelineRunConfig) PipelineRunConfig {
	recorded := options
	downstreamAttributed := options.OnAttributedUsage
	downstreamLegacy := options.OnUsage
	recorded.OnAttributedUsage = func(attributed agent.AttributedUsage) {
		attributed.Sequence = len(ledger.records) + 1
		if attributed.UsageError != "" {
			attributed.Usage = zeroruntime.Usage{}
			attributed.Cost = costError(attributed.UsageError)
		} else if attributed.UsageReported {
			normalized, err := zeroruntime.NormalizeUsage(zeroruntime.TokenUsage{
				InputTokens:       attributed.Usage.InputTokens,
				PromptTokens:      attributed.Usage.PromptTokens,
				CachedInputTokens: attributed.Usage.CachedInputTokens,
				CacheWriteTokens:  attributed.Usage.CacheWriteTokens,
				OutputTokens:      attributed.Usage.OutputTokens,
				CompletionTokens:  attributed.Usage.CompletionTokens,
				ReasoningTokens:   attributed.Usage.ReasoningTokens,
				WebSearchRequests: attributed.Usage.WebSearchRequests,
				WebSearchEngine:   attributed.Usage.WebSearchEngine,
			})
			if err != nil {
				attributed.Usage = zeroruntime.Usage{}
				attributed.Cost = costError(err.Error())
			} else {
				attributed.Usage = normalized
				if attributed.ReportedCostUSD != nil {
					// The provider told us the exact charge; trust it over the
					// registry estimate instead of computing one we'd discard.
					attributed.Cost = reportedUsageCost(*attributed.ReportedCostUSD)
				} else {
					attributed.Cost = estimateUsageCost(options.EstimateUsageCost, attributed)
				}
			}
		} else {
			attributed.Usage = zeroruntime.Usage{}
			attributed.Cost = estimateUsageCost(options.EstimateUsageCost, attributed)
		}
		if err := validateUsageCostEstimate(attributed.Cost); err != nil {
			attributed.Cost = costError("invalid cost estimate: " + err.Error())
		}

		record := schemas.PipelineUsageRecord{
			Sequence:          attributed.Sequence,
			Provider:          attributed.ProviderName,
			Model:             attributed.Model,
			Stage:             attributed.Stage,
			Iteration:         attributed.Iteration,
			UsageReported:     attributed.UsageReported,
			InputTokens:       attributed.Usage.EffectiveInputTokens(),
			OutputTokens:      attributed.Usage.EffectiveOutputTokens(),
			CachedTokens:      attributed.Usage.CachedInputTokens,
			CacheWrite:        attributed.Usage.CacheWriteTokens,
			Reasoning:         attributed.Usage.ReasoningTokens,
			WebSearchRequests: attributed.Usage.WebSearchRequests,
			WebSearchEngine:   attributed.Usage.WebSearchEngine,
			CostStatus:        attributed.Cost.Status,
			CostProvenance:    attributed.Cost.Provenance,
			PricingSource:     attributed.Cost.PricingSource,
			PricingAsOf:       attributed.Cost.PricingAsOf,
			UnpricedReason:    attributed.Cost.UnpricedReason,
		}
		if attributed.Cost.CostUSD != nil {
			cost := *attributed.Cost.CostUSD
			record.CostUSD = &cost
		}
		ledger.append(record)

		if downstreamAttributed != nil {
			downstreamAttributed(attributed)
		} else if downstreamLegacy != nil && attributed.UsageReported {
			downstreamLegacy(attributed.Usage)
		}
	}
	return recorded
}

func estimateUsageCost(estimator func(string, agent.Usage, bool) agent.UsageCostEstimate, attributed agent.AttributedUsage) agent.UsageCostEstimate {
	if estimator != nil {
		return estimator(attributed.Model, attributed.Usage, attributed.UsageReported)
	}
	reason := "usage not reported by provider"
	if attributed.UsageReported {
		reason = "cost estimator unavailable"
	}
	return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: reason}
}

func costError(reason string) agent.UsageCostEstimate {
	return agent.UsageCostEstimate{Status: agent.CostStatusError, UnpricedReason: reason}
}

// reportedUsageCost builds a priced UsageCostEstimate from a provider-reported
// exact charge (currently only OpenRouter's usage.cost). PricingSource and
// PricingAsOf are non-registry sentinels rather than empty: validateUsageCostEstimate
// requires both non-empty for any priced estimate, and "as of a rate table
// verified on some date" would misdescribe a live, per-request billed figure.
func reportedUsageCost(costUSD float64) agent.UsageCostEstimate {
	cost := costUSD
	return agent.UsageCostEstimate{
		CostUSD:       &cost,
		Status:        agent.CostStatusPriced,
		Provenance:    agent.CostProvenanceReported,
		PricingSource: "openrouter",
		PricingAsOf:   "live",
	}
}

func validateUsageCostEstimate(estimate agent.UsageCostEstimate) error {
	switch estimate.Status {
	case agent.CostStatusPriced:
		if estimate.CostUSD == nil || math.IsNaN(*estimate.CostUSD) || math.IsInf(*estimate.CostUSD, 0) || *estimate.CostUSD < 0 {
			return errors.New("priced estimate requires a finite non-negative cost")
		}
		switch estimate.Provenance {
		case agent.CostProvenanceRuntimeEstimate, agent.CostProvenancePersistedEstimate, agent.CostProvenanceReconstructedEstimate, agent.CostProvenanceReported:
		default:
			return fmt.Errorf("invalid cost provenance %q", estimate.Provenance)
		}
		if estimate.PricingSource == "" || estimate.PricingAsOf == "" || estimate.UnpricedReason != "" {
			return errors.New("priced estimate requires source, date, and no unpriced reason")
		}
	case agent.CostStatusUnpriced, agent.CostStatusError:
		if estimate.CostUSD != nil || estimate.Provenance != "" || estimate.PricingSource != "" || estimate.PricingAsOf != "" || estimate.UnpricedReason == "" {
			return fmt.Errorf("%s estimate has inconsistent pricing fields", estimate.Status)
		}
	default:
		return fmt.Errorf("invalid cost status %q", estimate.Status)
	}
	return nil
}

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

	cfg := PipelineConfigFromAgentOptions(options)
	result, err := runExecutionPlan(ctx, runID, plan, provider, cfg, mem, rec)
	if err != nil {
		return agent.Result{}, err
	}

	finalAnswer, _ := json.MarshalIndent(result, "", "  ")
	emitText(cfg, completionSummary(result))
	return agent.Result{
		FinalAnswer:      string(finalAnswer),
		Incomplete:       result.Status != "completed",
		IncompleteReason: abortReason(result),
	}, nil
}

func runExecutionPlan(ctx context.Context, runID string, plan schemas.ExecutionPlan, provider agent.Provider, options PipelineRunConfig, mem MemoryStore, rec WorkspaceRecovery) (schemas.PipelineResult, error) {
	workDir := options.Cwd
	if workDir == "" {
		workDir = "."
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("resolve work dir: %w", err)
	}

	// Memory identity is the stable repo root (options.ProjectRoot for worktree
	// runs), never the per-run worktree path. Tool execution and workspace
	// summaries keep working in absWorkDir.
	projectRoot := memoryProjectRoot(options, absWorkDir)

	// Memory status is active / off / unavailable. The caller's resolved status
	// wins; an empty value derives from the MemoryStore (nil = off, non-nil =
	// active) for backward compatibility.
	memoryStatus := options.MemoryStatus
	if memoryStatus == "" {
		if mem != nil {
			memoryStatus = "active"
		} else {
			memoryStatus = "off"
		}
	}

	// Preflight: diagnose substrate interference (permission mode, hooks,
	// provider capability) before any stage runs. Advisory only: each issue is
	// emitted as a warning and the run continues. User machinery is
	// authoritative, exactly as before.
	for _, issue := range Preflight(plan, options) {
		label := issue.Stage
		if label == "" {
			label = "preflight"
		}
		emitProgress(options, fmt.Sprintf("[%s] %s\n", label, issue.Message))
	}

	registry, err := buildStageRegistry(options, absWorkDir)
	if err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("build stage registry: %w", err)
	}
	emitPipelinePlan(options, plan)

	// A trace store is the memory client asserting the TraceStore interface; a
	// nil MemoryStore (memory off) means tracing is off too.
	var tracer TraceStore
	if t, ok := mem.(TraceStore); ok && t != nil {
		tracer = t
	}

	// LN2: learned budgets. Calibrated fits override the static per-stage
	// budgets before the iteration loop, and the fitted plan is embedded in the
	// trace so applied budgets are always recorded. Only LLM-backed stages have
	// a token budget; deterministic stages keep their zero budget untouched.
	budgetProvenance := map[string]string{}
	toolFingerprint := ""
	topologyHash := ""
	stagePromptHashes := map[string]string{}
	if tracer != nil {
		if querier, ok := mem.(learn.TraceQuerier); ok && querier != nil {
			stageNames := make([]string, len(plan.Stages))
			for i, s := range plan.Stages {
				stageNames[i] = s.Name
			}
			toolFingerprint = learn.Hash(stages.VerificationToolIdentities()...)
			topologyHash = learn.Hash(stageNames...)
			calibrated := 0
			for i := range plan.Stages {
				stage := &plan.Stages[i]
				if registry[stage.Name].Capabilities().ModelFree {
					continue
				}
				promptHash := learn.Hash(stages.StagePrompt(stage.Name))
				stagePromptHashes[stage.Name] = promptHash
				key := learn.BucketKey{
					RepoRoot:        projectRoot,
					Stage:           stage.Name,
					PromptHash:      promptHash,
					Model:           resolvedModelForStage(options, stage.Name),
					ToolFingerprint: toolFingerprint,
					TopologyHash:    topologyHash,
				}
				fit, ferr := learn.FitBudget(ctx, querier, key, memoryStatus, stage.Budget)
				if ferr != nil {
					budgetProvenance[stage.Name] = "fit error: " + ferr.Error()
					continue
				}
				budgetProvenance[stage.Name] = fit.Provenance
				if fit.Calibrated {
					stage.Budget.InputMax = fit.InputMax
					stage.Budget.OutputMax = fit.OutputMax
					calibrated++
				}
			}
			emitProgress(options, fmt.Sprintf("budgets: %d/%d stages calibrated\n", calibrated, len(plan.Stages)))
		}
	}

	var tr *runTraceAccumulator
	if tracer != nil {
		tr = newRunTraceAccumulator(tracer, runID, options.SessionID, projectRoot, plan, memoryStatus)
		tr.toolFingerprint = toolFingerprint
		tr.topologyHash = topologyHash
		tr.stagePromptHash = stagePromptHashes
		tr.budgetProvenance = budgetProvenance
		upstreamPermission := options.OnPermission
		options.OnPermission = func(event agent.PermissionEvent) {
			tr.recordPermission(event)
			if upstreamPermission != nil {
				upstreamPermission(event)
			}
		}
	}

	runner := newAgentToolRunner(options, absWorkDir)

	// Deterministic stage subprocesses run under their own enforce-mode
	// engine: workspace-scoped filesystem, network denied. This is separate
	// from options.Sandbox, which keeps the interactive user policy for
	// model-driven tool calls.
	options.StageSandbox = procrun.NewStageEngine(absWorkDir)

	ledger := newRequestLedger()
	ledgerOpts := ledger.recordingOptions(options)

	if mem != nil {
		obs := buildConfigObservation(runID, projectRoot, plan)
		persistObservation(ctx, mem, obs, func(msg string) {
			emitProgress(options, fmt.Sprintf("[orchestrator] %s", msg))
		})
	}

	result, err := runIterationLoop(ctx, runID, plan, registry, provider, ledgerOpts, absWorkDir, runner, mem, rec, tr)
	if err != nil {
		return schemas.PipelineResult{}, err
	}

	if err := applyRequestLedger(&result, ledger); err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("apply request ledger: %w", err)
	}

	if err := result.Validate(); err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("validate pipeline result: %w", err)
	}

	// Trace write happens after the ledger and result validation so the stored
	// stage records carry the authoritative token totals. A validate failure is
	// a schema bug (fail loudly); a store write failure is a warning and never
	// fails the run.
	if tr != nil {
		trace, buildErr := tr.buildRunOutcome(result)
		if buildErr != nil {
			return schemas.PipelineResult{}, fmt.Errorf("build run outcome: %w", buildErr)
		}
		if writeErr := tracer.UpsertTrace(ctx, trace); writeErr != nil {
			emitProgress(options, fmt.Sprintf("[trace] write skipped: %v", writeErr))
		}
	}
	return result, nil
}

// resolvedModelForStage returns the model string a stage will use, mirroring
// the resolution in runPass: the per-stage resolver when set, else the default
// run model. It is used at run start to compute the LN2 bucket key.
func resolvedModelForStage(options PipelineRunConfig, stageName string) string {
	if options.StageModelResolver != nil {
		if resolved, err := options.StageModelResolver(stageName); err == nil && resolved.Provider != nil && resolved.Model != "" {
			return resolved.Model
		}
	}
	return options.Model
}

func runIterationLoop(
	ctx context.Context,
	runID string,
	plan schemas.ExecutionPlan,
	registry stageRegistry,
	provider agent.Provider,
	options PipelineRunConfig,
	workDir string,
	runner ToolRunner,
	mem MemoryStore,
	rec WorkspaceRecovery,
	tr *runTraceAccumulator,
) (schemas.PipelineResult, error) {
	maxWallSeconds := defaultMaxWallSeconds
	tokenBudget := plan.TokenBudget.TotalInputBudget + plan.TokenBudget.TotalOutputBudget

	history := []schemas.IterationState{}
	allRecords := []schemas.StageRecord{}
	wallDeadline := time.Now().Add(time.Duration(maxWallSeconds) * time.Second)
	var revisionContext *string
	var priorFailure string

	// escalated tracks whether the escalation model resolver has been called
	// for this run. Escalation fires at most once (AR10c).
	escalated := false

	// MaxTurns applies to agent.Run, not deterministic pipeline passes.
	maxIterations := defaultMaxIterations

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
		if ctx.Err() != nil {
			return schemas.PipelineResult{}, context.Canceled
		}
		if !time.Now().Before(wallDeadline) {
			return finishWithReason(runID, plan, allRecords, "aborted", "wall time exceeded")
		}

		emitProgress(options, fmt.Sprintf("Starting pipeline iteration %d\n", i))
		passRecords, passOutputs, completed, err := runPass(ctx, runID, i, plan, registry, provider, options, workDir, runner, wallDeadline, revisionContext, mem, tr)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return schemas.PipelineResult{}, context.Canceled
			}
			if errors.Is(err, errWallTimeExceeded) {
				allRecords = append(allRecords, passRecords...)
				return finishWithReason(runID, plan, allRecords, "aborted", "wall time exceeded")
			}
			return finishWithReason(runID, plan, allRecords, "failed", err.Error())
		}
		allRecords = append(allRecords, passRecords...)

		if !completed {
			failed := findFailed(passRecords)
			failure := failed.Name + "\x00" + DerefString(failed.OutputSummary)
			if failure == priorFailure {
				reason := fmt.Sprintf("repeated unchanged stage failure in iterations %d and %d: %s", i-1, i, failed.Name)
				if detail := DerefString(failed.OutputSummary); detail != "" {
					reason += ": " + detail
				}
				return finishWithReason(runID, plan, allRecords, "failed", reason)
			}
			priorFailure = failure
			if i < maxIterations {
				rc := buildRevisionContext(plan.RequestIntent, history, passRecords, passOutputs, fmt.Sprintf("Recovery: stage failure in iteration %d: %s", i, DerefString(failed.OutputSummary)))
				revisionContext = &rc
				continue
			}
			reason := fmt.Sprintf("stage failed in iteration %d", i)
			if detail := DerefString(failed.OutputSummary); detail != "" {
				reason += ": " + detail
			}
			return finishWithReason(runID, plan, allRecords, "failed", reason)
		}
		priorFailure = ""

		changeSummary := summarizeWorkspaceChanges(ctx, workDir)
		state, err := ComputeIterationState(i, passOutputs, passRecords, changeSummary, nil)
		if err != nil {
			return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("compute iteration state: %v", err))
		}
		history = append(history, state)
		if tr != nil {
			tr.recordHistory(state)
			tr.persistPartial(ctx)
		}

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
			rc := buildRevisionContext(plan.RequestIntent, history, passRecords, passOutputs, "")
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
			rc := buildRevisionContext(plan.RequestIntent, history, passRecords, passOutputs, fmt.Sprintf("Rollback: restored iteration %d at score %.1f. %s", target.iter, target.score, decision.Reason))
			revisionContext = &rc
			continue
		}
		if decision.Action == schemas.ActionStepBack {
			report := buildStepBackReport(plan.RequestIntent, history, passOutputs, decision)
			sbSelection := agent.ModelSelection{
				Provider:        provider,
				ProviderName:    options.ProviderName,
				Model:           options.Model,
				ReasoningEffort: options.ReasoningEffort,
			}
			stageOpts := stageOptions("step_back", i, sbSelection, options, workDir, runner, stages.Capabilities{})
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
					escalation, escErr := options.EscalationModelResolver()
					if escErr != nil {
						emitProgress(options, fmt.Sprintf("[escalation] resolver error: %v (continuing without escalation)\n", escErr))
					} else if escalation.Provider == nil {
						emitProgress(options, "[escalation] no escalation provider configured (continuing without escalation)\n")
					} else {
						provider = escalation.Provider
						options.ProviderName = escalation.ProviderName
						options.Model = escalation.Model
						options.ReasoningEffort = escalation.ReasoningEffort
						// Escalation applies to every later LLM-backed stage. Do not let
						// the original stage router replace it on the next iteration.
						options.StageModelResolver = nil
						emitProgress(options, fmt.Sprintf("[escalation] switched to model %s for iteration %d\n", escalation.Model, i+1))
					}
				} else {
					escalated = true
					emitProgress(options, "[escalation] no EscalationModelResolver configured (continuing without escalation)\n")
				}
			}
			rc := buildRevisionContext(plan.RequestIntent, history, passRecords, passOutputs, fmt.Sprintf("Recovery: %s — %s", decision.Action, decision.Reason))
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

func runPass(
	ctx context.Context,
	runID string,
	iteration int,
	plan schemas.ExecutionPlan,
	registry stageRegistry,
	provider agent.Provider,
	options PipelineRunConfig,
	workDir string,
	runner ToolRunner,
	wallDeadline time.Time,
	revisionContext *string,
	mem MemoryStore,
	tr *runTraceAccumulator,
) ([]schemas.StageRecord, []schemas.HarnessStageOutput, bool, error) {
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	records := []schemas.StageRecord{}
	outputs := []schemas.HarnessStageOutput{}

	stageNames := make([]string, len(plan.Stages))
	for i, stage := range plan.Stages {
		stageNames[i] = stage.Name
	}

	for seq, stage := range plan.Stages {
		if ctx.Err() != nil {
			return records, outputs, false, context.Canceled
		}
		if !wallDeadline.IsZero() && !time.Now().Before(wallDeadline) {
			return records, outputs, false, errWallTimeExceeded
		}
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

		var nextStage string
		if seq+1 < len(stageNames) {
			nextStage = stageNames[seq+1]
		}

		input := schemas.HarnessStageInput{
			RunID:             runID,
			StageName:         stageName,
			Sequence:          seq + 1,
			PlanTier:          plan.Tier,
			RequestIntent:     plan.RequestIntent,
			AcceptanceFacts:   append([]schemas.AcceptanceFact(nil), plan.AcceptanceFacts...),
			PriorSummaries:    maps.Clone(priorSummaries),
			PriorChangedFiles: cloneChangedFiles(priorChangedFiles),
			RevisionContext:   revisionContext,
			PipelineStages:    stageNames,
			NextStage:         nextStage,
		}

		caps := agentStage.Capabilities()
		if tr != nil {
			tr.noteStage(stageName, iteration)
		}
		if mem != nil && caps.ConsumesMemory {
			bundle, mErr := mem.Search(ctx, newMemoryQuery(stageName, plan.RequestIntent, memoryProjectRoot(options, workDir)))
			if mErr != nil {
				emitProgress(options, fmt.Sprintf("[%s] memory retrieval skipped: %v\n", stageName, mErr))
				// A mid-run retrieval failure degrades the run's memory status to
				// unavailable (a warm run that failed must not record as cold).
				if tr != nil {
					tr.noteMemorySearchFailed()
				}
			} else {
				bundle.RequestingAgent = stageName
				// PC3: append kept-run exemplars. Best-effort and silent on
				// failure; an empty exemplar set is correct, not a bug.
				if querier, ok := mem.(learn.TraceQuerier); ok && querier != nil {
					if exemplars, eErr := retrieveExemplars(ctx, querier, memoryProjectRoot(options, workDir), plan.RequestIntent); eErr == nil {
						bundle.Exemplars = exemplars
						if len(exemplars) > 0 {
							emitProgress(options, fmt.Sprintf("exemplars: %d from kept runs\n", len(exemplars)))
						}
					}
				}
				input.MemoryBundle = &bundle
				if tr != nil {
					tr.recordMemory(stageName, iteration, bundle)
				}
			}
		}

		if err := input.Validate(); err != nil {
			return records, outputs, false, fmt.Errorf("stage %s input: %w", stageName, err)
		}

		// Record the stage-boundary payload size for the trace. Best-effort: a
		// marshal failure never stops the run and just leaves the field zero.
		if tr != nil {
			if encoded, encErr := json.Marshal(input); encErr == nil {
				tr.recordEdge(stageName, iteration, len(encoded))
			}
		}

		emitProgress(options, fmt.Sprintf("[%s] stage started\n", stageName))
		emitStageEvent(options, stageName, "running", caps.Description, 0, nil)

		// Model-free stages skip provider resolution and attribution.
		modelFree := caps.ModelFree
		selection := agent.ModelSelection{
			Provider:        provider,
			ProviderName:    options.ProviderName,
			Model:           options.Model,
			ReasoningEffort: options.ReasoningEffort,
		}
		if options.StageModelResolver != nil && !modelFree {
			resolved, rerr := options.StageModelResolver(stageName)
			if rerr != nil {
				emitProgress(options, fmt.Sprintf("[%s] stage model resolution failed: %v\n", stageName, rerr))
			} else if resolved.Provider != nil {
				selection = resolved
				if resolved.Model != "" {
					detail := resolved.Model
					if caps.Description != "" {
						detail = caps.Description + " · " + resolved.Model
					}
					emitStageEvent(options, stageName, "running", detail, 0, nil)
				}
			}
		}
		if modelFree {
			selection = agent.ModelSelection{}
		}

		// The stage deadline cannot exceed the pipeline wall deadline.
		stageCtx := ctx
		var cancelStage context.CancelFunc
		if !wallDeadline.IsZero() {
			stageCtx, cancelStage = context.WithDeadline(ctx, wallDeadline)
		}

		start := time.Now()
		output, err := runStageWithContext(stageCtx, input, agentStage, iteration, selection, options, workDir, runner, mem, stage.Budget.OutputMax, tr)
		if cancelStage != nil {
			cancelStage()
		}
		latencyMs := int(time.Since(start).Milliseconds())
		emitProgress(options, fmt.Sprintf("[%s] stage finished\n", stageName))
		if ctx.Err() != nil {
			return records, outputs, false, context.Canceled
		}
		if !wallDeadline.IsZero() && !time.Now().Before(wallDeadline) {
			return records, outputs, false, errWallTimeExceeded
		}
		if errors.Is(err, context.Canceled) {
			return records, outputs, false, context.Canceled
		}

		record := schemas.StageRecord{
			Name:      stageName,
			Iteration: iteration,
			LatencyMs: latencyMs,
		}
		if !modelFree {
			if selection.Model != "" {
				record.Model = Ptr(selection.Model)
			}
			if selection.ProviderName != "" {
				record.Provider = Ptr(selection.ProviderName)
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
			if tr != nil {
				tr.recordStageCompletion(record)
				tr.persistPartial(ctx)
			}
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
			if tr != nil {
				tr.recordStageCompletion(record)
				tr.persistPartial(ctx)
			}
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
		if tr != nil {
			tr.recordStageCompletion(record)
			tr.persistPartial(ctx)
		}
		if record.Status == schemas.StageIncomplete {
			emitStageEvent(options, stageName, "incomplete", summary, 0, nil)
		} else {
			emitStageEvent(options, stageName, "completed", summary, 100, stageChangedFiles(output))
		}
		for _, obs := range extractWriteObservations(stageName, runID, memoryProjectRoot(options, workDir), output) {
			persistObservation(ctx, mem, obs, func(msg string) {
				emitProgress(options, fmt.Sprintf("[%s] %s", stageName, msg))
			})
		}
		priorSummaries[stageName] = *record.OutputSummary
		priorChangedFiles[stageName] = append([]string(nil), stageChangedFiles(output)...)
		outputs = append(outputs, output)

		// DM2: when the test runner completes with failing tests, route a focused
		// revision request back to code_writer and re-run the tests. The repair
		// loop is bounded (maxLocalRepairs) and only fires when a code_writer
		// summary exists to re-enter.
		if stageName == "test_runner" && record.Status == schemas.StageCompleted {
			if results, ok := output.Data["test_results"].(schemas.TestRunResults); ok && results.Failed() > 0 {
				if _, hasWriter := priorSummaries["code_writer"]; hasWriter {
					if _, interaction, rerr := attemptLocalRepair(ctx, runID, iteration, plan, registry, provider, options, workDir, runner, mem, tr, wallDeadline, &records, &outputs, &priorSummaries, &priorChangedFiles, output); rerr != nil {
						return records, outputs, false, rerr
					} else if interaction != nil && tr != nil {
						tr.recordInteraction(*interaction)
						tr.persistPartial(ctx)
					}
				}
			}
		}
	}

	return records, outputs, true, nil
}

type stageRunError struct {
	err   error
	usage *schemas.StageUsage
}

func (e stageRunError) Error() string                   { return e.err.Error() }
func (e stageRunError) Unwrap() error                   { return e.err }
func (e stageRunError) StageUsage() *schemas.StageUsage { return e.usage }

func withStageUsage(err error, usage *schemas.StageUsage) error {
	if err == nil || usage == nil {
		return err
	}
	var metered interface{ StageUsage() *schemas.StageUsage }
	if errors.As(err, &metered) {
		usage = mergeStageUsage(usage, metered.StageUsage())
	}
	return stageRunError{err: err, usage: usage}
}

func applyStageUsage(record *schemas.StageRecord, usage *schemas.StageUsage) {
	if record == nil || usage == nil {
		return
	}
	record.TokensInput = usage.InputTokens
	record.TokensOutput = usage.OutputTokens
	record.TokensCached = usage.CachedInputTokens
	record.TokensCacheWrite = usage.CacheWriteTokens
	record.TokensReasoning = usage.ReasoningTokens
	record.WebSearchRequests = usage.WebSearchRequests
	record.WebSearchEngine = usage.WebSearchEngine
}

func runStageWithContext(
	ctx context.Context,
	input schemas.HarnessStageInput,
	stage stages.Stage,
	iteration int,
	selection agent.ModelSelection,
	options PipelineRunConfig,
	workDir string,
	runner ToolRunner,
	mem MemoryStore,
	outputMax int,
	tr *runTraceAccumulator,
) (schemas.HarnessStageOutput, error) {
	stageOpts := stageOptions(input.StageName, iteration, selection, options, workDir, runner, stage.Capabilities())
	if outputMax > 0 {
		// The stage's output budget caps every LLM request this stage makes. Zero
		// keeps the provider default (no per-request override).
		stageOpts.MaxOutputTokens = outputMax
	}
	stageOpts.ModelOverride = selection.Model
	stageOpts.ReasoningEffort = selection.ReasoningEffort
	output, err := stage.Run(ctx, input, selection.Provider, stageOpts)
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	if output.ContextRequest == nil {
		return output, nil
	}

	bundle, err := FulfillContextRequest(ctx, *output.ContextRequest, runner)
	if err != nil {
		return schemas.HarnessStageOutput{}, withStageUsage(fmt.Errorf("fulfill context: %w", err), output.Usage)
	}
	input.Context = &bundle
	if tr != nil {
		tr.recordContext(input.StageName, iteration, bundle)
	}
	if mem != nil {
		for _, obs := range extractDegradationObservations(input.StageName, input.RunID, memoryProjectRoot(options, workDir), bundle) {
			persistObservation(ctx, mem, obs, func(msg string) {
				emitProgress(options, fmt.Sprintf("[%s] %s", input.StageName, msg))
			})
		}
	}
	finalOutput, err := stage.Run(ctx, input, selection.Provider, stageOpts)
	if err != nil {
		return schemas.HarnessStageOutput{}, withStageUsage(err, output.Usage)
	}
	if finalOutput.ContextRequest != nil {
		usage := mergeStageUsage(output.Usage, finalOutput.Usage)
		return schemas.HarnessStageOutput{}, withStageUsage(fmt.Errorf("stage requested context more than once"), usage)
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
	if state.TestsFailing > 0 || state.TestsErrored > 0 || state.AcceptanceFactsFailing > 0 {
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

func buildRevisionContext(intent string, history []schemas.IterationState, records []schemas.StageRecord, outputs []schemas.HarnessStageOutput, note string) string {
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
	changedFiles := make([]string, 0)
	for _, output := range outputs {
		changedFiles = append(changedFiles, stageChangedFiles(output)...)
	}
	if len(changedFiles) > 0 {
		changedFiles = uniqueStrings(changedFiles)
		if len(changedFiles) > 50 {
			changedFiles = changedFiles[:50]
		}
		lines = append(lines, "", "Files written by the prior iteration (use change_type modify and overwrite: true when editing them):")
		lines = append(lines, "  "+strings.Join(changedFiles, ", "))
	}
	if note != "" {
		lines = append(lines, "", note)
	}
	return strings.Join(lines, "\n")
}

func cloneChangedFiles(input map[string][]string) map[string][]string {
	if len(input) == 0 {
		return nil
	}
	clone := make(map[string][]string, len(input))
	for stage, paths := range input {
		clone[stage] = append([]string(nil), paths...)
	}
	return clone
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
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

	// Both git reads run under the stage profile: fixed allowlist, workspace
	// scope, network denied. The engine is local because this helper only
	// knows its own workspace root.
	engine := procrun.NewStageEngine(workDir)

	statusCmd, plan, cerr := stages.PrepareStageCommand(ctx, engine, workDir, []string{"git", "-C", workDir, "status", "--porcelain", "--untracked-files=all"})
	if cerr != nil {
		return schemas.ChangeSummary{}, false
	}
	defer plan.Cleanup()
	statusOut, err := statusCmd.Output()
	if err != nil {
		return schemas.ChangeSummary{}, false
	}

	diffCmd, diffPlan, cerr := stages.PrepareStageCommand(ctx, engine, workDir, []string{"git", "-C", workDir, "diff", "HEAD", "--no-color"})
	if cerr != nil {
		return schemas.ChangeSummary{}, false
	}
	defer diffPlan.Cleanup()
	diffOut, err := diffCmd.Output()
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

func newAgentToolRunner(options PipelineRunConfig, cwd string) ToolRunner {
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
		if denied, blocked := agent.DeniedByToolFilters(name, options.EnabledTools, options.DisabledTools); blocked {
			res := ToolResult{
				OK:           false,
				Output:       denied.Output,
				Meta:         map[string]string{"denial_reason": string(denied.DenialReason)},
				Status:       denied.Status,
				DenialReason: denied.DenialReason,
			}
			emitToolResult(options, call, res)
			return res, nil
		}
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
		if options.Sandbox != nil {
			decision := options.Sandbox.Evaluate(ctx, sandbox.Request{
				WorkspaceRoot:    cwd,
				ToolName:         name,
				SideEffect:       sandbox.SideEffect(tool.Safety().SideEffect),
				Permission:       sandbox.Permission(permission),
				PermissionMode:   sandbox.PermissionMode(options.PermissionMode),
				TrustedWorkspace: options.TrustedWorkspace,
				Args:             args,
				Reason:           tool.Safety().Reason,
			})
			if decision.Action == sandbox.ActionAllow && decision.AutoAllowed {
				permissionGranted = true
			}
		}
		if permission == tools.PermissionPrompt && !permissionGranted {
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
				permissionGranted = true
				emitPermissionDecision(options, request, agent.PermissionDecisionAllow, "permission mode allowed prompt-gated tool", true)
			}
		}
		agentOpts := options.agentOptions()
		if outcome, blocked := agent.RunBeforeToolHooks(ctx, agentOpts, call, args); blocked {
			blockedResult := agent.HookBlockedResult(call, outcome)
			res := ToolResult{
				OK:           false,
				Output:       blockedResult.Output,
				Meta:         map[string]string{"denial_reason": string(blockedResult.DenialReason)},
				Status:       blockedResult.Status,
				DenialReason: blockedResult.DenialReason,
			}
			emitToolResult(options, call, res)
			return res, nil
		}
		// Keep the pipeline's auto/spec-draft grant semantics. The shared helper
		// only builds tools.RunOptions; it does not replace this prompt flow.
		res := options.Registry.RunWithOptions(ctx, name, args, agent.NewToolRunOptions(agentOpts, call, cwd, permissionGranted))
		feedback := agent.RunAfterToolHooks(ctx, agentOpts, call, args, res)
		if feedback != "" {
			combined, redacted := agent.AppendHookFeedback(res.Output, feedback)
			res.Output = combined
			res.Redacted = res.Redacted || redacted
		}
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

func emitProgress(options PipelineRunConfig, text string) {
	if options.OnReasoning != nil {
		options.OnReasoning(text)
	}
}

// stageEventMarkerBegin and stageEventMarkerEnd delimit a structured stage event
// in the OnReasoning stream. Stored sessions can use these markers to rebuild
// the pipeline panel. The payload is a compact JSON object.
const (
	stageEventMarkerBegin = "\x00STAGE"
	stageEventMarkerEnd   = "\x00"
)

// emitPipelinePlan announces the complete ordered roster before stage execution.
func emitPipelinePlan(options PipelineRunConfig, plan schemas.ExecutionPlan) {
	if options.OnPipelinePlan == nil {
		return
	}
	stages := make([]string, len(plan.Stages))
	for i, stage := range plan.Stages {
		stages[i] = stage.Name
	}
	options.OnPipelinePlan(agent.PipelinePlanEvent{Stages: stages})
}

// emitStageEvent sends a typed stage lifecycle event. It also writes the
// deprecated NUL marker on OnReasoning for one release. status is one of:
// running, completed, failed, skipped, incomplete.
func emitStageEvent(options PipelineRunConfig, stageName, status, detail string, progress int, changedFiles []string) {
	event := agent.StageEvent{
		Name:         stageName,
		Status:       status,
		Detail:       detail,
		Progress:     progress,
		ChangedFiles: append([]string(nil), changedFiles...),
	}
	if options.OnStageEvent != nil {
		options.OnStageEvent(event)
	}
	if options.OnReasoning == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"name":         event.Name,
		"status":       event.Status,
		"detail":       event.Detail,
		"progress":     event.Progress,
		"changedFiles": event.ChangedFiles,
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

func emitText(options PipelineRunConfig, text string) {
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

func emitToolCall(options PipelineRunConfig, call agent.ToolCall) {
	if options.OnToolCall != nil {
		options.OnToolCall(call)
	}
}

func emitToolResult(options PipelineRunConfig, call agent.ToolCall, result ToolResult) {
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
		DenialReason: result.DenialReason,
	})
}

func emitPermissionPrompt(options PipelineRunConfig, request agent.PermissionRequest) {
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

func emitPermissionDecision(options PipelineRunConfig, request agent.PermissionRequest, action agent.PermissionDecisionAction, reason string, granted bool) {
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
	return schemas.PipelineResult{
		RunID:  runID,
		Status: "completed",
		Tier:   plan.Tier,
		Stages: records,
	}, nil
}

func finishWithReason(runID string, plan schemas.ExecutionPlan, records []schemas.StageRecord, status, reason string) (schemas.PipelineResult, error) {
	return schemas.PipelineResult{
		RunID:       runID,
		Status:      status,
		Tier:        plan.Tier,
		Stages:      records,
		AbortReason: &reason,
	}, nil
}

func abortReason(result schemas.PipelineResult) string {
	if result.AbortReason != nil {
		return *result.AbortReason
	}
	return ""
}

// applyRequestLedger replaces stage-derived totals with authoritative request totals.
func applyRequestLedger(result *schemas.PipelineResult, ledger *requestLedger) error {
	type stageUsageKey struct {
		name      string
		iteration int
	}
	expected := make(map[stageUsageKey]schemas.StageUsage, len(result.Stages))
	stageIndex := make(map[stageUsageKey]int, len(result.Stages))
	for i, stage := range result.Stages {
		key := stageUsageKey{stage.Name, stage.Iteration}
		expected[key] = schemas.StageUsage{
			InputTokens: stage.TokensInput, OutputTokens: stage.TokensOutput,
			CachedInputTokens: stage.TokensCached, CacheWriteTokens: stage.TokensCacheWrite,
			ReasoningTokens: stage.TokensReasoning,
		}
		stageIndex[key] = i
	}
	result.UsageRecords = append([]schemas.PipelineUsageRecord(nil), ledger.records...)

	// Reset old stage-derived totals so retries and auxiliary calls are counted
	// once.
	result.TotalTokensInput = 0
	result.TotalTokensOutput = 0
	result.TotalTokensCached = 0
	result.TotalTokensCacheWrite = 0
	result.TotalTokensReasoning = 0
	result.TotalCostUSD = 0
	for i := range result.Stages {
		result.Stages[i].TokensInput = 0
		result.Stages[i].TokensOutput = 0
		result.Stages[i].TokensCached = 0
		result.Stages[i].TokensCacheWrite = 0
		result.Stages[i].TokensReasoning = 0
		result.Stages[i].CostUSD = 0
	}

	// Sum pipeline totals and matching stage usage from request records.
	var priced, unpriced, costErrors int
	grouped := make(map[stageUsageKey]schemas.StageUsage)
	for _, r := range ledger.records {
		result.TotalTokensInput += r.InputTokens
		result.TotalTokensOutput += r.OutputTokens
		result.TotalTokensCached += r.CachedTokens
		result.TotalTokensCacheWrite += r.CacheWrite
		result.TotalTokensReasoning += r.Reasoning
		if r.CostUSD != nil {
			result.TotalCostUSD += *r.CostUSD
		}
		key := stageUsageKey{r.Stage, r.Iteration}
		if i, ok := stageIndex[key]; ok {
			usage := grouped[key]
			usage.InputTokens += r.InputTokens
			usage.OutputTokens += r.OutputTokens
			usage.CachedInputTokens += r.CachedTokens
			usage.CacheWriteTokens += r.CacheWrite
			usage.ReasoningTokens += r.Reasoning
			grouped[key] = usage
			result.Stages[i].TokensInput += r.InputTokens
			result.Stages[i].TokensOutput += r.OutputTokens
			result.Stages[i].TokensCached += r.CachedTokens
			result.Stages[i].TokensCacheWrite += r.CacheWrite
			result.Stages[i].TokensReasoning += r.Reasoning
			if r.CostUSD != nil {
				result.Stages[i].CostUSD += *r.CostUSD
			}
		}
		switch r.CostStatus {
		case schemas.CostStatusPriced:
			priced++
		case schemas.CostStatusUnpriced:
			unpriced++
		case schemas.CostStatusError:
			costErrors++
		}
	}

	for key, got := range grouped {
		want := expected[key]
		if got != want {
			return fmt.Errorf("stage %s iteration %d usage %+v does not match request usage %+v", key.name, key.iteration, want, got)
		}
	}
	for key, want := range expected {
		if _, ok := grouped[key]; !ok && want != (schemas.StageUsage{}) {
			return fmt.Errorf("stage %s iteration %d reported usage %+v without request records", key.name, key.iteration, want)
		}
	}

	// Derive priced/unpriced/error counts and coverage.
	result.PricedRequestCount = priced
	result.UnpricedRequestCount = unpriced
	result.ErrorRequestCount = costErrors

	// Derive coverage.
	total := len(ledger.records)
	switch {
	case total == 0:
		result.CostCoverage = schemas.CostCoverageNotApplicable
	case priced == total:
		result.CostCoverage = schemas.CostCoverageComplete
	case priced > 0:
		result.CostCoverage = schemas.CostCoveragePartial
	default:
		result.CostCoverage = schemas.CostCoverageUnavailable
	}
	return nil
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
	engine := a.WebSearchEngine
	if engine == "" {
		engine = b.WebSearchEngine
	} else if b.WebSearchEngine != "" && b.WebSearchEngine != engine {
		// Keep the first non-empty engine when both sides agree. Mark a mismatch
		// as "mixed" so pricing cannot silently use either engine's rate.
		engine = "mixed"
	}
	return &schemas.StageUsage{
		InputTokens:       a.InputTokens + b.InputTokens,
		OutputTokens:      a.OutputTokens + b.OutputTokens,
		CachedInputTokens: a.CachedInputTokens + b.CachedInputTokens,
		CacheWriteTokens:  a.CacheWriteTokens + b.CacheWriteTokens,
		ReasoningTokens:   a.ReasoningTokens + b.ReasoningTokens,
		WebSearchRequests: a.WebSearchRequests + b.WebSearchRequests,
		WebSearchEngine:   engine,
	}
}

func completionSummary(result schemas.PipelineResult) string {
	summary := fmt.Sprintf("Pipeline %s after %d stage record(s).", result.Status, len(result.Stages))
	if result.AbortReason != nil && strings.TrimSpace(*result.AbortReason) != "" {
		summary += " " + strings.TrimSpace(*result.AbortReason) + "."
	}
	return summary + "\n"
}
