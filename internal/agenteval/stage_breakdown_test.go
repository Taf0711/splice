package agenteval

import (
	"testing"
)

func TestParsePipelineStagesFromStdout(t *testing.T) {
	// A stream-json final event whose text is a PipelineResult JSON with
	// two stages. Mirrors the real splice exec -o stream-json output.
	const stdout = `{"type":"run_start","runId":"r1"}
{"type":"reasoning","delta":"[code_writer] stage started"}
{"type":"usage","promptTokens":100,"completionTokens":200}
{"type":"final","text":"{\n  \"run_id\": \"r1\",\n  \"status\": \"completed\",\n  \"stages\": [\n    {\n      \"name\": \"code_writer\",\n      \"status\": \"completed\",\n      \"model\": \"moonshotai/kimi-k3\",\n      \"tokens_input\": 1000,\n      \"tokens_output\": 500,\n      \"tokens_cached\": 200,\n      \"cost_usd\": 0.005,\n      \"latency_ms\": 3000\n    },\n    {\n      \"name\": \"test_generator\",\n      \"status\": \"completed\",\n      \"model\": \"moonshotai/kimi-k3\",\n      \"tokens_input\": 800,\n      \"tokens_output\": 300,\n      \"tokens_cached\": 0,\n      \"cost_usd\": 0.003,\n      \"latency_ms\": 2000\n    }\n  ]\n}"}
{"type":"run_end","status":"success"}`

	stages := parsePipelineStagesFromStdout(stdout)
	if len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(stages))
	}
	if stages[0].Name != "code_writer" {
		t.Fatalf("stage[0] name = %q, want code_writer", stages[0].Name)
	}
	if stages[0].TokensInput != 1000 || stages[0].TokensOutput != 500 {
		t.Fatalf("stage[0] tokens = in=%d out=%d, want in=1000 out=500", stages[0].TokensInput, stages[0].TokensOutput)
	}
	if stages[0].Model != "moonshotai/kimi-k3" {
		t.Fatalf("stage[0] model = %q, want moonshotai/kimi-k3", stages[0].Model)
	}
	if stages[0].CostUSD != 0.005 {
		t.Fatalf("stage[0] cost = %v, want 0.005", stages[0].CostUSD)
	}
	if stages[1].Name != "test_generator" {
		t.Fatalf("stage[1] name = %q, want test_generator", stages[1].Name)
	}
	if stages[1].TokensCached != 0 {
		t.Fatalf("stage[1] cached = %d, want 0", stages[1].TokensCached)
	}
}

func TestParsePipelineStagesFromStdoutNoFinalEvent(t *testing.T) {
	// Non-pipeline agent: no final event with PipelineResult JSON.
	const stdout = `{"type":"run_start"}
{"type":"text","delta":"hello"}
{"type":"usage","promptTokens":10,"completionTokens":20}
{"type":"final","text":"just plain answer text, not JSON"}`

	stages := parsePipelineStagesFromStdout(stdout)
	if stages != nil {
		t.Fatalf("expected nil stages for non-pipeline agent, got %d stages", len(stages))
	}
}

func TestParsePipelineStagesFromStdoutEmpty(t *testing.T) {
	stages := parsePipelineStagesFromStdout("")
	if stages != nil {
		t.Fatalf("expected nil for empty stdout, got %v", stages)
	}
}

func TestFormatStageBreakdown(t *testing.T) {
	stages := []StageBreakdown{
		{Name: "code_writer", TokensInput: 1000, TokensOutput: 500, CostUSD: 0.005},
		{Name: "test_generator", TokensInput: 800, TokensOutput: 300, CostUSD: 0.003},
	}
	got := formatStageBreakdown(stages)
	want := "code_writer:in=1000,out=500,cost=0.0050;test_generator:in=800,out=300,cost=0.0030"
	if got != want {
		t.Fatalf("formatStageBreakdown = %q, want %q", got, want)
	}
	if formatStageBreakdown(nil) != "" {
		t.Fatalf("expected empty string for nil stages")
	}
}

func TestPopulateAgentRunUsageIncludesStages(t *testing.T) {
	const stdout = `{"type":"usage","promptTokens":100,"completionTokens":200}
{"type":"final","text":"{\"stages\":[{\"name\":\"code_writer\",\"status\":\"completed\",\"model\":\"moonshotai/kimi-k3\",\"tokens_input\":100,\"tokens_output\":50,\"cost_usd\":0.001,\"latency_ms\":1000}]}"}`

	result := &AgentRunResult{Stdout: stdout}
	populateAgentRunUsage(result)
	if result.InputTokens != 100 {
		t.Fatalf("input tokens = %d, want 100", result.InputTokens)
	}
	if len(result.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(result.Stages))
	}
	if result.Stages[0].Name != "code_writer" {
		t.Fatalf("stage name = %q, want code_writer", result.Stages[0].Name)
	}
	if result.Stages[0].Model != "moonshotai/kimi-k3" {
		t.Fatalf("stage model = %q, want moonshotai/kimi-k3", result.Stages[0].Model)
	}
}
