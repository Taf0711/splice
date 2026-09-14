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
	// ExemplarModeRetrieveNoPrompt runs full retrieval and records what was
	// retrieved, but delivers NO cognition prose to the model. It measures
	// control reuse (deterministic downstream effects of retrieval) with
	// reasoning reuse (prompt text) removed. It is an ablation mode only.
	ExemplarModeRetrieveNoPrompt ExemplarMode = "retrieve-no-prompt"
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
	case ExemplarModeBoth, ExemplarModeObsOnly, ExemplarModeExemplarOnly, ExemplarModeNone, ExemplarModeRetrieveNoPrompt:
		return ExemplarMode(raw), nil
	default:
		return "", fmt.Errorf("%s: invalid exemplar mode %q (want one of both, obs-only, exemplar-only, none, retrieve-no-prompt)", exemplarModeEnv, raw)
	}
}

// deliverObservations reports whether the mode includes miss-path
// observations. retrieve-no-prompt retrieves observations (the retrieval
// side effects and telemetry stay real) but never delivers them.
func (m ExemplarMode) deliverObservations() bool {
	return m == ExemplarModeBoth || m == ExemplarModeObsOnly
}

// deliverExemplars reports whether the mode retrieves miss-path exemplars.
func (m ExemplarMode) deliverExemplars() bool {
	return m == ExemplarModeBoth || m == ExemplarModeExemplarOnly || m == ExemplarModeRetrieveNoPrompt
}

// deliverToModel reports whether ANY cognition prose may reach the model.
// retrieve-no-prompt is the only mode that retrieves without delivering.
func (m ExemplarMode) deliverToModel() bool {
	return m != ExemplarModeRetrieveNoPrompt
}
