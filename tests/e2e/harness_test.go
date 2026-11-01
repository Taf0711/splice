package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
)

const (
	e2eEnv         = "SPLICE_E2E"
	defaultTimeout = 120 * time.Second
	versionLDFlag  = "-X github.com/Taf0711/splice/internal/version.Version=0.3.0"
)

// envAllowlist names the parent variables a child may inherit (spec 2.3).
var envAllowlist = []string{"PATH", "TMPDIR", "LANG", "TERM", "GOCACHE", "GOPATH", "GOFLAGS"}

// guardedConfigFiles are the real user files the suite must never change.
var guardedConfigFiles = []string{"config.json", "trust.json", "stage-models.json", "credentials.enc"}

// guardedConfigDirs are the real user directories the suite must never
// change. The guard hashes every file under them.
var guardedConfigDirs = []string{"pipelines"}

// nativeSandboxTools names the executable that the native stage sandbox
// needs on each platform.
var nativeSandboxTools = map[string]string{"darwin": "sandbox-exec", "linux": "bwrap"}

var (
	binaryPath string            // the built splice binary
	goEnv      map[string]string // suite GOCACHE and GOPATH
	realConfig string            // the developer's real <config>/splice dir
	realHashes map[string]string // guarded file -> hash at suite start
)

func TestMain(m *testing.M) {
	if os.Getenv(e2eEnv) != "1" {
		os.Exit(m.Run())
	}
	os.Exit(runSuite(m))
}

func runSuite(m *testing.M) int {
	// Compute the real config dir from the PARENT env before any child env
	// exists. The test process env is never changed by the harness.
	dir, err := config.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: resolve the real config dir:", err)
		return 1
	}
	realConfig = filepath.Join(dir, "splice")
	realHashes = hashGuarded(realConfig)

	goEnv, err = readGoEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	// The native sandbox allows writes only under /tmp and TMPDIR. A host
	// GOCACHE outside them (for example ~/.cache/go-build on Linux) makes the
	// pipeline's go build fail as read-only. One suite-owned cache under
	// TMPDIR serves every scenario and is removed when the suite ends.
	goCache, err := os.MkdirTemp("", "splice-e2e-gocache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	defer os.RemoveAll(goCache)
	goEnv["GOCACHE"] = goCache
	buildDir, err := os.MkdirTemp("", "splice-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	defer os.RemoveAll(buildDir)
	binaryPath = filepath.Join(buildDir, "splice")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	build := exec.Command("go", "build", "-ldflags", versionLDFlag, "-o", binaryPath, "github.com/Taf0711/splice/cmd/splice")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build splice:", err)
		return 1
	}

	// A -timeout panic stops the process before this check. NewScenario
	// also checks the guard in each scenario's t.Cleanup, so a change made
	// by a scenario that finished is reported before the panic.
	code := m.Run()
	if changed := changedGuarded(realConfig, realHashes); len(changed) > 0 {
		fmt.Fprintf(os.Stderr, "e2e: FAIL: the suite changed the real user config: %s\n", strings.Join(changed, ", "))
		return 1
	}
	return code
}

// requireE2E skips the test unless SPLICE_E2E=1. Every test calls it. The
// suite uses symlinks, sh, and /dev/zero, so it also skips on Windows.
func requireE2E(t testing.TB) {
	t.Helper()
	if os.Getenv(e2eEnv) != "1" {
		t.Skip("set SPLICE_E2E=1 to run the binary e2e suite (make e2e)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the binary e2e suite does not run on windows (it uses symlinks, sh, and /dev/zero)")
	}
}

