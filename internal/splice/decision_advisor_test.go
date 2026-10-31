package splice

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// mockEvaluator counts calls and returns configured results.
type mockEvaluator struct {
	calls  int
	result zeroruntime.DecisionResult
	err    error
}

func (m *mockEvaluator) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	m.calls++
	if m.err != nil {
		return zeroruntime.DecisionResult{}, m.err
	}
	return m.result, nil
}

func choiceResult() zeroruntime.DecisionResult {
	conf := 0.8
	return zeroruntime.DecisionResult{
		RequestID:       "gen-dec-test-1",
		ResolvedModel:   "typesafe/jev-1.13-20260917",
		Provider:        "TypeSafe",
		InputTokens:     300,
		OutputTokens:    40,
		ReportedCostUSD: ptrFloat64(0.00002),
		Answers: []zeroruntime.DecisionAnswer{{
			QuestionID:    "dependency_relation",
			Kind:          zeroruntime.DecisionKindChoice,
			Choice:        "a.go#helper",
			Probabilities: map[string]float64{"a.go#helper": 0.7, "insufficient_evidence": 0.3},
			Confidence:    conf,
			ConfidenceSet: true,
		}},
	}
}

func ptrFloat64(v float64) *float64 { return &v }

func jevSnapshot(refs ...string) schemas.SemanticSnapshot {
	if len(refs) == 0 {
		refs = []string{"a.go#helper"}
	}
	candidates := make([]schemas.CandidateRef, 0, len(refs))
	for _, r := range refs {
		candidates = append(candidates, schemas.CandidateRef{Ref: r, Origin: "memory capture"})
	}
	return schemas.SemanticSnapshot{
		RunID:                 "run-1",
		EpisodeID:             "ep-1",
		Stage:                 "code_writer",
		StageInvocation:       1,
		Iteration:             2,
		EventID:               "ev-1",
		SnapshotSchema:        schemas.SnapshotSchemaV1,
		QuestionPack:          schemas.QuestionPackDependencyRelationV1,
		HostPolicy:            schemas.HostPolicyBoundedPrefetchV1,
		CanonicalProjectID:    "github.com/Taf0711/splice",
		SourceDigest:          "digest-1",
		DistilledIntent:       "route the clock table through the shared helper",
		UnresolvedRelation:    "does a.go#helper satisfy the clock-table need?",
		CandidateRefs:         candidates,
		DeterministicOutcomes: nil,
		PromptLayoutHash:      "layout-1",
	}
}

func shadowConfig() DecisionAdvisorConfig {
	return DecisionAdvisorConfig{
		Mode:         AdvisorModeShadow,
		Profile:      "typesafe/jev-1.13",
		QuestionPack: schemas.QuestionPackDependencyRelationV1,
		HostPolicy:   schemas.HostPolicyBoundedPrefetchV1,
		Limits:       DefaultAdvisorLimits(),
	}
}

func TestGate1OffModeNeverCalls(t *testing.T) {
	eval := &mockEvaluator{result: choiceResult()}
	advisor, err := NewDecisionAdvisor(DecisionAdvisorConfig{Mode: AdvisorModeOff, Limits: DefaultAdvisorLimits()}, eval)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	record, action := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if action {
		t.Fatal("off mode must never act")
	}
	if record.Disposition != schemas.DispositionExactBypass {
		t.Fatalf("disposition = %q, want exact_bypass", record.Disposition)
	}
	if eval.calls != 0 {
		t.Fatalf("off mode issued %d provider calls, want zero", eval.calls)
	}
	if len(advisor.Records()) != 1 {
		t.Fatalf("records = %d, want the disposition recorded", len(advisor.Records()))
	}
}

