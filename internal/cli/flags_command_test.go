package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/mcp"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/tui"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// flagsTestResolve runs the real config resolver with a fixed environment, so
// flag sources reach the command exactly as they do in production.
func flagsTestResolve(env map[string]string) func(string, config.Overrides) (config.ResolvedConfig, error) {
	return func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
		return config.Resolve(config.ResolveOptions{Env: env, Overrides: overrides})
	}
}

func flagsTestDeps(cwd string, env map[string]string) appDeps {
	return appDeps{
		getwd:         func() (string, error) { return cwd, nil },
		resolveConfig: flagsTestResolve(env),
	}
}

// flagRowFields returns the whitespace-separated fields of the table row for
// one flag, or nil when the flag has no row.
func flagRowFields(output, name string) []string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return fields
		}
	}
	return nil
}

func TestFlagsCommandTableListsEveryRegistryFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"flags"}, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{}))
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", exitCode, stderr.String())
	}
	for _, def := range flags.Registry {
		if flagRowFields(stdout.String(), string(def.Name)) == nil {
			t.Fatalf("table has no row for %s:\n%s", def.Name, stdout.String())
		}
	}
	if fields := flagRowFields(stdout.String(), string(flags.MemoryPrefetch)); len(fields) < 4 || fields[1] != "off" || fields[2] != "default" {
		t.Fatalf("memory.prefetch row = %v, want off from default", fields)
	}
	if !strings.Contains(stdout.String(), "experimental") {
		t.Fatalf("experimental stability is not shown:\n%s", stdout.String())
	}
}

func TestFlagsCommandJSONRoundTrips(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"flags", "--json"}, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{}))
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", exitCode, stderr.String())
	}
	var rows []flagJSON
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, stdout.String())
	}
	if len(rows) != len(flags.Registry) {
		t.Fatalf("got %d rows, want %d", len(rows), len(flags.Registry))
	}
	for i, def := range flags.Registry {
		row := rows[i]
		if row.Name != string(def.Name) || row.Default != def.Default || row.Value != def.Default {
			t.Fatalf("row %d = %+v, want default state of %s", i, row, def.Name)
		}
		if row.Stability != def.Stability.String() || row.Scope != def.Scope.String() || row.LegacyEnv != def.LegacyEnv || row.Description != def.Description {
			t.Fatalf("row %d metadata = %+v, want %+v", i, row, def)
		}
		if row.Source != "default" {
			t.Fatalf("row %d source = %q, want default", i, row.Source)
		}
	}
}

func TestFlagsCommandShowsEnvSourceAndLegacyVariable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := map[string]string{
		flags.EnvVar:             "-pipeline.stage.test_generator",
		"SPLICE_MEMORY_PREFETCH": "on",
	}
	exitCode := runWithDeps([]string{"flags"}, &stdout, &stderr, flagsTestDeps(t.TempDir(), env))
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if fields := flagRowFields(stdout.String(), string(flags.StageTestGenerator)); len(fields) < 3 || fields[1] != "off*" || fields[2] != "SPLICE_FLAGS" {
		t.Fatalf("test_generator row = %v, want off* from SPLICE_FLAGS:\n%s", fields, stdout.String())
	}
	if fields := flagRowFields(stdout.String(), string(flags.MemoryPrefetch)); len(fields) < 3 || fields[1] != "on*" || fields[2] != "SPLICE_MEMORY_PREFETCH" {
		t.Fatalf("memory.prefetch row = %v, want on* from SPLICE_MEMORY_PREFETCH:\n%s", fields, stdout.String())
	}
	if !strings.Contains(stdout.String(), "* differs from the default") {
		t.Fatalf("missing changed-value legend:\n%s", stdout.String())
	}
}

func TestFlagsCommandFailsNamingBadSource(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"flags"}, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{
		flags.EnvVar: "pipeline.no_such_flag",
	}))
	if exitCode != exitCrash {
		t.Fatalf("exit = %d, want %d", exitCode, exitCrash)
	}
	if !strings.Contains(stderr.String(), "SPLICE_FLAGS") || !strings.Contains(stderr.String(), "pipeline.no_such_flag") {
		t.Fatalf("stderr does not name the source and flag: %q", stderr.String())
	}
}

