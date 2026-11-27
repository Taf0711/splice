package hooks

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
)

func beforeToolConfig(hooks ...Definition) Config {
	return Config{Enabled: true, Hooks: hooks}
}

func TestDispatchRunsMatchingHooksAndRecordsAudit(t *testing.T) {
	var calls []string
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		calls = append(calls, command)
		return commandResult{ExitCode: 0, Stdout: "ok"}
	}
	audit, err := NewAuditStore(AuditStoreOptions{AuditPath: filepath.Join(t.TempDir(), "audit.jsonl")})
	if err != nil {
		t.Fatalf("NewAuditStore: %v", err)
	}
	config := beforeToolConfig(
		Definition{ID: "h1", Event: EventBeforeTool, Matcher: "bash", Command: "guard", Enabled: true},
		Definition{ID: "h2", Event: EventBeforeTool, Command: "log", Enabled: true}, // no matcher = always
		Definition{ID: "h3", Event: EventBeforeTool, Matcher: "read_file", Command: "skip", Enabled: true},
	)
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, Audit: audit, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash", ToolCallID: "call_1"})
	if outcome.Blocked {
		t.Fatalf("unexpected block: %#v", outcome)
	}
	if outcome.Ran != 2 {
		t.Fatalf("Ran = %d, want 2 (h1 matcher + h2 unmatched), calls=%v", outcome.Ran, calls)
	}
	events, err := audit.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	started, completed := 0, 0
	for _, event := range events {
		switch event.Type {
		case "hook_execution_started":
			started++
		case "hook_execution_completed":
			completed++
			if event.Status != AuditCompleted {
				t.Fatalf("status = %q, want completed", event.Status)
			}
		}
	}
	if started != 2 || completed != 2 {
		t.Fatalf("audit events: started=%d completed=%d, want 2/2", started, completed)
	}
}

func TestDispatchBeforeToolBlocksOnNonZeroExitAndStops(t *testing.T) {
	ran := 0
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		ran++
		if command == "deny" {
			return commandResult{ExitCode: 2, Stderr: "policy violation"}
		}
		return commandResult{ExitCode: 0}
	}
	config := beforeToolConfig(
		Definition{ID: "deny", Event: EventBeforeTool, Command: "deny", Enabled: true},
		Definition{ID: "after-deny", Event: EventBeforeTool, Command: "second", Enabled: true},
	)
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"})
	if !outcome.Blocked || outcome.BlockedBy != "deny" {
		t.Fatalf("outcome = %#v, want blocked by deny", outcome)
	}
	if outcome.Reason != "policy violation" {
		t.Fatalf("reason = %q, want hook stderr", outcome.Reason)
	}
	if ran != 1 {
		t.Fatalf("ran %d hooks, want 1 (must stop after the first veto)", ran)
	}
}

func TestDispatchNonBlockingEventDoesNotVetoOnNonZero(t *testing.T) {
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		return commandResult{ExitCode: 1, Stderr: "noisy"}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "notify", Event: EventAfterTool, Command: "notify", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "bash"})
	if outcome.Blocked {
		t.Fatalf("afterTool must not block: %#v", outcome)
	}
	if outcome.Ran != 1 {
		t.Fatalf("Ran = %d, want 1", outcome.Ran)
	}
}

func TestDispatchCollectsHookOutputMessages(t *testing.T) {
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		switch command {
		case "fmt":
			return commandResult{ExitCode: 0, Stdout: "  reformatted main.go  "}
		case "vet":
			return commandResult{ExitCode: 1, Stderr: "vet: suspicious construct"} // stdout empty → stderr surfaces
		case "quiet":
			return commandResult{ExitCode: 0} // no output → omitted
		}
		return commandResult{}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "fmt", Event: EventAfterTool, Command: "fmt", Enabled: true},
		{ID: "vet", Event: EventAfterTool, Command: "vet", Enabled: true},
		{ID: "quiet", Event: EventAfterTool, Command: "quiet", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "write_file"})
	if outcome.Blocked {
		t.Fatalf("afterTool must not block: %#v", outcome)
	}
	if outcome.Ran != 3 {
		t.Fatalf("Ran = %d, want 3", outcome.Ran)
	}
	want := []string{"reformatted main.go", "vet: suspicious construct"}
	if strings.Join(outcome.Messages, "|") != strings.Join(want, "|") {
		t.Fatalf("Messages = %#v, want trimmed stdout then stderr-fallback, quiet omitted: %#v", outcome.Messages, want)
	}
}

