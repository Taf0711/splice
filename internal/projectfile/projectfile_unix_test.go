//go:build unix

package projectfile

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadRefusesFIFO(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "pipe.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	_, err := Read(fifo, root)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("FIFO: want ErrNotRegular, got %v", err)
	}
}
