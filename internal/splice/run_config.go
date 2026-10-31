package splice

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/streamjson"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// PipelineRunConfig is the subset of agent.Options the deterministic pipeline
// actually reads. Public Run and design-plan entry points still accept
// agent.Options and convert here so inherited Zero types stay free of
// Splice-specific methods.
type PipelineRunConfig struct {
	SessionID string
	// Flags is the resolved feature-flag set for this run. The zero value means
	// all defaults, so a caller that does not set it keeps today's behavior.
	Flags flags.Set
	// passEvidence measures each pass's workspace change for the run. It is
	// set by runIterationLoop and nil outside it.
	passEvidence *passEvidenceTracker
	// passNodes collects each stage node's result for the pass in progress.
	// runIterationLoop sets it per pass; nil means no pass attribution.
	passNodes *[]schemas.NodeEvidence
	// Advisor is the run's decision advisor (Jev). Nil means the advisor is
	// off: no snapshot is built, no decision is bought, no call is made.
	// Construction is resolveDecisionAdvisor's job (config mode gated by
	// the pipeline.decision_advisor flag); a nil advisor keeps today's
	// behavior byte-identically.
	Advisor               *DecisionAdvisor
	DecisionAdvisorConfig config.DecisionAdvisorConfig
	TrajectoryConfig      config.TrajectoryConfig
	ClassifierPolicy      *hostTrajectoryPolicy
	// MaxWall is the wall-time limit of the run. Zero uses defaultMaxWall.
	MaxWall time.Duration

	// TraceWriteWarn is invoked at most once when a trace persistence
	// attempt fails, so telemetry loss can reach the caller's output seam.
	// Same name as agent.Options.TraceWriteWarn: PipelineConfigFromAgentOptions
	// copies it, and the option-classification test keys on that name match.
	TraceWriteWarn      func(msg string)
	ProviderName        string
	Model               string
	ReasoningEffort     string
	Cwd                 string
	ProjectRoot         string
	VerifyCommand       []string
	VerifyDir           string
	ProjectInstructions string
	MemoryStatus        string
	Images              []zeroruntime.ImageBlock
	Registry            *tools.Registry
	PermissionMode      agent.PermissionMode
	Autonomy            string
	TrustedWorkspace    bool
	Sandbox             *sandbox.Engine
	// StageSandbox is the enforce-mode engine for deterministic stage
	// subprocesses: workspace-scoped filesystem, network denied. It is
	// distinct from Sandbox, which keeps the interactive user policy for
	// model-driven tool calls.
	StageSandbox *sandbox.Engine
	// StageRequireReadBeforeWrite turns on the strict read-before-write guard
	// for this run's write_file and edit_file calls: an existing path with no
	// session baseline is refused so a blind rewrite becomes a loud,
	// recoverable failure. Stage runs enable it; interactive sessions do not.
	StageRequireReadBeforeWrite bool
	FileTracker                 *tools.FileTracker
	Hooks                       *hooks.Dispatcher
	EnabledTools                []string
	DisabledTools               []string
	OnText                      func(string)
	OnReasoning                 func(string)
	OnToolCall                  func(agent.ToolCall)
	OnToolCallStart             func(id, name string)
	OnToolCallDelta             func(id, fragment string)
	OnPermissionRequest         func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error)
	OnPermission                func(agent.PermissionEvent)
	OnToolResult                func(agent.ToolResult)
	OnUsage                     func(agent.Usage)
	OnAttributedUsage           func(agent.AttributedUsage)
	EstimateUsageCost           func(model string, usage agent.Usage, reported bool) agent.UsageCostEstimate
	EstimateServedUsageCost     agent.ServedUsageCostEstimator
	OnToolProgress              func(toolCallID string, event streamjson.Event)
	OnToolOutput                func(tools.OutputSnapshot)
	FileDiagnostics             func(ctx context.Context, absPath string) string
	StageModelResolver          agent.StageModelResolver
	NodeModelResolver           agent.NodeModelResolver
	EscalationModelResolver     agent.EscalationModelResolver
	OnSurfaceToUser             func(ctx context.Context, req agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error)
	OnPipelinePlan              func(agent.PipelinePlanEvent)
	OnStageEvent                func(agent.StageEvent)
	OnNotice                    func(text string)
	EventSink                   func(agent.StageEvent)
	// SecretValues are the exact secret strings the event sink redacts. The list
	// is copied once per run.
	SecretValues []string
	// IsolatedWorktree marks that this run executes in a Splice-managed
	// worktree (DoD 26): stage events stamp Workspace="isolated" with the
	// worktree path, and the sidebar badges those lanes. Empty means the
	// run shares the authoritative working directory (shared_cwd).
	IsolatedWorktree string
	// OnPresentationState receives a presentation.State snapshot after each
	// significant pipeline transition when emission is enabled. Nil disables
	// emission: no accumulator is created and no snapshots are produced.
	OnPresentationState func(presentation.State)
	ModelRegistry       modelregistry.Registry

	// stageRoute receives the route each stage pass applied: the model the
	// run sends, the fitted effort, and the budget after learned fits. Set by
	// wirePresentation; nil when no presentation consumer is wired.
	stageRoute func(stage string, iteration int, route presentation.NodeRoute)
	// escalatedModel is the model run escalation switched to, or empty.
	escalatedModel string
}

