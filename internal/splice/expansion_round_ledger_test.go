package splice

// This test pins the F1 spend labeling on the provider-produced expansion
// path. A provider-generated request_context action must create an expansion
// round that the ledger records as spend source expansion with
// context_round >= 1. The first provider-bearing round must stay generation
// with context_round 0.
//
// The test is provider-free. The scripted gate provider plays both turns, and
// a wrapper appends a usage event so the decomposition check is not vacuous.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// usageReportingProvider delegates to the scripted gate provider and inserts a
// usage event before the terminal event, so the ledger records non-zero
// tokens for each provider request.
type usageReportingProvider struct {
	inner   *dGateProvider
	input   int
	output  int
	cached  int
	written int
}

func (p *usageReportingProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	inner, err := p.inner.StreamCompletion(ctx, request)
	if err != nil {
		return nil, err
	}
	out := make(chan zeroruntime.StreamEvent, 16)
	go func() {
		defer close(out)
		for event := range inner {
			if event.Type == zeroruntime.StreamEventDone {
				out <- zeroruntime.StreamEvent{
					Type: zeroruntime.StreamEventUsage,
					Usage: zeroruntime.Usage{
						InputTokens:       p.input,
						OutputTokens:      p.output,
						CachedInputTokens: p.cached,
						CacheWriteTokens:  p.written,
						ReasoningTokens:   2,
					},
				}
			}
			out <- event
		}
	}()
	return out, nil
}