func TestDispatchSkipsWhenDisabledOrUnmatched(t *testing.T) {
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		t.Fatal("runner must not be called")
		return commandResult{}
	}
	disabled := Config{Enabled: false, Hooks: []Definition{{ID: "h", Event: EventBeforeTool, Command: "x", Enabled: true}}}
	if outcome := NewDispatcher(DispatcherOptions{Config: disabled, run: runner}).Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"}); outcome.Ran != 0 {
		t.Fatalf("disabled config ran hooks: %#v", outcome)
	}
	unmatched := beforeToolConfig(Definition{ID: "h", Event: EventBeforeTool, Matcher: "read_file", Command: "x", Enabled: true})
	if outcome := NewDispatcher(DispatcherOptions{Config: unmatched, run: runner}).Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"}); outcome.Ran != 0 {
		t.Fatalf("unmatched matcher ran hooks: %#v", outcome)
	}
}

func TestDispatchDeliversJSONPayloadOnStdin(t *testing.T) {
	var gotStdin string
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		gotStdin = string(stdin)
		return commandResult{ExitCode: 0}
	}
	config := beforeToolConfig(Definition{ID: "h", Event: EventBeforeTool, Command: "x", Enabled: true})
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	dispatcher.Dispatch(context.Background(), DispatchInput{
		Event:    EventBeforeTool,
		ToolName: "bash",
		Payload:  map[string]any{"tool": "bash", "args": map[string]any{"command": "ls"}},
	})
	if !strings.Contains(gotStdin, `"tool":"bash"`) || !strings.Contains(gotStdin, `"command":"ls"`) {
		t.Fatalf("stdin payload = %q, want serialized tool call", gotStdin)
	}
}

func TestExecCommandRunnerCapturesExitAndStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	// Echoes stdin to stderr and exits non-splice so we exercise both paths.
	result := execCommandRunner(context.Background(), "/bin/sh", []string{"-c", "cat 1>&2; exit 4"}, []byte("payload-123"), t.TempDir(), nil)
	if result.Err != nil {
		t.Fatalf("unexpected launch error: %v", result.Err)
	}
	if result.ExitCode != 4 {
		t.Fatalf("exit code = %d, want 4", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "payload-123") {
		t.Fatalf("stderr = %q, want stdin echoed", result.Stderr)
	}
}

// legacyDirectExecRunner replicates the pre-migration execCommandRunner body
// (direct exec.CommandContext, explicit env assignment) so the parity table
// compares migrated outcomes against the real old behavior, not a model of it.
func legacyDirectExecRunner(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = cwd
	cmd.Env = env
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := commandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result
	}
	// Could not launch (binary missing, timeout, etc.).
	result.ExitCode = -1
	result.Err = err
	return result
}

