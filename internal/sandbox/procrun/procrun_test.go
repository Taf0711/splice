package procrun

import (
	"context"
	"os"
	"os/exec"
	"testing"

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
