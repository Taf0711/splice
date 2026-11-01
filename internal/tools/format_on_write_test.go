package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/flags"
)

// gofmt ships with the Go toolchain, so it is the one formatter guaranteed to
// exist wherever these tests run.
func requireGofmt(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not on PATH")
	}
}

func TestFormatOnWriteDisabledByDefault(t *testing.T) {
	requireGofmt(t)
	flags.SetProcess(nil)
	t.Cleanup(func() { flags.SetProcess(nil) })
	dir := t.TempDir()
	ugly := "package a\n\nfunc  A( ) {   }\n"

	result := NewWriteFileTool(dir).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":    "a.go",
		"content": ugly,
	}, RunOptions{})
	if result.Status != StatusOK {
		t.Fatalf("write failed: %q", result.Output)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != ugly {
		t.Fatalf("formatting must be off by default, got %q", onDisk)
	}
}

func TestFormatOnWriteFormatsAndKeepsTrackerConsistent(t *testing.T) {
	requireGofmt(t)
	flags.SetProcess(flags.Set{flags.ToolsFormatOnWrite: true})
	t.Cleanup(func() { flags.SetProcess(nil) })
	dir := t.TempDir()
	tracker := NewFileTracker()

	write := NewWriteFileTool(dir).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":    "a.go",
		"content": "package a\n\nfunc  A( ) {   }\n",
	}, RunOptions{FileTracker: tracker, TrustedWorkspace: true})
	if write.Status != StatusOK {
		t.Fatalf("write failed: %q", write.Output)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "func A() {") {
		t.Fatalf("expected gofmt-formatted content, got %q", onDisk)
	}

	// The tracker must have been re-baselined to the POST-format content: a
	// follow-up edit must not trip the external-modification conflict guard.
	edit := NewEditFileTool(dir).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":       "a.go",
		"old_string": "func A() {",
		"new_string": "func B() {",
	}, RunOptions{FileTracker: tracker, TrustedWorkspace: true})
	if edit.Status != StatusOK {
		t.Fatalf("follow-up edit must not conflict after formatting: %q", edit.Output)
	}
}

func TestFormatOnWriteSkipsUnknownExtensions(t *testing.T) {
	flags.SetProcess(flags.Set{flags.ToolsFormatOnWrite: true})
	t.Cleanup(func() { flags.SetProcess(nil) })
	content, notice := maybeFormatWrittenFile(context.Background(), filepath.Join(t.TempDir(), "notes.xyz"), "raw   text", true)
	if content != "raw   text" || notice != "" {
		t.Fatalf("unknown extension must pass through: %q", content)
	}
}

// TestFormatOnWriteSkippedInUntrustedWorkspace pins the trust gate: a
// formatter runs outside the sandbox and reads project config, so an
// untrusted workspace never runs it, and the tool result says so.
func TestFormatOnWriteSkippedInUntrustedWorkspace(t *testing.T) {
	requireGofmt(t)
	flags.SetProcess(flags.Set{flags.ToolsFormatOnWrite: true})
	t.Cleanup(func() { flags.SetProcess(nil) })
	dir := t.TempDir()
	ugly := "package a\n\nfunc  A( ) {   }\n"

	write := NewWriteFileTool(dir).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":    "a.go",
		"content": ugly,
	}, RunOptions{})
	if write.Status != StatusOK {
		t.Fatalf("write failed: %q", write.Output)
	}
	edit := NewEditFileTool(dir).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":       "a.go",
		"old_string": "func  A( )",
		"new_string": "func  B( )",
	}, RunOptions{})
	if edit.Status != StatusOK {
		t.Fatalf("edit failed: %q", edit.Output)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != strings.Replace(ugly, "func  A( )", "func  B( )", 1) {
		t.Fatalf("untrusted workspace ran the formatter: %q", onDisk)
	}
	for name, output := range map[string]string{"write_file": write.Output, "edit_file": edit.Output} {
		if !strings.Contains(output, "Format-on-write skipped: the workspace is not trusted.") {
			t.Fatalf("%s output = %q, want the untrusted notice", name, output)
		}
	}
}
