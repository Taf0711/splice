package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSandboxedCommandEnvironmentSetsWritablePersistentGOCACHE is the
// regression test for the write-through Go cache failure: a sandboxed `go test`
// passed, then the next `go` command failed on a missing cache entry because
// the default cache path was outside the write jail and its entries were never
// written. The cache must stay inside a write root and persist across plans.
func TestSandboxedCommandEnvironmentSetsWritablePersistentGOCACHE(t *testing.T) {
	t.Setenv("GOCACHE", "")
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendLinuxBwrap,
			Available:  true,
			Executable: "/usr/bin/splice-linux-sandbox",
			Platform:   "linux",
		},
	})

	first, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: root})
	if err != nil {
		t.Fatalf("first BuildCommandPlan: %v", err)
	}
	second, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: root})
	if err != nil {
		t.Fatalf("second BuildCommandPlan: %v", err)
	}

	cache := envListValue(first.Env, "GOCACHE", "")
	if cache == "" {
		t.Fatal("sandbox environment is missing GOCACHE")
	}
	if got := envListValue(second.Env, "GOCACHE", ""); got != cache {
		t.Fatalf("second sandbox GOCACHE = %q, want persistent path %q", got, cache)
	}
	insideWriteRoot := false
	for _, writeRoot := range defaultTempWriteRoots() {
		if pathWithinRoot(writeRoot, cache) {
			insideWriteRoot = true
			break
		}
	}
	if !insideWriteRoot {
		t.Fatalf("sandbox GOCACHE %q is outside write roots %#v", cache, defaultTempWriteRoots())
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("sandbox GOCACHE directory %q was not created: %v", cache, err)
	}
	if filepath.Clean(cache) == filepath.Clean(root) || strings.HasPrefix(filepath.Clean(cache), filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("sandbox GOCACHE %q must not pollute the workspace %q", cache, root)
	}
}

func TestSandboxedCommandEnvironmentHonorsExplicitGOCACHE(t *testing.T) {
	t.Setenv("GOCACHE", "")
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendLinuxBwrap,
			Available:  true,
			Executable: "/usr/bin/splice-linux-sandbox",
			Platform:   "linux",
		},
	})
	want := filepath.Join(root, "caller-cache")
	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "/bin/sh",
		Dir:  root,
		Env:  []string{"GOCACHE=" + want},
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if got := envListValue(plan.Env, "GOCACHE", ""); got != want {
		t.Fatalf("sandbox GOCACHE = %q, want explicit caller value %q", got, want)
	}
}

func TestUnsandboxedCommandEnvironmentDoesNotInjectGOCACHE(t *testing.T) {
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendUnavailable, Message: "native sandbox unavailable"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: root, Env: []string{"PATH=/bin"}})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if plan.Wrapped || plan.EnforcementLevel != EnforcementDegraded {
		t.Fatalf("plan = %#v, want a degraded direct command", plan)
	}
	if got := envListValue(plan.Env, "GOCACHE", ""); got != "" {
		t.Fatalf("degraded command unexpectedly received GOCACHE=%q", got)
	}
}

func TestSandboxGoCacheDirectoryCreationFailureIsClear(t *testing.T) {
	t.Setenv("GOCACHE", "")
	root := t.TempDir()
	cacheDir, err := sandboxGoCachePath(root)
	if err != nil {
		t.Fatalf("sandboxGoCachePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o700); err != nil {
		t.Fatalf("create cache parent: %v", err)
	}
	if err := os.WriteFile(cacheDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create cache conflict: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendLinuxBwrap,
			Available:  true,
			Executable: "/usr/bin/splice-linux-sandbox",
			Platform:   "linux",
		},
	})
	_, err = engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: root})
	if err == nil || !strings.Contains(err.Error(), "create sandbox Go cache directory") {
		t.Fatalf("cache creation error = %v, want a clear cache directory error", err)
	}
}
