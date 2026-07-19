package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// banditCheck is a deterministic security adapter that runs Bandit on Python
// files and reports findings. It wraps the existing Bandit JSON parsing logic
// behind the VerificationCheck interface.
type banditCheck struct{}

func (banditCheck) Name() string     { return "bandit" }
func (banditCheck) Category() string { return "security" }

func (c banditCheck) Run(ctx context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	pyPaths := filterPythonPaths(req.Paths)
	if len(pyPaths) == 0 {
		return VerificationCheckResult{
			ToolRun: schemas.VerificationToolRun{
				Tool:     c.Name(),
				Required: true,
				Scope:    "security",
				Status:   schemas.VerificationNotApplicable,
				Summary:  "No Python files to scan.",
			},
		}, nil
	}

	relPaths := make([]string, len(pyPaths))
	for i, p := range pyPaths {
		rel, _ := filepath.Rel(req.WorkDir, p)
		if rel == "" || strings.HasPrefix(rel, "..") {
			rel = p
		}
		relPaths[i] = rel
	}

	var out []byte
	var err error
	if req.RunTool != nil {
		res, terr := req.RunTool(ctx, "bandit", map[string]any{"paths": toStringAny(relPaths)})
		if terr != nil {
			return VerificationCheckResult{}, fmt.Errorf("bandit tool: %w", terr)
		}
		if !res.OK {
			outLower := strings.ToLower(res.Output)
			switch {
			case strings.Contains(outLower, "permission required") || strings.Contains(outLower, "permission denied"):
				return VerificationCheckResult{}, fmt.Errorf("bandit tool failed: %s", res.Output)
			case strings.Contains(outLower, "not installed") || strings.Contains(outLower, "not available"):
				return VerificationCheckResult{
					ToolRun: schemas.VerificationToolRun{
						Tool:     c.Name(),
						Required: true,
						Scope:    "security",
						Status:   schemas.VerificationIncomplete,
						Summary:  "Bandit is not installed; security scan incomplete.",
					},
				}, nil
			default:
				return VerificationCheckResult{}, fmt.Errorf("bandit tool failed: %s", res.Output)
			}
		}
		out = []byte(res.Output)
	} else {
		args := append([]string{"-m", "bandit", "-f", "json"}, pyPaths...)
		cmd := exec.CommandContext(ctx, "python", args...)
		cmd.Dir = req.WorkDir
		out, err = cmd.CombinedOutput()
		if err != nil && !strings.Contains(string(out), `"results"`) {
			outLower := strings.ToLower(string(out))
			if strings.Contains(outLower, "no module named bandit") || strings.Contains(outLower, "not found") {
				return VerificationCheckResult{
					ToolRun: schemas.VerificationToolRun{
						Tool:     c.Name(),
						Required: true,
						Scope:    "security",
						Status:   schemas.VerificationIncomplete,
						Summary:  "Bandit is not installed; security scan incomplete.",
					},
				}, nil
			}
			return VerificationCheckResult{}, fmt.Errorf("bandit failed: %w\n%s", err, string(out))
		}
	}

	var report banditReport
	if err := json.Unmarshal(out, &report); err != nil {
		return VerificationCheckResult{}, fmt.Errorf("parse bandit JSON: %w", err)
	}
	findings := make([]schemas.VerificationFinding, 0, len(report.Results))
	for _, r := range report.Results {
		line := 0
		if len(r.LineRange) > 0 {
			line = r.LineRange[0]
		}
		sev := severityForBandit(r.IssueSeverity)
		msg := r.IssueText
		if r.TestID != "" {
			msg = fmt.Sprintf("%s: %s", r.TestID, msg)
		}
		path := r.Filename
		if rel, _ := filepath.Rel(req.WorkDir, path); rel != "" && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		var linePtr *int
		if line > 0 {
			linePtr = &line
		}
		findings = append(findings, schemas.VerificationFinding{
			Tool:      c.Name(),
			Authority: "deterministic",
			RuleID:    r.TestID,
			Category:  c.Category(),
			Path:      path,
			Line:      linePtr,
			Message:   msg,
			Severity:  sev,
		})
	}
	status := schemas.VerificationPassed
	summary := fmt.Sprintf("Bandit scanned %d Python file(s), no issues.", len(relPaths))
	if len(findings) > 0 {
		status = schemas.VerificationFindings
		summary = fmt.Sprintf("Bandit found %d security issue(s).", len(findings))
	}
	return VerificationCheckResult{
		ToolRun: schemas.VerificationToolRun{
			Tool:     c.Name(),
			Required: true,
			Scope:    "security",
			Status:   status,
			Summary:  summary,
		},
		Findings: findings,
	}, nil
}

type banditReport struct {
	Results []struct {
		Filename      string `json:"filename"`
		LineRange     []int  `json:"line_range"`
		IssueText     string `json:"issue_text"`
		IssueSeverity string `json:"issue_severity"`
		TestID        string `json:"test_id"`
	} `json:"results"`
}

func filterPythonPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasSuffix(strings.ToLower(p), ".py") {
			out = append(out, p)
		}
	}
	return out
}

func severityForBandit(sev string) schemas.Severity {
	switch strings.ToLower(sev) {
	case "low":
		return schemas.SeverityLow
	case "medium":
		return schemas.SeverityMedium
	case "high":
		return schemas.SeverityHigh
	default:
		return schemas.SeverityMedium
	}
}
