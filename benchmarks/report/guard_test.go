package report

import (
	"strings"
	"testing"
)

func mustRec(t *testing.T, benchmark, instance string, costUSDDelta float64) *Record {
	t.Helper()
	cost := 1.25 + costUSDDelta
	in := int64(100)
	out := int64(50)
	rec := &Record{
		SchemaVersion:    SchemaVersion,
		Benchmark:        benchmark,
		BenchmarkVersion: "rev-1",
		InstanceID:       instance,
		DatasetSplit:     "test",
		SpliceCommit:     "abc123",
		Provider:         "test-provider",
		Model:            "test-model",
		Reasoning:        "default",
		CognitionMode:    "off",
		Official:         map[string]any{"status": "failed"},
		SpliceTelemetry: &SpliceTelemetry{
			InputTokens:    &in,
			OutputTokens:   &out,
			CostUSD:        &cost,
			CostProvenance: CostReported,
		},
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("fixture record invalid: %v", err)
	}
	return rec
}

// sumCost is a value function over records used by the guard tests.
func sumCost(r *Record) (float64, bool) {
	if r == nil || r.SpliceTelemetry == nil || r.SpliceTelemetry.CostUSD == nil {
		return 0, false
	}
	return *r.SpliceTelemetry.CostUSD, true
}

func sumInputTokens(r *Record) (float64, bool) {
	if r == nil || r.SpliceTelemetry == nil || r.SpliceTelemetry.InputTokens == nil {
		return 0, false
	}
	return float64(*r.SpliceTelemetry.InputTokens), true
}

// TestGuardRejectsCrossBenchmarkAggregation is the CRITICAL GUARD test: any
// attempt to collapse numeric metrics across different benchmarks into one
// number must fail.
func TestGuardRejectsCrossBenchmarkAggregation(t *testing.T) {
	cases := []struct {
		name  string
		kind  AggKind
		label string
		value func(*Record) (float64, bool)
	}{
		{"official score across benchmarks", AggOfficialMetric, "overall resolved", sumCost},
		{"token average across benchmarks", AggTelemetry, "mean tokens", sumInputTokens},
		{"unlabeled spend across benchmarks", AggSpendTotal, "total", sumCost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records := []*Record{
				mustRec(t, "swebench_pro_os", "instance_a", 0),
				mustRec(t, "terminal_bench", "task_b", 0),
				mustRec(t, "agentbench_style", "task_c", 0),
			}
			_, err := Aggregate(tc.kind, tc.label, records, tc.value)
			if err == nil {
				t.Fatalf("guard did not fire: cross-benchmark %s aggregation was allowed", tc.kind)
			}
			var crossErr ErrCrossBenchmarkAggregation
			isCross := false
			switch e := err.(type) {
			case ErrCrossBenchmarkAggregation:
				crossErr, isCross = e, true
			case AggregationError:
				// spend-without-label path: still a refusal, keep it
				if tc.kind == AggSpendTotal && !strings.Contains(err.Error(), "experiment spend") {
					t.Fatalf("spend refusal should name the labeling requirement, got: %v", err)
				}
			}
			if isCross {
				if len(crossErr.Benchmarks) != 3 {
					t.Fatalf("guard error should name all 3 benchmarks, got %v", crossErr.Benchmarks)
				}
			}
		})
	}
}

// TestGuardAllowsWithinBenchmarkOfficialAggregation: official metrics may be
// aggregated inside ONE benchmark.
func TestGuardAllowsWithinBenchmarkOfficialAggregation(t *testing.T) {
	records := []*Record{
		mustRec(t, "swebench_pro_os", "instance_a", 0),
		mustRec(t, "swebench_pro_os", "instance_b", 0.5),
		mustRec(t, "swebench_pro_os", "instance_c", 1.0),
	}
	got, err := Aggregate(AggOfficialMetric, "resolved count", records, func(r *Record) (float64, bool) {
		return 1, true // every official object exists -> count
	})
	if err != nil {
		t.Fatalf("within-benchmark official aggregation refused: %v", err)
	}
	if got.Value != 3 || got.Count != 3 {
		t.Fatalf("want value=3 count=3, got value=%v count=%d", got.Value, got.Count)
	}
	if got.Benchmark != "swebench_pro_os" {
		t.Fatalf("scope should be swebench_pro_os, got %q", got.Benchmark)
	}
}

