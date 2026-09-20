package splice

import "testing"

// A1 regression tests: typed effective treatment resolution. The three
// environment settings alone do not establish retrieval; every treatment
// must survive contradictory ambient settings; and the legacy warm-under-
// ambient-cold normalization keeps its historical meaning, labeled.

func effectiveDims(t *testing.T, eff EffectiveTreatmentSpec) (retrieval, delivery, policy bool) {
	t.Helper()
	if eff.Retrieval == nil || eff.PromptDelivery == nil || eff.ContextPolicy == nil {
		t.Fatalf("effective dimensions must be resolved, got %+v", eff)
	}
	return *eff.Retrieval, *eff.PromptDelivery, *eff.ContextPolicy
}

// TestEffectiveTreatmentEveryTreatmentSurvivesContradictoryArm pins the A1
// contract: the resolved effective treatment is independent of which arm
// runs it. A cold arm and a warm arm under the same explicit treatment keep
// that treatment's name and delivery/policy dimensions; only retrieval
// follows the arm's memory flag (it is the flag actually passed to the
// child).
func TestEffectiveTreatmentEveryTreatmentSurvivesContradictoryArm(t *testing.T) {
	treatments := []TreatmentName{
		TreatmentCold, TreatmentRetrievalOnly, TreatmentDeliveryOnly,
		TreatmentScopeOnly, TreatmentFull,
	}
	for _, name := range treatments {
		for _, arm := range []struct {
			memory string
			warm   bool
		}{{"off", false}, {"on", true}} {
			eff, err := ResolveEffectiveTreatment(arm.memory, string(name), "available")
			if err != nil {
				t.Fatalf("%s/%s: %v", name, arm.memory, err)
			}
			want, err := ResolveTreatment(string(name))
			if err != nil {
				t.Fatalf("resolve %s: %v", name, err)
			}
			// The historical exception: a warm arm under ambient cold
			// normalized to retrieval-only (tested separately below).
			wantName := want.Name
			if want.Name == TreatmentCold && arm.warm {
				wantName = TreatmentRetrievalOnly
			}
			if eff.Treatment != wantName {
				t.Errorf("%s arm=%s: effective treatment = %q, want %q", name, arm.memory, eff.Treatment, wantName)
			}
			if eff.RequestedTreatment != string(name) {
				t.Errorf("%s arm=%s: requested = %q, want %q", name, arm.memory, eff.RequestedTreatment, name)
			}
			_, delivery, policy := effectiveDims(t, eff)
			if want.Name != TreatmentCold || !arm.warm {
				wantDelivery := want.PromptDelivery()
				wantPolicy := want.ContextPolicy()
				if delivery != wantDelivery || policy != wantPolicy {
					t.Errorf("%s arm=%s: delivery=%v policy=%v, want %v/%v", name, arm.memory, delivery, policy, wantDelivery, wantPolicy)
				}
			}
			retrieval, _, _ := effectiveDims(t, eff)
			if retrieval != arm.warm {
				t.Errorf("%s arm=%s: retrieval = %v, want %v (the arm's memory flag realizes it)", name, arm.memory, retrieval, arm.warm)
			}
			if eff.StoreAvailable == nil || !*eff.StoreAvailable {
				t.Errorf("%s arm=%s: store availability lost", name, arm.memory)
			}
		}
	}
}

// TestEffectiveTreatmentWarmUnderAmbientColdNormalizes pins the legacy
// special case: a warm arm under ambient SPLICE_TREATMENT=cold meant
// retrieval-only (bdc3344 semantics). The normalization is preserved AND
// labeled, so old scripts' meaning never silently changes.
func TestEffectiveTreatmentWarmUnderAmbientColdNormalizes(t *testing.T) {
	eff, err := ResolveEffectiveTreatment("on", "cold", "available")
	if err != nil {
		t.Fatal(err)
	}
	if eff.Treatment != TreatmentRetrievalOnly {
		t.Fatalf("warm under ambient cold = %q, want retrieval-only", eff.Treatment)
	}
	if eff.RequestedTreatment != "cold" {
		t.Fatalf("requested = %q, want cold", eff.RequestedTreatment)
	}
	if eff.NormalizedFromAmbient != "cold" {
		t.Fatalf("normalization must be labeled, got %q", eff.NormalizedFromAmbient)
	}
	retrieval, _, _ := effectiveDims(t, eff)
	if !retrieval {
		t.Fatal("retrieval-only normalizes to retrieval ON")
	}
	// A cold arm under ambient cold stays cold, unlabeled.
	coldEff, err := ResolveEffectiveTreatment("off", "cold", "available")
	if err != nil {
		t.Fatal(err)
	}
	if coldEff.Treatment != TreatmentCold || coldEff.NormalizedFromAmbient != "" {
		t.Fatalf("cold under ambient cold = %+v, want plain cold", coldEff)
	}
}

// TestEffectiveTreatmentStoreAvailabilityGatesRetrieval pins the A1 store
// dimension: the three environment settings alone do not establish
// retrieval. A warm arm with an unavailable store realizes retrieval OFF,
// and an unknown store leaves retrieval unknown (nil, never a fabricated
// false).
func TestEffectiveTreatmentStoreAvailabilityGatesRetrieval(t *testing.T) {
	eff, err := ResolveEffectiveTreatment("on", "full", "unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if eff.Retrieval == nil || *eff.Retrieval {
		t.Fatalf("warm with unavailable store: retrieval = %v, want false", eff.Retrieval)
	}
	if eff.StoreAvailable == nil || *eff.StoreAvailable {
		t.Fatalf("store availability = %v, want false", eff.StoreAvailable)
	}
	// Unknown store: the memory flag still realizes retrieval (it was
	// actually passed to the child), and availability stays its own
	// unknown dimension. Unknown is never folded into a fabricated
	// false.
	unknown, err := ResolveEffectiveTreatment("on", "full", "")
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Retrieval == nil || !*unknown.Retrieval {
		t.Fatalf("unknown store: the arm flag still realizes retrieval, got %v", unknown.Retrieval)
	}
	if unknown.StoreAvailable != nil {
		t.Fatalf("unknown store must leave availability unknown, got %v", *unknown.StoreAvailable)
	}
}

// TestEffectiveTreatmentUnresolvableAmbientFailsLoud pins fail-loud
// configuration: an unresolvable ambient treatment is an error naming the
// offender, never a silent default that poisons the comparison.
func TestEffectiveTreatmentUnresolvableAmbientFailsLoud(t *testing.T) {
	if _, err := ResolveEffectiveTreatment("on", "wizard-mode", "available"); err == nil {
		t.Fatal("unresolvable ambient treatment must error")
	}
}

// TestEffectiveTreatmentNoTreatmentNamesLegacyShape pins the legacy
// no-treatment shape: dimensions follow the arm's memory flag, delivery and
// policy stay unknown (the environment knobs were not pinned), and no
// treatment name is fabricated.
func TestEffectiveTreatmentNoTreatmentNamesLegacyShape(t *testing.T) {
	eff, err := ResolveEffectiveTreatment("on", "", "available")
	if err != nil {
		t.Fatal(err)
	}
	if eff.Treatment != "" || eff.RequestedTreatment != "" {
		t.Fatalf("legacy shape must not fabricate a treatment name, got %+v", eff)
	}
	if eff.Retrieval == nil || !*eff.Retrieval {
		t.Fatal("warm arm with available store: retrieval is a measured true")
	}
	if eff.PromptDelivery != nil || eff.ContextPolicy != nil {
		t.Fatalf("legacy shape leaves delivery/policy unknown, got %+v", eff)
	}
}
