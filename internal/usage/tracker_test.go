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
	tracker, err := NewTracker(TrackerOptions{Now: fixedUsageClock("2026-06-04T13:00:00Z")})
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

	summary := tracker.Summary()
	if summary.RecordCount != 1 || summary.TotalTokens != 1_250 || summary.ByModel[0].ModelID != "gpt-4.1" {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if FormatSummary(summary) != "1 request, 1,250 tokens, "+summary.FormattedTotalCost {
		t.Fatalf("unexpected formatted summary: %q", FormatSummary(summary))
	}
}

func TestTrackerRejectsInvalidUsageAndUnknownModels(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{})
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	if _, err := tracker.Record(RecordInput{ModelID: "missing", Usage: zeroruntime.Usage{InputTokens: 1}}); err == nil {
		t.Fatal("expected unknown model error")
	}
	if _, err := tracker.Record(RecordInput{ModelID: "gpt-4.1", Usage: zeroruntime.Usage{InputTokens: -1}}); err == nil {
		t.Fatal("expected invalid usage error")
	}
}

func TestTrackerTreatsReasoningAsOutputBreakdown(t *testing.T) {
	tracker, err := NewTracker(TrackerOptions{})
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
	tracker, err := NewTracker(TrackerOptions{})
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
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	return NewCostEstimator(&registry)
}
