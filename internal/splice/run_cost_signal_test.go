package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestTrajectoryCostRuleFiresWhenRoundsFallButCostRises pins the W3 contract:
// the rule reads billed cost, not the round count. Two iterations with
// identical correctness mean no correctness gain; the billed cost grew, so the
// rule fires even though the round count is low.
func TestTrajectoryCostRuleFiresWhenRoundsFallButCostRises(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withTests(3, 1, 0), withHash("a")),
		iterState(withTests(3, 1, 0), withHash("b")),
	}
	cost := &RunCostSignal{BilledCostDeltaUSD: 0.42, ProviderRequestDelta: 1}
	decision, err := EvaluateTrajectoryWithCost(history, 5, nil, cost)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if decision.Action != schemas.ActionStepBack {
		t.Fatalf("action = %q, want step_back (%+v)", decision.Action, decision)
	}
}

// TestTrajectoryCostRuleDoesNotFireOnCorrectnessGain pins the other half: more
// spend with a correctness gain is legitimate progress, so the rule stays
// silent even though more rounds ran.
func TestTrajectoryCostRuleDoesNotFireOnCorrectnessGain(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withTests(2, 1, 0), withHash("a")),
		iterState(withTests(4, 0, 0), withHash("b")),
	}
	cost := &RunCostSignal{BilledCostDeltaUSD: 1.5, ProviderRequestDelta: 2}
	decision, err := EvaluateTrajectoryWithCost(history, 5, nil, cost)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if decision.Action == schemas.ActionStepBack {
		t.Fatalf("must not fire on a correctness gain: %+v", decision)
	}
}

// TestTrajectoryCostRuleInactiveWithoutSignal pins that the rule is not
// round-only: with no cost signal the same flat-correctness history continues.
func TestTrajectoryCostRuleInactiveWithoutSignal(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withTests(3, 1, 0), withHash("a")),
		iterState(withTests(3, 1, 0), withHash("b")),
	}
	decision := EvaluateTrajectory(history, 5, nil)
	if decision.Action != schemas.ActionContinue {
		t.Fatalf("action = %q, want continue", decision.Action)
	}
}

// TestEvaluateTrajectoryWithCostRejectsMalformedSignal pins the fail-loud
// contract: a negative delta is a caller error, never a silent default.
func TestEvaluateTrajectoryWithCostRejectsMalformedSignal(t *testing.T) {
	if _, err := EvaluateTrajectoryWithCost(nil, 5, nil, &RunCostSignal{BilledCostDeltaUSD: -1}); err == nil {
		t.Fatal("negative billed cost delta must fail loud")
	}
	if _, err := EvaluateTrajectoryWithCost(nil, 5, nil, &RunCostSignal{ProviderRequestDelta: -1}); err == nil {
		t.Fatal("negative provider request delta must fail loud")
	}
}

// TestRunCostSignalFromReportReadsBilledCostAndRequests pins that the signal
// comes from the F2 report's billed USD total and request count, and that
// missing pricing fails loud instead of counting as zero cost.
func TestRunCostSignalFromReportReadsBilledCostAndRequests(t *testing.T) {
	cost := 1.25
	report := &WorkflowCostReport{
		CostCoverage: schemas.CostCoverageComplete,
		Totals:       WorkflowSourceSpend{Requests: 3, CostUSD: &cost},
	}
	sig, err := RunCostSignalFromReport(report, 0.25, 1)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if sig.BilledCostDeltaUSD != 1.0 {
		t.Fatalf("billed delta = %v, want 1.0", sig.BilledCostDeltaUSD)
	}
	if sig.ProviderRequestDelta != 2 {
		t.Fatalf("request delta = %d, want 2", sig.ProviderRequestDelta)
	}
}

func TestRunCostSignalFromReportFailsOnIncompleteCoverage(t *testing.T) {
	if _, err := RunCostSignalFromReport(&WorkflowCostReport{CostCoverage: schemas.CostCoveragePartial}, 0, 0); err == nil {
		t.Fatal("incomplete cost coverage must fail loud")
	}
	if _, err := RunCostSignalFromReport(nil, 0, 0); err == nil {
		t.Fatal("nil report must fail loud")
	}
}

