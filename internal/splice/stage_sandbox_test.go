package splice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// The tests in this file pin the stage sandbox: the registry tool calls of
// the model-free stages (test runner, acceptance, scanners, command nodes)
// carry the stage engine, which is derived from the user policy. Model-driven
// tool calls keep the user engine.

// stageSandboxMarker tags a verify command, so a test can tell the
// test-runner spawn from any other bash call in the run.
const stageSandboxMarker = "splice-stage-sandbox-marker"

// recordingBashTool stands in for the registry bash tool. It records the
// sandbox engine and the arguments of each call and runs nothing.
type recordingBashTool struct {
	mu    *sync.Mutex
	calls *[]recordedBashCall
}

type recordedBashCall struct {
	command string
	args    map[string]any
	engine  *sandbox.Engine
}

func newRecordingBashTool() (recordingBashTool, func() []recordedBashCall) {
	tool := recordingBashTool{mu: &sync.Mutex{}, calls: &[]recordedBashCall{}}
	return tool, func() []recordedBashCall {
		tool.mu.Lock()
		defer tool.mu.Unlock()
		return append([]recordedBashCall(nil), *tool.calls...)
	}
}

func (recordingBashTool) Name() string        { return "bash" }
func (recordingBashTool) Description() string { return "records the sandbox engine of a bash call" }
func (recordingBashTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", Properties: map[string]tools.PropertySchema{}, AdditionalProperties: true}
}
func (recordingBashTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectShell, Permission: tools.PermissionPrompt, Reason: "recording bash"}
}
func (tool recordingBashTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return tool.RunWithOptions(ctx, args, tools.RunOptions{})
}
func (tool recordingBashTool) RunWithOptions(_ context.Context, args map[string]any, options tools.RunOptions) tools.Result {
	command, _ := args["command"].(string)
	tool.mu.Lock()
	*tool.calls = append(*tool.calls, recordedBashCall{command: command, args: args, engine: options.Sandbox})
	tool.mu.Unlock()
	return tools.Result{Status: tools.StatusOK, Output: "ok"}
}

// noModelProvider fails every request. A topology with only model-free
// stages must not call a model.
type noModelProvider struct{}

func (noModelProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	return nil, errors.New("this topology must not call a model")
}

// fakeNativeBackend is an available native backend for tests that record
// engines and never build a command plan.
func fakeNativeBackend() sandbox.Backend {
	return sandbox.Backend{Name: sandbox.BackendLinuxBwrap, Platform: "linux", Available: true, Executable: "/usr/bin/bwrap", CommandWrapping: true, NativeIsolation: true}
}

// runTestRunnerTopology runs a topology with one test runner node under the
// user engine and returns the bash calls that carry the verify marker.
func runTestRunnerTopology(t *testing.T, workDir string, user *sandbox.Engine) []recordedBashCall {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("setup: write go.mod: %v", err)
	}
	writeWorkspaceTopology(t, workDir, "stage-sandbox", schemas.PipelineNode{Name: "test_runner", Type: "test_runner"})

	bash, recordedCalls := newRecordingBashTool()
	registry := tools.NewRegistry()
	registry.Register(bash)

	var mu sync.Mutex
	var toolCalls []string
	_, err := Run(context.Background(), "run the tests", noModelProvider{}, agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		FileTracker:      tools.NewFileTracker(),
		TrustedWorkspace: true,
		Sandbox:          user,
		VerifyCommand:    []string{"true", stageSandboxMarker},
		OnToolCall: func(call agent.ToolCall) {
			mu.Lock()
			toolCalls = append(toolCalls, call.Name+" "+call.Arguments)
			mu.Unlock()
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("setup: Run: %v", err)
	}
	// Control: the test runner did spawn its command. A fix that skips the
	// spawn must not pass.
	spawned := false
	for _, line := range toolCalls {
		if strings.HasPrefix(line, "splice.test ") && strings.Contains(line, stageSandboxMarker) {
			spawned = true
		}
	}
	if !spawned {
		t.Fatalf("setup: no splice.test spawn with the marker was recorded; tool calls = %q", toolCalls)
	}
	var marked []recordedBashCall
	for _, call := range recordedCalls() {
		if strings.Contains(call.command, stageSandboxMarker) {
			marked = append(marked, call)
		}
	}
	if len(marked) == 0 {
		t.Fatalf("the test runner spawn did not reach the registry bash tool")
	}
	return marked
}

