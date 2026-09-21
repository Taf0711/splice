// Package flags declares and resolves Splice feature flags.
//
// A flag is a named, typed switch resolved once per process. Resolution reads
// configuration, the environment, and the command line, and never runs a
// remote service. The package holds no provider, filesystem, or network
// dependency so that resolution stays deterministic and testable.
//
// The registry is the single source of truth. Every flag declares its default,
// its config scope, and the call site that reads it. A flag that is declared
// but never read is a defect; internal/splice and internal/config carry tests
// that fail when a consumer drifts from the registry.
package flags

import (
	"fmt"
	"sort"
)

// Flag is a closed-enum feature-flag name.
//
// Consumers use the declared constants below, never a raw string. This keeps a
// flag key typed across a package boundary and makes a typo a compile error
// rather than a silently disabled feature.
type Flag string

// Scope controls which configuration scope may set a flag.
type Scope uint8

const (
	// ScopeUser lets the user config, the environment, and the command line set
	// the flag. Project config may not. Use for anything that relaxes a safety,
	// cost, or coverage bound, so a cloned repository cannot grant itself the
	// relaxation.
	ScopeUser Scope = iota
	// ScopeProject lets project config set the flag as well. Use only for flags
	// that are inert or that only tighten behavior.
	ScopeProject
)

// Definition declares one flag.
type Definition struct {
	// Name is the flag's registry name.
	Name Flag
	// Default is the value when no source sets the flag.
	Default bool
	// Scope is the highest config scope allowed to set the flag.
	Scope Scope
	// Description states what the flag does, in one sentence.
	Description string
	// ReadBy names the call site that consumes the flag. It must be non-empty:
	// the project's dominant defect class is a value produced and never read.
	ReadBy string
}

// Declared flags. A consumer references these constants, never a literal.
const (
	// StageSecurityAuditor runs the security auditor stage when true.
	StageSecurityAuditor Flag = "pipeline.stage.security_auditor"
	// StageTestGenerator runs the test generator stage when true.
	StageTestGenerator Flag = "pipeline.stage.test_generator"
	// TUIPipelineEnabled routes interactive TUI turns through the pipeline.
	TUIPipelineEnabled Flag = "tui.pipeline.enabled"
)

// Registry is the single source of truth for declared flags.
var Registry = []Definition{
	{
		Name:        StageSecurityAuditor,
		Default:     true,
		Scope:       ScopeUser,
		Description: "Run the security auditor stage in the pipeline.",
		ReadBy:      "splice.FilterStageNames",
	},
	{
		Name:        StageTestGenerator,
		Default:     true,
		Scope:       ScopeUser,
		Description: "Run the test generator stage in the pipeline.",
		ReadBy:      "splice.FilterStageNames",
	},
	{
		Name:        TUIPipelineEnabled,
		Default:     true,
		Scope:       ScopeProject,
		Description: "Route interactive TUI turns through the pipeline.",
		ReadBy:      "tui.usesPipeline",
	},
}

// Set is a resolved flag set. The zero value behaves as "all defaults".
type Set map[Flag]bool

// Enabled reports whether a flag is on.
//
// An unregistered flag reads as its default, which for a missing definition is
// false. A flag whose name is not in the registry can only come from a code
// mistake, and false is the conservative answer: an unknown feature stays off.
func (s Set) Enabled(f Flag) bool {
	if s != nil {
		if v, ok := s[f]; ok {
			return v
		}
	}
	def, ok := Lookup(f)
	if !ok {
		return false
	}
	return def.Default
}

// EnabledNames returns the sorted names of every enabled flag.
//
// Sorting makes the result byte-stable, so a plan that records the enabled set
// is reproducible across runs and map iteration order cannot leak into output.
func (s Set) EnabledNames() []string {
	names := make([]string, 0, len(Registry))
	for _, def := range Registry {
		if s.Enabled(def.Name) {
			names = append(names, string(def.Name))
		}
	}
	sort.Strings(names)
	return names
}

// Lookup returns the definition for a flag name.
func Lookup(f Flag) (Definition, bool) {
	for _, def := range Registry {
		if def.Name == f {
			return def, true
		}
	}
	return Definition{}, false
}

// knownFlagNames returns the sorted registered names for error messages.
func knownFlagNames() []string {
	names := make([]string, 0, len(Registry))
	for _, def := range Registry {
		names = append(names, string(def.Name))
	}
	sort.Strings(names)
	return names
}

// unknownFlagError reports an unrecognized flag name and the source that named
// it, so a typo is a loud failure instead of a silent default.
func unknownFlagError(name, source string) error {
	return fmt.Errorf("unknown feature flag %q in %s (known: %v)", name, source, knownFlagNames())
}
