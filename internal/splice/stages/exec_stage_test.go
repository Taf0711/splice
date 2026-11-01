package stages

// Adversarial coverage for the stage process chokepoint: tampered binaries are
// rejected loudly, poisoned-manifest code still runs under a network-deny
// engine plan, repair-loop re-entry keeps executing the identical command
// form, and no direct exec construction may reappear in the migrated files.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
)

// TestPrepareStageCommandRejectsTamperedBinary pins fail-loud behavior: a
// command whose binary is outside the fixed stage allowlist is refused with an
// error naming the offending input, and the audit stream records the refusal.
func TestPrepareStageCommandRejectsTamperedBinary(t *testing.T) {
	var audits []procrun.AuditRecord
	procrun.SetAuditSink(func(record procrun.AuditRecord) {
		audits = append(audits, record)
	})
	t.Cleanup(func() { procrun.SetAuditSink(nil) })

	workDir := t.TempDir()
	_, _, err := PrepareStageCommand(context.Background(), nil, workDir,
		[]string{"definitely-not-a-real-runner-9x7", "--flag"})
	if err == nil {
		t.Fatal("tampered binary must be rejected")
	}
	if !strings.Contains(err.Error(), "definitely-not-a-real-runner-9x7") {
		t.Fatalf("refusal must name the binary, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), procrun.ProfileSpliceStage) {
		t.Fatalf("refusal must name the profile, got %q", err.Error())
	}
	if len(audits) != 1 || !audits[0].Decision.Rejected {
		t.Fatalf("audit = %+v, want exactly one rejected record", audits)
	}
}

// TestStageAllowlistRemainsFirstRefusal is the T5 regression pin: a forbidden
// binary is refused by the allowlist even when the engine is nil, before any
// sandbox fail-closed rule can fire, and exactly one audit record is emitted.
func TestStageAllowlistRemainsFirstRefusal(t *testing.T) {
	var audits []procrun.AuditRecord
	procrun.SetAuditSink(func(record procrun.AuditRecord) {
		audits = append(audits, record)
	})
	t.Cleanup(func() { procrun.SetAuditSink(nil) })

	workDir := t.TempDir()
	_, _, err := PrepareStageCommand(context.Background(), nil, workDir,
		[]string{"not-a-stage-binary-77", "--flag"})
	if err == nil {
		t.Fatal("forbidden binary must be refused")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("first refusal must be the allowlist error, got %q", err.Error())
	}
	if len(audits) != 1 || !audits[0].Decision.Rejected {
		t.Fatalf("audit = %+v, want exactly one rejected record", audits)
	}
	if !strings.Contains(audits[0].Decision.Reason, "allowlist") {
		t.Fatalf("audit reason must be the allowlist refusal: %q", audits[0].Decision.Reason)
	}
}

// TestPoisonedManifestRunsUnderNetworkDenyPlan is the T1.4 fix pin: a manifest
// can make an allowed runner execute arbitrary script content (the poisoning
// premise, proven here by a written marker file), while every such spawn
// carries the network-deny workspace-scoped engine plan (the mitigation).
// It requires a host native sandbox backend; without one the deterministic
// stage profile fails closed and the test skips.
func TestPoisonedManifestRunsUnderNetworkDenyPlan(t *testing.T) {
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 unavailable: %v", err)
	}
	workDir := t.TempDir()
	engine := procrun.NewStageEngine(workDir)

	cmd, plan, err := PrepareStageCommand(context.Background(), engine, workDir,
		[]string{"python3", "-c", "open('POISON_MARKER','w').write('ran')"})
	if err != nil {
		t.Fatalf("allowed runner refused: %v", err)
	}
	defer plan.Cleanup()
	if err := cmd.Run(); err != nil {
		t.Fatalf("poisoned-script execution failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "POISON_MARKER")); err != nil {
		t.Fatalf("marker missing: arbitrary manifest script content did not run: %v", err)
	}

	// The mitigation travels on the plan regardless of platform backend.
	if plan.Policy.Network != sandbox.NetworkDeny {
		t.Fatalf("plan network = %q, want deny", plan.Policy.Network)
	}
	if plan.Policy.Mode != sandbox.ModeEnforce || !plan.Policy.EnforceWorkspace {
		t.Fatalf("plan policy = mode %q enforceWorkspace %v, want enforce+workspace", plan.Policy.Mode, plan.Policy.EnforceWorkspace)
	}
	resolved := workDir
	if r, rerr := filepath.EvalSymlinks(workDir); rerr == nil {
		resolved = r
	}
	if plan.Dir != resolved {
		t.Fatalf("plan dir = %q, want resolved workspace %q", plan.Dir, resolved)
	}
}

// TestRepairReentryUsesIdenticalCommandForm guards the repair interaction:
// test_runner's initial run and its repair-loop re-entry resolve the exact
// same command from the same inputs, so the allowlist admits both forms and
// repair cannot break because of a drifted argv shape.
func TestRepairReentryUsesIdenticalCommandForm(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example.com/repairpin\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	capture := func() []string {
		t.Helper()
		var captured []string
		record := func(_ context.Context, name string, args map[string]any, _ func(context.Context) (ToolResult, error)) (ToolResult, error) {
			if name != "splice.test" {
				t.Fatalf("record name = %q, want splice.test", name)
			}
			raw, ok := args["command"].([]string)
			if !ok {
				t.Fatalf("command arg type = %T, want []string", args["command"])
			}
			captured = append([]string(nil), raw...)
			return ToolResult{OK: true, Output: "{}"}, nil
		}
		options := StageOptions{
			WorkDir:       workDir,
			Sandbox:       procrun.NewStageEngine(workDir),
			RecordCommand: record,
		}
		stage := TestRunner{}
		if _, err := stage.Run(context.Background(), newHarnessInput("run tests"), nil, options); err != nil {
			t.Fatalf("run: %v", err)
		}
		if len(captured) == 0 {
			t.Fatal("re-entry capture got no command")
		}
		return captured
	}

	initial := capture()
	reentry := capture()
	if strings.Join(initial, "\x00") != strings.Join(reentry, "\x00") {
		t.Fatalf("command drift between runs: initial=%v reentry=%v", initial, reentry)
	}
	// Both forms are also the allowlisted discovery default for Go workspaces.
	if initial[0] != "go" {
		t.Fatalf("discovered runner = %q, want go", initial[0])
	}
}

// TestStageFilesNeverConstructExecDirectly is the reintroduction guard: the
// Tier-1.4 files lost their direct exec fallback branches, and this test fails
// if anyone adds one back instead of routing through PrepareStageCommand.
func TestStageFilesNeverConstructExecDirectly(t *testing.T) {
	guarded := []string{
		"commands.go",
		"test_runner.go",
		"quality_python.go",
		"quality_javascript.go",
		"quality_typescript.go",
		"security_bandit.go",
		"security_gosec.go",
		"security_sarif.go",
		"security_trivy.go",
		filepath.Join("..", "run.go"),
		filepath.Join("..", "dtools", "bandit.go"),
		filepath.Join("..", "dtools", "gosec.go"),
		filepath.Join("..", "dtools", "sarif.go"),
	}
	for _, rel := range guarded {
		data, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, banned := range []string{"exec.CommandContext(", "exec.Command("} {
			if strings.Contains(string(data), banned) {
				t.Fatalf("%s constructs %s directly; route spawns through PrepareStageCommand/procrun instead", rel, banned)
			}
		}
	}
}
