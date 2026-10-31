//go:build unix

package splice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// The tests in this file make FIFOs with syscall.Mkfifo, which exists only on
// Unix systems.

// TestPassEvidenceSymlinkAndFifoAreNotContent: a symbolic link counts by its
// target text. A FIFO is not a regular file and is skipped, so the pass does
// not block reading it.
func TestPassEvidenceSymlinkAndFifoAreNotContent(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "target.txt", "one\n")
	if err := os.Symlink("target.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	if err := os.Remove(filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan passMeasure, 1)
	go func() { done <- tr.measure(ctx, 1, schemas.ChangeSummary{}) }()
	select {
	case m := <-done:
		if !m.ok {
			t.Fatalf("measure degraded: %s", m.pair.pass.DegradedReason)
		}
		if got := statusOf(m.summary.ChangedFiles); got["link"] != "modified" || len(got) != 1 {
			t.Fatalf("pass 1 = %v, want only the retargeted link", got)
		}
		if m.pair.pass.LinesAdded != 1 || m.pair.pass.LinesRemoved != 1 {
			t.Fatalf("link counts +%d -%d, want the target text +1 -1", m.pair.pass.LinesAdded, m.pair.pass.LinesRemoved)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pass blocked on a FIFO")
	}
}

// TestHeadSummaryDoesNotBlockOnFifo: an untracked FIFO in a git workspace must
// not block the HEAD summary's read of created files.
func TestHeadSummaryDoesNotBlockOnFifo(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	gitInit(t, dir)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	writePassFile(t, dir, "new.go", "package n\n")
	done := make(chan schemas.ChangeSummary, 1)
	go func() { done <- summarizeWorkspaceChanges(context.Background(), dir) }()
	select {
	case head := <-done:
		if head.DegradedReason != "" {
			t.Fatalf("HEAD summary degraded: %s", head.DegradedReason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the HEAD summary blocked on a FIFO")
	}
}

// TestWalkSummaryDoesNotBlockOnFifo: a FIFO in a non-git workspace must not
// block the walk summary. A degraded git read falls back to the same walk.
func TestWalkSummaryDoesNotBlockOnFifo(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "a.txt", "one\n")
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan schemas.ChangeSummary, 1)
	go func() { done <- summarizeWorkspaceChanges(context.Background(), dir) }()
	select {
	case head := <-done:
		if got := statusOf(head.ChangedFiles); len(got) != 1 || got["a.txt"] == "" {
			t.Fatalf("walk summary = %v, want only a.txt", got)
		}
	case <-time.After(10 * time.Second):
		// Unblock the stuck open so the goroutine ends.
		if f, err := os.OpenFile(filepath.Join(dir, "pipe"), os.O_WRONLY, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("the walk summary blocked on a FIFO")
	}
}

// TestOpenRegularRefusesAFifo: a path that is a FIFO when it is opened is
// refused at once. A Lstat before the open cannot be trusted, because the path
// can change between the two calls.
func TestOpenRegularRefusesAFifo(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	done := make(chan error, 1)
	go func() {
		f, _, err := openRegular(root, "pipe")
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errNotRegular) {
			t.Fatalf("openRegular(FIFO) error = %v, want errNotRegular", err)
		}
	case <-time.After(10 * time.Second):
		if f, err := os.OpenFile(filepath.Join(dir, "pipe"), os.O_WRONLY, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("openRegular blocked on a FIFO")
	}
}