// TestExecCommandRunnerParityWithDirectExec pins the procrun migration: for
// every outcome class the migrated runner must match the legacy direct-exec
// runner exactly: same stdout/stderr capture, same exit-code reporting // Err reserved for launch failures, and identical argv/dir/env/stdin handling.
func TestExecCommandRunnerParityWithDirectExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	cases := []struct {
		name  string
		run   string
		stdin []byte
		env   []string
	}{
		{name: "clean exit", run: "exit 0"},
		{name: "nonzero exit is not a launch failure", run: "echo out-4; echo err-4 1>&2; exit 4"},
		{name: "stdin piping", run: "cat", stdin: []byte("payload-123")},
		{name: "env passthrough", run: `printf %s "$PARITY_VAR"`, env: []string{"PARITY_VAR=parity-ok"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			args := []string{"-c", testCase.run}
			got := execCommandRunner(context.Background(), "/bin/sh", args, testCase.stdin, dir, testCase.env)
			want := legacyDirectExecRunner(context.Background(), "/bin/sh", args, testCase.stdin, dir, testCase.env)
			if got.ExitCode != want.ExitCode || got.Stdout != want.Stdout || got.Stderr != want.Stderr ||
				(got.Err == nil) != (want.Err == nil) {
				t.Fatalf("parity drift:\n got  %+v\n want %+v", got, want)
			}
		})
	}
	// The launch-failure class must agree too: both runners report a missing
	// binary as Err with ExitCode -1, never as a non-splice exit.
	dir := t.TempDir()
	got := execCommandRunner(context.Background(), "definitely-not-a-real-binary-zzz", nil, nil, dir, nil)
	want := legacyDirectExecRunner(context.Background(), "definitely-not-a-real-binary-zzz", nil, nil, dir, nil)
	if got.ExitCode != want.ExitCode || got.Err == nil || got.Stdout != want.Stdout || got.Stderr != want.Stderr {
		t.Fatalf("launch-failure drift:\n got  %+v\n want %+v", got, want)
	}
}

// TestExecCommandRunnerEmitsProcrunAuditRecord pins the pairing contract: every
// hook spawn emits one audit record under the hooks.dispatch profile carrying
// the binary name, argv, and directory.
func TestExecCommandRunnerEmitsProcrunAuditRecord(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	var records []procrun.AuditRecord
	previous := procrun.SetAuditSink(func(record procrun.AuditRecord) {
		records = append(records, record)
	})
	t.Cleanup(func() { procrun.SetAuditSink(previous) })

	dir := t.TempDir()
	result := execCommandRunner(context.Background(), "/bin/sh", []string{"-c", "exit 0"}, nil, dir, os.Environ())
	if result.Err != nil || result.ExitCode != 0 {
		t.Fatalf("result = %+v, want a clean run", result)
	}
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want 1 per spawn", len(records))
	}
	record := records[0]
	if record.ProfileID != procrun.ProfileHooksDispatch {
		t.Fatalf("profile = %q, want %q", record.ProfileID, procrun.ProfileHooksDispatch)
	}
	if record.Name != "/bin/sh" || len(record.Args) != 2 || record.Args[0] != "-c" || record.Dir != dir {
		t.Fatalf("record identity = %+v, want the spawned argv and dir", record)
	}
	if record.Decision.Rejected {
		t.Fatalf("decision = %+v, want a non-rejected spawn", record.Decision)
	}
}

func TestExecCommandRunnerReportsLaunchFailureWithoutBlocking(t *testing.T) {
	result := execCommandRunner(context.Background(), "definitely-not-a-real-binary-zzz", nil, nil, t.TempDir(), nil)
	if result.Err == nil {
		t.Fatal("expected launch error for a missing binary")
	}
	// A launch failure is an error, never a block, even for beforeTool.
	if status, blocked := classifyResult(EventBeforeTool, result); blocked || status != AuditError {
		t.Fatalf("classify = (%q, %v), want (error, false) for a launch failure", status, blocked)
	}
}