// TestSpendViewForRecordMapsCostStatus pins the ledger-to-report bridge the
// live cost rule reads. An unknown or empty status is treated as unpriced, so
// it can never be mistaken for a priced zero.
func TestSpendViewForRecordMapsCostStatus(t *testing.T) {
	cost := 0.25
	cases := []struct {
		status string
		want   string
	}{
		{schemas.CostStatusPriced, costStatusPriced},
		{schemas.CostStatusUnpriced, costStatusUnpriced},
		{schemas.CostStatusError, costStatusError},
		{"", costStatusUnpriced},
	}
	for _, tc := range cases {
		rec := schemas.PipelineUsageRecord{
			InputTokens: 10, OutputTokens: 2, CachedTokens: 1,
			CacheWrite: 3, Reasoning: 4, CostUSD: &cost, CostStatus: tc.status,
		}
		v := spendViewForRecord(rec)
		if v.input != 10 || v.output != 2 || v.cached != 1 || v.cacheWrite != 3 || v.reasoning != 4 {
			t.Fatalf("%s: view = %+v", tc.status, v)
		}
		if v.costStatus != tc.want {
			t.Fatalf("%s: status = %q, want %q", tc.status, v.costStatus, tc.want)
		}
		if v.costUSD == nil || *v.costUSD != cost {
			t.Fatalf("%s: cost = %v", tc.status, v.costUSD)
		}
	}
}

// TestLedgerCostSnapshotIncompleteCoverageKeepsRuleInactive pins the live W3
// contract: partial pricing is not a zero-cost window. The snapshot reports the
// incomplete state with a reason, and the pairing keeps the cost rule inactive.
func TestLedgerCostSnapshotIncompleteCoverageKeepsRuleInactive(t *testing.T) {
	ledger := newRequestLedger()
	cost := 1.5
	ledger.records = append(ledger.records,
		schemas.PipelineUsageRecord{CostUSD: &cost, CostStatus: schemas.CostStatusPriced},
		schemas.PipelineUsageRecord{CostStatus: schemas.CostStatusUnpriced},
	)
	snap, err := ledgerCostSnapshot(ledger)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Complete {
		t.Fatal("partial coverage must not be complete")
	}
	if snap.Note == "" {
		t.Fatal("incomplete coverage must carry a reason")
	}
	if snap.BilledUSD != cost || snap.Requests != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
	prev := billedCostSnapshot{Complete: true}
	sig, err := iterationCostSignal(prev, snap)
	if err != nil {
		t.Fatalf("signal: %v", err)
	}
	if sig != nil {
		t.Fatalf("incomplete coverage must keep the rule inactive, got %+v", sig)
	}
}

// TestLedgerCostSnapshotCompleteYieldsDelta pins the positive path: complete
// pricing yields the billed USD and request deltas the rule consumes.
func TestLedgerCostSnapshotCompleteYieldsDelta(t *testing.T) {
	ledger := newRequestLedger()
	a, b := 1.0, 2.5
	ledger.records = append(ledger.records,
		schemas.PipelineUsageRecord{CostUSD: &a, CostStatus: schemas.CostStatusPriced},
	)
	first, err := ledgerCostSnapshot(ledger)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if !first.Complete || first.BilledUSD != a || first.Requests != 1 {
		t.Fatalf("first = %+v", first)
	}
	ledger.records = append(ledger.records,
		schemas.PipelineUsageRecord{CostUSD: &b, CostStatus: schemas.CostStatusPriced},
	)
	second, err := ledgerCostSnapshot(ledger)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	sig, err := iterationCostSignal(first, second)
	if err != nil {
		t.Fatalf("signal: %v", err)
	}
	if sig == nil || sig.BilledCostDeltaUSD != b || sig.ProviderRequestDelta != 1 {
		t.Fatalf("signal = %+v", sig)
	}
}

// TestLedgerCostSnapshotNilAndEmptyAreInactive pins that a missing or empty
// ledger never fabricates a measured zero.
func TestLedgerCostSnapshotNilAndEmptyAreInactive(t *testing.T) {
	nilSnap, err := ledgerCostSnapshot(nil)
	if err != nil {
		t.Fatalf("nil snapshot: %v", err)
	}
	if nilSnap.Complete || nilSnap.Note == "" {
		t.Fatalf("nil snapshot = %+v", nilSnap)
	}
	emptySnap, err := ledgerCostSnapshot(newRequestLedger())
	if err != nil {
		t.Fatalf("empty snapshot: %v", err)
	}
	if emptySnap.Complete || emptySnap.Note == "" {
		t.Fatalf("empty snapshot = %+v", emptySnap)
	}
}
