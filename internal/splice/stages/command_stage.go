package stages

import (
	"context"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// maxCommandOutputChars bounds the output a command node carries downstream.
// A command node's output is untrusted: a downstream LLM stage receives it as
// delimited data, never as instructions.
const maxCommandOutputChars = 4000

// commandToolName is the one registry tool a command node may run through.
// It is fixed by the implementation, so a topology cannot name an arbitrary
// tool and route around the registry.
const commandToolName = "bash"

// CommandStage runs one topology command node. It executes the node's argv
// through the designated shell tool on the sandboxed registry path and is
// model-free. A command node is not a verification authority in v1: its output
// is a bounded summary, not a VerificationReport.
type CommandStage struct {
	// Name is the node name; it labels errors and activity.
	Name string
	// Command is the argv to run.
	Command []string
}

var _ Stage = CommandStage{}

// Capabilities reports the command node as model-free so the executor skips
// provider resolution.
func (s CommandStage) Capabilities() Capabilities {
	return Capabilities{ModelFree: true, Description: "running command " + s.Name}
}

// Run executes the node's command. It requires a sandboxed runner: with
// neither RunTool nor RecordCommand it fails loud instead of spawning a
// process outside the registry path.
func (s CommandStage) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if len(s.Command) == 0 {
		return schemas.HarnessStageOutput{}, fmt.Errorf("command node %s: command is empty", s.Name)
	}
	if options.RunTool == nil && options.RecordCommand == nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("command node %s: no sandboxed shell tool is available; refusing to run outside the registry path", s.Name)
	}
	timeout := options.TimeoutSeconds
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds
	}
	command := strings.Join(s.Command, " ")
	options.report("running " + command)
	args := map[string]any{
		"command":    command,
		"cwd":        options.WorkDir,
		"timeout_ms": timeout * 1000,
	}
	run := func(runCtx context.Context) (ToolResult, error) {
		if options.RunTool == nil {
			return ToolResult{}, fmt.Errorf("command node %s: no registry tool runner", s.Name)
		}
		return options.RunTool(runCtx, commandToolName, args)
	}
	var (
		result ToolResult
		err    error
	)
	if options.RecordCommand != nil {
		result, err = options.RecordCommand(ctx, "splice.command", args, run)
	} else {
		result, err = run(ctx)
	}
	if err != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("command node %s: %w", s.Name, err)
	}
	output := truncateCommandOutput(result.Output)
	status := "failed"
	if result.OK {
		status = "succeeded"
	}
	return schemas.HarnessStageOutput{
		Summary:    fmt.Sprintf("command %q %s", command, status),
		Detail:     output,
		Confidence: 1.0,
		Data: map[string]any{
			"command":        command,
			"command_output": output,
			"command_ok":     result.OK,
		},
	}, nil
}

// truncateCommandOutput bounds untrusted command output to maxCommandOutputChars
// characters, counting runes so the result stays valid UTF-8.
func truncateCommandOutput(value string) string {
	if len(value) <= maxCommandOutputChars {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxCommandOutputChars {
		return value
	}
	return string(runes[:maxCommandOutputChars])
}