func requireHostNativeBackend(t *testing.T) sandbox.Backend {
	t.Helper()
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	return backend
}

// homeCacheDir makes a new temp directory under ~/.cache. It creates ~/.cache
// when it is missing. Cleanup removes the directory, and it removes ~/.cache
// only when this call created it and it is empty.
func homeCacheDir(t *testing.T, prefix string) (string, error) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cache := filepath.Join(home, ".cache")
	_, statErr := os.Stat(cache)
	created := os.IsNotExist(statErr)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(cache, prefix)
	if err != nil {
		return "", err
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
		if created {
			_ = os.Remove(cache) // fails unless the directory is empty
		}
	})
	return dir, nil
}

// TestTestRunnerSpawnUsesStageSandbox runs the test runner under a user
// policy that is looser than the stage policy (network allowed, one extra
// write root). The spawn must reach the bash tool with the stage profile.
func TestTestRunnerSpawnUsesStageSandbox(t *testing.T) {
	backend := requireHostNativeBackend(t)
	workDir := t.TempDir()

	// The agent-only write root must sit outside every system temp root
	// (t.TempDir paths are under one on macOS), or the stage profile would
	// cover it too and the write-root check would prove nothing.
	agentOnlyRoot, err := homeCacheDir(t, "splice-stage-sandbox-")
	if err != nil {
		t.Skipf("home cache dir unavailable: %v", err)
	}
	agentOnlyRoot, err = filepath.EvalSymlinks(agentOnlyRoot)
	if err != nil {
		t.Fatalf("setup: resolve agent-only root: %v", err)
	}

	user := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workDir,
		Policy: sandbox.Policy{
			Mode:             sandbox.ModeEnforce,
			Network:          sandbox.NetworkAllow,
			EnforceWorkspace: true,
			AllowWrite:       []string{agentOnlyRoot},
		},
		Backend: backend,
	})

	for _, call := range runTestRunnerTopology(t, workDir, user) {
		if call.engine == nil {
			t.Errorf("test runner spawn %q reached the bash tool with no sandbox engine", call.command)
			continue
		}
		plan, err := call.engine.BuildCommandPlan(sandbox.CommandSpec{Name: "true", Dir: workDir})
		if err != nil {
			t.Fatalf("setup: build plan for the recorded engine: %v", err)
		}
		profile := plan.PermissionProfile
		if profile.FileSystem.Kind != sandbox.FileSystemRestricted {
			t.Errorf("test runner spawn %q ran with filesystem %q, want the restricted stage profile", call.command, profile.FileSystem.Kind)
		}
		if sandbox.NormalizeNetworkMode(profile.Network.Mode) != sandbox.NetworkDeny {
			t.Errorf("test runner spawn %q ran with network %q (user policy), want %q (stage profile)", call.command, profile.Network.Mode, sandbox.NetworkDeny)
		}
		for _, root := range profile.FileSystem.WriteRoots {
			if root.Root == agentOnlyRoot || strings.HasPrefix(agentOnlyRoot, root.Root+string(filepath.Separator)) {
				t.Errorf("test runner spawn %q can write the agent-only root %s, want workspace-scoped stage profile", call.command, agentOnlyRoot)
			}
		}
	}
}

