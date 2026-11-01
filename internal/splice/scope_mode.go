package splice

import (
	"fmt"
	"os"
	"strings"

	"github.com/Taf0711/splice/internal/flags"
)

// ScopeMode is the Part A ablation switch: whether the cognition scope
// changes context acquisition and tool behavior. "off" (default) runs
// planDiscovery for telemetry only - the retrieval happens and is recorded,
// but the model's input and the host's context operations stay byte-
// identical to a cold run. This is the retrieval-only arm of the treatment
// matrix: it measures retrieval overhead and detects unintended
// control-flow side effects, which "no memory text" alone does not, because
// scope construction changes context acquisition separately from delivery.
// "on" applies the StageScopePlan to the context request and tool runner.
type ScopeMode string

const (
	ScopeModeOn  ScopeMode = "on"
	ScopeModeOff ScopeMode = "off"
)

// ScopeModeEnvVar is the legacy name of the memory.scope flag. The eval
// harness uses it to filter child environments.
const ScopeModeEnvVar = "SPLICE_SCOPE_MODE"

const scopeModeEnv = ScopeModeEnvVar

// RealizedDimensions reports what the PROCESS ENVIRONMENT actually
// realizes, as opposed to what a treatment name declares. It runs the same
// resolvers the production paths use, so a caller that constructs a child
// environment can prove the child will behave as requested instead of
// asserting only on the strings it wrote.
//
// This exists because SPLICE_TREATMENT outranks SPLICE_EXEMPLAR_MODE and
// SPLICE_SCOPE_MODE: an env that merely CONTAINS the right knob values can
// still realize a different treatment when an ambient shorthand survives.
type RealizedDimensions struct {
	ScopeOn        bool
	ExemplarMode   ExemplarMode
	PromptDelivery bool
}

// RealizedTreatmentDimensions resolves the ambient environment through the
// production resolvers. An invalid environment is a loud error, matching
// the resolvers' own contract.
//
// The child process resolves its own flags from the environment it inherits,
// so this proof uses the same environment-only sources. User and project
// config are not part of it: a child reads them from its own config files.
func RealizedTreatmentDimensions() (RealizedDimensions, error) {
	mode, err := resolveExemplarMode()
	if err != nil {
		return RealizedDimensions{}, err
	}
	set, err := flags.Resolve(flags.Sources{
		LegacyEnv: flags.LegacyEnvValues(os.Getenv),
		Env:       os.Getenv(flags.EnvVar),
	})
	if err != nil {
		return RealizedDimensions{}, err
	}
	scope, err := scopeEnabled(set)
	if err != nil {
		return RealizedDimensions{}, err
	}
	return RealizedDimensions{
		ScopeOn:        scope,
		ExemplarMode:   mode,
		PromptDelivery: mode.deliverToModel(),
	}, nil
}

// scopeEnabled reports whether the scope may change context acquisition
// and tool behavior for this run. SPLICE_TREATMENT takes precedence over
// the memory.scope flag when set. Invalid treatments fail loud.
func scopeEnabled(set flags.Set) (bool, error) {
	if raw := strings.TrimSpace(os.Getenv(treatmentEnv)); raw != "" {
		spec, err := ResolveTreatment(raw)
		if err != nil {
			return false, fmt.Errorf("%s: %w", treatmentEnv, err)
		}
		return spec.ScopeOnlyContext, nil
	}
	return set.Enabled(flags.MemoryScope), nil
}
