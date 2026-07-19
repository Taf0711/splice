package stages

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

//go:embed prompts/code_writer.md
var codeWriterSystemPrompt string

const codeWriterToolName = "submit_code"

// CodeWriter is the code writer pipeline stage.
type CodeWriter struct{}

var _ Stage = CodeWriter{}

func (CodeWriter) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	if input.Context == nil {
		req := options.contextRequest(input.RequestIntent)
		if req != nil {
			options.report("requesting context: " + req.Reason)
			return schemas.HarnessStageOutput{
				Summary:        "Code Writer requested codebase context.",
				Detail:         req.Reason,
				Confidence:     1.0,
				ContextRequest: req,
			}, nil
		}
	}

	if input.Context != nil {
		options.report(fmt.Sprintf("reviewed %d context item(s)", len(input.Context.Items)))
	}

	cwInput := schemas.CodeWriterInput{
		Intent:          input.RequestIntent,
		Language:        options.language("python"),
		TargetPaths:     options.TargetPaths,
		RelevantContext: selectRelevantContext(options.RelevantContext, input.PriorSummaries, input.Context),
		RevisionContext: input.RevisionContext,
		Memory:          selectMemory(input.MemoryBundle),
	}
	if err := cwInput.Validate(); err != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("code writer input: %w", err)
	}

	options.report("generating code changes")
	payload, err := json.MarshalIndent(cwInput, "", "  ")
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	collected, err := callValidatedToolUse(ctx, provider, options.model("medium"), options.ReasoningEffort, composeSystemPrompt(codeWriterSystemPrompt), string(payload), options.Images, submitCodeToolDefinition(), &options.Stream, func(collected *zeroruntime.CollectedStream) error {
		_, err := parseCodeWriterOutput(collected)
		return err
	})
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	output, err := parseCodeWriterOutput(collected)
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}

	changedPaths := make([]string, len(output.Files))
	for i, f := range output.Files {
		changedPaths[i] = f.Path
	}
	options.report("proposed changes: " + formatPathList(changedPaths, 5))

	data := map[string]any{
		"code_writer_input":  cwInput,
		"code_writer_output": output,
	}

	if len(output.Files) > 0 {
		if options.WorkDir == "" {
			return schemas.HarnessStageOutput{}, fmt.Errorf("code writer: WorkDir is required to apply %d file change(s)", len(output.Files))
		}
		options.report(fmt.Sprintf("applying %d file change(s)", len(output.Files)))
		apply, err := applyFileChanges(ctx, options.WorkDir, output.Files, options.RunTool)
		if err != nil {
			return schemas.HarnessStageOutput{}, fmt.Errorf("code writer: %w", err)
		}
		if len(apply.Applied) != len(output.Files) {
			return schemas.HarnessStageOutput{}, fmt.Errorf("code writer: applied %d of %d file changes", len(apply.Applied), len(output.Files))
		}
		options.report(fmt.Sprintf("applied %d file change(s)", len(apply.Applied)))
		data["file_apply_result"] = apply
	}

	return schemas.HarnessStageOutput{
		Summary:    output.Intent,
		Detail:     strings.Join(changedPaths, ", "),
		Confidence: output.Confidence,
		Data:       data,
		Usage:      usageFromCollected(collected),
	}, nil
}

func parseCodeWriterOutput(collected *zeroruntime.CollectedStream) (schemas.CodeWriterOutput, error) {
	tc := findToolCall(collected, codeWriterToolName)
	if tc == nil {
		return schemas.CodeWriterOutput{}, fmt.Errorf("model did not call %s", codeWriterToolName)
	}
	var output schemas.CodeWriterOutput
	if err := json.Unmarshal([]byte(tc.Arguments), &output); err != nil {
		return schemas.CodeWriterOutput{}, fmt.Errorf("parse %s args: %w", codeWriterToolName, err)
	}
	if err := output.Validate(); err != nil {
		return schemas.CodeWriterOutput{}, err
	}
	return output, nil
}

func submitCodeToolDefinition() zeroruntime.ToolDefinition {
	return zeroruntime.ToolDefinition{
		Name:        codeWriterToolName,
		Description: "Submit the complete CodeWriterOutput for the requested implementation.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files":             fileChangeArraySchema(),
				"language":          map[string]any{"type": "string"},
				"intent":            map[string]any{"type": "string"},
				"dependencies":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"known_limitations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"confidence":        map[string]any{"type": "number"},
			},
			"required": []string{"files", "language", "intent", "confidence"},
		},
	}
}

