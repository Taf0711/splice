package splice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/projectdocs"
	"github.com/Taf0711/splice/internal/redaction"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/cognition"
	"github.com/Taf0711/splice/internal/splice/learn"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/worktrees"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const (
	defaultMaxIterations = 5
	// defaultMaxWall bounds one run when the caller sets no MaxWall.
	defaultMaxWall = 600 * time.Second

	// stepBackOutputMax bounds the auxiliary step-back request. The analysis is
	// a small typed tool call, so it gets its own per-request cap instead of
	// the provider default, matching the bounded-output rule for stage calls.
	stepBackOutputMax = 2048
)

var errWallTimeExceeded = errors.New("wall time exceeded")

type requestLedger struct {
	records []schemas.PipelineUsageRecord
}

func newRequestLedger() *requestLedger { return &requestLedger{} }

// spendSourceForInvocation is the F1 mechanical source classification for a
// stage invocation before any expansion round: ordinal 0 is the initial
// generation pass, ordinal 1+ are repair re-entries. The D2 loop reclassifies
// round 1+ as expansion spend (see runStageWithContextBudgeted). Auxiliary
// calls (step_back) and capture-time calls, if any ever exist, set their own
// source explicitly.
func spendSourceForInvocation(invocationOrdinal int) string {
	if invocationOrdinal > 0 {
		return schemas.SpendSourceRepair
	}
	return schemas.SpendSourceGeneration
}

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
		// usageTrusted is true only when a normalized provider report exists.
		// cache_hit stays nil otherwise, because a missing report is not a miss.
		usageTrusted := false
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
				usageTrusted = true
				attributed.Cost = ResolveRequestCostFor(options.EstimateUsageCost, options.EstimateServedUsageCost, attributed)
			}
		} else {
			attributed.Usage = zeroruntime.Usage{}
			attributed.Cost = estimateUsageCost(options.EstimateUsageCost, options.EstimateServedUsageCost, attributed)
		}
		if err := validateUsageCostEstimate(attributed.Cost); err != nil {
			attributed.Cost = costError("invalid cost estimate: " + err.Error())
		}

		var cacheHit *bool
		if usageTrusted {
			hit := attributed.Usage.CachedInputTokens > 0
			cacheHit = &hit
		}
		record := schemas.PipelineUsageRecord{
			Sequence:          attributed.Sequence,
			Provider:          attributed.ProviderName,
			Model:             attributed.Model,
			Stage:             attributed.Stage,
			Iteration:         attributed.Iteration,
			InvocationOrdinal: attributed.InvocationOrdinal,
			ContextRound:      attributed.ContextRound,
			SpendSource:       attributed.SpendSource,
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
			RequestID:         attributed.RequestID,
			PricingSource:     attributed.Cost.PricingSource,
			PricingAsOf:       attributed.Cost.PricingAsOf,
			UnpricedReason:    attributed.Cost.UnpricedReason,
			PromptLayoutHash:  attributed.PromptLayoutHash,
			CacheHit:          cacheHit,
			MemoryPosition:    schemas.MemoryPositionAfterPrefix,
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

// estimateUsageCost prices one request. A served estimator prices it from the
// catalog of the provider that served it. Without one, the legacy estimator
// prices it, and a request with neither is unpriced.
func estimateUsageCost(estimator func(string, agent.Usage, bool) agent.UsageCostEstimate, served agent.ServedUsageCostEstimator, attributed agent.AttributedUsage) agent.UsageCostEstimate {
	if served != nil {
		return served(attributed.ProviderName, attributed.Model, attributed.Usage, attributed.UsageReported)
	}
	if estimator != nil {
		return estimator(attributed.Model, attributed.Usage, attributed.UsageReported)
	}
	reason := "usage not reported by provider"
	if attributed.UsageReported {
		reason = "cost estimator unavailable"
	}
	return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: reason}
}

// ResolveRequestCost is the one cost rule for a streamed request, shared by the
// pipeline ledger and the loop ledger, so one request gets one cost on both
// paths. A provider-reported charge above zero wins (provenance reported). A
// reported zero on a request that used tokens is not trusted: OpenRouter sends
// cost 0 for BYOK requests, so the catalog estimate decides instead. The one
// exception is a free model (zeroReportIsFree): its zero is the real price.
// Without a report, the catalog estimate decides.
func ResolveRequestCost(estimator func(string, agent.Usage, bool) agent.UsageCostEstimate, attributed agent.AttributedUsage) agent.UsageCostEstimate {
	return ResolveRequestCostFor(estimator, nil, attributed)
}

// ResolveRequestCostFor applies the cost rule with the served estimator when
// one is given, so the request is priced from its serving provider's catalog.
func ResolveRequestCostFor(estimator func(string, agent.Usage, bool) agent.UsageCostEstimate, served agent.ServedUsageCostEstimator, attributed agent.AttributedUsage) agent.UsageCostEstimate {
	// A flat-subscription request is never priced per request, whatever it reports.
	if served != nil {
		if probe := served(attributed.ProviderName, attributed.Model, attributed.Usage, attributed.UsageReported); probe.Subscription {
			return probe
		}
	}
	if attributed.ReportedCostUSD == nil {
		return estimateUsageCost(estimator, served, attributed)
	}
	reported := *attributed.ReportedCostUSD
	tokens := attributed.Usage.EffectiveInputTokens() + attributed.Usage.EffectiveOutputTokens()
	if reported > 0 || tokens == 0 {
		return reportedUsageCost(reported)
	}
	// Zero charge on a request with usage: a catalog price is used when one
	// exists. Otherwise the request stays unpriced, never a priced zero.
	estimate := estimateUsageCost(estimator, served, attributed)
	if zeroReportIsFree(attributed.Model, estimate) {
		return reportedUsageCost(0)
	}
	if estimate.Status != agent.CostStatusPriced {
		estimate.UnpricedReason = "provider reported a zero charge for a request with usage; " + estimate.UnpricedReason
	}
	return estimate
}

// zeroReportIsFree reports whether a zero provider charge on a request with
// usage is the model's real price. That holds for a ":free" model id, and for a
// model whose serving catalog entry lists it at zero (the estimate is priced 0).
// A catalog entry that prices above zero, or no entry, does not qualify.
func zeroReportIsFree(model string, estimate agent.UsageCostEstimate) bool {
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(model)), ":free") {
		return true
	}
	return estimate.Status == agent.CostStatusPriced && estimate.CostUSD != nil && *estimate.CostUSD == 0
}

func costError(reason string) agent.UsageCostEstimate {
	return agent.UsageCostEstimate{Status: agent.CostStatusError, UnpricedReason: reason}
}

// decisionAdvisorStage is the ledger stage name of decision-advisor requests.
// It is not a pipeline stage, so the stage-usage reconciliation leaves these
// records alone while the run totals count them.
const decisionAdvisorStage = "decision_advisor"

// decisionUsageRecord is the ledger record of one decision request, with no
// catalog to price a request that carries no report.
func decisionUsageRecord(pack string, iteration int, res zeroruntime.DecisionResult) schemas.PipelineUsageRecord {
	return decisionUsageRecordFor(pack, iteration, res, "", nil)
}

// decisionUsageRecordFor prices a decision request. A reported charge wins, as
// before. With no report, the request is priced from the catalog of the profile
// that served it (served), so a decision is never priced from another provider.
func decisionUsageRecordFor(pack string, iteration int, res zeroruntime.DecisionResult, profile string, served agent.ServedUsageCostEstimator) schemas.PipelineUsageRecord {
	cost := agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "the decision backend reported no charge"}
	decisionUsage := zeroruntime.Usage{InputTokens: res.InputTokens, OutputTokens: res.OutputTokens}
	// A request with no tokens reported (a failed or empty call) has no usage to
	// price, so it is not reported as priced.
	usageReported := res.InputTokens+res.OutputTokens > 0
	if served != nil && profile != "" {
		if probe := served(profile, res.ResolvedModel, decisionUsage, usageReported); probe.Subscription {
			// A ChatGPT subscription request is unpriced whatever it reports.
			cost = probe
			res.ReportedCostUSD = nil
		} else if res.ReportedCostUSD == nil {
			cost = probe
		}
	}
	if res.ReportedCostUSD != nil {
		if *res.ReportedCostUSD > 0 || res.InputTokens+res.OutputTokens == 0 || zeroReportIsFree(res.ResolvedModel, agent.UsageCostEstimate{}) {
			cost = reportedUsageCost(*res.ReportedCostUSD)
		} else {
			// Same rule as ResolveRequestCost: a zero charge on a request with
			// usage is not trusted, and this path has no catalog estimate.
			cost = agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "provider reported a zero charge for a request with usage"}
		}
	}
	record := schemas.PipelineUsageRecord{
		Provider:       res.Provider,
		Model:          res.ResolvedModel,
		Stage:          decisionAdvisorStage,
		Iteration:      max(iteration, 1),
		SpendSource:    schemas.SpendSourceDecision,
		DecisionPack:   pack,
		UsageReported:  usageReported,
		InputTokens:    res.InputTokens,
		OutputTokens:   res.OutputTokens,
		CostStatus:     cost.Status,
		CostProvenance: cost.Provenance,
		RequestID:      res.RequestID,
		PricingSource:  cost.PricingSource,
		PricingAsOf:    cost.PricingAsOf,
		UnpricedReason: cost.UnpricedReason,
	}
	if cost.CostUSD != nil {
		c := *cost.CostUSD
		record.CostUSD = &c
	}
	return record
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

// resolveRunAdvisor builds the decision advisor for a run from the flags, the
// decision_advisor config section, and the environment, and sets cfg.Advisor.
// When report is true it also prints the trajectory classifier's state. A
// design plan builds a fresh advisor for each task, as each task is its own
// run, and reports once for the plan.
func resolveRunAdvisor(options agent.Options, cfg *PipelineRunConfig, report bool) error {
	// Decision advisor (Jev M4): constructed from the flag gate, the mode
	// environment, and the CLI-built evaluator. Flag off or mode off
	// returns nil: the advisor is absent and today's behavior is
	// byte-identical. An explicit but invalid configuration fails loud
	// here, before any stage runs.
	advisor, err := resolveDecisionAdvisor(options.Flags, options.DecisionEvaluator, options.DecisionAdvisorConfig, os.Getenv)
	if err != nil {
		return fmt.Errorf("decision advisor: %w", err)
	}
	cfg.Advisor = advisor
	// The classifier's state is reported once per run: the reason it is off,
	// or the profile and model it runs on.
	if report && options.Flags.Enabled(flags.TrajectoryClassifierEnabled) {
		switch {
		case advisor != nil && advisor.classifierOff:
		case advisor != nil && advisor.selectorOff:
			emitProgress(*cfg, fmt.Sprintf("[trajectory] classifier: %s (%s)\n", options.DecisionAdvisorConfig.ProviderProfile, options.DecisionAdvisorConfig.Profile))
		case advisor == nil && options.DecisionAdvisorConfig.TrajectoryDefault:
			emitProgress(*cfg, "[trajectory] classifier: rules only (the ChatGPT sign-in profile could not start)\n")
		case advisor == nil && options.DecisionAdvisorConfig.Mode == "":
			emitProgress(*cfg, "[trajectory] classifier: rules only (no ChatGPT sign-in)\n")
		}
	}
	return nil
}

// ErrInvalidRunInput marks a Run error that the run input or config causes:
// a topology that does not load or validate, a plan that does not build, or a
// node pin or stage route that does not resolve. Run finds each one before
// any provider request. A provider that cannot start for a valid route (a
// missing key or token) is not invalid input. Test for it with errors.Is.
var ErrInvalidRunInput = errors.New("invalid run input")

// runInputError keeps the message of err and adds ErrInvalidRunInput to the
// error chain.
type runInputError struct{ err error }

func (e runInputError) Error() string   { return e.err.Error() }
func (e runInputError) Unwrap() []error { return []error{e.err, ErrInvalidRunInput} }

// preflightRunError marks a preflight failure as invalid input. A provider
// that cannot start for a valid route is not invalid input: that error keeps
// the provider-failure exit.
func preflightRunError(err error) error {
	var build *providerBuildError
	if errors.As(err, &build) {
		return err
	}
	return runInputError{err}
}

// Run executes the Splice deterministic pipeline for a user prompt.
// It mirrors agent.Run's signature so it can be swapped in at the TUI/CLI seams.
// Run executes one prompt through the pipeline. It brackets the run with the
// run start and end events on Options.EventSink.
func Run(ctx context.Context, prompt string, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery) (agent.Result, error) {
	return bracketRun(options, func() (agent.Result, error) {
		return runPipeline(ctx, prompt, provider, options, mem, rec)
	})
}

// bracketRun emits the run start and end around one run on the event sink. Both
// Run and RunDesignPlanWithResume use it. The redaction options are built here,
// once per run.
func bracketRun(options agent.Options, run func() (agent.Result, error)) (agent.Result, error) {
	secrets := append([]string(nil), options.SecretValues...)
	emitSink(options.EventSink, secrets, agent.StageEvent{Kind: "run", Status: "running"})
	result, err := run()
	end := agent.StageEvent{Kind: "run", Status: "completed"}
	if err != nil {
		end.Status = "failed"
		end.Detail = err.Error()
	}
	emitSink(options.EventSink, secrets, end)
	return result, err
}

