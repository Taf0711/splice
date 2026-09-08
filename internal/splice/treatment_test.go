package splice

// Treatment matrix and control-separation pins. The five-treatment
// diagnostic matrix must resolve to knob values that differ only in the
// declared memory/context dimensions, and scope-off must never be
// conflated with prompt-memory off (F11).

import (
	"strings"
	"testing"
)

func TestResolveTreatmentMatrix(t *testing.T) {
	tests := []struct {
		name         string
		wantScope    bool
		wantMemory   string
		wantExemplar ExemplarMode
	}{
		{"cold", false, "off", ExemplarModeRetrieveNoPrompt},
		{"retrieval-only", false, "on", ExemplarModeRetrieveNoPrompt},
		{"delivery-only", false, "on", ExemplarModeBoth},
		{"scope-only", true, "off", ExemplarModeRetrieveNoPrompt},
		{"full", true, "on", ExemplarModeBoth},
	}
	for _, tc := range tests {
		spec, err := ResolveTreatment(tc.name)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if spec.Name != TreatmentName(tc.name) {
			t.Fatalf("%s: name = %q", tc.name, spec.Name)
		}
		if spec.ScopeOnlyContext != tc.wantScope {
			t.Fatalf("%s: scope = %v, want %v", tc.name, spec.ScopeOnlyContext, tc.wantScope)
		}
		if spec.PromptMemory != tc.wantMemory {
			t.Fatalf("%s: memory = %q, want %q", tc.name, spec.PromptMemory, tc.wantMemory)
		}
		if spec.ExemplarMode != tc.wantExemplar {
			t.Fatalf("%s: exemplar = %q, want %q", tc.name, spec.ExemplarMode, tc.wantExemplar)
		}
		if got := spec.MemoryRetrieval(); got != (tc.name != "cold") {
			t.Fatalf("%s: MemoryRetrieval = %v", tc.name, got)
		}
	}
}

func TestResolveTreatmentUnknownFailsLoud(t *testing.T) {
	if _, err := ResolveTreatment("warm-ish"); err == nil {
		t.Fatal("unknown treatment must fail loud")
	} else if !strings.Contains(err.Error(), "warm-ish") {
		t.Fatalf("error must name the offender, got %v", err)
	}
}

func TestTreatmentEnvironmentDisjointKnobs(t *testing.T) {
	// The treatment's environment entries carry the scope and exemplar
	// knobs as a self-contained list: nothing mutates the parent
	// environment, so concurrent arms cannot observe each other's knobs.
	full, err := ResolveTreatment("full")
	if err != nil {
		t.Fatal(err)
	}
	env := full.Environment()
	joined := strings.Join(env, ";")
	if !strings.Contains(joined, "SPLICE_SCOPE_MODE=on") || !strings.Contains(joined, "SPLICE_EXEMPLAR_MODE=both") {
		t.Fatalf("full treatment env wrong: %v", env)
	}
	cold, err := ResolveTreatment("cold")
	if err != nil {
		t.Fatal(err)
	}
	coldEnv := strings.Join(cold.Environment(), ";")
	if !strings.Contains(coldEnv, "SPLICE_SCOPE_MODE=off") || !strings.Contains(coldEnv, "SPLICE_EXEMPLAR_MODE=retrieve-no-prompt") {
		t.Fatalf("cold treatment env wrong: %v", cold.Environment())
	}
	// Scope-off must NOT imply prompt memory off: cold and delivery-only
	// share the scope knob value but differ in the memory flag.
	delivery, err := ResolveTreatment("delivery-only")
	if err != nil {
		t.Fatal(err)
	}
	if delivery.ScopeOnlyContext == cold.ScopeOnlyContext && delivery.PromptMemory == cold.PromptMemory {
		t.Fatal("delivery-only must differ from cold in the memory dimension")
	}
}

func TestTreatmentMatrixDistinguishesDeclaredDimensionsOnly(t *testing.T) {
	// Treatments sharing a dimension must share its knob value exactly.
	// The context dimension is ScopeOnlyContext; the memory dimension is
	// (PromptMemory, ExemplarMode) together. retrieval-only and
	// delivery-only differ ONLY in the memory dimension; scope-only and
	// full differ ONLY in the memory dimension too.
	retr, _ := ResolveTreatment("retrieval-only")
	delivery, _ := ResolveTreatment("delivery-only")
	if retr.ScopeOnlyContext != delivery.ScopeOnlyContext {
		t.Fatal("retrieval-only and delivery-only must share the context dimension")
	}
	if retr.ExemplarMode == delivery.ExemplarMode {
		t.Fatal("retrieval-only and delivery-only must differ in the memory dimension")
	}
	scopeOnly, _ := ResolveTreatment("scope-only")
	full, _ := ResolveTreatment("full")
	if scopeOnly.ScopeOnlyContext != full.ScopeOnlyContext {
		t.Fatal("scope-only and full must share the context dimension")
	}
	if scopeOnly.ExemplarMode == full.ExemplarMode {
		t.Fatal("scope-only and full must differ in the memory dimension")
	}
	// Cold and retrieval-only share the baseline context policy and differ
	// only in whether retrieval runs and its telemetry path.
	cold, _ := ResolveTreatment("cold")
	if cold.ScopeOnlyContext != retr.ScopeOnlyContext {
		t.Fatal("cold and retrieval-only must share the baseline context dimension")
	}
}

func TestTreatmentOverridesIndividualKnobs(t *testing.T) {
	// SPLICE_TREATMENT takes precedence: the declared treatment and the
	// realized delivery/context cannot disagree through mixed knob state.
	t.Setenv(treatmentEnv, "retrieval-only")
	t.Setenv(exemplarModeEnv, "both")           // contradicts the treatment
	t.Setenv(scopeModeEnv, string(ScopeModeOn)) // contradicts the treatment
	mode, err := resolveExemplarMode()
	if err != nil {
		t.Fatal(err)
	}
	if mode != ExemplarModeRetrieveNoPrompt {
		t.Fatalf("exemplar mode = %q, want the treatment's retrieve-no-prompt", mode)
	}
	scope, err := scopeEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if scope {
		t.Fatal("scope must be off under retrieval-only despite SPLICE_SCOPE_MODE=on")
	}
	// An invalid treatment name fails loud even when individual knobs
	// would have been valid.
	t.Setenv(treatmentEnv, "banana")
	if _, err := resolveExemplarMode(); err == nil {
		t.Fatal("invalid treatment must fail loud")
	}
	if _, err := scopeEnabled(); err == nil {
		t.Fatal("invalid treatment must fail loud for scope too")
	}
}
