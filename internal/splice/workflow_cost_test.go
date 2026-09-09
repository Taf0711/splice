package splice

// Package F2 tests (warm-cost handoff Section 10): the WorkflowCostReport
// accounting identity, failed-attempt spend, maintenance isolation, provider
// cache separation, and absence semantics for unknown fields.

import (
	"strings"
	"testing"
)

func f2View(cost float64, status string, cached, cacheWrite int) spendRecordView {
	v := spendRecordView{
		input:      100,
		output:     50,
		cached:     cached,
		cacheWrite: cacheWrite,
		reasoning:  10,
		costStatus: status,
	}
	if status == costStatusPriced {
		c := cost
		v.costUSD = &c
	}
	return v
}

// The accounting identity: sum over sources equals totals for every counter,
// including cost.
func TestWorkflowCostReportIdentity(t *testing.T) {
	views := []spendRecordView{
		f2View(0.10, costStatusPriced, 40, 10),
		f2View(0.20, costStatusPriced, 40, 10),
		f2View(0.05, costStatusPriced, 0, 0),
	}
	sources := []string{"generation", "expansion", "repair"}
	report, err := BuildWorkflowCostReport(views, func(i int) string { return sources[i] }, WorkflowCostOptions{VerifiedCompletions: 1})
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("identity: %v", err)
	}
	if report.Totals.InputTokens != 300 || report.Totals.OutputTokens != 150 || report.Totals.TotalTokens != 450 {
		t.Fatalf("totals mismatch: %+v", report.Totals)
	}
	if report.Totals.CostUSD == nil || *report.Totals.CostUSD < 0.349 || *report.Totals.CostUSD > 0.351 {
		t.Fatalf("cost total mismatch: %v", report.Totals.CostUSD)
	}
	if len(report.Sources) != 3 {
		t.Fatalf("expected 3 sources, got %d", len(report.Sources))
	}
}

// Failed-attempt spend is included: the report builder never filters
// records by outcome, so a failed attempt's usage rides in the totals.
func TestWorkflowCostReportIncludesFailedAttemptSpend(t *testing.T) {
	views := []spendRecordView{
		f2View(0.10, costStatusPriced, 0, 0), // failed attempt 1
		f2View(0.15, costStatusPriced, 0, 0), // failed attempt 2 (repair)
		f2View(0.10, costStatusPriced, 0, 0), // the attempt that completed
	}
	report, err := BuildWorkflowCostReport(views, func(i int) string {
		if i == 1 {
			return "repair"
		}
		return "generation"
	}, WorkflowCostOptions{VerifiedCompletions: 1})
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if report.Totals.TotalTokens != 450 {
		t.Fatalf("failed-attempt spend missing from totals: %+v", report.Totals)
	}
	if report.PerVerifiedCompletion == nil || !report.PerVerifiedCompletion.IncludesFailedAttemptSpend {
		t.Fatalf("per-completion must include failed-attempt spend")
	}
	if report.PerVerifiedCompletion.TotalTokensPerCompletion == nil || *report.PerVerifiedCompletion.TotalTokensPerCompletion != 450 {
		t.Fatalf("per-completion total mismatch: %v", report.PerVerifiedCompletion)
	}
}

// Maintenance isolation: capture spend is charged to warm only when the cold
// baseline does not also perform it.
func TestWorkflowCostReportMaintenanceIsolation(t *testing.T) {
	views := []spendRecordView{
		f2View(0.10, costStatusPriced, 0, 0),
		f2View(0.02, costStatusPriced, 0, 0),
	}
	sources := []string{"generation", "capture"}

	// Cold performs capture too: no incremental maintenance (absent spend).
	warm, err := BuildWorkflowCostReport(views, func(i int) string { return sources[i] }, WorkflowCostOptions{ColdPerformsCapture: true})
	if err != nil {
		t.Fatalf("build warm report: %v", err)
	}
	if warm.Maintenance == nil {
		t.Fatalf("maintenance explanation missing")
	}
	if warm.Maintenance.AllocatedToWarm || warm.Maintenance.Spend != nil {
		t.Fatalf("capture spend must not be isolated when cold performs it: %+v", warm.Maintenance)
	}
	if strings.Contains(warm.Maintenance.Basis, "isolated") {
		t.Fatalf("basis should describe the shared-work decision, got %q", warm.Maintenance.Basis)
	}

	// Cold does not perform capture: capture isolates as warm-only
	// maintenance and aliases the capture source entry.
	cold, err := BuildWorkflowCostReport(views, func(i int) string { return sources[i] }, WorkflowCostOptions{ColdPerformsCapture: false})
	if err != nil {
		t.Fatalf("build cold report: %v", err)
	}
	if cold.Maintenance == nil || !cold.Maintenance.AllocatedToWarm || cold.Maintenance.Spend == nil {
		t.Fatalf("capture spend must isolate as maintenance: %+v", cold.Maintenance)
	}
	if cold.Maintenance.Spend != cold.Sources["capture"] {
		t.Fatalf("maintenance spend must alias the capture entry")
	}
	if *cold.Maintenance.Spend.CostUSD != 0.02 {
		t.Fatalf("maintenance spend mismatch: %v", cold.Maintenance.Spend.CostUSD)
	}
}

