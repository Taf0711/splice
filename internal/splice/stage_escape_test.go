package splice

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/dtools"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
)

// The tests in this file run one adversarial script through each stage path
// that runs repository code, with the REAL registry bash tool and the host
// native backend. The script tries four prohibited actions (a write outside
// the workspace and outside every temp root, two credential reads under a fake
// HOME, an outbound TCP connection to a loopback listener) and one permitted
// action (a write inside the workspace, the positive control).

type escapeFixture struct {
	workDir    string
	outsideDir string
	fakeHome   string
	netrc      string
	sshKey     string
	apiKey     string
	accepted   atomic.Int32
	port       int
	script     string
}

func newEscapeFixture(t *testing.T) *escapeFixture {
	t.Helper()
	requireHostNativeBackend(t)
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 unavailable for the network check: %v", err)
	}
	outside, err := homeCacheDir(t, "splice-audit3-")
	if err != nil {
		t.Skipf("home cache dir unavailable: %v", err)
	}
	f := &escapeFixture{}
	f.outsideDir = mustEval(t, outside)
	f.workDir = mustEval(t, t.TempDir())
	f.fakeHome = mustEval(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(f.workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("setup: go.mod: %v", err)
	}
	f.netrc = "NETRC-SECRET-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	f.sshKey = "SSH-SECRET-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.WriteFile(filepath.Join(f.fakeHome, ".netrc"), []byte(f.netrc), 0o600); err != nil {
		t.Fatalf("setup: netrc: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(f.fakeHome, ".ssh"), 0o700); err != nil {
		t.Fatalf("setup: ssh dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.fakeHome, ".ssh", "id_test"), []byte(f.sshKey), 0o600); err != nil {
		t.Fatalf("setup: ssh key: %v", err)
	}
	t.Setenv("HOME", f.fakeHome)
	f.apiKey = "sk-audit3-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Setenv("OPENAI_API_KEY", f.apiKey)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.accepted.Add(1)
			_ = conn.Close()
		}
	}()
	f.port = ln.Addr().(*net.TCPAddr).Port
	// Positive control for the listener: a host dial is counted.
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatalf("setup: host dial of the listener: %v", err)
	}
	_ = conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for f.accepted.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.accepted.Load() == 0 {
		t.Fatalf("setup: the listener did not count the host dial")
	}
	f.accepted.Store(0)

	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	f.script = strings.Join([]string{
		"echo INSIDE > " + q(filepath.Join(f.workDir, "inside.txt")),
		"echo ESCAPED > " + q(filepath.Join(f.outsideDir, "escape.txt")) + " 2>/dev/null",
		"cat " + q(filepath.Join(f.fakeHome, ".netrc")) + " > " + q(filepath.Join(f.workDir, "leak_netrc.txt")) + " 2>/dev/null",
		"cat " + q(filepath.Join(f.fakeHome, ".ssh", "id_test")) + " > " + q(filepath.Join(f.workDir, "leak_ssh.txt")) + " 2>/dev/null",
		"python3 -c 'import socket,sys; s=socket.create_connection((\"127.0.0.1\", int(sys.argv[1])), 3); s.close(); open(sys.argv[2], \"w\").write(\"NET\")' " +
			strconv.Itoa(f.port) + " " + q(filepath.Join(f.workDir, "net.txt")) + " 2>/dev/null",
		"printenv OPENAI_API_KEY > " + q(filepath.Join(f.workDir, "env.txt")),
		"exit 0",
	}, "\n")
	return f
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("setup: resolve %s: %v", path, err)
	}
	return resolved
}

func (f *escapeFixture) reset(t *testing.T) {
	t.Helper()
	for _, name := range []string{"inside.txt", "leak_netrc.txt", "leak_ssh.txt", "net.txt", "env.txt"} {
		_ = os.Remove(filepath.Join(f.workDir, name))
	}
	_ = os.Remove(filepath.Join(f.outsideDir, "escape.txt"))
	f.accepted.Store(0)
}

func (f *escapeFixture) ran() bool {
	data, err := os.ReadFile(filepath.Join(f.workDir, "inside.txt"))
	return err == nil && strings.Contains(string(data), "INSIDE")
}

