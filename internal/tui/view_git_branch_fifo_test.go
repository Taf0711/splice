//go:build unix

package tui

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestGitBranchDoesNotStallOnFIFOHead pins the bounded read: a FIFO HEAD is
// refused without an open, so the redraw path never blocks on it.
func TestGitBranchDoesNotStallOnFIFOHead(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(workspace, ".git", "HEAD")
	if err := syscall.Mkfifo(head, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- gitBranch(workspace) }()
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("FIFO HEAD gave branch %q, want none", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gitBranch stalled on a FIFO HEAD")
	}
}