// TestTestRunnerSpawnKeepsUserDenyRead runs the test runner under a user
// policy that denies reads of <workspace>/secrets. The stage engine of the
// spawn must keep that denial: its plan names the path, and a stage command
// under it cannot read a file there.
func TestTestRunnerSpawnKeepsUserDenyRead(t *testing.T) {
	backend := requireHostNativeBackend(t)
	workDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("setup: resolve workspace: %v", err)
	}
	secrets := filepath.Join(workDir, "secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatalf("setup: mkdir secrets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "x"), []byte("USERSECRET"), 0o600); err != nil {
		t.Fatalf("setup: write secret: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "ok.txt"), []byte("WSOK"), 0o644); err != nil {
		t.Fatalf("setup: write workspace file: %v", err)
	}
	policy := sandbox.DefaultPolicy()
	policy.DenyRead = append(policy.DenyRead, secrets)
	user := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: policy, Backend: backend})

	for _, call := range runTestRunnerTopology(t, workDir, user) {
		if call.engine == nil {
			t.Fatalf("test runner spawn %q reached the bash tool with no sandbox engine", call.command)
		}
		plan, err := call.engine.BuildCommandPlan(sandbox.CommandSpec{Name: "true", Dir: workDir})
		if err != nil {
			t.Fatalf("setup: build plan for the recorded engine: %v", err)
		}
		denied := false
		for _, entry := range plan.PermissionProfile.FileSystem.DenyRead {
			if entry == secrets {
				denied = true
			}
		}
		if !denied {
			t.Errorf("test runner engine plan DenyRead = %v, want the user entry %s", plan.PermissionProfile.FileSystem.DenyRead, secrets)
		}

		read := func(path string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			prepared, err := procrun.Prepare(ctx, procrun.Request{
				ProfileID:       procrun.ProfileSpliceStage,
				Engine:          call.engine,
				AllowedBinaries: procrun.StageBinaries,
				Spec: sandbox.CommandSpec{
					Name: "python3",
					Args: []string{"-c", "import sys; sys.stdout.write(open(sys.argv[1]).read())", path},
					Dir:  workDir,
				},
			})
			if err != nil {
				return "", err
			}
			out, runErr := prepared.Cmd.CombinedOutput()
			return string(out), runErr
		}
		// Positive control: the same reader and engine read a workspace file.
		if out, err := read(filepath.Join(workDir, "ok.txt")); err != nil || !strings.Contains(out, "WSOK") {
			t.Fatalf("setup: reader control failed under the stage engine: err=%v output=%q", err, out)
		}
		if out, err := read(filepath.Join(secrets, "x")); strings.Contains(out, "USERSECRET") {
			t.Errorf("stage command read the user-denied %s/x (err=%v)", secrets, err)
		}
	}
}

// TestPrepareGitReadKeepsUserDenyRead runs a Splice git read under a user
// policy that denies reads of <workspace>/secrets. The user engine is carried
// on the context. Hashing a file under the denied path must fail, and a file
// elsewhere in the workspace must still hash. Without the user engine on the
// context the same read has the fresh stage policy and hashes the denied file.
func TestPrepareGitReadKeepsUserDenyRead(t *testing.T) {
	backend := requireHostNativeBackend(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not available: %v", err)
	}
	workDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("setup: resolve workspace: %v", err)
	}
	secrets := filepath.Join(workDir, "secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatalf("setup: mkdir secrets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "x"), []byte("USERSECRET"), 0o600); err != nil {
		t.Fatalf("setup: write secret: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "ok.txt"), []byte("WSOK"), 0o644); err != nil {
		t.Fatalf("setup: write workspace file: %v", err)
	}
	policy := sandbox.DefaultPolicy()
	policy.DenyRead = append(policy.DenyRead, secrets)
	user := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: policy, Backend: backend})

	hash := func(ctx context.Context, file string) error {
		cmd, done, err := prepareGitRead(ctx, workDir, "hash-object", "--", file)
		if err != nil {
			t.Fatalf("prepareGitRead: %v", err)
		}
		defer done()
		_, runErr := cmd.CombinedOutput()
		return runErr
	}
	denied := filepath.Join(secrets, "x")
	allowed := filepath.Join(workDir, "ok.txt")

	userCtx := withUserSandbox(context.Background(), user)
	if err := hash(userCtx, allowed); err != nil {
		t.Fatalf("setup: git read of the workspace file under the user engine failed: %v", err)
	}
	if err := hash(userCtx, denied); err == nil {
		t.Errorf("git read of the user-denied %s succeeded, want the user's DenyRead to apply", denied)
	}
	if err := hash(context.Background(), denied); err != nil {
		t.Errorf("control: without a user engine the fresh stage policy should read %s: %v", denied, err)
	}
}

