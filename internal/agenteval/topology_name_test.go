package agenteval

import (
	"bytes"
	"strings"
	"testing"
)

// TestPipelineFinalCarriesTopologyName pins the round trip: the PipelineResult
// JSON in the stream-json final event keeps topology_name, and the parser
// surfaces it alongside the stages.
func TestPipelineFinalCarriesTopologyName(t *testing.T) {
	line := `{"type":"final","text":"{\"topology_name\":\"team\",\"stages\":[{\"name\":\"code_writer\",\"status\":\"completed\"}]}"}`
	stages, topologyName := parsePipelineFinalFromJSONLLine([]byte(line))
	if topologyName != "team" {
		t.Fatalf("topology name = %q, want team", topologyName)
	}
	if len(stages) != 1 || stages[0].Name != "code_writer" {
		t.Fatalf("stages = %#v, want one code_writer", stages)
	}
	fromStdout, name := parsePipelineFinalFromStdout("noise\n" + line + "\n")
	if name != "team" || len(fromStdout) != 1 {
		t.Fatalf("stdout parse = (%#v, %q), want one stage and team", fromStdout, name)
	}
}

// TestUsageCollectorCapturesTopologyName pins that the collector carries the
// topology name for the fallback path.
func TestUsageCollectorCapturesTopologyName(t *testing.T) {
	collector := newUsageCollector(1024)
	line := `{"type":"final","text":"{\"topology_name\":\"team\",\"stages\":[{\"name\":\"code_writer\",\"status\":\"completed\"}]}"}`
	if _, err := collector.Write([]byte(line)); err != nil {
		t.Fatalf("Write = %v", err)
	}
	if err := collector.Flush(); err != nil {
		t.Fatalf("Flush = %v", err)
	}
	if collector.finalTopologyName != "team" {
		t.Fatalf("collector topology name = %q, want team", collector.finalTopologyName)
	}
}

// TestBenchmarkCSVCarriesTopologyName pins that the CSV writer emits the column
// and the value.
func TestBenchmarkCSVCarriesTopologyName(t *testing.T) {
	report := BenchmarkReport{
		Tasks: []BenchmarkTaskReport{{TaskID: "t1", RunnerKind: "pipeline", TopologyName: "team"}},
	}
	var buf bytes.Buffer
	if err := WriteBenchmarkCSV(&buf, report); err != nil {
		t.Fatalf("WriteBenchmarkCSV: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv lines = %d, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "topologyName") {
		t.Fatalf("header missing topologyName: %s", lines[0])
	}
	if !strings.Contains(lines[1], "team") {
		t.Fatalf("row missing the topology name: %s", lines[1])
	}
}
