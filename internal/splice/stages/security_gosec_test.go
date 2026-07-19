package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestGosecCheckMissingToolDegrades(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "gosec" {
			return ToolResult{
				OK:     false,
				Output: "Gosec is not installed or not available: exec: \"gosec\": executable file not found in $PATH",
			}, nil
		}
		return ToolResult{}, fmt.Errorf("unexpected tool %s", name)
	}

	result, err := gosecCheck{}.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{filepath.Join(workDir, "main.go")},
		RunTool: runTool,
	})
	if err != nil {
		t.Fatalf("expected degradation, got error: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationIncomplete {
		t.Fatalf("expected incomplete, got %q", result.ToolRun.Status)
	}
	if result.ToolRun.Summary != "Gosec is not installed; Go security scan incomplete." {
		t.Fatalf("unexpected summary: %q", result.ToolRun.Summary)
	}
}

func TestGosecCheckFindingsAndLineParsing(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	report := map[string]any{
		"Issues": []map[string]any{
			{
				"severity":   "LOW",
				"confidence": "HIGH",
				"rule_id":    "G101",
				"file":       "main.go",
				"line":       "12",
				"column":     "5",
				"cwe":        map[string]any{"id": "798", "url": "https://cwe.mitre.org/data/definitions/798.html"},
				"details":    "Potential hardcoded credentials",
				"code":       "password := \"secret\"",
			},
			{
				"severity":   "MEDIUM",
				"confidence": "MEDIUM",
				"rule_id":    "G201",
				"file":       "main.go",
				"line":       "20-25",
				"column":     "2",
				"cwe":        map[string]any{"id": "89", "url": "https://cwe.mitre.org/data/definitions/89.html"},
				"details":    "SQL string formatting",
				"code":       "fmt.Sprintf(\"SELECT * WHERE id = %s\", id)",
			},
			{
				"severity":   "HIGH",
				"confidence": "HIGH",
				"rule_id":    "G204",
				"file":       "main.go",
				"line":       "",
				"column":     "0",
				"cwe":        map[string]any{"id": "78", "url": "https://cwe.mitre.org/data/definitions/78.html"},
				"details":    "Subprocess launched with variable",
				"code":       "exec.Command(cmd)",
			},
		},
	}
	mockReport, _ := json.Marshal(report)

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "gosec" {
			return ToolResult{OK: true, Output: string(mockReport)}, nil
		}
		return ToolResult{}, fmt.Errorf("unexpected tool %s", name)
	}

	result, err := gosecCheck{}.Run(context.Background(), VerificationCheckRequest{
		WorkDir:  workDir,
		Language: "go",
		Paths:    []string{filepath.Join(workDir, "main.go")},
		RunTool:  runTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationFindings {
		t.Fatalf("expected findings, got %q", result.ToolRun.Status)
	}
	if len(result.Findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(result.Findings))
	}

	f0 := result.Findings[0]
	if f0.RuleID != "G101" || f0.Severity != schemas.SeverityLow {
		t.Fatalf("finding[0] rule/severity = %q/%v, want G101/Low", f0.RuleID, f0.Severity)
	}
	if f0.Line == nil || *f0.Line != 12 {
		t.Fatalf("finding[0] line = %v, want 12", f0.Line)
	}
	if f0.Message != "G101: Potential hardcoded credentials" {
		t.Fatalf("finding[0] message = %q", f0.Message)
	}

	f1 := result.Findings[1]
	if f1.RuleID != "G201" || f1.Severity != schemas.SeverityMedium {
		t.Fatalf("finding[1] rule/severity = %q/%v, want G201/Medium", f1.RuleID, f1.Severity)
	}
	if f1.Line == nil || *f1.Line != 20 {
		t.Fatalf("finding[1] line = %v, want 20", f1.Line)
	}

	f2 := result.Findings[2]
	if f2.RuleID != "G204" || f2.Severity != schemas.SeverityHigh {
		t.Fatalf("finding[2] rule/severity = %q/%v, want G204/High", f2.RuleID, f2.Severity)
	}
	if f2.Line != nil {
		t.Fatalf("finding[2] line = %v, want nil", *f2.Line)
	}

	for i, f := range result.Findings {
		if f.Tool != "gosec" {
			t.Fatalf("finding[%d].Tool = %q, want gosec", i, f.Tool)
		}
		if f.Authority != "deterministic" {
			t.Fatalf("finding[%d].Authority = %q, want deterministic", i, f.Authority)
		}
		if f.Category != "security" {
			t.Fatalf("finding[%d].Category = %q, want security", i, f.Category)
		}
	}
}

func TestGosecCheckNonGoPathsNotApplicable(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x.py"), []byte("eval(input())\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: `{"Issues":[]}`}, nil
	}

	result, err := gosecCheck{}.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{filepath.Join(workDir, "x.py")},
		RunTool: runTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationNotApplicable {
		t.Fatalf("expected not_applicable, got %q", result.ToolRun.Status)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("expected no findings, got %d", len(result.Findings))
	}
}
