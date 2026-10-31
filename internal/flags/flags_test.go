package flags

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRegistryEntriesAreWellFormed is the producer side of the pairing test.
// Every declared flag must carry a description and a named consumer, because a
// flag that is declared and never read is the project's dominant defect class.
func TestRegistryEntriesAreWellFormed(t *testing.T) {
	seen := make(map[Flag]bool, len(Registry))
	for _, def := range Registry {
		if err := ValidateName(string(def.Name)); err != nil {
			t.Errorf("registry entry %q: %v", def.Name, err)
		}
		if def.Description == "" {
			t.Errorf("registry entry %q has an empty Description", def.Name)
		}
		if def.ReadBy == "" {
			t.Errorf("registry entry %q has an empty ReadBy: name the consumer", def.Name)
		}
		if seen[def.Name] {
			t.Errorf("registry entry %q is declared twice", def.Name)
		}
		seen[def.Name] = true
	}
}

// TestDeclaredConstantsAreRegistered pins the constants against the registry.
// A constant that is not registered cannot be resolved or enabled.
func TestDeclaredConstantsAreRegistered(t *testing.T) {
	for _, name := range []Flag{
		StageSecurityAuditor, StageTestGenerator, TUIPipelineEnabled, DecisionAdvisorEnabled,
		MemoryPrefetch, MemoryEvidenceSubstitution, MemoryScope, PipelineBudgetFit,
		ToolsFormatOnWrite, SessionsCheckpoints, ProviderPromptCacheKey, PassEvidence,
	} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("declared flag constant %q is missing from Registry", name)
		}
	}
}