func TestProviderExpansionRoundLedgerLabelsGenerationThenExpansion(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	main := "package main\n\nfunc Hello() string { return \"wrong\" }\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := "package main\n\nfunc compute() int { return 42 }\n"
	if err := os.WriteFile(filepath.Join(workDir, "helper.go"), []byte(helper), 0o644); err != nil {
		t.Fatal(err)
	}
	// The gate provider returns a provider-produced request_context action on
	// turn 1 and submits the edit on turn 2.
	provider := &usageReportingProvider{
		inner:   &dGateProvider{helperBase: helper, workDir: workDir},
		input:   100,
		output:  40,
		cached:  25,
		written: 3,
	}
	dGateWorkDir = workDir
	defer func() { dGateWorkDir = "" }()

	plan, err := BuildExecutionPlan("wire the helper", nil)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	cfg := PipelineConfigFromAgentOptions(agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		FileTracker:    tools.NewFileTracker(),
	})
	result, err := runExecutionPlan(context.Background(), "run-expansion-ledger", plan, provider, cfg, nil, nil)
	if err != nil {
		t.Fatalf("runExecutionPlan: %v", err)
	}

	// (a) The stage ran a second round after the context request.
	if provider.inner.turn < 2 {
		t.Fatalf("provider turns = %d, want >= 2: the expansion loop did not re-invoke", provider.inner.turn)
	}

	records := result.UsageRecords
	if len(records) < 2 {
		t.Fatalf("usage records = %d, want >= 2 (generation and expansion)", len(records))
	}
	for i, r := range records {
		t.Logf("record[%d] stage=%s iter=%d ordinal=%d round=%d source=%s in=%d out=%d cached=%d cost=%s",
			i, r.Stage, r.Iteration, r.InvocationOrdinal, r.ContextRound, r.SpendSource,
			r.InputTokens, r.OutputTokens, r.CachedTokens, r.CostStatus)
	}

	// (c) The first provider-bearing request is the generation round.
	first := records[0]
	if first.Stage != "code_writer" {
		t.Fatalf("first record stage = %q, want code_writer", first.Stage)
	}
	if first.SpendSource != schemas.SpendSourceGeneration {
		t.Fatalf("first record spend source = %q, want %q", first.SpendSource, schemas.SpendSourceGeneration)
	}
	if first.ContextRound != 0 {
		t.Fatalf("first record context round = %d, want 0", first.ContextRound)
	}
	if first.InputTokens <= 0 {
		t.Fatalf("first record input tokens = %d, want > 0: the usage event did not reach the ledger", first.InputTokens)
	}

	// (b) A later provider-bearing request is the expansion round.
	var expansionRecords []schemas.PipelineUsageRecord
	firstExpansion := -1
	for i, r := range records {
		if r.SpendSource != schemas.SpendSourceExpansion {
			continue
		}
		if r.ContextRound < 1 {
			t.Fatalf("expansion record context round = %d, want >= 1", r.ContextRound)
		}
		if firstExpansion < 0 {
			firstExpansion = i
		}
		expansionRecords = append(expansionRecords, r)
	}
	if firstExpansion < 0 {
		t.Fatalf("no record carries spend source %q; records = %+v", schemas.SpendSourceExpansion, records)
	}
	if firstExpansion == 0 {
		t.Fatalf("the first record is already expansion; the generation round must precede it")
	}
	expansionInput := 0
	for _, r := range expansionRecords {
		expansionInput += r.InputTokens
	}
	if expansionInput <= 0 {
		t.Fatalf("expansion records carry %d input tokens, want > 0", expansionInput)
	}

	// (d) The per-source decomposition reconstructs the run totals. The
	// builder calls Validate, which fails loud when the per-source counters
	// do not sum to the totals.
	views := make([]spendRecordView, 0, len(records))
	for _, r := range records {
		views = append(views, spendViewForRecord(r))
	}
	sourceOf := func(i int) string { return records[i].SpendSource }
	report, err := BuildWorkflowCostReport(views, sourceOf, WorkflowCostOptions{})
	if err != nil {
		t.Fatalf("BuildWorkflowCostReport: %v", err)
	}
	gen := report.Sources[schemas.SpendSourceGeneration]
	if gen == nil {
		t.Fatalf("report has no generation source; sources = %v", costSourceNames(report))
	}
	exp := report.Sources[schemas.SpendSourceExpansion]
	if exp == nil {
		t.Fatalf("report has no expansion source; sources = %v", costSourceNames(report))
	}
	if got := gen.Requests + exp.Requests; got != report.Totals.Requests {
		t.Fatalf("generation+expansion requests = %d, want report total %d", got, report.Totals.Requests)
	}
	if gen.InputTokens <= 0 || exp.InputTokens <= 0 {
		t.Fatalf("per-source input tokens = generation %d, expansion %d; want both > 0",
			gen.InputTokens, exp.InputTokens)
	}
	if report.Totals.InputTokens != result.TotalTokensInput {
		t.Fatalf("report input tokens = %d, want result total %d", report.Totals.InputTokens, result.TotalTokensInput)
	}
	if report.Totals.OutputTokens != result.TotalTokensOutput {
		t.Fatalf("report output tokens = %d, want result total %d", report.Totals.OutputTokens, result.TotalTokensOutput)
	}
	if report.Totals.InputTokens <= 0 || report.Totals.OutputTokens <= 0 {
		t.Fatalf("report totals = in %d, out %d; want both > 0", report.Totals.InputTokens, report.Totals.OutputTokens)
	}
	// A missing price is never read as zero. When coverage is complete the
	// per-source USD must reconstruct the total. When no request is priced
	// the report must carry no cost total, and it must name the coverage.
	switch report.CostCoverage {
	case schemas.CostCoverageComplete:
		sum := 0.0
		for _, entry := range report.Sources {
			if entry.CostUSD != nil {
				sum += *entry.CostUSD
			}
		}
		if report.Totals.CostUSD == nil || math.Abs(sum-*report.Totals.CostUSD) > 1e-9 {
			t.Fatalf("complete coverage but the per-source cost does not reconstruct the total")
		}
	case schemas.CostCoverageUnavailable:
		if report.Totals.CostUSD != nil {
			t.Fatalf("coverage is unavailable but a cost total %v was reported; a missing price is not zero", *report.Totals.CostUSD)
		}
	case schemas.CostCoveragePartial:
		if report.Totals.CostUSD == nil {
			t.Fatalf("coverage is partial but no cost total was reported")
		}
	default:
		t.Fatalf("cost coverage = %q; missing pricing must be reported, never assumed", report.CostCoverage)
	}
}

func costSourceNames(r *WorkflowCostReport) []string {
	names := make([]string, 0, len(r.Sources))
	for name := range r.Sources {
		names = append(names, name)
	}
	return names
}
