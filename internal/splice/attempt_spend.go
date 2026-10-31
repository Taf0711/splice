package splice

// Exported attempt-spend seam for the Section-11 campaign runner
// (internal/cli). The campaign reports the F2 WorkflowCostReport per
// condition from the attempts rows' measured token counters. This seam is
// the exported wrapper over BuildWorkflowCostReport with the fixed
// generation source mapping: per-attempt rows carry raw measured usage,
// so every record is priced by the caller's pricing table upstream and the
// report keeps the sum==total identity.

// SpendRecordView is the exported normalized view of one attempt's measured
// spend (raw input/output tokens). Cached and reasoning subsets stay
// reported separately by the report, never folded into these raw totals.
type SpendRecordView struct {
	InputTokens  int
	OutputTokens int
}

// BuildAttemptSpendReport builds the F2 WorkflowCostReport from per-attempt
// measured token views. Every attempt counts as one generation request
// (failed attempts included: the ledger keeps them, so per-verified-
// completion accounting includes their spend). A report with zero attempts
// returns (nil, nil): an absent report is never a fabricated zero.
func BuildAttemptSpendReport(views []SpendRecordView, verifiedCompletions int) (*WorkflowCostReport, error) {
	if len(views) == 0 {
		return nil, nil
	}
	records := make([]spendRecordView, 0, len(views))
	for _, v := range views {
		records = append(records, spendRecordView{
			input:      v.InputTokens,
			output:     v.OutputTokens,
			costStatus: costStatusPriced,
		})
	}
	return BuildWorkflowCostReport(records, nil, WorkflowCostOptions{
		VerifiedCompletions: verifiedCompletions,
	})
}