// requireNativeSandbox stops a scenario when the native stage sandbox is not
// available, because every exec stage runs in it. On Linux CI (CI is set) the
// test fails, because CI must run the suite. Elsewhere it skips with the cause.
func requireNativeSandbox(t testing.TB) {
	t.Helper()
	missing := nativeSandboxMissing(runtime.GOOS, exec.LookPath)
	if missing == "" {
		return
	}
	if runtime.GOOS == "linux" && os.Getenv("CI") != "" {
		t.Fatalf("native stage sandbox unavailable on Linux CI: %s", missing)
	}
	t.Skipf("native stage sandbox unavailable, so the scenario cannot run: %s", missing)
}

// nativeSandboxMissing returns why the native stage sandbox is not available
// on goos, or "" when its executable is on PATH.
func nativeSandboxMissing(goos string, lookPath func(string) (string, error)) string {
	tool, ok := nativeSandboxTools[goos]
	if !ok {
		return "no native stage sandbox on " + goos
	}
	if _, err := lookPath(tool); err != nil {
		return tool + " not found on PATH: " + err.Error()
	}
	return ""
}

func readGoEnv() (map[string]string, error) {
	out, err := exec.Command("go", "env", "-json", "GOCACHE", "GOPATH").Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	env := map[string]string{}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	return env, nil
}

// hashGuarded hashes each guarded file and every file under each guarded
// directory, keyed by the path relative to dir. A missing guarded file is
// "missing"; a file under a guarded directory is keyed only when it exists.
func hashGuarded(dir string) map[string]string {
	out := map[string]string{}
	for _, name := range guardedConfigFiles {
		out[name] = hashGuardedFile(filepath.Join(dir, name))
	}
	for _, sub := range guardedConfigDirs {
		err := filepath.WalkDir(filepath.Join(dir, sub), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			out[rel] = hashGuardedFile(path)
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			out[sub] = "unreadable: " + err.Error()
		}
	}
	return out
}

func hashGuardedFile(path string) string {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "missing"
	case err != nil:
		return "unreadable: " + err.Error()
	default:
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
}

// changedGuarded returns each guarded path that was created, changed, or
// removed since before, in sorted order.
func changedGuarded(dir string, before map[string]string) []string {
	after := hashGuarded(dir)
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	var changed []string
	for name := range names {
		if before[name] != after[name] {
			changed = append(changed, filepath.Join(dir, name))
		}
	}
	sort.Strings(changed)
	return changed
}

