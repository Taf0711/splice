package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// gosecCheck is a deterministic security adapter that runs Gosec on Go
// files and reports findings. It wraps the Gosec JSON output behind the
// VerificationCheck interface.
type gosecCheck struct{}

func (gosecCheck) Name() string     { return "gosec" }
func (gosecCheck) Category() string { return "security" }

func (c gosecCheck) Run(ctx context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	goPaths := filterGoPaths(req.Paths)
	if len(goPaths) == 0 {
		return VerificationCheckResult{
			ToolRun: schemas.VerificationToolRun{
				Tool:     c.Name(),
				Required: true,
				Scope:    "security",
				Status:   schemas.VerificationNotApplicable,
				Summary:  "No Go files to scan.",
			},
		}, nil
	}

	relPaths := make([]string, len(goPaths))
	for i, p := range goPaths {
		rel, _ := filepath.Rel(req.WorkDir, p)
		if rel == "" || strings.HasPrefix(rel, "..") {
			rel = p
		}
		relPaths[i] = rel
	}

	var out []byte
	var err error
	if req.RunTool != nil {
		res, terr := req.RunTool(ctx, "gosec", map[string]any{"paths": toStringAny(relPaths)})
		if terr != nil {
			return VerificationCheckResult{}, fmt.Errorf("gosec tool: %w", terr)
		}
		if !res.OK {
			outLower := strings.ToLower(res.Output)
			switch {
			case strings.Contains(outLower, "permission required") || strings.Contains(outLower, "permission denied"):
				return VerificationCheckResult{}, fmt.Errorf("gosec tool failed: %s", res.Output)
			case strings.Contains(outLower, "not installed") || strings.Contains(outLower, "not available"):
				return VerificationCheckResult{
					ToolRun: schemas.VerificationToolRun{
						Tool:     c.Name(),
						Required: true,
						Scope:    "security",
						Status:   schemas.VerificationIncomplete,
						Summary:  "Gosec is not installed; Go security scan incomplete.",
					},
				}, nil
			default:
				return VerificationCheckResult{}, fmt.Errorf("gosec tool failed: %s", res.Output)
			}
		}
		out = []byte(res.Output)
	} else {
		args := append([]string{"-fmt", "json"}, goPaths...)
		cmd := exec.CommandContext(ctx, "gosec", args...)
		cmd.Dir = req.WorkDir
		out, err = cmd.CombinedOutput()
		if err != nil && !strings.Contains(string(out), `"Issues"`) {
			// gosec not installed: exec.ErrNotFound produces no output, so check
			// the error message too (not just the output) to degrade to incomplete.
			outLower := strings.ToLower(string(out))
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(outLower, "not found") || strings.Contains(outLower, "not installed") ||
				strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "not installed") {
				return VerificationCheckResult{
					ToolRun: schemas.VerificationToolRun{
						Tool:     c.Name(),
						Required: true,
						Scope:    "security",
						Status:   schemas.VerificationIncomplete,
						Summary:  "Gosec is not installed; Go security scan incomplete.",
					},
				}, nil
			}
			return VerificationCheckResult{}, fmt.Errorf("gosec failed: %w\n%s", err, string(out))
		}
	}

	var report gosecReport
	if err := json.Unmarshal(out, &report); err != nil {
		return VerificationCheckResult{}, fmt.Errorf("parse gosec JSON: %w", err)
	}
	findings := make([]schemas.VerificationFinding, 0, len(report.Issues))
	for _, issue := range report.Issues {
		linePtr := parseGosecLine(issue.Line)
		sev := severityForGosec(issue.Severity)
		msg := issue.Details
		if issue.RuleID != "" {
			msg = fmt.Sprintf("%s: %s", issue.RuleID, msg)
		}
		path := issue.File
		if rel, _ := filepath.Rel(req.WorkDir, path); rel != "" && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		findings = append(findings, schemas.VerificationFinding{
			Tool:      c.Name(),
			Authority: "deterministic",
			RuleID:    issue.RuleID,
			Category:  c.Category(),
			Path:      path,
			Line:      linePtr,
			Message:   msg,
			Severity:  sev,
		})
	}
	status := schemas.VerificationPassed
	summary := fmt.Sprintf("Gosec scanned %d Go file(s), no issues.", len(relPaths))
	if len(findings) > 0 {
		status = schemas.VerificationFindings
		summary = fmt.Sprintf("Gosec found %d security issue(s).", len(findings))
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

type gosecCWE struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type gosecIssue struct {
	Severity   string   `json:"severity"`
	Confidence string   `json:"confidence"`
	RuleID     string   `json:"rule_id"`
	File       string   `json:"file"`
	Line       string   `json:"line"`
	Column     string   `json:"column"`
	CWE        gosecCWE `json:"cwe"`
	Details    string   `json:"details"`
	Code       string   `json:"code"`
}

type gosecReport struct {
	Issues []gosecIssue `json:"Issues"`
	Stats  any          `json:"Stats,omitempty"`
}

func filterGoPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasSuffix(strings.ToLower(p), ".go") {
			out = append(out, p)
		}
	}
	return out
}

func parseGosecLine(line string) *int {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	parts := strings.SplitN(line, "-", 2)
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || n < 1 {
		return nil
	}
	return &n
}

func severityForGosec(sev string) schemas.Severity {
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
