package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteMarkdownStandardizedSections pins the report file contract:
// per-benchmark sections with the two required labels, a qualitative
// cross-benchmark matrix, and reproduction commands.
func TestWriteMarkdownStandardizedSections(t *testing.T) {
	records := []*Record{
		mustRec(t, "swebench_pro_os", "instance_a", 0),
		mustRec(t, "swebench_pro_os", "instance_b", 0),
	}
	path := filepath.Join(t.TempDir(), "STANDARDIZED_EVAL_REPORT.md")
	cmds := []string{
		"go run ./benchmarks/swebench_pro/cmd/swebench-runner -config cfg.json",
		"go test ./benchmarks/report/ ./benchmarks/swebench_pro/",
	}
	if err := WriteMarkdown(path, records, cmds); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	md := string(data)
	if !strings.Contains(md, "# STANDARDIZED EVAL REPORT") {
		t.Fatal("missing report title")
	}
	if strings.Count(md, "### OFFICIAL BENCHMARK METRIC") != 1 {
		t.Fatal("expected exactly one OFFICIAL BENCHMARK METRIC section per benchmark")
	}
	if strings.Count(md, "### SPLICE SUPPLEMENTAL TELEMETRY") != 1 {
		t.Fatal("expected exactly one SPLICE SUPPLEMENTAL TELEMETRY section per benchmark")
	}
	if !strings.Contains(md, "## Cross-benchmark evidence matrix") {
		t.Fatal("missing evidence matrix")
	}
	for _, cmd := range cmds {
		if !strings.Contains(md, cmd) {
			t.Fatalf("reproduction command missing: %q", cmd)
		}
	}
	if !strings.Contains(md, "instance_a") || !strings.Contains(md, "instance_b") {
		t.Fatal("per-instance rows missing")
	}
}

func TestGenerateMarkdownFailsEmpty(t *testing.T) {
	if _, err := GenerateMarkdown(nil, nil); err == nil {
		t.Fatal("empty record set must fail loudly")
	}
}