// childEnv builds a child environment from the allowlist. lookup reads the
// parent env; fixed values win over the allowlist, and extra wins over both.
// An empty value is left out.
func childEnv(lookup func(string) (string, bool), fixed, extra map[string]string) []string {
	vals := map[string]string{}
	for _, name := range envAllowlist {
		if v, ok := lookup(name); ok {
			vals[name] = v
		}
	}
	for k, v := range fixed {
		vals[k] = v
	}
	for k, v := range extra {
		vals[k] = v
	}
	out := make([]string, 0, len(vals))
	for k, v := range vals {
		if v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// Scenario is one isolated e2e world: temp HOME and XDG dirs, a fake
// provider, a fixture workspace, and a seeded user config.
type Scenario struct {
	T          *testing.T
	Root       string
	Home       string
	ConfigHome string // XDG_CONFIG_HOME
	ConfigDir  string // ConfigHome/splice
	Workspace  string
	Provider   *FakeProvider
	Env        map[string]string // extra child env values
	Timeout    time.Duration
}

// NewScenario skips without SPLICE_E2E=1, then builds the scenario world.
func NewScenario(t *testing.T) *Scenario {
	t.Helper()
	requireE2E(t)
	// A short root keeps unix socket paths (memd, 103 bytes) in range.
	requireNativeSandbox(t)
	root, err := os.MkdirTemp("", "e2e-")
	if err != nil {
		t.Fatal(err)
	}
	// Check the real config after each scenario too, so a change is reported
	// even when a later -timeout panic skips the check in TestMain.
	t.Cleanup(func() {
		if changed := changedGuarded(realConfig, realHashes); len(changed) > 0 {
			t.Errorf("the suite changed the real user config: %s", strings.Join(changed, ", "))
		}
	})
	t.Cleanup(func() {
		if t.Failed() && os.Getenv("SPLICE_E2E_KEEP") == "1" {
			t.Logf("kept scenario root %s", root)
			return
		}
		_ = os.RemoveAll(root)
	})
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{
		T:          t,
		Root:       root,
		Home:       filepath.Join(root, "home"),
		ConfigHome: filepath.Join(root, "home", ".config"),
		Workspace:  filepath.Join(root, "ws"),
		Provider:   NewFakeProvider(t),
		Env:        map[string]string{FakeAPIKeyEnv: FakeAPIKey},
		Timeout:    defaultTimeout,
	}
	s.ConfigDir = filepath.Join(s.ConfigHome, "splice")
	for _, d := range []string{s.ConfigDir, filepath.Join(s.Home, ".local", "share"), filepath.Join(s.Home, ".local", "state"), filepath.Join(root, "markers"), s.Workspace} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if s.ConfigDir == realConfig {
		t.Fatalf("scenario config dir %s is the real config dir", s.ConfigDir)
	}
	writeFile(t, filepath.Join(s.Home, ".gitconfig"), "[user]\n\tname = splice e2e\n\temail = e2e@example.invalid\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n")
	s.WriteUserConfig(s.DefaultUserConfig())
	s.initFixture()
	return s
}

// ChildEnv returns the environment every child process of this scenario gets.
func (s *Scenario) ChildEnv() []string {
	fixed := map[string]string{
		"HOME":                        s.Home,
		"XDG_CONFIG_HOME":             s.ConfigHome,
		"XDG_DATA_HOME":               filepath.Join(s.Home, ".local", "share"),
		"XDG_STATE_HOME":              filepath.Join(s.Home, ".local", "state"),
		"SPLICE_CRED_STORAGE":         "encrypted-file",
		"SPLICE_OAUTH_STORAGE":        "encrypted-file",
		"SPLICE_DISABLE_MODELS_FETCH": "1",
		"SPLICE_UPDATE_RELEASE_URL":   s.Provider.URL + "/update-check",
		"GIT_CONFIG_NOSYSTEM":         "1",
		// The suite-owned build cache and the resolved GOPATH, so a temp HOME
		// does not move them and child builds stay warm.
		"GOCACHE": goEnv["GOCACHE"],
		"GOPATH":  goEnv["GOPATH"],
	}
	return childEnv(os.LookupEnv, fixed, s.Env)
}

// DefaultUserConfig is the seeded user config.json: one openai-compatible
// profile at the fake provider, its key read from FakeAPIKeyEnv.
func (s *Scenario) DefaultUserConfig() map[string]any {
	return map[string]any{
		"activeProvider": "fake",
		"providers": []any{map[string]any{
			"name":          "fake",
			"provider_kind": "openai-compatible",
			"baseURL":       s.Provider.BaseURL,
			"apiKeyEnv":     FakeAPIKeyEnv,
			"model":         FakeModel,
		}},
	}
}

// WriteUserConfig replaces the user config.json.
func (s *Scenario) WriteUserConfig(cfg map[string]any) {
	s.T.Helper()
	writeJSON(s.T, filepath.Join(s.ConfigDir, "config.json"), cfg)
}

// WriteStageModels writes the user stage-models.json.
func (s *Scenario) WriteStageModels(v any) {
	s.T.Helper()
	writeJSON(s.T, filepath.Join(s.ConfigDir, "stage-models.json"), v)
}

// WriteFile writes a file under the workspace.
func (s *Scenario) WriteFile(rel, content string) string {
	s.T.Helper()
	path := filepath.Join(s.Workspace, rel)
	writeFile(s.T, path, content)
	return path
}

// Git runs git in the workspace with the scenario env and fsmonitor off.
func (s *Scenario) Git(args ...string) string {
	s.T.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = s.Workspace
	cmd.Env = s.ChildEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.T.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// Marker returns an absolute marker path under the scenario root.
func (s *Scenario) Marker(name string) string { return filepath.Join(s.Root, "markers", name) }

// MarkerExists reports whether the marker file exists.
func (s *Scenario) MarkerExists(name string) bool {
	_, err := os.Stat(s.Marker(name))
	return err == nil
}

// initFixture writes a zero-dependency Go module and commits it before any
// scenario (or hostile) file exists.
func (s *Scenario) initFixture() {
	s.WriteFile("go.mod", "module example.com/e2e\n\ngo 1.22\n")
	s.WriteFile("main.go", "package main\n\nfunc main() {}\n")
	s.Git("init", "-q")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "initial")
}

// Result is the outcome of one binary run.
type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	Events   []map[string]any // stream-json events (lines that parse as JSON objects with a type)
}

// Run runs the binary in the workspace.
func (s *Scenario) Run(args ...string) Result {
	s.T.Helper()
	return s.RunIn(s.Workspace, args...)
}

// RunIn runs the binary in dir with the scenario env and an empty stdin.
func (s *Scenario) RunIn(dir string, args ...string) Result {
	s.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), s.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = dir
	cmd.Env = s.ChildEnv()
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := Result{Args: args, Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() != nil {
		s.T.Fatalf("splice %s: timed out after %s\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), s.Timeout, r.Stdout, r.Stderr)
	}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		r.ExitCode = exitErr.ExitCode()
	case err != nil:
		s.T.Fatalf("splice %s: %v", strings.Join(args, " "), err)
	}
	for _, line := range strings.Split(r.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) == nil && ev["type"] != nil {
			r.Events = append(r.Events, ev)
		}
	}
	return r
}

