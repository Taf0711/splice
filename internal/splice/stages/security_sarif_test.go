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

func sarifFixture() map[string]any {
	return map[string]any{
		"runs": []map[string]any{
			{
				"tool": map[string]any{
					"driver": map[string]any{"name": "eslint"},
				},
				"results": []map[string]any{
					{
						"ruleId": "no-eval",
						"level":  "error",
						"message": map[string]any{
							"text": "Do not use eval().",
						},
						"locations": []map[string]any{
							{
								"physicalLocation": map[string]any{
									"artifactLocation": map[string]any{"uri": "src/index.js"},
									"region":           map[string]any{"startLine": 10, "startColumn": 5},
								},
							},
						},
					},
					{
						"ruleId": "no-console",
						"level":  "warning",
						"message": map[string]any{
							"text": "Unexpected console statement.",
						},
						"locations": []map[string]any{
							{
								"physicalLocation": map[string]any{
									"artifactLocation": map[string]any{"uri": "src/util.ts"},
									"region":           map[string]any{"startLine": 4},
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestSarifCheckFindingsAndSeverityMapping(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "index.js"), []byte("eval('x')\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	fixture, _ := json.Marshal(sarifFixture())
	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "sarif" {
			command, _ := args["command"].(string)
			if command != "npx" {
				return ToolResult{}, fmt.Errorf("expected command npx, got %q", command)
			}
			return ToolResult{OK: true, Output: string(fixture)}, nil
		}
		return ToolResult{}, fmt.Errorf("unexpected tool %s", name)
	}

	result, err := sarifCheck{scanners: defaultSarifScanners()}.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{filepath.Join(workDir, "index.js")},
		RunTool: runTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationFindings {
		t.Fatalf("expected findings, got %q", result.ToolRun.Status)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(result.Findings))
	}

	f0 := result.Findings[0]
	if f0.RuleID != "no-eval" || f0.Severity != schemas.SeverityHigh {
		t.Fatalf("finding[0] rule/severity = %q/%v, want no-eval/High", f0.RuleID, f0.Severity)
	}
	if f0.Message != "no-eval: Do not use eval()." {
		t.Fatalf("finding[0] message = %q, want prefixed message", f0.Message)
	}
	if f0.Path != "src/index.js" {
		t.Fatalf("finding[0] path = %q, want src/index.js", f0.Path)
	}
	if f0.Line == nil || *f0.Line != 10 {
		t.Fatalf("finding[0] line = %v, want 10", f0.Line)
	}

	f1 := result.Findings[1]
	if f1.RuleID != "no-console" || f1.Severity != schemas.SeverityMedium {
		t.Fatalf("finding[1] rule/severity = %q/%v, want no-console/Medium", f1.RuleID, f1.Severity)
	}
	if f1.Path != "src/util.ts" {
		t.Fatalf("finding[1] path = %q, want src/util.ts", f1.Path)
	}
	if f1.Line == nil || *f1.Line != 4 {
		t.Fatalf("finding[1] line = %v, want 4", f1.Line)
	}

	for i, f := range result.Findings {
		if f.Tool != "sarif" {
			t.Fatalf("finding[%d].Tool = %q, want sarif", i, f.Tool)
		}
		if f.Authority != "deterministic" {
			t.Fatalf("finding[%d].Authority = %q, want deterministic", i, f.Authority)
		}
		if f.Category != "security" {
			t.Fatalf("finding[%d].Category = %q, want security", i, f.Category)
		}
	}
}

func TestSarifCheckNestedMessageText(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "index.js"), []byte("eval('x')\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	fixture := map[string]any{
		"runs": []map[string]any{
			{
				"tool": map[string]any{
					"driver": map[string]any{"name": "eslint"},
				},
				"results": []map[string]any{
					{
						"ruleId": "no-eval",
						"level":  "note",
						"message": map[string]any{
							"text": "NESTED_MESSAGE_VALUE",
						},
						"locations": []map[string]any{
							{
								"physicalLocation": map[string]any{
									"artifactLocation": map[string]any{"uri": "src/index.js"},
									"region":           map[string]any{"startLine": 7},
								},
							},
						},
					},
				},
			},
		},
	}
	fixtureJSON, _ := json.Marshal(fixture)

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "sarif" {
			return ToolResult{OK: true, Output: string(fixtureJSON)}, nil
		}
		return ToolResult{}, fmt.Errorf("unexpected tool %s", name)
	}

	result, err := sarifCheck{scanners: defaultSarifScanners()}.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{filepath.Join(workDir, "index.js")},
		RunTool: runTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != schemas.SeverityLow {
		t.Fatalf("expected note->Low, got %v", result.Findings[0].Severity)
	}
	if result.Findings[0].Message != "no-eval: NESTED_MESSAGE_VALUE" {
		t.Fatalf("unexpected message: %q", result.Findings[0].Message)
	}
}

func TestSarifCheckMissingToolDegrades(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "index.js"), []byte("eval('x')\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "sarif" {
			return ToolResult{
				OK:     false,
				Output: "SARIF scanner is not installed or not available: exec: \"npx\": executable file not found in $PATH",
			}, nil
		}
		return ToolResult{}, fmt.Errorf("unexpected tool %s", name)
	}

	result, err := sarifCheck{scanners: defaultSarifScanners()}.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{filepath.Join(workDir, "index.js")},
		RunTool: runTool,
	})
	if err != nil {
		t.Fatalf("expected degradation, got error: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationIncomplete {
		t.Fatalf("expected incomplete, got %q", result.ToolRun.Status)
	}
	if result.ToolRun.Summary != "SARIF scanner(s) not installed; security scan incomplete." {
		t.Fatalf("unexpected summary: %q", result.ToolRun.Summary)
	}
}

func TestSarifCheckNoApplicableFiles(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.py"), []byte("eval(input())\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "sarif" {
			return ToolResult{OK: true, Output: "{\"runs\":[]}"}, nil
		}
		return ToolResult{}, fmt.Errorf("unexpected tool %s", name)
	}

	result, err := sarifCheck{scanners: defaultSarifScanners()}.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{filepath.Join(workDir, "main.py")},
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

func TestSarifCheckEmptyLocationsStillAFinding(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "index.js"), []byte("eval('x')\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	fixture := map[string]any{
		"runs": []map[string]any{
			{
				"tool": map[string]any{
					"driver": map[string]any{"name": "eslint"},
				},
				"results": []map[string]any{
					{
						"ruleId":    "no-empty-loc",
						"level":     "warning",
						"message":   map[string]any{"text": "No location available."},
						"locations": []map[string]any{},
					},
				},
			},
		},
	}
	fixtureJSON, _ := json.Marshal(fixture)

	runTool := func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		if name == "sarif" {
			return ToolResult{OK: true, Output: string(fixtureJSON)}, nil
		}
		return ToolResult{}, fmt.Errorf("unexpected tool %s", name)
	}

	result, err := sarifCheck{scanners: defaultSarifScanners()}.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{filepath.Join(workDir, "index.js")},
		RunTool: runTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationFindings {
		t.Fatalf("expected findings, got %q", result.ToolRun.Status)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	f := result.Findings[0]
	if f.Path != "" {
		t.Fatalf("expected empty path, got %q", f.Path)
	}
	if f.Line != nil {
		t.Fatalf("expected nil line, got %v", f.Line)
	}
	if f.Message != "no-empty-loc: No location available." {
		t.Fatalf("unexpected message: %q", f.Message)
	}
}
