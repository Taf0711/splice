package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGitBranchReadsRepositoryHeads pins the branch label for a plain checkout,
// a worktree whose .git file names a relative gitdir, and a detached HEAD.
func TestGitBranchReadsRepositoryHeads(t *testing.T) {
	plain := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plain, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, filepath.Join(plain, ".git", "HEAD"), "ref: refs/heads/main\n")
	if got := gitBranch(plain); got != "main" {
		t.Fatalf("plain checkout branch = %q, want main", got)
	}

	repo := t.TempDir()
	gitdir := filepath.Join(repo, ".git", "worktrees", "w")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, filepath.Join(gitdir, "HEAD"), "ref: refs/heads/feature/x\n")
	worktree := filepath.Join(repo, "w")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, filepath.Join(worktree, ".git"), "gitdir: ../.git/worktrees/w\n")
	if got := gitBranch(worktree); got != "feature/x" {
		t.Fatalf("worktree branch = %q, want feature/x", got)
	}

	detached := t.TempDir()
	if err := os.MkdirAll(filepath.Join(detached, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, filepath.Join(detached, ".git", "HEAD"), "0123456789abcdef\n")
	if got := gitBranch(detached); got != "0123456" {
		t.Fatalf("detached HEAD = %q, want the short SHA", got)
	}
}

// TestGitBranchRefusesHeadOutsideRepository pins containment: a plain checkout
// whose HEAD is a symlink out of the workspace shows no branch.
func TestGitBranchRefusesHeadOutsideRepository(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "HEAD")
	writeGitTestFile(t, outside, "ref: refs/heads/leaked\n")
	if err := os.Symlink(outside, filepath.Join(workspace, ".git", "HEAD")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := gitBranch(workspace); got != "" {
		t.Fatalf("symlinked HEAD outside the repository gave branch %q, want none", got)
	}
}

func writeGitTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
