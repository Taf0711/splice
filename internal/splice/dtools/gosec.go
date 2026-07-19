package dtools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/tools"
)

// gosecTool runs Gosec security analysis on Go files.
type gosecTool struct {
	workspaceRoot string
}

// NewGosecTool returns a Tool that runs Gosec security analysis on Go files.
func NewGosecTool(workspaceRoot string) tools.Tool {
	return gosecTool{workspaceRoot: workspaceRoot}
}

func (t gosecTool) Name() string {
	return "gosec"
}

func (t gosecTool) Description() string {
	return "Run Gosec security analysis on Go files and return JSON output."
}

func (t gosecTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"paths": {
				Type:        "array",
				Description: "Relative paths of Go files or packages to scan.",
				Items:       &tools.PropertySchema{Type: "string"},
			},
		},
		Required:             []string{"paths"},
		AdditionalProperties: false,
	}
}

func (t gosecTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectShell,
		Permission: tools.PermissionPrompt,
		Reason:     "Runs the Gosec security scanner as a subprocess.",
	}
}

func (t gosecTool) Run(ctx context.Context, args map[string]any) tools.Result {
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

	gosecPath, err := exec.LookPath("gosec")
	if err != nil {
		return tools.Result{
			Status: tools.StatusError,
			Output: "Gosec is not installed or not available: " + err.Error(),
		}
	}

	cmdArgs := append([]string{"-fmt", "json"}, resolvedPaths...)
	cmd := exec.CommandContext(ctx, gosecPath, cmdArgs...)
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
				Output: "Gosec is not installed or not available: " + err.Error(),
			}
		}
		// Gosec ran and exited non-zero, usually because it found issues.
		// The JSON output is still useful to the caller.
	}

	return tools.Result{
		Status: tools.StatusOK,
		Output: string(out),
	}
}

func (t gosecTool) resolveWorkspacePath(requested string) (string, error) {
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
