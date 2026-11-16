package dtools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/tools"
)

func resolveWorkspacePath(workspaceRoot, requested string) (string, error) {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	// Resolve the root through symlinks too, not just the target below. Without
	// this, a workspace root reached through a symlink (e.g. macOS /var ->
	// /private/var) compares an unresolved root against a resolved target,
	// so every path rejects as "escapes workspace".
	root, err = filepath.EvalSymlinks(root)
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

	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%s is not a valid workspace path: %w", requested, err)
	}

	realRel, err := filepath.Rel(root, real)
	if err != nil {
		return "", fmt.Errorf("%s escapes workspace", requested)
	}
	if realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) || filepath.IsAbs(realRel) {
		return "", fmt.Errorf("%s escapes workspace", requested)
	}
	return real, nil
}

// spawnEngine returns the sandbox for one scanner spawn. A call that carries an
// engine (the user engine for a model call, or a stage engine for a stage call)
// derives its spawn policy from that engine, so the read and write denials
// carry over. procrun.NewStageEngineFrom keeps only tightenings. A call without
// an engine uses the engine built at construction.
func spawnEngine(fallback *sandbox.Engine, workspaceRoot string, options tools.RunOptions) *sandbox.Engine {
	if options.Sandbox == nil {
		return fallback
	}
	return procrun.NewStageEngineFrom(options.Sandbox, workspaceRoot)
}
