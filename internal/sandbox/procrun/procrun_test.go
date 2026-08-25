package procrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/secrets"
)

// collectAudit installs a sink that records every audit emission and returns
// a function to read them. The previous sink state is restored on cleanup so
// tests never leak receivers into each other.
func collectAudit(t *testing.T) func() []AuditRecord {
	t.Helper()
	var records []AuditRecord
	SetAuditSink(func(record AuditRecord) {
		records = append(records, record)
	})
	t.Cleanup(func() { SetAuditSink(nil) })
	return func() []AuditRecord { return records }
}

// TestPrepareDirectPathMatchesLegacyConstruction is the parity pin for the
// unsandboxed path: the runner must produce exactly what buildBashCommand
// built before the migration — same pass-through plan naming the unavailable
// backend, same scrubbed inherited environment, same argv and directory.
func TestPrepareDirectPathMatchesLegacyConstruction(t *testing.T) {
	spec := sandbox.CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", "echo hi"},
		Dir:  t.TempDir(),
	}

	prepared, err := Prepare(context.Background(), Request{ProfileID: ProfileToolsBash, Spec: spec})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// Legacy construction, replicated verbatim from the pre-migration
	// buildBashCommand body.
	wantPlan := sandbox.CommandPlan{
		Backend: sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "sandbox engine not provided"},
		Wrapped: false,
		Name:    spec.Name,
		Args:    spec.Args,
		Dir:     spec.Dir,
	}
	wantCmd := exec.CommandContext(context.Background(), spec.Name, spec.Args...)
	wantCmd.Dir = spec.Dir
	wantCmd.Env = secrets.ScrubChildEnv(os.Environ())

	if prepared.Plan.Backend.Name != wantPlan.Backend.Name || prepared.Plan.Backend.Message != wantPlan.Backend.Message ||
		prepared.Plan.Wrapped != wantPlan.Wrapped ||
		prepared.Plan.Name != wantPlan.Name || prepared.Plan.Dir != wantPlan.Dir {
		t.Fatalf("plan drift:\n got %+v\nwant %+v", prepared.Plan, wantPlan)
	}
	if len(prepared.Cmd.Args) != len(wantCmd.Args) {
		t.Fatalf("cmd args = %v, want %v", prepared.Cmd.Args, wantCmd.Args)
	}
	for i := range wantCmd.Args {
		if prepared.Cmd.Args[i] != wantCmd.Args[i] {
			t.Fatalf("cmd args[%d] = %q, want %q", i, prepared.Cmd.Args[i], wantCmd.Args[i])
		}
	}
	if prepared.Cmd.Dir != wantCmd.Dir {
		t.Fatalf("cmd dir = %q, want %q", prepared.Cmd.Dir, wantCmd.Dir)
	}
	if len(prepared.Cmd.Env) != len(wantCmd.Env) {
		t.Fatalf("env length = %d, want %d (scrubbed inheritance)", len(prepared.Cmd.Env), len(wantCmd.Env))
	}
	for i := range wantCmd.Env {
		if prepared.Cmd.Env[i] != wantCmd.Env[i] {
			t.Fatalf("env[%d] = %q, want %q", i, prepared.Cmd.Env[i], wantCmd.Env[i])
		}
	}
}

// TestPrepareEnginePathCarriesPlanAndAudit pins the sandboxed path: the
// returned command matches the plan, and the audit record names the profile
// plus the engine decision.
func TestPrepareEnginePathCarriesPlanAndAudit(t *testing.T) {
	read := collectAudit(t)
	root := t.TempDir()
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: root})

	spec := sandbox.CommandSpec{Name: "echo", Args: []string{"hi"}, Dir: root}
	prepared, err := Prepare(context.Background(), Request{ProfileID: ProfileToolsExecCommand, Spec: spec, Engine: engine})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if prepared.Cmd.Dir != prepared.Plan.Dir {
		t.Fatalf("cmd dir %q does not match plan dir %q", prepared.Cmd.Dir, prepared.Plan.Dir)
	}
	if prepared.Audit.Decision.Backend == "" {
		t.Fatal("audit decision must carry the backend name")
	}
	if prepared.Audit.Decision.Wrapped != prepared.Plan.Wrapped {
		t.Fatalf("audit wrapped = %v, want plan value %v", prepared.Audit.Decision.Wrapped, prepared.Plan.Wrapped)
	}
	if prepared.Audit.ProfileID != ProfileToolsExecCommand || prepared.Audit.Name != "echo" {
		t.Fatalf("audit identity = %+v", prepared.Audit)
	}
	records := read()
	if len(records) != 1 || records[0].ProfileID != ProfileToolsExecCommand {
		t.Fatalf("audit stream = %+v, want exactly one exec_command record", records)
	}
}