func applyFileChanges(ctx context.Context, workDir string, files []schemas.FileChange, runTool func(context.Context, string, map[string]any) (ToolResult, error)) (schemas.FileChangeApplyResult, error) {
	if workDir == "" {
		return schemas.FileChangeApplyResult{}, fmt.Errorf("workDir is required")
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return schemas.FileChangeApplyResult{}, fmt.Errorf("resolve work dir: %w", err)
	}

	result := schemas.FileChangeApplyResult{Workspace: absWorkDir, Applied: []schemas.AppliedFileChange{}}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("apply %s %s: %w", f.ChangeType, f.Path, err)
		}
		if err := f.Validate(); err != nil {
			return result, fmt.Errorf("apply invalid change: %w", err)
		}

		// Workspace confinement is enforced in two places on purpose. In registry
		// mode the scoped tool is the authority: it enforces the workspace AND any
		// explicitly granted extra write roots (--add-dir), so a resolve failure
		// here must not pre-empt a grant the tool would honor. In fallback mode
		// there is no tool, so the resolver is the only guard and stays mandatory.
		absTarget, relTarget, resolveErr := resolveApplyTarget(absWorkDir, f.Path)
		if resolveErr == nil && relTarget == "." {
			return result, fmt.Errorf("apply %s %s: cannot target workspace root", f.ChangeType, f.Path)
		}

		var bytesRead int
		if resolveErr == nil && (f.ChangeType == "modify" || f.ChangeType == "delete") {
			// Best-effort prior size for the apply record; the tool re-validates
			// existence and type itself in registry mode.
			if prior, rerr := os.ReadFile(absTarget); rerr == nil {
				bytesRead = len(prior)
			} else if runTool == nil {
				return result, fmt.Errorf("apply %s %s: read prior content: %w", f.ChangeType, f.Path, rerr)
			}
		}

		if runTool != nil {
			if err := ctx.Err(); err != nil {
				return result, fmt.Errorf("apply %s %s: %w", f.ChangeType, f.Path, err)
			}
			var toolName string
			var args map[string]any
			switch f.ChangeType {
			case "create":
				toolName = "write_file"
				args = map[string]any{"path": f.Path, "content": f.Content}
			case "modify":
				toolName = "write_file"
				args = map[string]any{"path": f.Path, "content": f.Content, "overwrite": true}
			case "delete":
				toolName = "delete_file"
				args = map[string]any{"path": f.Path}
			}
			res, err := runTool(ctx, toolName, args)
			if err != nil {
				return result, fmt.Errorf("apply %s %s: tool error: %w", f.ChangeType, f.Path, err)
			}
			if !res.OK {
				return result, fmt.Errorf("apply %s %s: %s", f.ChangeType, f.Path, res.Output)
			}
		} else {
			if resolveErr != nil {
				return result, fmt.Errorf("apply %s %s: %w", f.ChangeType, f.Path, resolveErr)
			}
			if err := ctx.Err(); err != nil {
				return result, fmt.Errorf("apply %s %s: %w", f.ChangeType, f.Path, err)
			}
			info, serr := os.Lstat(absTarget)
			switch f.ChangeType {
			case "create":
				if serr == nil {
					return result, fmt.Errorf("apply create %s: file already exists", f.Path)
				}
				if !os.IsNotExist(serr) {
					return result, fmt.Errorf("apply create %s: %w", f.Path, serr)
				}
				if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil {
					return result, fmt.Errorf("apply create %s: %w", f.Path, err)
				}
				if err := os.WriteFile(absTarget, []byte(f.Content), 0o644); err != nil {
					return result, fmt.Errorf("apply create %s: %w", f.Path, err)
				}
			case "modify":
				if serr != nil {
					return result, fmt.Errorf("apply modify %s: %w", f.Path, serr)
				}
				if info.IsDir() {
					return result, fmt.Errorf("apply modify %s: is a directory", f.Path)
				}
				if !info.Mode().IsRegular() {
					return result, fmt.Errorf("apply modify %s: not a regular file", f.Path)
				}
				if err := os.WriteFile(absTarget, []byte(f.Content), 0o644); err != nil {
					return result, fmt.Errorf("apply modify %s: %w", f.Path, err)
				}
			case "delete":
				if serr != nil {
					return result, fmt.Errorf("apply delete %s: %w", f.Path, serr)
				}
				if info.IsDir() {
					return result, fmt.Errorf("apply delete %s: is a directory", f.Path)
				}
				if !info.Mode().IsRegular() {
					return result, fmt.Errorf("apply delete %s: not a regular file", f.Path)
				}
				if err := os.Remove(absTarget); err != nil {
					return result, fmt.Errorf("apply delete %s: %w", f.Path, err)
				}
			}
		}

		recordPath := absTarget
		if resolveErr != nil {
			// A scope-granted target outside the workspace has no workspace-relative
			// resolution; record the path the tool accepted.
			recordPath = f.Path
		}
		result.Applied = append(result.Applied, schemas.AppliedFileChange{
			Path:       recordPath,
			ChangeType: f.ChangeType,
			BytesRead:  bytesRead,
		})
	}
	return result, nil
}

// resolveApplyTarget resolves requestedPath against workDir, follows workspace
// symlinks at the root level only, and rejects traversal outside the workspace,
// symlink traversal inside the workspace, and the workspace root itself.
// It is the direct-filesystem fallback companion to the scoped tool helpers.
func resolveApplyTarget(workDir, requestedPath string) (string, string, error) {
	root, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve workDir: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve workDir: %w", err)
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", fmt.Errorf("resolve target: %w", err)
	}

	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", "", fmt.Errorf("%s: outside workspace", requestedPath)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("%s: outside workspace", requestedPath)
	}
	if relative == "." {
		return "", "", fmt.Errorf("%s: workspace root is not a file target", requestedPath)
	}

	// Reject symlink traversal through any existing path segment.
	clean := filepath.Clean(relative)
	current := root
	for _, segment := range strings.Split(clean, string(filepath.Separator)) {
		if segment == "." || segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return "", "", fmt.Errorf("%s: %w", requestedPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("%s: must not traverse symlink", requestedPath)
		}
	}

	return target, filepath.ToSlash(relative), nil
}
