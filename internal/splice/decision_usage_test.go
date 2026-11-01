package splice

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestDecisionRequestsJoinTheLedger: every paid decision request reaches
// the run's request ledger with spend source "decision" and its pack, the
// run totals count its tokens and charge, and a cache hit adds nothing.
// (Before this, Jev's own tokens were outside every campaign total.)
func TestDecisionRequestsJoinTheLedger(t *testing.T) {
	charge := 0.0001
	eval := &recordingEvaluator{p: map[string]float64{"same_cause": 0.2, "new_information": 0.9}}
	advisor, err := NewDecisionAdvisor(shadowConfig(), &usageEvaluator{inner: eval, in: 900, out: 12, usd: &charge})
	if err != nil {
		t.Fatal(err)
	}
	ledger := newRequestLedger()
	advisor.usageSink = func(pack string, iteration int, res zeroruntime.DecisionResult) {
		ledger.append(decisionUsageRecord(pack, iteration, res))
	}
	t.Setenv(TrajectoryPackEnvVar, QuestionPackTrajV1)
	prev := TrajectoryStep{Change: "a", Failure: "x failed"}
	latest := TrajectoryStep{Change: "b", Failure: "y failed"}
	boundary := fabricatedBoundary()
	boundary.Repair = 1
	boundary.Previous = &prev
	boundary.Latest = &latest
	classifier := advisorClassifier{advisor: advisor, pack: noulTrajectoryPack{}, budget: trajectoryRequestBudget}
	for i := 0; i < 2; i++ { // The second request uses the cache and adds no charge.
		if _, err := classifier.Classify(context.Background(), boundary); err != nil {
			t.Fatal(err)
		}
	}
	if len(ledger.records) != 1 {
		t.Fatalf("ledger records = %d, want 1 (a cache hit adds no charge)", len(ledger.records))
	}
	rec := ledger.records[0]
	if err := rec.Validate(); err != nil {
		t.Fatalf("decision record invalid: %v", err)
	}
	if rec.SpendSource != schemas.SpendSourceDecision || rec.DecisionPack != QuestionPackTrajV1 || rec.InputTokens != 900 || rec.CostUSD == nil || *rec.CostUSD != charge {
		t.Fatalf("record = %+v", rec)
	}

	result := schemas.PipelineResult{}
	if err := applyRequestLedger(&result, ledger); err != nil {
		t.Fatalf("a decision record must not break the stage reconciliation: %v", err)
	}
	if result.TotalTokensInput != 900 || result.TotalCostUSD != charge {
		t.Fatalf("run totals = %d tokens, $%v; want the decision request counted", result.TotalTokensInput, result.TotalCostUSD)
	}
}

// usageEvaluator adds reported usage to another evaluator's answers.
type usageEvaluator struct {
	inner   zeroruntime.DecisionEvaluator
	in, out int
	usd     *float64
}

func (u *usageEvaluator) Evaluate(ctx context.Context, req zeroruntime.DecisionRequest) (zeroruntime.DecisionResult, error) {
	res, err := u.inner.Evaluate(ctx, req)
	res.InputTokens, res.OutputTokens, res.ReportedCostUSD, res.Provider = u.in, u.out, u.usd, "TypeSafe"
	return res, err
}
