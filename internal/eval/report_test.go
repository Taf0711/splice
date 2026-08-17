package eval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleReport() Report {
	return Report{
		Contract:  ReportContractVersion,
		Taskset:   "ts",
		Model:     "model-x",
		Provider:  "provider-x",
		Timestamp: time.Unix(0, 0).Format(time.RFC3339),
		Pairs:     10,
		Cold:      ArmStats{Successes: 8, Tokens: 1000, WeightedInterventions: 2},
		Warm:      ArmStats{Successes: 8, Tokens: 600, WeightedInterventions: 2},
		Tasks:     []TaskPair{{Name: "t", ColdSuccess: true, WarmSuccess: true, ColdTokens: 100, WarmTokens: 60}},
		Gates:     []GateResult{{Name: "evidence", Passed: true}, {Name: "success", Passed: true}, {Name: "cost", Passed: true}, {Name: "burden", Passed: true}},
		Verdict:   VerdictConclusive,
		Reason:    "warm wins: 40.0% fewer tokens at equal success",
		Constants: ReportConstants(),
	}
}

// TestReportGoldenKeys pins the machine JSON shape so a downstream consumer
// does not silently break on a renamed field.
func TestReportGoldenKeys(t *testing.T) {
	data, err := json.Marshal(sampleReport())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keys map[string]any
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, want := range []string{"contract", "taskset", "model", "provider", "timestamp", "pairs", "cold", "warm", "tasks", "gates", "verdict", "reason", "constants"} {
		if _, ok := keys[want]; !ok {
			t.Errorf("report JSON missing key %q: %s", want, data)
		}
	}
}

func TestReportMarkdownContainsSections(t *testing.T) {
	md := sampleReport().RenderMarkdown()
	for _, want := range []string{"## Verdict", "## Gates", "## Tasks", "## Arms", "## Constants", "warm wins"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}
