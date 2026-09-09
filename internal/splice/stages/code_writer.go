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
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

//go:embed prompts/code_writer.md
var codeWriterSystemPrompt string

const codeWriterToolName = "submit_code"

// CodeWriter is the code writer pipeline stage.
type CodeWriter struct{}

var _ Stage = CodeWriter{}

func (CodeWriter) Capabilities() Capabilities {
	return Capabilities{ConsumesMemory: true, PullContext: true, Description: "writing code changes"}
}

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
		RelevantContext: selectRelevantContext(options.RelevantContext, input.PriorSummaries, input.Context, input.PipelineStages),
		RevisionContext: input.RevisionContext,
		Memory:          selectMemory(input.MemoryBundle),
		PipelineStages:  input.PipelineStages,
		NextStage:       input.NextStage,
	}
	if err := cwInput.Validate(); err != nil {
		return schemas.HarnessStageOutput{}, fmt.Errorf("code writer input: %w", err)
	}

	options.report("generating code changes")
	payload, err := json.MarshalIndent(cwInput, "", "  ")
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	// D1: the validate callback decodes the discriminated action. A
	// request_context action is VALID typed output (no format retry); the
	// orchestrator fulfills it and re-invokes. A submit action goes
	// through the existing proposal parse.
	collected, err := callValidatedToolUse(ctx, provider, options.model("medium"), options.ReasoningEffort, composeSystemPrompt(codeWriterSystemPrompt), string(payload), options.Images, submitCodeToolDefinition(len(cwInput.Memory) > 0), options.MaxOutputTokens, &options.Stream, func(collected *zeroruntime.CollectedStream) error {
		action, err := TryDecodeStageAction(codeWriterToolName, collected)
		if err != nil {
			return err
		}
		if action.Request != nil {
			return nil // valid context action; no retry
		}
		_, err = parseCodeWriterArgs(action.ProposalArgs)
		return err
	}, options.PromptCacheKey)
	if err != nil {
		return schemas.HarnessStageOutput{}, withCollectedUsage(err, collected)
	}
	// Decode the terminal action. A request_context surfaces as
	// output.ContextRequest for the orchestrator's expansion loop; a
	// submit normalizes through the shared materializer below.
	action, err := TryDecodeStageAction(codeWriterToolName, collected)
	if err != nil {
		return schemas.HarnessStageOutput{}, withCollectedUsage(err, collected)
	}
	if action.Request != nil {
		options.report("requesting context expansion: " + action.Request.Reason)
		return schemas.HarnessStageOutput{
			Summary:        "Code Writer requested additional context.",
			Detail:         action.Request.Reason,
			Confidence:     1.0,
			ContextRequest: action.Request,
			Usage:          usageFromCollected(collected),
		}, nil
	}
	output, err := parseCodeWriterOutput(collected)
	if err != nil {
		return schemas.HarnessStageOutput{}, withCollectedUsage(err, collected)
	}
	// Disposition bookkeeping is reconciled separately from core output:
	// malformed claims already could not fail validation, and they never
	// discard the files above.
	claims, claimIssues := parseDispositionClaims(codeWriterToolName, collected)
	memoryReview, reviewNote := reconcileMemoryReview(cwInput.Memory, claims, claimIssues)
	if reviewNote != "" {
		options.report(reviewNote)
	}
	output.MemoryDisposition = claims

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
			return schemas.HarnessStageOutput{}, withCollectedUsage(fmt.Errorf("code writer: WorkDir is required to apply %d file change(s)", len(output.Files)), collected)
		}
		options.report(fmt.Sprintf("applying %d file change(s)", len(output.Files)))
		apply, err := applyFileChanges(ctx, options.WorkDir, output.Files, options.RunTool)
		if err != nil {
			return schemas.HarnessStageOutput{}, withCollectedUsage(fmt.Errorf("code writer: %w", err), collected)
		}
		if len(apply.Applied) != len(output.Files) {
			return schemas.HarnessStageOutput{}, withCollectedUsage(fmt.Errorf("code writer: applied %d of %d file changes", len(apply.Applied), len(output.Files)), collected)
		}
		options.report(fmt.Sprintf("applied %d file change(s)", len(apply.Applied)))
		data["file_apply_result"] = apply
	}

	return schemas.HarnessStageOutput{
		Summary:      output.Intent,
		Detail:       strings.Join(changedPaths, ", "),
		Confidence:   output.Confidence,
		MemoryReview: memoryReview,
		Data:         data,
		Usage:        usageFromCollected(collected),
	}, nil
}