func TestClassifyResultTimedOutFailsClosedForBeforeTool(t *testing.T) {
	// Unlike a launch failure, a hook that STARTED but was killed by its deadline
	// gave no verdict — a beforeTool policy hook must fail CLOSED (veto), or a hung
	// hook would silently wave the tool through.
	timedOut := commandResult{TimedOut: true, Err: context.DeadlineExceeded}
	if status, blocked := classifyResult(EventBeforeTool, timedOut); !blocked || status != AuditBlocked {
		t.Fatalf("beforeTool timeout classify = (%q, %v), want (blocked, true)", status, blocked)
	}
	// Observational events still never veto, even on timeout.
	if status, blocked := classifyResult(EventAfterTool, timedOut); blocked || status != AuditError {
		t.Fatalf("afterTool timeout classify = (%q, %v), want (error, false)", status, blocked)
	}
	if reason := blockReason(timedOut); !strings.Contains(reason, "timed out") {
		t.Fatalf("blockReason = %q, want a timeout message", reason)
	}
}

func TestDispatchBeforeToolFailsClosedWhenHookTimesOut(t *testing.T) {
	// End-to-end: a beforeTool hook that hangs past the dispatcher timeout vetoes
	// the tool. The injected runner blocks until its context is cancelled.
	runner := func(ctx context.Context, _ string, _ []string, _ []byte, _ string, _ []string) commandResult {
		<-ctx.Done()
		return commandResult{Err: ctx.Err()}
	}
	config := beforeToolConfig(Definition{ID: "slow-guard", Event: EventBeforeTool, Command: "hang", Enabled: true})
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner, Timeout: 20 * time.Millisecond})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"})
	if !outcome.Blocked {
		t.Fatal("a timed-out beforeTool hook must fail closed (block the tool)")
	}
	if outcome.BlockedBy != "slow-guard" {
		t.Fatalf("BlockedBy = %q, want slow-guard", outcome.BlockedBy)
	}
	if !strings.Contains(outcome.Reason, "timed out") {
		t.Fatalf("Reason = %q, want a timeout reason", outcome.Reason)
	}
}

// hookSandboxFixture returns a resolved workspace, the default-policy agent
// engine, the stage engine, and a target outside the workspace and every
// temp root (under $HOME). It skips without a native sandbox backend.
func hookSandboxFixture(t *testing.T) (string, *sandbox.Engine, *sandbox.Engine, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("user home unavailable: %v", err)
	}
	outsideDir, err := os.MkdirTemp(home, ".splice-hook-test-")
	if err != nil {
		t.Skipf("home dir not writable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	policy := sandbox.DefaultPolicy()
	policy.TempRoot = t.TempDir()
	agentEngine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workspace, Policy: policy, Backend: backend})
	return workspace, agentEngine, procrun.NewStageEngine(workspace), filepath.Join(outsideDir, "pwned.txt")
}

func writerHook(id string, event Event, target string) Definition {
	return Definition{ID: id, Event: event, Command: "/bin/sh", Args: []string{"-c", `echo pwned > "$1"`, "hook", target}, Enabled: true}
}

func lastCompleted(t *testing.T, audit *AuditStore, hookID string) AuditEvent {
	t.Helper()
	events, err := audit.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == "hook_execution_completed" && events[index].HookID == hookID {
			return events[index]
		}
	}
	t.Fatalf("no completed record for %s in %+v", hookID, events)
	return AuditEvent{}
}

