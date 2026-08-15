package splice

import (
	"context"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// PipelineRunConfig is the subset of agent.Options the deterministic pipeline
// actually reads. Public Run and design-plan entry points still accept
// agent.Options and convert here so inherited Zero types stay free of
// Splice-specific methods.
type PipelineRunConfig struct {
	SessionID               string
	ProviderName            string
	Model                   string
	ReasoningEffort         string
	Cwd                     string
	Images                  []zeroruntime.ImageBlock
	Registry                *tools.Registry
	PermissionMode          agent.PermissionMode
	Autonomy                string
	TrustedWorkspace        bool
	Sandbox                 *sandbox.Engine
	FileTracker             *tools.FileTracker
	OnText                  func(string)
	OnReasoning             func(string)
	OnToolCall              func(agent.ToolCall)
	OnToolCallStart         func(id, name string)
	OnToolCallDelta         func(id, fragment string)
	OnPermissionRequest     func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error)
	OnPermission            func(agent.PermissionEvent)
	OnToolResult            func(agent.ToolResult)
	OnUsage                 func(agent.Usage)
	OnAttributedUsage       func(agent.AttributedUsage)
	EstimateUsageCost       func(model string, usage agent.Usage, reported bool) agent.UsageCostEstimate
	StageModelResolver      agent.StageModelResolver
	EscalationModelResolver agent.EscalationModelResolver
	OnSurfaceToUser         func(ctx context.Context, req agent.SurfaceToUserRequest) (agent.SurfaceToUserDecision, error)
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
		Images:                  options.Images,
		Registry:                options.Registry,
		PermissionMode:          options.PermissionMode,
		Autonomy:                options.Autonomy,
		TrustedWorkspace:        options.TrustedWorkspace,
		Sandbox:                 options.Sandbox,
		FileTracker:             options.FileTracker,
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
		StageModelResolver:      options.StageModelResolver,
		EscalationModelResolver: options.EscalationModelResolver,
		OnSurfaceToUser:         options.OnSurfaceToUser,
	}
}

// pipelineIgnoredAgentOptionReasons lists every agent.Options field the
// pipeline does not consume. Reasons must stay non-empty. SD12 may move
// Hooks, EnabledTools, DisabledTools, OnToolProgress, OnToolOutput, and
// FileDiagnostics from this map onto PipelineRunConfig.
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
	"Hooks":                      "pipeline tool runner does not dispatch beforeTool/afterTool (SD12)",
	"EnabledTools":               "pipeline tool runner does not enforce tool filters (SD12)",
	"DisabledTools":              "pipeline tool runner does not enforce tool filters (SD12)",
	"OnAskUser":                  "ask_user is agent-loop; pipeline stages do not invoke it",
	"OnCompactionUsage":          "compaction is agent-loop only",
	"OnToolProgress":             "Task/specialist progress; the pipeline does not invoke Task (SD12)",
	"OnToolOutput":               "pipeline does not forward live shell snapshots (SD12)",
	"OnContext":                  "per-turn context budget is agent-loop",
	"ModelSwitcher":              "mid-run escalate_model is agent-loop",
	"SelfCorrect":                "post-edit verify cycle is agent-loop",
	"FileDiagnostics":            "pipeline does not pass Diagnostics to tools.RunOptions (SD12)",
	"RequireCompletionSignal":    "headless agent.Run completion gate",
	"runPermissions":             "unexported agent-loop permission session state",
}
