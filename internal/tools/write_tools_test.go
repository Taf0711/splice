package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreToolsExposeWriteAndPlanTools(t *testing.T) {
	toolset := CoreTools(t.TempDir())
	byName := make(map[string]Tool, len(toolset))
	for _, tool := range toolset {
		byName[tool.Name()] = tool
	}

	for _, name := range []string{"write_file", "edit_file", "apply_patch"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("expected core tools to include %s", name)
		}
		if tool.Safety().SideEffect != SideEffectWrite {
			t.Fatalf("%s side effect = %s, want write", name, tool.Safety().SideEffect)
		}
		if tool.Safety().Permission != PermissionPrompt {
			t.Fatalf("%s permission = %s, want prompt", name, tool.Safety().Permission)
		}
	}

	planTool, ok := byName["update_plan"]
	if !ok {
		t.Fatalf("expected core tools to include update_plan")
	}
	if planTool.Safety().Permission != PermissionAllow {
		t.Fatalf("update_plan permission = %s, want allow", planTool.Safety().Permission)
	}
}

func TestRegistryBlocksPromptToolsWithoutGrant(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "blocked.txt")
	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))

	result := registry.Run(context.Background(), "write_file", map[string]any{
		"path":    "blocked.txt",
		"content": "nope",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "Permission required for write_file") {
		t.Fatalf("expected permission error, got %q", result.Output)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file to remain absent, stat err=%v", err)
	}
}

func TestRegistryRunsPromptToolsWithGrant(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))

	result := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":    "allowed.txt",
		"content": "hello",
	}, RunOptions{PermissionGranted: true})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "allowed.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("expected written content, got %q", string(content))
	}
}

func TestWriteFileToolCreatesAndProtectsExistingFiles(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)

	created := tool.Run(context.Background(), map[string]any{
		"path":    "nested/file.txt",
		"content": "first",
	})
	if created.Status != StatusOK {
		t.Fatalf("expected create ok, got %s: %s", created.Status, created.Output)
	}
	if !strings.Contains(created.Output, "Created nested/file.txt") {
		t.Fatalf("unexpected create output: %q", created.Output)
	}

	refused := tool.Run(context.Background(), map[string]any{
		"path":    "nested/file.txt",
		"content": "second",
	})
	if refused.Status != StatusError {
		t.Fatalf("expected overwrite refusal, got %s", refused.Status)
	}
	content, err := os.ReadFile(filepath.Join(root, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first" {
		t.Fatalf("expected original content, got %q", string(content))
	}

	overwrote := tool.Run(context.Background(), map[string]any{
		"path":      "nested/file.txt",
		"content":   "second",
		"overwrite": true,
	})
	if overwrote.Status != StatusOK {
		t.Fatalf("expected overwrite ok, got %s: %s", overwrote.Status, overwrote.Output)
	}
	content, err = os.ReadFile(filepath.Join(root, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "second" {
		t.Fatalf("expected overwritten content, got %q", string(content))
	}
}

func TestWriteFileSummaryReportsLineCount(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)
	// Three lines, no trailing newline -> "3 lines" (not a byte count).
	result := tool.Run(context.Background(), map[string]any{
		"path":    "multi.txt",
		"content": "one\ntwo\nthree",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "(3 lines)") {
		t.Fatalf("summary should report a line count, got %q", result.Output)
	}
	if strings.Contains(result.Output, "bytes") {
		t.Errorf("summary should no longer report bytes: %q", result.Output)
	}
}

func TestWriteFileToolAllowsEmptyContent(t *testing.T) {
	root := t.TempDir()

	result := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path":    "empty.txt",
		"content": "",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "empty.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "" {
		t.Fatalf("expected empty file, got %q", string(content))
	}
}

func TestWriteFileToolReportsTypeErrorsForEmptyAllowedStrings(t *testing.T) {
	result := NewWriteFileTool(t.TempDir()).Run(context.Background(), map[string]any{
		"path":    "bad.txt",
		"content": 42,
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "content must be a string") {
		t.Fatalf("expected string type error, got %q", result.Output)
	}
}

func TestWriteFileToolRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	result := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path":    outside,
		"content": "secret",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must stay inside the workspace") {
		t.Fatalf("expected workspace error, got %q", result.Output)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("expected outside file to remain absent, stat err=%v", err)
	}
}

func TestWriteFileToolRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.MkdirAll(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDirectory, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	result := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path":    "link/escape.txt",
		"content": "secret",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must not traverse symlink") {
		t.Fatalf("expected symlink error, got %q", result.Output)
	}
	if _, err := os.Stat(filepath.Join(realDirectory, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected symlink target file to remain absent, stat err=%v", err)
	}
}

func TestEditFileToolReplacesExactStrings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "code.go")
	writeTestFile(t, path, "const a = 1\nconst b = 2\n")

	result := NewEditFileTool(root).Run(context.Background(), map[string]any{
		"path":       "code.go",
		"old_string": "const a = 1",
		"new_string": "const a = 42",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected edit ok, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "const a = 42\nconst b = 2\n" {
		t.Fatalf("unexpected edited content: %q", string(content))
	}
}

func TestEditFileToolReplacesCRLF(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "code.go")
	writeTestFile(t, path, "const a = 1\r\nconst b = 2\r\n")

	result := NewEditFileTool(root).Run(context.Background(), map[string]any{
		"path":       "code.go",
		"old_string": "const a = 1\nconst b = 2",
		"new_string": "const a = 42\nconst b = 24",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected edit ok, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "const a = 42\r\nconst b = 24\r\n" {
		t.Fatalf("unexpected edited content: %q", string(content))
	}
}

func TestEditFileToolEmitsUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "code.go"), "const a = 1\nconst b = 2\n")
	res := NewEditFileTool(root).Run(context.Background(), map[string]any{
		"path": "code.go", "old_string": "const a = 1", "new_string": "const a = 42",
	})
	if res.Status != StatusOK {
		t.Fatalf("edit failed: %s", res.Output)
	}
	// The model-facing Output stays the one-line summary; the red/green diff lives
	// on the card-only Display.Preview, so it costs the model splice tokens.
	if !strings.HasPrefix(res.Output, "Successfully edited") {
		t.Fatalf("summary must be the Output: %q", res.Output)
	}
	if strings.Contains(res.Output, "@@") {
		t.Fatalf("Output must NOT carry the diff (card-only preview): %q", res.Output)
	}
	for _, want := range []string{"@@", "-const a = 1", "+const a = 42"} {
		if !strings.Contains(res.Display.Preview, want) {
			t.Fatalf("edit preview missing diff marker %q: %q", want, res.Display.Preview)
		}
	}
}

func TestWriteFileToolEmitsAdditionsDiff(t *testing.T) {
	root := t.TempDir()
	res := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path": "new.txt", "content": "line one\nline two\n",
	})
	if res.Status != StatusOK {
		t.Fatalf("write failed: %s", res.Output)
	}
	if strings.Contains(res.Output, "@@") {
		t.Fatalf("Output must stay summary-only (the diff is card-only): %q", res.Output)
	}
	for _, want := range []string{"@@", "+line one", "+line two"} {
		if !strings.Contains(res.Display.Preview, want) {
			t.Fatalf("new-file preview missing additions diff %q: %q", want, res.Display.Preview)
		}
	}
	if strings.Contains(res.Display.Preview, "\n-line") {
		t.Fatalf("a fresh-create diff must have no removed lines: %q", res.Display.Preview)
	}
}

func TestWriteFileToolOverwriteEmitsRedGreenDiff(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "f.txt"), "old line\nkeep\n")
	res := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path": "f.txt", "content": "new line\nkeep\n", "overwrite": true,
	})
	if res.Status != StatusOK {
		t.Fatalf("overwrite failed: %s", res.Output)
	}
	if strings.Contains(res.Output, "@@") {
		t.Fatalf("Output must stay summary-only (the diff is card-only): %q", res.Output)
	}
	for _, want := range []string{"-old line", "+new line"} {
		if !strings.Contains(res.Display.Preview, want) {
			t.Fatalf("overwrite preview missing %q: %q", want, res.Display.Preview)
		}
	}
}

func TestEditFileToolAllowsDeletingRegions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	writeTestFile(t, path, "keep\nremove\nkeep\n")

	result := NewEditFileTool(root).Run(context.Background(), map[string]any{
		"path":       "notes.txt",
		"old_string": "remove\n",
		"new_string": "",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected edit ok, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep\nkeep\n" {
		t.Fatalf("unexpected edited content: %q", string(content))
	}
}

