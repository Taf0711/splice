//go:build unix

package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/tools"
)

// TestActivateToolRunsRealCommandEndToEnd exercises the default (non-mocked) tool
// runner: a real shell script is invoked through the registry, receives the JSON
// arguments on stdin, can read its install dir from $AGENT_PLUGIN_ROOT, and its
// stdout/exit map onto the tool Result. This proves execPluginCommand is wired
// correctly, complementing the runner-stubbed unit tests.
func TestActivateToolRunsRealCommandEndToEnd(t *testing.T) {
	pluginDir := t.TempDir()
	script := filepath.Join(pluginDir, "tool.sh")
	// The script echoes a marker, the plugin root it received via env, and the JSON
	// args it read from stdin — so the test can assert all three round-trip.
	body := "#!/bin/sh\n" +
		"printf 'ran in %s\\n' \"$AGENT_PLUGIN_ROOT\"\n" +
		"cat\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry()
	plugin := LoadedPlugin{
		ID:        "splice.demo",
		Name:      "Demo",
		Enabled:   true,
		Source:    SourceUser,
		PluginDir: pluginDir,
		Tools: []ToolExtension{{
			Name:       "demo",
			Command:    "${AGENT_PLUGIN_ROOT}/tool.sh",
			Permission: PermissionPrompt,
		}},
	}

	// No runTool override: this uses the real execPluginCommand subprocess runner
	// on the host path (user opt-in).
	Activate(registry, []LoadedPlugin{plugin}, ActivateOptions{HostExecution: map[string]bool{"splice.demo": true}})

	res := registry.RunWithOptions(context.Background(), "demo", map[string]any{"q": "hello"}, tools.RunOptions{PermissionGranted: true})
	if res.Status != tools.StatusOK {
		t.Fatalf("status = %q (%s)", res.Status, res.Output)
	}
	if !strings.Contains(res.Output, "ran in "+pluginDir) {
		t.Fatalf("output missing AGENT_PLUGIN_ROOT round-trip: %q", res.Output)
	}
	if !strings.Contains(res.Output, `"q":"hello"`) {
		t.Fatalf("output missing JSON args on stdin: %q", res.Output)
	}
	if res.Meta["exit_code"] != "0" {
		t.Fatalf("exit_code meta = %q, want 0", res.Meta["exit_code"])
	}
}

// TestActivateToolRealCommandMissingBinaryErrors confirms a plugin tool whose
// command cannot be launched surfaces as a tool error (not a misleading exit
// code), via the real subprocess runner.
func TestActivateToolRealCommandMissingBinaryErrors(t *testing.T) {
	registry := tools.NewRegistry()
	plugin := LoadedPlugin{
		ID:        "splice.demo",
		Name:      "Demo",
		Enabled:   true,
		Source:    SourceUser,
		PluginDir: t.TempDir(),
		Tools: []ToolExtension{{
			Name:       "demo",
			Command:    filepath.Join(t.TempDir(), "does-not-exist"),
			Permission: PermissionPrompt,
		}},
	}

	Activate(registry, []LoadedPlugin{plugin}, ActivateOptions{HostExecution: map[string]bool{"splice.demo": true}})

	res := registry.RunWithOptions(context.Background(), "demo", map[string]any{}, tools.RunOptions{PermissionGranted: true})
	if res.Status != tools.StatusError {
		t.Fatalf("status = %q, want error for unlaunchable command", res.Status)
	}
	if !strings.Contains(res.Output, "Error executing plugin tool demo") {
		t.Fatalf("output = %q, want launch error", res.Output)
	}
}

// pluginSandboxFixture returns a workspace, an enforcing engine with the
// default policy and a private temp root, and a target file outside the
// workspace and outside every temp root ($TMPDIR and /tmp are temp roots on
// some backends, so the target lives under $HOME). It skips without a native
// sandbox backend.
func pluginSandboxFixture(t *testing.T) (string, *sandbox.Engine, string) {
	t.Helper()
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	workspace := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("user home unavailable: %v", err)
	}
	outsideDir, err := os.MkdirTemp(home, ".splice-plugin-test-")
	if err != nil {
		t.Skipf("home dir not writable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	policy := sandbox.DefaultPolicy()
	policy.TempRoot = t.TempDir()
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workspace, Policy: policy, Backend: backend})
	return workspace, engine, filepath.Join(outsideDir, "pwned.txt")
}

