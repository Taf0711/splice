package warmcost

// M5 offline proofs (SPEC_DECISION_SELECTOR_M5_DIAGNOSTIC_2026-09-22
// Section 5). These run provider-free and fast; the paid campaign is a
// release-cadence tool, never a CI test. Proof 1 pins the arm matrix: WJ is
// W0 plus exactly the selector switches, so the paired comparison isolates
// the selector and no ambient value can leak into a measurement.

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// nullEvaluator stands in for a built provider transport. The realization
// check never makes a call.
type nullEvaluator struct{}

func (nullEvaluator) Evaluate(context.Context, zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	return zeroruntime.DecisionResult{}, nil
}

// TestEveryArmRealizesItsAdvisorDefinition resolves each arm's environment
// with a ChatGPT sign-in profile configured. The default classifier must not
// reach any arm, and the advisor each arm realizes must match its definition.
func TestEveryArmRealizesItsAdvisorDefinition(t *testing.T) {
	providers := []config.ProviderProfile{{Name: "chatgpt-61", CatalogID: "chatgpt", Model: "gpt-6.1-sol"}}
	for _, arm := range []Arm{ArmCold, ArmColdWide, ArmWarm, ArmRetrievalOnly, ArmLoop, ArmWarmJev} {
		t.Run(string(arm), func(t *testing.T) {
			env := map[string]string{}
			for _, kv := range arm.Env() {
				key, value, _ := strings.Cut(kv, "=")
				env[key] = value
			}
			resolved, err := config.Resolve(config.ResolveOptions{Env: env, Overrides: config.Overrides{Providers: providers, ActiveProvider: "chatgpt-61"}})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if resolved.DecisionAdvisor.TrajectoryDefault {
				t.Fatal("the default classifier reached an eval arm")
			}
			got, err := splice.RealizeDecisionAdvisor(resolved.Flags, nullEvaluator{}, resolved.DecisionAdvisor, func(k string) string { return env[k] })
			if err != nil {
				t.Fatalf("realize: %v", err)
			}
			if arm == ArmWarmJev {
				want := splice.RealizedAdvisor{Active: true, Selector: true, Classifier: false, Mode: splice.AdvisorModeActiveExperimental, Model: "typesafe/jev-1.13"}
				if got != want {
					t.Fatalf("warm-jev advisor = %+v, want %+v", got, want)
				}
				return
			}
			if got.Active || got.Selector || got.Classifier {
				t.Fatalf("%s realized an advisor %+v, want none", arm, got)
			}
		})
	}
}

