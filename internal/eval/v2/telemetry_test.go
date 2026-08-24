package v2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLookupFilterCarriesFullExactIdentityAndRejectsDuplicates(t *testing.T) {
	key := TraceLookupKey{ExperimentID: "exp", TaskID: "task", Arm: ArmEmptyControl, RepetitionID: 1, EnvironmentBlock: 2, RunID: "run-1", SessionID: "session-1"}
	if err := key.Validate(); err != nil {
		t.Fatalf("key.Validate: %v", err)
	}
	filter := LookupFilter(key)
	data, err := json.Marshal(filter)
	if err != nil {
		t.Fatalf("marshal filter: %v", err)
	}
	for _, value := range []string{"exp", "task", "empty_control", "run-1", "session-1"} {
		if !strings.Contains(string(data), value) {
			t.Fatalf("filter JSON %s does not contain %q", data, value)
		}
	}
	result := TraceLookupResult(filter)
	if err := ValidateLookupResults(key, []TraceLookupResult{result}); err != nil {
		t.Fatalf("exact result rejected: %v", err)
	}
	if err := ValidateLookupResults(key, []TraceLookupResult{result, result}); err == nil || !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "run-1") {
		t.Fatalf("duplicate result error = %v", err)
	}
	result.TaskID = "other-task"
	if err := ValidateLookupResults(key, []TraceLookupResult{result}); err == nil || !strings.Contains(err.Error(), "exactly match") {
		t.Fatalf("non-exact result accepted: %v", err)
	}
}

func TestVerifyRoutesReportsEveryDriftClass(t *testing.T) {
	manifest := validManifest()
	key := TraceLookupKey{ExperimentID: manifest.Protocol.ExperimentID, TaskID: manifest.Tasks[0].ID, Arm: manifest.Protocol.Arms[0].Name, RepetitionID: 1, EnvironmentBlock: 1, RunID: "run", SessionID: "session"}
	if err := VerifyRoutes(manifest.StageRoutes, manifest, key); err != nil {
		t.Fatalf("matching routes rejected: %v", err)
	}
	cases := []struct {
		name, want string
		mutate     func([]StageRoute) []StageRoute
	}{
		{"provider drift", "field=provider", func(routes []StageRoute) []StageRoute { routes[0].Provider = "other"; return routes }},
		{"model drift", "field=model", func(routes []StageRoute) []StageRoute { routes[0].Model = "other"; return routes }},
		{"effort drift", "field=reasoning_effort", func(routes []StageRoute) []StageRoute { routes[0].ReasoningEffort = "high"; return routes }},
		{"missing stage", "observed_route=<absent>", func(routes []StageRoute) []StageRoute { return nil }},
		{"extra stage", "expected_route=<absent>", func(routes []StageRoute) []StageRoute {
			return append(routes, StageRoute{Stage: "extra", Provider: "p", Model: "m"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routes := append([]StageRoute(nil), manifest.StageRoutes...)
			if err := VerifyRoutes(tc.mutate(routes), manifest, key); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("VerifyRoutes error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCompletenessReportsAllGapsAndZeroFillAdvisory(t *testing.T) {
	valid := validTelemetry()
	if report := CheckCompleteness(valid); !report.Complete() {
		t.Fatalf("valid telemetry incomplete: %v", report.InvalidReasons())
	}
	partial := valid
	partial.PricingCoverage = PricingPartial
	report := CheckCompleteness(partial)
	if report.Complete() || !containsGap(report, "pricing_coverage", "claim coverage must be full") {
		t.Fatalf("partial pricing report = %+v", report)
	}
	broken := valid
	broken.Source = ""
	broken.SessionID = ""
	broken.PricingCoverage = PricingNone
	broken.DispositionsComplete = boolp(false)
	broken.Tokens.TotalTokens = nil
	report = CheckCompleteness(broken)
	for _, want := range []string{"source", "session_id", "pricing_coverage", "dispositions_complete", "tokens"} {
		if !hasField(report, want) {
			t.Fatalf("report missing %q: %+v", want, report.InvalidReasons())
		}
	}
	zero := validTelemetry()
	for _, usage := range []*TokenUsage{&zero.Tokens, &zero.Stages[0].Requests[0].Usage} {
		*usage.TotalTokens = 0
		*usage.InputTokens = 0
		*usage.CachedInputTokens = 0
		*usage.CacheWriteTokens = 0
		*usage.OutputTokens = 0
		*usage.ReasoningTokens = 0
	}
	if err := RejectZeroFilled(zero); err == nil || !strings.Contains(err.Error(), "zero-filled") {
		t.Fatalf("zero-filled telemetry not detected: %v", err)
	}
	legitimate := validTelemetry()
	*legitimate.ProviderCostUSD = 0
	*legitimate.Stages[0].Requests[0].ProviderCostUSD = 0
	if err := RejectZeroFilled(legitimate); err != nil {
		t.Fatalf("legitimate zero-cost run rejected: %v", err)
	}
}

func TestFallbackComparisonExactFieldsAndSummary(t *testing.T) {
	trace := usageForTest(10, 7, 3)
	stream := usageForTest(10, 7, 3)
	match, diffs := CompareFallback(trace, stream)
	if !match || len(diffs) != 0 {
		t.Fatalf("equal fallback comparison = %v, %v", match, diffs)
	}
	*stream.OutputTokens = 4
	match, diffs = CompareFallback(trace, stream)
	if match || len(diffs) != 1 || diffs[0] != "output_tokens" {
		t.Fatalf("single-field drift = %v, %v", match, diffs)
	}
	key := TrialKey{ExperimentID: "exp", TaskID: "task", Arm: ArmEmptyControl, RepetitionID: 1, EnvironmentBlock: 1}
	summary := FallbackEquivalenceSummary{Pairs: 2, Matched: 1, Mismatches: []FallbackPair{{Key: key, FromTrace: trace, FromStream: stream}}}
	if err := summary.Validate(); err != nil {
		t.Fatalf("valid fallback summary rejected: %v", err)
	}
	bad := summary
	bad.Mismatches = nil
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "no mismatches") {
		t.Fatalf("incomplete summary accepted: %v", err)
	}
}

func containsGap(report CompletenessReport, field, rule string) bool {
	for _, gap := range report.Gaps {
		if gap.Field == field && gap.Rule == rule {
			return true
		}
	}
	return false
}
func hasField(report CompletenessReport, field string) bool {
	for _, gap := range report.Gaps {
		if gap.Field == field {
			return true
		}
	}
	return false
}
func usageForTest(total, input, output uint64) TokenUsage {
	zero := uint64(0)
	return TokenUsage{TotalTokens: &total, InputTokens: &input, CachedInputTokens: &zero, CacheWriteTokens: &zero, OutputTokens: &output, ReasoningTokens: &zero}
}