// violations lists the prohibited actions that succeeded.
func (f *escapeFixture) violations() []string {
	var out []string
	if _, err := os.Stat(filepath.Join(f.outsideDir, "escape.txt")); err == nil {
		out = append(out, "wrote outside the workspace ("+f.outsideDir+")")
	}
	if data, _ := os.ReadFile(filepath.Join(f.workDir, "leak_netrc.txt")); strings.Contains(string(data), f.netrc) {
		out = append(out, "read ~/.netrc")
	}
	if data, _ := os.ReadFile(filepath.Join(f.workDir, "leak_ssh.txt")); strings.Contains(string(data), f.sshKey) {
		out = append(out, "read ~/.ssh/id_test")
	}
	if data, _ := os.ReadFile(filepath.Join(f.workDir, "env.txt")); strings.Contains(string(data), f.apiKey) {
		out = append(out, "saw the provider key OPENAI_API_KEY in its environment")
	}
	time.Sleep(50 * time.Millisecond)
	if f.accepted.Load() > 0 {
		out = append(out, "connected to 127.0.0.1:"+strconv.Itoa(f.port))
	} else if data, _ := os.ReadFile(filepath.Join(f.workDir, "net.txt")); strings.Contains(string(data), "NET") {
		out = append(out, "network connect reported success")
	}
	return out
}

// escapeUserEngines are the user (interactive) engines a run can carry: the
// default policy, and deliberately loose ones. The stage engine must not
// inherit the loosening.
func escapeUserEngines(f *escapeFixture) []struct {
	name string
	mode agent.PermissionMode
	user func() *sandbox.Engine
} {
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	return []struct {
		name string
		mode agent.PermissionMode
		user func() *sandbox.Engine
	}{
		{name: "default policy", mode: agent.PermissionModeAuto, user: func() *sandbox.Engine {
			return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: f.workDir, Policy: sandbox.DefaultPolicy(), Backend: backend})
		}},
		{name: "loose: network allow and outside write", mode: agent.PermissionModeAuto, user: func() *sandbox.Engine {
			return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: f.workDir, Backend: backend, Policy: sandbox.Policy{
				Mode: sandbox.ModeEnforce, Network: sandbox.NetworkAllow, EnforceWorkspace: true, AllowWrite: []string{f.outsideDir},
			}})
		}},
		{name: "loose: unsafe mode", mode: agent.PermissionModeUnsafe, user: func() *sandbox.Engine {
			return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: f.workDir, Backend: backend, Policy: sandbox.Policy{
				Mode: sandbox.ModeEnforce, Network: sandbox.NetworkAllow, EnforceWorkspace: true, AllowWrite: []string{f.outsideDir},
			}})
		}},
		{name: "loose: mode disabled", mode: agent.PermissionModeAuto, user: func() *sandbox.Engine {
			return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: f.workDir, Backend: backend, Policy: sandbox.Policy{Mode: sandbox.ModeDisabled}})
		}},
	}
}

func escapeRegistry(workDir string) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(workDir))
	return registry
}

// runEscapeThroughTestRunner runs the script as the verify command of a
// one-node test runner topology, through splice.Run.
func runEscapeThroughTestRunner(t *testing.T, f *escapeFixture, user *sandbox.Engine, mode agent.PermissionMode) {
	t.Helper()
	writeWorkspaceTopology(t, f.workDir, "escape-audit", schemas.PipelineNode{Name: "test_runner", Type: "test_runner"})
	_, err := Run(context.Background(), "run the tests", noModelProvider{}, agent.Options{
		Cwd:              f.workDir,
		Registry:         escapeRegistry(f.workDir),
		PermissionMode:   mode,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
		Sandbox:          user,
		VerifyCommand:    []string{"sh", "-c", f.script},
	}, nil, nil)
	if err != nil {
		t.Logf("Run returned %.160v", err)
	}
}

// stageRunnerFor builds the production stage runner of a run (the runner the
// model-free stages get) over the real bash tool.
func stageRunnerFor(f *escapeFixture, registry *tools.Registry, user *sandbox.Engine, mode agent.PermissionMode) (PipelineRunConfig, ToolRunner) {
	return buildStageToolRunner(PipelineConfigFromAgentOptions(agent.Options{
		Cwd:              f.workDir,
		Registry:         registry,
		PermissionMode:   mode,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
		Sandbox:          user,
	}), f.workDir)
}

