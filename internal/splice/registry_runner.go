package splice

import (
	"context"

	"github.com/Taf0711/splice/internal/tools"
)

// ToolResult is the deterministic context adapter's view of a tool run.
type ToolResult struct {
	OK        bool
	Output    string
	Truncated bool
	Meta      map[string]string
	Status    tools.Status
	Redacted  bool
	// ChangedFiles and Display mirror tools.Result so agent.Options callbacks can
	// receive faithful tool_result payloads without parsing tool output.
	ChangedFiles []string
	Display      tools.Display
}

// ToolRunner runs a tool by name for deterministic context fulfillment.
type ToolRunner interface {
	RunTool(ctx context.Context, name string, args map[string]any) (ToolResult, error)
}

// ToolRunnerFunc adapts a function to the ToolRunner interface.
type ToolRunnerFunc func(ctx context.Context, name string, args map[string]any) (ToolResult, error)

func (f ToolRunnerFunc) RunTool(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	return f(ctx, name, args)
}

// RegistryToolRunner adapts Zero's tools.Registry to the ToolRunner interface.
type RegistryToolRunner struct {
	registry *tools.Registry
}

// NewRegistryToolRunner wraps a tools.Registry. The passed registry should
// contain only read-only tools (read_file, list_directory, grep).
func NewRegistryToolRunner(registry *tools.Registry) RegistryToolRunner {
	return RegistryToolRunner{registry: registry}
}

// RunTool implements ToolRunner.
func (r RegistryToolRunner) RunTool(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	if _, ok := r.registry.Get(name); !ok {
		return ToolResult{}, errToolNotFound{tool: name}
	}
	res := r.registry.RunWithOptions(ctx, name, args, tools.RunOptions{})
	meta := res.Meta
	if meta == nil {
		meta = map[string]string{}
	}
	return ToolResult{
		OK:           res.Status == tools.StatusOK,
		Output:       res.Output,
		Truncated:    res.Truncated || meta["truncated"] == "true",
		Meta:         meta,
		Status:       res.Status,
		Redacted:     res.Redacted,
		ChangedFiles: res.ChangedFiles,
		Display:      res.Display,
	}, nil
}

type errToolNotFound struct {
	tool string
}

func (e errToolNotFound) Error() string {
	return "tool not found in registry: " + e.tool
}
