package v2

import (
	"fmt"
	"sort"
)

// CompletenessGap is one independently reportable telemetry requirement.
type CompletenessGap struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
}

// CompletenessReport lists every detected telemetry gap. It is consumed by
// the future runner post-attempt gate and report.
type CompletenessReport struct {
	Gaps []CompletenessGap `json:"gaps"`
}

// CheckCompleteness reports all known floor and claim-run telemetry gaps in
// one pass. Missing values are invalid and are never converted to zero.
func CheckCompleteness(r TelemetryRecord) CompletenessReport {
	report := CompletenessReport{}
	add := func(field, rule string) {
		for _, gap := range report.Gaps {
			if gap.Field == field && gap.Rule == rule {
				return
			}
		}
		report.Gaps = append(report.Gaps, CompletenessGap{Field: field, Rule: rule})
	}
	if !ValidTelemetrySource(r.Source) {
		add("source", "must be present and known")
	}
	if r.RunID == "" {
		add("run_id", "required")
	}
	if r.SessionID == "" {
		add("session_id", "required")
	}
	if len(r.Stages) == 0 {
		add("stages", "at least one stage is required")
	}
	if err := r.Tokens.Validate(); err != nil {
		add("tokens", err.Error())
	}
	if r.DispositionsComplete == nil || !*r.DispositionsComplete {
		add("dispositions_complete", "must be true")
	}
	if r.InvalidClaimCount == nil || *r.InvalidClaimCount < 0 {
		add("invalid_claim_count", "required and non-negative")
	}
	if r.DeterministicCheckPassed == nil {
		add("deterministic_check_passed", "required")
	}
	if !validHash(r.DeterministicCheckOutputSHA256) {
		add("deterministic_check_output_sha256", "must be a sha256 hex digest")
	}
	if r.RetryCount == nil || *r.RetryCount < 0 {
		add("retry_count", "required and non-negative")
	}
	if r.RepairCount == nil || *r.RepairCount < 0 {
		add("repair_count", "required and non-negative")
	}
	if r.CompactionCalls == nil || *r.CompactionCalls < 0 {
		add("compaction_calls", "required and non-negative")
	}
	if r.WallTimeMs == nil || *r.WallTimeMs < 0 {
		add("wall_time_ms", "required and non-negative")
	}
	if r.MemoryQueryLatencyMs == nil || *r.MemoryQueryLatencyMs < 0 {
		add("memory_query_latency_ms", "required and non-negative")
	}
	if r.ProviderCostUSD == nil || *r.ProviderCostUSD < 0 || !finiteValue(r.ProviderCostUSD) {
		add("provider_cost_usd", "required and finite non-negative")
	}
	if r.WebSearchCostUSD == nil || *r.WebSearchCostUSD < 0 || !finiteValue(r.WebSearchCostUSD) {
		add("web_search_cost_usd", "required and finite non-negative")
	}
	if r.WebSearchRequests == nil {
		add("web_search_requests", "required")
	}
	if r.WebSearchEngines == nil {
		add("web_search_engines", "must be present, even when empty")
	}
	if r.PricingCoverage != PricingFull {
		add("pricing_coverage", "claim coverage must be full")
	}
	if !ValidPricingCoverage(r.PricingCoverage) {
		add("pricing_coverage", "must be a known coverage value")
	}
	if r.SelectedMemoryIDs == nil {
		add("selected_memory_ids", "must be present, even when empty")
	}
	if r.DeliveredMemoryIDs == nil {
		add("delivered_memory_ids", "must be present, even when empty")
	}
	if r.Dispositions == nil {
		add("dispositions", "must be present, even when empty")
	}
	for i, stage := range r.Stages {
		if err := stage.Validate(); err != nil {
			add(fmt.Sprintf("stages[%d]", i), err.Error())
		}
	}
	if err := r.Validate(); err != nil {
		add("record", err.Error())
	}
	sort.Slice(report.Gaps, func(i, j int) bool {
		if report.Gaps[i].Field == report.Gaps[j].Field {
			return report.Gaps[i].Rule < report.Gaps[j].Rule
		}
		return report.Gaps[i].Field < report.Gaps[j].Field
	})
	return report
}

// Complete reports whether no completeness gap was found.
func (r CompletenessReport) Complete() bool { return len(r.Gaps) == 0 }

// InvalidReasons returns deterministic human-readable reasons.
func (r CompletenessReport) InvalidReasons() []string {
	reasons := make([]string, 0, len(r.Gaps))
	for _, gap := range r.Gaps {
		reasons = append(reasons, gap.Field+": "+gap.Rule)
	}
	sort.Strings(reasons)
	return reasons
}

// RejectZeroFilled is an advisory cycle-2 detector. It flags all-zero token
// fields with positive wall time and stages, but permits legitimate zero-cost
// runs when token fields are populated.
func RejectZeroFilled(r TelemetryRecord) error {
	if len(r.Stages) == 0 || r.WallTimeMs == nil || *r.WallTimeMs <= 0 || !allTokenFieldsPresentAndZero(r.Tokens) {
		return nil
	}
	return fmt.Errorf("advisory zero-filled telemetry detected: all token fields are zero while wall_time_ms=%d and stages ran", *r.WallTimeMs)
}

func allTokenFieldsPresentAndZero(u TokenUsage) bool {
	return u.TotalTokens != nil && u.InputTokens != nil && u.CachedInputTokens != nil && u.CacheWriteTokens != nil && u.OutputTokens != nil && u.ReasoningTokens != nil && *u.TotalTokens == 0 && *u.InputTokens == 0 && *u.CachedInputTokens == 0 && *u.CacheWriteTokens == 0 && *u.OutputTokens == 0 && *u.ReasoningTokens == 0
}

func finiteValue(value *float64) bool {
	return value != nil && *value == *value && *value < 1.7976931348623157e308 && *value > -1.7976931348623157e308
}