// TestPrepareRejectedEngineDecisionAuditsTheRefusal pins fail-loud behavior:
// an engine refusal returns an error AND emits an audit record whose decision
// says rejected with the reason, so a refused spawn is never silent.
func TestPrepareRejectedEngineDecisionAuditsTheRefusal(t *testing.T) {
	read := collectAudit(t)
	root := t.TempDir()
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: root})

	_, err := Prepare(context.Background(), Request{
		ProfileID: ProfileToolsBash,
		Spec:      sandbox.CommandSpec{Name: "", Dir: root},
		Engine:    engine,
	})
	if err == nil {
		t.Fatal("empty binary name must be refused")
	}
	records := read()
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want 1", len(records))
	}
	if !records[0].Decision.Rejected || records[0].Decision.Reason == "" {
		t.Fatalf("decision = %+v, want rejected with reason", records[0].Decision)
	}
}

// TestPrepareDirectPathHonorsExplicitEnv pins the explicit-environment seam:
// a non-nil Spec.Env travels verbatim, while a nil Spec.Env keeps the scrubbed
// process inheritance. The hooks dispatcher relies on the verbatim case to run
// hook commands with the scrubbed env plus its own extra entries, and the
// direct path must emit an audit record for the spawn either way.
func TestPrepareDirectPathHonorsExplicitEnv(t *testing.T) {
	read := collectAudit(t)
	spec := sandbox.CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", "exit 0"},
		Env:  []string{"PARITY_A=1", "PARITY_B=2"},
	}
	prepared, err := Prepare(context.Background(), Request{ProfileID: ProfileHooksDispatch, Spec: spec})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(prepared.Cmd.Env) != len(spec.Env) {
		t.Fatalf("env = %v, want verbatim %v", prepared.Cmd.Env, spec.Env)
	}
	for i := range spec.Env {
		if prepared.Cmd.Env[i] != spec.Env[i] {
			t.Fatalf("env[%d] = %q, want %q", i, prepared.Cmd.Env[i], spec.Env[i])
		}
	}
	records := read()
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want 1", len(records))
	}
	if records[0].ProfileID != ProfileHooksDispatch || records[0].Name != spec.Name {
		t.Fatalf("record = %+v, want profile %s for %q", records[0], ProfileHooksDispatch, spec.Name)
	}
	if records[0].Decision.Rejected {
		t.Fatalf("decision = %+v, want a non-rejected spawn", records[0].Decision)
	}
}

// syntheticUnavailableBackend is a deterministic synthetic backend whose
// native support is unavailable, so an engine built with it degrades to a
// direct unwrapped plan.
func syntheticUnavailableBackend() sandbox.Backend {
	return sandbox.Backend{
		Name:            sandbox.BackendUnavailable,
		Available:       false,
		Fallback:        true,
		CommandWrapping: false,
		NativeIsolation: false,
		Message:         "synthetic unavailable backend for tests",
	}
}

func degradedEngine(workspace string) *sandbox.Engine {
	return sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspace,
		Policy: sandbox.Policy{
			Mode:             sandbox.ModeEnforce,
			Network:          sandbox.NetworkDeny,
			EnforceWorkspace: true,
		},
		Backend: syntheticUnavailableBackend(),
	})
}

