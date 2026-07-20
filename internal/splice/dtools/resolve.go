package dtools

import (
	"fmt"
	"path/filepath"
	"strings"
)

func resolveWorkspacePath(workspaceRoot, requested string) (string, error) {
	root, err := filepath.Abs(workspaceRoot)
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