func parseCodeWriterOutput(collected *zeroruntime.CollectedStream) (schemas.CodeWriterOutput, error) {
	tc := findToolCall(collected, codeWriterToolName)
	if tc == nil {
		return schemas.CodeWriterOutput{}, fmt.Errorf("model did not call %s", codeWriterToolName)
	}
	stripped, err := stripDispositionClaims(tc.Arguments)
	if err != nil {
		return schemas.CodeWriterOutput{}, fmt.Errorf("parse %s args: %w", codeWriterToolName, err)
	}
	output, err := parseCodeWriterArgs(stripped)
	if err != nil {
		return schemas.CodeWriterOutput{}, err
	}
	if err := output.Validate(); err != nil {
		return schemas.CodeWriterOutput{}, err
	}
	return output, nil
}

// parseCodeWriterArgs decodes submit_code args in EITHER protocol version:
// the compact/1 proposal form (files carry base_ref/edits/content per the
// C1 rules) or the legacy full/1 full-content form. Compact proposals are
// normalized into canonical full-content FileChanges through the shared
// materializer BEFORE validation, so every downstream consumer (repair
// hashes, test attribution, changed-path extraction) keeps its full-file
// contract. The base-snapshot resolver is the caller's view of what the
// model actually received; a nil resolver makes every compact modify/
// delete fail with unknown base_ref (loud, never guessed).
func parseCodeWriterArgs(raw string) (schemas.CodeWriterOutput, error) {
	var probe struct {
		Files []json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return schemas.CodeWriterOutput{}, fmt.Errorf("parse submit_code args: %w", err)
	}
	var output schemas.CodeWriterOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		return schemas.CodeWriterOutput{}, fmt.Errorf("parse submit_code args: %w", err)
	}
	if !proposalsContainEdits(probe.Files) {
		// Legacy full/1: every file carried full content; decode as-is.
		return output, nil
	}
	proposals, err := decodeProposals(probe.Files)
	if err != nil {
		return schemas.CodeWriterOutput{}, err
	}
	changes, _, err := MaterializeProposals(proposals, currentProposalSnapshot)
	if err != nil {
		return schemas.CodeWriterOutput{}, fmt.Errorf("normalize compact proposals: %w", err)
	}
	output.Files = changes
	return output, nil
}

// proposalsContainEdits reports whether any raw file entry carries the
// compact/1 fields (base_ref or edits). Used to pick the protocol version
// per payload: mixed-version payloads normalize through the proposal path
// and fail there if inconsistent.
func proposalsContainEdits(files []json.RawMessage) bool {
	for _, raw := range files {
		var p struct {
			BaseRef string            `json:"base_ref"`
			Edits   []TextReplacement `json:"edits"`
		}
		if json.Unmarshal(raw, &p) == nil && (p.BaseRef != "" || len(p.Edits) > 0) {
			return true
		}
	}
	return false
}