func TestParseList(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[string]bool
		wantErr bool
	}{
		{name: "empty", raw: "", want: map[string]bool{}},
		{name: "enable short form", raw: "a.b", want: map[string]bool{"a.b": true}},
		{name: "disable short form", raw: "-a.b", want: map[string]bool{"a.b": false}},
		{name: "explicit one", raw: "a.b=1", want: map[string]bool{"a.b": true}},
		{name: "explicit zero", raw: "a.b=0", want: map[string]bool{"a.b": false}},
		{name: "explicit true word", raw: "a.b=true", want: map[string]bool{"a.b": true}},
		{name: "explicit off word", raw: "a.b=off", want: map[string]bool{"a.b": false}},
		{name: "whitespace and trailing comma", raw: " a.b , c.d=0 , ", want: map[string]bool{"a.b": true, "c.d": false}},
		{name: "duplicate is an error", raw: "a.b,-a.b", wantErr: true},
		{name: "invalid boolean", raw: "a.b=yes", wantErr: true},
		{name: "invalid name", raw: "A.B", wantErr: true},
		{name: "empty name from dash", raw: "-", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseList(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseList(%q) = %v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseList(%q) unexpected error: %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseList(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestResolveDefaults(t *testing.T) {
	set, err := Resolve(Sources{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, def := range Registry {
		if got := set.Enabled(def.Name); got != def.Default {
			t.Errorf("default for %q = %v, want %v", def.Name, got, def.Default)
		}
	}
	// The zero Set must behave identically to an all-defaults resolution.
	var zero Set
	for _, def := range Registry {
		if zero.Enabled(def.Name) != def.Default {
			t.Errorf("zero Set default for %q = %v, want %v", def.Name, zero.Enabled(def.Name), def.Default)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	target := StageTestGenerator
	set, err := Resolve(Sources{
		User:    map[string]bool{string(target): false},
		CLI:     map[string]bool{string(target): true},
		Env:     "-" + string(target),
		Project: map[string]bool{},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !set.Enabled(target) {
		t.Fatalf("CLI must outrank env, user, and project; got disabled")
	}

	// Drop the CLI layer: env must win over user config.
	set, err = Resolve(Sources{
		User: map[string]bool{string(target): true},
		Env:  "-" + string(target),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if set.Enabled(target) {
		t.Fatalf("env must outrank user config; got enabled")
	}
}

func TestResolveRejectsUnknownFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  Sources
		want string
	}{
		{name: "user config", src: Sources{User: map[string]bool{"nope": true}}, want: "user config"},
		{name: "cli", src: Sources{CLI: map[string]bool{"nope": true}}, want: "command line"},
		{name: "env", src: Sources{Env: "nope"}, want: EnvVar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tc.src)
			if err == nil {
				t.Fatalf("Resolve accepted unknown flag from %s", tc.name)
			}
			if !strings.Contains(err.Error(), "unknown feature flag") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q must name the unknown flag and the source %q", err, tc.want)
			}
		})
	}
}

// TestResolveRefusesUserScopeFromProject is the adversarial guard: a cloned
// repository must not be able to relax a user-scope flag.
func TestResolveRefusesUserScopeFromProject(t *testing.T) {
	_, err := Resolve(Sources{Project: map[string]bool{string(StageSecurityAuditor): false}})
	if err == nil {
		t.Fatal("project config was allowed to disable a user-scope flag")
	}
	if !strings.Contains(err.Error(), "user-scope") {
		t.Fatalf("error %q must explain the scope refusal", err)
	}

	// A project-scope flag from project config is allowed.
	set, err := Resolve(Sources{Project: map[string]bool{string(TUIPipelineEnabled): false}})
	if err != nil {
		t.Fatalf("Resolve with project-scope flag: %v", err)
	}
	if set.Enabled(TUIPipelineEnabled) {
		t.Fatal("project-scope flag set from project config was ignored")
	}
}

func TestEnabledNamesSortedAndStable(t *testing.T) {
	set, err := Resolve(Sources{User: map[string]bool{string(StageTestGenerator): false}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := set.EnabledNames()
	want := []string{
		string(PipelineBudgetFit), string(StageSecurityAuditor), string(TrajectoryClassifierEnabled),
		string(ProviderPromptCacheKey), string(SessionsCheckpoints), string(TUIPipelineEnabled),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnabledNames() = %v, want %v", got, want)
	}
	for i := 0; i < 5; i++ {
		if again := set.EnabledNames(); !reflect.DeepEqual(again, got) {
			t.Fatalf("EnabledNames() not stable: %v then %v", got, again)
		}
	}
}

// TestExperimentalFlagsDefaultOff keeps treatments off until the owner turns
// them on. An experimental flag that defaults to true would run unmeasured.
func TestExperimentalFlagsDefaultOff(t *testing.T) {
	for _, def := range Registry {
		if def.Stability == Experimental && def.Default {
			t.Errorf("experimental flag %q defaults to true", def.Name)
		}
	}
	if Stable.String() != "stable" || Experimental.String() != "experimental" {
		t.Fatalf("Stability.String() = %q, %q", Stable, Experimental)
	}
}

// TestLegacyAliasValues pins the legacy environment spellings for each alias.
// The rules differ per flag: some fail loud on an unknown value, and some keep
// today's permissive reading, which the comments in the registry explain.
func TestLegacyAliasValues(t *testing.T) {
	tests := []struct {
		name    string
		flag    Flag
		env     string
		raw     string
		want    bool
		wantErr bool
	}{
		{name: "prefetch on", flag: MemoryPrefetch, env: "SPLICE_MEMORY_PREFETCH", raw: "on", want: true},
		{name: "prefetch case and space", flag: MemoryPrefetch, env: "SPLICE_MEMORY_PREFETCH", raw: " ON ", want: true},
		{name: "prefetch off", flag: MemoryPrefetch, env: "SPLICE_MEMORY_PREFETCH", raw: "off", want: false},
		{name: "prefetch empty is not set", flag: MemoryPrefetch, env: "SPLICE_MEMORY_PREFETCH", raw: "  ", want: false},
		{name: "prefetch invalid fails loud", flag: MemoryPrefetch, env: "SPLICE_MEMORY_PREFETCH", raw: "banana", wantErr: true},
		{name: "evidence invalid fails loud", flag: MemoryEvidenceSubstitution, env: "SPLICE_EVIDENCE_SUBSTITUTION", raw: "banana", wantErr: true},
		{name: "scope on", flag: MemoryScope, env: "SPLICE_SCOPE_MODE", raw: "on", want: true},
		{name: "scope invalid fails loud", flag: MemoryScope, env: "SPLICE_SCOPE_MODE", raw: "maybe", wantErr: true},
		{name: "budget fit", flag: PipelineBudgetFit, env: "SPLICE_BUDGET_FIT", raw: "fit", want: true},
		{name: "budget off", flag: PipelineBudgetFit, env: "SPLICE_BUDGET_FIT", raw: "off", want: false},
		{name: "budget invalid fails loud", flag: PipelineBudgetFit, env: "SPLICE_BUDGET_FIT", raw: "banana", wantErr: true},
		{name: "format on", flag: ToolsFormatOnWrite, env: "SPLICE_FORMAT_ON_WRITE", raw: "1", want: true},
		{name: "format false", flag: ToolsFormatOnWrite, env: "SPLICE_FORMAT_ON_WRITE", raw: "FALSE", want: false},
		{name: "format any other value is on", flag: ToolsFormatOnWrite, env: "SPLICE_FORMAT_ON_WRITE", raw: "gofmt", want: true},
		{name: "checkpoints off", flag: SessionsCheckpoints, env: "SPLICE_CHECKPOINTS", raw: "off", want: false},
		{name: "checkpoints any other value is on", flag: SessionsCheckpoints, env: "SPLICE_CHECKPOINTS", raw: "yes", want: true},
		{name: "cache key disable switch on", flag: ProviderPromptCacheKey, env: "SPLICE_DISABLE_PROMPT_CACHE_KEY", raw: "1", want: false},
		{name: "cache key falsy keeps key", flag: ProviderPromptCacheKey, env: "SPLICE_DISABLE_PROMPT_CACHE_KEY", raw: "0", want: true},
		{name: "cache key any other value disables", flag: ProviderPromptCacheKey, env: "SPLICE_DISABLE_PROMPT_CACHE_KEY", raw: "whatever", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set, err := Resolve(Sources{LegacyEnv: map[string]string{tc.env: tc.raw}})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("%s=%q: want error, got %v", tc.env, tc.raw, set.Enabled(tc.flag))
				}
				if !strings.Contains(err.Error(), tc.env) || !strings.Contains(err.Error(), tc.raw) {
					t.Fatalf("error %q must name %s and %q", err, tc.env, tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s=%q: unexpected error: %v", tc.env, tc.raw, err)
			}
			if got := set.Enabled(tc.flag); got != tc.want {
				t.Fatalf("%s=%q: %s = %v, want %v", tc.env, tc.raw, tc.flag, got, tc.want)
			}
		})
	}
}

// TestPrecedenceWithLegacyAlias checks the full order: defaults, user config,
// project config, legacy alias, SPLICE_FLAGS, command line.
func TestPrecedenceWithLegacyAlias(t *testing.T) {
	legacy := map[string]string{"SPLICE_MEMORY_PREFETCH": "on"}
	set, err := Resolve(Sources{User: map[string]bool{string(MemoryPrefetch): false}, LegacyEnv: legacy})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !set.Enabled(MemoryPrefetch) {
		t.Fatal("legacy alias must outrank user config")
	}
	set, err = Resolve(Sources{LegacyEnv: legacy, Env: "-" + string(MemoryPrefetch)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if set.Enabled(MemoryPrefetch) {
		t.Fatal("SPLICE_FLAGS must outrank the legacy alias")
	}
	set, err = Resolve(Sources{LegacyEnv: legacy, Env: "-" + string(MemoryPrefetch), CLI: map[string]bool{string(MemoryPrefetch): true}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !set.Enabled(MemoryPrefetch) {
		t.Fatal("command line must outrank SPLICE_FLAGS")
	}
}

// TestExplainReportsSources checks the source name for each layer, and that
// Resolve gives the same values as Explain.
func TestExplainReportsSources(t *testing.T) {
	src := Sources{
		User:      map[string]bool{string(StageSecurityAuditor): false},
		LegacyEnv: map[string]string{"SPLICE_MEMORY_PREFETCH": "on"},
		Env:       "-" + string(TUIPipelineEnabled),
		CLI:       map[string]bool{string(SessionsCheckpoints): false},
	}
	resolutions, err := Explain(src)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(resolutions) != len(Registry) {
		t.Fatalf("Explain returned %d rows, want %d", len(resolutions), len(Registry))
	}
	wantSource := map[Flag]string{
		StageSecurityAuditor:   "user config",
		MemoryPrefetch:         "SPLICE_MEMORY_PREFETCH",
		TUIPipelineEnabled:     "SPLICE_FLAGS",
		SessionsCheckpoints:    "command line",
		StageTestGenerator:     "default",
		ProviderPromptCacheKey: "default",
	}
	bySource := make(map[Flag]Resolution, len(resolutions))
	for i, r := range resolutions {
		if r.Name != Registry[i].Name {
			t.Fatalf("row %d = %q, want registry order %q", i, r.Name, Registry[i].Name)
		}
		bySource[r.Name] = r
	}
	for name, want := range wantSource {
		if got := bySource[name].Source; got != want {
			t.Errorf("source for %q = %q, want %q", name, got, want)
		}
	}
	if bySource[MemoryPrefetch].Default || !bySource[MemoryPrefetch].Value {
		t.Errorf("memory.prefetch row = %+v, want default false and value true", bySource[MemoryPrefetch])
	}

	set, err := Resolve(src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, r := range resolutions {
		if set.Enabled(r.Name) != r.Value {
			t.Errorf("Resolve and Explain disagree on %q", r.Name)
		}
	}
}

// TestLegacyEnvValuesOnlySetVariables checks that only variables with a
// non-empty value are passed on, and that only registered aliases are read.
func TestLegacyEnvValuesOnlySetVariables(t *testing.T) {
	env := map[string]string{"SPLICE_MEMORY_PREFETCH": "on", "SPLICE_SCOPE_MODE": "", "UNRELATED": "x"}
	got := LegacyEnvValues(func(name string) string { return env[name] })
	want := map[string]string{"SPLICE_MEMORY_PREFETCH": "on"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LegacyEnvValues = %v, want %v", got, want)
	}
}

// TestProcessSetRoundTrip checks the process-level accessor. A nil process
// set reads every flag as its default.
func TestProcessSetRoundTrip(t *testing.T) {
	t.Cleanup(func() { SetProcess(nil) })
	if Process().Enabled(MemoryScope) {
		t.Fatal("unset process set must read the default")
	}
	SetProcess(Set{MemoryScope: true})
	if !Process().Enabled(MemoryScope) {
		t.Fatal("process set was not recorded")
	}
}

// TestProcessFallsBackToEnvironment checks the read path for code that runs
// when no set was recorded, such as a command that failed before config
// resolution. The fallback reads SPLICE_FLAGS and the legacy aliases only.
func TestProcessFallsBackToEnvironment(t *testing.T) {
	SetProcess(nil)
	t.Cleanup(func() { SetProcess(nil) })

	t.Setenv(EnvVar, "memory.scope")
	t.Setenv("SPLICE_CHECKPOINTS", "off")
	if !Process().Enabled(MemoryScope) {
		t.Error("SPLICE_FLAGS was not applied by the environment fallback")
	}
	if Process().Enabled(SessionsCheckpoints) {
		t.Error("legacy SPLICE_CHECKPOINTS=off was not applied by the environment fallback")
	}
}

// TestProcessFallbackUsesDefaultsOnResolveError checks that a bad environment
// value does not panic and does not partly apply. Every flag reads its default.
func TestProcessFallbackUsesDefaultsOnResolveError(t *testing.T) {
	SetProcess(nil)
	t.Cleanup(func() { SetProcess(nil) })

	t.Setenv("SPLICE_MEMORY_PREFETCH", "banana")
	t.Setenv(EnvVar, "memory.scope")
	got := Process()
	for _, def := range Registry {
		if got.Enabled(def.Name) != def.Default {
			t.Errorf("after a resolve error, %q = %v, want default %v", def.Name, got.Enabled(def.Name), def.Default)
		}
	}
}

// TestProcessFallbackIsCachedUntilSet checks that the environment is read once.
// SetProcess replaces the cached value, and SetProcess(nil) clears it.
func TestProcessFallbackIsCachedUntilSet(t *testing.T) {
	SetProcess(nil)
	t.Cleanup(func() { SetProcess(nil) })

	t.Setenv(EnvVar, "memory.scope")
	if !Process().Enabled(MemoryScope) {
		t.Fatal("first read did not apply SPLICE_FLAGS")
	}
	t.Setenv(EnvVar, "")
	if !Process().Enabled(MemoryScope) {
		t.Fatal("fallback was not cached between reads")
	}

	SetProcess(Set{MemoryScope: false})
	if Process().Enabled(MemoryScope) {
		t.Fatal("SetProcess did not replace the environment fallback")
	}

	SetProcess(nil)
	if Process().Enabled(MemoryScope) {
		t.Fatal("after SetProcess(nil) the environment must be read again, and it is now empty")
	}
}

// TestFeatureFlagsDocMatchesRegistry fails when docs/FEATURE_FLAGS.md drifts
// from the registry. It checks the flag names in both directions, the
// default, stability, scope, and legacy variable columns for each flag, and
// that the Legacy variables list names each legacy variable.
func TestFeatureFlagsDocMatchesRegistry(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "FEATURE_FLAGS.md"))
	if err != nil {
		t.Fatalf("read FEATURE_FLAGS.md: %v", err)
	}
	// Only the flag table counts. Other sections may hold tables of their own.
	rows := map[string][]string{}
	legacyListed := map[string]bool{}
	inTable, inLegacy := false, false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			inTable = strings.TrimSpace(line) == "## Flag table"
			inLegacy = strings.TrimSpace(line) == "## Legacy variables"
			continue
		}
		if inLegacy && strings.HasPrefix(line, "- `") {
			legacyListed[strings.SplitN(line[3:], "`", 2)[0]] = true
			continue
		}
		if !inTable || !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 7 {
			t.Fatalf("FEATURE_FLAGS.md flag row has %d cells, want 6: %q", len(cells)-2, line)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		name := strings.Trim(cells[1], "`")
		if _, dup := rows[name]; dup {
			t.Fatalf("FEATURE_FLAGS.md lists %q twice", name)
		}
		rows[name] = cells
	}

	registered := map[string]bool{}
	for _, def := range Registry {
		name := string(def.Name)
		registered[name] = true
		cells, ok := rows[name]
		if !ok {
			t.Errorf("registry flag %q is missing from docs/FEATURE_FLAGS.md", name)
			continue
		}
		legacy := "-"
		if def.LegacyEnv != "" {
			legacy = "`" + def.LegacyEnv + "`"
			if !legacyListed[def.LegacyEnv] {
				t.Errorf("legacy variable %s of %q is missing from the Legacy variables list in docs/FEATURE_FLAGS.md", def.LegacyEnv, name)
			}
		}
		want := map[string]string{
			"default":   onOffWord(def.Default),
			"stability": def.Stability.String(),
			"scope":     def.Scope.String(),
			"legacy":    legacy,
		}
		got := map[string]string{
			"default":   cells[2],
			"stability": cells[3],
			"scope":     cells[4],
			"legacy":    cells[5],
		}
		for column, w := range want {
			if got[column] != w {
				t.Errorf("docs/FEATURE_FLAGS.md %s column for %q = %q, want %q (from the registry)", column, name, got[column], w)
			}
		}
	}
	for name := range rows {
		if !registered[name] {
			t.Errorf("docs/FEATURE_FLAGS.md lists %q, which is not registered", name)
		}
	}
}

// onOffWord spells a default the way the flag table does.
func onOffWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