// sandboxRecordingStage calls the bash tool once through StageOptions.RunTool.
type sandboxRecordingStage struct{ modelFree bool }

func (s sandboxRecordingStage) Capabilities() stages.Capabilities {
	return stages.Capabilities{ModelFree: s.modelFree}
}

func (s sandboxRecordingStage) Run(ctx context.Context, _ schemas.HarnessStageInput, _ zeroruntime.Provider, options stages.StageOptions) (schemas.HarnessStageOutput, error) {
	res, err := options.RunTool(ctx, "bash", map[string]any{"command": "echo hi"})
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	if !res.OK {
		return schemas.HarnessStageOutput{}, errors.New(res.Output)
	}
	return schemas.HarnessStageOutput{Summary: "ran", Confidence: 1}, nil
}

// TestStageRunnerFollowsStageCapabilities pins the runner choice: a
// model-free stage gets the stage engine, a model-driven stage keeps the user
// engine (network allowed here) unchanged.
func TestStageRunnerFollowsStageCapabilities(t *testing.T) {
	workDir := t.TempDir()
	user := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workDir,
		Policy:        sandbox.Policy{Mode: sandbox.ModeEnforce, Network: sandbox.NetworkAllow, EnforceWorkspace: true},
		Backend:       fakeNativeBackend(),
	})
	bash, recordedCalls := newRecordingBashTool()
	registry := tools.NewRegistry()
	registry.Register(bash)
	options, runner := buildStageToolRunner(PipelineConfigFromAgentOptions(agent.Options{
		Cwd:              workDir,
		Registry:         registry,
		PermissionMode:   agent.PermissionModeAuto,
		TrustedWorkspace: true,
		Sandbox:          user,
	}), workDir)
	if options.StageSandbox == nil || options.StageSandbox == user {
		t.Fatalf("StageSandbox = %p, want a stage engine distinct from the user engine %p", options.StageSandbox, user)
	}

	for _, tc := range []struct {
		name      string
		modelFree bool
		want      *sandbox.Engine
	}{
		{name: "model-driven", modelFree: false, want: user},
		{name: "model-free", modelFree: true, want: options.StageSandbox},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(recordedCalls())
			stage := sandboxRecordingStage{modelFree: tc.modelFree}
			if _, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{RunID: "run-stage-sandbox", StageName: "probe_stage"},
				stage, 1, agent.ModelSelection{}, options, workDir, runner, nil, stage.Capabilities(), 0, nil, nil, 0); err != nil {
				t.Fatalf("runStageWithContext: %v", err)
			}
			calls := recordedCalls()[before:]
			if len(calls) != 1 {
				t.Fatalf("bash calls = %d, want 1", len(calls))
			}
			if calls[0].engine != tc.want {
				t.Fatalf("bash call engine = %p, want %p (user %p, stage %p)", calls[0].engine, tc.want, user, options.StageSandbox)
			}
		})
	}
}

// TestStageRunnerRefusesEscalationAndGaps pins the stage runner guards: a
// stage call that asks for more than the stage policy is refused, and no
// stage call runs when the stage engine does not enforce.
func TestStageRunnerRefusesEscalationAndGaps(t *testing.T) {
	cases := []struct {
		name    string
		backend sandbox.Backend
		args    map[string]any
		want    string
	}{
		{name: "require_escalated", backend: fakeNativeBackend(), args: map[string]any{"command": "ls", "sandbox_permissions": "require_escalated"}, want: "sandbox_permissions"},
		{name: "use_default", backend: fakeNativeBackend(), args: map[string]any{"command": "ls", "sandbox_permissions": "use_default"}, want: "sandbox_permissions"},
		{name: "additional_permissions", backend: fakeNativeBackend(), args: map[string]any{"command": "ls", "additional_permissions": map[string]any{"network": map[string]any{"enabled": true}}}, want: "additional_permissions"},
		{name: "unavailable backend", backend: sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "no backend in this test"}, args: map[string]any{"command": "ls"}, want: "stage sandbox is not enforcing: no native sandbox backend is available (no backend in this test)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			user := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.DefaultPolicy(), Backend: tc.backend})
			bash, recordedCalls := newRecordingBashTool()
			registry := tools.NewRegistry()
			registry.Register(bash)
			options, _ := buildStageToolRunner(PipelineConfigFromAgentOptions(agent.Options{
				Cwd:              workDir,
				Registry:         registry,
				PermissionMode:   agent.PermissionModeUnsafe,
				TrustedWorkspace: true,
				Sandbox:          user,
			}), workDir)
			res, err := options.stageToolRunner.RunTool(context.Background(), "bash", tc.args)
			if err != nil {
				t.Fatalf("RunTool: %v", err)
			}
			if res.OK || !strings.Contains(res.Output, tc.want) {
				t.Fatalf("stage call result OK=%v output=%q, want a refusal naming %q", res.OK, res.Output, tc.want)
			}
			if n := len(recordedCalls()); n != 0 {
				t.Fatalf("refused stage call reached the bash tool %d time(s)", n)
			}
		})
	}
}

