package schemas

import "testing"

func TestPipelineResultTotalsRemainTokenAndCostTotals(t *testing.T) {
	// Web-search count and engine stay on StageRecord. PipelineResult has no
	// aggregate because the existing invariant sums token and cost usage records,
	// while one result cannot represent multiple stage search engines.
	result := PipelineResult{
		RunID:  "run-1",
		Status: "completed",
		Tier:   TierLight,
		Stages: []StageRecord{{
			Name:              "code_writer",
			Status:            StageCompleted,
			Iteration:         1,
			WebSearchRequests: 2,
			WebSearchEngine:   "parallel",
		}},
		CostCoverage: CostCoverageNotApplicable,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("PipelineResult.Validate() = %v, want nil", err)
	}
}
