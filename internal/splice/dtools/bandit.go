package dtools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/tools"
)

// banditTool runs Bandit security analysis on Python files.
type banditTool struct {
	workspaceRoot string
}

// NewBanditTool returns a Tool that runs Bandit security analysis on Python files.
func NewBanditTool(workspaceRoot string) tools.Tool {
	return banditTool{workspaceRoot: workspaceRoot}
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
		abs, err := t.resolveWorkspacePath(p)
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

	cmdArgs := append([]string{"-m", "bandit", "-f", "json"}, resolvedPaths...)
	cmd := exec.CommandContext(ctx, python, cmdArgs...)
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
				Output: "Bandit is not installed or not available: " + err.Error(),
			}
		}
		if strings.Contains(string(out), "No module named") || strings.Contains(string(out), "ModuleNotFoundError") {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Bandit is not installed or not available: bandit module not found",
			}
		}
		// Bandit ran and exited non-zero, usually because it found issues.
		// The JSON output is still useful to the caller.
	}

	return tools.Result{
		Status: tools.StatusOK,
		Output: string(out),
	}
}

func (t banditTool) resolveWorkspacePath(requested string) (string, error) {
	root, err := filepath.Abs(t.workspaceRoot)
	if err != nil {
		return "", err
	}

	target := requested
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("%s escapes workspace", requested)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s escapes workspace", requested)
	}
	return abs, nil
}
