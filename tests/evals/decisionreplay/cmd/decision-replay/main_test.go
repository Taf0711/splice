package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/tests/evals/decisionreplay"
)

func TestReplayCommandLoadsXDGProviderConfigAndForcesAdvisor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("SPLICE_PROVIDER_COMMAND", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	dir := filepath.Join(root, "splice")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	configJSON := `{"flags":{"pipeline.decision_advisor":false},"providers":[{"name":"replay-fixture","provider":"openai-compatible","base_url":"http://fixture.invalid/v1","model":"fixture-model","api_key":"fixture-key"}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0600); err != nil {
		t.Fatal(err)
	}
	backendPath := filepath.Join(root, "backend.json")
	if err := os.WriteFile(backendPath, []byte(`{"backend":"provider","provider_profile":"replay-fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	recordsPath := filepath.Join(root, "records.jsonl")
	if err := os.WriteFile(recordsPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(root, "out.jsonl")
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--records", recordsPath, "--backend-config", backendPath, "--out", outPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var summary decisionreplay.Summary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Boundaries != 0 || summary.Errors != 0 {
		t.Fatalf("summary: %+v", summary)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil || len(raw) != 0 {
		t.Fatalf("output=%s error=%v", raw, err)
	}
}

// TestReplayCommandCompareWritesSummaryAndTable checks that --evidence compare
// needs no --out, prints the JSON summary to stdout, and the drift table to
// stderr.
func TestReplayCommandCompareWritesSummaryAndTable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("SPLICE_PROVIDER_COMMAND", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	dir := filepath.Join(root, "splice")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	configJSON := `{"flags":{"pipeline.decision_advisor":false},"providers":[{"name":"replay-fixture","provider":"openai-compatible","base_url":"http://fixture.invalid/v1","model":"fixture-model","api_key":"fixture-key"}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0600); err != nil {
		t.Fatal(err)
	}
	backendPath := filepath.Join(root, "backend.json")
	if err := os.WriteFile(backendPath, []byte(`{"backend":"provider","provider_profile":"replay-fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	recordsPath := filepath.Join(root, "records.jsonl")
	if err := os.WriteFile(recordsPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--records", recordsPath, "--backend-config", backendPath, "--evidence", "compare"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var comparison decisionreplay.Comparison
	if err := json.Unmarshal(stdout.Bytes(), &comparison); err != nil {
		t.Fatalf("stdout is not the JSON summary: %v\n%s", err, stdout.String())
	}
	if comparison.Boundaries != 0 || !strings.Contains(stderr.String(), "boundaries") {
		t.Fatalf("comparison=%+v stderr=%q", comparison, stderr.String())
	}
	if comparison.Traces != nil || !strings.Contains(stderr.String(), "not read") {
		t.Fatalf("records-only compare must say the traces were not read: traces=%+v stderr=%q", comparison.Traces, stderr.String())
	}

	// With --traces, every pass in the traces counts, not only the passes at
	// a boundary. A degraded pass is named.
	traces := filepath.Join(root, "traces")
	if err := os.MkdirAll(traces, 0700); err != nil {
		t.Fatal(err)
	}
	trace := `{"schema_version":"1","run_id":"run-a","pass_evidence":[{"iteration":1,"builder":"head"},{"iteration":1,"builder":"pass","degraded_reason":"HEAD summary degraded: git status failed"},{"iteration":2,"builder":"pass"}]}`
	if err := os.WriteFile(filepath.Join(traces, "run-a.json"), []byte(trace), 0600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := run(context.Background(), []string{"--records", recordsPath, "--backend-config", backendPath, "--evidence", "compare", "--traces", traces}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	comparison = decisionreplay.Comparison{}
	if err := json.Unmarshal(stdout.Bytes(), &comparison); err != nil {
		t.Fatalf("stdout is not the JSON summary: %v\n%s", err, stdout.String())
	}
	if comparison.Traces == nil || comparison.Traces.Runs != 1 || comparison.Traces.Passes != 2 || len(comparison.Traces.Degraded) != 1 ||
		comparison.Traces.Degraded[0].RunID != "run-a" || comparison.Traces.Degraded[0].Iteration != 1 {
		t.Fatalf("traces = %+v, want one degraded pass of two in run-a", comparison.Traces)
	}
	if !strings.Contains(stderr.String(), "git status failed") {
		t.Fatalf("table does not name the degraded pass: %q", stderr.String())
	}
	if err := run(context.Background(), []string{"--records", recordsPath, "--backend-config", backendPath, "--out", filepath.Join(root, "out.jsonl"), "--traces", traces}, &stdout, &stderr); err == nil {
		t.Fatal("--traces without --evidence compare must be refused")
	}
}
