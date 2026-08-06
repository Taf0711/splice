package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectBackendLinuxSelfDispatchWhenHelperIsMissing(t *testing.T) {
	// The Linux sandbox was inactive on every released install because the helper was resolved from $PATH alone.
	restore := osExecutable
	defer func() { osExecutable = restore }()
	self := filepath.Join(t.TempDir(), "splice")
	osExecutable = func() (string, error) { return self, nil }

	backend := selectPlatformBackend("linux", func(name string) (string, error) {
		if name == "bwrap" {
			return "/usr/bin/bwrap", nil
		}
		return "", errors.New("missing")
	})
	if backend.Name != BackendLinuxBwrap || !backend.Available {
		t.Fatalf("linux backend = %#v, want available Linux bwrap backend", backend)
	}
	if backend.Executable != self {
		t.Fatalf("self-dispatch executable = %q, want %q", backend.Executable, self)
	}
	if len(backend.ExecutableArgsPrefix) != 1 || backend.ExecutableArgsPrefix[0] != LinuxSandboxSubcommand {
		t.Fatalf("self-dispatch args prefix = %#v, want [%q]", backend.ExecutableArgsPrefix, LinuxSandboxSubcommand)
	}
}

func TestSelectBackendLinuxSelfDispatchStillRequiresBubblewrap(t *testing.T) {
	restore := osExecutable
	defer func() { osExecutable = restore }()
	osExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "splice"), nil }

	backend := selectPlatformBackend("linux", func(string) (string, error) {
		return "", errors.New("missing")
	})
	if backend.Name != BackendUnavailable || backend.Available {
		t.Fatalf("linux backend = %#v, want unavailable backend", backend)
	}
	if !strings.Contains(backend.Message, "bubblewrap") {
		t.Fatalf("linux unavailable message = %q, want bubblewrap", backend.Message)
	}
}

func TestSelectBackendLinuxAdjacentHelperWinsOverSelfDispatch(t *testing.T) {
	restore := osExecutable
	defer func() { osExecutable = restore }()
	dir := t.TempDir()
	self := filepath.Join(dir, "splice")
	adjacent := filepath.Join(dir, LinuxSandboxHelperName)
	if err := os.WriteFile(adjacent, []byte("helper"), 0o755); err != nil {
		t.Fatalf("write adjacent helper: %v", err)
	}
	osExecutable = func() (string, error) { return self, nil }

	backend := selectPlatformBackend("linux", func(name string) (string, error) {
		if name == "bwrap" {
			return "/usr/bin/bwrap", nil
		}
		return "", errors.New("missing")
	})
	if backend.Executable != adjacent || len(backend.ExecutableArgsPrefix) != 0 {
		t.Fatalf("linux adjacent helper = %#v, want adjacent helper without self-dispatch prefix", backend)
	}
}

func TestLinuxSelfDispatchPrefixReachesCommandPlan(t *testing.T) {
	restore := osExecutable
	defer func() { osExecutable = restore }()
	self := filepath.Join(t.TempDir(), "splice")
	osExecutable = func() (string, error) { return self, nil }
	backend := selectPlatformBackend("linux", func(name string) (string, error) {
		if name == "bwrap" {
			return "/usr/bin/bwrap", nil
		}
		return "", errors.New("missing")
	})

	root := t.TempDir()
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: backend})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: root})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if !plan.Wrapped || plan.Name != self {
		t.Fatalf("plan = %#v, want wrapped self-dispatch command", plan)
	}
	if len(plan.Args) == 0 || plan.Args[0] != LinuxSandboxSubcommand {
		t.Fatalf("plan arguments = %#v, want Linux self-dispatch token first", plan.Args)
	}
}

func TestFindLinuxSandboxHelperCommandUsesSelfDispatch(t *testing.T) {
	restore := osExecutable
	defer func() { osExecutable = restore }()
	osExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "splice"), nil }
	t.Setenv("PATH", t.TempDir())

	helper, err := findLinuxSandboxHelperCommand()
	if err != nil {
		t.Fatalf("findLinuxSandboxHelperCommand: %v", err)
	}
	if helper.Name == "" || len(helper.ArgsPrefix) != 1 || helper.ArgsPrefix[0] != LinuxSandboxSubcommand {
		t.Fatalf("resolved Linux helper = %#v, want self-dispatch prefix", helper)
	}
}