// TestStageEngineSelectsHostBackend is the T1 regression pin: the production
// stage engine must select the host backend at construction, so a stage plan
// never carries the synthetic "backend was not selected" downgrade. When the
// host backend is available the plan must be wrapped, native, platform-
// confined, network-denying, and workspace-enforcing. When it is unavailable,
// deterministic Prepare must fail closed instead of accepting a direct plan.
func TestStageEngineSelectsHostBackend(t *testing.T) {
	host := sandbox.SelectBackend(sandbox.BackendOptions{})
	workspace := t.TempDir()
	engine := NewStageEngine(workspace)

	prepared, err := Prepare(context.Background(), Request{
		ProfileID:       ProfileSpliceStage,
		AllowedBinaries: StageBinaries,
		Engine:          engine,
		Spec:            sandbox.CommandSpec{Name: "go", Args: []string{"version"}, Dir: workspace},
	})
	if !host.Available {
		if err == nil {
			t.Fatal("deterministic Prepare must fail closed when the host backend is unavailable")
		}
		if !strings.Contains(err.Error(), ProfileSpliceStage) {
			t.Fatalf("fail-closed error must name the profile: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("prepare with selected host backend: %v", err)
	}
	plan := prepared.Plan
	if strings.Contains(plan.DowngradeReason, "native sandbox backend was not selected") {
		t.Fatalf("plan carries the synthetic unselected-backend downgrade: %q", plan.DowngradeReason)
	}
	if !plan.Wrapped {
		t.Fatal("stage plan must be wrapped when the host backend is available")
	}
	if plan.Backend.Name != host.Name {
		t.Fatalf("plan backend = %q, want selected host backend %q", plan.Backend.Name, host.Name)
	}
	if plan.EnforcementLevel != sandbox.EnforcementNative {
		t.Fatalf("plan enforcement = %q, want native", plan.EnforcementLevel)
	}
	if !plan.RequiresPlatformSandbox {
		t.Fatal("stage plan must require platform sandbox confinement")
	}
	if plan.Policy.Network != sandbox.NetworkDeny {
		t.Fatalf("plan network = %q, want deny", plan.Policy.Network)
	}
	if plan.Policy.Mode != sandbox.ModeEnforce || !plan.Policy.EnforceWorkspace {
		t.Fatalf("plan policy = mode %q enforceWorkspace %v, want enforce+workspace", plan.Policy.Mode, plan.Policy.EnforceWorkspace)
	}
}

// TestDeterministicDegradedPlansFailClosed is the T2 regression pin: a
// deterministic profile whose plan requires a platform sandbox but cannot get
// native enforcement must be refused before a command is returned, with one
// rejected audit record carrying backend, wrapped, enforcement, and reason.
func TestDeterministicDegradedPlansFailClosed(t *testing.T) {
	for _, profile := range []string{ProfileSpliceStage, ProfileSpliceDTools} {
		t.Run(profile, func(t *testing.T) {
			read := collectAudit(t)
			workspace := t.TempDir()
			prepared, err := Prepare(context.Background(), Request{
				ProfileID:       profile,
				AllowedBinaries: StageBinaries,
				Engine:          degradedEngine(workspace),
				Spec:            sandbox.CommandSpec{Name: "go", Args: []string{"version"}, Dir: workspace},
			})
			if err == nil {
				t.Fatal("deterministic spawn with an unavailable backend must fail closed")
			}
			if prepared.Cmd != nil {
				t.Fatal("fail-closed Prepare must not return a command")
			}
			if !strings.Contains(err.Error(), profile) {
				t.Fatalf("error must name the profile %q: %v", profile, err)
			}
			if !strings.Contains(err.Error(), string(sandbox.BackendUnavailable)) {
				t.Fatalf("error must name the backend: %v", err)
			}
			records := read()
			if len(records) != 1 {
				t.Fatalf("audit records = %d, want exactly 1", len(records))
			}
			decision := records[0].Decision
			if !decision.Rejected {
				t.Fatalf("decision = %+v, want rejected", decision)
			}
			if decision.Backend != string(sandbox.BackendUnavailable) {
				t.Fatalf("decision backend = %q, want unavailable", decision.Backend)
			}
			if decision.Wrapped {
				t.Fatal("degraded plan must be unwrapped")
			}
			if decision.EnforcementLevel != string(sandbox.EnforcementDegraded) {
				t.Fatalf("decision enforcement = %q, want degraded", decision.EnforcementLevel)
			}
			if decision.Reason == "" {
				t.Fatal("decision must carry a nonempty refusal reason")
			}
		})
	}
}

// TestNilEngineDeterministicFailsClosed is the T3 regression pin: a
// deterministic request with no engine at all must fail before process
// creation, and one rejected audit record must name the deterministic profile.
func TestNilEngineDeterministicFailsClosed(t *testing.T) {
	read := collectAudit(t)
	workspace := t.TempDir()
	prepared, err := Prepare(context.Background(), Request{
		ProfileID:       ProfileSpliceStage,
		AllowedBinaries: StageBinaries,
		Spec:            sandbox.CommandSpec{Name: "true", Dir: workspace},
	})
	if err == nil {
		t.Fatal("deterministic spawn without an engine must fail closed")
	}
	if prepared.Cmd != nil {
		t.Fatal("fail-closed Prepare must not return a command")
	}
	if !strings.Contains(err.Error(), ProfileSpliceStage) {
		t.Fatalf("error must name the deterministic profile: %v", err)
	}
	records := read()
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want exactly 1", len(records))
	}
	if records[0].ProfileID != ProfileSpliceStage {
		t.Fatalf("audit profile = %q, want %q", records[0].ProfileID, ProfileSpliceStage)
	}
	if !records[0].Decision.Rejected || records[0].Decision.Reason == "" {
		t.Fatalf("decision = %+v, want rejected with a nonempty reason", records[0].Decision)
	}
	if !strings.Contains(records[0].Decision.Reason, ProfileSpliceStage) {
		t.Fatalf("refusal reason must name the deterministic profile: %q", records[0].Decision.Reason)
	}
}

// TestNonDeterministicDegradedPathUnchanged is the T4 regression pin: a
// non-deterministic profile keeps the existing degraded behavior when its
// engine cannot provide native confinement, so the fail-closed rule never
// leaks outside the deterministic profiles.
func TestNonDeterministicDegradedPathUnchanged(t *testing.T) {
	read := collectAudit(t)
	workspace := t.TempDir()
	prepared, err := Prepare(context.Background(), Request{
		ProfileID: ProfileToolsBash,
		Engine:    degradedEngine(workspace),
		Spec:      sandbox.CommandSpec{Name: "/bin/sh", Args: []string{"-c", "exit 0"}, Dir: workspace},
	})
	if err != nil {
		t.Fatalf("non-deterministic degraded prepare must stay allowed: %v", err)
	}
	if prepared.Cmd == nil {
		t.Fatal("degraded prepare must still return a command")
	}
	if prepared.Plan.Wrapped {
		t.Fatal("degraded plan must stay unwrapped")
	}
	if prepared.Plan.EnforcementLevel != sandbox.EnforcementDegraded {
		t.Fatalf("plan enforcement = %q, want degraded", prepared.Plan.EnforcementLevel)
	}
	records := read()
	if len(records) != 1 || records[0].Decision.Rejected {
		t.Fatalf("audit = %+v, want exactly one non-rejected record", records)
	}
}

// TestRealHostConfinementSmoke is the T6 smoke test: a real stage binary runs
// through the deterministic Prepare path under the host native backend. A
// write inside the workspace succeeds; a write to a reviewer-created writable
// directory outside the workspace is denied by the OS sandbox itself.
func TestRealHostConfinementSmoke(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	host := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !host.Available || host.Name != sandbox.BackendMacOSSeatbelt {
		t.Skipf("host native backend unavailable: %s", host.Message)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 unavailable: %v", err)
	}

	workspace := t.TempDir()
	engine := NewStageEngine(workspace)

	// The outside target must not live under a baseline-writable temp tree:
	// the seatbelt profile allows writes to /tmp and /var/folders, so a write
	// there would not prove the workspace boundary. Use a directory under
	// $HOME instead. Some environments set $HOME under $TMPDIR; skip there.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory for the outside write target: %v", err)
	}
	outside, err := os.MkdirTemp(home, ".splice-rse1-outside-")
	if err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	resolvedOutside := outside
	if resolved, rerr := filepath.EvalSymlinks(outside); rerr == nil {
		resolvedOutside = resolved
	}
	for _, sub := range []string{"/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp", "/var/folders", "/private/var/folders"} {
		if resolvedOutside == sub || strings.HasPrefix(resolvedOutside, sub+string(os.PathSeparator)) {
			t.Skipf("outside path %s is under a writable temp tree; cannot demonstrate denial", resolvedOutside)
		}
	}

	runStage := func(t *testing.T, script string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		prepared, perr := Prepare(ctx, Request{
			ProfileID:       ProfileSpliceStage,
			AllowedBinaries: StageBinaries,
			Engine:          engine,
			Spec:            sandbox.CommandSpec{Name: "python3", Args: []string{"-c", script}, Dir: workspace},
		})
		if perr != nil {
			t.Fatalf("prepare: %v", perr)
		}
		command, plan := prepared.Cmd, prepared.Plan
		defer plan.Cleanup()
		if !plan.Wrapped || plan.EnforcementLevel != sandbox.EnforcementNative {
			t.Fatalf("plan wrapped=%v enforcement=%s, want wrapped native", plan.Wrapped, plan.EnforcementLevel)
		}
		output, runErr := command.CombinedOutput()
		return string(output), runErr
	}

	t.Run("InsideWorkspaceWriteSucceeds", func(t *testing.T) {
		target := filepath.Join(workspace, "inside.txt")
		script := fmt.Sprintf("open(%q,'w').write('inside-ok')", target)
		output, runErr := runStage(t, script)
		if runErr != nil {
			t.Fatalf("workspace write failed: %v\noutput: %s", runErr, output)
		}
		content, readErr := os.ReadFile(target)
		if readErr != nil || string(content) != "inside-ok" {
			t.Fatalf("workspace file = %q (read err %v), want inside-ok", content, readErr)
		}
	})

	t.Run("OutsideWorkspaceWriteIsDenied", func(t *testing.T) {
		target := filepath.Join(resolvedOutside, "denied.txt")
		script := fmt.Sprintf("open(%q,'w').write('must-not-exist')", target)
		output, runErr := runStage(t, script)
		if runErr == nil {
			t.Fatalf("outside write succeeded, want sandbox denial\noutput: %s", output)
		}
		if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
			t.Fatalf("Lstat(%s) = %v, want not-exist", target, statErr)
		}
	})
}