func runEscapeThroughStage(t *testing.T, f *escapeFixture, stage stages.Stage, input schemas.HarnessStageInput, user *sandbox.Engine, mode agent.PermissionMode) {
	t.Helper()
	options, runner := stageRunnerFor(f, escapeRegistry(f.workDir), user, mode)
	if _, err := runStageWithContext(context.Background(), input, stage, 1, agent.ModelSelection{}, options, f.workDir, runner, nil, stage.Capabilities(), 0, nil, nil, 0); err != nil {
		t.Logf("stage returned %.160v", err)
	}
}

func runEscapeThroughAcceptance(t *testing.T, f *escapeFixture, user *sandbox.Engine, mode agent.PermissionMode) {
	t.Helper()
	command := f.script
	runEscapeThroughStage(t, f, stages.AcceptanceVerifier{}, schemas.HarnessStageInput{
		RunID: "run-escape", StageName: "acceptance_verifier",
		AcceptanceFacts: []schemas.AcceptanceFact{{Statement: "escape", AutomatedVerification: true, VerificationCommand: &command}},
	}, user, mode)
}

func runEscapeThroughCommandNode(t *testing.T, f *escapeFixture, user *sandbox.Engine, mode agent.PermissionMode) {
	t.Helper()
	runEscapeThroughStage(t, f, stages.CommandStage{Name: "escape_node", Command: []string{"sh", "-c", f.script}},
		schemas.HarnessStageInput{RunID: "run-escape", StageName: "escape_node"}, user, mode)
}

// runEscapeThroughBandit runs the script as a fake "python" (the bandit
// scanner binary) through the stage runner and the real bandit dtool.
func runEscapeThroughBandit(t *testing.T, f *escapeFixture, user *sandbox.Engine, mode agent.PermissionMode) {
	t.Helper()
	binDir := mustEval(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(binDir, "python"), []byte("#!/bin/sh\n"+f.script+"\n"), 0o755); err != nil {
		t.Fatalf("setup: fake python: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(f.workDir, "main.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatalf("setup: main.py: %v", err)
	}
	registry := escapeRegistry(f.workDir)
	registry.Register(dtools.NewBanditTool(f.workDir))
	options, _ := stageRunnerFor(f, registry, user, mode)
	res, err := options.stageToolRunner.RunTool(context.Background(), "bandit", map[string]any{"paths": []any{"main.py"}})
	if err != nil {
		t.Logf("bandit returned %v", err)
	}
	t.Logf("bandit OK=%v output=%.200q", res.OK, res.Output)
}

type escapePath struct {
	name string
	run  func(*testing.T, *escapeFixture, *sandbox.Engine, agent.PermissionMode)
}

func escapePaths() []escapePath {
	return []escapePath{
		{name: "test runner (Run)", run: runEscapeThroughTestRunner},
		{name: "acceptance fact", run: runEscapeThroughAcceptance},
		{name: "command node", run: runEscapeThroughCommandNode},
		{name: "bandit scanner", run: runEscapeThroughBandit},
	}
}

// TestStagePathsDenyEscapeUnderAnyUserPolicy checks that every stage path that runs repository code denies the four
// prohibited actions, under the default user policy and under deliberately
// loose ones, while the in-workspace write succeeds.
func TestStagePathsDenyEscapeUnderAnyUserPolicy(t *testing.T) {
	for _, path := range escapePaths() {
		t.Run(path.name, func(t *testing.T) {
			f := newEscapeFixture(t)
			for _, engine := range escapeUserEngines(f) {
				t.Run(engine.name, func(t *testing.T) {
					f.reset(t)
					path.run(t, f, engine.user(), engine.mode)
					v := f.violations()
					if !f.ran() && engine.mode == agent.PermissionModeUnsafe && len(v) == 0 {
						// Unsafe mode refuses a stage command that the risk
						// classifier reads as network, before any spawn. A
						// refusal is a valid outcome; there is nothing else to
						// check.
						t.Logf("%s refused the command in unsafe mode (network classification)", path.name)
						return
					}
					if !f.ran() {
						t.Fatalf("setup: the script did not run (no inside.txt); the positive control failed")
					}
					if len(v) > 0 {
						t.Errorf("%s under %s: prohibited actions succeeded: %s", path.name, engine.name, strings.Join(v, "; "))
					}
				})
			}
		})
	}
}

// countWorkspaceSpawns installs a procrun audit sink that counts the spawns
// in workDir that were not refused. It returns the counter.
func countWorkspaceSpawns(t *testing.T, workDir string) func() int {
	t.Helper()
	var mu sync.Mutex
	spawned := 0
	previous := procrun.SetAuditSink(func(record procrun.AuditRecord) {
		if record.Decision.Rejected || !strings.HasPrefix(record.Dir, workDir) {
			return
		}
		mu.Lock()
		spawned++
		mu.Unlock()
	})
	t.Cleanup(func() { procrun.SetAuditSink(previous) })
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		n := spawned
		spawned = 0
		return n
	}
}

