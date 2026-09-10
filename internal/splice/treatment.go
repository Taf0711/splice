package splice

import (
	"fmt"
	"strings"
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
//
// Retrieval is gated by the exec --memory flag, so this reports what
// PromptMemory realizes rather than a second opinion about it. Cold is the
// only treatment with --memory off, and off is the deliberate-cold path:
// nil memory store, no retrieval, no scope construction.
func (t TreatmentSpec) MemoryRetrieval() bool {
	return t.PromptMemory == "on"
}

// PromptDelivery reports whether cognition prose reaches the model. It is
// the delivery dimension of the treatment triple, and it is owned by the
// exemplar mode: retrieve-no-prompt retrieves without delivering, which is
// how retrieval-only and scope-only suppress prompt text while keeping
// retrieval and its downstream effects real.
func (t TreatmentSpec) PromptDelivery() bool {
	return t.ExemplarMode.deliverToModel()
}

// ContextPolicy reports whether the scope context policy applies. It is the
// context dimension of the triple and maps to SPLICE_SCOPE_MODE.
func (t TreatmentSpec) ContextPolicy() bool {
	return t.ScopeOnlyContext
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
		// PromptMemory is "on" so RETRIEVAL RUNS. With "off", exec takes
		// the deliberate-cold path (nil memory store, no retrieval, no
		// scope construction), so scope-only degenerated into cold and
		// measured nothing it claimed to measure. Prompt delivery is
		// suppressed by the exemplar mode instead: retrieve-no-prompt
		// retrieves and records, but delivers no cognition prose to the
		// model. The realized triple is (retrieval on, delivery off,
		// scope on), which is the treatment's contract.
		return TreatmentSpec{
			Name:             TreatmentScopeOnly,
			ScopeOnlyContext: true,
			PromptMemory:     "on",
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

// EffectiveTreatmentSpec records what one attempt's treatment ACTUALLY was:
// the resolved treatment name, its three causal dimensions (each a pointer
// so false is a measured fact and nil is unknown), and the memory store
// availability that gates retrieval independently of every environment
// setting. Requested and effective stay separate: the requested names come
// from the run configuration, the effective dimensions from the resolved
// spec plus the store the child will actually see.
//
// The three environment settings (SPLICE_TREATMENT, SPLICE_SCOPE_MODE,
// SPLICE_EXEMPLAR_MODE) alone do not establish retrieval: a warm-labeled
// arm with no usable memory store realizes retrieval OFF no matter what
// the knobs say. StoreAvailable carries that fact so a row never claims a
// realized condition the child could not run.
type EffectiveTreatmentSpec struct {
	// Treatment is the resolved treatment name. Empty only when no
	// treatment name was in play (a legacy ambient invocation).
	Treatment TreatmentName
	// Retrieval is whether graph/FTS retrieval actually runs: the exec
	// --memory flag realized (the arm's memory argument) AND a usable
	// memory store. nil means the caller could not determine it.
	Retrieval *bool
	// PromptDelivery is whether cognition prose reaches the model. nil
	// means unknown.
	PromptDelivery *bool
	// ContextPolicy is whether the scope context policy applies. nil
	// means unknown.
	ContextPolicy *bool
	// StoreAvailable is whether a usable memory store backs this
	// attempt. nil means the caller could not determine it. It is a
	// first-class dimension, not part of the treatment name: the same
	// treatment runs with and without a store, and the difference is
	// causal.
	StoreAvailable *bool
	// RequestedTreatment is the treatment name the run configuration
	// asked for, before any ambient normalization. It differs from
	// Treatment only on the legacy ambient path.
	RequestedTreatment string
	// NormalizedFromAmbient names the ambient SPLICE_TREATMENT value
	// this effective spec was normalized from, when the caller resolved
	// a legacy ambient setting instead of an explicit run-configuration
	// treatment. Empty for explicit-treatment launches. The label keeps
	// old scripts' meaning visible instead of silently relabeled.
	NormalizedFromAmbient string
}

// boolPtr returns a pointer to b (helper for the three dimension fields).
func boolPtr(b bool) *bool { return &b }

// ResolveEffectiveTreatment resolves one attempt's typed treatment ONCE,
// pre-launch, from the arm's memory argument and the ambient
// SPLICE_TREATMENT value. Two shapes, one code path:
//
//   - explicit: ambientTreatment names a resolvable treatment (the run
//     configuration set it, or the operator did and the historical
//     semantics map it). The spec's declared dimensions are the effective
//     dimensions.
//   - legacy ambient: ambientTreatment is empty or unresolvable. The
//     treatment name stays empty (never a fabricated name) and the
//     dimensions fall back to what the arm's memory flag realizes. A
//     WARM arm under ambient SPLICE_TREATMENT=cold normalizes to
//     retrieval-only and labels the normalization: that special case is
//     the historical behavior (bdc3344 R1-R3 era), preserved here rather
//     than silently changed.
//
// storeAvailable feeds retrieval directly: retrieval = memoryFlag &&
// storeAvailable when both are known. An unknown store leaves retrieval
// nil (unknown, not false).
func ResolveEffectiveTreatment(armMemory, ambientTreatment, storeAvailable string) (EffectiveTreatmentSpec, error) {
	eff := EffectiveTreatmentSpec{}
	armRetrieval := armMemory == "on"

	ambient := strings.TrimSpace(ambientTreatment)
	if ambient == "" {
		// No treatment name anywhere: legacy shape. Dimensions follow
		// the arm's memory flag; delivery and policy are unknown
		// because the environment knobs were not pinned.
		eff.RequestedTreatment = ""
		if storeKnown := storeAvailable != ""; storeKnown {
			eff.StoreAvailable = boolPtr(storeAvailable == "available")
			eff.Retrieval = boolPtr(armRetrieval && *eff.StoreAvailable)
		}
		return eff, nil
	}

	spec, err := ResolveTreatment(ambient)
	if err != nil {
		// An unresolvable ambient treatment is a loud configuration
		// error: spending a live attempt under it would poison the
		// comparison.
		return EffectiveTreatmentSpec{}, err
	}
	eff.RequestedTreatment = ambient
	eff.Treatment = spec.Name
	eff.PromptDelivery = boolPtr(spec.PromptDelivery())
	eff.ContextPolicy = boolPtr(spec.ContextPolicy())
	if storeAvailable != "" {
		eff.StoreAvailable = boolPtr(storeAvailable == "available")
	}
	// Historical special case, preserved and labeled: a warm arm under
	// ambient SPLICE_TREATMENT=cold meant retrieval-only. The declared
	// treatment's retrieval dimension (cold: off) contradicts the arm's
	// memory flag (warm: on), and the ARM won (the flag is what the
	// child actually received). Normalize to retrieval-only so the row
	// names the realized condition, and mark where the name came from.
	if spec.Name == TreatmentCold && armRetrieval {
		normalized, nerr := ResolveTreatment(string(TreatmentRetrievalOnly))
		if nerr != nil {
			return EffectiveTreatmentSpec{}, nerr
		}
		eff.Treatment = normalized.Name
		eff.PromptDelivery = boolPtr(normalized.PromptDelivery())
		eff.ContextPolicy = boolPtr(normalized.ContextPolicy())
		eff.NormalizedFromAmbient = ambient
	}
	// The arm's memory flag realizes retrieval, gated by store
	// availability when known. This is the actual retrieval fact, not
	// the treatment's declared dimension.
	if eff.StoreAvailable != nil {
		eff.Retrieval = boolPtr(armRetrieval && *eff.StoreAvailable)
	} else {
		eff.Retrieval = boolPtr(armRetrieval)
	}
	return eff, nil
}