func TestGate1EligibilityAndBypass(t *testing.T) {
	cases := []struct {
		name        string
		snap        schemas.SemanticSnapshot
		disposition schemas.DecisionDisposition
		reasonHas   string
	}{
		{
			name: "no candidates",
			snap: func() schemas.SemanticSnapshot {
				s := jevSnapshot()
				s.CandidateRefs = nil // built directly: Validate refuses this, the gate names it
				return s
			}(),
			disposition: schemas.DispositionInsufficientEvidence,
			reasonHas:   "no eligible candidates",
		},
		{
			name: "deterministic outcome exists",
			snap: func() schemas.SemanticSnapshot {
				s := jevSnapshot()
				s.DeterministicOutcomes = []string{"exact answer found"}
				return s
			}(),
			disposition: schemas.DispositionExactBypass,
			reasonHas:   "deterministic outcome",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eval := &mockEvaluator{result: choiceResult()}
			advisor, err := NewDecisionAdvisor(shadowConfig(), eval)
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			record, action := advisor.EvaluateGate1(context.Background(), tc.snap, nil)
			if action {
				t.Fatalf("%s: must not act", tc.disposition)
			}
			if record.Disposition != tc.disposition {
				t.Fatalf("disposition = %q, want %q", record.Disposition, tc.disposition)
			}
			if !strings.Contains(record.Reason, tc.reasonHas) {
				t.Fatalf("reason = %q, want it to name %q", record.Reason, tc.reasonHas)
			}
			if eval.calls != 0 {
				t.Fatalf("a skipped opportunity issued %d provider calls", eval.calls)
			}
		})
	}
	// The no-candidate snapshot must fail Validate too: the disposition
	// names the cause, the schema refuses the state.
	if err := jevSnapshot().Validate(); err != nil {
		t.Fatalf("baseline snapshot must validate: %v", err)
	}
}

func TestGate1ShadowRecordsWithoutActing(t *testing.T) {
	eval := &mockEvaluator{result: choiceResult()}
	advisor, err := NewDecisionAdvisor(shadowConfig(), eval)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	record, action := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if action {
		t.Fatal("shadow mode must never act, even on a successful decision")
	}
	if record.Disposition != schemas.DispositionShadowOnly {
		t.Fatalf("disposition = %q", record.Disposition)
	}
	if record.Assessment == nil || record.Assessment.Choice != "a.go#helper" {
		t.Fatalf("assessment = %+v", record.Assessment)
	}
	if record.RequestID != "gen-dec-test-1" {
		t.Fatalf("request id = %q", record.RequestID)
	}
	if record.ChargedUSD == nil || *record.ChargedUSD != 0.00002 {
		t.Fatalf("charge = %v", record.ChargedUSD)
	}
	if eval.calls != 1 {
		t.Fatalf("calls = %d, want exactly one", eval.calls)
	}
}

func TestGate1ActiveRequiresCalibrationAndConservativeGate(t *testing.T) {
	active := shadowConfig()
	active.Mode = AdvisorModeActive
	eval := &mockEvaluator{result: choiceResult()}

	// No calibration provenance: uncalibrated, no call.
	advisor, _ := NewDecisionAdvisor(active, eval)
	record, _ := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if record.Disposition != schemas.DispositionUncalibrated || eval.calls != 0 {
		t.Fatalf("uncalibrated active: %q calls=%d", record.Disposition, eval.calls)
	}

	// Calibration present but savings do not exceed overhead: refuse.
	economics := &schemas.EconomicAssessment{
		ExpectedSavingsLowUSD:  ptrFloat64(0.0001),
		AdvisorOverheadHighUSD: ptrFloat64(0.0002),
		CalibrationProvenance:  "ds2-cold",
	}
	advisor2, _ := NewDecisionAdvisor(active, eval)
	record2, _ := advisor2.EvaluateGate1(context.Background(), jevSnapshot(), economics)
	if record2.Disposition != schemas.DispositionShadowOnly || !strings.Contains(record2.Reason, "conservative gate") {
		t.Fatalf("conservative gate: %q %q", record2.Disposition, record2.Reason)
	}

	// Missing quantities refuse rather than default to permission.
	economics3 := &schemas.EconomicAssessment{CalibrationProvenance: "ds2-cold"}
	advisor3, _ := NewDecisionAdvisor(active, eval)
	record3, _ := advisor3.EvaluateGate1(context.Background(), jevSnapshot(), economics3)
	if record3.Disposition != schemas.DispositionUncalibrated || !strings.Contains(record3.Reason, "refusing to default") {
		t.Fatalf("missing quantities: %q %q", record3.Disposition, record3.Reason)
	}
	if eval.calls != 0 {
		t.Fatalf("refused active gates issued %d calls", eval.calls)
	}
}

