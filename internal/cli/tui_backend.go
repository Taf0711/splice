package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Taf0711/splice/internal/tui"
)

// tuiBackendCommand is a CLI surface the TUI may run in-process. Every entry
// is read-only toward configuration and git state: the TUI shows backend
// state through the CLI's own tested renderers instead of re-deriving it.
// Mutating forms (hooks add, plugins remove, changes commit/push/pr, ...)
// stay CLI-only because they need their own confirmation flow.
type tuiBackendCommand struct {
	run   func([]string, io.Writer, io.Writer, appDeps) int
	sub   string          // required subcommand, "" when the command has none
	flags map[string]bool // allowed flags; true means the flag takes a value
}

var tuiBackendCommands = map[string]tuiBackendCommand{
	"hooks":      {run: runHooks, sub: "list"},
	"plugins":    {run: runPlugins, sub: "list"},
	"specialist": {run: runSpecialists, sub: "list"},
	"usage":      {run: runUsage, sub: "report", flags: map[string]bool{"--days": true, "--since": true, "--session": true}},
	"changes":    {run: runChanges, sub: "inspect", flags: map[string]bool{"--base": true}},
	"verify":     {run: runVerifyCommand, flags: map[string]bool{"--only": true}},
}

// tuiBackendArgs validates args against the allowlist and returns the
// command to run. The TUI builds these args, but this is the side that
// executes, so it enforces the boundary.
func tuiBackendArgs(args []string) (tuiBackendCommand, error) {
	if len(args) == 0 {
		return tuiBackendCommand{}, fmt.Errorf("no command")
	}
	command, ok := tuiBackendCommands[args[0]]
	if !ok {
		return tuiBackendCommand{}, fmt.Errorf("splice %s is not available from the TUI", args[0])
	}
	rest := args[1:]
	if command.sub != "" {
		if len(rest) == 0 || rest[0] != command.sub {
			return tuiBackendCommand{}, fmt.Errorf("splice %s: only %q is available from the TUI", args[0], command.sub)
		}
		rest = rest[1:]
	}
	for i := 0; i < len(rest); i++ {
		takesValue, allowed := command.flags[rest[i]]
		if !allowed {
			return tuiBackendCommand{}, fmt.Errorf("splice %s: %q is not available from the TUI", args[0], rest[i])
		}
		if takesValue {
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
				return tuiBackendCommand{}, fmt.Errorf("splice %s: %s needs a value", args[0], rest[i])
			}
			i++
		}
	}
	return command, nil
}

// newTUIBackendCommand adapts the allowlisted CLI commands to the TUI's
// BackendCommand option. Output is captured, never written to the terminal
// the TUI owns.
func newTUIBackendCommand(deps appDeps) func(context.Context, []string) tui.BackendCommandResult {
	return func(_ context.Context, args []string) tui.BackendCommandResult {
		command, err := tuiBackendArgs(args)
		if err != nil {
			return tui.BackendCommandResult{Error: err.Error(), ExitCode: 2}
		}
		var stdout, stderr bytes.Buffer
		code := command.run(args[1:], &stdout, &stderr, deps)
		return tui.BackendCommandResult{
			Output:   strings.TrimSpace(stdout.String()),
			Error:    strings.TrimSpace(stderr.String()),
			ExitCode: code,
		}
	}
}