// WantExit fails the test with the full output when the exit code differs.
func (r Result) WantExit(t testing.TB, code int) {
	t.Helper()
	if r.ExitCode != code {
		t.Fatalf("splice %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(r.Args, " "), r.ExitCode, code, r.Stdout, r.Stderr)
	}
}

// EventsOfType returns the stream-json events of one type, in order.
func (r Result) EventsOfType(typ string) []map[string]any {
	var out []map[string]any
	for _, ev := range r.Events {
		if ev["type"] == typ {
			out = append(out, ev)
		}
	}
	return out
}

// Reasoning joins every reasoning delta (progress and notices).
func (r Result) Reasoning() string {
	var b strings.Builder
	for _, ev := range r.EventsOfType("reasoning") {
		if d, ok := ev["delta"].(string); ok {
			b.WriteString(d)
		}
	}
	return b.String()
}

// Final decodes final.text, the serialized pipeline result. It fails the
// test unless there is exactly one final event.
func (r Result) Final(t testing.TB) map[string]any {
	t.Helper()
	finals := r.EventsOfType("final")
	if len(finals) != 1 {
		t.Fatalf("want 1 final event, got %d\nstdout:\n%s\nstderr:\n%s", len(finals), r.Stdout, r.Stderr)
	}
	text, _ := finals[0]["text"].(string)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode final.text: %v\n%s", err, text)
	}
	return out
}

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t testing.TB, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(b)+"\n")
}