// TestInheritedSandboxMarkersRefuseStageCommands checks inherited sandbox markers.
// With SPLICE_SANDBOXED=1 and SPLICE_SANDBOX_BACKEND set in the Splice
// process environment (a Splice run started from inside a Splice-wrapped
// shell, or a hand-exported pair), and no user acceptance, each stage path
// refuses before any spawn: the escape cannot succeed and the script does not
// run.
func TestInheritedSandboxMarkersRefuseStageCommands(t *testing.T) {
	for _, path := range escapePaths() {
		t.Run(path.name, func(t *testing.T) {
			f := newEscapeFixture(t)
			spawns := countWorkspaceSpawns(t, f.workDir)
			// Control without the markers: the same path spawns and denies the escape.
			f.reset(t)
			loose := escapeUserEngines(f)[1]
			path.run(t, f, loose.user(), loose.mode)
			if !f.ran() || len(f.violations()) > 0 || spawns() == 0 {
				t.Fatalf("setup: control without markers: ran=%v violations=%v", f.ran(), f.violations())
			}

			t.Setenv(sandbox.EnvSandboxed, "1")
			t.Setenv(sandbox.EnvSandboxBackend, string(sandbox.BackendMacOSSeatbelt))
			f.reset(t)
			path.run(t, f, loose.user(), loose.mode)
			if v := f.violations(); len(v) > 0 {
				t.Errorf("%s with inherited sandbox markers ran the stage command unwrapped: %s", path.name, strings.Join(v, "; "))
			}
			if n := spawns(); n != 0 || f.ran() {
				t.Errorf("%s with inherited sandbox markers: %d spawn(s), script ran=%v; want a refusal before any spawn", path.name, n, f.ran())
			}
		})
	}
}

// TestEnforcementGapTruthfulUnderMarkers checks that the stage engine's
// EnforcementGap is the stage runner's only gate. When it reports no gap,
// the plan it builds must be wrapped. Under the markers the gap is the
// nested-sandbox gap, and NestedSandbox tells it from the other gaps.
func TestEnforcementGapTruthfulUnderMarkers(t *testing.T) {
	requireHostNativeBackend(t)
	workDir := mustEval(t, t.TempDir())
	stage := procrun.NewStageEngineFrom(sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workDir, Policy: sandbox.DefaultPolicy(), Backend: sandbox.SelectBackend(sandbox.BackendOptions{}),
	}), workDir)
	plan, err := stage.BuildCommandPlan(sandbox.CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: workDir})
	if err != nil || !plan.Wrapped || stage.EnforcementGap() != "" {
		t.Fatalf("setup: control without markers: wrapped=%v gap=%q err=%v", plan.Wrapped, stage.EnforcementGap(), err)
	}
	t.Setenv(sandbox.EnvSandboxed, "1")
	t.Setenv(sandbox.EnvSandboxBackend, string(sandbox.BackendMacOSSeatbelt))
	plan, err = stage.BuildCommandPlan(sandbox.CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: workDir})
	if err != nil {
		t.Fatalf("setup: plan under markers: %v", err)
	}
	if gap := stage.EnforcementGap(); gap == "" && !plan.Wrapped {
		t.Errorf("stage engine EnforcementGap() = \"\" (enforcing) but its plan is unwrapped under inherited markers (backend=%s enforcement=%s)", plan.Backend.Name, plan.EnforcementLevel)
	}
	if gap := stage.EnforcementGap(); gap != sandbox.NestedSandboxGap || !stage.NestedSandbox() {
		t.Errorf("under the markers gap = %q nested = %v, want the nested-sandbox gap", gap, stage.NestedSandbox())
	}
	unavailable := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.DefaultPolicy(), Backend: sandbox.Backend{Name: sandbox.BackendUnavailable}})
	disabled := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.Policy{Mode: sandbox.ModeDisabled}, Backend: sandbox.SelectBackend(sandbox.BackendOptions{})})
	for name, engine := range map[string]*sandbox.Engine{"no backend": unavailable, "disabled policy": disabled, "nil engine": nil} {
		if engine.NestedSandbox() || engine.EnforcementGap() == sandbox.NestedSandboxGap {
			t.Errorf("%s under the markers reports the nested-sandbox gap (%q); a user acceptance would then cover it", name, engine.EnforcementGap())
		}
	}
}

