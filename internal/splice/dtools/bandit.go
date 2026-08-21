package dtools

import (
	"context"
	"os/exec"
	"strings"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/tools"
)

// banditTool runs Bandit security analysis on Python files.
type banditTool struct {
	workspaceRoot string
	// sandbox scopes the scanner subprocess to the workspace with network
	// denied. It is built once at construction from the workspace root.
	sandbox *sandbox.Engine
}

// NewBanditTool returns a Tool that runs Bandit security analysis on Python files.
func NewBanditTool(workspaceRoot string) tools.Tool {
	return banditTool{workspaceRoot: workspaceRoot, sandbox: procrun.NewStageEngine(workspaceRoot)}
}

func (t banditTool) Name() string {
	return "bandit"
}

func (t banditTool) Description() string {
	return "Run Bandit security analysis on Python files and return JSON output."
}

func (t banditTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"paths": {
				Type:        "array",
				Description: "Relative paths of Python files to scan.",
				Items:       &tools.PropertySchema{Type: "string"},
			},
		},
		Required:             []string{"paths"},
		AdditionalProperties: false,
	}
}

func (t banditTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectShell,
		Permission: tools.PermissionPrompt,
		Reason:     "Runs the Bandit security scanner as a subprocess.",
	}
}

func (t banditTool) Run(ctx context.Context, args map[string]any) tools.Result {
	rawPaths, ok := args["paths"]
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: "paths is required"}
	}
	pathsArg, ok := rawPaths.([]any)
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: "paths must be an array of strings"}
	}
	if len(pathsArg) == 0 {
		return tools.Result{Status: tools.StatusError, Output: "paths must not be empty"}
	}

	resolvedPaths := make([]string, 0, len(pathsArg))
	for _, item := range pathsArg {
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

	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
		if err != nil {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Bandit is not installed or not available: " + err.Error(),
			}
		}
	}

	command := append([]string{python, "-m", "bandit", "-f", "json"}, resolvedPaths...)
	prepared, cerr := procrun.Prepare(ctx, procrun.Request{
		ProfileID:       procrun.ProfileSpliceDTools,
		AllowedBinaries: procrun.StageBinaries,
		Engine:          t.sandbox,
		Spec:            sandbox.CommandSpec{Name: command[0], Args: command[1:], Dir: t.workspaceRoot},
	})
	if cerr != nil {
		return tools.Result{
			Status: tools.StatusError,
			Output: "Bandit is not installed or not available: " + cerr.Error(),
		}
	}
	defer prepared.Plan.Cleanup()
	cmd := prepared.Cmd

	out, err := cmd.Output()
	if ctx.Err() != nil {
		return tools.Result{
			Status: tools.StatusError,
			Output: ctx.Err().Error(),
		}
	}
	if err != nil {
		exitErr, isExit := err.(*exec.ExitError)
		if !isExit {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Bandit is not installed or not available: " + err.Error(),
			}
		}
		stderr := string(exitErr.Stderr)
		if strings.Contains(stderr, "No module named") || strings.Contains(stderr, "ModuleNotFoundError") {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Bandit is not installed or not available: bandit module not found",
			}
		}
		// Bandit ran and exited non-zero, usually because it found issues.
		// The JSON output is still useful to the caller.
		if len(out) == 0 {
			return tools.Result{
				Status: tools.StatusError,
				Output: stderr,
			}
		}
	}

	return tools.Result{
		Status: tools.StatusOK,
		Output: string(out),
	}
}
