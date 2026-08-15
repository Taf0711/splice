package agent

import (
	"context"

	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/streamjson"
	"github.com/Taf0711/splice/internal/tools"
)

// DeniedByToolFilters reports whether name is blocked by the run's operator
// filters and, if so, returns the same denial the agent loop surfaces.
func DeniedByToolFilters(name string, enabledTools []string, disabledTools []string) (ToolResult, bool) {
	if ToolAllowedByFilters(name, enabledTools, disabledTools) {
		return ToolResult{}, false
	}
	return ToolResult{
		Name:         name,
		Status:       tools.StatusError,
		Output:       `Error: Tool "` + name + `" is not enabled for this run.`,
		DenialReason: DenialFiltered,
	}, true
}

// RunBeforeToolHooks dispatches beforeTool hooks. A blocked outcome means the
// tool must not run.
func RunBeforeToolHooks(ctx context.Context, options Options, call ToolCall, args map[string]any) (hooks.DispatchOutcome, bool) {
	return dispatchBeforeTool(ctx, options, call, args)
}

// HookBlockedResult is the tool result for a beforeTool veto.
func HookBlockedResult(call ToolCall, outcome hooks.DispatchOutcome) ToolResult {
	return blockedByHookResult(call, outcome)
}

// RunAfterToolHooks dispatches afterTool hooks and returns advisory feedback.
func RunAfterToolHooks(ctx context.Context, options Options, call ToolCall, args map[string]any, result tools.Result) string {
	return dispatchAfterTool(ctx, options, call, args, result)
}

// AppendHookFeedback appends afterTool output onto a tool result string.
func AppendHookFeedback(output string, feedback string) (string, bool) {
	return appendHookFeedback(output, feedback)
}

// ToolProgressFunc wires specialist progress for a Task call. It is nil for
// every other tool, or when OnToolProgress is unset.
func ToolProgressFunc(call ToolCall, onProgress func(toolCallID string, event streamjson.Event)) func(streamjson.Event) {
	if call.Name != "Task" || onProgress == nil {
		return nil
	}
	toolCallID := call.ID
	return func(event streamjson.Event) {
		onProgress(toolCallID, event)
	}
}

// NewToolRunOptions builds the registry options for one tool call, including
// operator filters, live output, specialist progress, and diagnostics.
func NewToolRunOptions(options Options, call ToolCall, cwd string, permissionGranted bool) tools.RunOptions {
	if cwd == "" {
		cwd = options.Cwd
	}
	return tools.RunOptions{
		PermissionGranted: permissionGranted,
		PermissionMode:    string(options.PermissionMode),
		Autonomy:          options.Autonomy,
		TrustedWorkspace:  options.TrustedWorkspace,
		Sandbox:           options.Sandbox,
		ToolCallID:        call.ID,
		SessionID:         options.SessionID,
		Model:             options.Model,
		ReasoningEffort:   options.ReasoningEffort,
		Depth:             options.Depth,
		Cwd:               cwd,
		FileTracker:       options.FileTracker,
		EnabledTools:      options.EnabledTools,
		DisabledTools:     options.DisabledTools,
		Progress:          ToolProgressFunc(call, options.OnToolProgress),
		OnToolOutput:      options.OnToolOutput,
		Diagnostics:       options.FileDiagnostics,
	}
}