func TestEditFileToolRejectsMissingAndAmbiguousMatches(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dup.txt")
	writeTestFile(t, path, "x\nx\n")
	tool := NewEditFileTool(root)

	missing := tool.Run(context.Background(), map[string]any{
		"path":       "dup.txt",
		"old_string": "missing",
		"new_string": "y",
	})
	if missing.Status != StatusError || !strings.Contains(missing.Output, "Could not find") {
		t.Fatalf("expected missing error, got %s: %s", missing.Status, missing.Output)
	}

	ambiguous := tool.Run(context.Background(), map[string]any{
		"path":       "dup.txt",
		"old_string": "x",
		"new_string": "y",
	})
	if ambiguous.Status != StatusError || !strings.Contains(ambiguous.Output, "matches 2 locations") {
		t.Fatalf("expected ambiguity error, got %s: %s", ambiguous.Status, ambiguous.Output)
	}

	all := tool.Run(context.Background(), map[string]any{
		"path":        "dup.txt",
		"old_string":  "x",
		"new_string":  "y",
		"replace_all": true,
	})
	if all.Status != StatusOK {
		t.Fatalf("expected replace_all ok, got %s: %s", all.Status, all.Output)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "y\ny\n" {
		t.Fatalf("expected all replacements, got %q", string(content))
	}
}

func TestApplyPatchToolAppliesUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "hello.txt"), "hello\nold\n")
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

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{
		"patch": patch,
	})

	if result.Status != StatusOK {
		t.Fatalf("expected patch ok, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(content), "\r\n", "\n") != "hello\nnew\n" {
		t.Fatalf("unexpected patched content: %q", string(content))
	}
}

// A hunk-body line that removes content beginning with "-- " appears in the diff
// as "--- ..."; it must NOT be mistaken for a file header (which previously made
// apply_patch reject a valid patch as targeting an outside path).
func TestApplyPatchToolHandlesHunkBodyLookingLikeHeader(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "notes.md"), "keep\n-- /etc/old\n")
	patch := strings.Join([]string{
		"diff --git a/notes.md b/notes.md",
		"--- a/notes.md",
		"+++ b/notes.md",
		"@@ -1,2 +1,1 @@",
		" keep",
		"--- /etc/old",
		"",
	}, "\n")

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{"patch": patch})

	if result.Status != StatusOK {
		t.Fatalf("expected patch ok (hunk body must not be parsed as a header), got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(content), "\r\n", "\n") != "keep\n" {
		t.Fatalf("unexpected patched content: %q", string(content))
	}
}

func TestApplyPatchToolRejectsHunkCountInflationHidingEscapePath(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "safe.txt"), "old\n")
	// A crafted section heading after the closing "@@" injects a "+9,999999"
	// token. If parseHunkCounts scanned the whole line it would treat 999999 as
	// the new-line count, stay stuck in hunk mode, and swallow the second file
	// header below — hiding the ../escape.txt write from path validation.
	patch := strings.Join([]string{
		"diff --git a/safe.txt b/safe.txt",
		"--- a/safe.txt",
		"+++ b/safe.txt",
		"@@ -1,1 +1,1 @@ +9,999999",
		"-old",
		"+new",
		"--- a/../escape.txt",
		"+++ b/../escape.txt",
		"@@ -1,1 +1,1 @@",
		"-secret",
		"+pwned",
		"",
	}, "\n")

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{"patch": patch})

	if result.Status != StatusError {
		t.Fatalf("crafted hunk header must not hide the out-of-workspace path, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "must stay inside the workspace") {
		t.Fatalf("expected workspace-confinement rejection, got %q", result.Output)
	}
}

