package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestMergeStageUsageSumsWebSearchRequestsAndKeepsMatchingEngine(t *testing.T) {
	got := mergeStageUsage(
		&schemas.StageUsage{WebSearchRequests: 2, WebSearchEngine: "parallel"},
		&schemas.StageUsage{WebSearchRequests: 3, WebSearchEngine: "parallel"},
	)
	if got.WebSearchRequests != 5 {
		t.Fatalf("web search requests = %d, want 5", got.WebSearchRequests)
	}
	if got.WebSearchEngine != "parallel" {
		t.Fatalf("web search engine = %q, want %q", got.WebSearchEngine, "parallel")
	}
}

func TestMergeStageUsageMarksMismatchedWebSearchEngines(t *testing.T) {
	got := mergeStageUsage(
		&schemas.StageUsage{WebSearchRequests: 1, WebSearchEngine: "parallel"},
		&schemas.StageUsage{WebSearchRequests: 2, WebSearchEngine: "exa"},
	)
	if got.WebSearchRequests != 3 {
		t.Fatalf("web search requests = %d, want 3", got.WebSearchRequests)
	}
	if got.WebSearchEngine != "mixed" {
		t.Fatalf("web search engine = %q, want mismatch marker %q", got.WebSearchEngine, "mixed")
	}
}

func TestMergeStageUsageNilSideCarriesWebSearchFields(t *testing.T) {
	for name, tc := range map[string]struct {
		a, b *schemas.StageUsage
	}{
		"nil first":  {a: nil, b: &schemas.StageUsage{WebSearchRequests: 2, WebSearchEngine: "exa"}},
		"nil second": {a: &schemas.StageUsage{WebSearchRequests: 3, WebSearchEngine: "parallel"}, b: nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := mergeStageUsage(tc.a, tc.b)
			want := tc.a
			if want == nil {
				want = tc.b
			}
			if got.WebSearchRequests != want.WebSearchRequests || got.WebSearchEngine != want.WebSearchEngine {
				t.Fatalf("web search usage = (%d, %q), want (%d, %q)", got.WebSearchRequests, got.WebSearchEngine, want.WebSearchRequests, want.WebSearchEngine)
			}
		})
	}
}

func TestRequestLedgerRecordCarriesWebSearchFields(t *testing.T) {
	ledger := newRequestLedger()
	options := ledger.recordingOptions(agent.Options{
		EstimateUsageCost: func(string, agent.Usage, bool) agent.UsageCostEstimate {
			return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "test"}
		},
	})
	options.OnAttributedUsage(agent.AttributedUsage{
		Usage: zeroruntime.Usage{
			InputTokens:       10,
			OutputTokens:      5,
			WebSearchRequests: 2,
			WebSearchEngine:   "parallel",
		},
		UsageReported: true,
		Stage:         "code_writer",
		Iteration:     1,
	})
	if len(ledger.records) != 1 {
		t.Fatalf("records = %d, want 1", len(ledger.records))
	}
	record := ledger.records[0]
	if record.WebSearchRequests != 2 || record.WebSearchEngine != "parallel" {
		t.Fatalf("record web search usage = (%d, %q), want (2, %q)", record.WebSearchRequests, record.WebSearchEngine, "parallel")
	}
}

func TestRequestLedgerRecordWithoutWebSearchIsUnchanged(t *testing.T) {
	ledger := newRequestLedger()
	options := ledger.recordingOptions(agent.Options{
		EstimateUsageCost: func(string, agent.Usage, bool) agent.UsageCostEstimate {
			return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "test"}
		},
	})
	options.OnAttributedUsage(agent.AttributedUsage{
		Usage:         zeroruntime.Usage{InputTokens: 10, OutputTokens: 5},
		UsageReported: true,
		Stage:         "code_writer",
		Iteration:     1,
	})
	record := ledger.records[0]
	if record.WebSearchRequests != 0 || record.WebSearchEngine != "" {
		t.Fatalf("record web search usage = (%d, %q), want (0, %q)", record.WebSearchRequests, record.WebSearchEngine, "")
	}
}
