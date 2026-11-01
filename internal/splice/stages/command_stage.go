package stages

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// maxCommandOutputChars bounds the output a command node carries downstream.
// A command node's output is untrusted. A downstream model stage receives it
// as bounded, marked data. The marker is not a sanitizer.
const maxCommandOutputChars = 4000

// commandToolName is the one registry tool a command node may run through.
// It is fixed by the implementation, so a topology cannot name an arbitrary
// tool and route around the registry.
const commandToolName = "bash"

// CommandLine returns the shell line that runs argv on this host. Each element
// stays one argument. On a POSIX host, the bash tool runs the line through
// /bin/sh, so each element is quoted for sh. A Windows host runs cmd.exe, and
// its quoting is not applied here: the elements are joined with spaces.
func CommandLine(argv []string) string {
	return commandLineFor(argv, runtime.GOOS)
}

func commandLineFor(argv []string, goos string) string {
	if goos == "windows" {
		return strings.Join(argv, " ")
	}
	words := make([]string, 0, len(argv))
	for _, arg := range argv {
		words = append(words, shellWord(arg))
	}
	return strings.Join(words, " ")
}

// shellSafeChars are the bytes that sh reads as themselves. An element made only
// of these stays unquoted, so the line stays readable.
const shellSafeChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_./=:,+@%-"

// shellWord quotes one argument for sh. An empty argument is the empty
// single-quoted word. Any other argument with a byte outside shellSafeChars is
// wrapped in single quotes. Each single quote inside it is written as a close
// quote, a backslash-escaped quote, and an open quote.
func shellWord(arg string) string {
	if arg != "" && strings.IndexFunc(arg, func(r rune) bool { return !strings.ContainsRune(shellSafeChars, r) }) < 0 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// CommandStage runs one topology command node. It executes the node's argv
// through the designated shell tool on the sandboxed registry path and is
// model-free. A command node is not a verification authority in v1: its output
// is a bounded summary, not a VerificationReport.
type CommandStage struct {
	// Name is the node name; it labels errors and activity.
	Name string
	// Command is the argv to run. Each element is passed as one argument. Use
	// ["sh", "-c", "..."] for shell syntax.
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
	command := CommandLine(s.Command)
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
	if !result.OK {
		// A failed command fails the stage. The pass then takes the
		// orchestrator's failed-stage path, so the run never verifies on it.
		lower := strings.ToLower(result.Output)
		if strings.Contains(lower, "permission required") || strings.Contains(lower, "permission denied") {
			return schemas.HarnessStageOutput{}, fmt.Errorf("command node %s: command %q denied by permission policy: %s", s.Name, command, output)
		}
		return schemas.HarnessStageOutput{}, fmt.Errorf("command node %s: command %q failed: %s", s.Name, command, output)
	}
	return schemas.HarnessStageOutput{
		Summary:    fmt.Sprintf("command %q succeeded", command),
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