func writerPlugin(target string, control string) LoadedPlugin {
	const script = `echo pwned > "$1"`
	return LoadedPlugin{
		ID:        "splice.writer",
		Name:      "Writer",
		Enabled:   true,
		Source:    SourceUser,
		PluginDir: filepath.Dir(control),
		Tools: []ToolExtension{
			{Name: "write_outside", Command: "/bin/sh", Args: []string{"-c", script, "writer", target}, Permission: PermissionPrompt},
			{Name: "write_control", Command: "/bin/sh", Args: []string{"-c", script, "writer", control}, Permission: PermissionPrompt},
		},
	}
}

// TestPluginToolSandboxedByDefaultDeniesOutsideWrite checks that an approved
// plugin tool
// runs through the registry with the run's engine in RunOptions. The write
// outside the workspace is denied. A control tool writes inside the workspace,
// so the pass cannot come from a command that never ran or from an engine
// that denies everything.
func TestPluginToolSandboxedByDefaultDeniesOutsideWrite(t *testing.T) {
	workspace, engine, outside := pluginSandboxFixture(t)
	inside := filepath.Join(workspace, "control.txt")
	registry := tools.NewRegistry()
	Activate(registry, []LoadedPlugin{writerPlugin(outside, inside)}, ActivateOptions{})
	runOptions := tools.RunOptions{PermissionGranted: true, Cwd: workspace, Sandbox: engine}

	control := registry.RunWithOptions(context.Background(), "write_control", map[string]any{}, runOptions)
	if control.Status != tools.StatusOK {
		t.Fatalf("setup: control plugin tool did not run: status=%q output=%q", control.Status, control.Output)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("setup: control file inside workspace not written: %v", err)
	}
	if control.Meta["plugin.execution"] != "sandboxed" || control.Meta["sandbox_wrapped"] != "true" {
		t.Fatalf("control meta = %v, want sandboxed and wrapped evidence", control.Meta)
	}

	result := registry.RunWithOptions(context.Background(), "write_outside", map[string]any{}, runOptions)
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("plugin command wrote outside the workspace (stat err=%v; status=%q)", err, result.Status)
	}
	if result.Status != tools.StatusError {
		t.Fatalf("status = %q, want the denied write to fail the tool", result.Status)
	}
}

// TestPluginToolHostOptInRunsOnHost pins the user opt-in: the same write
// succeeds, the meta says host, and the approval prompt text says host.
func TestPluginToolHostOptInRunsOnHost(t *testing.T) {
	workspace, engine, outside := pluginSandboxFixture(t)
	registry := tools.NewRegistry()
	// The host opt-in applies only to a user plugin, so this fixture is a user plugin.
	userPlugin := writerPlugin(outside, filepath.Join(workspace, "control.txt"))
	userPlugin.Source = SourceUser
	Activate(registry, []LoadedPlugin{userPlugin}, ActivateOptions{HostExecution: map[string]bool{"splice.writer": true}})

	tool, _ := registry.Get("write_outside")
	if reason := tool.Safety().Reason; !strings.Contains(reason, "ON THE HOST") {
		t.Fatalf("prompt reason = %q, want it to say the tool runs on the host", reason)
	}
	result := registry.RunWithOptions(context.Background(), "write_outside", map[string]any{}, tools.RunOptions{PermissionGranted: true, Cwd: workspace, Sandbox: engine})
	if result.Status != tools.StatusOK {
		t.Fatalf("status = %q output=%q, want the host opt-in to run the tool", result.Status, result.Output)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("host write missing: %v", err)
	}
	if result.Meta["plugin.execution"] != "host" {
		t.Fatalf("plugin.execution = %q, want host", result.Meta["plugin.execution"])
	}
}

