package dtools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/tools"
)

type scannerToolSpec struct {
	name    string
	scanner string
	path    string
	new     func(string) tools.Tool
}

func scannerToolSpecs() []scannerToolSpec {
	return []scannerToolSpec{
		{name: "bandit", scanner: "python", path: "main.py", new: NewBanditTool},
		{name: "gosec", scanner: "gosec", path: "main.go", new: NewGosecTool},
		{name: "sarif", scanner: "sarif-scanner", path: "main.go", new: NewSarifTool},
	}
}

func (s scannerToolSpec) validArgs(path string) map[string]any {
	if s.name == "sarif" {
		return map[string]any{"command": s.scanner, "paths": []any{path}}
	}
	return map[string]any{"paths": []any{path}}
}

func writeScannerScript(t *testing.T, dir, name, output string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s' '" + output + "'\nexit 7\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write scanner %s: %v", name, err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod scanner %s: %v", name, err)
	}
}

func writeWorkspaceFile(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
}

// This pins the scanner argument contract, including workspace confinement.
func TestScannerArgumentValidation(t *testing.T) {
	tests := []struct {
		name string
		tool scannerToolSpec
		args map[string]any
		want string
	}{
		{name: "bandit missing paths", tool: scannerToolSpecs()[0], args: map[string]any{}, want: "paths is required"},
		{name: "gosec missing paths", tool: scannerToolSpecs()[1], args: map[string]any{}, want: "paths is required"},
		{name: "sarif missing command", tool: scannerToolSpecs()[2], args: map[string]any{}, want: "command is required"},
		{name: "bandit paths not array", tool: scannerToolSpecs()[0], args: map[string]any{"paths": "main.py"}, want: "paths must be an array of strings"},
		{name: "gosec paths not array", tool: scannerToolSpecs()[1], args: map[string]any{"paths": "main.go"}, want: "paths must be an array of strings"},
		{name: "sarif paths not array", tool: scannerToolSpecs()[2], args: map[string]any{"command": "scanner", "paths": "main.go"}, want: "paths must be an array of strings"},
		{name: "bandit empty paths", tool: scannerToolSpecs()[0], args: map[string]any{"paths": []any{}}, want: "paths must not be empty"},
		{name: "gosec empty paths", tool: scannerToolSpecs()[1], args: map[string]any{"paths": []any{}}, want: "paths must not be empty"},
		{name: "bandit non-string path", tool: scannerToolSpecs()[0], args: map[string]any{"paths": []any{7}}, want: "paths must be strings"},
		{name: "gosec non-string path", tool: scannerToolSpecs()[1], args: map[string]any{"paths": []any{7}}, want: "paths must be strings"},
		{name: "sarif non-string path", tool: scannerToolSpecs()[2], args: map[string]any{"command": "scanner", "paths": []any{7}}, want: "paths must be strings"},
		{name: "sarif command not string", tool: scannerToolSpecs()[2], args: map[string]any{"command": 7}, want: "command must be a string"},
		{name: "sarif args not array", tool: scannerToolSpecs()[2], args: map[string]any{"command": "scanner", "args": "--sarif"}, want: "args must be an array of strings"},
		{name: "sarif non-string arg", tool: scannerToolSpecs()[2], args: map[string]any{"command": "scanner", "args": []any{7}}, want: "args must be strings"},
		{name: "bandit escape", tool: scannerToolSpecs()[0], args: map[string]any{"paths": []any{"../escape"}}, want: "escapes workspace"},
		{name: "gosec escape", tool: scannerToolSpecs()[1], args: map[string]any{"paths": []any{"../escape"}}, want: "escapes workspace"},
		{name: "sarif escape", tool: scannerToolSpecs()[2], args: map[string]any{"command": "scanner", "paths": []any{"../escape"}}, want: "escapes workspace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.tool.new(t.TempDir()).Run(context.Background(), tt.args)
			if result.Status != tools.StatusError || !strings.Contains(result.Output, tt.want) {
				t.Fatalf("result = %+v, want StatusError containing %q", result, tt.want)
			}
		})
	}
}

// This pins the explicit unavailable-scanner error for every dtool.
func TestScannerMissingFromPath(t *testing.T) {
	for _, spec := range scannerToolSpecs() {
		spec := spec
		t.Run(spec.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeWorkspaceFile(t, workspace, spec.path)
			t.Setenv("PATH", t.TempDir())

			result := spec.new(workspace).Run(context.Background(), spec.validArgs(spec.path))
			if result.Status != tools.StatusError || !strings.Contains(result.Output, "is not installed or not available") {
				t.Fatalf("result = %+v, want unavailable-scanner StatusError", result)
			}
		})
	}
}

// This pins that scanner findings keep their output even when the process exits non-zero.
func TestScannerReturnsNonZeroOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script scanner requires a Unix-like platform")
	}

	for _, spec := range scannerToolSpecs() {
		spec := spec
		t.Run(spec.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeWorkspaceFile(t, workspace, spec.path)
			binDir := t.TempDir()
			want := "finding output from " + spec.name
			writeScannerScript(t, binDir, spec.scanner, want)
			t.Setenv("PATH", binDir)

			args := spec.validArgs(spec.path)
			if spec.name == "sarif" {
				args["paths"] = []any{}
			}
			result := spec.new(workspace).Run(context.Background(), args)
			if result.Status != tools.StatusOK {
				t.Fatalf("result status = %s, want %s (output: %q)", result.Status, tools.StatusOK, result.Output)
			}
			if result.Output != want {
				t.Fatalf("result output = %q, want %q", result.Output, want)
			}
		})
	}
}

// This pins cancellation as an error after a scanner process is selected.
func TestScannerContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script scanner requires a Unix-like platform")
	}

	for _, spec := range scannerToolSpecs() {
		spec := spec
		t.Run(spec.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeWorkspaceFile(t, workspace, spec.path)
			binDir := t.TempDir()
			writeScannerScript(t, binDir, spec.scanner, "should not be returned")
			t.Setenv("PATH", binDir)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result := spec.new(workspace).Run(ctx, spec.validArgs(spec.path))
			if result.Status != tools.StatusError || !strings.Contains(result.Output, context.Canceled.Error()) {
				t.Fatalf("result = %+v, want cancelled StatusError", result)
			}
		})
	}
}
