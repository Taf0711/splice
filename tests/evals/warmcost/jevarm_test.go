package warmcost

// M5 offline proofs (SPEC_DECISION_SELECTOR_M5_DIAGNOSTIC_2026-09-22
// Section 5). These run provider-free and fast; the paid campaign is a
// release-cadence tool, never a CI test. Proof 1 pins the arm matrix: WJ is
// W0 plus exactly the selector switches, so the paired comparison isolates
// the selector and no ambient value can leak into a measurement.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
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
	args := execArgs(ArmLoop, "model-x", "session-x", "task", false)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--loop") {
		t.Fatalf("loop args = %v, missing --loop", args)
	}
	if strings.Contains(strings.Join(execArgs(ArmCold, "model-x", "session-x", "task", false), " "), "--loop") {
		t.Fatal("cold args unexpectedly selected the plain loop")
	}
}

func TestExecArgsTrustProjectSelectsTrustFlag(t *testing.T) {
	// Default: the exact invocation every existing campaign uses.
	wantUntrusted := []string{
		"--no-trust", "exec",
		"--output-format", "stream-json",
		"--memory", "off",
		"--init-session-id", "session-x",
		"--model", "model-x",
		"task",
	}
	if got := execArgs(ArmCold, "model-x", "session-x", "task", false); !reflect.DeepEqual(got, wantUntrusted) {
		t.Fatalf("untrusted args = %v, want %v", got, wantUntrusted)
	}

	// Trusted through the environment: no root trust flag at all. The
	// attempt sets SPLICE_TRUST_WORKSPACE=1, which trusts without writing the
	// trust store, so passing --trust would write it.
	wantTrusted := wantUntrusted[1:]
	if got := execArgs(ArmCold, "model-x", "session-x", "task", true); !reflect.DeepEqual(got, wantTrusted) {
		t.Fatalf("trusted args = %v, want %v", got, wantTrusted)
	}
}

func TestAttemptEnvTrustProjectSetsTrustEnv(t *testing.T) {
	// An operator value must never reach the child, in either mode.
	base := []string{"PATH=/bin", "SPLICE_TRUST_WORKSPACE=0"}
	count := func(env []string, prefix string) (n int, last string) {
		for _, kv := range env {
			if strings.HasPrefix(kv, prefix) {
				n++
				last = kv
			}
		}
		return n, last
	}

	off := attemptEnv(base, false)
	if n, _ := count(off, "SPLICE_TRUST_WORKSPACE="); n != 0 {
		t.Fatalf("untrusted env kept inherited SPLICE_TRUST_WORKSPACE: %v", off)
	}
	if !containsEnv(off, "PATH=/bin") {
		t.Fatalf("untrusted env dropped PATH: %v", off)
	}

	on := attemptEnv(base, true)
	if n, last := count(on, "SPLICE_TRUST_WORKSPACE="); n != 1 || last != "SPLICE_TRUST_WORKSPACE=1" {
		t.Fatalf("trusted env = %v, want exactly SPLICE_TRUST_WORKSPACE=1", on)
	}
}

