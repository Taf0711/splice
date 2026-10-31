package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestUsesPipelineHonorsFlagAndRunKind(t *testing.T) {
	set, err := flags.Resolve(flags.Sources{User: map[string]bool{
		string(flags.TUIPipelineEnabled): false,
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	tests := []struct {
		name       string
		runKind    tuiRunKind
		set        flags.Set
		execDirect bool
		want       bool
	}{
		{name: "default runs the pipeline", runKind: tuiRunPipeline, set: nil, want: true},
		{name: "flag off falls back to agent loop", runKind: tuiRunPipeline, set: set, want: false},
		{name: "spec draft always skips the pipeline", runKind: tuiRunSpecDraft, set: nil, want: false},
		{name: "spec draft skips even with flag on", runKind: tuiRunSpecDraft, set: flags.Set{flags.TUIPipelineEnabled: true}, want: false},
		{name: "design conversation always skips the pipeline", runKind: tuiRunDesignConversation, set: nil, want: false},
		{name: "exec runs the pipeline with flag off", runKind: tuiRunPipeline, set: set, execDirect: true, want: true},
		{name: "exec with default flag runs the pipeline", runKind: tuiRunPipeline, set: nil, execDirect: true, want: true},
		{name: "non-exec with flag on runs the pipeline", runKind: tuiRunPipeline, set: flags.Set{flags.TUIPipelineEnabled: true}, want: true},
		{name: "spec draft from exec still skips the pipeline", runKind: tuiRunSpecDraft, set: nil, execDirect: true, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := usesPipeline(tc.runKind, tc.set, tc.execDirect); got != tc.want {
				t.Fatalf("usesPipeline(%v, execDirect=%v) = %v, want %v", tc.runKind, tc.execDirect, got, tc.want)
			}
		})
	}
}

// A pipeline usage event that arrives with no cost is priced by the guard. An
// unknown model is recorded as unpriced with a reason. It must not produce an
// error row, because an unpriced request is not a usage failure.
func TestPipelineUnpricedUsageIsRecordedWithReasonNotError(t *testing.T) {
	estimate := func(_ string, model string, _ agent.Usage, _ bool) agent.UsageCostEstimate {
		return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "no price for " + model}
	}
	au := priceUnpricedAttributedUsage(agent.AttributedUsage{
		Model:         "custom-coder",
		Usage:         agent.Usage{InputTokens: 10, OutputTokens: 5},
		UsageReported: true,
	}, estimate)
	if au.Cost.Status != agent.CostStatusUnpriced || au.Cost.UnpricedReason == "" {
		t.Fatalf("guard produced cost %+v, want unpriced with a reason", au.Cost)
	}

	m := newModel(context.Background(), Options{ModelName: "custom-coder"})
	m, rows := m.recordUsageEvent("custom-coder", zeroruntime.Usage{InputTokens: 10, OutputTokens: 5}, &au.Cost)
	for _, row := range rows {
		if strings.Contains(row.text, "usage:") {
			t.Fatalf("unpriced usage appended an error row: %#v", rows)
		}
	}
	if got := m.usageStatusSegment(); !strings.Contains(got, "cost unavailable") {
		t.Fatalf("status = %q, want the request recorded as unpriced", got)
	}

	// Malformed usage is still an error, and it is never a priced zero.
	bad := priceUnpricedAttributedUsage(agent.AttributedUsage{Model: "custom-coder", UsageError: "bad usage"}, estimate)
	if bad.Cost.Status != agent.CostStatusError || bad.Cost.UnpricedReason != "bad usage" {
		t.Fatalf("malformed usage cost = %+v, want an error status with the reason", bad.Cost)
	}
}
