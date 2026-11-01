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

// String returns the lower-case scope name.
func (s Scope) String() string {
	if s == ScopeProject {
		return "project"
	}
	return "user"
}

// Stability states how settled a flag is.
type Stability uint8

const (
	// Stable flags are supported behavior switches.
	Stable Stability = iota
	// Experimental flags are treatments under measurement. They default to off.
	Experimental
)

// String returns the lower-case stability name.
func (s Stability) String() string {
	if s == Experimental {
		return "experimental"
	}
	return "stable"
}

// Definition declares one flag.
type Definition struct {
	// Name is the flag's registry name.
	Name Flag
	// Default is the value when no source sets the flag.
	Default bool
	// Scope is the highest config scope allowed to set the flag.
	Scope Scope
	// Stability is Stable or Experimental. An experimental flag must default to false.
	Stability Stability
	// Description states what the flag does, in one sentence.
	Description string
	// ReadBy names the call site that consumes the flag. It must be non-empty:
	// the project's dominant defect class is a value produced and never read.
	ReadBy string
	// LegacyEnv is an older environment variable that also sets the flag. Empty means none.
	LegacyEnv string
	// LegacyOn and LegacyOff list the LegacyEnv values that mean true and false
	// (compared case-insensitively after TrimSpace). An empty value always means "not set".
	// Any other value is a resolution error that names the variable and the value,
	// unless LegacyAnyOtherOn or LegacyAnyOtherOff says otherwise.
	LegacyOn, LegacyOff []string
	// LegacyAnyOtherOn makes any non-empty LegacyEnv value that is in neither list mean true.
	LegacyAnyOtherOn bool
	// LegacyAnyOtherOff makes any non-empty LegacyEnv value that is in neither list mean false.
	LegacyAnyOtherOff bool
}

// Declared flags. A consumer references these constants, never a literal.
const (
	// StageSecurityAuditor runs the security auditor stage when true.
	StageSecurityAuditor Flag = "pipeline.stage.security_auditor"
	// StageTestGenerator runs the test generator stage when true.
	StageTestGenerator Flag = "pipeline.stage.test_generator"
	// TUIPipelineEnabled routes interactive TUI turns through the pipeline.
	TUIPipelineEnabled Flag = "tui.pipeline.enabled"
	// DecisionAdvisorEnabled is the advisor kill switch. When false the
	// decision advisor is off regardless of its configured mode; when true
	// the decision_advisor config section governs. Default false: no
	// decision call happens before the owner flips this on.
	DecisionAdvisorEnabled Flag = "pipeline.decision_advisor"
	// TrajectoryClassifierEnabled runs the trajectory classifier by default.
	// When it is on and no decision_advisor section is set, the classifier
	// uses the validated ChatGPT sign-in profile. The selector stays off.
	TrajectoryClassifierEnabled Flag = "pipeline.trajectory_classifier"
	// PassEvidence makes each pass's workspace change, measured since the end
	// of the previous pass, the value the trajectory state reads. Off keeps the
	// HEAD-relative values; the per-pass evidence is recorded either way.
	PassEvidence Flag = "pipeline.pass_evidence"
	// MemoryPrefetch prefetches the source file a verified memory record names.
	MemoryPrefetch Flag = "memory.prefetch"
	// MemoryEvidenceSubstitution turns on production evidence substitution.
	MemoryEvidenceSubstitution Flag = "memory.evidence_substitution"
	// MemoryScope scopes context acquisition and tool behavior to cognition.
	MemoryScope Flag = "memory.scope"
	// PipelineBudgetFit applies learned stage budgets when true and freezes
	// every stage at its static budget when false.
	PipelineBudgetFit Flag = "pipeline.budget_fit"
	// ToolsFormatOnWrite runs the language formatter after each file write.
	ToolsFormatOnWrite Flag = "tools.format_on_write"
	// SessionsCheckpoints captures file checkpoints before each mutating tool call.
	SessionsCheckpoints Flag = "sessions.checkpoints"
	// ProviderPromptCacheKey sends the session prompt cache key to the provider.
	ProviderPromptCacheKey Flag = "provider.prompt_cache_key"
)

