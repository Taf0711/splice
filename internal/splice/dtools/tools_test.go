package dtools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/tools"
)

type scannerToolSpec struct {
	name    string
	scanner string
	path    string
	new     func(string) tools.Tool
	// allowlistedScanner, when set, is the on-PATH executable name the fixture
	// uses instead of scanner: the process chokepoint only spawns allowlisted
	// base names, so a fake scanner must impersonate a real one.
	allowlistedScanner string
}

// executableName is the binary name the fixture writes onto PATH.
func (s scannerToolSpec) executableName() string {
	if s.allowlistedScanner != "" {
		return s.allowlistedScanner
	}
	return s.scanner
}

func scannerToolSpecs() []scannerToolSpec {
	return []scannerToolSpec{
		{name: "bandit", scanner: "python", path: "main.py", new: NewBanditTool},
		{name: "gosec", scanner: "gosec", path: "main.go", new: NewGosecTool},
		{name: "sarif", scanner: "sarif-scanner", path: "main.go", new: NewSarifTool, allowlistedScanner: "npx"},
	}
}

func (s scannerToolSpec) validArgs(path string) map[string]any {
	if s.name == "sarif" {
		return map[string]any{"command": s.executableName(), "paths": []any{path}}
	}
	return map[string]any{"paths": []any{path}}
}

func (s scannerToolSpec) structuredOutput() string {
	switch s.name {
	case "bandit":
		return `{"results":[]}`
	case "gosec":
		return `{"Issues":[]}`
	case "sarif":
		return `{"runs":[]}`
	default:
		return `{}`
	}
}

func writeScannerScript(t *testing.T, dir, name, output string) {
	writeScannerScriptWithOutput(t, dir, name, output, "", 7)
}

func writeScannerScriptWithOutput(t *testing.T, dir, name, stdout, stderr string, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\n"
	if stderr != "" {
		script += "printf '%s' '" + stderr + "' >&2\n"
	}
	script += "printf '%s' '" + stdout + "'\nexit " + strconv.Itoa(exitCode) + "\n"
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

// This pins that scanner findings keep their valid document even when the process exits non-zero.
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
			want := spec.structuredOutput()
			writeScannerScript(t, binDir, spec.executableName(), want)
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

// Regression: combined output put scanner log lines ahead of the JSON, so the
// security stage failed to parse it and the pipeline never reached its test stages.
func TestScannerReturnsOnlyStructuredStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script scanner requires a Unix-like platform")
	}

	for _, spec := range scannerToolSpecs() {
		spec := spec
		t.Run(spec.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeWorkspaceFile(t, workspace, spec.path)
			binDir := t.TempDir()
			want := spec.structuredOutput()
			writeScannerScriptWithOutput(t, binDir, spec.executableName(), want, "[scanner] Including rules: default", 0)
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
				t.Fatalf("result output = %q, want JSON %q without stderr log noise", result.Output, want)
			}
		})
	}
}

func TestScannerFailureSurfacesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script scanner requires a Unix-like platform")
	}

	for _, spec := range scannerToolSpecs() {
		spec := spec
		t.Run(spec.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeWorkspaceFile(t, workspace, spec.path)
			binDir := t.TempDir()
			wantErr := "scanner failed from " + spec.name
			writeScannerScriptWithOutput(t, binDir, spec.executableName(), "", wantErr, 9)
			t.Setenv("PATH", binDir)

			args := spec.validArgs(spec.path)
			if spec.name == "sarif" {
				args["paths"] = []any{}
			}
			result := spec.new(workspace).Run(context.Background(), args)
			if result.Status != tools.StatusError {
				t.Fatalf("result status = %s, want %s (output: %q)", result.Status, tools.StatusError, result.Output)
			}
			if result.Output != wantErr {
				t.Fatalf("result output = %q, want scanner stderr %q", result.Output, wantErr)
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
			writeScannerScript(t, binDir, spec.executableName(), "should not be returned")
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
