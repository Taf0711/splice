package usage

import (
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestTrackerNormalizesUsageAndComputesModelCost(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{Now: fixedUsageClock("2026-06-04T13:00:00Z"), Registry: mustTestPricedRegistry(t)})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}

	record, err := tracker.Record(RecordInput{
		ModelID: "gpt-4.1",
		Source:  "exec",
		Usage: zeroruntime.Usage{
			PromptTokens:      1_000,
			CompletionTokens:  250,
			CachedInputTokens: 200,
		},
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if record.Sequence != 1 || record.ID != "zero_usage_1" || record.CreatedAt != "2026-06-04T13:00:00Z" {
		t.Fatalf("unexpected record identity: %#v", record)
	}
	if record.Usage.InputTokens != 1_000 || record.Usage.OutputTokens != 250 || record.Usage.TotalTokens != 1_250 {
		t.Fatalf("usage not normalized: %#v", record.Usage)
	}
	if record.Cost.TotalCost <= 0 || record.Cost.ModelID != "gpt-4.1" {
		t.Fatalf("cost not computed: %#v", record.Cost)
	}
	if record.Cost.CostStatus != CostStatusPriced || record.Cost.CostProvenance != CostProvenanceReconstructedEstimate {
		t.Fatalf("cost metadata = %#v, want reconstructed priced", record.Cost)
	}

	summary := tracker.Summary()
	if summary.RecordCount != 1 || summary.TotalTokens != 1_250 || summary.ByModel[0].ModelID != "gpt-4.1" {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if FormatSummary(summary) != "1 request, 1,250 tokens, "+summary.FormattedTotalCost {
		t.Fatalf("unexpected formatted summary: %q", FormatSummary(summary))
	}
}

func TestFormatCostDisplay(t *testing.T) {
	tests := []struct {
		name         string
		coverage     string
		total        float64
		unpriced     int
		wantCost     string
		wantUnpriced string
	}{
		{name: "complete", coverage: CostCoverageComplete, total: 0.42, wantCost: "$0.4200"},
		{name: "partial positive", coverage: CostCoveragePartial, total: 0.42, unpriced: 3, wantCost: "~$0.4200", wantUnpriced: "3 unpriced requests"},
		{name: "partial singular", coverage: CostCoveragePartial, total: 0.42, unpriced: 1, wantCost: "~$0.4200", wantUnpriced: "1 unpriced request"},
		{name: "partial zero", coverage: CostCoveragePartial, total: 0, unpriced: 3, wantCost: "cost partial"},
		{name: "unavailable", coverage: CostCoverageUnavailable, total: 0.42, unpriced: 3, wantCost: "cost unavailable"},
		{name: "not applicable", coverage: CostCoverageNotApplicable, total: 0, wantCost: "cost n/a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FormatCostDisplay(test.coverage, test.total, test.unpriced)
			if got.Cost != test.wantCost || got.Unpriced != test.wantUnpriced {
				t.Fatalf("display = %#v, want cost %q and unpriced %q", got, test.wantCost, test.wantUnpriced)
			}
		})
	}
}

func TestTrackerPrefersPersistedEstimateAfterRegistryPriceChange(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{Registry: mustTestPricedRegistry(t)})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	persisted := 7.25
	first, err := tracker.Record(RecordInput{
		ModelID: "gpt-4.1",
		Usage:   zeroruntime.Usage{InputTokens: 100, OutputTokens: 20},
		Cost: &CostEstimate{
			CostUSD:        &persisted,
			CostStatus:     CostStatusPriced,
			CostProvenance: CostProvenancePersistedEstimate,
			PricingSource:  "persisted-catalog",
			PricingAsOf:    "2026-06-01",
		},
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}

	entries := tracker.registry.List(modelregistry.ListOptions{IncludeDeprecated: true})
	for index := range entries {
		if entries[index].ID == "gpt-4.1" {
			entries[index].Cost.InputPerMillion *= 10
			entries[index].Cost.OutputPerMillion *= 10
		}
	}
	changed, err := modelregistry.NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	tracker.registry = changed
	second, err := tracker.Record(RecordInput{
		ModelID: "gpt-4.1",
		Usage:   zeroruntime.Usage{InputTokens: 100, OutputTokens: 20},
		Cost: &CostEstimate{
			CostUSD:        &persisted,
			CostStatus:     CostStatusPriced,
			CostProvenance: CostProvenancePersistedEstimate,
			PricingSource:  "persisted-catalog",
			PricingAsOf:    "2026-06-01",
		},
	})
	if err != nil {
		t.Fatalf("Record returned error after registry change: %v", err)
	}
	if first.Cost.CostUSD == nil || second.Cost.CostUSD == nil || *second.Cost.CostUSD != persisted {
		t.Fatalf("persisted cost changed: first=%#v second=%#v", first.Cost, second.Cost)
	}
	if summary := tracker.Summary(); summary.PersistedCount != 2 || summary.TotalCost != persisted*2 {
		t.Fatalf("persisted summary = %#v", summary)
	}
}

func TestTrackerReconstructsCostWhenEstimateMissing(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{Registry: mustTestPricedRegistry(t)})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	record, err := tracker.Record(RecordInput{
		ModelID: "gpt-4.1",
		Usage:   zeroruntime.Usage{InputTokens: 100, OutputTokens: 20},
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if record.Cost == nil || record.Cost.CostStatus != CostStatusPriced || record.Cost.CostProvenance != CostProvenanceReconstructedEstimate {
		t.Fatalf("record cost = %#v, want reconstructed priced", record.Cost)
	}
	if summary := tracker.Summary(); summary.ReconstructedCount != 1 || summary.CostCoverage != CostCoverageComplete {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestTrackerRecordsUnpricedWithoutModelAndRetainsTokens(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{Registry: mustTestPricedRegistry(t)})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	record, err := tracker.Record(RecordInput{Usage: zeroruntime.Usage{InputTokens: 80, OutputTokens: 20}})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if record.Cost == nil || record.Cost.CostStatus != CostStatusUnpriced || record.Cost.CostUSD != nil {
		t.Fatalf("record cost = %#v, want unpriced with unknown cost", record.Cost)
	}
	summary := tracker.Summary()
	if summary.TotalTokens != 100 || summary.UnpricedCount != 1 || summary.CostCoverage != CostCoverageUnavailable || summary.TotalCost != 0 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestTrackerRecordsKnownUnpricedModelWithoutError(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	tracker, err := NewTracker(TrackerOptions{Registry: &registry})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	record, err := tracker.Record(RecordInput{
		ModelID: "claude-haiku-3.5",
		Usage:   zeroruntime.Usage{InputTokens: 100, OutputTokens: 20},
	})
	if err != nil {
		t.Fatalf("Record returned error for unpriced model: %v", err)
	}
	if record.Cost == nil || record.Cost.CostStatus != CostStatusUnpriced {
		t.Fatalf("record cost = %#v, want unpriced", record.Cost)
	}
	if summary := tracker.Summary(); summary.UnpricedCount != 1 || summary.CostCoverage != CostCoverageUnavailable || summary.ErrorCount != 0 {
		t.Fatalf("summary = %#v, want unavailable coverage without errors", summary)
	}
}

func TestTrackerDistinguishesPricedZeroFromUnpriced(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{Registry: mustTestPricedRegistry(t)})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	zero := 0.0
	priced, err := tracker.Record(RecordInput{
		Cost: &CostEstimate{
			CostUSD:        &zero,
			CostStatus:     CostStatusPriced,
			CostProvenance: CostProvenancePersistedEstimate,
			PricingSource:  "test",
			PricingAsOf:    "2026-06-01",
		},
		Usage: zeroruntime.Usage{InputTokens: 1},
	})
	if err != nil {
		t.Fatalf("Record priced zero returned error: %v", err)
	}
	unpriced, err := tracker.Record(RecordInput{Usage: zeroruntime.Usage{InputTokens: 1}})
	if err != nil {
		t.Fatalf("Record unpriced returned error: %v", err)
	}
	if priced.Cost.CostUSD == nil || *priced.Cost.CostUSD != 0 {
		t.Fatalf("priced zero cost = %#v", priced.Cost)
	}
	if unpriced.Cost.CostUSD != nil {
		t.Fatalf("unpriced cost = %#v, want nil CostUSD", unpriced.Cost)
	}
	summary := tracker.Summary()
	if summary.PersistedCount != 1 || summary.UnpricedCount != 1 || summary.CostCoverage != CostCoveragePartial {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestTrackerCoverageStates(t *testing.T) {
	zero := 0.0
	persisted := &CostEstimate{
		CostUSD:        &zero,
		CostStatus:     CostStatusPriced,
		CostProvenance: CostProvenancePersistedEstimate,
		PricingSource:  "test",
		PricingAsOf:    "2026-06-01",
	}
	tests := []struct {
		name     string
		inputs   []RecordInput
		coverage string
	}{
		{name: "none", coverage: CostCoverageNotApplicable},
		{name: "all unpriced", inputs: []RecordInput{{Usage: zeroruntime.Usage{InputTokens: 1}}}, coverage: CostCoverageUnavailable},
		{name: "mixed", inputs: []RecordInput{{Cost: persisted, Usage: zeroruntime.Usage{InputTokens: 1}}, {Usage: zeroruntime.Usage{InputTokens: 1}}}, coverage: CostCoveragePartial},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracker, err := NewTracker(TrackerOptions{Registry: mustTestPricedRegistry(t)})
			if err != nil {
				t.Fatalf("NewTracker returned error: %v", err)
			}
			for _, input := range test.inputs {
				if _, err := tracker.Record(input); err != nil {
					t.Fatalf("Record returned error: %v", err)
				}
			}
			if got := tracker.Summary().CostCoverage; got != test.coverage {
				t.Fatalf("coverage = %q, want %q", got, test.coverage)
			}
		})
	}
}

func TestTrackerRejectsInvalidUsageAndUnknownModels(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{Registry: mustTestPricedRegistry(t)})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	if _, err := tracker.Record(RecordInput{ModelID: "missing", Usage: zeroruntime.Usage{InputTokens: 1}}); err == nil {
		t.Fatal("expected unknown model error")
	}
	if _, err := tracker.Record(RecordInput{ModelID: "gpt-4.1", Usage: zeroruntime.Usage{InputTokens: -1}}); err == nil {
		t.Fatal("expected invalid usage error")
	}
	summary := tracker.Summary()
	if summary.ErrorCount != 1 || summary.TotalTokens != 1 || summary.CostCoverage != CostCoverageUnavailable {
		t.Fatalf("error record summary = %#v", summary)
	}
}

func TestTrackerTreatsReasoningAsOutputBreakdown(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{Registry: mustTestPricedRegistry(t)})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	record, err := tracker.Record(RecordInput{
		ModelID: "gpt-4.1",
		Usage: zeroruntime.Usage{
			InputTokens:     100,
			OutputTokens:    40,
			ReasoningTokens: 10,
		},
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if record.Usage.TotalTokens != 140 {
		t.Fatalf("total tokens = %d, want 140", record.Usage.TotalTokens)
	}
}

func TestTrackerResetClearsRecords(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{Registry: mustTestPricedRegistry(t)})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	if _, err := tracker.Record(RecordInput{ModelID: "gpt-4.1", Usage: zeroruntime.Usage{InputTokens: 1, OutputTokens: 1}}); err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	tracker.Reset()
	if summary := tracker.Summary(); summary.RecordCount != 0 || len(summary.ByModel) != 0 {
		t.Fatalf("Reset did not clear tracker: %#v", summary)
	}
}

func fixedUsageClock(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}

func TestNormalizeRejectsAllMalformedSubsets(t *testing.T) {
	tests := []struct {
		name    string
		usage   zeroruntime.Usage
		wantErr string
	}{
		{"cached exceeds input", zeroruntime.Usage{InputTokens: 10, CachedInputTokens: 15, OutputTokens: 5}, "cached input tokens"},
		{"cache write plus cached exceeds input", zeroruntime.Usage{InputTokens: 100, CachedInputTokens: 60, CacheWriteTokens: 50, OutputTokens: 10}, "cache write tokens"},
		{"reasoning exceeds output", zeroruntime.Usage{InputTokens: 100, OutputTokens: 10, ReasoningTokens: 20}, "reasoning tokens"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Normalize(tc.usage)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestNewCostEstimatorPricedZero(t *testing.T) {
	estimator := newDefaultCostEstimator(t)

	result := estimator("gpt-4.1", zeroruntime.Usage{InputTokens: 0, OutputTokens: 0}, true)
	if result.Status != "priced" {
		t.Fatalf("status = %q, want priced", result.Status)
	}
	if result.CostUSD == nil {
		t.Fatal("CostUSD should be non-nil for priced zero")
	}
	if *result.CostUSD != 0 {
		t.Fatalf("CostUSD = %v, want 0", *result.CostUSD)
	}
	if result.Provenance != "runtime_estimate" {
		t.Fatalf("provenance = %q, want runtime_estimate", result.Provenance)
	}
}

func TestNewCostEstimatorUnknownModel(t *testing.T) {
	estimator := newDefaultCostEstimator(t)

	result := estimator("nonexistent-model", zeroruntime.Usage{InputTokens: 100, OutputTokens: 50}, true)
	if result.Status != "unpriced" {
		t.Fatalf("status = %q, want unpriced", result.Status)
	}
	if result.CostUSD != nil {
		t.Fatalf("CostUSD should be nil for unpriced, got %v", *result.CostUSD)
	}
}

func TestNewCostEstimatorMissingUsage(t *testing.T) {
	estimator := NewCostEstimator(nil)

	result := estimator("gpt-4.1", zeroruntime.Usage{}, false)
	if result.Status != "unpriced" {
		t.Fatalf("status = %q, want unpriced", result.Status)
	}
	if result.UnpricedReason == "" {
		t.Fatal("unpriced_reason should not be empty")
	}
}

func TestNewCostEstimatorMalformedUsage(t *testing.T) {
	estimator := newDefaultCostEstimator(t)

	result := estimator("gpt-4.1", zeroruntime.Usage{InputTokens: 10, CachedInputTokens: 20, OutputTokens: 5}, true)
	if result.Status != "error" {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if result.UnpricedReason == "" {
		t.Fatal("unpriced_reason should not be empty for error")
	}
}

func TestNewCostEstimatorNilRegistry(t *testing.T) {
	estimator := NewCostEstimator(nil)

	result := estimator("gpt-4.1", zeroruntime.Usage{InputTokens: 100, OutputTokens: 50}, true)
	if result.Status != "unpriced" {
		t.Fatalf("status = %q, want unpriced for nil registry", result.Status)
	}
}

func newDefaultCostEstimator(t *testing.T) func(string, zeroruntime.Usage, bool) agent.UsageCostEstimate {
	t.Helper()
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	return NewCostEstimator(&registry)
}

func testPricedRegistry(t *testing.T) (modelregistry.Registry, error) {
	t.Helper()
	entries := modelregistry.DefaultModelEntries()
	prices := map[string]modelregistry.ModelCost{
		"gpt-4.1": {
			Currency: "USD", Unit: "per_1m_tokens", InputPerMillion: 2,
			CachedInputPerMillion: 0.5, OutputPerMillion: 8,
		},
		"claude-sonnet-4.5": {
			Currency: "USD", Unit: "per_1m_tokens", InputPerMillion: 3,
			CachedInputPerMillion: 0.3, CacheWritePerMillion: 3.75, OutputPerMillion: 15,
		},
	}
	for index := range entries {
		if cost, ok := prices[entries[index].ID]; ok {
			cost.Source = "test"
			cost.SourceLastVerified = "2026-01-01"
			entries[index].Cost = cost
		}
	}
	return modelregistry.NewRegistry(entries)
}

func mustTestPricedRegistry(t *testing.T) *modelregistry.Registry {
	t.Helper()
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("test priced registry: %v", err)
	}
	return &registry
}