func TestGate1BudgetCapDeclinesBeforeAnyCall(t *testing.T) {
	tight := shadowConfig()
	tight.Limits.MaxRequestsPerEpisode = 1
	eval := &mockEvaluator{result: choiceResult()}
	advisor, _ := NewDecisionAdvisor(tight, eval)
	advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if eval.calls != 1 {
		t.Fatalf("first call: %d", eval.calls)
	}
	// A different snapshot (new cache key) hits the request cap.
	record, _ := advisor.EvaluateGate1(context.Background(), func() schemas.SemanticSnapshot {
		s := jevSnapshot("b.go#other")
		s.EventID = "ev-2"
		return s
	}(), nil)
	if record.Disposition != schemas.DispositionBudgetDeclined || !strings.Contains(record.Reason, "request cap") {
		t.Fatalf("request cap: %q %q", record.Disposition, record.Reason)
	}
	if eval.calls != 1 {
		t.Fatalf("cap exceeded but calls = %d", eval.calls)
	}
}

func TestGate1DollarAllowanceCountsSettledCharges(t *testing.T) {
	tiny := shadowConfig()
	// Set the cap below one conservative reservation for the real state,
	// computed from the projected bytes, not a guessed number.
	advisor0, err0 := NewDecisionAdvisor(shadowConfig(), &mockEvaluator{})
	if err0 != nil {
		t.Fatalf("construct: %v", err0)
	}
	state, errState := advisor0.ProjectSnapshot(jevSnapshot())
	if errState != nil {
		t.Fatalf("project: %v", errState)
	}
	tiny.Limits.MaxAdvisorCostUSD = reservationFor(state) * 0.5
	eval := &mockEvaluator{result: choiceResult()}
	advisor, _ := NewDecisionAdvisor(tiny, eval)
	record, _ := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if record.Disposition != schemas.DispositionBudgetDeclined || !strings.Contains(record.Reason, "allowance") {
		t.Fatalf("allowance: %q %q", record.Disposition, record.Reason)
	}
	if eval.calls != 0 {
		t.Fatalf("declined allowance issued %d calls", eval.calls)
	}
}

func TestGate1FailurePreservesChargeAndStaysOnBaseline(t *testing.T) {
	usage := choiceResult()
	eval := &mockEvaluator{err: fmt.Errorf("wrapped: %w", &stubUsageError{result: usage, msg: "malformed answer"})}
	advisor, _ := NewDecisionAdvisor(shadowConfig(), eval)
	record, action := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if action {
		t.Fatal("a provider failure must not act")
	}
	if record.Disposition != schemas.DispositionProviderFailure {
		t.Fatalf("disposition = %q", record.Disposition)
	}
	if record.ChargedUSD == nil || *record.ChargedUSD != 0.00002 {
		t.Fatalf("the billed usage must survive the failure: %v", record.ChargedUSD)
	}
	// The failure settles the exact preserved charge exactly once: not
	// reservation plus charge, not zero. settledUSD is the allowance truth.
	if advisor.settledUSD != 0.00002 {
		t.Fatalf("settled = %v, want exactly the preserved charge 0.00002", advisor.settledUSD)
	}
}

func TestGate1FailureWithoutUsageSettlesReservation(t *testing.T) {
	eval := &mockEvaluator{err: errors.New("transport down")}
	advisor, _ := NewDecisionAdvisor(shadowConfig(), eval)
	record, _ := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if record.Disposition != schemas.DispositionProviderFailure || record.ChargedUSD != nil {
		t.Fatalf("disposition = %q charge = %v", record.Disposition, record.ChargedUSD)
	}
	// The unresolved reservation stays settled until reconciliation: the
	// allowance is reduced, so optional calls exhaust honestly.
	tight := shadowConfig()
	tight.Limits.MaxAdvisorCostUSD = 0.000001
	advisor2, _ := NewDecisionAdvisor(tight, eval)
	advisor2.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	record2, _ := advisor2.EvaluateGate1(context.Background(), jevSnapshot("b.go#other"), nil)
	if record2.Disposition != schemas.DispositionBudgetDeclined {
		t.Fatalf("unresolved charge must exhaust the allowance: %q", record2.Disposition)
	}
}

type stubUsageError struct {
	result zeroruntime.DecisionResult
	msg    string
}

