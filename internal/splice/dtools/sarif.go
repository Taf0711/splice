package dtools

import (
	"context"
	"os/exec"
	"time"

	"github.com/Taf0711/splice/internal/tools"
)

// sarifTool runs an arbitrary SARIF-emitting scanner in the workspace.
type sarifTool struct {
	workspaceRoot string
}

// NewSarifTool returns a Tool that runs a configurable SARIF scanner.
func NewSarifTool(workspaceRoot string) tools.Tool {
	return sarifTool{workspaceRoot: workspaceRoot}
}

func (t sarifTool) Name() string {
	return "sarif"
}

func (t sarifTool) Description() string {
	return "Run a SARIF-compatible scanner and return SARIF JSON output."
}

func (t sarifTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"command": {
				Type:        "string",
				Description: "Scanner binary name (e.g. eslint, gosec).",
			},
			"args": {
				Type:        "array",
				Description: "Arguments to pass to the scanner, including the SARIF format flag.",
				Items:       &tools.PropertySchema{Type: "string"},
			},
			"paths": {
				Type:        "array",
				Description: "Relative paths to scope the scan.",
				Items:       &tools.PropertySchema{Type: "string"},
			},
		},
		Required:             []string{"command"},
		AdditionalProperties: false,
	}
}

func (t sarifTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectShell,
		Permission: tools.PermissionPrompt,
		Reason:     "Runs a SARIF security scanner as a subprocess.",
	}
}

func (t sarifTool) Run(ctx context.Context, args map[string]any) tools.Result {
	commandRaw, ok := args["command"]
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: "command is required"}
	}
	command, ok := commandRaw.(string)
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: "command must be a string"}
	}

	scannerArgs := []string{}
	if rawArgs, ok := args["args"]; ok && rawArgs != nil {
		argsAny, ok := rawArgs.([]any)
		if !ok {
			return tools.Result{Status: tools.StatusError, Output: "args must be an array of strings"}
		}
		for _, a := range argsAny {
			s, ok := a.(string)
			if !ok {
				return tools.Result{Status: tools.StatusError, Output: "args must be strings"}
			}
			scannerArgs = append(scannerArgs, s)
		}
	}

	resolvedPaths := []string{}
	if rawPaths, ok := args["paths"]; ok && rawPaths != nil {
		pathsAny, ok := rawPaths.([]any)
		if !ok {
			return tools.Result{Status: tools.StatusError, Output: "paths must be an array of strings"}
		}
		for _, item := range pathsAny {
			p, ok := item.(string)
			if !ok {
				return tools.Result{Status: tools.StatusError, Output: "paths must be strings"}
			}
			abs, err := resolveWorkspacePath(t.workspaceRoot, p)
			if err != nil {
				return tools.Result{Status: tools.StatusError, Output: err.Error()}
			}
			resolvedPaths = append(resolvedPaths, abs)
		}
	}

	scannerPath, err := exec.LookPath(command)
	if err != nil {
		return tools.Result{
			Status: tools.StatusError,
			Output: "SARIF scanner is not installed or not available: " + err.Error(),
		}
	}

	cmdArgs := append(scannerArgs, resolvedPaths...)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, scannerPath, cmdArgs...)
	cmd.Dir = t.workspaceRoot

	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return tools.Result{
			Status: tools.StatusError,
			Output: ctx.Err().Error(),
		}
	}
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			return tools.Result{
				Status: tools.StatusError,
				Output: "SARIF scanner is not installed or not available: " + err.Error(),
			}
		}
		// Scanner produced output with a non-zero exit (common with findings).
	}

	return tools.Result{
		Status: tools.StatusOK,
		Output: string(out),
	}
}