// TestAlreadySandboxedEnvironmentCannotBypassGuard is the T7 regression pin:
// inherited SPLICE_SANDBOXED and SPLICE_SANDBOX_BACKEND markers flip the
// manager to SandboxPreferenceForbid, which returns an unwrapped disabled
// plan. The positive guard must refuse it, so an inherited marker can never
// make a deterministic spawn acceptable.
func TestAlreadySandboxedEnvironmentCannotBypassGuard(t *testing.T) {
	t.Setenv(sandbox.EnvSandboxed, "1")
	t.Setenv(sandbox.EnvSandboxBackend, "macos-seatbelt")
	read := collectAudit(t)
	workspace := t.TempDir()
	engine := NewStageEngine(workspace)

	prepared, err := Prepare(context.Background(), Request{
		ProfileID:       ProfileSpliceStage,
		AllowedBinaries: StageBinaries,
		Engine:          engine,
		Spec:            sandbox.CommandSpec{Name: "true", Dir: workspace},
	})
	if err == nil {
		t.Fatal("deterministic spawn under inherited sandbox markers must fail closed")
	}
	if prepared.Cmd != nil {
		t.Fatal("fail-closed Prepare must not return a command")
	}
	if !strings.Contains(err.Error(), ProfileSpliceStage) {
		t.Fatalf("error must name the deterministic profile: %v", err)
	}
	records := read()
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want exactly 1", len(records))
	}
	if !records[0].Decision.Rejected || records[0].Decision.Reason == "" {
		t.Fatalf("decision = %+v, want rejected with a nonempty reason", records[0].Decision)
	}
}