// TestHookPlacementBySourceAndMode pins where each hook runs, in
// the interactive path (DispatchInput.Sandbox is the agent engine) and in a
// pipeline run (ForPipeline with the stage engine). A sandbox denial fails
// loudly with the hook id and the host opt-in key, and the audit record says
// where the hook ran.
func TestHookPlacementBySourceAndMode(t *testing.T) {
	workspace, agentEngine, stageEngine, outside := hookSandboxFixture(t)
	cases := []struct {
		name      string
		hook      Definition
		pipeline  bool
		wantWrite bool
		execution string
		wantKey   string
	}{
		{name: "plugin hook interactive is sandboxed", hook: Definition{PluginID: "acme"}, execution: ExecutionSandboxed, wantKey: "plugins.acme.hostExecution"},
		{name: "plugin hook pipeline is sandboxed", hook: Definition{PluginID: "acme"}, pipeline: true, execution: ExecutionSandboxed, wantKey: "plugins.acme.hostExecution"},
		{name: "plugin hook with user opt-in runs on host", hook: Definition{PluginID: "acme", RunOnHost: true}, pipeline: true, wantWrite: true, execution: ExecutionHost},
		{name: "user hook interactive runs on host", hook: Definition{}, wantWrite: true, execution: ExecutionHost},
		{name: "user hook pipeline is sandboxed", hook: Definition{}, pipeline: true, execution: ExecutionSandboxed, wantKey: `"runOnHost": true`},
		{name: "user hook pipeline with runOnHost runs on host", hook: Definition{RunOnHost: true}, pipeline: true, wantWrite: true, execution: ExecutionHost},
	}
	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			target := outside + strconv.Itoa(index)
			hook := writerHook("probe-"+strconv.Itoa(index), EventBeforeTool, target)
			hook.PluginID, hook.RunOnHost = testCase.hook.PluginID, testCase.hook.RunOnHost
			audit, err := NewAuditStore(AuditStoreOptions{AuditPath: filepath.Join(t.TempDir(), "audit.jsonl")})
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := NewDispatcher(DispatcherOptions{Config: beforeToolConfig(hook), Audit: audit, Cwd: workspace})
			if testCase.pipeline {
				dispatcher = dispatcher.ForPipeline(stageEngine, workspace)
			}
			outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash", Sandbox: agentEngine})

			_, statErr := os.Stat(target)
			if testCase.wantWrite {
				if statErr != nil || outcome.Blocked {
					t.Fatalf("want the host write: stat=%v outcome=%+v", statErr, outcome)
				}
			} else {
				if !os.IsNotExist(statErr) {
					t.Fatalf("hook wrote outside the workspace (stat err=%v)", statErr)
				}
				if !outcome.Blocked || !strings.Contains(outcome.Reason, hook.ID) || !strings.Contains(outcome.Reason, testCase.wantKey) {
					t.Fatalf("outcome = %+v, want a loud block naming %s and %s", outcome, hook.ID, testCase.wantKey)
				}
			}
			record := lastCompleted(t, audit, hook.ID)
			if record.Execution != testCase.execution || record.PluginID != testCase.hook.PluginID {
				t.Fatalf("audit execution=%q plugin=%q, want %q %q", record.Execution, record.PluginID, testCase.execution, testCase.hook.PluginID)
			}
		})
	}
}