func TestApplyPatchToolRejectsSymlinkPath(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.MkdirAll(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDirectory, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	patch := strings.Join([]string{
		"diff --git a/link/new.txt b/link/new.txt",
		"new file mode 100644",
		"index 0000000..e965047",
		"--- /dev/null",
		"+++ b/link/new.txt",
		"@@ -0,0 +1 @@",
		"+hello",
		"",
	}, "\n")

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{
		"patch": patch,
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must not traverse symlink") {
		t.Fatalf("expected symlink error, got %q", result.Output)
	}
	if _, err := os.Stat(filepath.Join(realDirectory, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected symlink target file to remain absent, stat err=%v", err)
	}
}

func TestApplyPatchToolRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{
		"cwd": outside,
		"patch": strings.Join([]string{
			"diff --git a/nope.txt b/nope.txt",
			"--- a/nope.txt",
			"+++ b/nope.txt",
			"@@ -0,0 +1 @@",
			"+nope",
			"",
		}, "\n"),
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must stay inside the workspace") {
		t.Fatalf("expected workspace error, got %q", result.Output)
	}
}

// Finding 3: apply_patch with cwd != "." must report WORKSPACE-relative
// ChangedFiles (cwd-prefixed), not cwd-relative paths. Otherwise the session's
// rewind/diff layer keys off the wrong path.
func TestApplyPatchReportsWorkspaceRelativeChangedFilesUnderCwd(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-one\n+two\n"

	res := NewApplyPatchTool(root).Run(context.Background(), map[string]any{"patch": patch, "cwd": "sub/dir"})
	if res.Status != StatusOK {
		if gitApplyUnavailable(res.Output) {
			t.Skipf("git binary unavailable: %s", res.Output)
		}
		t.Fatalf("apply_patch with cwd failed (possible regression): %s", res.Output)
	}
	if len(res.ChangedFiles) != 1 || res.ChangedFiles[0] != "sub/dir/a.txt" {
		t.Fatalf("ChangedFiles = %v, want [sub/dir/a.txt]", res.ChangedFiles)
	}
}

func TestWriteFileReportsChangedFileAndDisplay(t *testing.T) {
	root := t.TempDir()
	res := NewWriteFileTool(root).Run(context.Background(), map[string]any{"path": "notes.txt", "content": "hello"})
	if res.Status != StatusOK {
		t.Fatalf("status=%s output=%s", res.Status, res.Output)
	}
	if len(res.ChangedFiles) != 1 || res.ChangedFiles[0] != "notes.txt" {
		t.Fatalf("ChangedFiles = %v, want [notes.txt]", res.ChangedFiles)
	}
	if res.Display.Kind != "file" {
		t.Errorf("Display.Kind = %q, want file", res.Display.Kind)
	}
	if res.Display.Summary == "" {
		t.Error("expected a non-empty Display.Summary")
	}
}

func TestEditFileReportsChangedFileAndDisplay(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("alpha beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewEditFileTool(root).Run(context.Background(), map[string]any{"path": "f.txt", "old_string": "alpha", "new_string": "gamma"})
	if res.Status != StatusOK {
		t.Fatalf("status=%s output=%s", res.Status, res.Output)
	}
	if len(res.ChangedFiles) != 1 || res.ChangedFiles[0] != "f.txt" {
		t.Fatalf("ChangedFiles = %v, want [f.txt]", res.ChangedFiles)
	}
	if res.Display.Kind != "diff" {
		t.Errorf("Display.Kind = %q, want diff", res.Display.Kind)
	}
}

func TestApplyPatchReportsChangedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-one\n+two\n"
	res := NewApplyPatchTool(root).Run(context.Background(), map[string]any{"patch": patch})
	if res.Status != StatusOK {
		if gitApplyUnavailable(res.Output) {
			t.Skipf("git binary unavailable: %s", res.Output)
		}
		t.Fatalf("apply_patch failed (possible regression): %s", res.Output)
	}
	found := false
	for _, f := range res.ChangedFiles {
		if f == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a.txt in ChangedFiles, got %v", res.ChangedFiles)
	}
	if res.Display.Kind != "diff" {
		t.Errorf("Display.Kind = %q, want diff", res.Display.Kind)
	}
}

func TestWriteFileAcceptsContentAlias(t *testing.T) {
	root := t.TempDir()
	// minimax-style: content under an alias key instead of "content".
	res := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path":     "shop.html",
		"contents": "<html>hi</html>",
	})
	if res.Status != StatusOK {
		t.Fatalf("alias content should write, got %s: %s", res.Status, res.Output)
	}
	got, _ := os.ReadFile(filepath.Join(root, "shop.html"))
	if string(got) != "<html>hi</html>" {
		t.Fatalf("file content = %q", got)
	}
}

func TestDeleteFileToolRegistration(t *testing.T) {
	toolset := CoreTools(t.TempDir())
	byName := make(map[string]Tool, len(toolset))
	for _, tool := range toolset {
		byName[tool.Name()] = tool
	}

	tool, ok := byName["delete_file"]
	if !ok {
		t.Fatal("expected core tools to include delete_file")
	}
	if tool.Safety().SideEffect != SideEffectWrite {
		t.Fatalf("delete_file side effect = %s, want write", tool.Safety().SideEffect)
	}
	if tool.Safety().Permission != PermissionPrompt {
		t.Fatalf("delete_file permission = %s, want prompt", tool.Safety().Permission)
	}
}

func TestDeleteFileToolRemovesRegularFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "remove.txt")
	if err := os.WriteFile(path, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewDeleteFileTool(root).Run(context.Background(), map[string]any{"path": "remove.txt"})
	if res.Status != StatusOK {
		t.Fatalf("expected ok, got %s: %s", res.Status, res.Output)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err=%v", err)
	}
	if len(res.ChangedFiles) != 1 || res.ChangedFiles[0] != "remove.txt" {
		t.Fatalf("ChangedFiles = %v, want [remove.txt]", res.ChangedFiles)
	}
	if !strings.Contains(res.Display.Summary, "Deleted") {
		t.Fatalf("expected Deleted summary, got %q", res.Display.Summary)
	}
	if res.Display.Kind != "diff" {
		t.Errorf("Display.Kind = %q, want diff", res.Display.Kind)
	}
	if !strings.Contains(res.Display.Preview, "-line one") {
		t.Fatalf("expected removed-content diff preview, got %q", res.Display.Preview)
	}
}

func TestDeleteFileToolRejectsMissingFile(t *testing.T) {
	root := t.TempDir()
	res := NewDeleteFileTool(root).Run(context.Background(), map[string]any{"path": "missing.txt"})
	if res.Status != StatusError {
		t.Fatalf("expected error status, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "does not exist") {
		t.Fatalf("expected missing-file error, got %q", res.Output)
	}
}

func TestDeleteFileToolRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	res := NewDeleteFileTool(root).Run(context.Background(), map[string]any{"path": "subdir"})
	if res.Status != StatusError {
		t.Fatalf("expected error status, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "is a directory") {
		t.Fatalf("expected directory refusal, got %q", res.Output)
	}
}

func TestDeleteFileToolRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewDeleteFileTool(root).Run(context.Background(), map[string]any{"path": outside})
	if res.Status != StatusError {
		t.Fatalf("expected error status, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "must stay inside the workspace") {
		t.Fatalf("expected workspace error, got %q", res.Output)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file removed unexpectedly: %v", err)
	}
}

func TestDeleteFileToolRejectsSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "target.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	res := NewDeleteFileTool(root).Run(context.Background(), map[string]any{"path": "link/target.txt"})
	if res.Status != StatusError {
		t.Fatalf("expected error status, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "must not traverse symlink") {
		t.Fatalf("expected symlink error, got %q", res.Output)
	}
	if _, err := os.Stat(filepath.Join(realDir, "target.txt")); err != nil {
		t.Fatalf("symlink target removed unexpectedly: %v", err)
	}
}

func TestDeleteFileToolHonorsCancelledContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := NewDeleteFileTool(root).Run(ctx, map[string]any{"path": "keep.txt"})
	if res.Status != StatusError {
		t.Fatalf("expected error status, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "cancelled") {
		t.Fatalf("expected cancellation error, got %q", res.Output)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file removed despite cancellation: %v", err)
	}
}

func TestDeleteFileToolRequiresPermissionGrant(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "blocked.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.Register(NewDeleteFileTool(root))

	res := registry.Run(context.Background(), "delete_file", map[string]any{"path": "blocked.txt"})
	if res.Status != StatusError {
		t.Fatalf("expected error status, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "Permission required for delete_file") {
		t.Fatalf("expected permission error, got %q", res.Output)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file removed without permission: %v", err)
	}
}

func TestDeleteFileToolRunsWithPermissionGrant(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "allowed.txt")
	if err := os.WriteFile(path, []byte("remove"), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.Register(NewDeleteFileTool(root))

	res := registry.RunWithOptions(context.Background(), "delete_file", map[string]any{"path": "allowed.txt"}, RunOptions{PermissionGranted: true})
	if res.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", res.Status, res.Output)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err=%v", err)
	}
}

// gitApplyUnavailable reports whether an apply_patch failure is due to the git
// binary being absent (an environment condition worth skipping) rather than a
// real regression (which must fail the test). apply_patch shells out to
// `git apply`; a missing binary surfaces as exec's "executable file not found".
func gitApplyUnavailable(output string) bool {
	return strings.Contains(output, "executable file not found")
}