// TestDisabledPolicyCannotBypassGuard is the T8 regression pin: a disabled
// policy mode also flips the manager to SandboxPreferenceForbid. The positive
// guard must refuse the resulting unwrapped disabled plan.
func TestDisabledPolicyCannotBypassGuard(t *testing.T) {
	read := collectAudit(t)
	workspace := t.TempDir()
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspace,
		Policy: sandbox.Policy{
			Mode:             sandbox.ModeDisabled,
			Network:          sandbox.NetworkDeny,
			EnforceWorkspace: true,
		},
		Backend: sandbox.SelectBackend(sandbox.BackendOptions{}),
	})

	prepared, err := Prepare(context.Background(), Request{
		ProfileID:       ProfileSpliceDTools,
		AllowedBinaries: StageBinaries,
		Engine:          engine,
		Spec:            sandbox.CommandSpec{Name: "go", Args: []string{"version"}, Dir: workspace},
	})
	if err == nil {
		t.Fatal("deterministic spawn under a disabled policy must fail closed")
	}
	if prepared.Cmd != nil {
		t.Fatal("fail-closed Prepare must not return a command")
	}
	if !strings.Contains(err.Error(), ProfileSpliceDTools) {
		t.Fatalf("error must name the deterministic profile: %v", err)
	}
	records := read()
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want exactly 1", len(records))
	}
	if !records[0].Decision.Rejected || records[0].Decision.Reason == "" {
		t.Fatalf("decision = %+v, want rejected with a nonempty reason", records[0].Decision)
	}
}
