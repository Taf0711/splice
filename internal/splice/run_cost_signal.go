package splice

// Work package W3 (warm-cost review fold): the billed-cost signal the
// trajectory cost rule reads.
//
// The rule must not gate on the round count. Total cost is a sum over
// provider requests, and the round count is only one factor: a change that
// lowers per-request input or output lowers cost at a fixed round count, and
// fewer rounds do not imply lower cost. The signal below carries billed cost
// from the F2 workflow cost report and the provider request count, never raw
// token totals, because a round whose prefix is cache-read costs a fraction
// of a cold round and a token total hides that.

import (
	"errors"
	"fmt"
	"math"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// RunCostSignal is the billed-cost and provider-request change one iteration
// added. The trajectory cost rule reads it together with the correctness
// signal from the iteration state.
type RunCostSignal struct {
	// BilledCostDeltaUSD is the billed cost the latest iteration added, in US
	// dollars. It is the change in the F2 report's priced USD total.
	BilledCostDeltaUSD float64 `json:"billed_cost_delta_usd"`
	// ProviderRequestDelta is the number of provider requests the latest
	// iteration issued.
	ProviderRequestDelta int `json:"provider_request_delta"`
}

// Validate fails loud on a malformed signal. A NaN, an infinite cost, or a
// negative delta is a caller error, never a silent default.
func (s RunCostSignal) Validate() error {
	if math.IsNaN(s.BilledCostDeltaUSD) || math.IsInf(s.BilledCostDeltaUSD, 0) {
		return fmt.Errorf("run cost signal: billed cost delta %v is not finite", s.BilledCostDeltaUSD)
	}
	if s.BilledCostDeltaUSD < 0 {
		return fmt.Errorf("run cost signal: billed cost delta %v must be non-negative", s.BilledCostDeltaUSD)
	}
	if s.ProviderRequestDelta < 0 {
		return fmt.Errorf("run cost signal: provider request delta %d must be non-negative", s.ProviderRequestDelta)
	}
	return nil
}

// RunCostSignalFromReport derives one iteration's spend signal from the F2
// workflow cost report. It reads the billed USD total and the request count.
// A report whose cost coverage is not complete fails loud: missing pricing is
// unknown spend, never zero cost. previousBilledUSD and previousRequests are
// the report values sampled before the iteration.
func RunCostSignalFromReport(report *WorkflowCostReport, previousBilledUSD float64, previousRequests int) (RunCostSignal, error) {
	if report == nil {
		return RunCostSignal{}, errors.New("run cost signal: nil workflow cost report")
	}
	if report.CostCoverage != schemas.CostCoverageComplete {
		return RunCostSignal{}, fmt.Errorf("run cost signal: cost coverage %q is not complete, so billed cost is unknown", report.CostCoverage)
	}
	billed := 0.0
	if report.Totals.CostUSD != nil {
		billed = *report.Totals.CostUSD
	}
	sig := RunCostSignal{
		BilledCostDeltaUSD:   billed - previousBilledUSD,
		ProviderRequestDelta: report.Totals.Requests - previousRequests,
	}
	if err := sig.Validate(); err != nil {
		return RunCostSignal{}, err
	}
	return sig, nil
}

// spendViewForRecord normalizes one authoritative request-ledger record into
// the F2 report's view. The schema cost-status constants match the report's
// own priced/unpriced/error set, so the mapping is exact.
func spendViewForRecord(r schemas.PipelineUsageRecord) spendRecordView {
	status := costStatusUnpriced
	switch r.CostStatus {
	case schemas.CostStatusPriced:
		status = costStatusPriced
	case schemas.CostStatusUnpriced:
		status = costStatusUnpriced
	case schemas.CostStatusError:
		status = costStatusError
	}
	return spendRecordView{
		input:      r.InputTokens,
		output:     r.OutputTokens,
		cached:     r.CachedTokens,
		cacheWrite: r.CacheWrite,
		reasoning:  r.Reasoning,
		costUSD:    r.CostUSD,
		costStatus: status,
		source:     r.SpendSource,
		provenance: r.CostProvenance,
	}
}

// billedCostSnapshot is the cumulative billed spend of the authoritative
// request ledger at one moment in a run. Complete is false when any ledger
// record is unpriced or errored, so a caller must not read the cost as zero.
// Note explains an incomplete window in one sentence.
type billedCostSnapshot struct {
	// BilledUSD and Requests are the WHOLE-LEDGER totals: every request,
	// including auxiliary and capture spend. They feed global budget
	// enforcement, never the task-progress rule.
	BilledUSD float64
	Requests  int
	// TaskBilledUSD and TaskRequests sum the TASK spend sources only:
	// generation, format retry, expansion, and repair. The trajectory
	// progress rule reads these. Auxiliary work (a step-back analysis, a
	// future decision advisor) and capture are real spend inside the global
	// budget, but they are not task progress: including them would let the
	// monitor react to its own overhead.
	TaskBilledUSD float64
	TaskRequests  int
	Complete      bool
	Note          string
}

// taskSpendSources is the spend-source set whose deltas belong to the
// task-progress signal. Capture is deterministic today (no provider
// requests) and stays excluded by construction.
var taskSpendSources = map[string]bool{
	schemas.SpendSourceGeneration:  true,
	schemas.SpendSourceFormatRetry: true,
	schemas.SpendSourceExpansion:   true,
	schemas.SpendSourceRepair:      true,
}

// ledgerCostSnapshot joins the request ledger into the F2 report and reads the
// cumulative billed USD and request count. Incomplete pricing never fails the
// build: the snapshot records that coverage is incomplete so the caller keeps
// the cost rule inactive for that iteration.
func ledgerCostSnapshot(ledger *requestLedger) (billedCostSnapshot, error) {
	if ledger == nil {
		return billedCostSnapshot{Note: "request ledger unavailable"}, nil
	}
	if len(ledger.records) == 0 {
		return billedCostSnapshot{Note: "no request records yet"}, nil
	}
	views := make([]spendRecordView, 0, len(ledger.records))
	for _, r := range ledger.records {
		views = append(views, spendViewForRecord(r))
	}
	report, err := BuildWorkflowCostReport(views, func(i int) string { return views[i].source }, WorkflowCostOptions{})
	if err != nil {
		return billedCostSnapshot{}, fmt.Errorf("ledger cost snapshot: %w", err)
	}
	snap := billedCostSnapshot{Requests: report.Totals.Requests}
	if report.Totals.CostUSD != nil {
		snap.BilledUSD = *report.Totals.CostUSD
	}
	// Task-scoped sums over the named sources. Absent sources contribute
	// nothing (an absent source is unknown nothing-happened, never zero), a
	// nil cost on a present source contributes zero cost while its requests
	// still count, matching the whole-ledger treatment of Totals.CostUSD.
	for name, spend := range report.Sources {
		if !taskSpendSources[name] || spend == nil {
			continue
		}
		snap.TaskRequests += spend.Requests
		if spend.CostUSD != nil {
			snap.TaskBilledUSD += *spend.CostUSD
		}
	}
	snap.Complete = report.CostCoverage == schemas.CostCoverageComplete
	if !snap.Complete {
		snap.Note = fmt.Sprintf("cost coverage is %s, so billed cost is unknown", report.CostCoverage)
	}
	return snap, nil
}

// iterationCostSignal returns the billed-cost and provider-request delta the
// latest iteration added, scoped to the TASK spend sources. The trajectory
// progress rule consumes this signal: auxiliary or capture spend must never
// read as task progress, or the monitor reacts to its own overhead. Total
// spend enforcement keeps the whole-ledger snapshot. It returns nil when
// either window is incomplete: a missing price is never a zero cost, and the
// trajectory cost rule must stay inactive rather than fire on an unmeasured
// delta.
func iterationCostSignal(previous, current billedCostSnapshot) (*RunCostSignal, error) {
	if !current.Complete || !previous.Complete {
		return nil, nil
	}
	sig := RunCostSignal{
		BilledCostDeltaUSD:   current.TaskBilledUSD - previous.TaskBilledUSD,
		ProviderRequestDelta: current.TaskRequests - previous.TaskRequests,
	}
	if err := sig.Validate(); err != nil {
		return nil, err
	}
	return &sig, nil
}
