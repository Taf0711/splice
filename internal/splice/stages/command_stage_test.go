package stages

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestCommandStageUsesSandboxedRecordCommand pins that a command node runs
// through exactly one designated registry tool on the recorded path, and that
// the topology cannot name an arbitrary tool.
func TestCommandStageUsesSandboxedRecordCommand(t *testing.T) {
	stage := CommandStage{Name: "lint", Command: []string{"true", "--strict"}}
	var (
		recordName string
		toolName   string
		gotArgs    map[string]any
	)
	options := StageOptions{
		WorkDir: "/tmp/work",
		RunTool: func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			toolName = name
			return ToolResult{OK: true, Output: "clean"}, nil
		},
		RecordCommand: func(ctx context.Context, name string, args map[string]any, run func(context.Context) (ToolResult, error)) (ToolResult, error) {
			recordName = name
			gotArgs = args
			return run(ctx)
		},
	}
	output, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, options)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if recordName != "splice.command" {
		t.Fatalf("recorded command name = %q, want splice.command", recordName)
	}
	if toolName != commandToolName {
		t.Fatalf("registry tool = %q, want %q", toolName, commandToolName)
	}
	if got := gotArgs["command"]; got != "true --strict" {
		t.Fatalf("command arg = %v, want %q", got, "true --strict")
	}
	if got := output.Data["command_ok"]; got != true {
		t.Fatalf("command_ok = %v, want true", got)
	}
	if got := output.Data["command_output"]; got != "clean" {
		t.Fatalf("command_output = %v, want clean", got)
	}
	if output.Confidence != 1.0 {
		t.Fatalf("confidence = %v, want 1.0", output.Confidence)
	}
	if !stage.Capabilities().ModelFree {
		t.Fatal("command node must be model-free")
	}
}

// TestCommandStageRefusesWithoutSandboxedRunner pins that a command node
// never spawns a process outside the registry path.
func TestCommandStageRefusesWithoutSandboxedRunner(t *testing.T) {
	stage := CommandStage{Name: "lint", Command: []string{"true"}}
	_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{})
	if err == nil {
		t.Fatal("Run returned nil error without a sandboxed runner")
	}
	if !strings.Contains(err.Error(), "no sandboxed shell tool") {
		t.Fatalf("error = %v, want the sandbox refusal naming the node", err)
	}
	if !strings.Contains(err.Error(), "lint") {
		t.Fatalf("error = %v, want the node name", err)
	}
}

// TestCommandStageRejectsEmptyCommand pins the fail-loud empty-command case.
func TestCommandStageRejectsEmptyCommand(t *testing.T) {
	stage := CommandStage{Name: "lint"}
	_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: true}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "command is empty") {
		t.Fatalf("error = %v, want the empty-command refusal", err)
	}
}

// TestCommandStageFailsOnNonZeroExit pins that a command that exits non-zero
// fails the stage. A stage that returns nil here lets a failing lint node pass.
func TestCommandStageFailsOnNonZeroExit(t *testing.T) {
	stage := CommandStage{Name: "lint", Command: []string{"go", "vet", "./..."}}
	_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: false, Output: "vet: unreachable code"}, nil
		},
	})
	if err == nil {
		t.Fatal("Run returned nil error for a failing command")
	}
	for _, want := range []string{"lint", "failed", "vet: unreachable code"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "denied") {
		t.Fatalf("error = %v, a non-zero exit must not read as a denial", err)
	}
}

// TestCommandStageNamesPermissionDenial pins that a permission denial fails the
// stage and says so, so the reason is distinct from a failing command.
func TestCommandStageNamesPermissionDenial(t *testing.T) {
	stage := CommandStage{Name: "lint", Command: []string{"make", "lint"}}
	_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: false, Output: "permission required for bash"}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "denied by permission policy") {
		t.Fatalf("error = %v, want the permission denial naming the node", err)
	}
}

// TestCommandLineKeepsEachElementOneArgument pins D3: a POSIX host gets a shell
// line in which each element is one argument. Shell syntax needs an explicit
// sh -c element. A Windows host keeps the plain join, which cmd.exe reads.
func TestCommandLineKeepsEachElementOneArgument(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"shell syntax as one element", []string{"sh", "-c", "a && b"}, "sh -c 'a && b'"},
		{"element with a space", []string{"echo", "a b"}, "echo 'a b'"},
		{"element with a single quote", []string{"echo", "it's"}, `echo 'it'\''s'`},
		{"empty element", []string{"echo", ""}, "echo ''"},
		{"plain args stay unquoted", []string{"go", "vet", "./...", "--flag=a,b+c@d%e:f"}, "go vet ./... --flag=a,b+c@d%e:f"},
		{"dollar and glob are quoted", []string{"echo", "$HOME*"}, "echo '$HOME*'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandLineFor(tt.argv, "darwin"); got != tt.want {
				t.Fatalf("commandLineFor(%q) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
	if got := commandLineFor([]string{"sh", "-c", "a && b"}, "windows"); got != "sh -c a && b" {
		t.Fatalf("windows join = %q, want the cmd.exe join", got)
	}
}

// TestCommandStageSendsQuotedLine pins that the line handed to the bash tool is
// the quoted form, so the argv reaches the shell as listed.
func TestCommandStageSendsQuotedLine(t *testing.T) {
	stage := CommandStage{Name: "check", Command: []string{"sh", "-c", "a && b"}}
	var got any
	_, err := stage.Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		RunTool: func(_ context.Context, _ string, args map[string]any) (ToolResult, error) {
			got = args["command"]
			return ToolResult{OK: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "sh -c 'a && b'"
	if runtime.GOOS == "windows" {
		want = "sh -c a && b"
	}
	if got != want {
		t.Fatalf("command arg = %v, want %q", got, want)
	}
}
