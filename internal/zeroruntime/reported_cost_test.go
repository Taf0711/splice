package zeroruntime

import (
	"context"
	"testing"
)

// OpenRouter can send more than one usage chunk on a stream, and the last one
// may carry cost 0. A real charge already seen must survive that zero.
func TestCollectStreamKeepsReportedChargeOverLaterZero(t *testing.T) {
	charge, zero := 0.0013749, 0.0
	events := make(chan StreamEvent, 4)
	events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{InputTokens: 900, OutputTokens: 40}, ReportedCostUSD: &charge}
	events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{InputTokens: 900, OutputTokens: 40}, ReportedCostUSD: &zero}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if got.ReportedCostUSD == nil || *got.ReportedCostUSD != charge {
		t.Fatalf("ReportedCostUSD = %v, want %v after a later zero chunk", got.ReportedCostUSD, charge)
	}
}

// A zero with no earlier charge is still a reported zero, so a free model keeps
// its report.
func TestCollectStreamKeepsReportedZeroWhenNoCharge(t *testing.T) {
	zero := 0.0
	events := make(chan StreamEvent, 2)
	events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{InputTokens: 900, OutputTokens: 40}, ReportedCostUSD: &zero}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if got.ReportedCostUSD == nil || *got.ReportedCostUSD != 0 {
		t.Fatalf("ReportedCostUSD = %v, want reported 0", got.ReportedCostUSD)
	}
}
