package splice

import (
	"fmt"
)

// Typed treatment specification for the cognition diagnostic experiment
// matrix. Each treatment names the causal dimensions it toggles so the
// experiment manifest, the run telemetry, and the report all describe the
// same condition. The treatment resolves to the existing environment knobs
// (SPLICE_SCOPE_MODE, SPLICE_EXEMPLAR_MODE, the exec --memory flag); it
// never introduces a second control plane.

// TreatmentName identifies one cell of the diagnostic matrix.
type TreatmentName string

const (
	// TreatmentCold: no retrieval, no prompt memory, baseline context.
	TreatmentCold TreatmentName = "cold"
	// TreatmentRetrievalOnly: retrieval runs and is recorded, but no
	// cognition prose reaches the model; context stays baseline.
	TreatmentRetrievalOnly TreatmentName = "retrieval-only"
	// TreatmentDeliveryOnly: retrieval runs and the memory text is
	// delivered, but context acquisition stays baseline.
	TreatmentDeliveryOnly TreatmentName = "delivery-only"
	// TreatmentScopeOnly: retrieval runs, no prompt memory, and the
	// context policy applies.
	TreatmentScopeOnly TreatmentName = "scope-only"
	// TreatmentFull: retrieval, prompt memory, and context policy all on.
	TreatmentFull TreatmentName = "full"
)

// TreatmentSpec is the resolved knob configuration for one treatment. The
// struct is the experiment manifest's unit of record: every attempt row
// carries the resolved values so the analysis can verify the treatment
// actually held. ScopeOnlyContext maps to SPLICE_SCOPE_MODE; PromptMemory
// is the exec --memory flag; ExemplarMode maps to SPLICE_EXEMPLAR_MODE.
type TreatmentSpec struct {
	Name             TreatmentName
	ScopeOnlyContext bool         // SPLICE_SCOPE_MODE=on when true, off when false
	PromptMemory     string       // exec --memory flag value: "on" or "off"
	ExemplarMode     ExemplarMode // SPLICE_EXEMPLAR_MODE value
}

// MemoryRetrieval reports whether the treatment runs graph/FTS retrieval.
// Retrieval runs in every treatment except cold: the scope-off telemetry
// path still plans discovery, so retrieval-only, delivery-only, scope-only,
// and full all retrieve. Cold never builds a sidecar trace.
func (t TreatmentSpec) MemoryRetrieval() bool {
	return t.Name != TreatmentCold
}

// ResolveTreatment maps a treatment name to its knob configuration.
// An unknown name is a loud configuration error naming the offender:
// spending live model budget under a silently-defaulted treatment would
// poison the comparison.
func ResolveTreatment(name string) (TreatmentSpec, error) {
	switch TreatmentName(name) {
	case TreatmentCold:
		return TreatmentSpec{
			Name:             TreatmentCold,
			ScopeOnlyContext: false,
			PromptMemory:     "off",
			ExemplarMode:     ExemplarModeRetrieveNoPrompt,
		}, nil
	case TreatmentRetrievalOnly:
		return TreatmentSpec{
			Name:             TreatmentRetrievalOnly,
			ScopeOnlyContext: false,
			PromptMemory:     "on",
			ExemplarMode:     ExemplarModeRetrieveNoPrompt,
		}, nil
	case TreatmentDeliveryOnly:
		return TreatmentSpec{
			Name:             TreatmentDeliveryOnly,
			ScopeOnlyContext: false,
			PromptMemory:     "on",
			ExemplarMode:     ExemplarModeBoth,
		}, nil
	case TreatmentScopeOnly:
		return TreatmentSpec{
			Name:             TreatmentScopeOnly,
			ScopeOnlyContext: true,
			PromptMemory:     "off",
			ExemplarMode:     ExemplarModeRetrieveNoPrompt,
		}, nil
	case TreatmentFull:
		return TreatmentSpec{
			Name:             TreatmentFull,
			ScopeOnlyContext: true,
			PromptMemory:     "on",
			ExemplarMode:     ExemplarModeBoth,
		}, nil
	default:
		return TreatmentSpec{}, fmt.Errorf("treatment: unknown treatment %q (want one of cold, retrieval-only, delivery-only, scope-only, full)", name)
	}
}

// Environment returns the subprocess environment entries that realize this
// treatment. The caller appends these to the child's environment; nothing
// here mutates the parent process environment, so concurrent arms cannot
// observe each other's knobs.
func (t TreatmentSpec) Environment() []string {
	scope := ScopeModeOff
	if t.ScopeOnlyContext {
		scope = ScopeModeOn
	}
	return []string{
		scopeModeEnv + "=" + string(scope),
		exemplarModeEnv + "=" + string(t.ExemplarMode),
	}
}
