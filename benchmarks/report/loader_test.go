package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTrace writes a minimal but realistic Splice stream-json trace.
func writeTrace(t *testing.T, path string, usageRows int) {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"schemaVersion":2,"type":"run_start","runId":"run_1","timestampMs":1000}` + "\n")
	for i := 0; i < usageRows; i++ {
		b.WriteString(`{"schemaVersion":2,"type":"usage","runId":"run_1","provider":"openrouter","model":"test-model","stage":"code_writer","iteration":1,"usageSequence":` + itoa(i+1) + `,"usageReported":true,"promptTokens":100,"completionTokens":40,"totalTokens":140,"timestampMs":` + itoa(2000+i*100) + `}` + "\n")
	}
	b.WriteString(`{"schemaVersion":2,"type":"tool_call","runId":"run_1","stage":"read_file","timestampMs":2500}` + "\n")
	b.WriteString(`{"schemaVersion":2,"type":"tool_call","runId":"run_1","stage":"search","timestampMs":2600}` + "\n")
	b.WriteString(`{"schemaVersion":2,"type":"final","runId":"run_1","timestampMs":5000}` + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}
}

func TestLoadTraceSumsWithinOneRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instance_x.jsonl")
	writeTrace(t, path, 2)
	tel, _, err := LoadTrace(path)
	if err != nil {
		t.Fatalf("LoadTrace: %v", err)
	}
	if tel.InputTokens == nil || *tel.InputTokens != 200 {
		t.Fatalf("input tokens: want 200, got %v", tel.InputTokens)
	}
	if tel.OutputTokens == nil || *tel.OutputTokens != 80 {
		t.Fatalf("output tokens: want 80, got %v", tel.OutputTokens)
	}
	if tel.ModelCalls == nil || *tel.ModelCalls != 2 {
		t.Fatalf("model calls: want 2, got %v", tel.ModelCalls)
	}
	if tel.ToolCalls == nil || *tel.ToolCalls != 2 {
		t.Fatalf("tool calls: want 2, got %v", tel.ToolCalls)
	}
	if tel.CostProvenance != CostUnpriced {
		t.Fatalf("trace-only cost provenance should be unpriced, got %q", tel.CostProvenance)
	}
	if tel.CostUSD != nil {
		t.Fatal("trace-only cost must be null (unpriced)")
	}
	if tel.LatencyMS == nil || *tel.LatencyMS <= 0 {
		t.Fatalf("latency should be positive, got %v", tel.LatencyMS)
	}
}

func TestLoaderJoinsOfficialAndTrace(t *testing.T) {
	dir := t.TempDir()
	officialDir := filepath.Join(dir, "official")
	tracesDir := filepath.Join(dir, "traces")
	if err := os.MkdirAll(officialDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tracesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	official := `{"instance_id":"instance_x","resolved":false,"summary":"official evaluator output"}`
	if err := os.WriteFile(filepath.Join(officialDir, "instance_x.json"), []byte(official), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTrace(t, filepath.Join(tracesDir, "instance_x.jsonl"), 1)

	meta := RunMeta{
		Benchmark:        "swebench_pro_os",
		BenchmarkVersion: "7ab5114912baf22bb098818e604c02fe7ad2c11f",
		DatasetSplit:     "test",
		SpliceCommit:     "deadbeef",
		Provider:         "openrouter",
		Model:            "test-model",
		Reasoning:        "default",
		CognitionMode:    "off",
	}
	records, err := LoadRecords(officialDir, tracesDir, meta)
	if err != nil {
		t.Fatalf("LoadRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	r := records[0]
	if err := r.Validate(); err != nil {
		t.Fatalf("record invalid: %v", err)
	}
	if r.Official["resolved"] != false {
		t.Fatalf("official object should pass through verbatim, got %v", r.Official)
	}
	if r.SpliceTelemetry == nil || r.SpliceTelemetry.InputTokens == nil || *r.SpliceTelemetry.InputTokens != 100 {
		t.Fatalf("telemetry join failed: %+v", r.SpliceTelemetry)
	}
	if r.Artifacts.OfficialResult == "" || r.Artifacts.SpliceTrace == "" {
		t.Fatalf("artifacts must name both sources: %+v", r.Artifacts)
	}
}

func TestValidateRejectsUnpricedCost(t *testing.T) {
	rec := mustRec(t, "swebench_pro_os", "i", 0)
	rec.SpliceTelemetry.CostProvenance = CostUnpriced
	if err := rec.Validate(); err == nil {
		t.Fatal("unpriced with a cost set must be rejected")
	}
	rec.SpliceTelemetry.CostUSD = nil
	if err := rec.Validate(); err != nil {
		t.Fatalf("unpriced with null cost should validate: %v", err)
	}
}
