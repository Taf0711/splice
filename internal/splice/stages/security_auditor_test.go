package stages

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestSecurityAuditorGoOnlyWorkspace(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	gosecReport := map[string]any{
		"Issues": []map[string]any{
			{
				"severity":   "HIGH",
				"confidence": "HIGH",
				"rule_id":    "G204",
				"file":       "main.go",
				"line":       "10",
				"column":     "1",
				"details":    "Subprocess launched with a variable",
				"code":       "exec.Command(input)",
			},
		},
	}
	gosecJSON, _ := json.Marshal(gosecReport)

	mockTool := func(toolName string) func(context.Context, string, map[string]any) (ToolResult, error) {
		return func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			if name == toolName {
				return ToolResult{OK: true, Output: string(gosecJSON)}, nil
			}
			return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
		}
	}

	stage, _ := NewSecurityAuditor(DefaultSecurityChecks()...)
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:  workDir,
		Language: "go",
		RunTool:  mockTool("gosec"),
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if strings.Contains(result.Summary, "No security scanner available for non-Python files") {
		t.Fatalf("old Python-only short-circuit still present: %q", result.Summary)
	}
	report, ok := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("security_auditor_output missing or wrong type: %T", result.Data["security_auditor_output"])
	}
	if report.Status != schemas.VerificationFindings {
		t.Fatalf("expected findings, got %q: %q", report.Status, report.Summary)
	}
	if len(report.Findings) != 1 || report.Findings[0].Tool != "gosec" {
		t.Fatalf("expected one gosec finding, got %+v", report.Findings)
	}
}

func TestSecurityAuditorMixedWorkspace(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x.py"), []byte("eval(input())\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	banditReport := map[string]any{
		"results": []map[string]any{
			{
				"filename":       "x.py",
				"line_range":     []int{1},
				"issue_text":     "Use of eval",
				"issue_severity": "HIGH",
				"test_id":        "B307",
			},
		},
	}
	banditJSON, _ := json.Marshal(banditReport)

	gosecReport := map[string]any{
		"Issues": []map[string]any{
			{
				"severity":   "MEDIUM",
				"confidence": "HIGH",
				"rule_id":    "G101",
				"file":       "main.go",
				"line":       "5",
				"column":     "1",
				"details":    "Potential hardcoded credentials",
				"code":       "password := \"secret\"",
			},
		},
	}
	gosecJSON, _ := json.Marshal(gosecReport)

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		switch name {
		case "bandit":
			return ToolResult{OK: true, Output: string(banditJSON)}, nil
		case "gosec":
			return ToolResult{OK: true, Output: string(gosecJSON)}, nil
		}
		return ToolResult{OK: false, Output: name + " is not installed or not available"}, nil
	}

	stage, _ := NewSecurityAuditor(DefaultSecurityChecks()...)
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:  workDir,
		Language: "go",
		RunTool:  runTool,
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	report, ok := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("security_auditor_output missing or wrong type: %T", result.Data["security_auditor_output"])
	}
	if report.Status != schemas.VerificationFindings {
		t.Fatalf("expected findings, got %q: %q", report.Status, report.Summary)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(report.Findings), report.Findings)
	}

	hasBandit, hasGosec := false, false
	for _, f := range report.Findings {
		if f.Tool == "bandit" {
			hasBandit = true
		}
		if f.Tool == "gosec" {
			hasGosec = true
		}
	}
	if !hasBandit || !hasGosec {
		t.Fatalf("expected one bandit and one gosec finding, got bandit=%v gosec=%v", hasBandit, hasGosec)
	}
}

func TestSecurityAuditorNoSourceFilesNotApplicable(t *testing.T) {
	workDir := t.TempDir()

	// Use the per-file checks without trivy (trivy is workspace-level and
	// always applicable, so it would prevent a not_applicable report).
	stage, _ := NewSecurityAuditor(banditCheck{}, gosecCheck{}, sarifCheck{scanners: defaultSarifScanners()})
	result, err := stage.Run(context.Background(), newHarnessInput("audit security"), panickingProvider{}, StageOptions{
		WorkDir:  workDir,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	report, ok := result.Data["security_auditor_output"].(schemas.VerificationReport)
	if !ok {
		t.Fatalf("security_auditor_output missing or wrong type: %T", result.Data["security_auditor_output"])
	}
	if report.Status != schemas.VerificationNotApplicable {
		t.Fatalf("expected not_applicable, got %q: %q", report.Status, report.Summary)
	}
}
