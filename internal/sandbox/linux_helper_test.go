package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestBuildLinuxSandboxCommandArgsSerializesPermissionProfile(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemRestricted,
			ReadRoots: []string{"/workspace"},
			WriteRoots: []WritableRoot{{
				Root:                   "/workspace",
				ProtectedMetadataNames: []string{".git", ".splice"},
			}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		CommandCWD:        "/workspace/app",
		PermissionProfile: profile,
		UseLandlock:       true,
		BlockUnixSockets:  true,
		Command:           []string{"/bin/sh", "-c", "pwd"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}

	wantPrefix := []string{"--sandbox-policy-cwd", "/workspace", "--command-cwd", "/workspace/app", "--permission-profile"}
	if len(args) < len(wantPrefix)+1 || !reflect.DeepEqual(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("args prefix = %#v, want %#v", args, wantPrefix)
	}
	var gotProfile PermissionProfile
	if err := json.Unmarshal([]byte(args[len(wantPrefix)]), &gotProfile); err != nil {
		t.Fatalf("permission profile JSON: %v", err)
	}
	if !reflect.DeepEqual(gotProfile, profile) {
		t.Fatalf("permission profile = %#v, want %#v", gotProfile, profile)
	}
	separator := indexString(args, "--")
	if separator < 0 {
		t.Fatalf("args missing command separator: %#v", args)
	}
	if !reflect.DeepEqual(args[separator+1:], []string{"/bin/sh", "-c", "pwd"}) {
		t.Fatalf("command args = %#v", args[separator+1:])
	}
	if !stringSliceContains(args, "--use-landlock") || !stringSliceContains(args, "--block-unix-sockets") {
		t.Fatalf("args missing helper feature flags: %#v", args)
	}
}

func TestParseLinuxSandboxHelperArgs(t *testing.T) {
	profile := DefaultPermissionProfile("/workspace")
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:     "/workspace",
		PermissionProfile:    profile,
		ApplySeccompThenExec: true,
		BlockUnixSockets:     true,
		NoProc:               true,
		Command:              []string{"true"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	config, err := ParseLinuxSandboxHelperArgs(args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	if config.SandboxPolicyCWD != "/workspace" || config.CommandCWD != "/workspace" {
		t.Fatalf("cwd config = %#v", config)
	}
	if !config.ApplySeccompThenExec || !config.BlockUnixSockets || !config.NoProc {
		t.Fatalf("feature config = %#v", config)
	}
	if !reflect.DeepEqual(config.PermissionProfile, profile) || !reflect.DeepEqual(config.Command, []string{"true"}) {
		t.Fatalf("parsed config = %#v", config)
	}
}

func TestBuildLinuxSandboxBwrapArgsWrapsInnerSeccompStage(t *testing.T) {
	helperPath := filepath.Join(t.TempDir(), LinuxSandboxHelperName)
	if err := os.WriteFile(helperPath, []byte("helper"), 0o755); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		PermissionProfile: DefaultPermissionProfile("/workspace"),
		BlockUnixSockets:  true,
		Command:           []string{"true"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	config, err := ParseLinuxSandboxHelperArgs(args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	bwrapArgs, err := BuildLinuxSandboxBwrapArgs(LinuxSandboxBwrapOptions{
		Config:     config,
		HelperPath: helperPath,
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxBwrapArgs: %v", err)
	}
	for _, want := range [][]string{
		{"--new-session"},
		{"--die-with-parent"},
		{"--unshare-user"},
		{"--unshare-pid"},
		{"--unshare-net"},
		{"--ro-bind", "/", "/"},
		{"--chdir", "/workspace"},
		{"--setenv", EnvSandboxBackend, string(BackendLinuxBwrap)},
		{"--ro-bind", helperPath, helperPath},
		{"--", helperPath},
		{"--apply-seccomp-then-exec"},
		{"--block-unix-sockets"},
		{"--", "true"},
	} {
		assertArgsContainSequence(t, bwrapArgs, want...)
	}
	if argsContainSequence(bwrapArgs, "--tmpfs", "/") {
		t.Fatalf("default workspace-write profile must not start from an empty root: %#v", bwrapArgs)
	}
	if argsContainSequence(bwrapArgs, "--tmpfs", "/tmp") {
		t.Fatalf("default workspace-write profile must not replace host /tmp: %#v", bwrapArgs)
	}
	if stringSliceContains(bwrapArgs, "--clearenv") {
		t.Fatalf("Linux bwrap args must preserve caller environment like upstream: %#v", bwrapArgs)
	}
	for _, unwanted := range []string{"--unshare-ipc", "--unshare-uts"} {
		if stringSliceContains(bwrapArgs, unwanted) {
			t.Fatalf("Linux bwrap args should match upstream namespace set; found %s in %#v", unwanted, bwrapArgs)
		}
	}
}

func TestBuildLinuxSandboxBwrapArgsKeepsHostNetworkWhenAllowed(t *testing.T) {
	helperPath := filepath.Join(t.TempDir(), LinuxSandboxHelperName)
	if err := os.WriteFile(helperPath, []byte("helper"), 0o755); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	profile := DefaultPermissionProfile("/workspace")
	profile.Network = NetworkPolicy{Mode: NetworkAllow}
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		PermissionProfile: profile,
		BlockUnixSockets:  true,
		Command:           []string{"python3", "-m", "http.server", "8000"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	config, err := ParseLinuxSandboxHelperArgs(args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	bwrapArgs, err := BuildLinuxSandboxBwrapArgs(LinuxSandboxBwrapOptions{
		Config:     config,
		HelperPath: helperPath,
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxBwrapArgs: %v", err)
	}
	if indexString(bwrapArgs, "--unshare-net") >= 0 {
		t.Fatalf("network-allowed bwrap args must not isolate loopback: %#v", bwrapArgs)
	}
	assertArgsContainSequence(t, bwrapArgs, "--setenv", "SPLICE_SANDBOX_NETWORK", string(NetworkAllow))
}

func TestLinuxBwrapRootReadUsesReadOnlyHostRoot(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            []string{string(filepath.Separator)},
			WriteRoots:           []WritableRoot{{Root: "/workspace"}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkAllow},
	}

	args := linuxBwrapFilesystemArgs(profile)
	assertArgsContainSequence(t, args, "--ro-bind", "/", "/")
	if argsContainSequence(args, "--tmpfs", "/") {
		t.Fatalf("root-read profile must not start from an empty root: %#v", args)
	}
}

func TestLinuxBwrapTempUsesHostWriteRoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux bwrap temp root assertions use Unix paths")
	}
	tmpdir := t.TempDir()
	t.Setenv("TMPDIR", tmpdir)
	workspace := filepath.Join(tmpdir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:       FileSystemRestricted,
			ReadRoots:  []string{string(filepath.Separator)},
			WriteRoots: []WritableRoot{{Root: workspace, ProtectedMetadataNames: []string{".git"}}},
			AllowTemp:  true,
		},
		Network: NetworkPolicy{Mode: NetworkAllow},
	}

	args := linuxBwrapFilesystemArgs(profile)
	if argsContainSequence(args, "--tmpfs", "/tmp") {
		t.Fatalf("workspace-write temp access must bind host /tmp, not create private tmpfs: %#v", args)
	}
	for _, tempRoot := range defaultTempWriteRoots() {
		if pathExists(tempRoot) {
			assertArgsContainSequence(t, args, "--bind", tempRoot, tempRoot)
		}
	}
	assertArgsContainSequence(t, args, "--bind", workspace, workspace)

	if runtime.GOOS == "linux" {
		tmpdirBind := argsSequenceIndex(args, "--bind", tmpdir, tmpdir)
		workspaceBind := argsSequenceIndex(args, "--bind", workspace, workspace)
		if tmpdirBind < 0 || workspaceBind < 0 || tmpdirBind > workspaceBind {
			t.Fatalf("broader temp root must be bound before nested workspace root; args=%#v", args)
		}
	}
}

func TestLinuxBwrapUnrestrictedFilesystemUsesWritableHostRoot(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemUnrestricted,
			AllowTemp: true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}

	args := linuxBwrapFilesystemArgs(profile)
	assertArgsContainSequence(t, args, "--bind", "/", "/")
	if argsContainSequence(args, "--ro-bind", "/", "/") {
		t.Fatalf("unrestricted filesystem profile must not make host root read-only: %#v", args)
	}
	if argsContainSequence(args, "--tmpfs", "/tmp") {
		t.Fatalf("unrestricted filesystem profile must not replace host /tmp: %#v", args)
	}
	if argsContainSequence(args, "--dev", "/dev") {
		t.Fatalf("unrestricted filesystem profile must not replace host /dev: %#v", args)
	}
}

func TestLinuxHelperSandboxEnvironmentPreservesCallerEnv(t *testing.T) {
	env := linuxHelperSandboxEnvironment(
		PermissionProfile{Network: NetworkPolicy{Mode: NetworkDeny}},
		[]string{
			"PATH=/custom/bin",
			"HOME=/home/user",
			EnvSandboxed + "=0",
			EnvSandboxBackend + "=other",
		},
	)

	for _, want := range []string{
		"PATH=/custom/bin",
		"HOME=/home/user",
		EnvSandboxed + "=1",
		EnvSandboxBackend + "=" + string(BackendLinuxBwrap),
		"SPLICE_SANDBOX_NETWORK=deny",
	} {
		if !stringSliceContains(env, want) {
			t.Fatalf("linux helper env = %#v, missing %q", env, want)
		}
	}
	if stringSliceContains(env, EnvSandboxed+"=0") || stringSliceContains(env, EnvSandboxBackend+"=other") {
		t.Fatalf("linux helper env did not replace stale sandbox markers: %#v", env)
	}
}

func indexString(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}

func argsContainSequence(args []string, sequence ...string) bool {
	return argsSequenceIndex(args, sequence...) >= 0
}

func argsSequenceIndex(args []string, sequence ...string) int {
	if len(sequence) == 0 {
		return 0
	}
	for index := 0; index <= len(args)-len(sequence); index++ {
		matched := true
		for offset, want := range sequence {
			if args[index+offset] != want {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return -1
}

// TestLinuxBwrapMissingMaskedPathIsSkipped is the T1 pin: a masked path that
// does not exist must not produce a mountpoint-creating argument.
func TestLinuxBwrapMissingMaskedPathIsSkipped(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing-dir")

	var args []string
	args = appendReadOnlyLinuxPathArgs(args, missing)
	if argsContainSequence(args, "--tmpfs", missing) || argsContainSequence(args, "--ro-bind", missing, missing) {
		t.Fatalf("missing path must not produce a mask argument: %#v", args)
	}

	args = nil
	args = appendUnreadableLinuxPathArgs(args, missing)
	if argsContainSequence(args, "--tmpfs", missing) || argsContainSequence(args, "--ro-bind", "/dev/null", missing) {
		t.Fatalf("missing path must not produce a mask argument: %#v", args)
	}

	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            []string{string(filepath.Separator)},
			WriteRoots:           []WritableRoot{{Root: workspace, ProtectedMetadataNames: []string{".git", ".splice", ".agents"}}},
			DenyRead:             []string{filepath.Join(root, "deny-missing")},
			DenyWrite:            []string{filepath.Join(root, "ro-missing")},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	args = linuxBwrapFilesystemArgs(profile)
	for _, p := range []string{
		filepath.Join(workspace, ".git"),
		filepath.Join(workspace, ".splice"),
		filepath.Join(workspace, ".agents"),
		filepath.Join(root, "deny-missing"),
		filepath.Join(root, "ro-missing"),
	} {
		if argsContainSequence(args, "--tmpfs", p) || argsContainSequence(args, "--ro-bind", p, p) {
			t.Fatalf("missing path %s must not be masked: %#v", p, args)
		}
	}
}

// TestLinuxBwrapExistingMaskedPathStillMasked is the T2 pin: an existing
// masked path is still masked.
func TestLinuxBwrapExistingMaskedPathStillMasked(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing-dir")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatalf("mkdir existing: %v", err)
	}
	var args []string
	args = appendReadOnlyLinuxPathArgs(args, existing)
	if !argsContainSequence(args, "--ro-bind", existing, existing) {
		t.Fatalf("existing path must be masked with --ro-bind: %#v", args)
	}

	existingFile := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(existingFile, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	args = nil
	args = appendUnreadableLinuxPathArgs(args, existingFile)
	if !argsContainSequence(args, "--ro-bind", "/dev/null", existingFile) {
		t.Fatalf("existing file must be masked with /dev/null: %#v", args)
	}

	args = nil
	args = appendUnreadableLinuxPathArgs(args, existing)
	if !argsContainSequence(args, "--perms", "000", "--tmpfs", existing, "--remount-ro", existing) {
		t.Fatalf("existing directory must be masked with 000 tmpfs: %#v", args)
	}
}

// TestLinuxBwrapNetworkDenyProducesUnshareNet is the T5 pin: NetworkDeny
// still produces --unshare-net in the bwrap args.
func TestLinuxBwrapNetworkDenyProducesUnshareNet(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            []string{string(filepath.Separator)},
			WriteRoots:           []WritableRoot{{Root: "/workspace"}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	helperPath := filepath.Join(t.TempDir(), LinuxSandboxHelperName)
	if err := os.WriteFile(helperPath, []byte("helper"), 0o755); err != nil {
		t.Fatalf("write helper stub: %v", err)
	}
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		PermissionProfile: profile,
		Command:           []string{"true"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	config, err := ParseLinuxSandboxHelperArgs(args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	bwrapArgs, err := BuildLinuxSandboxBwrapArgs(LinuxSandboxBwrapOptions{
		Config:     config,
		HelperPath: helperPath,
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxBwrapArgs: %v", err)
	}
	if !argsContainSequence(bwrapArgs, "--unshare-net") {
		t.Fatalf("NetworkDeny must produce --unshare-net: %#v", bwrapArgs)
	}
}

// requireLinuxBwrap builds the standalone helper and returns an engine that
// uses it. It skips when Linux, bwrap, go, or the repo root are unavailable.
func requireLinuxBwrap(t *testing.T) (*Engine, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only integration test")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skipf("bwrap unavailable: %v", err)
	}
	root := linuxSandboxRepoRoot()
	if root == "" {
		t.Skip("splice repo root not found")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go unavailable to build the helper: %v", err)
	}
	binDir := t.TempDir()
	helperPath := filepath.Join(binDir, LinuxSandboxHelperName)
	build := exec.Command("go", "build", "-o", helperPath, "./cmd/"+LinuxSandboxHelperName)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("build splice-linux-sandbox: %v\n%s", err, out)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	backend := SelectBackend(BackendOptions{})
	if !backend.Available || backend.Name != BackendLinuxBwrap {
		t.Skipf("host backend is not linux-bwrap after helper build: %s", backend.Message)
	}
	workspace := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy: Policy{
			Mode:             ModeEnforce,
			Network:          NetworkDeny,
			EnforceWorkspace: true,
		},
		Backend: backend,
	})
	return engine, workspace
}

// TestLinuxBwrapWrappedCommandCompletes is the T3 integration pin: a real
// wrapped command through the Linux path returns its own exit status, not 129.
func TestLinuxBwrapWrappedCommandCompletes(t *testing.T) {
	engine, ws := requireLinuxBwrap(t)

	t.Run("exit zero", func(t *testing.T) {
		cmd, plan, err := engine.CommandContext(context.Background(), CommandSpec{Name: "true", Dir: ws})
		if err != nil {
			t.Fatalf("CommandContext: %v", err)
		}
		defer plan.Cleanup()
		if err := cmd.Run(); err != nil {
			t.Fatalf("true should exit 0: %v", err)
		}
	})

	t.Run("exit non-zero not 129", func(t *testing.T) {
		cmd, plan, err := engine.CommandContext(context.Background(), CommandSpec{Name: "false", Dir: ws})
		if err != nil {
			t.Fatalf("CommandContext: %v", err)
		}
		defer plan.Cleanup()
		err = cmd.Run()
		if err == nil {
			t.Fatal("false should exit non-zero")
		}
		if ee, ok := err.(*exec.ExitError); ok {
			if ee.ExitCode() == 129 {
				t.Fatal("command must not exit 129; the empty-.git synthesis is fixed")
			}
		}
	})

	t.Run("git diff in non-repo exits 129 (git usage exit)", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
		cmd, plan, err := engine.CommandContext(context.Background(), CommandSpec{Name: "git", Args: []string{"diff", "--name-only", "HEAD"}, Dir: ws})
		if err != nil {
			t.Fatalf("CommandContext: %v", err)
		}
		defer plan.Cleanup()
		// git diff --name-only HEAD in a non-repository exits 129 (git's
		// usage exit code). This is git's own behavior, not a sandbox
		// defect. The test confirms the exit code is propagated correctly
		// and is not a signal death.
		runErr := cmd.Run()
		if ee, ok := runErr.(*exec.ExitError); ok {
			// 129 is git's own usage exit; 128+1 is not a signal here.
			if ee.ExitCode() != 129 {
				t.Fatalf("git diff --name-only HEAD in a non-repo should exit 129 (git usage), got %d", ee.ExitCode())
			}
		} else {
			t.Fatal("git diff in a non-repo should exit non-zero")
		}
	})
}

// TestLinuxBwrapWorkspaceConfinement is the T4 integration pin: a write inside
// the workspace succeeds; a write outside is denied by the kernel.
func TestLinuxBwrapWorkspaceConfinement(t *testing.T) {
	engine, ws := requireLinuxBwrap(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	outside, err := os.MkdirTemp(home, ".splice-rse3-outside-")
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

	t.Run("InsideWorkspaceWriteSucceeds", func(t *testing.T) {
		target := filepath.Join(ws, "inside.txt")
		cmd, plan, err := engine.CommandContext(context.Background(), CommandSpec{Name: "/bin/sh", Args: []string{"-c", "echo ok > " + target}, Dir: ws})
		if err != nil {
			t.Fatalf("CommandContext: %v", err)
		}
		defer plan.Cleanup()
		if err := cmd.Run(); err != nil {
			t.Fatalf("workspace write failed: %v", err)
		}
		content, readErr := os.ReadFile(target)
		if readErr != nil || string(content) != "ok\n" {
			t.Fatalf("workspace file = %q (read err %v), want ok", content, readErr)
		}
	})

	t.Run("OutsideWorkspaceWriteIsDenied", func(t *testing.T) {
		target := filepath.Join(resolvedOutside, "denied.txt")
		cmd, plan, err := engine.CommandContext(context.Background(), CommandSpec{Name: "/bin/sh", Args: []string{"-c", "echo x > " + target}, Dir: ws})
		if err != nil {
			t.Fatalf("CommandContext: %v", err)
		}
		defer plan.Cleanup()
		runErr := cmd.Run()
		if runErr == nil {
			t.Fatalf("outside write succeeded, want sandbox denial")
		}
		if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
			t.Fatalf("Lstat(%s) = %v, want not-exist", target, statErr)
		}
	})
}