func runPipeline(ctx context.Context, prompt string, provider agent.Provider, options agent.Options, mem MemoryStore, rec WorkspaceRecovery) (agent.Result, error) {
	if _, err := ResolveExpansionRounds(); err != nil {
		return agent.Result{}, runInputError{err}
	}
	runID := options.SessionID
	if runID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		runID = "run-" + hex.EncodeToString(b)
	}

	topology, topologySource, topologyWarnings, err := resolveRunTopology(options)
	if err != nil {
		return agent.Result{}, runInputError{fmt.Errorf("resolve topology: %w", err)}
	}
	plan, err := BuildExecutionPlanWithTopology(topology, prompt, options.Flags)
	if err != nil {
		return agent.Result{}, runInputError{fmt.Errorf("build plan: %w", err)}
	}
	if err := plan.Validate(); err != nil {
		return agent.Result{}, runInputError{fmt.Errorf("validate plan: %w", err)}
	}

	cfg := PipelineConfigFromAgentOptions(options)
	// Check every node pin and stage route before any provider request, so a
	// model that cannot resolve stops the run before earlier stages spend
	// tokens.
	if err := preflightNodePins(plan, cfg); err != nil {
		return agent.Result{}, preflightRunError(err)
	}
	if err := preflightStageRoutes(plan, cfg); err != nil {
		return agent.Result{}, preflightRunError(err)
	}
	// Load repository instructions once for this run. Cwd is the task worktree
	// when the caller requested workspace isolation.
	instructionDir := cfg.Cwd
	if instructionDir == "" {
		instructionDir = cfg.ProjectRoot
	}
	cfg.ProjectInstructions = projectdocs.Load(instructionDir)
	if err := resolveRunAdvisor(options, &cfg, true); err != nil {
		return agent.Result{}, err
	}
	if advisor := cfg.Advisor; os.Getenv(DecisionRecordsDirEnvVar) != "" && advisor != nil {
		dir := os.Getenv(DecisionRecordsDirEnvVar)
		defer func() {
			if err := writeDecisionRecords(dir, runID, advisor.Records()); err != nil {
				emitProgress(cfg, fmt.Sprintf("[decision advisor] warning: cannot write decision records: %v\n", err))
			}
		}()
	}
	for _, warning := range topologyWarnings {
		emitNotice(cfg, sanitizeNoticeLine("[topology] warning: "+warning))
	}
	if topologySource != TopologySourceDefault {
		emitNotice(cfg, sanitizeNoticeLine("[topology] active pipeline from "+topologySource))
	}
	if cfg.MaxWall > 0 && cfg.MaxWall != defaultMaxWall {
		emitNotice(cfg, fmt.Sprintf("[limits] wall-time limit %s (default %s)", cfg.MaxWall, defaultMaxWall))
	}
	emitCommandNodeNotices(cfg, topology)
	emitAgentNodeNotices(cfg, topology)
	if err := preflightAgentNodeSandbox(cfg, topology); err != nil {
		return agent.Result{}, err
	}
	if err := preflightHeadlessAskAgentNodes(cfg, topology); err != nil {
		return agent.Result{}, runInputError{err}
	}
	// Workspace isolation (DoD 26): a run whose Cwd is a worktree path
	// distinct from the stable repo root is an isolated lane; stage events
	// stamp that so the sidebar can badge the lane honestly.
	if cfg.ProjectRoot != "" && cfg.Cwd != cfg.ProjectRoot {
		cfg.IsolatedWorktree = cfg.Cwd
	}
	// F4: one digest memo per run. Same stage need re-hashes only when new
	// evidence (a recorded mutation) invalidates the cached content version.
	memo := newDigestMemo()
	SetRunDigestMemo(memo)
	defer SetRunDigestMemo(nil)
	result, err := runExecutionPlan(ctx, runID, plan, topology, provider, cfg, mem, rec)
	if err != nil {
		return agent.Result{}, err
	}

	finalAnswer, _ := json.MarshalIndent(result, "", "  ")
	emitText(cfg, completionSummary(result))
	return agent.Result{
		FinalAnswer:      string(finalAnswer),
		Incomplete:       result.Status != "completed",
		IncompleteReason: abortReason(result),
		Verified:         result.Verified,
		UnverifiedReason: result.UnverifiedReason,
	}, nil
}

// preflightNodePins resolves every node model pin of the plan against the
// run's providers. It builds providers but sends no request. The first pin
// that cannot resolve fails the run and names the stage.
func preflightNodePins(plan schemas.ExecutionPlan, options PipelineRunConfig) error {
	for _, stage := range plan.Stages {
		if stage.Model == nil {
			continue
		}
		if options.NodeModelResolver == nil {
			return fmt.Errorf("preflight: stage %s model: node model pin has no resolver configured", stage.Name)
		}
		resolved, err := options.NodeModelResolver(stage.Name, nodeModelOverride(stage.Model))
		if err != nil {
			return fmt.Errorf("preflight: stage %s model: %w", stage.Name, err)
		}
		if resolved.Provider == nil {
			return fmt.Errorf("preflight: stage %s model: node model pin resolved no provider", stage.Name)
		}
	}
	return nil
}

// preflightStageRoutes resolves the stage route (stage-models.json) of every
// model-backed stage that has no node pin, as the stage loop does. It builds
// providers but sends no request. The first route that cannot resolve fails
// the run and names the stage. A tier fallback is not an error here: that
// stage runs on the run model and reports it when it starts.
func preflightStageRoutes(plan schemas.ExecutionPlan, options PipelineRunConfig) error {
	if options.StageModelResolver == nil {
		return nil
	}
	for _, stage := range plan.Stages {
		if stage.Model != nil || stageModelFree(stage) {
			continue
		}
		if _, err := options.StageModelResolver(stage.Name); err != nil {
			var tierFallback *stageTierFallback
			if errors.As(err, &tierFallback) {
				continue
			}
			return fmt.Errorf("preflight: stage %s model: %w", stage.Name, err)
		}
	}
	return nil
}

// runExecutionPlan runs one compiled plan. topology is the topology the plan
// was compiled from, so the runtime stage registry can resolve the custom
// nodes in that plan.
func runExecutionPlan(ctx context.Context, runID string, plan schemas.ExecutionPlan, topology *schemas.PipelineTopology, provider agent.Provider, options PipelineRunConfig, mem MemoryStore, rec WorkspaceRecovery) (schemas.PipelineResult, error) {
	if _, err := ResolveExpansionRounds(); err != nil {
		return schemas.PipelineResult{}, err
	}
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

	// Topology compile warnings: a non-fatal coupling problem in the plan's
	// graph is surfaced once at run start, never silently.
	for _, warning := range plan.Warnings {
		emitNotice(options, sanitizeNoticeLine("[topology] warning: "+warning))
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

	registry, err := buildStageRegistry(options, absWorkDir, topology)
	if err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("build stage registry: %w", err)
	}

	// Presentation emission: when a consumer is wired, create the snapshot
	// accumulator and wrap the event callbacks so every plan and stage event
	// flows through it. The accumulator is observability, not control flow: a
	// refused event is counted and logged, never allowed to abort the run.
	options, acc := wirePresentation(options, plan)
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
	// SPLICE_BUDGET_FIT=off freezes every stage at its static budget with
	// named provenance: warm/cold evaluation arms must never receive
	// different effective budgets from learned fitting, so the harness sets
	// this and the trace records that it did. Invalid values fail loud.
	budgetProvenance := map[string]string{}
	budgetFitFrozen := !options.Flags.Enabled(flags.PipelineBudgetFit)
	toolFingerprint := ""
	topologyHash := ""
	stagePromptHashes := map[string]string{}
	if tracer != nil {
		if budgetFitFrozen {
			for i := range plan.Stages {
				if registry[plan.Stages[i].Name].Capabilities().ModelFree {
					continue
				}
				budgetProvenance[plan.Stages[i].Name] = "budget frozen: SPLICE_BUDGET_FIT=off (static default applied)"
			}
		} else if querier, ok := mem.(learn.TraceQuerier); ok && querier != nil {
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
				promptHash := learn.Hash(stages.StagePrompt(stageRole(*stage)))
				stagePromptHashes[stage.Name] = promptHash
				// Deliberate: the learn key uses the node name, so each node
				// keeps its own learned budget, including a renamed one.
				key := budgetBucketKey(options, *stage, projectRoot, promptHash, toolFingerprint, topologyHash)
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
		tr = newRunTraceAccumulator(tracer, runID, options.SessionID, projectRoot, plan, memoryStatus, options.TraceWriteWarn)
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

	// The stage sandbox engine and the strict read-before-write flag are set
	// in buildStageToolRunner before the tool runner is built, so the
	// runner's captured config carries them.
	options, runner := buildStageToolRunner(options, absWorkDir)

	options.ClassifierPolicy = newHostTrajectoryPolicy(options.Advisor, options.TrajectoryConfig, options.IsolatedWorktree != "")
	ledger := newRequestLedger()
	ledgerOpts := ledger.recordingOptions(options)
	// The advisor's own requests are run spend: they join the ledger, so the
	// run totals and the cost report count them (spend source "decision").
	if options.Advisor != nil {
		// A provider-backed advisor is served by its configured profile. Other
		// backends have no profile, so their unreported requests stay unpriced.
		servingProfile := ""
		if options.DecisionAdvisorConfig.Backend == "provider" {
			servingProfile = options.DecisionAdvisorConfig.ProviderProfile
		}
		options.Advisor.usageSink = func(pack string, iteration int, res zeroruntime.DecisionResult) {
			ledger.append(decisionUsageRecordFor(pack, iteration, res, servingProfile, options.EstimateServedUsageCost))
		}
	}

	if mem != nil {
		obs := buildConfigObservation(runID, projectRoot, plan)
		persistObservation(ctx, mem, obs, func(msg string) {
			emitProgress(options, fmt.Sprintf("[orchestrator] %s", msg))
		})
	}

	// The graph client arrives through the mem MemoryStore when the concrete
	// type carries one (the memd-backed store does); a plain store skips it.
	type graphClientProvider interface{ GraphClient() *memd.Client }
	var graphClient *memd.Client
	if provider, ok := mem.(graphClientProvider); ok {
		graphClient = provider.GraphClient()
	}
	// The run's own changes are the diff from the tree before the run, not
	// from HEAD: uncommitted work of an earlier run is not this run's change.
	preRunTree := ""
	if graphClient != nil {
		preRunTree, _ = worktrees.SnapshotTree(ctx, absWorkDir)
	}

	result, err := runIterationLoop(ctx, runID, plan, registry, provider, ledgerOpts, absWorkDir, runner, mem, rec, tr, acc, ledger)
	if err == nil {
		finishPresentation(acc, options, result)
	}
	if err != nil {
		return schemas.PipelineResult{}, err
	}
	result.TopologyName = plan.TopologyName

	if err := applyRequestLedger(&result, ledger); err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("apply request ledger: %w", err)
	}
	if result.PromptLayoutFlips > 0 {
		emitProgress(options, fmt.Sprintf("[orchestrator] the cacheable prompt prefix changed %d time(s) mid-run; the provider prefix cache was forfeited", result.PromptLayoutFlips))
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
			tr.noteTraceWriteFailed()
			emitProgress(options, fmt.Sprintf("[trace] write skipped: %v", writeErr))
		}
	}

	// Graph capture: persist verified-run cognition (procedure + file facts)
	// through the sidecar graph. Gated on completion so only VERIFIED work
	// becomes durable; evidence rows carry the run id and revision. A
	// sidecar failure degrades to cold (warning), never fails the run.
	if graphClient != nil {
		snapshot, anchorReason := verifiedRevision(ctx, absWorkDir)
		if anchorReason != "" {
			emitProgress(options, fmt.Sprintf("[cognition] anchored at HEAD (%s)", anchorReason))
		}
		changedFiles := runChangedFiles(ctx, absWorkDir, preRunTree)
		captures := captureFromVerifiedRunVerified(
			projectRoot,
			captureOutcomeStatus(result),
			changedFiles,
			pipelineTestCommand(result, roleStageName(plan, "test_runner")),
			snapshot,
			runID,
			captureVerificationFromResult(result, roleStageName(plan, "test_runner")),
			CaptureOriginRuntime,
		)
		if delivered, hits, misses := tr.memoryHealth(changedFiles); delivered > 0 || misses > 0 {
			emitProgress(options, fmt.Sprintf("[memory] delivered %d file(s), %d changed by the run, %d requested in expansions that memory did not deliver", delivered, hits, misses))
		}
		if record, ok := workRecordCapture(plan.RequestIntent, tr.requestedPathList(), changedFiles, captures); ok {
			captures = append(captures, record)
		}
		for _, capture := range captures {
			id, capErr := persistGraphCapture(ctx, graphClient, capture)
			if capErr != nil {
				emitProgress(options, fmt.Sprintf("[cognition] capture skipped: %v", capErr))
				break
			}
			emitProgress(options, fmt.Sprintf("[cognition] captured node %d (%s)", id, capture.Kind))
		}
	}
	return result, nil
}

// runChangedFiles lists the files the run changed: the content diff from
// the tree before the run to the worktree now. Without that tree it falls
// back to the worktree's changes against HEAD.
func runChangedFiles(ctx context.Context, workDir, preRunTree string) []string {
	if preRunTree != "" {
		if changed, err := cognition.ChangedPaths(ctx, workDir, preRunTree, nil); err == nil {
			files := make([]string, 0, len(changed))
			for path := range changed {
				files = append(files, path)
			}
			sort.Strings(files)
			return files
		}
	}
	return worktreeChangedFiles(ctx, workDir)
}