// PipelineConfigFromAgentOptions copies the fields the pipeline consumes.
// Fields not present on PipelineRunConfig are classified in
// pipelineIgnoredAgentOptionReasons; a new agent.Options field fails
// TestPipelineRunConfigClassifiesEveryAgentOption until a human classifies it.
func PipelineConfigFromAgentOptions(options agent.Options) PipelineRunConfig {
	return PipelineRunConfig{
		SessionID:               options.SessionID,
		DecisionAdvisorConfig:   options.DecisionAdvisorConfig,
		TrajectoryConfig:        options.TrajectoryConfig,
		MaxWall:                 options.MaxWall,
		Flags:                   options.Flags,
		TraceWriteWarn:          options.TraceWriteWarn,
		ProviderName:            options.ProviderName,
		Model:                   options.Model,
		ReasoningEffort:         options.ReasoningEffort,
		Cwd:                     options.Cwd,
		ProjectRoot:             options.ProjectRoot,
		VerifyCommand:           append([]string(nil), options.VerifyCommand...),
		VerifyDir:               options.VerifyDir,
		MemoryStatus:            options.MemoryStatus,
		Images:                  options.Images,
		Registry:                options.Registry,
		PermissionMode:          options.PermissionMode,
		Autonomy:                options.Autonomy,
		TrustedWorkspace:        options.TrustedWorkspace,
		Sandbox:                 options.Sandbox,
		FileTracker:             options.FileTracker,
		Hooks:                   options.Hooks,
		EnabledTools:            options.EnabledTools,
		DisabledTools:           options.DisabledTools,
		OnText:                  options.OnText,
		OnReasoning:             options.OnReasoning,
		OnToolCall:              options.OnToolCall,
		OnToolCallStart:         options.OnToolCallStart,
		OnToolCallDelta:         options.OnToolCallDelta,
		OnPermissionRequest:     options.OnPermissionRequest,
		OnPermission:            options.OnPermission,
		OnToolResult:            options.OnToolResult,
		OnUsage:                 options.OnUsage,
		OnAttributedUsage:       options.OnAttributedUsage,
		EstimateUsageCost:       options.EstimateUsageCost,
		EstimateServedUsageCost: options.EstimateServedUsageCost,
		OnToolProgress:          options.OnToolProgress,
		OnToolOutput:            options.OnToolOutput,
		FileDiagnostics:         options.FileDiagnostics,
		StageModelResolver:      options.StageModelResolver,
		NodeModelResolver:       options.NodeModelResolver,
		EscalationModelResolver: options.EscalationModelResolver,
		OnSurfaceToUser:         options.OnSurfaceToUser,
		OnPipelinePlan:          options.OnPipelinePlan,
		OnStageEvent:            options.OnStageEvent,
		OnNotice:                options.OnNotice,
		EventSink:               options.EventSink,
		SecretValues:            append([]string(nil), options.SecretValues...),
		OnPresentationState:     options.OnPresentationState,
		ModelRegistry:           options.ModelRegistry,
	}
}

// agentOptions copies the fields shared policy helpers need. It is not a
// general conversion back to agent.Options.
func (c PipelineRunConfig) agentOptions() agent.Options {
	return agent.Options{
		SessionID:        c.SessionID,
		Model:            c.Model,
		ReasoningEffort:  c.ReasoningEffort,
		Cwd:              c.Cwd,
		Registry:         c.Registry,
		PermissionMode:   c.PermissionMode,
		Autonomy:         c.Autonomy,
		TrustedWorkspace: c.TrustedWorkspace,
		Sandbox:          c.Sandbox,
		FileTracker:      c.FileTracker,
		Hooks:            c.Hooks,
		EnabledTools:     c.EnabledTools,
		DisabledTools:    c.DisabledTools,
		OnToolProgress:   c.OnToolProgress,
		OnToolOutput:     c.OnToolOutput,
		FileDiagnostics:  c.FileDiagnostics,
	}
}