// TestChildEnvNeverLeaksTrustOrProvider pins spec 2.3: the child env comes
// from the allowlist, so trust and provider overrides in the developer
// shell never reach the binary.
func TestChildEnvNeverLeaksTrustOrProvider(t *testing.T) {
	requireE2E(t)
	t.Setenv("SPLICE_TRUST_WORKSPACE", "1")
	t.Setenv("SPLICE_PROVIDER", "evil")
	t.Setenv("OPENAI_API_KEY", "sk-real")
	s := &Scenario{T: t, Home: "/h", ConfigHome: "/h/.config", Provider: &FakeProvider{URL: "http://fake"}}
	env := s.ChildEnv()
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for _, leaked := range []string{"SPLICE_TRUST_WORKSPACE", "SPLICE_PROVIDER", "OPENAI_API_KEY"} {
		if _, ok := got[leaked]; ok {
			t.Errorf("child env leaks %s", leaked)
		}
	}
	for k, want := range map[string]string{
		"HOME": "/h", "XDG_CONFIG_HOME": "/h/.config",
		"SPLICE_CRED_STORAGE": "encrypted-file", "SPLICE_OAUTH_STORAGE": "encrypted-file",
		"SPLICE_DISABLE_MODELS_FETCH": "1", "SPLICE_UPDATE_RELEASE_URL": "http://fake/update-check",
	} {
		if got[k] != want {
			t.Errorf("child env %s = %q, want %q", k, got[k], want)
		}
	}
	allowed := map[string]bool{"HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true,
		"SPLICE_CRED_STORAGE": true, "SPLICE_OAUTH_STORAGE": true, "SPLICE_DISABLE_MODELS_FETCH": true,
		"SPLICE_UPDATE_RELEASE_URL": true, "GIT_CONFIG_NOSYSTEM": true}
	for _, k := range envAllowlist {
		allowed[k] = true
	}
	for k := range got {
		if !allowed[k] {
			t.Errorf("child env has %s, which is neither allowlisted nor fixed", k)
		}
	}
}

// TestRealConfigUntouched checks the real user config against the hashes
// taken at suite start. TestMain checks again after every test ran.
func TestRealConfigUntouched(t *testing.T) {
	requireE2E(t)
	if changed := changedGuarded(realConfig, realHashes); len(changed) > 0 {
		t.Fatalf("the suite changed the real user config: %s", strings.Join(changed, ", "))
	}
}

// TestConfigGuardDetectsChange pins the guard itself: a created, changed,
// or removed guarded file is reported, for a top-level file and for a file
// under a guarded directory.
func TestConfigGuardDetectsChange(t *testing.T) {
	requireE2E(t)
	for _, rel := range []string{"trust.json", "credentials.enc", filepath.Join("pipelines", "mine.json")} {
		t.Run(rel, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, rel)
			want := []string{path}
			before := hashGuarded(dir)
			if changed := changedGuarded(dir, before); len(changed) != 0 {
				t.Fatalf("no change, but guard reports %v", changed)
			}
			writeFile(t, path, "{}")
			if changed := changedGuarded(dir, before); !slices.Equal(changed, want) {
				t.Fatalf("created %s, guard reports %v", rel, changed)
			}
			before = hashGuarded(dir)
			writeFile(t, path, "{\"a\":1}")
			if changed := changedGuarded(dir, before); !slices.Equal(changed, want) {
				t.Fatalf("changed %s, guard reports %v", rel, changed)
			}
			before = hashGuarded(dir)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if changed := changedGuarded(dir, before); !slices.Equal(changed, want) {
				t.Fatalf("removed %s, guard reports %v", rel, changed)
			}
		})
	}
}

// TestNativeSandboxMissing pins the skip-or-fail input: the platform tool on
// PATH means available, and anything else names the cause.
func TestNativeSandboxMissing(t *testing.T) {
	requireE2E(t)
	found := func(string) (string, error) { return "/usr/bin/tool", nil }
	absent := func(name string) (string, error) { return "", errors.New("not found") }
	if got := nativeSandboxMissing("linux", found); got != "" {
		t.Fatalf("linux with bwrap: %q, want available", got)
	}
	if got := nativeSandboxMissing("linux", absent); !strings.Contains(got, "bwrap") {
		t.Fatalf("linux without bwrap: %q, want it to name bwrap", got)
	}
	if got := nativeSandboxMissing("darwin", absent); !strings.Contains(got, "sandbox-exec") {
		t.Fatalf("darwin without sandbox-exec: %q, want it to name sandbox-exec", got)
	}
	if got := nativeSandboxMissing("plan9", found); got == "" {
		t.Fatal("plan9: want unavailable")
	}
}