// worktreeChangedFiles lists the repo-relative files that differ from HEAD
// (modified, added, or renamed) via one porcelain git status. This is the
// deterministic changed-file source for graph capture: the verified run's
// own edits, exactly what a freshness diff will later compare against.
// Untracked files are included (git status --porcelain default) because the
// run may have created new files. Empty output on any failure: capture then
// persists only the procedure node, never wrong file facts.
func worktreeChangedFiles(ctx context.Context, workDir string) []string {
	cmd, done, cerr := prepareGitRead(ctx, workDir, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all")
	if cerr != nil {
		return nil
	}
	defer done()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range parseStatusZ(out) {
		files = append(files, f.Path)
	}
	return files
}

// pipelineTestCommand extracts the test command a completed run verified
// with, from the test_runner stage records. Empty when no test stage ran.
func pipelineTestCommand(result schemas.PipelineResult, testStage string) string {
	for i := len(result.Stages) - 1; i >= 0; i-- {
		stage := result.Stages[i]
		if stage.Name != testStage || stage.Status != schemas.StageCompleted {
			continue
		}
		return stage.TestCommand
	}
	return ""
}

// testCommandFromOutput returns the expanded argv recorded by test_runner.
// The stage output is the source of truth because detection and configured
// placeholders are resolved before the command runs.
func testCommandFromOutput(output schemas.HarnessStageOutput) string {
	raw, ok := output.Data["test_command"]
	if !ok {
		return ""
	}
	switch command := raw.(type) {
	case []string:
		return strings.Join(command, " ")
	case []any:
		parts := make([]string, 0, len(command))
		for _, item := range command {
			if part, ok := item.(string); ok {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, " ")
	case string:
		return command
	default:
		return ""
	}
}

// captureOutcomeStatus maps a run to the outcome the capture contract reads.
// Durable cognition needs a verified run, not only a completed one, so a
// completed run that verified nothing maps to "unverified", which captures
// nothing.
func captureOutcomeStatus(result schemas.PipelineResult) string {
	if result.Status == "completed" && !result.Verified {
		return "unverified"
	}
	return result.Status
}

// captureVerificationFromResult extracts the E2 verification observation
// from the pipeline result: did the applicable required checks EXECUTE and
// PASS? Stage status completed is not evidence; the counts come from the
// final iteration state's executed totals. A run with zero executed checks
// yields a zero-value observation, which capture labels provisional (or
// unverified for legacy callers), never verified. Skipped facts do not
// count: only acceptance facts that actually executed and passed do.
func captureVerificationFromResult(result schemas.PipelineResult, testStage string) captureVerification {
	ver := captureVerification{}
	for i := len(result.Stages) - 1; i >= 0; i-- {
		stage := result.Stages[i]
		if stage.Name != testStage || stage.Status != schemas.StageCompleted {
			continue
		}
		ver.TestStageRan = true
		ver.TestCommand = pipelineTestCommand(result, testStage)
		break
	}
	if state := result.FinalState; state != nil {
		ver.TestsExecuted = state.TestsPassing + state.TestsFailing + state.TestsErrored
		ver.TestsFailed = state.TestsFailing + state.TestsErrored
		ver.AcceptanceTotal = state.AcceptanceFactsPassing + state.AcceptanceFactsFailing
		ver.AcceptancePassed = state.AcceptanceFactsPassing
	}
	if ver.TestStageRan {
		ver.EnvironmentStdLib = true
		ver.ObservedResult = fmt.Sprintf("executed %d test(s), %d failed", ver.TestsExecuted, ver.TestsFailed)
	}
	return ver
}

// headRevision returns the current git HEAD sha of the working directory,
// or "" when unavailable (no repo, git failure). The read routes through
// the stage sandbox profile like every other deterministic git read.
func headRevision(ctx context.Context, workDir string) string {
	revCmd, done, cerr := prepareGitRead(ctx, workDir, "rev-parse", "HEAD")
	if cerr != nil {
		return ""
	}
	defer done()
	out, err := revCmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// verifiedRevision returns a git revision object naming the CURRENT worktree
// bytes: `git stash create` records the staged+unstaged state as a commit
// object WITHOUT touching HEAD, the index, or the working tree (it is the
// plumbing behind git stash and has no visible side effects beyond an
// unreferenced commit object). Captured cognition anchors at that snapshot,
// so a later freshness diff compares against the exact tree the run verified
// rather than the pre-run HEAD. Empty worktrees (nothing changed) return ""
// from stash create, and the caller falls back to HEAD so the anchor still
// names a resolvable revision. Any failure falls back to headRevision; a
// missing anchor degrades capture to the pre-run base revision, never fails
// the run.
//
// The returned reason names WHY a snapshot was unavailable ("snapshot
// unavailable: <cause>") so the capture progress line can say "anchored at
// HEAD (snapshot unavailable: ...)" instead of silently anchoring at HEAD.
// An anchor at HEAD is correct when the caller committed the verified tree
// before capturing (the eval harness does); it is a degradation, visible in
// telemetry, when the worktree was dirty and the snapshot failed.
//
// The host snapshot (worktrees.SnapshotCommit) runs first. Inside the stage
// sandbox, `git stash create` cannot write objects and always failed: all 61
// captures of the 2026-09-25 D0 campaign anchored at HEAD, so no capture of
// a changed file was ever fresh on a later run. The host snapshot also
// includes new untracked files, which `git stash create` leaves out.
func verifiedRevision(ctx context.Context, workDir string) (revision string, reason string) {
	if snapshot, err := worktrees.SnapshotCommit(ctx, workDir, "splice cognition capture snapshot"); err == nil {
		return snapshot, ""
	}
	fallback := func(why string) (string, string) {
		return headRevision(ctx, workDir), why
	}
	snapCmd, done, cerr := prepareGitRead(ctx, workDir, "stash", "create")
	if cerr != nil {
		return fallback(fmt.Sprintf("snapshot unavailable: %v", cerr))
	}
	defer done()
	out, err := snapCmd.Output()
	if err != nil {
		return fallback(fmt.Sprintf("snapshot unavailable: %v", err))
	}
	snapshot := strings.TrimSpace(string(out))
	if snapshot == "" {
		// Nothing to stash: the worktree is clean, so HEAD names the
		// verified bytes exactly. This is the correct, expected path when
		// the caller committed the verified tree before capture.
		return headRevision(ctx, workDir), ""
	}
	return snapshot, ""
}

// maxNoticeChars bounds one pipeline notice line. Warnings carry file paths,
// so the cap leaves room for two long paths.
const maxNoticeChars = 1024

// emitCommandNodeNotices names each command node the topology will run, before
// any stage runs. Trust is per directory, so a pulled topology can add commands
// the operator has not reviewed.
func emitCommandNodeNotices(options PipelineRunConfig, topology *schemas.PipelineTopology) {
	if topology == nil {
		return
	}
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeCommand {
			continue
		}
		line := fmt.Sprintf("[topology] command node %s will run: %s", node.Name, stages.CommandLine(node.Command))
		emitNotice(options, sanitizeNoticeLine(line))
	}
}

// emitAgentNodeNotices names each agent node the topology will run, with its
// tools and permission, before any stage runs. An agent node spends model
// calls and can edit files through its tools, so it is disclosed like a command.
func emitAgentNodeNotices(options PipelineRunConfig, topology *schemas.PipelineTopology) {
	if topology == nil {
		return
	}
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeAgent {
			continue
		}
		line := fmt.Sprintf("[topology] agent node %s will run: %s", node.Name, agentNodeRunLine(node, options.PermissionMode))
		emitNotice(options, sanitizeNoticeLine(line))
	}
}

// preflightAgentNodeSandbox refuses a run whose topology has an agent node
// when the run sandbox does not enforce. The run stops before the first
// provider request, with the error the node itself would return.
func preflightAgentNodeSandbox(options PipelineRunConfig, topology *schemas.PipelineTopology) error {
	if topology == nil {
		return nil
	}
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeAgent {
			continue
		}
		if err := agentNodeSandboxError(options.Sandbox); err != nil {
			return fmt.Errorf("agent node %s: %w", node.Name, err)
		}
	}
	return nil
}

// preflightHeadlessAskAgentNodes refuses a run that cannot ask for approval
// (no permission prompt handler) when an agent node asks before each
// mutating call and its allowlist has a mutating tool. Each such call would
// be denied, so the run stops before the first provider request. An ask node
// with only read-only tools still runs, because read-only tools do not ask.
func preflightHeadlessAskAgentNodes(options PipelineRunConfig, topology *schemas.PipelineTopology) error {
	if topology == nil || options.OnPermissionRequest != nil || options.Registry == nil {
		return nil
	}
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeAgent || effectiveAgentPermission(options.PermissionMode, node.Permission) != agent.PermissionModeAsk {
			continue
		}
		for _, name := range node.Tools {
			// A tool the registry does not know fails the node at start.
			tool, ok := options.Registry.Get(name)
			if !ok {
				continue
			}
			switch tool.Safety().SideEffect {
			case tools.SideEffectRead, tools.SideEffectNone:
				continue
			}
			return fmt.Errorf("agent node %s asks before writes, commands, and network calls, but this run cannot ask; set its permission to auto or run it in the TUI.", node.Name)
		}
	}
	return nil
}

// emitNotice sends one notice line to OnNotice, or to the progress stream when
// no notice callback is set (headless runs).
func emitNotice(options PipelineRunConfig, text string) {
	emitSink(options.EventSink, options.SecretValues, agent.StageEvent{Kind: "notice", Detail: text})
	if options.OnNotice != nil {
		options.OnNotice(text)
		return
	}
	emitProgress(options, text+"\n")
}

// sanitizeNoticeLine drops control characters and caps the line length, so a
// topology value cannot forge extra lines or flood the transcript.
func sanitizeNoticeLine(text string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	return truncateString(clean, maxNoticeChars)
}

// pinnedNodeNames returns the names of plan stages that carry a topology node
// model pin, in plan order.
func pinnedNodeNames(plan schemas.ExecutionPlan) []string {
	var names []string
	for _, stage := range plan.Stages {
		if stage.Model != nil {
			names = append(names, stage.Name)
		}
	}
	return names
}

// budgetBucketKey is the learned-budget key of one model-backed stage. Its
// model is the model the run sends that stage, in the run's order: the node
// pin first, then the per-stage route, then the run model.
func budgetBucketKey(options PipelineRunConfig, stage schemas.ExecutionStage, projectRoot, promptHash, toolFingerprint, topologyHash string) learn.BucketKey {
	return learn.BucketKey{
		RepoRoot:        projectRoot,
		Stage:           stage.Name,
		PromptHash:      promptHash,
		Model:           resolvedModelForStage(options, stage),
		ToolFingerprint: toolFingerprint,
		TopologyHash:    topologyHash,
	}
}