// pipelineIgnoredAgentOptionReasons lists every agent.Options field the
// pipeline does not consume. Reasons must stay non-empty.
var pipelineIgnoredAgentOptionReasons = map[string]string{
	"DecisionEvaluator":          "the CLI constructs the decision transport; the pipeline receives the built advisor through resolveDecisionAdvisor, not through the copied config",
	"MaxTurns":                   "agent.Run turn cap; pipeline passes use defaultMaxIterations (SD6)",
	"Pipeline":                   "the --pipeline topology selection is resolved by Run after workspace trust, not by PipelineConfigFromAgentOptions",
	"TrustRoot":                  "the project topology root is resolved by Run (topologyRoot) from the trusted directory, not copied into PipelineRunConfig",
	"DeferThreshold":             "deferred MCP/tool_search loading is agent-loop only",
	"Specialists":                "Task-tool prompt decoration is agent-loop only",
	"Skills":                     "skill-tool prompt decoration is agent-loop only",
	"CallingSessionID":           "specialist parent metadata; the pipeline is not a specialist child",
	"CallingToolUseID":           "specialist parent metadata; the pipeline is not a specialist child",
	"Tag":                        "specialist/sub-agent tagging is agent-loop only",
	"Depth":                      "specialist nesting; the pipeline tool runner does not set Depth",
	"SessionTitle":               "session chrome; pipeline stages do not read it",
	"ServerTools":                "provider-native tools are requested by agent.Run, not splicerun",
	"SystemPrompt":               "pipeline stages use their own typed prompts",
	"ResponseStyle":              "agent-loop system-prompt style; pipeline stages do not read it",
	"PriorMessages":              "agent-loop multi-turn seed; the pipeline is plan-driven",
	"ContextWindow":              "agent-loop compaction",
	"CompactionPreserveLast":     "agent-loop compaction",
	"CompactionReserveTokens":    "agent-loop compaction",
	"CompactionKeepRecentTokens": "agent-loop compaction",
	"OnAskUser":                  "ask_user is agent-loop; pipeline stages do not invoke it",
	"OnCompactionUsage":          "compaction is agent-loop only",
	"OnContext":                  "per-turn context budget is agent-loop",
	"ModelSwitcher":              "mid-run escalate_model is agent-loop",
	"SelfCorrect":                "post-edit verify cycle is agent-loop",
	"RequireCompletionSignal":    "headless agent.Run completion gate",
	"runPermissions":             "unexported agent-loop permission session state",
	"NoSandboxEscape":            "set by the agent node runner; an agent node never runs outside the sandbox, and the interactive run leaves it off",
	"SystemAddendum":             "set per agent node by the agent node runner, which appends the data-block rule; the interactive run leaves it empty",
	"RequireReadBeforeWrite":     "the pipeline sets StageRequireReadBeforeWrite for each stage run and passes it to agent nodes; the loop reads this field only for direct agent runs",
}

// BudgetFitEnvVar is the legacy name of the pipeline.budget_fit flag. Unset or
// "fit" keeps learned budgets; "off" freezes every stage at its static budget
// with named provenance. Warm/cold evaluation arms set "off" so learned
// fitting cannot give one arm different effective budgets than the other.
const BudgetFitEnvVar = "SPLICE_BUDGET_FIT"

// DecisionAdvisorModeEnvVar selects the advisor's run mode. It follows the
// same precedent as the other experimental treatments: the flag is the
// kill switch, and the mode comes from the environment until the config
// section is bridged at the CLI seam. Unset or "off" keeps the advisor
// nil: no snapshot, no decision, no call.
const DecisionAdvisorModeEnvVar = config.DecisionAdvisorModeEnv

// DecisionAdvisorModelEnvVar pins the decision model. Empty takes the
// documented alpha default.
const DecisionAdvisorModelEnvVar = config.DecisionAdvisorModelEnv

// DecisionRecordsDirEnvVar selects the directory for each run's decision records.
const DecisionRecordsDirEnvVar = "SPLICE_DECISION_RECORDS_DIR"

// DecisionAdvisorDefaultModel is the pinned alpha model from the verified
// M2 smoke.
const DecisionAdvisorDefaultModel = "typesafe/jev-1.13"

