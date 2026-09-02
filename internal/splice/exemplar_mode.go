package splice

import (
	"fmt"
	"os"
	"strings"
)

// ExemplarMode is the C1c ablation switch (report section 30): which memory
// classes the miss path delivers. The default reproduces current behavior
// exactly; the other modes exist for the 4-way ablation benchmark. The mode
// is recorded on the trace so benchmark results group by mode.
type ExemplarMode string

const (
	// ExemplarModeBoth delivers observations AND exemplars (today's behavior).
	ExemplarModeBoth ExemplarMode = "both"
	// ExemplarModeObsOnly delivers observations only.
	ExemplarModeObsOnly ExemplarMode = "obs-only"
	// ExemplarModeExemplarOnly delivers exemplars only (observations still
	// serve the direct path; this mode governs the miss-path injection).
	ExemplarModeExemplarOnly ExemplarMode = "exemplar-only"
	// ExemplarModeNone delivers neither on the miss path.
	ExemplarModeNone ExemplarMode = "none"
)

const exemplarModeEnv = "SPLICE_EXEMPLAR_MODE"

// resolveExemplarMode reads the ablation mode from the environment. Unset or
// empty means "both" (today's behavior, so the ablation is strictly opt-in).
// An invalid value is a loud configuration error naming the offender: a
// silently wrong mode would poison benchmark attribution.
func resolveExemplarMode() (ExemplarMode, error) {
	raw := strings.TrimSpace(os.Getenv(exemplarModeEnv))
	switch ExemplarMode(raw) {
	case "":
		return ExemplarModeBoth, nil
	case ExemplarModeBoth, ExemplarModeObsOnly, ExemplarModeExemplarOnly, ExemplarModeNone:
		return ExemplarMode(raw), nil
	default:
		return "", fmt.Errorf("%s: invalid exemplar mode %q (want one of both, obs-only, exemplar-only, none)", exemplarModeEnv, raw)
	}
}

// deliverObservations reports whether the mode includes miss-path
// observations.
func (m ExemplarMode) deliverObservations() bool {
	return m == ExemplarModeBoth || m == ExemplarModeObsOnly
}

// deliverExemplars reports whether the mode includes exemplars.
func (m ExemplarMode) deliverExemplars() bool {
	return m == ExemplarModeBoth || m == ExemplarModeExemplarOnly
}
