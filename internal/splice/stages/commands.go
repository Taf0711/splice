package stages

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// defaultSubprocessTimeout bounds deterministic subprocess execution in the
// verification hot path. A parent deadline or cancellation still propagates
// because the derived context is cancelled when its parent is.
const defaultSubprocessTimeout = 30 * time.Second

// withSubprocessTimeout caps a subprocess at a fixed budget so a single slow
// linter or compiler cannot hang the deterministic pipeline.
func withSubprocessTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, defaultSubprocessTimeout)
}

// relPath returns path relative to workDir, or path unchanged when it is not
// under workDir. Verification findings name files by their workspace-relative
// path rather than an absolute temp path so fingerprints stay portable.
func relPath(path, workDir string) string {
	rel, err := filepath.Rel(workDir, path)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

func runRecordedOutput(ctx context.Context, options StageOptions, name string, args map[string]any, cwd string, command []string) ([]byte, error) {
	run := func(runCtx context.Context) (ToolResult, error) {
		cmd := exec.CommandContext(runCtx, command[0], command[1:]...)
		if cwd != "" {
			cmd.Dir = cwd
		}
		out, err := cmd.Output()
		return ToolResult{OK: err == nil, Output: string(out)}, err
	}
	if options.RecordCommand == nil {
		result, err := run(ctx)
		return []byte(result.Output), err
	}
	result, err := options.RecordCommand(ctx, name, args, run)
	return []byte(result.Output), err
}
