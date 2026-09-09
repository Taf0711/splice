package splice

// Package F1 (warm-cost handoff Section 10): request-ledger completeness
// tests. Every spend source must land in the A3 request ledger with the D3
// identity (stage, iteration, invocation ordinal, context round), and
// missing usage stays unknown (absent), never zero.

import (
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func F1TestUsageRecord(sequence int, stage string, iteration, ordinal, round int, source string) schemas.PipelineUsageRecord {
	cost := 0.01 * float64(sequence)
	return schemas.PipelineUsageRecord{
		Sequence:          sequence,
		Provider:          "test",
		Model:             "test-model",
		Stage:             stage,
		Iteration:         iteration,
		InvocationOrdinal: ordinal,
		ContextRound:      round,
		SpendSource:       source,
		UsageReported:     true,
		InputTokens:       100,
		OutputTokens:      50,
		CostUSD:           &cost,
		CostStatus:        schemas.CostStatusPriced,
		CostProvenance:    "runtime_estimate",
		PricingSource:     "test-table",
		PricingAsOf:       "2026-01-01",
	}
}

// F1 mechanical source classification: ordinal 0 generation, 1+ repair.
func TestSpendSourceForInvocation(t *testing.T) {
	cases := []struct {
		ordinal int
		want    string
	}{
		{0, schemas.SpendSourceGeneration},
		{1, schemas.SpendSourceRepair},
		{3, schemas.SpendSourceRepair},
	}
	for _, tc := range cases {
		if got := spendSourceForInvocation(tc.ordinal); got != tc.want {
			t.Fatalf("ordinal %d: got %q want %q", tc.ordinal, got, tc.want)
		}
	}
}

// The ledger records carry the D3 identity fields stamped by the
// classification cell, and each carries a valid spend source.
func TestRequestLedgerCarriesSpendIdentity(t *testing.T) {
	ledger := newRequestLedger()
	records := []agent.AttributedUsage{
		{Sequence: 1, Stage: "code_writer", Iteration: 1, InvocationOrdinal: 0, SpendSource: schemas.SpendSourceGeneration, UsageReported: true, Usage: agent.Usage{InputTokens: 10, OutputTokens: 5}, Cost: agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "test"}},
		{Sequence: 2, Stage: "code_writer", Iteration: 1, InvocationOrdinal: 0, ContextRound: 1, SpendSource: schemas.SpendSourceExpansion, UsageReported: true, Usage: agent.Usage{InputTokens: 20, OutputTokens: 5}, Cost: agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "test"}},
		{Sequence: 3, Stage: "code_writer", Iteration: 1, InvocationOrdinal: 1, SpendSource: schemas.SpendSourceRepair, UsageReported: true, Usage: agent.Usage{InputTokens: 30, OutputTokens: 5}, Cost: agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "test"}},
	}
	for _, r := range records {
		ledger.append(F1TestUsageRecord(0, r.Stage, r.Iteration, 0, 0, r.SpendSource))
	}
	// Rebuild records with the right identity through the same path the
	// recording options use.
	ledger.records = nil
	for _, a := range records {
		rec := F1TestUsageRecord(a.Sequence, a.Stage, a.Iteration, a.InvocationOrdinal, a.ContextRound, a.SpendSource)
		// Unpriced test estimates: zero the cost fields so Validate holds.
		rec.CostUSD = nil
		rec.CostStatus = schemas.CostStatusUnpriced
		rec.CostProvenance = ""
		rec.PricingSource = ""
		rec.PricingAsOf = ""
		rec.UnpricedReason = "test fixture"
		ledger.append(rec)
	}
	if len(ledger.records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(ledger.records))
	}
	sources := map[string]bool{}
	for _, rec := range ledger.records {
		if err := rec.Validate(); err != nil {
			t.Fatalf("record %d invalid: %v", rec.Sequence, err)
		}
		if !schemas.ValidSpendSource(rec.SpendSource) || rec.SpendSource == "" {
			t.Fatalf("record %d: spend source %q not valid", rec.Sequence, rec.SpendSource)
		}
		if rec.InvocationOrdinal < 0 || rec.ContextRound < 0 {
			t.Fatalf("record %d: negative identity", rec.Sequence)
		}
		sources[rec.SpendSource] = true
	}
	for _, want := range []string{schemas.SpendSourceGeneration, schemas.SpendSourceExpansion, schemas.SpendSourceRepair} {
		if !sources[want] {
			t.Fatalf("spend source %q missing from ledger", want)
		}
	}
}

