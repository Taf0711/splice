package flags

import (
	"fmt"
	"sort"
)

// Sources are the raw inputs to flag resolution, in ascending precedence
// order. Resolution applies them over the registry defaults: defaults, then
// user config, then project config, then the SPLICE_FLAGS environment
// variable, then the command line.
type Sources struct {
	// User is the user config `flags` map. May be nil.
	User map[string]bool
	// Project is the project config `flags` map. May be nil. Only ScopeProject
	// flags may be set here.
	Project map[string]bool
	// Env is the raw SPLICE_FLAGS value. May be empty.
	Env string
	// CLI is the command-line override map. May be nil.
	CLI map[string]bool
}

// Resolve merges the sources over the registry defaults.
//
// Every source is validated before it is applied. An unknown flag name, a
// flag set twice in one list, or a ScopeUser flag set from project config is
// an error that names the flag and the source. Resolution is pure: the same
// inputs always produce the same set.
func Resolve(src Sources) (Set, error) {
	set := make(Set, len(Registry))
	for _, def := range Registry {
		set[def.Name] = def.Default
	}

	if err := applyConfig(set, src.User, "user config", false); err != nil {
		return nil, err
	}
	if err := applyConfig(set, src.Project, "project config", true); err != nil {
		return nil, err
	}

	envFlags, err := ParseList(src.Env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvVar, err)
	}
	if err := applyConfig(set, envFlags, EnvVar, false); err != nil {
		return nil, err
	}
	if err := applyConfig(set, src.CLI, "command line", false); err != nil {
		return nil, err
	}
	return set, nil
}

// applyConfig applies one name/value source over the set.
//
// Names are validated in sorted order so an error message is deterministic
// when several names are wrong at once. When project is true, a ScopeUser flag
// is refused: a cloned repository must not be able to relax a bound.
func applyConfig(set Set, values map[string]bool, source string, project bool) error {
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
		set[def.Name] = values[name]
	}
	return nil
}