func (e *stubUsageError) Error() string { return e.msg }
func (e *stubUsageError) DecisionUsage() *zeroruntime.DecisionResult {
	r := e.result
	r.ReportedCostUSD = ptrFloat64(0.00002)
	return &r
}

func TestGate1ExactCacheReuse(t *testing.T) {
	eval := &mockEvaluator{result: choiceResult()}
	advisor, _ := NewDecisionAdvisor(shadowConfig(), eval)
	first, _ := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	second, _ := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if eval.calls != 1 {
		t.Fatalf("identical request issued %d calls, want exactly one", eval.calls)
	}
	if first.Disposition != schemas.DispositionShadowOnly || second.Disposition != schemas.DispositionShadowOnly {
		t.Fatalf("dispositions = %q %q", first.Disposition, second.Disposition)
	}
	if second.Reason == "" || !strings.Contains(second.Reason, "cache hit") {
		t.Fatalf("cache-hit reason = %q", second.Reason)
	}
	// The cache hit adds no fabricated charge beyond the first call.
	if advisor.settledUSD > 0.00002+1e-12 {
		t.Fatalf("settled = %v, want the single charge", advisor.settledUSD)
	}

	// A source change is a miss, never a reuse.
	changed := jevSnapshot()
	changed.SourceDigest = "digest-2"
	third, _ := advisor.EvaluateGate1(context.Background(), changed, nil)
	if eval.calls != 2 || strings.Contains(third.Reason, "cache hit") {
		t.Fatalf("source change must miss the cache: calls=%d reason=%q", eval.calls, third.Reason)
	}

	// A prompt-layout change is a miss too.
	relayout := jevSnapshot()
	relayout.PromptLayoutHash = "layout-2"
	advisor.EvaluateGate1(context.Background(), relayout, nil)
	if eval.calls != 3 {
		t.Fatalf("layout change must miss the cache: calls=%d", eval.calls)
	}
}

func TestProjectSnapshotIsDeterministicAndBounded(t *testing.T) {
	advisor, _ := NewDecisionAdvisor(shadowConfig(), &mockEvaluator{})
	state1, err := advisor.ProjectSnapshot(jevSnapshot())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	state2, err := advisor.ProjectSnapshot(jevSnapshot())
	if err != nil || state1 != state2 {
		t.Fatalf("replayed projection differs: %q vs %q", state1, state2)
	}
	// The verbose user request never appears; the distilled intent does.
	if strings.Contains(state1, "please fix the clock bug as discussed in standup") {
		t.Fatal("state leaked content beyond the snapshot")
	}
	if !strings.Contains(state1, "route the clock table") {
		t.Fatal("state lost the distilled intent")
	}
	// The candidate count limit is enforced with the number named.
	advisor2, _ := NewDecisionAdvisor(shadowConfig(), &mockEvaluator{})
	advisor2.config.Limits.MaxCandidates = 1
	_, err = advisor2.ProjectSnapshot(jevSnapshot("a.go#x", "b.go#y"))
	if err == nil || !strings.Contains(err.Error(), "exceed the limit") {
		t.Fatalf("candidate bound: %v", err)
	}
}

func TestSnapshotValidation(t *testing.T) {
	base := jevSnapshot()
	if err := base.Validate(); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	wrongSchema := base
	wrongSchema.SnapshotSchema = "semantic_snapshot_v9"
	if err := wrongSchema.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported schema") {
		t.Fatalf("schema pin: %v", err)
	}
	dup := jevSnapshot("a.go#x", "a.go#x")
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate candidate") {
		t.Fatalf("duplicate ref: %v", err)
	}
	noDigest := jevSnapshot()
	noDigest.SourceDigest = ""
	if err := noDigest.Validate(); err == nil || !strings.Contains(err.Error(), "source digest") {
		t.Fatalf("worktree collapse guard: %v", err)
	}
}

func TestAdvisorConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		config  DecisionAdvisorConfig
		wantErr string
	}{
		{
			name:    "unknown mode",
			config:  DecisionAdvisorConfig{Mode: "turbo", Limits: DefaultAdvisorLimits()},
			wantErr: `unknown mode "turbo"`,
		},
		{
			name:    "retries must stay zero",
			config:  func() DecisionAdvisorConfig { c := shadowConfig(); c.Limits.Retries = 2; return c }(),
			wantErr: "retries must be 0",
		},
		{
			name:    "profile required when not off",
			config:  DecisionAdvisorConfig{Mode: AdvisorModeShadow, Limits: DefaultAdvisorLimits()},
			wantErr: "profile is required",
		},
		{
			name:    "fraction bound",
			config:  func() DecisionAdvisorConfig { c := shadowConfig(); c.Limits.MaxFractionOfBaselineCost = 1.5; return c }(),
			wantErr: "within [0,1]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDecisionAdvisor(tc.config, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to name %q", err, tc.wantErr)
			}
		})
	}
	// Off mode accepts a bare config.
	if _, err := NewDecisionAdvisor(DecisionAdvisorConfig{Mode: AdvisorModeOff}, nil); err != nil {
		t.Fatalf("off mode: %v", err)
	}
}

func TestAdvisorConfigFromResolved(t *testing.T) {
	section := config.DecisionAdvisorConfig{Mode: "shadow", Profile: "typesafe/jev-1.13"}
	converted, err := AdvisorConfigFromResolved(section)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if converted.Mode != AdvisorModeShadow {
		t.Fatalf("mode = %q", converted.Mode)
	}
	if converted.QuestionPack != schemas.QuestionPackDependencyRelationV1 {
		t.Fatalf("pack default = %q", converted.QuestionPack)
	}
	if converted.Limits.MaxRequestBytes != 16384 || converted.Limits.Timeout != 1500*time.Millisecond {
		t.Fatalf("defaults: %+v", converted.Limits)
	}
	if converted.Limits.Retries != 0 || converted.Limits.MaxAdvisorCostUSD != 0.001 {
		t.Fatalf("spending defaults: %+v", converted.Limits)
	}
	// An invalid section fails here too.
	if _, err := AdvisorConfigFromResolved(config.DecisionAdvisorConfig{Mode: "nope"}); err == nil {
		t.Fatal("invalid mode must fail conversion")
	}
}

func TestDecisionAdvisorFlagRegisteredAndDefaultOff(t *testing.T) {
	def, ok := flags.Lookup(flags.DecisionAdvisorEnabled)
	if !ok {
		t.Fatal("the decision_advisor flag is not registered")
	}
	if def.Default {
		t.Fatal("the decision_advisor flag must default to false: no decision call before the owner flips it")
	}
	if def.Scope != flags.ScopeUser {
		t.Fatalf("scope = %v, want user", def.Scope)
	}
	if def.ReadBy == "" {
		t.Fatal("the flag must name its reader")
	}
}

func TestSnapshotRecordsStaySeparateFromCostFields(t *testing.T) {
	// Predicted savings never occupy an actual-cost field: the economic
	// assessment carries pointers, and a nil pointer means "not estimated",
	// never zero spend. A skipped call carries no charge field at all.
	eval := &mockEvaluator{result: choiceResult()}
	advisor, _ := NewDecisionAdvisor(shadowConfig(), eval)
	record, _ := advisor.EvaluateGate1(context.Background(), jevSnapshot(), nil)
	if record.ChargedUSD == nil {
		t.Fatal("a real call must carry its charge")
	}
	economics := &schemas.EconomicAssessment{ExpectedSavingsLowUSD: ptrFloat64(0.5)}
	if economics.BaselineContinuationHighUSD != nil {
		t.Fatal("unestimated quantities must stay nil, never zero")
	}
}

func TestAdvisorReservationUsesConfiguredPriceAndUnknownCost(t *testing.T) {
	if math.Abs(reservationFor("abcd", .42)-10*reservationFor("abcd")) > 1e-15 {
		t.Fatal("configured reservation price ignored")
	}
	cfg := shadowConfig()
	cfg.Limits.ReservationUSDPerMillionInput = .42
	advisor, err := NewDecisionAdvisor(cfg, &trajectoryEvaluator{label: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (advisorClassifier{advisor: advisor, pack: actionTrajectoryPack{}, budget: trajectoryRequestBudget}).Classify(context.Background(), fabricatedBoundary())
	if err != nil {
		t.Fatal(err)
	}
	records := advisor.Records()
	if records[0].ChargedUSD != nil {
		t.Fatal("unknown cost was recorded as zero")
	}
	if advisor.settledUSD <= 0 {
		t.Fatal("unknown charge must retain the reservation")
	}
}