// nestedAcceptedRun runs a one-node test runner topology through Run with the
// inherited sandbox markers set and the user acceptance as given. The verify
// command is `go test ./...`; a fake go on PATH prints a passing go test
// -json stream and writes RAN and its OPENAI_API_KEY value to env.txt.
func nestedAcceptedRun(t *testing.T, accept bool, backend sandbox.Backend) (schemas.PipelineResult, error, string, func() int) {
	t.Helper()
	workDir := mustEval(t, t.TempDir())
	binDir := mustEval(t, t.TempDir())
	fakeGo := "#!/bin/sh\n" +
		"{ echo RAN; printenv OPENAI_API_KEY; } > " + filepath.Join(workDir, "env.txt") + "\n" +
		`echo '{"Action":"run","Package":"example","Test":"TestNested"}'` + "\n" +
		`echo '{"Action":"pass","Package":"example","Test":"TestNested","Elapsed":0.01}'` + "\n" +
		`echo '{"Action":"pass","Package":"example","Elapsed":0.02}'` + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatalf("setup: fake go: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	apiKey := "sk-nested-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Setenv("OPENAI_API_KEY", apiKey)
	t.Setenv(sandbox.EnvSandboxed, "1")
	t.Setenv(sandbox.EnvSandboxBackend, "outer-sandbox")
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("setup: go.mod: %v", err)
	}
	writeWorkspaceTopology(t, workDir, "nested-accepted", schemas.PipelineNode{Name: "test_runner", Type: "test_runner"})
	spawns := countWorkspaceSpawns(t, workDir)
	out, err := Run(context.Background(), "run the tests", noModelProvider{}, agent.Options{
		Cwd:                 workDir,
		Registry:            escapeRegistry(workDir),
		PermissionMode:      agent.PermissionModeAuto,
		FileTracker:         tools.NewFileTracker(),
		TrustedWorkspace:    true,
		Sandbox:             sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.DefaultPolicy(), Backend: backend}),
		VerifyCommand:       []string{"go", "test", "./..."},
		AcceptNestedSandbox: accept,
	}, nil, nil)
	var result schemas.PipelineResult
	if err == nil {
		if jsonErr := json.Unmarshal([]byte(out.FinalAnswer), &result); jsonErr != nil {
			t.Fatalf("setup: parse final answer: %v", jsonErr)
		}
	}
	envData, _ := os.ReadFile(filepath.Join(workDir, "env.txt"))
	if strings.Contains(string(envData), apiKey) {
		t.Errorf("a stage command in a nested run saw the provider key OPENAI_API_KEY")
	}
	return result, err, string(envData), spawns
}