// TestPipelineFormatterHookWritesInsideWorkspace: a formatter-style afterTool
// hook that writes inside the workspace still works under the stage engine.
func TestPipelineFormatterHookWritesInsideWorkspace(t *testing.T) {
	workspace, _, stageEngine, _ := hookSandboxFixture(t)
	audit, err := NewAuditStore(AuditStoreOptions{AuditPath: filepath.Join(t.TempDir(), "audit.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	hook := Definition{ID: "fmt", Event: EventAfterTool, Command: "/bin/sh", Args: []string{"-c", "echo formatted > formatted.txt"}, Enabled: true}
	dispatcher := NewDispatcher(DispatcherOptions{Config: beforeToolConfig(hook), Audit: audit, Cwd: t.TempDir()}).ForPipeline(stageEngine, workspace)
	dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "write_file"})
	if data, err := os.ReadFile(filepath.Join(workspace, "formatted.txt")); err != nil || strings.TrimSpace(string(data)) != "formatted" {
		t.Fatalf("formatter output = %q, %v; want the hook to write inside the run workspace", data, err)
	}
	if record := lastCompleted(t, audit, "fmt"); record.Status != AuditCompleted || record.Execution != ExecutionSandboxed {
		t.Fatalf("audit = %+v, want a completed sandboxed run", record)
	}
}

// TestSandboxedHookRefusesWithoutEnforcingEngine: a hook that must run
// sandboxed, with no enforcing engine, never starts. It never falls back to
// the host, and a beforeTool hook fails closed.
func TestSandboxedHookRefusesWithoutEnforcingEngine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	var spawns int
	previous := procrun.SetAuditSink(func(procrun.AuditRecord) { spawns++ })
	t.Cleanup(func() { procrun.SetAuditSink(previous) })
	disabled := sandbox.DefaultPolicy()
	disabled.Mode = sandbox.ModeDisabled
	disabledEngine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: t.TempDir(), Policy: disabled})
	for name, dispatch := range map[string]func(*Dispatcher) DispatchOutcome{
		"plugin hook, nil engine": func(d *Dispatcher) DispatchOutcome {
			return d.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"})
		},
		"plugin hook, disabled engine": func(d *Dispatcher) DispatchOutcome {
			return d.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash", Sandbox: disabledEngine})
		},
		"pipeline, disabled stage engine": func(d *Dispatcher) DispatchOutcome {
			return d.ForPipeline(disabledEngine, "").Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			spawns = 0
			marker := filepath.Join(t.TempDir(), "ran")
			hook := writerHook("guard", EventBeforeTool, marker)
			hook.PluginID = "acme"
			audit, err := NewAuditStore(AuditStoreOptions{AuditPath: filepath.Join(t.TempDir(), "audit.jsonl")})
			if err != nil {
				t.Fatal(err)
			}
			result := dispatch(NewDispatcher(DispatcherOptions{Config: beforeToolConfig(hook), Audit: audit}))
			if record := lastCompleted(t, audit, "guard"); record.Execution != ExecutionRefused || record.Status != AuditBlocked {
				t.Fatalf("audit = %+v, want a blocked record with execution refused", record)
			}
			if !result.Blocked || !strings.Contains(result.Reason, "plugins.acme.hostExecution") {
				t.Fatalf("outcome = %+v, want a fail-closed refusal naming the key", result)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) || spawns != 0 {
				t.Fatalf("hook started (stat err=%v, spawns=%d), want zero spawn", err, spawns)
			}
		})
	}
}

// TestUserHookScriptUnderConfigDirNeedsRunOnHostInPipeline: a user hook script
// stored under the Splice config directory (a default DenyRead entry) cannot
// run in the pipeline sandbox. User hooks get no read carve-out. The failure
// is loud and names runOnHost, and runOnHost makes the hook work.
func TestUserHookScriptUnderConfigDirNeedsRunOnHostInPipeline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	if backend := sandbox.SelectBackend(sandbox.BackendOptions{}); !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	script := filepath.Join(home, ".config", "splice", "hooks", "fmt.sh")
	workspace := filepath.Join(base, "workspace")
	for _, dir := range []string{filepath.Dir(script), workspace} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho formatted\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stageEngine := procrun.NewStageEngineFrom(sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workspace, Policy: sandbox.DefaultPolicy(), Backend: sandbox.SelectBackend(sandbox.BackendOptions{})}), workspace)

	hook := Definition{ID: "fmt", Event: EventBeforeTool, Command: script, Enabled: true}
	dispatcher := NewDispatcher(DispatcherOptions{Config: beforeToolConfig(hook), Cwd: workspace})
	outcome := dispatcher.ForPipeline(stageEngine, workspace).Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"})
	if !outcome.Blocked || !strings.Contains(outcome.Reason, `hook "fmt"`) || !strings.Contains(outcome.Reason, `"runOnHost": true`) {
		t.Fatalf("pipeline outcome = %+v, want a loud block naming the hook and runOnHost", outcome)
	}
	if outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"}); outcome.Blocked {
		t.Fatalf("interactive outcome = %+v, want the host run", outcome)
	}
	hook.RunOnHost = true
	trusted := NewDispatcher(DispatcherOptions{Config: beforeToolConfig(hook), Cwd: workspace}).ForPipeline(stageEngine, workspace)
	if outcome := trusted.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"}); outcome.Blocked {
		t.Fatalf("runOnHost outcome = %+v, want the host run", outcome)
	}
}
