package splice

// Work package F2 (warm-cost handoff Section 10): the typed workflow cost
// report. It joins the A3 request ledger across the FULL A-to-B workflow and
// answers the Section 11 campaign questions:
//
//   - per-arm totals split by spend source (generation, format retries,
//     expansion, repair, capture, auxiliary);
//   - maintenance cost ISOLATED: only capture work the cold baseline does not
//     also perform is charged to warm as incremental maintenance;
//   - per-verified-completion cost including failed-attempt spend (the
//     ledger keeps failed and killed attempts, so the totals include them by
//     construction);
//   - provider-cache effects reported SEPARATELY from raw tokens: cached
//     input and cache-write tokens never reduce the raw totals.
//
// The accounting identity is pinned: the sum over source entries equals the
// totals entry for every counter. Unknown usage stays absent (pointer or
// omitted entry), never zero (A3 convention). No negative entries are
// possible; Validate rejects them in case a future caller constructs the
// report by hand.

import (
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// WorkflowSourceSpend is the raw-token and cost spend of one spend source.
// Token counters are raw: cached input and cache-write subsets are reported
// separately and are already included in InputTokens per the A3
// normalization, so they are never subtracted or added twice.
type WorkflowSourceSpend struct {
	Requests            int      `json:"requests"`
	InputTokens         int      `json:"input_tokens"`
	OutputTokens        int      `json:"output_tokens"`
	TotalTokens         int      `json:"total_tokens"`
	CachedInputTokens   int      `json:"cached_input_tokens,omitempty"`
	CacheWriteTokens    int      `json:"cache_write_tokens,omitempty"`
	ReasoningTokens     int      `json:"reasoning_tokens,omitempty"`
	CostUSD             *float64 `json:"cost_usd,omitempty"`
	UnpricedRequests    int      `json:"unpriced_requests,omitempty"`
	ErrorRequests       int      `json:"error_requests,omitempty"`
	PricedRequests      int      `json:"priced_requests,omitempty"`
	PricingCoverageNote string   `json:"pricing_coverage_note,omitempty"`
}

// add accumulates one ledger record into the source spend.
func (s *WorkflowSourceSpend) add(r spendRecordView) {
	s.Requests++
	s.InputTokens += r.input
	s.OutputTokens += r.output
	s.TotalTokens += r.input + r.output
	s.CachedInputTokens += r.cached
	s.CacheWriteTokens += r.cacheWrite
	s.ReasoningTokens += r.reasoning
	switch r.costStatus {
	case costStatusPriced:
		s.PricedRequests++
		if r.costUSD != nil {
			cost := *r.costUSD
			if s.CostUSD == nil {
				s.CostUSD = &cost
			} else {
				merged := *s.CostUSD + cost
				s.CostUSD = &merged
			}
		}
	case costStatusUnpriced:
		s.UnpricedRequests++
	case costStatusError:
		s.ErrorRequests++
	}
}

// spendRecordView is the normalized view of one ledger record the report
// consumes. It decouples the report from schema internals so the offline
// slice can feed synthesized records without a pipeline run.
type spendRecordView struct {
	input      int
	output     int
	cached     int
	cacheWrite int
	reasoning  int
	costUSD    *float64
	costStatus string
}

// spendSources is the fixed source set; the identity sums over it.
var spendSources = []string{
	"generation",
	"format_retry",
	"expansion",
	"repair",
	"capture",
	"auxiliary",
}

// MaintenanceCost is the isolated warm-only maintenance spend: capture work
// the cold baseline does not also perform. When the cold baseline performs
// the same capture work, no isolated entry exists (nil, absent) rather than
// a fabricated zero.
type MaintenanceCost struct {
	// AllocatedToWarm reports whether any isolated maintenance was charged.
	AllocatedToWarm bool `json:"allocated_to_warm"`
	// Spend is the isolated capture-source spend. Present only when
	// AllocatedToWarm is true.
	Spend *WorkflowSourceSpend `json:"spend,omitempty"`
	// Basis explains the allocation decision in one sentence.
	Basis string `json:"basis"`
}

// PerVerifiedCompletionCost divides full-workflow spend (all launched
// attempts, failed ones included) by the number of verified completions.
// Pointers stay nil when the denominator is zero: an absent ratio is never
// reported as zero.
type PerVerifiedCompletionCost struct {
	VerifiedCompletions       int      `json:"verified_completions"`
	InputTokensPerCompletion  *float64 `json:"input_tokens_per_completion,omitempty"`
	OutputTokensPerCompletion *float64 `json:"output_tokens_per_completion,omitempty"`
	TotalTokensPerCompletion  *float64 `json:"total_tokens_per_completion,omitempty"`
	CostUSDPerCompletion      *float64 `json:"cost_usd_per_completion,omitempty"`
	// IncludesFailedAttemptSpend is always true: the ledger retains failed
	// and killed attempts, so their spend is inside the numerator.
	IncludesFailedAttemptSpend bool `json:"includes_failed_attempt_spend"`
}

// ProviderCacheReport carries provider-cache effects SEPARATELY from raw
// tokens. A stable shared prefix may improve billed cost; it never reduces
// the raw token totals in the source entries.
type ProviderCacheReport struct {
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`
	CacheWriteTokens  int `json:"cache_write_tokens,omitempty"`
	// ReportedSeparately pins the reporting contract in serialized form.
	ReportedSeparately bool `json:"reported_separately"`
}

// WorkflowCostReport is the complete-task cost accounting for one arm of one
// workflow (the number the Section 11 campaign reports).
type WorkflowCostReport struct {
	// Sources holds one entry per spend source with at least one request.
	// Sources without requests stay absent: an absent source is unknown
	// nothing-happened, and a zero entry would fabricate a metric.
	Sources map[string]*WorkflowSourceSpend `json:"sources,omitempty"`
	// Totals is the accounting identity target: sum over Sources equals
	// Totals for every counter (pinned by test).
	Totals WorkflowSourceSpend `json:"totals"`
	// Maintenance is the isolated warm-only capture spend. Nil when the cold
	// baseline performs the same capture work (or there is no capture
	// spend): absent, never zero.
	Maintenance *MaintenanceCost `json:"maintenance,omitempty"`
	// PerVerifiedCompletion reports spend per verified completion over ALL
	// launched attempts. Nil when no verified completion was recorded.
	PerVerifiedCompletion *PerVerifiedCompletionCost `json:"per_verified_completion,omitempty"`
	// ProviderCache is reported separately from raw tokens.
	ProviderCache ProviderCacheReport `json:"provider_cache"`
	// CostCoverage mirrors the A3 coverage semantics over the joined ledger.
	CostCoverage string `json:"cost_coverage,omitempty"`
}

// WorkflowCostOptions configures one report build.
type WorkflowCostOptions struct {
	// VerifiedCompletions is the count of verified completions the workflow
	// produced (the per-completion denominator). Zero leaves the ratio
	// absent.
	VerifiedCompletions int
	// ColdPerformsCapture is true when the cold baseline performs the same
	// capture work (the natural-capture comparison does). True means capture
	// spend is NOT incremental warm maintenance; false means capture spend
	// isolates as maintenance.
	ColdPerformsCapture bool
}

// costStatus constants mirror the schema cost statuses without importing the
// schema package into the report (keeps the offline slice dependency-free).
const (
	costStatusPriced   = "priced"
	costStatusUnpriced = "unpriced"
	costStatusError    = "error"
)

// BuildWorkflowCostReport joins ledger records into the typed report. It
// never mutates the input records.
func BuildWorkflowCostReport(views []spendRecordView, sourceOf func(int) string, opts WorkflowCostOptions) (*WorkflowCostReport, error) {
	report := &WorkflowCostReport{Sources: map[string]*WorkflowSourceSpend{}}
	var priced, unpriced, costErrors, total int
	for i, v := range views {
		source := schemas.SpendSourceGeneration
		if sourceOf != nil {
			source = sourceOf(i)
		}
		if source == "" {
			source = schemas.SpendSourceGeneration
		}
		entry := report.Sources[source]
		if entry == nil {
			entry = &WorkflowSourceSpend{}
			report.Sources[source] = entry
		}
		entry.add(v)
		report.Totals.add(v)
		total++
		switch v.costStatus {
		case costStatusPriced:
			priced++
		case costStatusUnpriced:
			unpriced++
		case costStatusError:
			costErrors++
		}
	}
	// Cache subsets are reported separately from raw tokens. Every record's
	// cache counters land here regardless of pricing status; the raw source
	// entries above are never reduced by them.
	report.ProviderCache.CachedInputTokens = 0
	report.ProviderCache.CacheWriteTokens = 0
	for _, v := range views {
		report.ProviderCache.CachedInputTokens += v.cached
		report.ProviderCache.CacheWriteTokens += v.cacheWrite
	}
	report.ProviderCache.ReportedSeparately = true

	// Maintenance isolation: only capture work the cold baseline does not
	// also perform is charged to warm.
	captureSpend := report.Sources[schemas.SpendSourceCapture]
	if captureSpend != nil && !opts.ColdPerformsCapture {
		report.Maintenance = &MaintenanceCost{
			AllocatedToWarm: true,
			Spend:           captureSpend,
			Basis:           "capture spend is not work the cold baseline performs; isolated as warm-only maintenance",
		}
	} else if captureSpend != nil {
		report.Maintenance = &MaintenanceCost{
			AllocatedToWarm: false,
			Basis:           "the cold baseline performs the same capture work; no incremental maintenance charged",
		}
	}

	// Per-verified-completion over all launched attempts (failed included).
	if opts.VerifiedCompletions > 0 {
		n := float64(opts.VerifiedCompletions)
		pvc := &PerVerifiedCompletionCost{
			VerifiedCompletions:        opts.VerifiedCompletions,
			IncludesFailedAttemptSpend: true,
		}
		in := float64(report.Totals.InputTokens) / n
		out := float64(report.Totals.OutputTokens) / n
		tot := float64(report.Totals.TotalTokens) / n
		pvc.InputTokensPerCompletion = &in
		pvc.OutputTokensPerCompletion = &out
		pvc.TotalTokensPerCompletion = &tot
		if report.Totals.CostUSD != nil {
			cpc := *report.Totals.CostUSD / n
			pvc.CostUSDPerCompletion = &cpc
		}
		report.PerVerifiedCompletion = pvc
	}

	// Coverage mirrors A3 semantics.
	switch {
	case total == 0:
		report.CostCoverage = "not_applicable"
	case priced == total:
		report.CostCoverage = "complete"
	case priced > 0:
		report.CostCoverage = "partial"
	default:
		report.CostCoverage = "unavailable"
	}
	_ = unpriced
	_ = costErrors

	// Mark sources with incomplete pricing so an unpriced source is visible
	// without fabricating its cost.
	for _, entry := range report.Sources {
		if entry.PricedRequests < entry.Requests {
			entry.PricingCoverageNote = "pricing incomplete for this source; missing usage stays unknown"
		}
	}
	if err := report.Validate(); err != nil {
		return nil, err
	}
	return report, nil
}

// Validate pins the accounting identity and the no-negative-entry invariant.
func (r *WorkflowCostReport) Validate() error {
	check := func(what string, sum, total int) error {
		if sum != total {
			return fmt.Errorf("workflow cost identity broken for %s: sources sum %d != total %d", what, sum, total)
		}
		if sum < 0 {
			return fmt.Errorf("workflow cost report has negative %s", what)
		}
		return nil
	}
	var req, in, out, tot, cached, cacheWrite, reasoning int
	var cost float64
	var costPresent bool
	for name, entry := range r.Sources {
		if entry == nil {
			return fmt.Errorf("workflow cost report has nil source %q", name)
		}
		req += entry.Requests
		in += entry.InputTokens
		out += entry.OutputTokens
		tot += entry.TotalTokens
		cached += entry.CachedInputTokens
		cacheWrite += entry.CacheWriteTokens
		reasoning += entry.ReasoningTokens
		if entry.InputTokens < 0 || entry.OutputTokens < 0 || entry.TotalTokens < 0 || entry.Requests < 0 {
			return fmt.Errorf("workflow cost source %q has a negative counter", name)
		}
		if entry.TotalTokens != entry.InputTokens+entry.OutputTokens {
			return fmt.Errorf("workflow cost source %q total_tokens != input + output", name)
		}
		if entry.CostUSD != nil {
			cost += *entry.CostUSD
			costPresent = true
		}
	}
	if err := check("requests", req, r.Totals.Requests); err != nil {
		return err
	}
	if err := check("input tokens", in, r.Totals.InputTokens); err != nil {
		return err
	}
	if err := check("output tokens", out, r.Totals.OutputTokens); err != nil {
		return err
	}
	if err := check("total tokens", tot, r.Totals.TotalTokens); err != nil {
		return err
	}
	if err := check("cached input tokens", cached, r.Totals.CachedInputTokens); err != nil {
		return err
	}
	if err := check("cache write tokens", cacheWrite, r.Totals.CacheWriteTokens); err != nil {
		return err
	}
	if err := check("reasoning tokens", reasoning, r.Totals.ReasoningTokens); err != nil {
		return err
	}
	if r.Totals.TotalTokens != r.Totals.InputTokens+r.Totals.OutputTokens {
		return fmt.Errorf("workflow cost totals total_tokens != input + output")
	}
	if costPresent {
		if r.Totals.CostUSD == nil || *r.Totals.CostUSD != cost {
			return fmt.Errorf("workflow cost identity broken for cost: sources sum %v != total %v", cost, r.Totals.CostUSD)
		}
	}
	// Maintenance, when allocated, must point at the capture source and stay
	// consistent with it.
	if r.Maintenance != nil && r.Maintenance.AllocatedToWarm {
		if r.Maintenance.Spend == nil {
			return fmt.Errorf("maintenance allocated to warm without a spend entry")
		}
		capture := r.Sources[schemas.SpendSourceCapture]
		if capture == nil || r.Maintenance.Spend != capture {
			return fmt.Errorf("maintenance spend must alias the capture source entry")
		}
	}
	return nil
}