// TestAcceptedNestedRunIsVisibleAndCanVerify checks an accepted nested run: with the markers set and
// the user acceptance, the test runner command runs as a pass-through under
// the outer sandbox. The evidence says not wrapped and accepted, the summary
// has the line, the provider key is scrubbed from the child environment
// and passing per-test evidence still makes the run VERIFIED.
func TestAcceptedNestedRunIsVisibleAndCanVerify(t *testing.T) {
	requireHostNativeBackend(t)
	result, err, envData, spawns := nestedAcceptedRun(t, true, sandbox.SelectBackend(sandbox.BackendOptions{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if spawns() == 0 || envData == "" {
		t.Fatalf("the accepted stage command did not spawn (env file %q)", envData)
	}
	if result.Status != "completed" || !result.Verified || result.VerificationOutcome != schemas.VerificationOutcomeVerified {
		t.Fatalf("status=%q verified=%v outcome=%q, want a VERIFIED run from the passing per-test evidence", result.Status, result.Verified, result.VerificationOutcome)
	}
	if !result.NestedSandboxAccepted {
		t.Fatal("PipelineResult.NestedSandboxAccepted = false on an accepted nested run")
	}
	found := false
	for _, record := range result.Stages {
		for _, entry := range record.SandboxEvidence {
			if err := entry.Validate(); err != nil {
				t.Fatalf("evidence %+v: %v", entry, err)
			}
			if entry.Source == schemas.SandboxSourceBashTool && entry.Outcome == schemas.SandboxOutcomeRan {
				found = true
				if entry.Wrapped || !entry.NestedAccepted || entry.Policy.Source != schemas.SandboxPolicyStage {
					t.Errorf("evidence %+v, want not wrapped, nested accepted, stage policy", entry)
				}
			}
		}
		if line := sandboxEvidenceLine(record); record.Name == "test_runner" && !strings.Contains(line, "not wrapped (nested run accepted by the user)") {
			t.Errorf("sandbox evidence line %q does not say the spawn was not wrapped", line)
		}
	}
	if !found {
		t.Fatalf("no bash tool evidence entry for the test command: %+v", result.Stages)
	}
	if summary := completionSummary(result); !strings.Contains(summary, presentation.NestedSandboxAcceptedLine) {
		t.Fatalf("summary %q lacks %q", summary, presentation.NestedSandboxAcceptedLine)
	}
}

// TestNestedRunAcceptanceNeedsTheSetting checks that the same run without the
// acceptance refuses before any spawn and is not marked accepted; with the
// acceptance and no native backend it still refuses: the acceptance covers
// the nested-sandbox gap only.
func TestNestedRunAcceptanceNeedsTheSetting(t *testing.T) {
	requireHostNativeBackend(t)
	for _, tc := range []struct {
		name    string
		accept  bool
		backend sandbox.Backend
	}{
		{"not accepted", false, sandbox.SelectBackend(sandbox.BackendOptions{})},
		{"accepted, no backend", true, sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "no backend in this test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err, envData, spawns := nestedAcceptedRun(t, tc.accept, tc.backend)
			if n := spawns(); n != 0 || envData != "" {
				t.Fatalf("%d spawn(s), env file %q; want a refusal before any spawn", n, envData)
			}
			if err == nil && (result.Verified || result.NestedSandboxAccepted) {
				t.Fatalf("verified=%v nested accepted=%v, want neither", result.Verified, result.NestedSandboxAccepted)
			}
		})
	}
}

// TestUnavailableBackendSpawnsNothing checks that with no
// native backend, no stage path spawns a process. Spawns are counted by the
// procrun audit sink (non-rejected records whose directory is the workspace)
// and by the script's own side effect.
func TestUnavailableBackendSpawnsNothing(t *testing.T) {
	for _, path := range escapePaths() {
		t.Run(path.name, func(t *testing.T) {
			f := newEscapeFixture(t)
			var mu sync.Mutex
			spawned := 0
			rejected := 0
			previous := procrun.SetAuditSink(func(record procrun.AuditRecord) {
				if !strings.HasPrefix(record.Dir, f.workDir) {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if record.Decision.Rejected {
					rejected++
				} else {
					spawned++
				}
			})
			defer procrun.SetAuditSink(previous)

			// Control: with the host backend the same path spawns.
			f.reset(t)
			loose := escapeUserEngines(f)[0]
			path.run(t, f, loose.user(), loose.mode)
			mu.Lock()
			controlSpawns := spawned
			spawned, rejected = 0, 0
			mu.Unlock()
			if !f.ran() || controlSpawns == 0 {
				t.Fatalf("setup: control with the host backend: ran=%v audited spawns=%d", f.ran(), controlSpawns)
			}

			for _, mode := range []agent.PermissionMode{agent.PermissionModeAuto, agent.PermissionModeUnsafe} {
				f.reset(t)
				user := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: f.workDir, Policy: sandbox.DefaultPolicy(),
					Backend: sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "no backend in this test"}})
				path.run(t, f, user, mode)
				mu.Lock()
				n := spawned
				spawned, rejected = 0, 0
				mu.Unlock()
				if n != 0 || f.ran() {
					t.Errorf("%s (%s) with no native backend: %d audited spawn(s), script ran=%v; want zero spawns", path.name, mode, n, f.ran())
				}
			}
		})
	}
}

