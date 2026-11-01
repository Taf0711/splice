package flags

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// Sources are the raw inputs to flag resolution, in ascending precedence
// order. Resolution applies them over the registry defaults: defaults, then
// user config, then project config, then legacy environment aliases, then the
// SPLICE_FLAGS environment variable, then the command line.
type Sources struct {
	// User is the user config `flags` map. May be nil.
	User map[string]bool
	// Project is the project config `flags` map. May be nil. Only ScopeProject
	// flags may be set here.
	Project map[string]bool
	// LegacyEnv maps each legacy variable name to its raw value. Only variables
	// that are set need an entry. See LegacyEnvValues. May be nil.
	LegacyEnv map[string]string
	// Env is the raw SPLICE_FLAGS value. May be empty.
	Env string
	// CLI is the command-line override map. May be nil.
	CLI map[string]bool
}

// Resolution is the outcome for one registry flag.
type Resolution struct {
	Name Flag
	// Value is the resolved state.
	Value bool
	// Default is the registry default.
	Default bool
	// Source names the layer that set Value: "default", "user config",
	// "project config", a legacy variable name, "SPLICE_FLAGS", or "command line".
	Source     string
	Definition Definition
}

// Source names used in Resolution.Source.
const (
	sourceDefault       = "default"
	sourceUserConfig    = "user config"
	sourceProjectConfig = "project config"
	sourceCommandLine   = "command line"
)

// Resolve merges the sources over the registry defaults.
//
// Every source is validated before it is applied. An unknown flag name, a
// flag set twice in one list, a ScopeUser flag set from project config, or an
// invalid legacy value is an error that names the flag and the source.
// Resolution is pure: the same inputs always produce the same set.
func Resolve(src Sources) (Set, error) {
	resolutions, err := Explain(src)
	if err != nil {
		return nil, err
	}
	set := make(Set, len(resolutions))
	for _, r := range resolutions {
		set[r.Name] = r.Value
	}
	return set, nil
}

// Explain resolves every registry flag and reports the layer that set each
// one. The results follow registry order. Resolve uses this same path, so the
// two can never disagree.
func Explain(src Sources) ([]Resolution, error) {
	state := make(map[Flag]Resolution, len(Registry))
	for _, def := range Registry {
		state[def.Name] = Resolution{Name: def.Name, Value: def.Default, Default: def.Default, Source: sourceDefault, Definition: def}
	}

	if err := applyConfig(state, src.User, sourceUserConfig, false); err != nil {
		return nil, err
	}
	if err := applyConfig(state, src.Project, sourceProjectConfig, true); err != nil {
		return nil, err
	}
	if err := applyLegacy(state, src.LegacyEnv); err != nil {
		return nil, err
	}

	envFlags, err := ParseList(src.Env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvVar, err)
	}
	if err := applyConfig(state, envFlags, EnvVar, false); err != nil {
		return nil, err
	}
	if err := applyConfig(state, src.CLI, sourceCommandLine, false); err != nil {
		return nil, err
	}

	out := make([]Resolution, 0, len(Registry))
	for _, def := range Registry {
		out = append(out, state[def.Name])
	}
	return out, nil
}

// applyConfig applies one name/value source over the resolution state.
//
// Names are validated in sorted order so an error message is deterministic
// when several names are wrong at once. When project is true, a ScopeUser flag
// is refused: a cloned repository must not be able to relax a bound.
func applyConfig(state map[Flag]Resolution, values map[string]bool, source string, project bool) error {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		def, ok := Lookup(Flag(name))
		if !ok {
			return unknownFlagError(name, source)
		}
		if project && def.Scope == ScopeUser {
			return fmt.Errorf("feature flag %q cannot be set from %s: it is user-scope only", name, source)
		}
		setState(state, def, values[name], source)
	}
	return nil
}

// applyLegacy applies the legacy environment aliases. Registry order keeps
// the error for a bad value deterministic.
func applyLegacy(state map[Flag]Resolution, env map[string]string) error {
	if len(env) == 0 {
		return nil
	}
	for _, def := range Registry {
		if def.LegacyEnv == "" {
			continue
		}
		raw, ok := env[def.LegacyEnv]
		if !ok {
			continue
		}
		value, set, err := parseLegacy(def, raw)
		if err != nil {
			return err
		}
		if set {
			setState(state, def, value, def.LegacyEnv)
		}
	}
	return nil
}

// parseLegacy maps one legacy value to a flag value. The boolean reports
// whether the value sets the flag at all: an empty value does not.
func parseLegacy(def Definition, raw string) (value bool, set bool, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, false, nil
	}
	if containsFold(def.LegacyOn, trimmed) {
		return true, true, nil
	}
	if containsFold(def.LegacyOff, trimmed) {
		return false, true, nil
	}
	switch {
	case def.LegacyAnyOtherOn:
		return true, true, nil
	case def.LegacyAnyOtherOff:
		return false, true, nil
	}
	want := append(append([]string(nil), def.LegacyOn...), def.LegacyOff...)
	return false, false, fmt.Errorf("%s: invalid value %q (want one of %s)", def.LegacyEnv, raw, strings.Join(want, ", "))
}

// containsFold reports whether any item equals value, ignoring case.
func containsFold(items []string, value string) bool {
	for _, item := range items {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

// setState records one flag value and the layer that set it.
func setState(state map[Flag]Resolution, def Definition, value bool, source string) {
	r := state[def.Name]
	r.Value = value
	r.Source = source
	state[def.Name] = r
}

// LegacyEnvValues returns the set legacy variables that registry flags read.
// The lookup supplies values: pass os.Getenv for the process, or a test map.
// Variables with an empty value are left out, because empty means not set.
func LegacyEnvValues(lookup func(string) string) map[string]string {
	out := make(map[string]string)
	for _, def := range Registry {
		if def.LegacyEnv == "" {
			continue
		}
		if raw := lookup(def.LegacyEnv); raw != "" {
			out[def.LegacyEnv] = raw
		}
	}
	return out
}

// processSet is the resolved set for code that has no flags.Set in reach
// (tools, providers, sessions). The CLI sets it once after config resolution.
//
// When no set has been recorded, Process resolves once from the process
// environment only (SPLICE_FLAGS and the legacy aliases). It does not read
// config files, so project and user config flags are missing from that
// fallback. If the environment does not resolve, the fallback is the registry
// defaults. SetProcess replaces the fallback, and SetProcess(nil) clears it so
// the next read resolves again.
var (
	processMu   sync.Mutex
	processSet  Set
	envSet      Set
	envResolved bool
)

// SetProcess records the resolved set for the process. A nil set clears the
// recorded set and any cached environment fallback.
func SetProcess(s Set) {
	processMu.Lock()
	defer processMu.Unlock()
	processSet = s
	envSet = nil
	envResolved = false
}

// Process returns the resolved set for the process. See the package note on
// the environment fallback above.
func Process() Set {
	processMu.Lock()
	defer processMu.Unlock()
	if processSet != nil {
		return processSet
	}
	if !envResolved {
		envSet = resolveFromEnv(os.Getenv)
		envResolved = true
	}
	return envSet
}

// resolveFromEnv resolves the flags from the environment alone. A resolution
// error returns nil, which reads every flag as its registry default.
func resolveFromEnv(lookup func(string) string) Set {
	set, err := Resolve(Sources{
		Env:       lookup(EnvVar),
		LegacyEnv: LegacyEnvValues(lookup),
	})
	if err != nil {
		return nil
	}
	return set
}
