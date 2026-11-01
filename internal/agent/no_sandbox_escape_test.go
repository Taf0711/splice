package agent

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/tools"
)

// countingBashTool is a shell tool that counts its runs. It reports a sandbox
// denial, so the automatic unsandboxed retry would fire for it.
type countingBashTool struct {
	runs *int
}

func (countingBashTool) Name() string        { return "bash" }
func (countingBashTool) Description() string { return "test shell" }
func (countingBashTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", Properties: map[string]tools.PropertySchema{}, AdditionalProperties: true}
}
func (countingBashTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectShell, Permission: tools.PermissionAllow, AdvertiseInAuto: true}
}
func (tool countingBashTool) Run(context.Context, map[string]any) tools.Result {
	*tool.runs++
	return tools.Result{Status: tools.StatusOK, Output: "ran"}
}

// TestNoSandboxEscapeRefusesRequireEscalated pins that a call asking for
// require_escalated is refused before it runs when the run forbids sandbox
// escape. The same call runs when the flag is off, so interactive runs are
// unchanged.
func TestNoSandboxEscapeRefusesRequireEscalated(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "bash", Arguments: `{"command":"true","sandbox_permissions":"require_escalated"}`}
	runs := 0
	registry := tools.NewRegistry()
	registry.Register(countingBashTool{runs: &runs})

	result, err := executeToolCall(context.Background(), registry, call, PermissionModeAuto, Options{NoSandboxEscape: true})
	if err != nil {
		t.Fatalf("executeToolCall() = %v", err)
	}
	if result.DenialReason != DenialPolicy || runs != 0 {
		t.Fatalf("refused call = denial %q, runs %d; want policy denial and no run", result.DenialReason, runs)
	}

	result, err = executeToolCall(context.Background(), registry, call, PermissionModeAuto, Options{})
	if err != nil {
		t.Fatalf("executeToolCall() = %v", err)
	}
	if result.DenialReason != DenialNone || runs != 1 {
		t.Fatalf("interactive call = denial %q, runs %d; want it to run", result.DenialReason, runs)
	}
}

// TestNoSandboxEscapeSkipsUnsandboxedRetry pins that a sandbox-denied shell
// result is not retried outside the sandbox when the run forbids escape. The
// same result is retried when the flag is off.
func TestNoSandboxEscapeSkipsUnsandboxedRetry(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "bash", Arguments: `{"command":"touch /x"}`}
	args := map[string]any{"command": "touch /x"}
	denied := tools.Result{Status: tools.StatusError, Output: "denied", Meta: map[string]string{tools.SandboxLikelyDeniedMeta: "true"}}
	// A disabled policy is the one case where the unsandboxed retry is allowed.
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: t.TempDir(), Policy: sandbox.Policy{Mode: sandbox.ModeDisabled}})
	runs := 0
	registry := tools.NewRegistry()
	registry.Register(countingBashTool{runs: &runs})

	_, _, retried, _, _, _, _ := maybeRetryUnsandboxedAfterSandboxRestriction(context.Background(), registry, call, countingBashTool{runs: &runs}, args, denied, PermissionModeUnsafe, Options{Sandbox: engine}, nil)
	if !retried {
		t.Fatal("precondition: the unsandboxed retry should fire for a sandbox-denied shell result when escape is allowed")
	}
	runs = 0
	_, _, retried, _, _, _, _ = maybeRetryUnsandboxedAfterSandboxRestriction(context.Background(), registry, call, countingBashTool{runs: &runs}, args, denied, PermissionModeUnsafe, Options{Sandbox: engine, NoSandboxEscape: true}, nil)
	if retried || runs != 0 {
		t.Fatalf("retried = %v, runs = %d; want no unsandboxed retry", retried, runs)
	}
}

// TestAgentNodeAutoAdvertisesPromptToolsInteractiveDoesNot pins that an agent
// node in auto offers its prompt-gated tools to the model, and an interactive
// auto run keeps hiding them.
func TestAgentNodeAutoAdvertisesPromptToolsInteractiveDoesNot(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(promptWriteTool{})
	defs, _ := partitionTools(registry, PermissionModeAuto, Options{NoSandboxEscape: true}, map[string]bool{})
	if len(defs) != 1 {
		t.Fatalf("agent node auto offered %d tools, want the prompt-gated write tool", len(defs))
	}
	defs, _ = partitionTools(registry, PermissionModeAuto, Options{}, map[string]bool{})
	if len(defs) != 0 {
		t.Fatalf("interactive auto offered %d tools, want the prompt-gated tool hidden", len(defs))
	}
}

// promptWriteTool is a prompt-gated write tool. Auto mode hides it from an
// interactive run.
type promptWriteTool struct{}

func (promptWriteTool) Name() string        { return "write_file" }
func (promptWriteTool) Description() string { return "writes" }
func (promptWriteTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", Properties: map[string]tools.PropertySchema{}, AdditionalProperties: true}
}
func (promptWriteTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectWrite, Permission: tools.PermissionPrompt}
}
func (promptWriteTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK}
}