// TestUserEngineGrantsNeverWidenStageCommands checks that user engine grants never widen stage commands.
// A model-driven call in the same session can widen the USER engine: a
// request_permissions session or turn profile (network and an outside write
// root), a session tool grant, a session command-prefix grant. None of them
// may reach a stage command, whether the stage engine is derived before or
// after the grants. The control shows that the grants did widen the user
// engine.
func TestUserEngineGrantsNeverWidenStageCommands(t *testing.T) {
	f := newEscapeFixture(t)
	enabled := true
	grant := func(user *sandbox.Engine) {
		t.Helper()
		profile := sandbox.RequestPermissionProfile{
			Network:    &sandbox.NetworkPermissions{Enabled: &enabled},
			FileSystem: &sandbox.FileSystemPermissions{Write: []string{f.outsideDir}, Read: []string{f.fakeHome}},
		}
		for _, scope := range []sandbox.PermissionGrantScope{sandbox.PermissionGrantScopeSession, sandbox.PermissionGrantScopeTurn} {
			if _, err := user.GrantRequestPermissions(profile, scope); err != nil {
				t.Fatalf("setup: grant %s profile: %v", scope, err)
			}
		}
		if _, err := user.GrantForSession(sandbox.GrantInput{ToolName: "bash", Decision: sandbox.GrantAllow, Reason: "audit"}); err != nil {
			t.Fatalf("setup: session tool grant: %v", err)
		}
		user.GrantCommandPrefixForSession("bash", []string{"sh"})
	}
	newUser := func() *sandbox.Engine {
		return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: f.workDir, Policy: sandbox.DefaultPolicy(), Backend: sandbox.SelectBackend(sandbox.BackendOptions{})})
	}
	// Control: the grants widen the user engine's own plans.
	control := newUser()
	grant(control)
	plan, err := control.BuildCommandPlan(sandbox.CommandSpec{Name: "true", Dir: f.workDir})
	if err != nil || sandbox.NormalizeNetworkMode(plan.PermissionProfile.Network.Mode) != sandbox.NetworkAllow {
		t.Fatalf("setup: control: granted user engine network = %q (err %v), want allow", plan.PermissionProfile.Network.Mode, err)
	}

	run := func(t *testing.T, options PipelineRunConfig, runner ToolRunner) {
		t.Helper()
		f.reset(t)
		stage := stages.CommandStage{Name: "escape_node", Command: []string{"sh", "-c", f.script}}
		if _, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{RunID: "run-escape", StageName: "escape_node"}, stage, 1,
			agent.ModelSelection{}, options, f.workDir, runner, nil, stage.Capabilities(), 0, nil, nil, 0); err != nil {
			t.Logf("stage returned %.160v", err)
		}
		if !f.ran() {
			t.Fatalf("setup: the script did not run")
		}
		if v := f.violations(); len(v) > 0 {
			t.Errorf("user-engine grants widened a stage command: %s", strings.Join(v, "; "))
		}
	}
	t.Run("grants before the stage engine", func(t *testing.T) {
		user := newUser()
		grant(user)
		options, runner := stageRunnerFor(f, escapeRegistry(f.workDir), user, agent.PermissionModeAuto)
		run(t, options, runner)
	})
	t.Run("grants after the stage engine", func(t *testing.T) {
		user := newUser()
		options, runner := stageRunnerFor(f, escapeRegistry(f.workDir), user, agent.PermissionModeAuto)
		grant(user)
		run(t, options, runner)
	})
}