// resolvedModelForStage returns the model name the stage is sent to. A node
// pin's declared model is the model the pin resolves to (the same profile
// build path), so it is read directly: no provider is built for a key.
func resolvedModelForStage(options PipelineRunConfig, stage schemas.ExecutionStage) string {
	if stage.Model != nil && stage.Model.Model != "" {
		return stage.Model.Model
	}
	if options.StageModelResolver != nil {
		if resolved, err := options.StageModelResolver(stage.Name); err == nil && resolved.Provider != nil && resolved.Model != "" {
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
	acc *presentrun.Accumulator,
	ledger *requestLedger,
) (schemas.PipelineResult, error) {
	maxWall := options.MaxWall
	if maxWall <= 0 {
		maxWall = defaultMaxWall
	}
	// Generation-only gate: input volume is bounded per call by compaction
	// and never aborts; the trajectory rule compares generated tokens against
	// TotalOutputBudget (reserve included).
	tokenBudget := plan.TokenBudget.TotalOutputBudget

	history := []schemas.IterationState{}
	allRecords := []schemas.StageRecord{}
	// prevCost is the cumulative billed spend the trajectory cost rule reads
	// as the previous sample. It starts unknown, so the rule stays inactive
	// until two consecutive iterations report complete pricing.
	var prevCost billedCostSnapshot
	wallDeadline := time.Now().Add(maxWall)
	var revisionContext *string
	var priorFailure string

	// escalated tracks whether the escalation model resolver has been called
	// for this run. Escalation fires at most once (AR10c).
	escalated := false

	// MaxTurns applies to agent.Run, not deterministic pipeline passes.
	maxIterations := defaultMaxIterations
	if options.Advisor == nil || options.Advisor.Mode() == AdvisorModeOff {
		options.ClassifierPolicy = nil
	} else if options.ClassifierPolicy == nil {
		options.ClassifierPolicy = newHostTrajectoryPolicy(options.Advisor, options.TrajectoryConfig, options.IsolatedWorktree != "")
	}
	if options.ClassifierPolicy != nil {
		options.ClassifierPolicy.maxIterations = maxIterations
		options.ClassifierPolicy.tokenBudget = tokenBudget
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

	// Per-pass progress (review F7): the workspace content before the first
	// pass, then after each judged pass.
	startChange := summarizeWorkspaceChanges(ctx, workDir)
	lastPassDigest := passDigest(workDir, startChange)
	// Pass evidence (P2.1): the baseline before pass 1. The pass builder is
	// shadow evidence with the flag off, and the state source with it on.
	passTracker := newPassEvidenceTracker(ctx, workDir, startChange)
	options.passEvidence = passTracker
	usePass := options.Flags.Enabled(flags.PassEvidence)
	// prevPassStep is the previous failed pass, for the pass-boundary judgment.
	var prevPassStep *TrajectoryStep
	for i := 1; i <= maxIterations; i++ {
		if ctx.Err() != nil {
			return schemas.PipelineResult{}, context.Canceled
		}
		if !time.Now().Before(wallDeadline) {
			return finishWithReason(runID, plan, allRecords, "aborted", "wall time exceeded")
		}

		emitProgress(options, fmt.Sprintf("Starting pipeline iteration %d\n", i))
		if tr != nil {
			tr.resetSuppressionRecorder()
		}
		if options.ClassifierPolicy != nil {
			options.ClassifierPolicy.history = history
			options.ClassifierPolicy.snapshots = snapshots
		}
		var passNodes []schemas.NodeEvidence
		options.passNodes = &passNodes
		passRecords, passOutputs, completed, err := runPass(ctx, runID, i, plan, registry, provider, options, workDir, runner, wallDeadline, revisionContext, mem, tr, NewStageExecutionBudget(0))
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

		// Measure this pass against the end of the previous pass, whatever the
		// flag says. The HEAD summary is read once and shared by both branches.
		headChange := summarizeWorkspaceChanges(ctx, workDir)
		// The baseline moves only when the pass enters History (see commit).
		measured := passTracker.measure(ctx, i, headChange)
		measured.pair.usedPass = usePass && measured.ok
		primaryChange := headChange
		if measured.pair.usedPass {
			primaryChange = measured.summary
		}
		noteDegradedPass(options, i, measured)
		measured.pair.pass.Nodes = passNodes
		measured.pair.head.Nodes = passNodes
		if measured.ok {
			unclaimed, unseen := passNodeDrift(headChange, measured.pair.pass.FilesChanged, passNodes)
			measured.pair.pass.Unclaimed = unclaimed
			measured.pair.pass.ClaimedUnchanged = unseen
			// The drift is recorded on every run. The notice is for the flag
			// that makes the pass builder the evidence source, so default runs
			// stay quiet about files such as test caches.
			if len(unclaimed) > 0 && usePass {
				emitNotice(options, unclaimedNotice(i, unclaimed))
			}
		}
		headTraceIndex := tr.recordPassEvidence(measured.pair)

		var failedBoundaryDecision *schemas.TrajectoryDecision
		if !completed {
			failed := findFailed(passRecords)
			if options.ClassifierPolicy != nil {
				latestPass := TrajectoryStep{Change: passChange(passRecords, roleStageName(plan, "code_writer")), Failure: passFailure(passRecords, passOutputs), FailureKind: passFailureKind(passRecords, passOutputs)}
				failedState, stateErr := ComputeIterationState(i, passOutputs, passRecords, primaryChange, nil)
				if stateErr != nil {
					recordBoundaryProjectionFailure(options, i, 0, stateErr, passTracker.shadowList(measured.pair), tr)
				} else {
					if measured.pair.usedPass {
						passChanged := len(measured.summary.ChangedFiles) > 0
						failedState.PassChanged = &passChanged
					}
					boundaryHistory := append(append([]schemas.IterationState{}, history...), failedState)
					rules := evaluateTrajectoryJudgmentsFor(boundaryHistory, nil, attributedWriter(options, plan))
					target, targetOK := selectBestSnapshot(snapshots, i)
					boundary := TrajectoryBoundary{
						RunID:          runID,
						ProjectID:      memoryProjectRoot(options, workDir),
						Intent:         plan.RequestIntent,
						WriterStage:    boundaryStage(plan),
						Iteration:      i,
						History:        boundaryHistory,
						EvidenceShadow: passTracker.shadowList(measured.pair),
						Previous:       prevPassStep,
						Latest:         &latestPass,
						Rules:          rules,
						Escalated:      escalated,
						RollbackTarget: targetOK && target.score > ComputeScore(failedState),
					}
					d, classified := classifyBoundary(ctx, options, boundary, tr)
					if classified && d.Action != schemas.ActionContinue {
						failedBoundaryDecision = &d
					}
				}
				prevPassStep = &latestPass
			}
			if failedBoundaryDecision == nil {
				failure := failed.Name + "\x00" + DerefString(failed.OutputSummary)
				if failure == priorFailure {
					reason := fmt.Sprintf("repeated unchanged stage failure (%s) in iterations %d and %d: %s",
						repeatedFailureLabel(plan, failed), i-1, i, failed.Name)
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
		}
		priorFailure = ""

		state, err := ComputeIterationState(i, passOutputs, passRecords, primaryChange, nil)
		if err != nil {
			return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("compute iteration state: %v", err))
		}
		digest := passDigest(workDir, headChange)
		changed := digest != lastPassDigest
		state.PassChanged = &changed
		if measured.pair.usedPass {
			passChanged := len(measured.summary.ChangedFiles) > 0
			state.PassChanged = &passChanged
		}
		lastPassDigest = digest
		measured.pair.head.PassChanged = &changed
		tr.setPassEvidencePassChanged(headTraceIndex, &changed)
		// W2: pair this iteration's host-side suppressions with the
		// correctness signal. A suppression that coincided with a correctness
		// decline is recorded as necessary on the invocation metrics. The
		// pairing is telemetry here: a scope switch that is off by default
		// must not abort a run on a coincident decline.
		if tr != nil && len(history) > 0 {
			if rec := tr.suppressionRecorder(); rec != nil && rec.Suppressed() > 0 {
				pair, pairErr := CheckScopeNonInferiority(rec, history[len(history)-1], state)
				if pairErr != nil {
					emitProgress(options, fmt.Sprintf("[scope] %v\n", pairErr))
				}
				if pair.NecessaryCallsSuppressed > 0 {
					tr.recordNecessarySuppressions(i, pair.NecessaryCallsSuppressed)
				}
			}
		}
		history = append(history, state)
		passTracker.records = append(passTracker.records, measured.pair)
		passTracker.commit(ctx, measured, headChange)
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
			// A completed run projects a VERIFIED receipt, so a pass whose
			// required verification could not run is not a success. The
			// unavailable checker is an environment fact that another model
			// pass cannot repair, so the run fails at once with a reason that
			// names the offending stage.
			if reason, incomplete := incompleteVerificationFailure(passRecords); incomplete {
				return finishWithReason(runID, plan, allRecords, "failed", reason)
			}
			return finishCompleted(runID, plan, allRecords, state, passRecords, passOutputs)
		}

		// W3: the cost rule reads BILLED cost from the authoritative request
		// ledger, never raw tokens, because a cache-read round costs a
		// fraction of a cold round and a token total hides that. The signal is
		// the delta since the previous iteration. When pricing coverage is not
		// complete the billed delta is unknown, so the cost rule stays
		// inactive and the run records why. Missing pricing is never a zero
		// cost.
		costSnapshot, cerr := ledgerCostSnapshot(ledger)
		if cerr != nil {
			return finishWithReason(runID, plan, allRecords, "failed", cerr.Error())
		}
		costSignal, cerr := iterationCostSignal(prevCost, costSnapshot)
		if cerr != nil {
			return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("trajectory cost signal: %v", cerr))
		}
		prevCost = costSnapshot
		allowance := generationAllowance(tokenBudget, len(history))
		var decision schemas.TrajectoryDecision
		if options.ClassifierPolicy != nil {
			decision = evaluateTrajectoryJudgmentsFor(history, costSignal, attributedWriter(options, plan))
		} else if costSignal != nil {
			decision, cerr = evaluateTrajectoryWithCostFor(history, maxIterations, &allowance, costSignal, attributedWriter(options, plan))
			if cerr != nil {
				return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("trajectory cost signal: %v", cerr))
			}
		} else {
			decision = evaluateTrajectoryFor(history, maxIterations, &allowance, nil, attributedWriter(options, plan))
			if costSnapshot.Note != "" {
				emitProgress(options, fmt.Sprintf("[trajectory] cost rule inactive: %s\n", costSnapshot.Note))
			}
		}
		monitorActed := false
		if options.ClassifierPolicy != nil {
			latestPass := TrajectoryStep{Change: passChange(passRecords, roleStageName(plan, "code_writer")), Failure: passFailure(passRecords, passOutputs), FailureKind: passFailureKind(passRecords, passOutputs)}
			if failedBoundaryDecision != nil {
				decision = *failedBoundaryDecision
				monitorActed = true
			} else {
				target, targetOK := selectBestSnapshot(snapshots, i)
				boundary := TrajectoryBoundary{
					RunID:          runID,
					ProjectID:      memoryProjectRoot(options, workDir),
					Intent:         plan.RequestIntent,
					WriterStage:    boundaryStage(plan),
					Iteration:      i,
					History:        history,
					EvidenceShadow: passTracker.shadowList(),
					Previous:       prevPassStep,
					Latest:         &latestPass,
					Rules:          decision,
					Escalated:      escalated,
					RollbackTarget: targetOK && target.score > ComputeScore(state),
				}
				decision, monitorActed = classifyBoundary(ctx, options, boundary, tr)
			}
			prevPassStep = &latestPass
		}
		if monitorActed {
			emitProgress(options, fmt.Sprintf("[trajectory] monitor: %s (%s)\n", decision.Action, strings.Join(decision.Evidence, ", ")))
		}
		if acc != nil && presentrun.IsTrajectoryIntervention(decision.Action) {
			failed := findFailed(passRecords)
			if adapted, ok := presentrun.AdaptTrajectoryDecision(decision.Action, decision.Reason, failed.Name); ok {
				acc.Apply(adapted)
				options.OnPresentationState(acc.Snapshot())
			} else {
				acc.Skip()
			}
		}
		if decision.Action == schemas.ActionContinue {
			rc := buildRevisionContext(plan.RequestIntent, history, passRecords, passOutputs, "")
			revisionContext = &rc
			continue
		}
		degradedRollback := false
		if decision.Action == schemas.ActionRollback {
			if rec == nil {
				// The score regressed, but this run has no isolated recovery.
				// Step back instead of aborting a run the rules would have
				// continued; a failed auxiliary step also continues below.
				degradedRollback = true
				emitProgress(options, fmt.Sprintf("[trajectory] rollback unavailable without --worktree; stepping back: %s\n", decision.Reason))
				decision.Action = schemas.ActionStepBack
			} else {
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
				// The restore rewrote the workspace, so the next pass is measured
				// from the restored state, not from the rejected pass end.
				passTracker.rebase(ctx, summarizeWorkspaceChanges(ctx, workDir))
				emitProgress(options, fmt.Sprintf("[recovery] rejected iteration %d (score=%.1f), restored iteration %d (score=%.1f)\n", i, current.score, target.iter, target.score))
				rc := buildRevisionContext(plan.RequestIntent, history, passRecords, passOutputs, fmt.Sprintf("Rollback: restored iteration %d at score %.1f. %s", target.iter, target.score, decision.Reason))
				revisionContext = &rc
				continue
			}
		}
		if decision.Action == schemas.ActionStepBack {
			if options.ClassifierPolicy != nil {
				options.ClassifierPolicy.stepBackUsed = true
			}
			report := buildStepBackReport(plan.RequestIntent, history, passOutputs, decision)
			sbSelection := agent.ModelSelection{
				Provider:        provider,
				ProviderName:    options.ProviderName,
				Model:           options.Model,
				ReasoningEffort: options.ReasoningEffort,
			}
			// F1: auxiliary model call. step_back runs outside the planned
			// stage sequence, so its spend is classified auxiliary and stays
			// in the ledger (no hidden paid work).
			stageOpts := stageOptions("step_back", i, sbSelection, options, workDir, runner, stages.Capabilities{}, agent.NewRequestAttribution(0, 0, schemas.SpendSourceAuxiliary))
			// Step-back is an auxiliary pipeline call, so it carries the same run
			// controls as a stage call: the resolved model and effort, a bounded
			// per-request output cap, and the pipeline deadline. Without the
			// deadline the request can outlive the wall budget the run reports on.
			stageOpts.ModelOverride = sbSelection.Model
			stageOpts.ReasoningEffort = sbSelection.ReasoningEffort
			stageOpts.MaxOutputTokens = stepBackOutputMax
			stepBackCtx := ctx
			cancelStepBack := func() {}
			if !wallDeadline.IsZero() {
				stepBackCtx, cancelStepBack = context.WithDeadline(ctx, wallDeadline)
			}
			analysis, sbErr := stages.StepBack(stepBackCtx, provider, stageOpts, report)
			cancelStepBack()
			if sbErr != nil {
				if errors.Is(sbErr, context.Canceled) || ctx.Err() != nil {
					return schemas.PipelineResult{}, context.Canceled
				}
				// A replan the monitor asked for must not fail a run the
				// rules would have continued. A rollback degraded to step-back
				// keeps the same rule: the auxiliary call is not the run.
				if monitorActed || degradedRollback {
					emitProgress(options, fmt.Sprintf("[trajectory] step-back failed (%v); continuing without it\n", sbErr))
					rc := buildRevisionContext(plan.RequestIntent, history, passRecords, passOutputs, "")
					revisionContext = &rc
					continue
				}
				return finishWithReason(runID, plan, allRecords, "failed", fmt.Sprintf("step-back analysis: %v", sbErr))
			}
			// The next pass keeps the failing tests and the history; the
			// analysis is added, not a replacement.
			rc := buildRevisionContext(plan.RequestIntent, history, passRecords, passOutputs, fmt.Sprintf("Step-back analysis: %s. Recommended approach: %s.", analysis.HypothesizedRootCause, analysis.RecommendedApproach))
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
						emitNotice(options, fmt.Sprintf("[escalation] resolver error: %v (continuing without escalation)", escErr))
					} else if escalation.Provider == nil {
						emitProgress(options, "[escalation] no escalation provider configured (continuing without escalation)\n")
					} else {
						provider = escalation.Provider
						options.ProviderName = escalation.ProviderName
						options.Model = escalation.Model
						// An escalation config without its own effort keeps the
						// run's; either way it is fitted to the escalated model.
						if escalation.ReasoningEffort != "" {
							options.ReasoningEffort = escalation.ReasoningEffort
						}
						options.ReasoningEffort = string(options.ModelRegistry.RequestEffort(options.Model, modelregistry.ReasoningEffort(options.ReasoningEffort)))
						// Escalation applies to every later LLM-backed stage. Do not let
						// the original stage router replace it on the next iteration.
						options.StageModelResolver = nil
						options.escalatedModel = escalation.Model
						// Escalation also overrides node model pins. The node
						// resolver is superseded so every later call takes the
						// escalated model, and the switch names the pins it replaced.
						msg := fmt.Sprintf("[escalation] switched to model %s for iteration %d", escalation.Model, i+1)
						if pinned := pinnedNodeNames(plan); len(pinned) > 0 {
							options.NodeModelResolver = func(string, agent.ModelOverride) (agent.ModelSelection, error) {
								return escalation, nil
							}
							msg += fmt.Sprintf("; overrides the model pin of node(s) %s", strings.Join(pinned, ", "))
						}
						emitProgress(options, msg+"\n")
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
				return finishWithUserAbort(runID, plan, allRecords, msg)
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
	execBudget *StageExecutionBudget,
) ([]schemas.StageRecord, []schemas.HarnessStageOutput, bool, error) {
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	summariesByStage := map[string]string{}
	changedByStage := map[string][]string{}
	outputsByStage := map[string]schemas.HarnessStageOutput{}
	records := []schemas.StageRecord{}
	outputs := []schemas.HarnessStageOutput{}

	stageNames := make([]string, len(plan.Stages))
	// A topology-compiled plan carries dependencies; a stage then sees only
	// the summaries its incoming edges deliver. A legacy plan carries none,
	// and every stage keeps today's cumulative view.
	planHasEdges := false
	for i, stage := range plan.Stages {
		stageNames[i] = stage.Name
		if len(stage.DependsOn) > 0 {
			planHasEdges = true
		}
	}

	// priorScope carries the cognition context scope across stage
	// invocations and repair re-entry within this run.
	var priorScope *StageScopePlan

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
				emitStageEvent(options, iteration-1, stageName, "skipped", summary, 0, nil)
				continue
			}
			summary := fmt.Sprintf("Stage unavailable: %s has no configured agent", stageName)
			records = append(records, schemas.StageRecord{
				Name:          stageName,
				Status:        schemas.StageFailed,
				Iteration:     iteration,
				OutputSummary: &summary,
				FailureKind:   schemas.NodeFailureStageError,
			})
			return records, outputs, false, nil
		}

		var nextStage string
		if seq+1 < len(stageNames) {
			nextStage = stageNames[seq+1]
		}

		prior, priorChanged := maps.Clone(priorSummaries), cloneChangedFiles(priorChangedFiles)
		if planHasEdges {
			prior, priorChanged = scopedStageInputs(stage, summariesByStage, changedByStage, outputsByStage)
		}
		input := schemas.HarnessStageInput{
			RunID:             runID,
			StageName:         stageName,
			Sequence:          seq + 1,
			PlanTier:          plan.Tier,
			RequestIntent:     plan.RequestIntent,
			AcceptanceFacts:   append([]schemas.AcceptanceFact(nil), plan.AcceptanceFacts...),
			PriorSummaries:    withRoleAlias(prior, plan, "code_writer"),
			PriorChangedFiles: withRoleAlias(priorChanged, plan, "code_writer"),
			WriterStage:       writerStageName(plan),
			RevisionContext:   revisionContext,
			PipelineStages:    stageNames,
			NextStage:         nextStage,
		}

		caps := effectiveCaps(stage, agentStage.Capabilities())
		if tr != nil {
			tr.noteStage(stageName, iteration)
		}
		// Single composition path shared with repair re-entry: retrieval,
		// deterministic admission, compaction, then post-compaction trace
		// accounting of delivered memory.
		preparedInput, stageScope, scopeSup, perr := prepareStageInput(ctx, stageInputPreparation{
			Input:      input,
			Stage:      agentStage,
			Caps:       caps,
			Budget:     stage.Budget,
			Tier:       plan.Tier,
			Iteration:  iteration,
			WorkDir:    workDir,
			Options:    options,
			Memory:     mem,
			Trace:      tr,
			NowUnix:    time.Now().Unix(),
			PriorScope: priorScope,
		})
		if perr != nil {
			return records, outputs, false, perr
		}
		input = preparedInput
		priorScope = &stageScope
		if tr != nil && !scopePlanIsUncomputed(stageScope) {
			tr.recordScopeMetricsOrdinal(stageName, iteration, 0, scopeSup, stageScope)
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
		emitStageEvent(options, iteration-1, stageName, "running", caps.Description, 0, nil)

		// Model-free stages skip provider resolution and attribution.
		modelFree := caps.ModelFree
		selection := agent.ModelSelection{
			Provider:        provider,
			ProviderName:    options.ProviderName,
			Model:           options.Model,
			ReasoningEffort: options.ReasoningEffort,
		}
		switch {
		case !modelFree && stage.Model != nil && options.NodeModelResolver != nil:
			// Strongest rung: the node's own declaration. A broken declaration
			// fails loud; it must not silently degrade to the per-stage file.
			resolved, rerr := options.NodeModelResolver(stageName, nodeModelOverride(stage.Model))
			if rerr != nil {
				return records, outputs, false, fmt.Errorf("stage %s model: %w", stageName, rerr)
			}
			selection = resolved
			emitResolvedModel(options, iteration-1, caps, stageName, resolved)
		case !modelFree && stage.Model != nil:
			// A pinned node with no resolver cannot honor its pin. It must not
			// run on the primary model without an error.
			return records, outputs, false, fmt.Errorf("stage %s model: node model pin has no resolver configured", stageName)
		case !modelFree && options.StageModelResolver != nil:
			// A stage route that cannot resolve fails the run, as a node pin
			// does. Only the automatic tier layer falls back to the run model.
			resolved, rerr := options.StageModelResolver(stageName)
			var tierFallback *stageTierFallback
			if rerr != nil && !errors.As(rerr, &tierFallback) {
				return records, outputs, false, fmt.Errorf("stage %s model: %w", stageName, rerr)
			}
			if rerr != nil {
				// A visible notice, like the escalation resolver error: the
				// stage runs on another model than the tier route chose.
				emitNotice(options, fmt.Sprintf("[%s] tier route failed, using the run model: %v", stageName, rerr))
			} else if resolved.Provider != nil {
				selection = resolved
				emitResolvedModel(options, iteration-1, caps, stageName, resolved)
			}
		}
		if modelFree {
			selection = agent.ModelSelection{}
		} else {
			selection.ReasoningEffort = stageReasoningEffort(options, selection)
		}
		emitStageRoute(options, iteration-1, stageName, selection, stage.Budget, !modelFree && options.escalatedModel != "")

		// The stage deadline cannot exceed the pipeline wall deadline.
		stageCtx := ctx
		var cancelStage context.CancelFunc
		if !wallDeadline.IsZero() {
			stageCtx, cancelStage = context.WithDeadline(ctx, wallDeadline)
		}

		start := time.Now()
		// D2: each stage invocation in the pass owns a fresh shared budget;
		// the local repair loop of that stage (attemptLocalRepair) reuses the
		// same instance, so pass + repairs of one stage share one allowance,
		// while different stages never starve each other.
		stageBudget := NewStageExecutionBudget(0)
		output, err := runStageWithContextBudgeted(stageCtx, input, agentStage, iteration, selection, options, workDir, runner, mem, caps, stage.Budget.OutputMax, tr, priorScope, 0, stageBudget)
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
			record.FailureKind = schemas.NodeFailureStageError
			var metered interface{ StageUsage() *schemas.StageUsage }
			if errors.As(err, &metered) {
				applyStageUsage(&record, metered.StageUsage())
			}
			// The summary is shown to the user. It carries the error text, not the
			// Go type name of the error.
			summary := err.Error()
			record.OutputSummary = &summary
			records = append(records, record)
			if tr != nil {
				tr.recordStageCompletion(record)
				tr.persistPartial(ctx)
			}
			noteNodeOutcome(options, iteration, stageName, schemas.StageFailed, failedStageOutput(err))
			emitStageEvent(options, iteration-1, stageName, "failed", summary, 0, nil)
			return records, outputs, false, nil
		}
		if output.ContextRequest != nil {
			return records, outputs, false, fmt.Errorf("stage %s requested context twice", stageName)
		}
		if err := output.Validate(); err != nil {
			record.Status = schemas.StageFailed
			record.FailureKind = schemas.NodeFailureStageError
			record.LatencyMs = latencyMs
			failSummary := fmt.Sprintf("invalid stage output: %v", err)
			record.OutputSummary = &failSummary
			records = append(records, record)
			if tr != nil {
				tr.recordStageCompletion(record)
				tr.persistPartial(ctx)
			}
			noteNodeOutcome(options, iteration, stageName, schemas.StageFailed, nil)
			emitStageEvent(options, iteration-1, stageName, "failed", failSummary, 0, nil)
			return records, outputs, false, nil
		}
		// A verification-producing node's report is re-keyed to the canonical
		// key here, so the trajectory monitor collects it without knowing which
		// builtin emitted it. The legacy keys stay for the severity counts.
		output = normalizeVerificationReport(output, stage.Caps != nil && stage.Caps.ProducesVerification)
		record.Status = schemas.StageCompleted
		if isVerificationIncompleteOutput(output) {
			record.Status = schemas.StageIncomplete
		} else if isVerificationNotApplicableOutput(output) {
			// No check applied (unknown language, nothing to analyze). This is
			// neither a failure nor a pass: the stage is skipped.
			record.Status = schemas.StageSkipped
		}
		if stageName == roleStageName(plan, "test_runner") && record.Status == schemas.StageCompleted {
			record.TestCommand = testCommandFromOutput(output)
		}
		record.Confidence = &output.Confidence
		if output.MemoryReview != nil {
			record.MemoryReviews = []schemas.MemoryReview{*output.MemoryReview}
		}
		applyStageUsage(&record, output.Usage)
		summary := SummarizeStageOutput(stageName, output)
		record.OutputSummary = &summary
		records = append(records, record)
		if tr != nil {
			tr.recordStageCompletion(record)
			tr.persistPartial(ctx)
		}
		noteNodeOutcome(options, iteration, stageName, record.Status, &output)
		if record.Status == schemas.StageIncomplete {
			emitStageEvent(options, iteration-1, stageName, "incomplete", summary, 0, nil)
		} else if record.Status == schemas.StageSkipped {
			emitStageEvent(options, iteration-1, stageName, "skipped", summary, 0, nil)
		} else {
			emitStageEvent(options, iteration-1, stageName, "completed", summary, 100, stageChangedFiles(output))
		}
		for _, obs := range extractWriteObservations(stageName, roleStageName(plan, "test_runner"), runID, memoryProjectRoot(options, workDir), output) {
			persistObservation(ctx, mem, obs, func(msg string) {
				emitProgress(options, fmt.Sprintf("[%s] %s", stageName, msg))
			})
		}
		changedFiles := stageChangedFiles(output)
		priorSummaries[stageName] = *record.OutputSummary
		priorChangedFiles[stageName] = append([]string(nil), changedFiles...)
		summariesByStage[stageName] = *record.OutputSummary
		changedByStage[stageName] = append([]string(nil), changedFiles...)
		outputsByStage[stageName] = output
		outputs = append(outputs, output)

		// DM2: when the test runner completes with failing tests, route a focused
		// revision request back to code_writer and re-run the tests. The repair
		// loop is bounded (maxLocalRepairs) and only fires when a code_writer
		// summary exists to re-enter.
		if stageName == roleStageName(plan, "test_runner") && record.Status == schemas.StageCompleted {
			if results, ok := output.Data["test_results"].(schemas.TestRunResults); ok && results.Failed() > 0 {
				if _, hasWriter := priorSummaries[roleStageName(plan, "code_writer")]; hasWriter {
					if _, interaction, rerr := attemptLocalRepair(ctx, runID, iteration, plan, registry, provider, options, workDir, runner, mem, tr, priorScope, wallDeadline, execBudget, &records, &outputs, &priorSummaries, &priorChangedFiles, output); rerr != nil {
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

// generationAllowance is the generated-token allowance after passes passes.
// The plan's output budget is one call per stage; a pass may call the writer
// again for each local repair, and every pass run counts. (Chain pilot 2: 6
// of 7 failures were abort_budget at 9.2k generated tokens, while verified
// runs reached 24.6k. Runaway calls stay bounded per call and per stage.)
func generationAllowance(budget, passes int) int {
	return budget * (1 + maxLocalRepairs) * passes
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
	caps stages.Capabilities,
	outputMax int,
	tr *runTraceAccumulator,
	priorScope *StageScopePlan,
	invocationOrdinal int,
) (schemas.HarnessStageOutput, error) {
	return runStageWithContextBudgeted(ctx, input, stage, iteration, selection, options, workDir, runner, mem, caps, outputMax, tr, priorScope, invocationOrdinal, nil)
}

// runStageWithContextBudgeted is runStageWithContext with an explicit
// shared execution budget (D2/D3). A nil budget creates a fresh one (the
// unthreaded pass path); repair re-entry passes the SAME budget as the
// pass so provider requests and expansions share one allowance.
func runStageWithContextBudgeted(
	ctx context.Context,
	input schemas.HarnessStageInput,
	stage stages.Stage,
	iteration int,
	selection agent.ModelSelection,
	options PipelineRunConfig,
	workDir string,
	runner ToolRunner,
	mem MemoryStore,
	caps stages.Capabilities,
	outputMax int,
	tr *runTraceAccumulator,
	priorScope *StageScopePlan,
	invocationOrdinal int,
	execBudget *StageExecutionBudget,
) (schemas.HarnessStageOutput, error) {
	// F1: the orchestrator owns the classification cell for this invocation.
	// The stage's usage closure reads it at emission time, so reclassification
	// between expansion rounds re-labels the spend source without threading
	// new parameters through every stage. The derived source is mechanical:
	// repair ordinal -> repair, otherwise the D2 loop reclassifies per round.
	invocationAttribution := agent.NewRequestAttribution(invocationOrdinal, 0, spendSourceForInvocation(invocationOrdinal))
	// F1: count the provider requests this invocation actually issues. The
	// writer's first round is a deterministic context handshake that makes no
	// provider call, so a round counter alone labels its successor expansion
	// and the live ledger then carries no generation record. The counter below
	// lets the loop put the generation label on the first provider-bearing
	// round.
	providerRequests := 0
	stageRunOptions := options
	if options.OnAttributedUsage != nil {
		baseAttributedUsage := options.OnAttributedUsage
		stageRunOptions.OnAttributedUsage = func(attributed agent.AttributedUsage) {
			providerRequests++
			baseAttributedUsage(attributed)
		}
	}
	stageOpts := withVerifyCommand(stageOptions(input.StageName, iteration, selection, stageRunOptions, workDir, runner, caps, invocationAttribution), stage, options, workDir)
	// Part A context bridge: fresh cognition narrows the default context
	// request. The scoped request replaces the default ONLY when the scope
	// resolved a question; otherwise the cold path stays byte-identical.
	// The suppression accounting is returned so the caller's trace records
	// host omissions, not inferences.
	scopeOn, serr := scopeEnabled(options.Flags)
	if serr != nil {
		return schemas.HarnessStageOutput{}, serr
	}
	if scopeOn && priorScope != nil && priorScope.CognitionResolved && stageOpts.RunTool != nil {
		// A7 host-side enforcement: the model-facing tool runner is
		// scoped when cognition resolved the location question. The
		// context fulfillment path uses the raw runner with explicit
		// granted paths, so the scoped request is never blocked by its
		// own scope.
		var supRec *SuppressionRecorder
		if tr != nil {
			supRec = tr.suppressionRecorder()
		}
		scoped := ScopedToolRunner{Inner: runner, Scope: *priorScope, Recorder: supRec}
		stageOpts.RunTool = func(ctx context.Context, name string, args map[string]any) (stages.ToolResult, error) {
			res, err := scoped.RunTool(ctx, name, args)
			if err != nil {
				return stages.ToolResult{}, err
			}
			return stages.ToolResult{OK: res.OK, Output: res.Output, Truncated: res.Truncated, Meta: res.Meta}, nil
		}
	}
	// Production evidence substitution: when admitted retained evidence
	// replaced a concrete cold-plan operation, the executor uses the warm
	// plan's remaining operations as the concrete context request. The
	// eliminated operation is never issued; required unresolved operations
	// stay in the request.
	// Only an actual admitted substitution may change the live context
	// request. Evidence-free stages keep the historical default request
	// byte-for-byte, so the improvement cannot silently reduce or reshape
	// the discovery coverage that Task A already relies on.
	if priorScope != nil && priorScope.Evidence != nil {
		if tr != nil && (priorScope.Evidence.SubstitutionCount() > 0 || len(priorScope.Evidence.Rejected) > 0) {
			tr.recordEvidencePlanOrdinal(input.StageName, iteration, invocationOrdinal, priorScope.Evidence)
		}
	}
	// Evidence substitution is the ONLY mechanism that may REMOVE a live
	// context operation. The legacy cognition scoping path
	// (ScopedContextRequest) is abandoned: it narrowed the host request
	// without ever changing what the model did, and measurement showed it
	// bought prompt growth rather than savings. Evidence-free stages keep
	// the historical default request byte-for-byte, so a stage with no
	// admitted substitution is unaffected.
	if priorScope != nil && priorScope.Evidence != nil && priorScope.Evidence.SubstitutionCount() > 0 {
		req := EvidenceRequestFromOperations(priorScope.Evidence.Warm.Operations,
			"Evidence-backed exact substitution: admitted retained records replaced redundant discovery operations.")
		// Delivery wiring: each substituted subject's CURRENT source is
		// fetched through the same guarded request, so the model receives
		// the declaration body instead of a location claim. Without this,
		// the substituted discovery loses its query and the answer never
		// reaches the model (the recorded retention failure).
		for _, q := range priorScope.Evidence.PrefetchQueries() {
			req.Queries = append(req.Queries, q)
		}
		stageOpts.OverrideContextRequest = &req
	}
	// Memory-assisted dependency prefetch (experimental treatment, OFF by
	// default): retained verified records name source files, and the host
	// fetches their current bounded views into the initial handshake. This
	// ADDS queries; it never removes an operation, and it is tracked
	// separately from substitution in the trace and the report.
	// The repair revision context names the failing files; the default
	// request reads them (D0.2), also when a prefetch extends it.
	revisionEvidence := ""
	if input.RevisionContext != nil {
		revisionEvidence = *input.RevisionContext
	}
	if priorScope != nil && len(priorScope.MemoryPrefetch) > 0 {
		var req schemas.ContextRequest
		if stageOpts.OverrideContextRequest != nil {
			req = *stageOpts.OverrideContextRequest
		} else {
			// Memory's files take the place of the arbitrary fallback slice
			// in a large workspace (DefaultContextRequestPreferring).
			paths := make([]string, 0, len(priorScope.MemoryPrefetch))
			for _, f := range priorScope.MemoryPrefetch {
				paths = append(paths, f.Path)
			}
			req = stages.DefaultContextRequestPreferring(input.RequestIntent, revisionEvidence, workDir, detectLanguage(workDir), paths)
			req.Reason = "Memory-assisted dependency prefetch: retained verified work names these sources; the host fetched their current views before your first request. " + req.Reason
		}
		have := map[string]bool{}
		for _, q := range req.Queries {
			if q.QueryType == schemas.ContextReadFile && q.Path != nil {
				have[*q.Path] = true
			}
		}
		// Memory's files go right after the leading listing queries, ahead
		// of the plan's other reads, so fitting the aggregate bound drops
		// fallback reads and never the files memory knows are needed.
		var missing []schemas.ContextQuery
		for _, q := range PrefetchQueriesFor(priorScope.MemoryPrefetch) {
			if !have[*q.Path] {
				missing = append(missing, q)
			}
		}
		req.Queries = insertAfterListing(req.Queries, missing)
		stageOpts.OverrideContextRequest = &req
		records := make([]schemas.MemoryPrefetchRecord, 0, len(priorScope.MemoryPrefetch))
		for _, f := range priorScope.MemoryPrefetch {
			records = append(records, schemas.MemoryPrefetchRecord{
				Path: f.Path, Reason: f.Reason, RecordRef: f.RecordRef, ContentVersion: f.ContentVersion,
			})
		}
		if tr != nil {
			tr.recordMemoryPrefetch(input.StageName, iteration, invocationOrdinal, records)
			paths := make([]string, 0, len(records))
			for _, r := range records {
				paths = append(paths, r.Path)
			}
			tr.noteMemoryDelivered(paths)
		}
		emitProgress(options, fmt.Sprintf("[%s] memory prefetch: fetching %d retained-record source view(s) before the first model request", input.StageName, len(records)))
	}
	// Decision-advisor dependency delivery (Jev M4, active modes only):
	// the selector's calibrated, gate-approved fetches join the initial
	// handshake through the same guarded context-request fulfillment.
	// Shadow decisions never reach this block. Queries deduplicate
	// against everything already present (evidence prefetch and memory
	// prefetch came first), so a delivered view is never fetched twice.
	if priorScope != nil && len(priorScope.JevPrefetch) > 0 {
		jevQueries := ContextQueriesFor(priorScope.JevPrefetch)
		if stageOpts.OverrideContextRequest == nil {
			req := stages.DefaultContextRequestFor(input.RequestIntent, revisionEvidence, workDir, detectLanguage(workDir))
			req.Reason = "Decision-approved dependency prefetch: the calibrated selector resolved this dependency; the host fetched the exact declarations before your first request."
			stageOpts.OverrideContextRequest = &req
		}
		req := stageOpts.OverrideContextRequest
		existing := map[string]bool{}
		keyFor := func(q schemas.ContextQuery) string {
			path, sym := "", ""
			if q.Path != nil {
				path = *q.Path
			}
			if q.Symbol != nil {
				sym = *q.Symbol
			}
			return fmt.Sprintf("%s|%s|%s", q.QueryType, path, sym)
		}
		for _, q := range req.Queries {
			existing[keyFor(q)] = true
		}
		added := 0
		for _, q := range jevQueries {
			if existing[keyFor(q)] {
				continue
			}
			existing[keyFor(q)] = true
			req.Queries = append(req.Queries, q)
			added++
		}
		if added > 0 {
			records := make([]schemas.JevPrefetchRecord, 0, len(priorScope.JevPrefetch))
			for _, f := range priorScope.JevPrefetch {
				records = append(records, schemas.JevPrefetchRecord{
					Path: f.Path, Symbol: f.Symbol, Selected: f.SelectedRef,
					RequestID: f.RequestID, Model: f.Model, ChargeUSD: f.ChargeUSD,
				})
			}
			if tr != nil {
				tr.recordJevPrefetch(input.StageName, iteration, invocationOrdinal, records)
			}
			emitProgress(options, fmt.Sprintf("[%s] decision prefetch: fetching %d selector-approved view(s) before the first model request", input.StageName, added))
		}
	}
	if stageOpts.OverrideContextRequest != nil {
		if dropped := stageOpts.OverrideContextRequest.FitAggregate(); dropped > 0 {
			emitProgress(options, fmt.Sprintf("[%s] context request: dropped %d lowest-priority read(s) to fit the aggregate bound", input.StageName, dropped))
		}
	}
	if outputMax > 0 {
		// The stage's output budget caps every LLM request this stage makes. Zero
		// keeps the provider default (no per-request override).
		stageOpts.MaxOutputTokens = outputMax
	}
	stageOpts.ModelOverride = selection.Model
	stageOpts.ReasoningEffort = selection.ReasoningEffort

	// Model-free stages keep the single-call path (no provider request,
	// no expansion semantics). The budget is created fresh here when the
	// caller did not thread one (pass loop), or reuses the shared one
	// (repair re-entry): additional context is never a fresh repair
	// budget.
	budget := execBudget
	if budget == nil {
		budget = NewStageExecutionBudget(0)
	}
	ledger := newExpansionLedger()
	var usageTotal *schemas.StageUsage
	accumulate := func(u *schemas.StageUsage) {
		usageTotal = mergeStageUsage(usageTotal, u)
	}
	providerRound := 0
	for round := 0; ; round++ {
		if ctx.Err() != nil {
			return schemas.HarnessStageOutput{}, withStageUsage(ctx.Err(), usageTotal)
		}
		// F1: the first provider-bearing round is the generation pass (or the
		// repair pass when invocationOrdinal > 0); later provider-bearing
		// rounds are expansion spend under the same D3 request identity. A
		// context-handshake round issues no provider request, so it must not
		// consume the generation label. The cell is set BEFORE the stage
		// streams so its usage closure stamps every request this round issues.
		if round > 0 {
			if providerRequests == 0 {
				invocationAttribution.Set(invocationOrdinal, 0, spendSourceForInvocation(invocationOrdinal))
			} else {
				invocationAttribution.Set(invocationOrdinal, providerRound, schemas.SpendSourceExpansion)
			}
		}
		requestsBefore := providerRequests
		output, err := stage.Run(ctx, input, selection.Provider, stageOpts)
		if err != nil {
			return schemas.HarnessStageOutput{}, withStageUsage(err, usageTotal)
		}
		if providerRequests > requestsBefore {
			providerRound++
		}
		accumulate(output.Usage)

		if output.ContextRequest == nil {
			// Terminal: the model submitted changes (bare C-protocol or
			// submit_changes envelope) or produced a model-free output.
			// D1: the writer/test-generator decoded its own action
			// internally — a request_context action is surfaced as
			// output.ContextRequest, and a submit action as the proposal.
			// Nothing else to decode here.
			output.Usage = usageTotal
			return output, nil
		}

		// Stage-level ContextRequest (the historical one-shot contract,
		// still used by the initial handshake). Fulfill it if the budget
		// allows; this replaces the old hard "requested context more
		// than once" error with a bounded continuation. Round 0 is the
		// handshake, the stage's first read; each later request is an
		// expansion and spends a round. expandContextRound spends the
		// request slot. (P2d: the old double spend here left one expansion
		// after the handshake, and every cpl-01 repair failed one read short
		// of the coupled file.)
		if round > 0 && !budget.SpendRound() {
			return schemas.HarnessStageOutput{}, withStageUsage(fmt.Errorf("%w while fulfilling the stage context request", errExpansionBudgetExhausted), usageTotal)
		}
		expanded, eerr := expandContextRound(ctx, input, output.ContextRequest, budget, ledger, runner, mem, tr, options, workDir, iteration, invocationOrdinal)
		if eerr != nil {
			return schemas.HarnessStageOutput{}, withStageUsage(eerr, usageTotal)
		}
		input = expanded
		// The stage re-runs on the next loop iteration with the new
		// evidence; the historical "requested context more than once"
		// error becomes a bounded continuation instead.
	}
}

// expandContextRound validates, bounds, and fulfills one context round
// (initial handshake or envelope expansion). It mutates nothing global;
// the caller re-invokes the stage with the returned input.
func expandContextRound(
	ctx context.Context,
	input schemas.HarnessStageInput,
	request *schemas.ContextRequest,
	budget *StageExecutionBudget,
	ledger *expansionLedger,
	runner ToolRunner,
	mem MemoryStore,
	tr *runTraceAccumulator,
	options PipelineRunConfig,
	workDir string,
	iteration int,
	invocationOrdinal int,
) (schemas.HarnessStageInput, error) {
	if request == nil {
		return input, nil
	}
	if !budget.SpendRequest() {
		return input, fmt.Errorf("%w: %d provider request(s) spent", errExpansionBudgetExhausted, budget.RequestsUsed())
	}
	if ledger != nil {
		if err := ledger.Check(*request); err != nil {
			return input, fmt.Errorf("expansion rejected: %w", err)
		}
	}
	// Every request is fitted to the aggregate bound here, where they all
	// pass. (Chain pilot 2: the test generator's post-write reads of many
	// changed files asked for 72000 characters and failed the stage in
	// iterations 2 and 3.)
	if dropped := request.FitAggregate(); dropped > 0 {
		emitProgress(options, fmt.Sprintf("[%s] context request: dropped %d lowest-priority read(s) to fit the aggregate bound\n", input.StageName, dropped))
	}
	bundle, err := FulfillContextRequest(ctx, *request, runner)
	if err != nil {
		return input, fmt.Errorf("fulfill context: %w", err)
	}
	if ledger != nil {
		ledger.Record(*request)
	}
	// No-new-bytes detection: an expansion whose every source item repeats
	// already-delivered content carries the authoritative note that the
	// context is complete. Without it a model that confabulates elided
	// ranges spends its whole allowance re-requesting files it holds (the
	// M6 campaign evidence), and the stage dies at the budget wall with
	// zero code written.
	if ledger != nil && input.Context != nil && len(input.Context.Items) > 0 && !ledger.NewBytes(&bundle) {
		bundle.HostNotes = append(bundle.HostNotes,
			"This expansion delivered no new content: every requested file was already delivered above in its complete current form, byte for byte. The delivered views are not truncated and contain no elided ranges. Do not request these files again; proceed with the task using the delivered evidence.")
	}
	if ledger != nil {
		ledger.RecordDigests(&bundle)
	}
	// An expansion round is the model naming a file it needed: record the
	// paths for the work record capture before the merge. A stage that
	// already holds delivered context is expanding. (budget.RoundsUsed does
	// not tell: a repair re-entry shares the budget of earlier rounds.)
	if tr != nil && input.Context != nil {
		tr.noteRequestedPaths(bundle.Items)
	}
	// Merge new evidence after previously delivered items: expansions
	// ADD, never remove acceptance or failure evidence.
	if input.Context != nil {
		bundle.Items = append(input.Context.Items, bundle.Items...)
	}
	// Every round records under the stage key: the merged bundle is the
	// stage's delivered context. (recordContextRound's map is not reported.)
	if tr != nil {
		tr.recordContext(input.StageName, iteration, bundle)
	}
	// C1: the delivered views are the proposal base for this invocation.
	stages.RecordProposalBases(&bundle)
	// Degradation observations persist per fulfilled bundle (the
	// historical behavior for the initial handshake; expansions observe
	// too, since a failed expansion query is the same delivery miss).
	if mem != nil {
		for _, obs := range extractDegradationObservations(input.StageName, input.RunID, memoryProjectRoot(options, workDir), bundle) {
			persistObservation(ctx, mem, obs, func(msg string) {
				emitProgress(options, fmt.Sprintf("[%s] %s", input.StageName, msg))
			})
		}
	}
	input.Context = &bundle
	emitProgress(options, fmt.Sprintf("[%s] context round fulfilled: %d item(s) delivered\n", input.StageName, len(bundle.Items)))
	return input, nil
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
	report, ok := verificationReport(output)
	return ok && report.Status == schemas.VerificationIncomplete
}

// isVerificationNotApplicableOutput reports whether a stage's verification
// report says no check applied. That is a skip, not an incomplete check.
func isVerificationNotApplicableOutput(output schemas.HarnessStageOutput) bool {
	report, ok := verificationReport(output)
	return ok && report.Status == schemas.VerificationNotApplicable
}

// incompleteVerificationFailure reports whether a pass carries a stage that
// could not run a required verification check. Such a pass must not complete:
// the stage status and the trajectory state already record the gap, and the
// graph-capture gate keys on the completed status alone. The reason names every
// offending stage and its summary so the unavailable check is visible without
// the trace.
func incompleteVerificationFailure(records []schemas.StageRecord) (string, bool) {
	var details []string
	for _, r := range records {
		if r.Status != schemas.StageIncomplete {
			continue
		}
		detail := fmt.Sprintf("stage %s", r.Name)
		if summary := DerefString(r.OutputSummary); summary != "" {
			detail += ": " + summary
		}
		details = append(details, detail)
	}
	if len(details) == 0 {
		return "", false
	}
	return "required verification incomplete: " + strings.Join(details, "; "), true
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
	// The failing tests themselves: a test run that completes with failures
	// is not a failed stage, so without these lines the next pass knew only
	// "tests_failing=1" and spent its context budget finding the test again
	// (chain pilot 3: iterations 2 and 3 of chn-04-write).
	for _, output := range outputs {
		if results, ok := output.Data["test_results"].(schemas.TestRunResults); ok && results.Failed() > 0 {
			evidence, _ := extractFailingTests(results)
			lines = append(lines, "", "Failing tests in the last test run:")
			for _, e := range evidence {
				lines = append(lines, "  "+e)
			}
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
		// The model cannot satisfy a modify for these paths. modify requires a
		// base_ref that keys a source view DELIVERED to the current invocation,
		// and this context is built before the stage runs, so the host holds no
		// such view. Minting a base from current bytes would key edits against
		// text the model never received, which the materializer's exact-match
		// rule forbids. The full-content create form needs no base, so it is the
		// only representation the model can actually submit here.
		lines = append(lines, "", "Files written by the prior iteration (re-emit each with change_type create and the full file content; the host replaces the prior bytes):")
		lines = append(lines, "  "+strings.Join(changedFiles, ", "))
	}
	if note != "" {
		lines = append(lines, "", note)
	}
	return strings.Join(lines, "\n")
}

// repeatedFailureLabel labels a repeated stage failure. A known cause family
// keeps its label. Any other failure gets the closed trajectory failure kind
// stage_error and names the node, so the reason never says "unknown".
func repeatedFailureLabel(plan schemas.ExecutionPlan, failed schemas.StageRecord) string {
	if class := failureCauseClass(DerefString(failed.OutputSummary)); class != "unknown" {
		return class
	}
	node := "stage " + failed.Name
	for _, stage := range plan.Stages {
		if stage.Name == failed.Name && stage.Type == schemas.NodeTypeCommand {
			node = "command node " + failed.Name
		}
	}
	return "stage_error: " + node
}

// failureCauseClass names the error family of a stage failure so aggregate
// telemetry can separate repeated failures that share the wrapper name but
// differ in root cause. The full detail is still appended to the reason;
// this label is additive, never a replacement. The order matters: a typed
// output error that reports a missing base_ref is a validation failure, and
// the more specific class must win.
func failureCauseClass(summary string) string {
	s := strings.ToLower(summary)
	switch {
	case strings.Contains(s, "base_ref"), strings.Contains(s, "proposal "), strings.Contains(s, "validation"):
		return "validation"
	case strings.Contains(s, "typedoutputerror"), strings.Contains(s, "typed output"):
		return "typed_output"
	case strings.Contains(s, "timed out"), strings.Contains(s, "timeout"):
		return "timeout"
	case strings.Contains(s, "auth"):
		return "auth"
	case strings.Contains(s, "provider request error"):
		return "provider"
	default:
		return "unknown"
	}
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
				// "Name: message": the analysis needs why a test fails,
				// not only which.
				evidence, _ := extractFailingTests(tr)
				report.FailingTests = append(report.FailingTests, evidence...)
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
	summary, err := gitChangeSummary(ctx, workDir)
	if err == nil {
		return summary
	}
	walked := walkChangeSummary(workDir)
	if errors.Is(err, errNotGitRepository) {
		return walked
	}
	walked.IsRepo = true
	walked.DegradedReason = err.Error()
	return walked
}

// errNotGitRepository marks a directory that is not a Git repository, so
// summarizeWorkspaceChanges can distinguish that case from a repository
// whose Git read could not run.
var errNotGitRepository = errors.New("not a git repository")

func gitChangeSummary(ctx context.Context, workDir string) (schemas.ChangeSummary, error) {
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return schemas.ChangeSummary{}, errNotGitRepository
	}
	if _, err := exec.LookPath("git"); err != nil {
		return schemas.ChangeSummary{}, fmt.Errorf("git binary not found: %w", err)
	}

	// Both git reads run under the stage profile: fixed allowlist, workspace
	// scope, network denied, and a private temp directory (prepareGitRead).
	statusCmd, statusDone, cerr := prepareGitRead(ctx, workDir, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all")
	if cerr != nil {
		return schemas.ChangeSummary{}, fmt.Errorf("git read refused: %w", cerr)
	}
	defer statusDone()
	statusOut, err := statusCmd.Output()
	if err != nil {
		return schemas.ChangeSummary{}, fmt.Errorf("git status failed: %w", err)
	}

	diffCmd, diffDone, cerr := prepareGitRead(ctx, workDir, "diff", "HEAD", "--no-color")
	if cerr != nil {
		return schemas.ChangeSummary{}, fmt.Errorf("git read refused: %w", cerr)
	}
	defer diffDone()
	diffOut, err := diffCmd.Output()
	if err != nil {
		return schemas.ChangeSummary{}, fmt.Errorf("git diff failed: %w", err)
	}

	truncated := false
	var files []schemas.ChangedFile
	var created []string
	for _, f := range parseStatusZ(statusOut) {
		files = append(files, f)
		if f.Status == "created" {
			created = append(created, f.Path)
		}
		if len(files) >= defaultMaxSummaryFiles {
			truncated = true
			break
		}
	}

	root, err := os.OpenRoot(workDir)
	if err != nil {
		return schemas.ChangeSummary{}, fmt.Errorf("open workspace: %w", err)
	}
	defer root.Close()
	diff := &strings.Builder{}
	diff.Write(diffOut)
	total := diff.Len()
	for _, path := range created {
		relPath := path
		if filepath.IsAbs(path) {
			relPath, _ = filepath.Rel(workDir, path)
		}
		// A FIFO or a device would block the read, and a symbolic link can
		// lead out of the workspace. Only regular targets inside it count.
		f, _, err := openRegular(root, relPath)
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
	}, nil
}

// prepareGitRead builds one of Splice's own read-only git commands under the
// stage sandbox profile. A repository clean or smudge filter runs inside it,
// because git-lfs and similar filters must keep working. The filter gets a
// private temp directory per call: TMPDIR, TMP, and TEMP name it, and it is
// the only temp directory the sandbox lets the command write. done ends the
// plan and removes the directory.
func prepareGitRead(ctx context.Context, workDir string, args ...string) (*exec.Cmd, func(), error) {
	tempRoot, err := os.MkdirTemp("", "splice-git-")
	if err != nil {
		return nil, nil, fmt.Errorf("make the git temp directory: %w", err)
	}
	engine := procrun.NewStageEngineWithTempRoot(workDir, tempRoot)
	cmd, plan, err := stages.PrepareStageCommand(ctx, engine, workDir, append([]string{"git", "-C", workDir}, args...))
	if err != nil {
		_ = os.RemoveAll(tempRoot)
		return nil, nil, err
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(withoutEnv(env, "TMPDIR", "TMP", "TEMP"), "TMPDIR="+tempRoot, "TMP="+tempRoot, "TEMP="+tempRoot)
	done := func() {
		plan.Cleanup()
		_ = os.RemoveAll(tempRoot)
	}
	return cmd, done, nil
}

// withoutEnv returns env without the named variables.
func withoutEnv(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }) {
			out = append(out, entry)
		}
	}
	return out
}

// parseStatusZ reads `git status --porcelain -z --no-renames`. -z gives each
// entry as "XY path" with no quoting, so a name with a space or a non-ASCII
// byte is the exact path. Renames are off, so no entry carries a second path.
func parseStatusZ(out []byte) []schemas.ChangedFile {
	var files []schemas.ChangedFile
	for _, line := range strings.Split(string(out), "\x00") {
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if path == "" {
			continue
		}
		files = append(files, schemas.ChangedFile{Path: path, Status: gitStatusToChangeStatus(line[:2])})
	}
	return files
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
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return schemas.ChangeSummary{ChangedFiles: files}
	}
	defer root.Close()

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

		// A FIFO or a device would block the read, and a symbolic link can
		// lead out of the workspace. Only regular targets inside it count.
		f, _, err := openRegular(root, rel)
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

// passDigest hashes the files a change summary lists (path, status, and
// current content). With Git the list is the working changes against HEAD,
// and every file not listed equals HEAD in both passes, so two digests
// differ exactly when the working content changed between the passes. The
// cost follows the size of the change, not the repository (a whole-tree
// hash read gigabytes on a large repository).
// ponytail: bounded by the summary's own caps (200 files); a pass that
// only touches files past the cap reads as unchanged.
func passDigest(workDir string, summary schemas.ChangeSummary) string {
	h := sha256.New()
	files := append([]schemas.ChangedFile(nil), summary.ChangedFiles...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	root, err := os.OpenRoot(workDir)
	if err == nil {
		defer root.Close()
	}
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%s\x00", f.Path, f.Status)
		// A FIFO or a device would block the read, and a symbolic link can lead
		// out of the workspace. Only regular targets inside it are hashed.
		if root != nil {
			if file, _, err := openRegular(root, f.Path); err == nil {
				_, _ = io.Copy(h, io.LimitReader(file, maxDigestFileBytes))
				_ = file.Close()
			}
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

const maxDigestFileBytes = 1 << 20

func skipSummaryDirComponent(rel string) bool {
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if skipSummaryDirs[part] {
			return true
		}
	}
	return false
}

// wirePresentation sets up the presentation snapshot accumulator and wraps
// the plan and stage event callbacks so every event flows through it. It
// returns the wrapped options and the accumulator. When OnPresentationState
// is nil it returns the options unchanged and a nil accumulator, so disabled
// emission adds zero work on the hot path.
func wirePresentation(options PipelineRunConfig, plan schemas.ExecutionPlan) (PipelineRunConfig, *presentrun.Accumulator) {
	if options.OnPresentationState == nil {
		return options, nil
	}
	acc := presentrun.New(func(msg string) { emitProgress(options, msg) })
	priorStage := options.OnStageEvent
	options.OnStageEvent = func(event agent.StageEvent) {
		acc.Apply(presentrun.AdaptStageEvent(event))
		options.OnPresentationState(acc.Snapshot())
		if priorStage != nil {
			priorStage(event)
		}
	}
	priorPlan := options.OnPipelinePlan
	options.OnPipelinePlan = func(event agent.PipelinePlanEvent) {
		acc.Apply(presentrun.AdaptPipelinePlan(plan, event))
		options.OnPresentationState(acc.Snapshot())
		if priorPlan != nil {
			priorPlan(event)
		}
	}
	// Spend reaches the node that made it. Requests outside the plan
	// stages (for example step_back) are run spend, not node spend, so the
	// accumulator counts them as skipped. The legacy usage sink keeps the
	// same fallback the request ledger applies.
	planStages := make(map[string]bool, len(plan.Stages))
	for _, stage := range plan.Stages {
		planStages[stage.Name] = true
	}
	options.stageRoute = func(stage string, iteration int, route presentation.NodeRoute) {
		acc.Apply(presentation.RouteEvent{NodeID: stage, Iteration: iteration, Route: route})
		options.OnPresentationState(acc.Snapshot())
	}
	priorUsage := options.OnAttributedUsage
	legacyUsage := options.OnUsage
	options.OnAttributedUsage = func(attributed agent.AttributedUsage) {
		if planStages[attributed.Stage] {
			acc.Apply(presentrun.AdaptAttributedUsage(attributed))
			options.OnPresentationState(acc.Snapshot())
		} else {
			acc.Skip()
		}
		if priorUsage != nil {
			priorUsage(attributed)
		} else if legacyUsage != nil && attributed.UsageReported {
			legacyUsage(attributed.Usage)
		}
	}
	return options, acc
}

// finishPresentation feeds the terminal run event and emits the final
// snapshot. Receipt kinds (v0.5 receipts contract): "completed" projects
// VERIFIED-eligible, "failed" projects failed, and a run the USER chose to
// stop projects "cancelled" (a distinct receipt: staged work preserved,
// nothing applied). The user stop is TYPED on the result (UserAborted),
// set only at the user-abort decision site. A run aborted for internal
// reasons (max iterations, wall time, rollback refuse) has UserAborted
// false and projects "failed", because cancelled means the user chose to
// stop. User cancels that travel as context.Canceled bypass this function
// entirely and classify at the TUI error boundary.
func finishPresentation(acc *presentrun.Accumulator, options PipelineRunConfig, result schemas.PipelineResult) {
	if acc == nil {
		return
	}
	status := "completed"
	if result.Status != "completed" {
		status = "failed"
		if result.Status == "aborted" && result.UserAborted {
			status = "cancelled"
		}
	}
	if status == "completed" && !result.Verified {
		// A completed run that verified nothing keeps its status and projects
		// the NOT VERIFIED receipt, with the reason as its detail.
		acc.Apply(presentrun.AdaptRunOutcomeEvent(status, result.UnverifiedReason, true))
		options.OnPresentationState(acc.Snapshot())
		return
	}
	acc.Apply(presentrun.AdaptRunEvent(status, abortReason(result)))
	options.OnPresentationState(acc.Snapshot())
}

// buildStageToolRunner prepares the runner options for a pipeline run and
// builds the agent tool runner from them. The stage sandbox engine and the
// strict read-before-write flag must be final before the runner is built:
// newAgentToolRunner captures options by value.
func buildStageToolRunner(options PipelineRunConfig, absWorkDir string) (PipelineRunConfig, ToolRunner) {
	// Deterministic stage subprocesses run under their own enforce-mode
	// engine: workspace-scoped filesystem, network denied. This is separate
	// from options.Sandbox, which keeps the interactive user policy for
	// model-driven tool calls. Set BEFORE the tool runner is built so the
	// runner's captured config carries it.
	options.StageSandbox = procrun.NewStageEngine(absWorkDir)

	// Strict read-before-write for every write_file/edit_file in this run: a
	// model that skips the read now gets a loud, repairable error instead of
	// silently overwriting live symbols. Set BEFORE the tool runner is built
	// so the runner's captured config carries it.
	options.StageRequireReadBeforeWrite = true

	return options, newAgentToolRunner(options, absWorkDir)
}

// newAgentToolRunner builds the agent tool runner for a pipeline run. It
// captures options by value, so every field a run depends on must be final
// before this call. Mutating the caller's struct afterwards changes nothing
// the runner will ever read.
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
		runOptions := agent.NewToolRunOptions(agentOpts, call, cwd, permissionGranted)
		// D1: host-seam marking. The deterministic pipeline IS the
		// orchestrator; when the requested tool implements
		// tools.HostSeamTool, this call is by definition host-initiated
		// (the model surface never sees such tools - they are Deny and
		// unadvertised). The registry gate still verifies the interface,
		// so the flag cannot widen the model surface.
		if tool, ok := options.Registry.Get(name); ok {
			if _, isHostSeam := tool.(tools.HostSeamTool); isHostSeam {
				runOptions.HostSeam = true
			}
		}
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
		if options.StageRequireReadBeforeWrite {
			runOptions.RequireReadBeforeWrite = true
		}
		if runOptions.Sandbox != nil {
			// Stage tools execute under the caller's engine re-rooted to this
			// run's working directory. A worktree bound after startup (TUI
			// /exec) leaves the session engine rooted at the session
			// workspace, and plan preparation inside process-spawning tools
			// would refuse the run directory as outside_workspace. Re-rooting
			// keeps the caller's policy, add-dir grants, grant store, and
			// backend; only the workspace root moves.
			runOptions.Sandbox = runOptions.Sandbox.ReRooted(cwd)
		}
		res := options.Registry.RunWithOptions(ctx, name, args, runOptions)
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

// emitSink is the only path to Options.EventSink. It redacts the event first,
// so a secret in a stage summary, a notice, a node name, or a file path never
// reaches a web client. A nil sink does nothing.
func emitSink(sink func(agent.StageEvent), secrets []string, event agent.StageEvent) {
	if sink == nil {
		return
	}
	sink(RedactStageEvent(event, secrets))
}

// RedactStageEvent removes secrets from the text of a stage event that a web
// client can show: Detail, Name, and each ChangedFiles entry. It does not
// change the caller's ChangedFiles slice.
func RedactStageEvent(event agent.StageEvent, secrets []string) agent.StageEvent {
	opts := redaction.Options{ExtraSecretValues: secrets}
	event.Detail = redaction.RedactString(event.Detail, opts)
	event.Name = redaction.RedactString(event.Name, opts)
	if len(event.ChangedFiles) > 0 {
		files := make([]string, len(event.ChangedFiles))
		for i, path := range event.ChangedFiles {
			files[i] = redaction.RedactString(path, opts)
		}
		event.ChangedFiles = files
	}
	return event
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
	dependencies := make(map[string][]string, len(plan.Stages))
	for i, stage := range plan.Stages {
		stages[i] = stage.Name
		if len(stage.DependsOn) > 0 {
			dependencies[stage.Name] = append([]string(nil), stage.DependsOn...)
		}
	}
	options.OnPipelinePlan(agent.PipelinePlanEvent{Stages: stages, Dependencies: dependencies})
}

// emitStageEvent sends a typed stage lifecycle event. It also writes the
// deprecated NUL marker on OnReasoning for one release. status is one of:
// running, completed, failed, skipped, incomplete.
// emitResolvedModel announces the model a stage resolved to, so the live panel
// names the model actually in use. iteration is the 0-based pass index.
func emitResolvedModel(options PipelineRunConfig, iteration int, caps stages.Capabilities, stageName string, resolved agent.ModelSelection) {
	if resolved.Model == "" {
		return
	}
	detail := resolved.Model
	if caps.Description != "" {
		detail = caps.Description + " · " + resolved.Model
	}
	emitStageEvent(options, iteration, stageName, "running", detail, 0, nil)
}

// emitStageRoute reports the route a stage pass applies, after the model is
// resolved, the effort is fitted, and learned budgets are applied, so the
// presentation shows what the run sends instead of the configuration.
func emitStageRoute(options PipelineRunConfig, iteration int, stageName string, selection agent.ModelSelection, budget schemas.StageBudget, escalated bool) {
	if options.stageRoute == nil {
		return
	}
	options.stageRoute(stageName, iteration, presentation.NodeRoute{
		Provider:     selection.ProviderName,
		Model:        selection.Model,
		Effort:       selection.ReasoningEffort,
		InputBudget:  budget.InputMax,
		OutputBudget: budget.OutputMax,
		Escalated:    escalated,
	})
}

// emitStageEvent stamps a stage lifecycle event. iteration is the 0-based
// pass index (the pipeline's first pass is 0, the runtime loop counts from
// 1), so the presentation layer keeps one node per (stage, pass) and leaves
// the first pass unmarked.
func emitStageEvent(options PipelineRunConfig, iteration int, stageName, status, detail string, progress int, changedFiles []string) {
	event := agent.StageEvent{
		Name:         stageName,
		Iteration:    iteration,
		Status:       status,
		Detail:       detail,
		Progress:     progress,
		ChangedFiles: append([]string(nil), changedFiles...),
	}
	// Workspace isolation (DoD 26): a run bound to a Splice worktree
	// stamps every stage event with the isolated badge so the sidebar can
	// show the lane's isolation honestly. The runtime derives it from its
	// own config, never from the renderer.
	if options.IsolatedWorktree != "" {
		event.Workspace = "isolated"
		event.WorktreePath = options.IsolatedWorktree
	} else {
		event.Workspace = "shared_cwd"
	}
	if options.OnStageEvent != nil {
		options.OnStageEvent(event)
	}
	emitSink(options.EventSink, options.SecretValues, event)
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

// attributedWriter returns the code writer's plan name for rule reasons when
// the pass evidence flag is on. It returns "" when the flag is off or when the
// plan has no code writer, so a reason never names a stage that does not exist.
func attributedWriter(options PipelineRunConfig, plan schemas.ExecutionPlan) string {
	if !options.Flags.Enabled(flags.PassEvidence) {
		return ""
	}
	return codeWriterName(plan)
}

// failedStageOutput returns the files a failed stage still changed, such as
// an agent node that wrote before it hit its budget, so those files reach the
// node evidence. It returns nil when the error carries no files.
func failedStageOutput(err error) *schemas.HarnessStageOutput {
	var changedFiles interface{ StageChangedFiles() []string }
	if errors.As(err, &changedFiles) {
		if files := changedFiles.StageChangedFiles(); len(files) > 0 {
			return &schemas.HarnessStageOutput{ChangedFiles: files}
		}
	}
	return nil
}

// noteNodeOutcome appends one stage node's result to the pass attribution. A
// failed stage has no output, so its changed files are empty. A call without a
// pass sink (runPass used directly) records nothing.
func noteNodeOutcome(options PipelineRunConfig, iteration int, stage string, status schemas.StageStatus, output *schemas.HarnessStageOutput) {
	if options.passNodes == nil {
		return
	}
	entry := schemas.NodeEvidence{
		Node:      stage,
		Iteration: iteration,
		Outcome:   schemas.NodeOutcome{Status: status, Failure: schemas.NodeFailureNone},
	}
	if status == schemas.StageFailed {
		entry.Outcome.Failure = schemas.NodeFailureStageError
	}
	if output != nil {
		entry.ChangedFiles = stageChangedFiles(*output)
		if outputHasTestFailure(*output) {
			entry.Outcome.Failure = schemas.NodeFailureTestFailure
		}
	}
	*options.passNodes = append(*options.passNodes, entry)
}

// outputHasTestFailure reports whether a stage output carries a test run with
// failing tests. passFailure reads the same fact.
func outputHasTestFailure(output schemas.HarnessStageOutput) bool {
	results, ok := output.Data["test_results"].(schemas.TestRunResults)
	return ok && results.Failed() > 0
}

// stageChangedFiles extracts the changed file paths from a completed stage
// output. It checks code_writer_output and test_generator_output — the two
// stage types that produce FileChange slices. Returns nil when neither is
// present, so non-completed callers and stages without files pass nil cleanly.
func stageChangedFiles(output schemas.HarnessStageOutput) []string {
	// Additive field first: a custom stage reports its changed paths
	// directly. The legacy Data keys stay as the builtin fallback so the
	// existing stages keep working unchanged.
	files := append([]string(nil), output.ChangedFiles...)
	if len(files) == 0 {
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

// finishCompleted records a completed run. The run keeps every pass's records
// as its history. The verified flag judges only the final pass: an earlier
// pass that failed and was repaired does not make the run unverified.
func finishCompleted(runID string, plan schemas.ExecutionPlan, records []schemas.StageRecord, finalState schemas.IterationState, finalRecords []schemas.StageRecord, finalOutputs []schemas.HarnessStageOutput) (schemas.PipelineResult, error) {
	verified, reason := passVerification(finalRecords, finalOutputs)
	if !verified && !planHasVerification(plan) {
		// The plan has no verification stage at all, so the reason names that
		// cause instead of a generic line.
		reason = absentVerificationReason(plan)
	}
	return schemas.PipelineResult{
		RunID:            runID,
		Status:           "completed",
		Tier:             plan.Tier,
		Stages:           records,
		FinalState:       &finalState,
		Verified:         verified,
		UnverifiedReason: reason,
	}, nil
}

// absentVerificationReason names why a plan with no verification stage is not
// verified. A topology with no verification node at any tier is named as the
// cause. Otherwise the tier decides: a tier without checks runs none, even
// when other tiers of the same topology verify.
func absentVerificationReason(plan schemas.ExecutionPlan) string {
	if plan.TopologyName != "" && slices.Contains(plan.Warnings, noVerificationWarning) {
		return "topology " + plan.TopologyName + " has no verification node"
	}
	return string(plan.Tier) + " tier runs no checks"
}

// passVerification reports whether a completed pass verified anything. The
// pass is verified when a verification report passed, or a test run executed
// tests and none failed, and no stage is incomplete or failed. A completed
// pass that is not verified returns the reason, naming the checks that did not
// apply. The completion gate already refuses incomplete stages, so this is a
// second guard for the verified flag.
func passVerification(records []schemas.StageRecord, outputs []schemas.HarnessStageOutput) (bool, string) {
	for _, r := range records {
		if r.Status == schemas.StageIncomplete || r.Status == schemas.StageFailed {
			return false, "verification did not finish at stage " + r.Name
		}
	}
	passed := false
	noTests := false
	for _, out := range outputs {
		if report, ok := verificationReport(out); ok && report.Status == schemas.VerificationPassed {
			passed = true
		}
		if results, ok := out.Data["test_results"].(schemas.TestRunResults); ok {
			switch {
			case results.Failed() > 0:
			case results.Passed() > 0:
				passed = true
			default:
				noTests = true
			}
		}
	}
	if passed {
		return true, ""
	}
	var notApplied []string
	for _, r := range records {
		if r.Status == schemas.StageSkipped {
			notApplied = append(notApplied, r.Name+" ("+DerefString(r.OutputSummary)+")")
		}
	}
	if noTests {
		notApplied = append(notApplied, "tests (no tests ran)")
	}
	if len(notApplied) > 0 {
		return false, "no check applied: " + strings.Join(notApplied, "; ")
	}
	return false, "no verification check passed"
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

// finishWithUserAbort records an aborted run the USER chose to stop. Only
// the user-abort decision site may call it: UserAborted is the typed
// signal the presentation layer keys on to project a CANCELLED receipt
// (staged work preserved, nothing applied). Internal aborts keep
// finishWithReason and project FAILED.
func finishWithUserAbort(runID string, plan schemas.ExecutionPlan, records []schemas.StageRecord, reason string) (schemas.PipelineResult, error) {
	return schemas.PipelineResult{
		RunID:       runID,
		Status:      "aborted",
		Tier:        plan.Tier,
		Stages:      records,
		AbortReason: &reason,
		UserAborted: true,
	}, nil
}

func abortReason(result schemas.PipelineResult) string {
	if result.AbortReason != nil {
		return *result.AbortReason
	}
	return ""
}

// countPromptLayoutFlips reports how many requests changed the cacheable prefix
// layout of their stage within one run. Records arrive in request order, and a
// record without a layout hash carries no layout to compare, so it is skipped
// rather than counted as a change. Distinct stages legitimately have distinct
// layouts, so a stage is only ever compared against itself.
func countPromptLayoutFlips(records []schemas.PipelineUsageRecord) int {
	last := make(map[string]string)
	seen := make(map[string]bool)
	flips := 0
	for _, r := range records {
		if r.PromptLayoutHash == "" {
			continue
		}
		if seen[r.Stage] && last[r.Stage] != r.PromptLayoutHash {
			flips++
		}
		seen[r.Stage] = true
		last[r.Stage] = r.PromptLayoutHash
	}
	return flips
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
	result.PromptLayoutFlips = countPromptLayoutFlips(ledger.records)

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
	if result.Status == "completed" && !result.Verified {
		summary += " Not verified: " + result.UnverifiedReason + "."
	}
	return summary + "\n"
}

// insertAfterListing places queries right after the leading listing
// queries, ahead of every other read, so FitAggregate trims other reads
// first.
func insertAfterListing(queries, first []schemas.ContextQuery) []schemas.ContextQuery {
	lead := 0
	for lead < len(queries) && queries[lead].QueryType == schemas.ContextListFiles {
		lead++
	}
	out := append([]schemas.ContextQuery{}, queries[:lead]...)
	out = append(out, first...)
	return append(out, queries[lead:]...)
}

// stageReasoningEffort is the effort one stage sends. A stage routed to its
// own model (stage-models.json, the cheaper-tier resolver, a topology node)
// without an effort of its own inherits the run's effort instead of sending
// none, and every stage's effort is fitted to the levels its model supports
// (a GLM that starts at high, a Claude with max, a model with no xhigh).
func stageReasoningEffort(options PipelineRunConfig, selection agent.ModelSelection) string {
	effort := selection.ReasoningEffort
	if effort == "" {
		effort = options.ReasoningEffort
	}
	model := selection.Model
	if model == "" {
		model = options.Model
	}
	return string(options.ModelRegistry.RequestEffort(model, modelregistry.ReasoningEffort(effort)))
}