func decodeProposals(files []json.RawMessage) ([]ProposedFileChange, error) {
	out := make([]ProposedFileChange, 0, len(files))
	for i, raw := range files {
		var p ProposedFileChange
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("files[%d]: parse proposal: %w", i, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func submitCodeToolDefinition(hasMemory bool) zeroruntime.ToolDefinition {
	definition := zeroruntime.ToolDefinition{
		Name:        codeWriterToolName,
		Description: "Submit the complete CodeWriterOutput for the requested implementation.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files":             proposalArraySchema(),
				"language":          map[string]any{"type": "string"},
				"intent":            map[string]any{"type": "string"},
				"dependencies":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"known_limitations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"confidence":        map[string]any{"type": "number"},
			},
			"required": []string{"files", "language", "intent", "confidence"},
		},
	}
	applyMemoryDefinition(definition.Parameters, hasMemory)
	return definition
}

// runToolWithExpectedBase invokes the mutating tool with the caller's
// expected-content digest attached when one exists. The digest rides the
// args under "expected_base"; write_file reads and verifies it inside the
// tool, immediately before mutation (C2): a file mutated between
// preflight and write is caught and nothing is written. Model-supplied
// args cannot forge safety here: a mismatching digest only FAILS a write,
// and a correct digest merely confirms the file the model's proposal was
// composed against is still current.
func runToolWithExpectedBase(ctx context.Context, runTool func(context.Context, string, map[string]any) (ToolResult, error), toolName string, args map[string]any, expectedBase string) (ToolResult, error) {
	if expectedBase != "" && toolName == "write_file" {
		clone := make(map[string]any, len(args)+1)
		for k, v := range args {
			clone[k] = v
		}
		clone["expected_base"] = expectedBase
		args = clone
	}
	return runTool(ctx, toolName, args)
}

func applyFileChanges(ctx context.Context, workDir string, files []schemas.FileChange, runTool func(context.Context, string, map[string]any) (ToolResult, error)) (schemas.FileChangeApplyResult, error) {
	if workDir == "" {
		return schemas.FileChangeApplyResult{}, fmt.Errorf("workDir is required")
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return schemas.FileChangeApplyResult{}, fmt.Errorf("resolve work dir: %w", err)
	}

	// C2 preflight: every change is validated and its target resolved
	// BEFORE the first write starts. A bad later proposal then rejects
	// the whole batch instead of leaving a half-applied set. The
	// expected-base digest per modify/delete target is captured here from
	// the CURRENT bytes; the write_file recheck verifies it again inside
	// the tool after hooks.
	type prepared struct {
		f            schemas.FileChange
		absTarget    string
		relTarget    string
		resolveErr   error
		expectedBase string
		priorBytes   int
	}
	preparedFiles := make([]prepared, 0, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return schemas.FileChangeApplyResult{Workspace: absWorkDir, Applied: []schemas.AppliedFileChange{}}, fmt.Errorf("preflight %s %s: %w", f.ChangeType, f.Path, err)
		}
		if err := f.Validate(); err != nil {
			return schemas.FileChangeApplyResult{Workspace: absWorkDir, Applied: []schemas.AppliedFileChange{}}, fmt.Errorf("preflight invalid change: %w", err)
		}
		p := prepared{f: f}
		p.absTarget, p.relTarget, p.resolveErr = resolveApplyTarget(absWorkDir, f.Path)
		if p.resolveErr == nil && p.relTarget == "." {
			return schemas.FileChangeApplyResult{Workspace: absWorkDir, Applied: []schemas.AppliedFileChange{}}, fmt.Errorf("preflight %s %s: cannot target workspace root", f.ChangeType, f.Path)
		}
		if p.resolveErr == nil && (f.ChangeType == "modify" || f.ChangeType == "delete") {
			if prior, rerr := os.ReadFile(p.absTarget); rerr == nil {
				p.expectedBase = tools.HashContent(prior)
				p.priorBytes = len(prior)
			} else if runTool == nil {
				return schemas.FileChangeApplyResult{Workspace: absWorkDir, Applied: []schemas.AppliedFileChange{}}, fmt.Errorf("preflight %s %s: read prior content: %w", f.ChangeType, f.Path, rerr)
			}
		}
		preparedFiles = append(preparedFiles, p)
	}

	result := schemas.FileChangeApplyResult{Workspace: absWorkDir, Applied: []schemas.AppliedFileChange{}}
	for _, pf := range preparedFiles {
		f := pf.f
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("apply %s %s: %w", f.ChangeType, f.Path, err)
		}

		absTarget, _, resolveErr := pf.absTarget, pf.relTarget, pf.resolveErr

		var bytesRead int
		if resolveErr == nil && (f.ChangeType == "modify" || f.ChangeType == "delete") {
			bytesRead = pf.priorBytes
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
			res, err := runToolWithExpectedBase(ctx, runTool, toolName, args, pf.expectedBase)
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
