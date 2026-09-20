package stages

import (
	"context"
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
