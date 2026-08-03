package stages

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeStaticAnalyzerFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func recordedGitOutputs(outputs map[string]ToolResult) func(context.Context, string, map[string]any, func(context.Context) (ToolResult, error)) (ToolResult, error) {
	return func(ctx context.Context, name string, args map[string]any, run func(context.Context) (ToolResult, error)) (ToolResult, error) {
		command, ok := args["command"].([]string)
		if name != "splice.shell" || !ok {
			return ToolResult{}, errors.New("unexpected recorded command")
		}
		key := command[1]
		if result, ok := outputs[key]; ok {
			return result, nil
		}
		return run(ctx)
	}
}

// Regression: a created file was skipped whenever the same change also modified a tracked file.
func TestGitScopedFilesIncludesCreatedFileWithModifiedTrackedFile(t *testing.T) {
	workDir := t.TempDir()
	tracked := writeStaticAnalyzerFile(t, workDir, "tracked.go")
	created := writeStaticAnalyzerFile(t, workDir, "created.go")

	got, err := gitScopedFiles(context.Background(), workDir, "go", StageOptions{
		RecordCommand: recordedGitOutputs(map[string]ToolResult{
			"diff":     {OK: true, Output: "tracked.go\n"},
			"ls-files": {OK: true, Output: "created.go\n"},
		}),
	})
	if err != nil {
		t.Fatalf("gitScopedFiles: %v", err)
	}
	want := []string{tracked, created}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("created file %q was skipped whenever the same change also modified a tracked file; got %v, want %v", created, got, want)
	}
}

func TestGitScopedFilesDeduplicatesTrackedAndUntrackedFiles(t *testing.T) {
	workDir := t.TempDir()
	path := writeStaticAnalyzerFile(t, workDir, "same.go")

	got, err := gitScopedFiles(context.Background(), workDir, "go", StageOptions{
		RecordCommand: recordedGitOutputs(map[string]ToolResult{
			"diff":     {OK: true, Output: "same.go\n"},
			"ls-files": {OK: true, Output: "same.go\n"},
		}),
	})
	if err != nil {
		t.Fatalf("gitScopedFiles: %v", err)
	}
	if want := []string{path}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want one occurrence %v", got, want)
	}
}

func TestGitScopedFilesKeepsTrackedFilesWhenUntrackedLookupFails(t *testing.T) {
	workDir := t.TempDir()
	tracked := writeStaticAnalyzerFile(t, workDir, "tracked.go")
	created := writeStaticAnalyzerFile(t, workDir, "created.go")

	got, err := gitScopedFiles(context.Background(), workDir, "go", StageOptions{
		RecordCommand: func(ctx context.Context, name string, args map[string]any, run func(context.Context) (ToolResult, error)) (ToolResult, error) {
			command := args["command"].([]string)
			if command[1] == "diff" {
				return ToolResult{OK: true, Output: "tracked.go\n"}, nil
			}
			return ToolResult{Output: "created.go\n"}, errors.New("git ls-files unavailable")
		},
	})
	if err != nil {
		t.Fatalf("gitScopedFiles returned an error after untracked lookup failure: %v", err)
	}
	if want := []string{tracked}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want tracked files %v", got, want)
	}
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("created file setup: %v", err)
	}
}

func TestGitScopedFilesFiltersUntrackedExtensions(t *testing.T) {
	workDir := t.TempDir()
	goPath := writeStaticAnalyzerFile(t, workDir, "created.go")
	_ = writeStaticAnalyzerFile(t, workDir, "created.txt")

	got, err := gitScopedFiles(context.Background(), workDir, "go", StageOptions{
		RecordCommand: recordedGitOutputs(map[string]ToolResult{
			"diff":     {OK: true},
			"ls-files": {OK: true, Output: "created.txt\ncreated.go\n"},
		}),
	})
	if err != nil {
		t.Fatalf("gitScopedFiles: %v", err)
	}
	if want := []string{goPath}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want only matching extension %v", got, want)
	}
}
