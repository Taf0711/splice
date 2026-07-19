package stages

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// A minimal SARIF fixture with one secret finding (error) and one vuln
// finding (warning), proving trivyCheck reuses the shared SARIF parser.
const trivySarifFixture = `{
  "version": "2.1.0",
  "runs": [{
    "tool": {"driver": {"name": "trivy"}},
    "results": [
      {
        "ruleId": "AVD-GS-0001",
        "level": "error",
        "message": {"text": "Hardcoded secret detected in source"},
        "locations": [{
          "physicalLocation": {
            "artifactLocation": {"uri": "config/secrets.go"},
            "region": {"startLine": 42}
          }
        }]
      },
      {
        "ruleId": "CVE-2024-1234",
        "level": "warning",
        "message": {"text": "golang.org/x/crypto vulnerability"},
        "locations": [{
          "physicalLocation": {
            "artifactLocation": {"uri": "go.mod"},
            "region": {"startLine": 15}
          }
        }]
      }
    ]
  }]
}`

func TestTrivyCheckFindingsAndSeverityMapping(t *testing.T) {
	workDir := t.TempDir()
	check := trivyCheck{}
	mockRunTool := func(_ context.Context, name string, _ map[string]any) (ToolResult, error) {
		if name != "sarif" {
			return ToolResult{}, nil
		}
		return ToolResult{OK: true, Output: trivySarifFixture}, nil
	}
	result, err := check.Run(context.Background(), VerificationCheckRequest{
		WorkDir:  workDir,
		Language: "go",
		Paths:    []string{workDir + "/config/secrets.go", workDir + "/go.mod"},
		RunTool:  mockRunTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationFindings {
		t.Fatalf("expected findings, got %v", result.ToolRun.Status)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(result.Findings))
	}
	// Findings are sorted by severity (High before Medium) by the aggregator,
	// but here we check the raw check output before aggregation.
	byRule := map[string]schemas.VerificationFinding{}
	for _, f := range result.Findings {
		byRule[f.RuleID] = f
	}
	secret := byRule["AVD-GS-0001"]
	if secret.Severity != schemas.SeverityHigh {
		t.Fatalf("secret severity = %v, want High (error->High)", secret.Severity)
	}
	if !strings.Contains(secret.Message, "AVD-GS-0001") {
		t.Fatalf("secret message missing ruleId: %q", secret.Message)
	}
	if !strings.Contains(secret.Path, "secrets.go") {
		t.Fatalf("secret path = %q, want secrets.go", secret.Path)
	}
	if secret.Line == nil || *secret.Line != 42 {
		t.Fatalf("secret line = %v, want 42", secret.Line)
	}
	vuln := byRule["CVE-2024-1234"]
	if vuln.Severity != schemas.SeverityMedium {
		t.Fatalf("vuln severity = %v, want Medium (warning->Medium)", vuln.Severity)
	}
	if !strings.Contains(vuln.Path, "go.mod") {
		t.Fatalf("vuln path = %q, want go.mod", vuln.Path)
	}
	// Authority is deterministic (trivy is authoritative, not advisory).
	for _, f := range result.Findings {
		if f.Authority != "deterministic" {
			t.Fatalf("finding %s authority = %q, want deterministic", f.RuleID, f.Authority)
		}
		if f.Tool != "trivy" {
			t.Fatalf("finding %s tool = %q, want trivy", f.RuleID, f.Tool)
		}
	}
}

func TestTrivyCheckMissingToolDegrades(t *testing.T) {
	workDir := t.TempDir()
	check := trivyCheck{}
	// Simulate trivy not installed: RunTool returns a not-installed error.
	mockRunTool := func(_ context.Context, name string, _ map[string]any) (ToolResult, error) {
		return ToolResult{OK: false, Output: "SARIF scanner is not installed or not available: exec: trivy: not found"}, nil
	}
	result, err := check.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{workDir + "/main.go"},
		RunTool: mockRunTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationIncomplete {
		t.Fatalf("expected incomplete, got %v", result.ToolRun.Status)
	}
	if !strings.Contains(result.ToolRun.Summary, "Trivy is not installed") {
		t.Fatalf("unexpected summary %q", result.ToolRun.Summary)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("expected no findings when trivy missing, got %d", len(result.Findings))
	}
}

func TestTrivyCheckNoFindings(t *testing.T) {
	workDir := t.TempDir()
	check := trivyCheck{}
	cleanSarif := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"trivy"}},"results":[]}]}`
	mockRunTool := func(_ context.Context, name string, _ map[string]any) (ToolResult, error) {
		return ToolResult{OK: true, Output: cleanSarif}, nil
	}
	result, err := check.Run(context.Background(), VerificationCheckRequest{
		WorkDir: workDir,
		Paths:   []string{workDir + "/main.go"},
		RunTool: mockRunTool,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ToolRun.Status != schemas.VerificationPassed {
		t.Fatalf("expected passed, got %v", result.ToolRun.Status)
	}
	if !strings.Contains(result.ToolRun.Summary, "no secrets or vulnerabilities") {
		t.Fatalf("unexpected summary %q", result.ToolRun.Summary)
	}
}