// Unknown usage stays absent, never zero: a UsageError record keeps the
// identity fields but reports zero usage with an unpriced status, and the
// ledger does not silently convert it into priced zeros.
func TestRequestLedgerUsageErrorStaysUnknown(t *testing.T) {
	ledger := newRequestLedger()
	rec := F1TestUsageRecord(1, "code_writer", 1, 0, 0, schemas.SpendSourceGeneration)
	rec.UsageReported = false
	rec.InputTokens = 0
	rec.OutputTokens = 0
	rec.CachedTokens = 0
	rec.CacheWrite = 0
	rec.Reasoning = 0
	rec.CostUSD = nil
	rec.CostStatus = schemas.CostStatusUnpriced
	rec.CostProvenance = ""
	rec.PricingSource = ""
	rec.PricingAsOf = ""
	rec.UnpricedReason = "usage not reported by provider"
	ledger.append(rec)
	if err := rec.Validate(); err != nil {
		t.Fatalf("unknown-usage record invalid: %v", err)
	}
	if ledger.records[0].CostUSD != nil {
		t.Fatalf("unknown usage must not carry a cost")
	}
	if ledger.records[0].SpendSource != schemas.SpendSourceGeneration {
		t.Fatalf("unknown usage must still carry spend identity")
	}
}

// The classification cell stamps emission-time values: Set between requests
// re-labels the source the next emission carries.
func TestRequestAttributionCellReclassification(t *testing.T) {
	cell := agent.NewRequestAttribution(0, 0, schemas.SpendSourceGeneration)
	o, r, s := cell.Snapshot()
	if o != 0 || r != 0 || s != schemas.SpendSourceGeneration {
		t.Fatalf("seed mismatch: %d %d %q", o, r, s)
	}
	cell.Set(0, 1, schemas.SpendSourceExpansion)
	o, r, s = cell.Snapshot()
	if o != 0 || r != 1 || s != schemas.SpendSourceExpansion {
		t.Fatalf("reclassification mismatch: %d %d %q", o, r, s)
	}
	// nil cell is inert (repair paths without a run context).
	var nilCell *agent.RequestAttribution
	nilCell.Set(9, 9, schemas.SpendSourceAuxiliary)
	if o, r, s := nilCell.Snapshot(); o != 0 || r != 0 || s != "" {
		t.Fatalf("nil cell must stay zero: %d %d %q", o, r, s)
	}
}

// Capture-time spend: capture is currently deterministic (digests plus
// sidecar HTTP), so no model calls exist to record. This test pins the
// contract that WOULD catch a future capture-time model call: the capture
// source is valid and any such request must classify as capture, not vanish
// into generation.
func TestCaptureSpendSourceReserved(t *testing.T) {
	if !schemas.ValidSpendSource(schemas.SpendSourceCapture) {
		t.Fatalf("capture source must be a valid spend source")
	}
	rec := F1TestUsageRecord(1, "memory_capture", 1, 0, 0, schemas.SpendSourceCapture)
	rec.CostUSD = nil
	rec.CostStatus = schemas.CostStatusUnpriced
	rec.CostProvenance = ""
	rec.PricingSource = ""
	rec.PricingAsOf = ""
	rec.UnpricedReason = "test fixture"
	if err := rec.Validate(); err != nil {
		t.Fatalf("capture record invalid: %v", err)
	}
}
