package stages

import (
	"context"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// Stage is one deterministic or LLM-backed pipeline step.
type Stage interface {
	Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error)
}

// StageOptions carries cross-stage dependencies and configuration.
type StageOptions struct {
	// WorkDir is the workspace root where file changes are applied.
	WorkDir string
	// Language is the dominant project language.
	Language string
	// TargetPaths are optional explicit files this stage should focus on.
	TargetPaths []string
	// RelevantContext is static context text injected into LLM prompts.
	RelevantContext []string
	// PullContext requests a default context bundle when true.
	PullContext bool
	// OverrideContextRequest is emitted instead of the default when set.
	OverrideContextRequest *schemas.ContextRequest
	// RunTool executes a deterministic tool from the registry.
	RunTool func(ctx context.Context, name string, args map[string]any) (ToolResult, error)
	// ReportActivity emits observable stage activity.
	ReportActivity func(message string)
	// Stream forwards live provider output for LLM-backed stages.
	Stream zeroruntime.CollectOptions
	// Images are optional user-supplied image attachments for the initial task.
	Images []zeroruntime.ImageBlock
	// RecordCommand wraps deterministic shell-like commands with tool callbacks.
	RecordCommand func(ctx context.Context, name string, args map[string]any, run func(context.Context) (ToolResult, error)) (ToolResult, error)
	// ModelOverride is an optional model identifier for LLM stages.
	ModelOverride string
	// ReasoningEffort is the per-stage reasoning effort (minimal/low/medium/high).
	// Empty means "let the provider decide".
	ReasoningEffort string
	// Command is an explicit command for deterministic stages like test_runner.
	Command []string
	// TimeoutSeconds bounds deterministic subprocess execution.
	TimeoutSeconds int
	// Plan is used by the plan_critic stage.
	Plan *schemas.DesignPlan
}

// ToolResult is the minimal deterministic-adapter result a stage needs.
type ToolResult struct {
	OK        bool
	Output    string
	Truncated bool
	Meta      map[string]string
}

func (o StageOptions) report(message string) {
	if o.ReportActivity != nil {
		o.ReportActivity(message)
	}
}

func (o StageOptions) model(defaultModel string) string {
	if o.ModelOverride != "" {
		return o.ModelOverride
	}
	return defaultModel
}