// No capture spend at all: maintenance stays absent, not zero.
func TestWorkflowCostReportMaintenanceAbsentWithoutCapture(t *testing.T) {
	report, err := BuildWorkflowCostReport([]spendRecordView{f2View(0.1, costStatusPriced, 0, 0)}, nil, WorkflowCostOptions{})
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if report.Maintenance != nil {
		t.Fatalf("maintenance must be absent without capture spend")
	}
	if report.Sources["capture"] != nil {
		t.Fatalf("capture source must be absent without capture records")
	}
}

// Provider-cache effects are reported separately from raw tokens: cached
// input never reduces the raw input totals.
func TestWorkflowCostReportProviderCacheSeparate(t *testing.T) {
	views := []spendRecordView{
		f2View(0.10, costStatusPriced, 100, 20),
		f2View(0.10, costStatusUnpriced, 50, 0),
	}
	report, err := BuildWorkflowCostReport(views, nil, WorkflowCostOptions{})
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if !report.ProviderCache.ReportedSeparately {
		t.Fatalf("cache report must carry the separation contract")
	}
	if report.ProviderCache.CachedInputTokens != 150 || report.ProviderCache.CacheWriteTokens != 20 {
		t.Fatalf("cache subset mismatch: %+v", report.ProviderCache)
	}
	if report.Totals.InputTokens != 200 {
		t.Fatalf("raw input tokens must not be reduced by cache hits: %d", report.Totals.InputTokens)
	}
}

// Unknown stays absent: an unpriced record contributes no cost, the ratio is
// absent with zero completions, and sources without requests never appear.
func TestWorkflowCostReportUnknownStaysAbsent(t *testing.T) {
	views := []spendRecordView{
		f2View(0, costStatusUnpriced, 0, 0),
		f2View(0, costStatusError, 0, 0),
	}
	report, err := BuildWorkflowCostReport(views, nil, WorkflowCostOptions{})
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if report.Totals.CostUSD != nil {
		t.Fatalf("unknown usage must not fabricate a cost")
	}
	if report.PerVerifiedCompletion != nil {
		t.Fatalf("per-completion ratio must be absent with zero completions")
	}
	if report.CostCoverage != "unavailable" {
		t.Fatalf("coverage with no priced records must be unavailable, got %q", report.CostCoverage)
	}
	if report.Sources["expansion"] != nil {
		t.Fatalf("absent source must not appear as a zero entry")
	}
}

// A zero cold cost leaves the saving ratio undefined: the report keeps the
// per-completion fields present (with their totals) rather than faking a
// ratio against zero.
func TestWorkflowCostReportZeroCostArm(t *testing.T) {
	views := []spendRecordView{f2View(0, costStatusUnpriced, 0, 0)}
	report, err := BuildWorkflowCostReport(views, nil, WorkflowCostOptions{VerifiedCompletions: 1})
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if report.Totals.CostUSD != nil {
		t.Fatalf("unpriced arm must not show a cost")
	}
	if report.PerVerifiedCompletion == nil || report.PerVerifiedCompletion.CostUSDPerCompletion != nil {
		t.Fatalf("cost-per-completion must stay absent when the arm has no priced usage")
	}
}

// Identity violations are loud: hand-built reports fail validation.
func TestWorkflowCostReportValidateRejectsBrokenIdentity(t *testing.T) {
	report := &WorkflowCostReport{
		Sources: map[string]*WorkflowSourceSpend{
			"generation": {Requests: 1, InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
		Totals: WorkflowSourceSpend{Requests: 2, InputTokens: 200, OutputTokens: 100, TotalTokens: 300},
	}
	if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "identity broken") {
		t.Fatalf("expected identity failure, got %v", err)
	}
	negative := &WorkflowCostReport{
		Sources: map[string]*WorkflowSourceSpend{
			"generation": {Requests: 1, InputTokens: -5, OutputTokens: 0, TotalTokens: -5},
		},
		Totals: WorkflowSourceSpend{Requests: 1, InputTokens: -5, OutputTokens: 0, TotalTokens: -5},
	}
	if err := negative.Validate(); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("expected negative-entry failure, got %v", err)
	}
}
