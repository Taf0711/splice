package stages

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBanditCheckParsesCompactReport pins that the check reads a compacted
// report with 50 findings and reports the omitted count in its summary.
func TestBanditCheckParsesCompactReport(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x.py"), []byte("eval(input())\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	results := make([]map[string]any, 0, 50)
	for i := 0; i < 50; i++ {
		results = append(results, map[string]any{
			"filename":       "x.py",
			"line_range":     []int{i + 1},
			"issue_text":     "Use of eval",
			"issue_severity": "HIGH",
			"test_id":        "B307",
		})
	}
	payload, _ := json.Marshal(map[string]any{"results": results, "omitted_results": 3})
	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: string(payload)}, nil
	}
	res, err := banditCheck{}.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{"x.py"},
		RunTool: runTool,
	})
	if err != nil {
		t.Fatalf("check run: %v", err)
	}
	if len(res.Findings) != 50 {
		t.Fatalf("expected 50 findings, got %d", len(res.Findings))
	}
	if !strings.Contains(res.ToolRun.Summary, "3 lower-severity Bandit findings omitted.") {
		t.Fatalf("summary missing omitted count: %q", res.ToolRun.Summary)
	}
}