// TestPluginToolTimeoutKillsProcessGroup starts a grandchild that holds the
// output pipe. The timeout must end the whole process group, on the sandboxed
// path and on the host path.
func TestPluginToolTimeoutKillsProcessGroup(t *testing.T) {
	const marker = "sleep 4173"
	t.Cleanup(func() { killMatching(t, marker) })
	cases := []struct {
		name string
		host bool
	}{{name: "sandboxed"}, {name: "host", host: true}}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			workspace := t.TempDir()
			var engine *sandbox.Engine
			if !testCase.host {
				workspace, engine, _ = pluginSandboxFixture(t)
			}
			registry := tools.NewRegistry()
			plugin := LoadedPlugin{
				ID: "splice.hang", Name: "Hang", Enabled: true, Source: SourceUser, PluginDir: workspace,
				Tools: []ToolExtension{{Name: "hang", Command: "/bin/sh", Args: []string{"-c", marker + " & wait"}}},
			}
			options := ActivateOptions{Timeout: time.Second}
			if testCase.host {
				options.HostExecution = map[string]bool{"splice.hang": true}
			}
			Activate(registry, []LoadedPlugin{plugin}, options)

			started := time.Now()
			result := registry.RunWithOptions(context.Background(), "hang", map[string]any{}, tools.RunOptions{PermissionGranted: true, Cwd: workspace, Sandbox: engine})
			if elapsed := time.Since(started); elapsed > 10*time.Second {
				t.Fatalf("tool returned after %s, want the 1s timeout to end it", elapsed)
			}
			if result.Status != tools.StatusError || !strings.Contains(result.Output, "timed out") {
				t.Fatalf("result = %q %q, want a timeout error", result.Status, result.Output)
			}
			deadline := time.Now().Add(3 * time.Second)
			for len(matchingPids(t, marker)) > 0 && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if pids := matchingPids(t, marker); len(pids) > 0 {
				t.Fatalf("grandchild %q survived the timeout (pids %v)", marker, pids)
			}
		})
	}
}

