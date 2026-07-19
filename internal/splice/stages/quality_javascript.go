package stages

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// jsSyntaxCheck is a deterministic quality adapter that runs `node --check`
// over JavaScript and JSX source files. Node itself is the only dependency;
// the check degrades to incomplete (never clean) when Node is unavailable.
type jsSyntaxCheck struct{}

func (jsSyntaxCheck) Name() string     { return "js_syntax" }
func (jsSyntaxCheck) Category() string { return "quality" }

func (c jsSyntaxCheck) Run(ctx context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	var jsPaths []string
	for _, p := range req.Paths {
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".js" || ext == ".jsx" {
			jsPaths = append(jsPaths, p)
		}
	}
	sort.Strings(jsPaths)
	if len(jsPaths) == 0 {
		return VerificationCheckResult{
			ToolRun: schemas.VerificationToolRun{
				Tool:     c.Name(),
				Required: true,
				Scope:    "quality",
				Status:   schemas.VerificationNotApplicable,
				Summary:  "No JavaScript files to check.",
			},
		}, nil
	}

	runCtx, cancel := withSubprocessTimeout(ctx)
	defer cancel()

	var findings []schemas.VerificationFinding
	for _, file := range jsPaths {
		select {
		case <-runCtx.Done():
			return VerificationCheckResult{}, runCtx.Err()
		default:
		}
		rel := relPath(file, req.WorkDir)
		command := []string{"node", "--check", file}
		var out []byte
		var runErr error
		if req.RunTool != nil {
			res, terr := req.RunTool(runCtx, "bash", map[string]any{
				"command": command,
				"cwd":     req.WorkDir,
			})
			if terr != nil {
				return VerificationCheckResult{}, fmt.Errorf("js syntax check: %w", terr)
			}
			out = []byte(res.Output)
			if !res.OK {
				ol := strings.ToLower(res.Output)
				switch {
				case strings.Contains(ol, "permission required") || strings.Contains(ol, "permission denied"):
					return VerificationCheckResult{}, fmt.Errorf("js syntax check failed: %s", res.Output)
				case strings.Contains(ol, "command not found") || strings.Contains(ol, "no such file") || strings.Contains(ol, "not found"):
					return jsIncomplete("Node.js is not installed; JavaScript syntax check incomplete.")
				default:
					findings = append(findings, jsFinding(rel, strings.TrimSpace(res.Output)))
				}
			}
		} else {
			cmd := exec.CommandContext(runCtx, command[0], command[1:]...)
			cmd.Dir = req.WorkDir
			out, runErr = cmd.CombinedOutput()
			if runErr != nil {
				if errors.Is(runErr, exec.ErrNotFound) ||
					strings.Contains(strings.ToLower(runErr.Error()), "executable file not found") {
					return jsIncomplete("Node.js is not installed; JavaScript syntax check incomplete.")
				}
				if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
					return jsIncomplete("JavaScript syntax check timed out.")
				}
				if errors.Is(runCtx.Err(), context.Canceled) {
					return VerificationCheckResult{}, runCtx.Err()
				}
				findings = append(findings, jsFinding(rel, strings.TrimSpace(string(out))))
			}
		}
	}

	status := schemas.VerificationPassed
	summary := fmt.Sprintf("Checked %d JavaScript file(s), no syntax errors.", len(jsPaths))
	if len(findings) > 0 {
		status = schemas.VerificationFindings
		summary = fmt.Sprintf("JavaScript syntax check found %d error(s).", len(findings))
	}
	return VerificationCheckResult{
		ToolRun: schemas.VerificationToolRun{
			Tool:     c.Name(),
			Required: true,
			Scope:    "quality",
			Status:   status,
			Summary:  summary,
		},
		Findings: findings,
	}, nil
}

func jsIncomplete(summary string) (VerificationCheckResult, error) {
	return VerificationCheckResult{
		ToolRun: schemas.VerificationToolRun{
			Tool:     "js_syntax",
			Required: true,
			Scope:    "quality",
			Status:   schemas.VerificationIncomplete,
			Summary:  summary,
		},
	}, nil
}

func jsFinding(rel, msg string) schemas.VerificationFinding {
	return schemas.VerificationFinding{
		Tool:      "js_syntax",
		Authority: "deterministic",
		RuleID:    "JS_SYNTAX",
		Category:  "quality",
		Path:      rel,
		Message:   msg,
		Severity:  schemas.SeverityHigh,
	}
}