// Registry is the single source of truth for declared flags.
var Registry = []Definition{
	{
		Name:        StageSecurityAuditor,
		Default:     true,
		Scope:       ScopeUser,
		Stability:   Stable,
		Description: "Run the security auditor stage in the pipeline.",
		ReadBy:      "splice.FilterStageNames",
	},
	{
		Name:        StageTestGenerator,
		Default:     true,
		Scope:       ScopeUser,
		Stability:   Stable,
		Description: "Run the test generator stage in the pipeline.",
		ReadBy:      "splice.FilterStageNames",
	},
	{
		Name:        TUIPipelineEnabled,
		Default:     true,
		Scope:       ScopeProject,
		Stability:   Stable,
		Description: "Route interactive TUI turns through the pipeline. /exec always uses the pipeline.",
		ReadBy:      "tui.usesPipeline",
	},
	{
		Name:        DecisionAdvisorEnabled,
		Default:     false,
		Scope:       ScopeUser,
		Stability:   Experimental,
		Description: "Enable the decision advisor per its decision_advisor config mode.",
		ReadBy:      "splice.DecisionAdvisorFromRunConfig",
	},
	{
		Name:        TrajectoryClassifierEnabled,
		Default:     true,
		Scope:       ScopeUser,
		Stability:   Stable,
		Description: "Run the trajectory classifier; by default it uses a ChatGPT sign-in profile, else the rules decide.",
		ReadBy:      "config.trajectoryClassifierDefault",
	},
	{
		Name:        MemoryPrefetch,
		Default:     false,
		Scope:       ScopeUser,
		Stability:   Experimental,
		Description: "Prefetch the source file that a verified memory record names.",
		ReadBy:      "splice.prepareStageInput",
		LegacyEnv:   "SPLICE_MEMORY_PREFETCH",
		LegacyOn:    []string{"on"},
		LegacyOff:   []string{"off"},
	},
	{
		Name:        MemoryEvidenceSubstitution,
		Default:     false,
		Scope:       ScopeUser,
		Stability:   Experimental,
		Description: "Substitute verified memory evidence for reads in the first stage request.",
		ReadBy:      "splice.prepareStageInput",
		LegacyEnv:   "SPLICE_EVIDENCE_SUBSTITUTION",
		LegacyOn:    []string{"on"},
		LegacyOff:   []string{"off"},
	},
	{
		Name:        MemoryScope,
		Default:     false,
		Scope:       ScopeUser,
		Stability:   Experimental,
		Description: "Scope context acquisition and tool behavior to the cognition scope.",
		ReadBy:      "splice.scopeEnabled",
		LegacyEnv:   "SPLICE_SCOPE_MODE",
		LegacyOn:    []string{"on"},
		LegacyOff:   []string{"off"},
	},
	{
		Name:        PipelineBudgetFit,
		Default:     true,
		Scope:       ScopeUser,
		Stability:   Stable,
		Description: "Apply learned stage budgets; off freezes every stage at its static budget.",
		ReadBy:      "splice.runExecutionPlan",
		LegacyEnv:   "SPLICE_BUDGET_FIT",
		LegacyOn:    []string{"fit"},
		LegacyOff:   []string{"off"},
	},
	{
		Name:        PassEvidence,
		Default:     false,
		Scope:       ScopeUser,
		Stability:   Experimental,
		Description: "Measure each pass's workspace change since the previous pass, not since HEAD.",
		ReadBy:      "splice.runExecutionPlan",
		LegacyEnv:   "SPLICE_PASS_EVIDENCE",
		LegacyOn:    []string{"on"},
		LegacyOff:   []string{"off"},
	},
	{
		Name:        ToolsFormatOnWrite,
		Default:     false,
		Scope:       ScopeUser,
		Stability:   Stable,
		Description: "Run the language formatter on each file that a tool writes.",
		ReadBy:      "tools.maybeFormatWrittenFile",
		LegacyEnv:   "SPLICE_FORMAT_ON_WRITE",
		LegacyOn:    []string{"1", "true", "on", "yes"},
		LegacyOff:   []string{"0", "false"},
		// Today any non-empty value other than 0 or false turns the formatter on.
		LegacyAnyOtherOn: true,
	},
	{
		Name:        SessionsCheckpoints,
		Default:     true,
		Scope:       ScopeUser,
		Stability:   Stable,
		Description: "Capture file checkpoints before each mutating tool call.",
		ReadBy:      "sessions.CheckpointsEnabled",
		LegacyEnv:   "SPLICE_CHECKPOINTS",
		LegacyOn:    []string{"on"},
		LegacyOff:   []string{"off"},
		// Today anything other than exactly "off" leaves checkpoints on.
		LegacyAnyOtherOn: true,
	},
	{
		Name:        ProviderPromptCacheKey,
		Default:     true,
		Scope:       ScopeUser,
		Stability:   Stable,
		Description: "Send the session prompt cache key to the OpenAI-compatible provider.",
		ReadBy:      "openai.promptCacheKeyDisabled",
		LegacyEnv:   "SPLICE_DISABLE_PROMPT_CACHE_KEY",
		// The legacy variable is a disable switch, so its values are inverted.
		LegacyOn:  []string{"0", "false"},
		LegacyOff: []string{"1", "true", "on", "yes"},
		// Today any non-empty value other than 0 or false disables the key.
		LegacyAnyOtherOff: true,
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
