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

func TestSandboxGoCachePathSharesRepositoryAcrossWorktrees(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "task")
	worktreeGitDir := filepath.Join(gitDir, "worktrees", "task")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeGitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+worktreeGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := sandboxGoCachePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := sandboxGoCachePath(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if base != linked {
		t.Fatalf("repository cache paths differ: base=%q worktree=%q", base, linked)
	}
}

func TestSandboxGoCachePathSeparatesRepositoriesAndSupportsNonGit(t *testing.T) {
	repoA := t.TempDir()
	repoB := t.TempDir()
	for _, repo := range []string{repoA, repoB} {
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cacheA, err := sandboxGoCachePath(repoA)
	if err != nil {
		t.Fatal(err)
	}
	cacheB, err := sandboxGoCachePath(repoB)
	if err != nil {
		t.Fatal(err)
	}
	if cacheA == cacheB {
		t.Fatalf("different repositories share cache path %q", cacheA)
	}
	if _, err := sandboxGoCachePath(t.TempDir()); err != nil {
		t.Fatalf("non-git workspace should still resolve cache path: %v", err)
	}
}

// TestWithSandboxGoCacheKeepsOnlyWritablePresetGOCACHE checks which caller
// GOCACHE values survive. A preset value stays only when the sandboxed process
// can write to it. Any other value is replaced by the sandbox cache directory.
func TestWithSandboxGoCacheKeepsOnlyWritablePresetGOCACHE(t *testing.T) {
	t.Setenv("GOCACHE", "")
	root := t.TempDir()
	cacheDir, err := sandboxGoCachePath(root)
	if err != nil {
		t.Fatalf("sandboxGoCachePath: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	outside := filepath.Join(home, "splice-gocache-outside-write-roots")
	inWorkspace := filepath.Join(root, "custom-cache")
	inTemp := filepath.Join(os.TempDir(), "splice-gocache-in-temp")

	tests := []struct {
		name      string
		env       []string
		wantCache bool   // true: expect the sandbox cache directory
		want      string // used when wantCache is false
	}{
		{name: "unset", env: []string{"PATH=/bin"}, wantCache: true},
		{name: "outside write roots", env: []string{"GOCACHE=" + outside}, wantCache: true},
		{name: "relative path", env: []string{"GOCACHE=relative-cache"}, wantCache: true},
		{name: "inside workspace", env: []string{"GOCACHE=" + inWorkspace}, want: inWorkspace},
		{name: "inside temp write root", env: []string{"GOCACHE=" + inTemp}, want: inTemp},
		{name: "off", env: []string{"GOCACHE=off"}, want: "off"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := PermissionProfileFromPolicy(root, DefaultPolicy(), nil)
			env, err := withSandboxGoCache(tt.env, DefaultPolicy(), BackendLinuxBwrap, root, profile)
			if err != nil {
				t.Fatalf("withSandboxGoCache: %v", err)
			}
			got := envListValue(env, "GOCACHE", "")
			want := tt.want
			if tt.wantCache {
				want = cacheDir
			}
			if got != want {
				t.Fatalf("GOCACHE = %q, want %q", got, want)
			}
		})
	}
}

// TestWithSandboxGoCacheRejectsUnwritablePresetGOCACHE covers preset values
// that look writable but are not: a symlink followed by "..", protected
// metadata names, DenyWrite entries, and a scope extra root that the profile
// grants. A kept value must be writable under the profile the command runs with.
func TestWithSandboxGoCacheRejectsUnwritablePresetGOCACHE(t *testing.T) {
	t.Setenv("GOCACHE", "")
	root := t.TempDir()
	cacheDir, err := sandboxGoCachePath(root)
	if err != nil {
		t.Fatalf("sandboxGoCachePath: %v", err)
	}
	// TempRoot turns off the system temp roots, so the outside dir is not writable.
	noTemp := DefaultPolicy()
	noTemp.TempRoot = t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Built as a raw string: filepath.Join would clean the ".." away first.
	dotdot := link + string(filepath.Separator) + ".." + string(filepath.Separator) + "x-cache"

	denyPolicy := noTemp
	denyPolicy.DenyWrite = []string{filepath.Join(root, "secret")}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	// Outside the temp roots, so only the scope can make it writable.
	scopeDir, err := os.MkdirTemp(home, "splice-gocache-scope-")
	if err != nil {
		t.Skipf("home directory not writable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scopeDir) })
	scope, err := NewScope(root, []string{scopeDir})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	tests := []struct {
		name     string
		profile  PermissionProfile
		value    string
		wantKept bool
	}{
		{"symlink then dot-dot", PermissionProfileFromPolicy(root, noTemp, nil), dotdot, false},
		{"protected metadata name", PermissionProfileFromPolicy(root, noTemp, nil), filepath.Join(root, ".splice", "gocache"), false},
		{"DenyWrite entry", PermissionProfileFromPolicy(root, denyPolicy, nil), filepath.Join(root, "secret", "cache"), false},
		{"scope extra root", PermissionProfileFromPolicy(root, DefaultPolicy(), scope), filepath.Join(scopeDir, "cache"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := withSandboxGoCache([]string{"GOCACHE=" + tt.value}, DefaultPolicy(), BackendLinuxBwrap, root, tt.profile)
			if err != nil {
				t.Fatalf("withSandboxGoCache: %v", err)
			}
			got := envListValue(env, "GOCACHE", "")
			want := cacheDir
			if tt.wantKept {
				want = tt.value
			}
			if got != want {
				t.Fatalf("GOCACHE = %q, want %q", got, want)
			}
		})
	}
}