// TestCommandNodeRunsOnlyUnderEnforcingStageSandbox pins that a command node
// never runs unsandboxed. A disabled user policy does not matter: the node
// runs under the enforcing stage engine. With no native backend the node is
// refused before any bash call.
func TestCommandNodeRunsOnlyUnderEnforcingStageSandbox(t *testing.T) {
	const node = "echo_check"
	cases := []struct {
		name          string
		user          func(workDir string) *sandbox.Engine
		requireRefuse bool
	}{
		{
			name: "disabled user policy",
			user: func(workDir string) *sandbox.Engine {
				return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.Policy{Mode: sandbox.ModeDisabled}, Backend: sandbox.SelectBackend(sandbox.BackendOptions{})})
			},
		},
		{
			name: "unavailable backend",
			user: func(workDir string) *sandbox.Engine {
				return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.DefaultPolicy(), Backend: sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "no backend in this test"}})
			},
			requireRefuse: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			workDir, registry := newRunTestWorkspace(t)
			bash, recordedCalls := newRecordingBashTool()
			registry.Register(bash)
			writeWorkspaceTopology(t, workDir, "command-node-sandbox", schemas.PipelineNode{
				Name: node, Type: schemas.NodeTypeCommand, Command: []string{"echo", "command-node-ran"},
			})

			result, err := Run(context.Background(), "check the project", runFakeProvider{}, agent.Options{
				Cwd:              workDir,
				Registry:         registry,
				PermissionMode:   agent.PermissionModeAuto,
				FileTracker:      tools.NewFileTracker(),
				TrustedWorkspace: true,
				Sandbox:          tc.user(workDir),
			}, nil, nil)

			var nodeCalls []recordedBashCall
			for _, call := range recordedCalls() {
				if strings.Contains(call.command, "command-node-ran") {
					nodeCalls = append(nodeCalls, call)
				}
			}

			refusal := ""
			if err != nil {
				refusal = err.Error()
			} else {
				var pipeline schemas.PipelineResult
				if jsonErr := json.Unmarshal([]byte(result.FinalAnswer), &pipeline); jsonErr != nil {
					t.Fatalf("setup: parse final answer: %v", jsonErr)
				}
				if pipeline.Status != "completed" || !pipeline.Verified {
					for _, record := range pipeline.Stages {
						refusal += " " + DerefString(record.OutputSummary)
					}
				}
			}
			refused := strings.Contains(refusal, node) && strings.Contains(strings.ToLower(refusal), "sandbox")

			switch {
			case refused && len(nodeCalls) == 0:
				// Outcome (a): a refusal that names the node and the sandbox.
			case tc.requireRefuse:
				t.Fatalf("command node %s with no native backend: refusal %q and %d bash call(s), want a refusal with zero bash calls", node, refusal, len(nodeCalls))
			case len(nodeCalls) == 0:
				t.Fatalf("command node %s neither ran nor was refused (refusal %q)", node, refusal)
			default:
				// Outcome (b): every bash call ran under an enforcing,
				// network-denying engine.
				for _, call := range nodeCalls {
					if call.engine == nil {
						t.Fatalf("command node bash call %q has no sandbox engine", call.command)
					}
					if gap := call.engine.EnforcementGap(); gap != "" {
						t.Errorf("command node bash call %q ran under a non-enforcing engine: %s", call.command, gap)
					}
					plan, err := call.engine.BuildCommandPlan(sandbox.CommandSpec{Name: "true", Dir: workDir})
					if err != nil {
						t.Fatalf("setup: build plan for the recorded engine: %v", err)
					}
					if sandbox.NormalizeNetworkMode(plan.PermissionProfile.Network.Mode) != sandbox.NetworkDeny {
						t.Errorf("command node bash call %q ran with network %q, want deny", call.command, plan.PermissionProfile.Network.Mode)
					}
				}
			}
		})
	}
}