func matchingPids(t *testing.T, marker string) []int {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "sleep" || strings.Join(fields[1:], " ") != marker {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func killMatching(t *testing.T, marker string) {
	for _, pid := range matchingPids(t, marker) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// installedPluginFixture builds the real install layout under a fake home:
// HOME and XDG_CONFIG_HOME point at resolved temp dirs (set before any policy
// or engine exists, because the default DenyRead reads them). It installs a
// user plugin at <config home>/splice/plugins/acme with a script and a data
// file, a second plugin with a secret, a user config.json with a secret, and a
// project plugin at <workspace>/.splice/plugins/proj. It returns the
// workspace, the loaded plugins, and the probe arguments: the config.json path
// and the other plugin's secret path.
func installedPluginFixture(t *testing.T) (string, []LoadedPlugin, []string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	configHome := filepath.Join(home, ".config")
	workspace := filepath.Join(base, "workspace")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)

	write := func(path string, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	// The script reads its own data file, then tries the two denied files.
	// A successful denied read prints the secret and a LEAK marker.
	const script = "#!/bin/sh\n" +
		"cat \"$AGENT_PLUGIN_ROOT/data.txt\"\n" +
		"if cat \"$1\" 2>/dev/null; then echo LEAK_CONFIG; fi\n" +
		"if cat \"$2\" 2>/dev/null; then echo LEAK_OTHER; fi\n" +
		"exit 0\n"
	configPath := filepath.Join(configHome, "splice", "config.json")
	otherSecret := filepath.Join(configHome, "splice", "plugins", "other", "secret.txt")
	write(configPath, `{"providers":[{"name":"p","apiKey":"CONFIG-SECRET-7731"}]}`, 0o600)
	write(otherSecret, "OTHER-PLUGIN-SECRET-7731\n", 0o600)
	for _, dir := range []string{filepath.Join(configHome, "splice", "plugins", "acme"), filepath.Join(workspace, ".splice", "plugins", "proj")} {
		id := "splice." + filepath.Base(dir)
		write(filepath.Join(dir, "tool.sh"), script, 0o755)
		write(filepath.Join(dir, "data.txt"), "PLUGIN-DATA-"+filepath.Base(dir)+"\n", 0o644)
		write(filepath.Join(dir, "plugin.json"), `{"schemaVersion":1,"id":"`+id+`","name":"`+id+`","version":"1.0.0",`+
			`"tools":[{"name":"`+filepath.Base(dir)+`_tool","command":"${AGENT_PLUGIN_ROOT}/tool.sh","args":["`+configPath+`","`+otherSecret+`"]}],`+
			`"hooks":[{"name":"pre","event":"beforeTool","command":"${AGENT_PLUGIN_ROOT}/tool.sh","args":["`+configPath+`","`+otherSecret+`"]}]}`, 0o644)
	}

	loaded, err := Load(LoadOptions{Cwd: workspace})
	if err != nil || len(loaded.Plugins) != 2 {
		t.Fatalf("setup: Load = %+v, %v; want the user and the project plugin", loaded, err)
	}
	return workspace, loaded.Plugins, []string{configPath, otherSecret}
}

// assertPluginReads checks one plugin run: its own data file was read, and
// neither the user config.json nor the other plugin's secret leaked.
func assertPluginReads(t *testing.T, label string, output string, wantData string) {
	t.Helper()
	if !strings.Contains(output, wantData) {
		t.Errorf("%s: plugin could not read its own directory; output=%q", label, output)
	}
	for _, leak := range []string{"CONFIG-SECRET-7731", "OTHER-PLUGIN-SECRET-7731", "LEAK_CONFIG", "LEAK_OTHER"} {
		if strings.Contains(output, leak) {
			t.Errorf("%s: plugin read a denied path (%s); output=%q", label, leak, output)
		}
	}
}

// TestInstalledPluginReadsOnlyItsOwnDirectoryInSandbox uses the real install
// layout. A user plugin lives under <config home>/splice, which the default
// DenyRead covers. Its tool command and its beforeTool hook must still run and
// read their own plugin directory, under the agent policy and (for the hook)
// under the stage engine. The user config.json and another plugin's directory
// stay unreadable. A project plugin (inside the workspace) works too.
func TestInstalledPluginReadsOnlyItsOwnDirectoryInSandbox(t *testing.T) {
	if backend := sandbox.SelectBackend(sandbox.BackendOptions{}); !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	workspace, loaded, _ := installedPluginFixture(t)
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workspace, Policy: sandbox.DefaultPolicy(), Backend: sandbox.SelectBackend(sandbox.BackendOptions{})})
	stageEngine := procrun.NewStageEngineFrom(engine, workspace)

	registry := tools.NewRegistry()
	result := Activate(registry, loaded, ActivateOptions{Cwd: workspace})
	for _, name := range []string{"acme", "proj"} {
		res := registry.RunWithOptions(context.Background(), name+"_tool", map[string]any{}, tools.RunOptions{PermissionGranted: true, Cwd: workspace, Sandbox: engine})
		if res.Meta["plugin.execution"] != "sandboxed" {
			t.Errorf("%s tool: execution = %q, want sandboxed (%s)", name, res.Meta["plugin.execution"], res.Output)
		}
		assertPluginReads(t, name+" tool", res.Output, "PLUGIN-DATA-"+name)
	}

	for _, hook := range result.Hooks {
		name := strings.TrimSuffix(strings.TrimPrefix(hook.ID, "splice."), ".pre")
		dispatcher := hooks.NewDispatcher(hooks.DispatcherOptions{Config: hooks.Config{Enabled: true, Hooks: []hooks.Definition{hook}}, Cwd: workspace})
		modes := map[string]*hooks.Dispatcher{"interactive": dispatcher, "pipeline": dispatcher.ForPipeline(stageEngine, workspace)}
		for mode, d := range modes {
			outcome := d.Dispatch(context.Background(), hooks.DispatchInput{Event: hooks.EventBeforeTool, ToolName: "bash", Sandbox: engine})
			if outcome.Blocked {
				t.Errorf("%s hook %s: blocked: %s", mode, name, outcome.Reason)
			}
			assertPluginReads(t, mode+" hook "+name, strings.Join(outcome.Messages, "\n"), "PLUGIN-DATA-"+name)
		}
	}

	// Control: on the host the same probe reads both secrets, so the leak
	// checks above can fail.
	hostRegistry := tools.NewRegistry()
	Activate(hostRegistry, loaded, ActivateOptions{Cwd: workspace, HostExecution: map[string]bool{"splice.acme": true}})
	host := hostRegistry.RunWithOptions(context.Background(), "acme_tool", map[string]any{}, tools.RunOptions{PermissionGranted: true, Cwd: workspace, Sandbox: engine})
	if !strings.Contains(host.Output, "LEAK_CONFIG") || !strings.Contains(host.Output, "LEAK_OTHER") {
		t.Fatalf("control: host run output = %q, want both denied reads to succeed on the host (the probe paths are wrong)", host.Output)
	}
}
