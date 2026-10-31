package cli

import (
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// The loop ledger prices a request with the pipeline's rule
// (splicerun.ResolveRequestCost). The same request must get the same cost on
// both paths. If the loop ledger priced from the estimate alone, the estimate
// below (99 USD) would replace the reported charge and this test would fail.
func TestLoopLedgerPricesLikePipelineLedger(t *testing.T) {
	zero, charge := 0.0, 0.0013749
	wrongEstimate := func(string, string, agent.Usage, bool) agent.UsageCostEstimate {
		cost := 99.0
		return agent.UsageCostEstimate{CostUSD: &cost, Status: agent.CostStatusPriced, Provenance: agent.CostProvenanceRuntimeEstimate}
	}
	cases := []struct {
		name           string
		model          string
		reported       *float64
		wantCost       float64
		wantProvenance string
	}{
		{name: "reported charge wins", model: "deepseek/deepseek-v4.1-flash", reported: &charge, wantCost: charge, wantProvenance: agent.CostProvenanceReported},
		{name: "free model id keeps its zero", model: "google/gemma-4-31b-it:free", reported: &zero, wantCost: 0, wantProvenance: agent.CostProvenanceReported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attributed := agent.AttributedUsage{
				Usage:           zeroruntime.Usage{InputTokens: 900, OutputTokens: 40},
				UsageReported:   true,
				Model:           tc.model,
				ReportedCostUSD: tc.reported,
			}
			ledger := newLoopUsageLedger(wrongEstimate)
			ledger.append(attributed)
			if len(ledger.records) != 1 {
				t.Fatalf("records = %d, want 1", len(ledger.records))
			}
			rec := ledger.records[0]

			pipeline := splicerun.ResolveRequestCostFor(nil, wrongEstimate, attributed)
			if rec.CostUSD == nil || pipeline.CostUSD == nil || *rec.CostUSD != *pipeline.CostUSD {
				t.Fatalf("loop cost %v, pipeline cost %v: must match", rec.CostUSD, pipeline.CostUSD)
			}
			if rec.CostUSD == nil || *rec.CostUSD != tc.wantCost || rec.CostProvenance != tc.wantProvenance {
				t.Fatalf("loop record = cost %v provenance %q, want %v %q", rec.CostUSD, rec.CostProvenance, tc.wantCost, tc.wantProvenance)
			}
		})
	}
}