func TestRootFlagsReachNonExecCommandAndTUI(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	var seen map[string]bool
	deps := flagsTestDeps(cwd, map[string]string{})
	resolve := deps.resolveConfig
	deps.resolveConfig = func(root string, overrides config.Overrides) (config.ResolvedConfig, error) {
		seen = overrides.Flags
		return resolve(root, overrides)
	}
	exitCode := runWithDeps([]string{"--flags", "memory.prefetch", "flags"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", exitCode, stderr.String())
	}
	if !seen[string(flags.MemoryPrefetch)] {
		t.Fatalf("root --flags did not reach the resolver: %v", seen)
	}
	if fields := flagRowFields(stdout.String(), string(flags.MemoryPrefetch)); len(fields) < 3 || fields[1] != "on*" || fields[2] != "command" {
		t.Fatalf("memory.prefetch row = %v, want on* from command line", fields)
	}

	var launched tui.Options
	stdout.Reset()
	stderr.Reset()
	deps = flagsTestDeps(cwd, map[string]string{})
	deps.userConfigPath = func() (string, error) { return filepath.Join(t.TempDir(), "config.json"), nil }
	deps.registerMCPTools = func(context.Context, *tools.Registry, config.MCPConfig, mcp.RegisterOptions) (mcpToolRuntime, error) {
		return noopMCPRuntime{}, nil
	}
	deps.newProvider = func(config.ProviderProfile) (zeroruntime.Provider, error) { return nil, nil }
	deps.runTUI = func(_ context.Context, options tui.Options) int {
		launched = options
		return 0
	}
	exitCode = runWithDeps([]string{"--flags=memory.prefetch"}, &stdout, &stderr, deps)
	if exitCode != 0 {
		t.Fatalf("TUI exit = %d, stderr = %q", exitCode, stderr.String())
	}
	found := false
	for _, r := range launched.FlagResolutions {
		if r.Name == flags.MemoryPrefetch {
			found = r.Value && r.Source == "command line"
		}
	}
	if !found {
		t.Fatalf("TUI did not receive the root --flags resolution: %+v", launched.FlagResolutions)
	}
}

func TestRootFlagsInvalidListIsUsageErrorNamingItem(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"--flags", "Bad Name", "flags"}, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{}))
	if exitCode != exitUsage {
		t.Fatalf("exit = %d, want usage %d", exitCode, exitUsage)
	}
	if !strings.Contains(stderr.String(), "Bad Name") {
		t.Fatalf("usage error does not name the item: %q", stderr.String())
	}
}

func TestWithRootFlagsLetsExecFlagWin(t *testing.T) {
	rootFlags = map[string]bool{"memory.prefetch": true, "memory.scope": true}
	t.Cleanup(func() { rootFlags = nil })
	var got map[string]bool
	resolve := withRootFlags(func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
		got = overrides.Flags
		return config.ResolvedConfig{}, nil
	})
	if _, err := resolve("", config.Overrides{Flags: map[string]bool{"memory.prefetch": false}}); err != nil {
		t.Fatal(err)
	}
	if got["memory.prefetch"] || !got["memory.scope"] {
		t.Fatalf("merged flags = %v, want exec to win for memory.prefetch and root to keep memory.scope", got)
	}
}

