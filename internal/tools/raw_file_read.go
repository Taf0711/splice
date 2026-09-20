package tools

// raw_file_read: the guarded raw-bytes file reader (work package B1). It
// exists so the orchestrator's source-reader seam can acquire a file's
// EXACT bytes through the same tool boundary as every other read: path
// scoping, extra-root grants, read exclusions, the FileTracker baseline,
// and redaction all apply here exactly as they do for read_file.
//
// It is deliberately NOT registered in the core toolsets the model sees:
// the model keeps using read_file (numbered display output). This tool is
// for the host-side SourceReader seam only. Adding it to CoreReadOnlyTools
// would put raw bytes in front of the model and double the read surface,
// so the registration lives in the splice registry constructor, not here.

import (
	"context"
	"os"
)

// RawFileReadToolName is the registry name of the raw source reader.
const RawFileReadToolName = "raw_file_read"

type rawFileReadTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

// NewRawFileReadTool builds the raw-bytes reader scoped like read_file.
func NewRawFileReadTool(workspaceRoot string) Tool {
	return NewScopedRawFileReadTool(workspaceRoot, nil)
}

// NewScopedRawFileReadTool builds the raw-bytes reader with an explicit
// PathScope (extra roots included).
func NewScopedRawFileReadTool(workspaceRoot string, scope PathScope) Tool {
	return rawFileReadTool{
		baseTool: baseTool{
			name:        RawFileReadToolName,
			description: "Read a file's exact bytes without line numbering or normalization. Host-side source seam; not model-facing.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path": {Type: "string", Description: "Path of the file to read."},
				},
				Required:             []string{"path"},
				AdditionalProperties: false,
			},
			// PermissionDeny keeps the tool out of every model-facing
			// surface: agent.ToolAdvertised returns false for Deny tools
			// in auto, member-auto, spec-draft, and ask alike, so the
			// schema never enters the provider tool list. Execution is
			// gated by the registry's host-seam check: only a call
			// carrying RunOptions.HostSeam (the orchestrator's
			// source-reader seam) runs it; a model-initiated call is
			// rejected before any path scoping even evaluates. The deny
			// reason documents the boundary for tool listings.
			safety: Safety{
				SideEffect: SideEffectRead,
				Permission: PermissionDeny,
				Reason:     "Host-seam-only source reader: the orchestrator reads exact bytes through the guarded tool boundary; the model uses read_file.",
			},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

// HostSeamOnly implements tools.HostSeamTool: the registry executes this
// tool only through a RunOptions.HostSeam call, which the agent loop never
// issues. The guard set (scoped paths, tracker baseline, redaction) is
// identical on the host path; the model simply never gets the channel.
func (tool rawFileReadTool) HostSeamOnly() bool { return true }

func (tool rawFileReadTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool rawFileReadTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filepath", "filename"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for raw_file_read: " + err.Error())
	}

	absolutePath, relativePath, err := resolveScopedReadPath(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error reading file " + requestedPath + ": " + err.Error())
	}

	content, err := os.ReadFile(absolutePath)
	if err != nil {
		return errorResult("Error reading file " + relativePath + ": " + err.Error())
	}
	// Record the whole-file baseline exactly as read_file does, so the
	// read-before-write contract sees raw reads too.
	info, _ := os.Stat(absolutePath)
	options.FileTracker.Record(absolutePath, content, info)

	// The output is the file's exact bytes. No CRLF normalization, no
	// line numbers, no header. Redaction rides the standard registry
	// boundary (RunWithOptions' caller scrubs output like every tool).
	// An empty file is a successful empty read: a valid snapshot.
	return okResult(string(content))
}