// The control: with the classifier flag on and nothing else set, the same
// profile does get the default classifier. This proves the arm test above
// would catch a leak.
func TestChatGPTProfileGivesDefaultClassifierWhenFlagIsOn(t *testing.T) {
	providers := []config.ProviderProfile{{Name: "chatgpt-61", CatalogID: "chatgpt", Model: "gpt-6.1-sol"}}
	resolved, err := config.Resolve(config.ResolveOptions{Env: map[string]string{}, Overrides: config.Overrides{Providers: providers, ActiveProvider: "chatgpt-61", Flags: map[string]bool{string(flags.TrajectoryClassifierEnabled): true}}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !resolved.DecisionAdvisor.TrajectoryDefault {
		t.Fatal("the default classifier was not built for a ChatGPT profile with the flag on")
	}
}

func TestArmMatrixWarmJevIsWarmPlusSelector(t *testing.T) {
	warm := ArmWarm.Env()
	jev := ArmWarmJev.Env()
	// The warm arm's empty SPLICE_FLAGS is replaced by the Jev value, so the
	// Jev arm has one SPLICE_FLAGS entry, not two. Everything else is added.
	if len(jev) != len(warm)+4 {
		t.Fatalf("jev env = %v (%d entries), want the warm env with SPLICE_FLAGS replaced plus four selector switches", jev, len(jev))
	}
	// Every other warm switch is present and byte-identical in the Jev arm: the
	// cognition treatment does not move.
	for _, w := range warm {
		if strings.HasPrefix(w, "SPLICE_FLAGS=") {
			continue
		}
		found := false
		for _, j := range jev {
			if j == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("the Jev arm dropped the warm switch %q", w)
		}
	}
	// The additions are exactly the selector controls.
	additions := make([]string, 0, 5)
	for _, j := range jev {
		isWarm := false
		for _, w := range warm {
			if j == w {
				isWarm = true
				break
			}
		}
		if !isWarm {
			additions = append(additions, j)
		}
	}
	want := map[string]bool{
		"SPLICE_FLAGS=pipeline.decision_advisor=true,-pipeline.trajectory_classifier": true,
		"SPLICE_DECISION_ADVISOR_MODE=active_experimental":                            true,
		"SPLICE_DECISION_ADVISOR_MODEL=typesafe/jev-1.13":                             true,
		"SPLICE_DECISION_ADVISOR_PUSEFUL=0.9":                                         true,
		"SPLICE_DECISION_ADVISOR_CAP_USD=0.05":                                        true,
	}
	for _, j := range additions {
		if !want[j] {
			t.Fatalf("unexpected Jev addition %q", j)
		}
		delete(want, j)
	}
	for k := range want {
		t.Fatalf("selector switch %q missing from the Jev env", k)
	}
	// The cold and retrieval arms never carry selector switches.
	for _, arm := range []Arm{ArmCold, ArmRetrievalOnly} {
		for _, e := range arm.Env() {
			if strings.Contains(e, "DECISION_ADVISOR") {
				t.Fatalf("%s leaked a selector switch: %q", arm, e)
			}
		}
	}
}

func TestArmWarmJevValidityAndBase(t *testing.T) {
	if !ArmWarmJev.Valid() {
		t.Fatal("warm-jev must be a valid arm")
	}
	if ArmWarmJev.Base() != ArmWarm {
		t.Fatalf("base = %q, want warm", ArmWarmJev.Base())
	}
	if ArmWarmJev.MemoryMode() != "on" {
		t.Fatal("warm-jev must keep the sidecar on")
	}
}

func TestColdWideAndLoopArms(t *testing.T) {
	cold := ArmCold.Env()
	wide := ArmColdWide.Env()
	if !ArmColdWide.Valid() || ArmColdWide.MemoryMode() != "off" || ArmColdWide.Base() != ArmCold {
		t.Fatal("cold-wide must be a valid cold-based arm with memory off")
	}
	for _, entry := range cold {
		found := false
		for _, candidate := range wide {
			if candidate == entry {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("cold-wide dropped cold setting %q", entry)
		}
	}
	if !containsEnv(wide, "SPLICE_EXPANSION_ROUNDS=8") {
		t.Fatalf("cold-wide env = %v, missing expansion override", wide)
	}
	if !ArmLoop.Valid() || ArmLoop.MemoryMode() != "off" || ArmLoop.Base() != ArmCold {
		t.Fatal("loop must be a valid cold-based arm with memory off")
	}
}

func containsEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func TestLoopExecArgsSelectPlainAgentLoop(t *testing.T) {
	args := execArgs(ArmLoop, "model-x", "session-x", "task")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--loop") {
		t.Fatalf("loop args = %v, missing --loop", args)
	}
	if strings.Contains(strings.Join(execArgs(ArmCold, "model-x", "session-x", "task"), " "), "--loop") {
		t.Fatal("cold args unexpectedly selected the plain loop")
	}
}

func TestManifestManifestShape(t *testing.T) {
	// The campaign manifest parses and its invariants hold: arms exact,
	// repeats positive, calibration a probability, cap non-negative, and
	// the budget freeze present.
	manifest, err := LoadJevManifest("manifests/jev-m5.json")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.Arms) != 3 {
		t.Fatalf("arms = %v, want cold, warm, warm-jev", manifest.Arms)
	}
	if manifest.Arms[0] != ArmCold || manifest.Arms[1] != ArmWarm || manifest.Arms[2] != ArmWarmJev {
		t.Fatalf("arm order = %v, want C0, W0, WJ", manifest.Arms)
	}
	if manifest.Repeats != 2 {
		t.Fatalf("repeats = %d, want 2", manifest.Repeats)
	}
	if manifest.CalibrationPUseful < 0 || manifest.CalibrationPUseful > 1 {
		t.Fatalf("p_useful = %v", manifest.CalibrationPUseful)
	}
	if manifest.CapUSD != 0.05 {
		t.Fatalf("cap = %v, want the DS2-pattern 0.05", manifest.CapUSD)
	}
	if !manifest.BudgetFitOff {
		t.Fatal("the budget freeze must be pinned in the manifest")
	}
	if manifest.ModelPin != "typesafe/jev-1.13" {
		t.Fatalf("model pin = %q", manifest.ModelPin)
	}
	// The manifest's env construction agrees with the arm matrix.
	jev := JevEnv("0.9", "0.05")
	joined := strings.Join(jev, " ")
	if !strings.Contains(joined, "pipeline.decision_advisor=true") || !strings.Contains(joined, "active_experimental") {
		t.Fatalf("manifest env construction lost a switch: %s", joined)
	}
}
