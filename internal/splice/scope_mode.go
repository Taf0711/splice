package splice

import (
	"fmt"
	"os"
	"strings"
)

// ScopeMode is the Part A ablation switch: whether the cognition scope
// changes context acquisition and tool behavior. "on" (default) applies
// the StageScopePlan to the context request and tool runner. "off" runs
// planDiscovery for telemetry only - the retrieval happens and is recorded,
// but the model's input and the host's context operations stay byte-
// identical to a cold run. This is the retrieval-only arm of the treatment
// matrix: it measures retrieval overhead and detects unintended
// control-flow side effects, which "no memory text" alone does not, because
// scope construction changes context acquisition separately from delivery.
type ScopeMode string

const (
	ScopeModeOn  ScopeMode = "on"
	ScopeModeOff ScopeMode = "off"
)

const scopeModeEnv = "SPLICE_SCOPE_MODE"

// resolveScopeMode reads the scope ablation mode from the environment.
// Unset or empty means "on" (the bridge is the feature under test).
// An invalid value is a loud configuration error naming the offender.
func resolveScopeMode() (ScopeMode, error) {
	raw := os.Getenv(scopeModeEnv)
	switch ScopeMode(raw) {
	case "":
		return ScopeModeOn, nil
	case ScopeModeOn:
		return ScopeModeOn, nil
	case ScopeModeOff:
		return ScopeModeOff, nil
	default:
		return "", fmt.Errorf("%s: invalid scope mode %q (want on or off)", scopeModeEnv, raw)
	}
}

// scopeEnabled reports whether the scope may change context acquisition
// and tool behavior for this run. SPLICE_TREATMENT takes precedence over
// SPLICE_SCOPE_MODE when set.
func scopeEnabled() (bool, error) {
	if raw := strings.TrimSpace(os.Getenv(treatmentEnv)); raw != "" {
		spec, err := ResolveTreatment(raw)
		if err != nil {
			return false, fmt.Errorf("%s: %w", treatmentEnv, err)
		}
		return spec.ScopeOnlyContext, nil
	}
	mode, err := resolveScopeMode()
	if err != nil {
		return false, err
	}
	return mode == ScopeModeOn, nil
}
