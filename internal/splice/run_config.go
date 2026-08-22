package splice

import (
	"context"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/streamjson"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// PipelineRunConfig is the subset of agent.Options the deterministic pipeline
// actually reads. Public Run and design-plan entry points still accept
// agent.Options and convert here so inherited Zero types stay free of
// Splice-specific methods.
type PipelineRunConfig struct {
	SessionID        string
	ProviderName     string
	Model            string
	ReasoningEffort  string
	Cwd              string
	ProjectRoot      string
	MemoryStatus     string
	Images           []zeroruntime.ImageBlock
	Registry         *tools.Registry
	PermissionMode   agent.PermissionMode
	Autonomy         string
	TrustedWorkspace bool
	Sandbox          *sandbox.Engine
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
	OnToolProgress              func(toolCallID string, event streamjson.Event)
	OnToolOutput                func(tools.OutputSnapshot)
	FileDiagnostics             func(ctx context.Context, absPath string) string
	StageModelResolver          agent.StageModelResolver
	EscalationModelResolver     agent.EscalationModelResolver
	OnSurfaceToUser             func(ctx context.Context, req agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error)
	OnPipelinePlan              func(agent.PipelinePlanEvent)
	OnStageEvent                func(agent.StageEvent)
	ModelRegistry               modelregistry.Registry
}

// PipelineConfigFromAgentOptions copies the fields the pipeline consumes.
// Fields not present on PipelineRunConfig are classified in
// pipelineIgnoredAgentOptionReasons; a new agent.Options field fails
// TestPipelineRunConfigClassifiesEveryAgentOption until a human classifies it.
func PipelineConfigFromAgentOptions(options agent.Options) PipelineRunConfig {
	return PipelineRunConfig{
		SessionID:               options.SessionID,
		ProviderName:            options.ProviderName,
		Model:                   options.Model,
		ReasoningEffort:         options.ReasoningEffort,
		Cwd:                     options.Cwd,
		ProjectRoot:             options.ProjectRoot,
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
		OnToolProgress:          options.OnToolProgress,
		OnToolOutput:            options.OnToolOutput,
		FileDiagnostics:         options.FileDiagnostics,
		StageModelResolver:      options.StageModelResolver,
		EscalationModelResolver: options.EscalationModelResolver,
		OnSurfaceToUser:         options.OnSurfaceToUser,
		OnPipelinePlan:          options.OnPipelinePlan,
		OnStageEvent:            options.OnStageEvent,
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
	"MaxTurns":                   "agent.Run turn cap; pipeline passes use defaultMaxIterations (SD6)",
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
}
