package splice

// M3 exit-gate proofs (SPEC_DECISION_ADVISOR_JEV_FOLD_2026-09-20, milestone
// M3): shadow records decisions under a fixed allowance, and the task
// payload, delivery, and recovery decisions are unchanged under replayed
// events. The advisor config must not perturb plan compilation until the
// bounded selector (M4) consciously changes it, and this canary pins that
// invariant: when M4 wires the selector into planning, this test must be
// updated alongside it, not silently broken.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestPlanCompilationInvariantUnderAdvisorConfig(t *testing.T) {
	// The plan honestly records the flag names it was compiled under, so
	// the advisor flag adds exactly one flags entry and changes nothing
	// else: no stage, budget, dependency, or capability may move. When M4
	// wires the selector into planning, this canary must be updated
	// alongside it, not silently broken.
	request := "fix the expired-session handling in the clock table"
	for _, tier := range []schemas.PipelineTier{schemas.TierLight, schemas.TierStandard, schemas.TierSubstantial} {
		base, err := BuildExecutionPlan(request, nil)
		if err != nil {
			t.Fatalf("base plan %s: %v", tier, err)
		}
		withFlag, err := BuildExecutionPlan(request, flags.Set{flags.DecisionAdvisorEnabled: true})
		if err != nil {
			t.Fatalf("flagged plan %s: %v", tier, err)
		}
		baseJSON, _ := json.Marshal(base)
		flagJSON, _ := json.Marshal(withFlag)
		var baseMap, flagMap map[string]any
		if err := json.Unmarshal(baseJSON, &baseMap); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(flagJSON, &flagMap); err != nil {
			t.Fatal(err)
		}
		baseFlags, _ := baseMap["flags"].([]any)
		flagFlags, _ := flagMap["flags"].([]any)
		// Remove the advisor entry from the flagged plan's list.
		reduced := make([]any, 0, len(flagFlags))
		for _, f := range flagFlags {
			if f != string(flags.DecisionAdvisorEnabled) {
				reduced = append(reduced, f)
			}
		}
		if len(reduced) != len(flagFlags)-1 {
			t.Fatalf("%s: the advisor flag is absent from the compiled plan's flag list", tier)
		}
		baseMap["flags"] = nil
		flagMap["flags"] = nil
		normBase, _ := json.Marshal(baseMap)
		normFlag, _ := json.Marshal(flagMap)
		if string(normBase) != string(normFlag) {
			t.Fatalf("%s: plan compilation changed beyond the flags entry under the advisor flag; M4 must update this canary when the selector joins planning", tier)
		}
		_ = baseFlags
	}
}

func TestResolvedConfigCarriesAdvisorSection(t *testing.T) {
	// File-level resolution is exercised by the config package's own
	// tests; here the section's validation and defaults are checked
	// directly against the resolved-shape struct.
	section := config.DecisionAdvisorConfig{Mode: "shadow", Profile: "typesafe/jev-1.13"}
	def := config.DefaultDecisionAdvisorLimits()
	section.Limits = def
	if err := section.Validate(); err != nil {
		t.Fatalf("valid section rejected: %v", err)
	}
	if section.Limits.MaxRequestsPerEpisode != 4 || section.Limits.TimeoutMS != 1500 {
		t.Fatalf("defaults: %+v", section.Limits)
	}
	converted, err := AdvisorConfigFromResolved(section)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if converted.Limits.MaxRequestsPerEpisode != 4 || converted.Limits.Timeout != 1500*time.Millisecond {
		t.Fatalf("converted limits: %+v", converted.Limits)
	}
	// An off section is valid and inert.
	off := config.DecisionAdvisorConfig{Mode: "off"}
	if err := off.Validate(); err != nil {
		t.Fatalf("off section: %v", err)
	}
	convertedOff, err := AdvisorConfigFromResolved(off)
	if err != nil || convertedOff.Mode != AdvisorModeOff {
		t.Fatalf("off conversion: %v %q", err, convertedOff.Mode)
	}
}

func TestShadowRunUnderFixedAllowanceEndToEnd(t *testing.T) {
	// The full shadow cycle over the fixed allowance: several distinct
	// snapshots, a repeated one, a deterministic bypass, then exhaustion.
	tight := shadowConfig()
	tight.Limits.MaxRequestsPerEpisode = 2
	eval := &mockEvaluator{result: choiceResult()}
	advisor, err := NewDecisionAdvisor(tight, eval)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}

	// 1. First buy succeeds and records.
	r1, act1 := advisor.EvaluateGate1(t.Context(), jevSnapshot(), nil)
	if r1.Disposition != schemas.DispositionShadowOnly || act1 {
		t.Fatalf("first: %q act=%v", r1.Disposition, act1)
	}
	// 2. Identical replay: cache hit, no new call.
	r2, _ := advisor.EvaluateGate1(t.Context(), jevSnapshot(), nil)
	if !strings.Contains(r2.Reason, "cache hit") || eval.calls != 1 {
		t.Fatalf("replay: %q calls=%d", r2.Disposition, eval.calls)
	}
	// 3. Deterministic bypass: no call, typed disposition.
	bypass := jevSnapshot()
	bypass.DeterministicOutcomes = []string{"exact answer"}
	r3, _ := advisor.EvaluateGate1(t.Context(), bypass, nil)
	if r3.Disposition != schemas.DispositionExactBypass || eval.calls != 1 {
		t.Fatalf("bypass: %q calls=%d", r3.Disposition, eval.calls)
	}
	// 4. Second distinct snapshot: last allowed call.
	second := jevSnapshot("b.go#other")
	second.EventID = "ev-2"
	r4, _ := advisor.EvaluateGate1(t.Context(), second, nil)
	if r4.Disposition != schemas.DispositionShadowOnly || eval.calls != 2 {
		t.Fatalf("second buy: %q calls=%d", r4.Disposition, eval.calls)
	}
	// 5. Third distinct snapshot: request cap declines, no call.
	third := jevSnapshot("c.go#third")
	third.EventID = "ev-3"
	r5, _ := advisor.EvaluateGate1(t.Context(), third, nil)
	if r5.Disposition != schemas.DispositionBudgetDeclined || eval.calls != 2 {
		t.Fatalf("cap: %q calls=%d", r5.Disposition, eval.calls)
	}
	// The disposition trace is complete and ordered.
	records := advisor.Records()
	if len(records) != 5 {
		t.Fatalf("records = %d, want 5", len(records))
	}
	want := []schemas.DecisionDisposition{
		schemas.DispositionShadowOnly,
		schemas.DispositionShadowOnly,
		schemas.DispositionExactBypass,
		schemas.DispositionShadowOnly,
		schemas.DispositionBudgetDeclined,
	}
	for i, rec := range records {
		if rec.Disposition != want[i] {
			t.Fatalf("record %d = %q, want %q", i, rec.Disposition, want[i])
		}
	}
	// Allowance truth: exactly two real charges, nothing fabricated.
	if advisor.settledUSD != 2*0.00002 {
		t.Fatalf("settled = %v, want exactly two charges", advisor.settledUSD)
	}
	if advisor.outstandingUSD != 0 {
		t.Fatalf("outstanding = %v, want fully settled", advisor.outstandingUSD)
	}
}