// TestGuardAllowsWithinBenchmarkTelemetry: supplemental telemetry may be
// aggregated inside ONE benchmark.
func TestGuardAllowsWithinBenchmarkTelemetry(t *testing.T) {
	records := []*Record{
		mustRec(t, "swebench_pro_os", "instance_a", 0),
		mustRec(t, "swebench_pro_os", "instance_b", 0),
	}
	got, err := Aggregate(AggTelemetry, "input tokens (swebench_pro_os only)", records, sumInputTokens)
	if err != nil {
		t.Fatalf("within-benchmark telemetry aggregation refused: %v", err)
	}
	if got.Value != 200 {
		t.Fatalf("want 200 input tokens, got %v", got.Value)
	}
}

// TestGuardAllowsLabeledExperimentSpendAcrossBenchmarks: experiment spend
// totals may span benchmarks ONLY when explicitly labeled as spend, and the
// result carries no benchmark scope.
func TestGuardAllowsLabeledExperimentSpendAcrossBenchmarks(t *testing.T) {
	records := []*Record{
		mustRec(t, "swebench_pro_os", "instance_a", 0),
		mustRec(t, "terminal_bench", "task_b", 0.75),
	}
	got, err := Aggregate(AggSpendTotal, "experiment spend total (budgeting only, not a score)", records, sumCost)
	if err != nil {
		t.Fatalf("labeled experiment spend refused: %v", err)
	}
	if got.Benchmark != "" {
		t.Fatalf("spend total must carry no benchmark scope, got %q", got.Benchmark)
	}
	if got.Value < 3.25 {
		t.Fatalf("spend total should be >= 3.25, got %v", got.Value)
	}
	if got.Kind != AggSpendTotal {
		t.Fatalf("kind should be experiment_spend_total, got %q", got.Kind)
	}
}

// TestGuardSpendLabelEnforced: the same cross-benchmark sum without the
// spend label must be refused even under AggSpendTotal.
func TestGuardSpendLabelEnforced(t *testing.T) {
	records := []*Record{
		mustRec(t, "swebench_pro_os", "instance_a", 0),
		mustRec(t, "terminal_bench", "task_b", 0),
	}
	if _, err := Aggregate(AggSpendTotal, "combined score", records, sumCost); err == nil {
		t.Fatal("unlabeled cross-benchmark total was allowed")
	}
}

// TestReportContainsNoCrossBenchmarkNumbers: the rendered Markdown must not
// contain an overall score or cross-benchmark numeric aggregation. The
// evidence matrix rows must be cells of checks/cross/na only.
func TestReportContainsNoCrossBenchmarkNumbers(t *testing.T) {
	records := []*Record{
		mustRec(t, "swebench_pro_os", "instance_a", 0),
		mustRec(t, "swebench_pro_os", "instance_b", 0),
		mustRec(t, "terminal_bench", "task_b", 0),
	}
	md, err := GenerateMarkdown(records, []string{"go run ./benchmarks/swebench_pro/cmd/swebench-runner"})
	if err != nil {
		t.Fatalf("GenerateMarkdown: %v", err)
	}
	for _, forbidden := range []string{
		"Overall", "OVERALL",
		"total tokens across", "average across benchmarks", "combined score",
	} {
		if strings.Contains(md, forbidden) {
			t.Fatalf("report contains forbidden cross-benchmark phrasing %q", forbidden)
		}
	}
	if !strings.Contains(md, "OFFICIAL BENCHMARK METRIC") {
		t.Fatal("report missing OFFICIAL BENCHMARK METRIC section")
	}
	if !strings.Contains(md, "SPLICE SUPPLEMENTAL TELEMETRY") {
		t.Fatal("report missing SPLICE SUPPLEMENTAL TELEMETRY section")
	}
	// evidence matrix cells
	for _, cell := range []string{"checks", "cross", "na"} {
		if !strings.Contains(md, cell) {
			t.Fatalf("evidence matrix missing %q cell vocabulary", cell)
		}
	}
	// The matrix section between the heading and the Guard section must
	// contain no digits (cells are words only).
	start := strings.Index(md, "## Cross-benchmark evidence matrix")
	end := strings.Index(md, "## Guard")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("evidence matrix section not found")
	}
	matrix := md[start:end]
	for _, r := range matrix {
		if r >= '0' && r <= '9' {
			t.Fatalf("evidence matrix contains a digit %q; cells must be qualitative only", r)
		}
	}
}