// TestProcessFlagsRecordedWhenNoActiveProvider is the CLI-level guard for the
// process flag set. The flag comes from the user config file, not from the
// environment, because the environment fallback would hide a missing SetProcess
// call. The config names an active provider that does not exist, so resolution
// returns ErrNoActiveProvider with a resolved flag set.
func TestProcessFlagsRecordedWhenNoActiveProvider(t *testing.T) {
	flags.SetProcess(nil)
	t.Cleanup(func() { flags.SetProcess(nil) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, ".config", "splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"activeProvider":"gone","flags":{"sessions.checkpoints":false},"providers":[{"name":"local","provider_kind":"openai-compatible","base_url":"http://127.0.0.1:9","api_key":"sk-local","model":"local-model"}]}`
	if err := os.WriteFile(filepath.Join(home, ".config", "splice", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	deps := defaultAppDeps()
	deps.getwd = func() (string, error) { return home, nil }
	var stdout, stderr bytes.Buffer
	runWithDeps([]string{"flags"}, &stdout, &stderr, deps)

	if flags.Process().Enabled(flags.SessionsCheckpoints) {
		t.Error("process flag set was not recorded after ErrNoActiveProvider: sessions.checkpoints read as on")
	}
	if sessions.CheckpointsEnabled() {
		t.Error("sessions.CheckpointsEnabled ignored the config flag after a provider-less resolve")
	}
}

// TestRootFlagsAndTrustParseInAnyOrder checks that --trust and --flags can
// come in either order before the subcommand.
func TestRootFlagsAndTrustParseInAnyOrder(t *testing.T) {
	for _, args := range [][]string{
		{"--flags", "memory.scope", "--trust", "flags"},
		{"--trust", "--flags", "memory.scope", "flags"},
		{"--flags=memory.scope", "--trust", "flags"},
	} {
		var stdout, stderr bytes.Buffer
		exitCode := runWithDeps(args, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{}))
		if exitCode != exitSuccess {
			t.Fatalf("%v: exit = %d, stderr = %q", args, exitCode, stderr.String())
		}
		if fields := flagRowFields(stdout.String(), string(flags.MemoryScope)); len(fields) < 2 || !strings.HasPrefix(fields[1], "on") {
			t.Fatalf("%v: memory.scope row = %v, want on from the root --flags", args, fields)
		}
	}
}

// TestRootFlagsRejectedWhereConfigIsNotRead checks that root --flags fails
// loudly on a command that does not resolve config, and that a second --flags
// list is refused.
func TestRootFlagsRejectedWhereConfigIsNotRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"--flags", "memory.scope", "daemon"}, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{}))
	if exitCode != exitUsage {
		t.Fatalf("daemon exit = %d, want usage %d (stderr %q)", exitCode, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--flags is only supported") || !strings.Contains(stderr.String(), "daemon") {
		t.Fatalf("usage error does not name the command: %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"--flags", "memory.scope", "--flags", "memory.prefetch", "flags"}, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{}))
	if exitCode != exitUsage || !strings.Contains(stderr.String(), "--flags may be given once") {
		t.Fatalf("repeated --flags: exit = %d, stderr = %q", exitCode, stderr.String())
	}
}

// TestRootFlagsRejectedForSubcommandThatSkipsConfig checks that root --flags
// fails loud on a subcommand that never resolves config, even when its parent
// command does. It also checks that a subcommand that resolves config still
// accepts the list.
func TestRootFlagsRejectedForSubcommandThatSkipsConfig(t *testing.T) {
	for _, args := range [][]string{
		{"--flags", "memory.scope", "providers", "setup"},
		{"--flags", "memory.scope", "mcp", "add", "docs", "--command", "true"},
	} {
		var stdout, stderr bytes.Buffer
		exitCode := runWithDeps(args, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{}))
		if exitCode != exitUsage {
			t.Fatalf("%v: exit = %d, want usage %d (stderr %q)", args, exitCode, exitUsage, stderr.String())
		}
		if !strings.Contains(stderr.String(), "--flags is only supported") || !strings.Contains(stderr.String(), args[2]+" "+args[3]) {
			t.Fatalf("%v: usage error does not name the subcommand: %q", args, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("%v: expected empty stdout, got %q", args, stdout.String())
		}
	}

	for _, args := range [][]string{
		{"--flags", "memory.scope", "config"},
		{"--flags", "memory.scope", "providers"},
	} {
		var stdout, stderr bytes.Buffer
		runWithDeps(args, &stdout, &stderr, flagsTestDeps(t.TempDir(), map[string]string{}))
		if strings.Contains(stderr.String(), "--flags is only supported") {
			t.Fatalf("%v reads config and must accept --flags, stderr %q", args, stderr.String())
		}
	}
}

// TestDoctorReportsBadFlagValueNotMissingProvider checks that an invalid flag
// value is reported as its own failed check. It must not look like a missing
// provider.
func TestDoctorReportsBadFlagValueNotMissingProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := flagsTestDeps(t.TempDir(), map[string]string{"SPLICE_MEMORY_PREFETCH": "bogus"})
	runWithDeps([]string{"doctor"}, &stdout, &stderr, deps)
	out := stdout.String()
	if !strings.Contains(out, "[fail] config.resolve") || !strings.Contains(out, "SPLICE_MEMORY_PREFETCH") {
		t.Fatalf("doctor does not report the bad flag value as a failed check:\n%s", out)
	}
	if strings.Contains(out, "No LLM provider is configured") {
		t.Fatalf("doctor claims no provider for a bad flag value:\n%s", out)
	}
}