// resolveDecisionAdvisor applies the flag gates and environment overrides to
// the config section. The trajectory classifier flag gates the classifier.
// The decision advisor flag gates the dependency selector and the prefetch
// decisions. An absent or off mode returns a nil advisor. The default section
// (set by the config resolver) runs the classifier alone. Invalid enabled
// config returns an error.
func resolveDecisionAdvisor(flagSet flags.Set, evaluator zeroruntime.DecisionEvaluator, section config.DecisionAdvisorConfig, env func(string) string) (*DecisionAdvisor, error) {
	selectorOn := flagSet.Enabled(flags.DecisionAdvisorEnabled)
	classifierOn := flagSet.Enabled(flags.TrajectoryClassifierEnabled)
	if mode := strings.TrimSpace(env(DecisionAdvisorModeEnvVar)); mode != "" {
		section.Mode = mode
	}
	if section.Mode == "" || section.Mode == string(AdvisorModeOff) {
		return nil, nil
	}
	// The default section is the trajectory classifier alone. It needs the
	// classifier flag and a built evaluator. A configured section needs the
	// decision advisor flag. The trajectory classifier flag alone drives only
	// the default section. The classifier flag still gates the classifier
	// inside a configured section.
	if section.TrajectoryDefault && (!classifierOn || evaluator == nil) {
		return nil, nil
	}
	if !section.TrajectoryDefault && !selectorOn {
		return nil, nil
	}
	if model := strings.TrimSpace(env(DecisionAdvisorModelEnvVar)); model != "" && !section.TrajectoryDefault {
		section.Profile = model
	}
	if section.Profile == "" {
		section.Profile = DecisionAdvisorDefaultModel
	}
	converted, err := AdvisorConfigFromResolved(section)
	if err != nil {
		return nil, err
	}
	advisor, err := NewDecisionAdvisor(converted, evaluator)
	if err != nil {
		return nil, err
	}
	advisor.selectorOff = !selectorOn || section.TrajectoryDefault
	advisor.classifierOff = !classifierOn
	if err := applyCalibrationCap(advisor, env); err != nil {
		return nil, err
	}
	return advisor, nil
}

// RealizedAdvisor is what one run's environment and config realize for the
// decision advisor. Active is false when no advisor is built. The eval harness
// compares it with each arm's definition.
type RealizedAdvisor struct {
	Active     bool
	Selector   bool
	Classifier bool
	Mode       AdvisorMode
	Model      string
}

// RealizeDecisionAdvisor applies the same gates a run applies and reports the
// advisor that results. It builds no request and makes no provider call.
func RealizeDecisionAdvisor(flagSet flags.Set, evaluator zeroruntime.DecisionEvaluator, section config.DecisionAdvisorConfig, env func(string) string) (RealizedAdvisor, error) {
	advisor, err := resolveDecisionAdvisor(flagSet, evaluator, section, env)
	if err != nil || advisor == nil {
		return RealizedAdvisor{}, err
	}
	return RealizedAdvisor{
		Active:     true,
		Selector:   !advisor.selectorOff,
		Classifier: !advisor.classifierOff,
		Mode:       advisor.config.Mode,
		Model:      advisor.config.Profile,
	}, nil
}

// DecisionAdvisorPUsefulEnvVar supplies the preregistered calibration for
// the dependency_selection class (the experiment-exception mechanism the
// M5 spec requires). Unset means uncalibrated: gate 2 refuses and the
// selector stays baseline. The value must parse as a probability in [0,1];
// anything else fails loud with the offending value named.
const DecisionAdvisorPUsefulEnvVar = "SPLICE_DECISION_ADVISOR_PUSEFUL"

// DecisionAdvisorCapUSDVar overrides the advisor's USD allowance from the
// campaign manifest. The task budget contains the allowance; it never
// extends it.
const DecisionAdvisorCapUSDVar = "SPLICE_DECISION_ADVISOR_CAP_USD"

// manifestCalibration is the fixed CalibrationStore a campaign manifest
// supplies through the environment. It implements the same seam W1's
// deterministic gate reads.
type manifestCalibration struct {
	p float64
}

func (m manifestCalibration) PUseful(class string) (float64, bool) {
	if class != "dependency_selection" {
		return 0, false
	}
	return m.p, true
}

// resolveCalibration reads the manifest calibration. It returns nil when
// the variable is unset: no calibration, named refusal, baseline.
func resolveCalibration(env func(string) string) (CalibrationStore, error) {
	raw := strings.TrimSpace(env(DecisionAdvisorPUsefulEnvVar))
	if raw == "" {
		return nil, nil
	}
	var p float64
	if _, err := fmt.Sscanf(raw, "%g", &p); err != nil || p < 0 || p > 1 {
		return nil, fmt.Errorf("decision_advisor: %s=%q is not a probability in [0,1]", DecisionAdvisorPUsefulEnvVar, raw)
	}
	return manifestCalibration{p: p}, nil
}

// applyCalibrationCap reads the manifest cap into the advisor's allowance.
func applyCalibrationCap(a *DecisionAdvisor, env func(string) string) error {
	raw := strings.TrimSpace(env(DecisionAdvisorCapUSDVar))
	if raw == "" {
		return nil
	}
	var cap float64
	if _, err := fmt.Sscanf(raw, "%g", &cap); err != nil || cap < 0 {
		return fmt.Errorf("decision_advisor: %s=%q is not a non-negative USD amount", DecisionAdvisorCapUSDVar, raw)
	}
	a.config.Limits.MaxAdvisorCostUSD = cap
	return nil
}
