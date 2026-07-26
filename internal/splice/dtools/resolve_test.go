package dtools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspacePathRejectsSymlinkOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()

	outsideFile := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(workspace, "link")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatal(err)
	}

	_, err := resolveWorkspacePath(workspace, "link")
	if err == nil {
		t.Fatal("resolveWorkspacePath() error = nil, want rejection of symlink outside workspace")
	}
}

// symlinkedWorkspace builds a real directory plus a symlink pointing at it, and
// returns the symlink path as the "root" a caller would pass in. This does not
// rely on the platform temp dir happening to be symlinked (t.TempDir() is not
// symlinked on Linux CI), so the regression reproduces on every platform.
func symlinkedWorkspace(t *testing.T) string {
	t.Helper()
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestResolveWorkspacePathAcceptsFileThroughSymlinkedRoot(t *testing.T) {
	root := symlinkedWorkspace(t)

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveWorkspacePath(root, "main.go")
	if err != nil {
		t.Fatalf("resolveWorkspacePath() error = %v, want acceptance of an in-root file reached through a symlinked root", err)
	}
	want := filepath.Join(realRoot, "main.go")
	if got != want {
		t.Fatalf("resolveWorkspacePath() = %q, want %q", got, want)
	}
}

func TestResolveWorkspacePathRejectsSymlinkOutsideSymlinkedRoot(t *testing.T) {
	root := symlinkedWorkspace(t)
	outside := t.TempDir()

	outsideFile := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(realRoot, "link")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatal(err)
	}

	_, err = resolveWorkspacePath(root, "link")
	if err == nil {
		t.Fatal("resolveWorkspacePath() error = nil, want rejection of a symlink escaping a symlinked root")
	}
}
