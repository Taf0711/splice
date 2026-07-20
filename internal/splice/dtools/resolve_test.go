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