// TestStageRunnerKeepsUserApprovalPrompts pins the permission behavior of a
// stage call in each mode: the stage runner asks for approval exactly when
// the user-engine runner (the behavior before the stage sandbox) asks, and a
// call it runs carries the stage engine.
func TestStageRunnerKeepsUserApprovalPrompts(t *testing.T) {
	engines := []struct {
		name string
		user func(workDir string) *sandbox.Engine
	}{
		{name: "no user engine", user: func(string) *sandbox.Engine { return nil }},
		{name: "disabled user policy", user: func(workDir string) *sandbox.Engine {
			return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.Policy{Mode: sandbox.ModeDisabled}, Backend: fakeNativeBackend()})
		}},
		{name: "enforcing user policy", user: func(workDir string) *sandbox.Engine {
			return sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workDir, Policy: sandbox.DefaultPolicy(), Backend: fakeNativeBackend()})
		}},
	}
	commands := []string{"go test ./...", "curl https://example.com"}
	modes := []agent.PermissionMode{agent.PermissionModeAsk, agent.PermissionModeAuto, agent.PermissionModeUnsafe}
	for _, engine := range engines {
		for _, mode := range modes {
			for _, command := range commands {
				t.Run(engine.name+"/"+string(mode)+"/"+command, func(t *testing.T) {
					workDir := t.TempDir()
					bash, recordedCalls := newRecordingBashTool()
					registry := tools.NewRegistry()
					registry.Register(bash)
					prompts := 0
					config := PipelineConfigFromAgentOptions(agent.Options{
						Cwd:              workDir,
						Registry:         registry,
						PermissionMode:   mode,
						TrustedWorkspace: true,
						Sandbox:          engine.user(workDir),
						OnPermissionRequest: func(context.Context, agent.PermissionRequest) (agent.PermissionDecision, error) {
							prompts++
							return agent.PermissionDecision{Action: agent.PermissionDecisionAllow}, nil
						},
					})
					args := map[string]any{"command": command}

					// Before: the stage call went through the user-engine runner.
					if _, err := newAgentToolRunner(config, workDir).RunTool(context.Background(), "bash", args); err != nil {
						t.Fatalf("user runner: %v", err)
					}
					before := prompts

					prompts = 0
					options, _ := buildStageToolRunner(config, workDir)
					res, err := options.stageToolRunner.RunTool(context.Background(), "bash", args)
					if err != nil {
						t.Fatalf("stage runner: %v", err)
					}
					if prompts != before {
						t.Fatalf("stage call prompted %d time(s), the user runner prompted %d", prompts, before)
					}
					calls := recordedCalls()
					if mode == agent.PermissionModeUnsafe && strings.HasPrefix(command, "curl") {
						// Unsafe mode never prompts, and the stage policy denies
						// network, so a network command is refused, not run.
						if res.OK || !strings.Contains(res.Output, "network") || (len(calls) > 0 && calls[len(calls)-1].engine == options.StageSandbox) {
							t.Fatalf("unsafe network stage call OK=%v output=%q; want a network refusal", res.OK, res.Output)
						}
						return
					}
					if !res.OK || len(calls) != 2 || calls[1].engine != options.StageSandbox {
						t.Fatalf("stage call OK=%v output=%q calls=%d; want one run under the stage engine", res.OK, res.Output, len(calls))
					}
				})
			}
		}
	}
}
