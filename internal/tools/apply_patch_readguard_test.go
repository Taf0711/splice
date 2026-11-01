package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyPatchReadBeforeWriteGuard pins the strict read guard for apply_patch.
// A patch to an existing file is refused until the run has read the file. A
// patch to a new file is not held back. With the guard off, the patch applies.
func TestApplyPatchReadBeforeWriteGuard(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "hello.txt")
	writeTestFile(t, target, "hello\nold\n")
	patch := strings.Join([]string{
		"diff --git a/hello.txt b/hello.txt",
		"--- a/hello.txt",
		"+++ b/hello.txt",
		"@@ -1,2 +1,2 @@",
		" hello",
		"-old",
		"+new",
		"",
	}, "\n")
	tool := NewApplyPatchTool(root)
	tracker := NewFileTracker()
	guarded := RunOptions{RequireReadBeforeWrite: true, FileTracker: tracker}

	result := tool.(applyPatchTool).RunWithOptions(context.Background(), map[string]any{"patch": patch}, guarded)
	if result.Status == StatusOK {
		t.Fatal("patch to an unread file applied under the read guard")
	}
	if data, _ := os.ReadFile(target); string(data) != "hello\nold\n" {
		t.Fatalf("file = %q, want the unread patch refused", data)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(target)
	// The tool keys the tracker by the resolved path, which differs from the
	// temp-dir spelling on hosts where the temp root is a symlink.
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	tracker.Record(resolved, content, info)
	result = tool.(applyPatchTool).RunWithOptions(context.Background(), map[string]any{"patch": patch}, guarded)
	if result.Status != StatusOK {
		t.Fatalf("patch after a read = %s: %s", result.Status, result.Output)
	}
}

// TestApplyPatchReadGuardRefusesWithoutTracker pins that the guard fails closed
// when no file tracker is set, the same as write_file and edit_file. No tracker
// means no file was read, so a patch to an existing file is refused.
func TestApplyPatchReadGuardRefusesWithoutTracker(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "hello.txt")
	writeTestFile(t, target, "hello\nold\n")
	patch := strings.Join([]string{
		"diff --git a/hello.txt b/hello.txt",
		"--- a/hello.txt",
		"+++ b/hello.txt",
		"@@ -1,2 +1,2 @@",
		" hello",
		"-old",
		"+new",
		"",
	}, "\n")
	tool := NewApplyPatchTool(root)
	result := tool.(applyPatchTool).RunWithOptions(context.Background(), map[string]any{"patch": patch}, RunOptions{RequireReadBeforeWrite: true})
	if result.Status == StatusOK {
		t.Fatal("patch applied under the read guard with no file tracker")
	}
	if data, _ := os.ReadFile(target); string(data) != "hello\nold\n" {
		t.Fatalf("file = %q, want the patch refused", data)
	}
}
