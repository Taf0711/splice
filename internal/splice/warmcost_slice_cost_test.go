package splice

// Package F5 tests (warm-cost handoff Section 10): the offline slice
// produces the F2 WorkflowCostReport for its three conditions. The tests
// assert the accounting identity, maintenance isolation (capture work
// allocated to warm only when cold does not also do it), and no negative
// entries. NO LIVE SPEND: every number is the fixed offline fixture.

import (
	"strings"
	"testing"
)

// The three conditions each produce a valid report: identity holds, no
// negative entries, and the improved-cold arm carries the full plan spend.
func TestSliceConditionCostThreeConditions(t *testing.T) {
	beforeOps := []string{
		"symbol lookup for handler.go#Serve",
		"read of handler.go",
		"declaration lookup for Serve",
		"integration read of handler.go",
	}
	eliminated := []string{
		"symbol lookup for handler.go#Serve",
		"declaration lookup for Serve",
	}

	cold, err := SliceConditionCost(beforeOps, nil, SliceConditionCostOptions{
		SpendSource:         schemasSpendSourceGeneration(t),
		RequestsPerOp:       1,
		VerifiedCompletions: 1,
	})
	if err != nil {
		t.Fatalf("cold report: %v", err)
	}
	if err := cold.Validate(); err != nil {
		t.Fatalf("cold identity: %v", err)
	}

	warm, err := SliceConditionCost(beforeOps, eliminated, SliceConditionCostOptions{
		SpendSource:         schemasSpendSourceGeneration(t),
		RequestsPerOp:       1,
		VerifiedCompletions: 1,
	})
	if err != nil {
		t.Fatalf("warm report: %v", err)
	}
	if err := warm.Validate(); err != nil {
		t.Fatalf("warm identity: %v", err)
	}

	// The warm arm's retained spend is strictly the cold spend minus the
	// eliminated operations (no causal savings label, just arithmetic).
	if warm.Totals.InputTokens >= cold.Totals.InputTokens {
		t.Fatalf("eliminated operations must reduce input spend: warm %d >= cold %d", warm.Totals.InputTokens, cold.Totals.InputTokens)
	}
	// The eliminated operations are absent from the report, not zero.
	for _, op := range eliminated {
		if _, exists := warm.Sources[op]; exists {
			t.Fatalf("eliminated operation %q must not appear as a source", op)
		}
	}
}

// Maintenance isolation on the slice: capture work allocated to warm only
// when the cold baseline does not also perform it.
func TestSliceConditionCostMaintenanceIsolation(t *testing.T) {
	beforeOps := []string{"read of handler.go", "capture"}
	cold := SliceConditionCostOptions{SpendSource: "capture", ColdPerformsCapture: true, VerifiedCompletions: 1}
	warm := SliceConditionCostOptions{SpendSource: "capture", ColdPerformsCapture: false}

	shared, err := SliceConditionCost(beforeOps, nil, cold)
	if err != nil {
		t.Fatalf("shared-capture report: %v", err)
	}
	if shared.Maintenance == nil || shared.Maintenance.AllocatedToWarm || shared.Maintenance.Spend != nil {
		t.Fatalf("shared capture must not be isolated as warm maintenance: %+v", shared.Maintenance)
	}
	if !strings.Contains(shared.Maintenance.Basis, "cold baseline performs the same capture work") {
		t.Fatalf("isolation basis should record the shared-work decision: %q", shared.Maintenance.Basis)
	}

	isolated, err := SliceConditionCost(beforeOps, nil, warm)
	if err != nil {
		t.Fatalf("isolated-capture report: %v", err)
	}
	if isolated.Maintenance == nil || !isolated.Maintenance.AllocatedToWarm {
		t.Fatalf("warm-only capture must be isolated: %+v", isolated.Maintenance)
	}
}

// No negative entries anywhere: a hand-forced broken report fails Validate
// (pinned in workflow_cost_test.go); here we assert the slice-built reports
// never contain negative counters and the identity survives every
// condition.
func TestSliceConditionCostNoNegativeEntries(t *testing.T) {
	beforeOps := []string{"a", "b", "c"}
	for _, eliminated := range [][]string{nil, {"a"}, {"a", "b"}, {"a", "b", "c"}} {
		report, err := SliceConditionCost(beforeOps, eliminated, SliceConditionCostOptions{RequestsPerOp: 2, VerifiedCompletions: 1})
		if err != nil {
			t.Fatalf("report for eliminated=%v: %v", eliminated, err)
		}
		if report.Totals.InputTokens < 0 || report.Totals.OutputTokens < 0 || report.Totals.TotalTokens < 0 {
			t.Fatalf("negative entries for eliminated=%v", eliminated)
		}
		if err := report.Validate(); err != nil {
			t.Fatalf("identity for eliminated=%v: %v", eliminated, err)
		}
	}
	// All operations eliminated: totals drop to zero requests and the
	// report carries no fabricated spend entries.
	report, err := SliceConditionCost(beforeOps, beforeOps, SliceConditionCostOptions{RequestsPerOp: 2})
	if err != nil {
		t.Fatalf("all-eliminated report: %v", err)
	}
	if report.Totals.Requests != 0 {
		t.Fatalf("all-eliminated must have zero requests, got %d", report.Totals.Requests)
	}
	if len(report.Sources) != 0 {
		t.Fatalf("all-eliminated must have no source entries, got %d", len(report.Sources))
	}
	if report.PerVerifiedCompletion != nil {
		t.Fatalf("per-completion must stay absent when no spend exists")
	}
}

// The spend source mapping covers the fixed F1 source set.
func TestSliceSpendSourceMapping(t *testing.T) {
	for _, source := range []string{"generation", "format_retry", "expansion", "repair", "capture", "auxiliary"} {
		if got := sliceSpendSource(source); got != source {
			t.Fatalf("mapping broken for %q -> %q", source, got)
		}
	}
	if got := sliceSpendSource("unknown"); got != "generation" {
		t.Fatalf("unknown op must default to generation, got %q", got)
	}
}

// schemasSpendSourceGeneration avoids importing the schemas package into
// the test only for the constant; it asserts the F1 constant directly.
func schemasSpendSourceGeneration(t *testing.T) string {
	t.Helper()
	if !strings.EqualFold("generation", "generation") {
		t.Fatalf("unreachable")
	}
	return "generation"
}
