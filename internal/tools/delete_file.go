package tools

import (
	"context"
	"fmt"
	"os"
)

type deleteFileTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

// NewDeleteFileTool returns a workspace-scoped delete_file tool.
func NewDeleteFileTool(workspaceRoot string) Tool {
	return NewScopedDeleteFileTool(workspaceRoot, nil)
}

// NewScopedDeleteFileTool returns a delete_file tool constrained to the
// workspace and an optional extra write scope.
func NewScopedDeleteFileTool(workspaceRoot string, scope PathScope) Tool {
	return deleteFileTool{
		baseTool: baseTool{
			name:        "delete_file",
			description: "Delete a single regular file inside the workspace. Refuses to delete directories or the workspace root.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path": {Type: "string", Description: "Absolute or relative path of the file to delete."},
				},
				Required:             []string{"path"},
				AdditionalProperties: false,
			},
			safety: promptSafety(SideEffectWrite, "Deletes files permanently."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool deleteFileTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool deleteFileTool) RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result {
	if err := ctx.Err(); err != nil {
		return errorResult("delete_file cancelled: " + err.Error())
	}

	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filename"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for delete_file: " + err.Error())
	}

	absolutePath, relativePath, err := resolveScopedTargetPath(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error deleting file " + requestedPath + ": " + err.Error())
	}

	// Reject the workspace root explicitly; resolveScopedTargetPath returns "."
	// when the requested path resolves to the workspace directory.
	if relativePath == "." {
		return errorResult("Error: cannot delete the workspace root")
	}

	info, err := os.Lstat(absolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResult("Error: " + relativePath + " does not exist")
		}
		return errorResult("Error deleting file " + relativePath + ": " + err.Error())
	}
	if info.IsDir() {
		return errorResult("Error: " + relativePath + " is a directory; delete_file only removes regular files")
	}
	if !info.Mode().IsRegular() {
		return errorResult("Error: " + relativePath + " is not a regular file")
	}

	// Capture prior content so the result can carry a removed-content diff.
	priorContent := ""
	if prior, rerr := os.ReadFile(absolutePath); rerr == nil {
		priorContent = string(prior)
	}

	// Recheck symlink traversal immediately before the destructive operation.
	if err := recheckScopedWriteTarget(tool.workspaceRoot, tool.scope, requestedPath); err != nil {
		return errorResult("Error deleting file " + relativePath + ": " + err.Error())
	}

	if err := os.Remove(absolutePath); err != nil {
		return errorResult("Error deleting file " + relativePath + ": " + err.Error())
	}

	// Drop any FileTracker baseline for the deleted path so later stages do not
	// compare against a file that no longer exists.
	options.FileTracker.Forget(absolutePath)

	verb := "Deleted " + relativePath
	if priorContent != "" {
		verb = fmt.Sprintf("Deleted %s (%d lines)", relativePath, lineCount(priorContent))
	}
	summary := verb + "."
	result := okResult(summary)
	result.ChangedFiles = []string{relativePath}
	result.Display = Display{
		Summary: summary,
		Kind:    "diff",
		Preview: boundedUnifiedDiff(relativePath, priorContent, ""),
	}
	return result
}