func TestTrustEnvBeatsNeverSetting(t *testing.T) {
	// attemptEnv relies on this order: env 1 trusts before the user setting
	// is read, and it does not persist. If this fails, --trust-project no
	// longer trusts a workspace whose user config says defaultProjectTrust
	// "never".
	decision, persist := config.ResolveTrust("/work/ws", nil, "never", false, false, "1")
	if decision != config.TrustTrusted || persist {
		t.Fatalf("env 1 with setting never = (%v, persist %v), want (trusted, no persist)", decision, persist)
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

func TestDesignArmIsColdWithMemoryOff(t *testing.T) {
	if !ArmDesign.Valid() || ArmDesign.MemoryMode() != "off" || ArmDesign.Base() != ArmCold {
		t.Fatal("design must be a valid cold-based arm with memory off")
	}
	if strings.Join(ArmDesign.Env(), " ") != strings.Join(ArmCold.Env(), " ") {
		t.Fatalf("design env = %v, want the cold env %v", ArmDesign.Env(), ArmCold.Env())
	}
}

func TestDesignExecArgsPassTheRequestAsTheDesignValue(t *testing.T) {
	args := execArgs(ArmDesign, "model-x", "session-x", "task", false)
	if len(args) < 2 || args[len(args)-2] != "--design" || args[len(args)-1] != "task" {
		t.Fatalf("design args = %v, want the request as the value of --design", args)
	}
	if strings.Contains(strings.Join(args, " "), "--loop") {
		t.Fatalf("design args = %v, must not select the plain loop", args)
	}
}

// TestDesignLedgerComesFromStreamUsage pins the design attempt parse. The
// final event is a design plan result with no usage records, so the ledger
// comes from the stream usage events, with their stage names.
func TestDesignLedgerComesFromStreamUsage(t *testing.T) {
	stream := strings.Join([]string{
		`{"schemaVersion":2,"type":"usage","runId":"r","promptTokens":100,"completionTokens":20,"costUsd":0.5,"costStatus":"priced","usageReported":true,"stage":"design_crystallize","iteration":1}`,
		`{"schemaVersion":2,"type":"usage","runId":"r","promptTokens":40,"completionTokens":10,"costUsd":0.25,"costStatus":"priced","usageReported":true,"stage":"code_writer","iteration":1}`,
		`{"schemaVersion":2,"type":"final","runId":"r","text":"{\"plan_id\":\"plan-x\",\"status\":\"completed\"}"}`,
	}, "\n")
	var attempt Attempt
	if err := applyDesignLedger(&attempt, []byte(stream)); err != nil {
		t.Fatalf("applyDesignLedger: %v", err)
	}
	if attempt.RunID != "plan-x" || attempt.RunStatus != "completed" {
		t.Fatalf("run id/status = %q/%q, want plan-x/completed", attempt.RunID, attempt.RunStatus)
	}
	if len(attempt.Requests) != 2 || attempt.Requests[0].Stage != "design_crystallize" || attempt.Requests[1].Stage != "code_writer" {
		t.Fatalf("requests = %+v, want design_crystallize then code_writer", attempt.Requests)
	}
	if attempt.Totals.Requests != 2 || attempt.Totals.InputTokens != 140 || attempt.Totals.OutputTokens != 30 {
		t.Fatalf("totals = %+v, want 2 requests, 140 input and 30 output tokens", attempt.Totals)
	}
	if attempt.Totals.BilledUSD != 0.75 || attempt.Totals.PricedRecords != 2 {
		t.Fatalf("totals = %+v, want 0.75 USD over 2 priced records", attempt.Totals)
	}
}

func TestDesignLedgerRejectsStreamWithoutFinal(t *testing.T) {
	var attempt Attempt
	if err := applyDesignLedger(&attempt, []byte(`{"type":"usage","stage":"plan_critic"}`)); err == nil {
		t.Fatal("a stream without a final design result must fail, not read as zero spend")
	}
}

// TestDesignLedgerKeepsUsageWithoutFinal pins the failed-run parse. A revision
// error ends the run with an error event and no final event. The usage that
// the attempt already spent must still count, and the attempt names the
// missing result as its ledger error.
func TestDesignLedgerKeepsUsageWithoutFinal(t *testing.T) {
	stream := strings.Join([]string{
		`{"schemaVersion":2,"type":"usage","runId":"r","promptTokens":100,"completionTokens":20,"costUsd":0.5,"costStatus":"priced","usageReported":true,"stage":"design_crystallize","iteration":1}`,
		`{"schemaVersion":2,"type":"usage","runId":"r","promptTokens":30,"completionTokens":5,"costUsd":0.25,"costStatus":"priced","usageReported":true,"stage":"plan_critic","iteration":1}`,
		`{"schemaVersion":2,"type":"error","runId":"r","code":"provider_error","message":"design revision: stream failed"}`,
		`{"schemaVersion":2,"type":"run_end","runId":"r","status":"error","exitCode":3}`,
	}, "\n")
	var attempt Attempt
	err := applyDesignLedger(&attempt, []byte(stream))
	if err == nil {
		t.Fatal("a run without a final result must name a ledger error")
	}
	if attempt.Totals.Requests != 2 || attempt.Totals.BilledUSD != 0.75 || attempt.Totals.PricedRecords != 2 {
		t.Fatalf("totals = %+v, want the spent usage of both requests kept", attempt.Totals)
	}
	if len(attempt.Requests) != 2 || attempt.Requests[1].Stage != "plan_critic" {
		t.Fatalf("requests = %+v, want both design requests kept", attempt.Requests)
	}
}

// TestDesignLedgerReportsTheCritiqueState pins the critique flag of a design
// attempt. The design_plan event says whether the critique was resolved.
func TestDesignLedgerReportsTheCritiqueState(t *testing.T) {
	for _, tc := range []struct {
		resolved bool
		want     bool
	}{
		{resolved: false, want: true},
		{resolved: true, want: false},
	} {
		stream := strings.Join([]string{
			fmt.Sprintf(`{"schemaVersion":2,"type":"design_plan","runId":"r","planId":"plan-x","tasks":["A"],"revised":true,"critiqueResolved":%t,"critiqueIssues":1}`, tc.resolved),
			`{"schemaVersion":2,"type":"final","runId":"r","text":"{\"plan_id\":\"plan-x\",\"status\":\"completed\"}"}`,
		}, "\n")
		var attempt Attempt
		if err := applyDesignLedger(&attempt, []byte(stream)); err != nil {
			t.Fatalf("applyDesignLedger: %v", err)
		}
		if attempt.CritiqueUnresolved != tc.want {
			t.Fatalf("resolved=%t: CritiqueUnresolved = %t, want %t", tc.resolved, attempt.CritiqueUnresolved, tc.want)
		}
	}
}

// TestAttemptLedgerDispatchesByArm pins the run-task branch. A design arm reads
// its design result, and a cold arm reads a pipeline result. Each arm reads only
// the result that its run produces.
func TestAttemptLedgerDispatchesByArm(t *testing.T) {
	design := strings.Join([]string{
		`{"schemaVersion":2,"type":"usage","runId":"r","promptTokens":10,"completionTokens":1,"costUsd":0.1,"costStatus":"priced","usageReported":true,"stage":"design_crystallize","iteration":1}`,
		`{"schemaVersion":2,"type":"final","runId":"r","text":"{\"plan_id\":\"plan-x\",\"status\":\"completed\"}"}`,
	}, "\n")
	pipelinePayload, _ := json.Marshal(map[string]any{
		"run_id": "run-cold", "status": "completed", "tier": "light",
		"stages": []any{}, "cost_coverage": "not_applicable",
		"total_cost_usd": 0, "priced_request_count": 0,
	})
	pipeline := `{"schemaVersion":2,"type":"final","runId":"r","text":` + strconvQuote(string(pipelinePayload)) + `}`

	var designAttempt Attempt
	if err := applyAttemptLedger(&designAttempt, ArmDesign, []byte(design)); err != nil {
		t.Fatalf("design arm: %v", err)
	}
	if designAttempt.RunID != "plan-x" || len(designAttempt.Requests) != 1 {
		t.Fatalf("design arm = run %q with %d request(s), want plan-x with 1", designAttempt.RunID, len(designAttempt.Requests))
	}

	var coldAttempt Attempt
	if err := applyAttemptLedger(&coldAttempt, ArmCold, []byte(pipeline)); err != nil {
		t.Fatalf("cold arm: %v", err)
	}
	if coldAttempt.RunID != "run-cold" {
		t.Fatalf("cold arm run id = %q, want run-cold", coldAttempt.RunID)
	}

	var wrong Attempt
	if err := applyAttemptLedger(&wrong, ArmCold, []byte(design)); err == nil {
		t.Fatal("the cold arm must not accept a design result")
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// TestDesignLedgerCostCoverage pins the coverage of a design attempt. A run
// without a valid final result is partial even when every record is priced,
// because the run did not finish. A complete run with every record priced is
// complete.
func TestDesignLedgerCostCoverage(t *testing.T) {
	priced := `{"schemaVersion":2,"type":"usage","runId":"r","promptTokens":100,"completionTokens":20,"costUsd":0.5,"costStatus":"priced","usageReported":true,"stage":"design_crystallize","iteration":1}`
	final := `{"schemaVersion":2,"type":"final","runId":"r","text":"{\"plan_id\":\"plan-x\",\"status\":\"completed\"}"}`
	revisionError := strings.Join([]string{
		priced,
		`{"schemaVersion":2,"type":"error","runId":"r","code":"provider_error","message":"design revision: stream failed"}`,
		`{"schemaVersion":2,"type":"run_end","runId":"r","status":"error","exitCode":3}`,
	}, "\n")
	complete := priced + "\n" + final

	var failed Attempt
	if err := applyDesignLedger(&failed, []byte(revisionError)); err == nil {
		t.Fatal("a run without a final result must name a ledger error")
	}
	if failed.CostCoverage != schemas.CostCoveragePartial {
		t.Fatalf("revision-error coverage = %q, want %q", failed.CostCoverage, schemas.CostCoveragePartial)
	}

	var done Attempt
	if err := applyDesignLedger(&done, []byte(complete)); err != nil {
		t.Fatalf("applyDesignLedger: %v", err)
	}
	if done.CostCoverage != schemas.CostCoverageComplete {
		t.Fatalf("complete run coverage = %q, want %q", done.CostCoverage, schemas.CostCoverageComplete)
	}
}
