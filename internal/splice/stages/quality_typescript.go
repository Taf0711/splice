package stages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// tsTypeCheck is a deterministic quality adapter that runs the project-local
// TypeScript compiler (`node_modules/.bin/tsc --noEmit`) over the workspace.
// It only runs when both tsconfig.json and the local tsc binary exist, and it
// degrades to incomplete (never clean) when tsc is missing.
type tsTypeCheck struct{}

func (tsTypeCheck) Name() string     { return "tsc" }
func (tsTypeCheck) Category() string { return "quality" }

// tsErrorLine matches `file(line,col): error TS####: message` output lines.
var tsErrorLine = regexp.MustCompile(`^(.+)\((\d+),(\d+)\): error (TS\d+): (.+)$`)

func (c tsTypeCheck) Run(ctx context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	tsconfigPath := filepath.Join(req.WorkDir, "tsconfig.json")
	if _, err := os.Stat(tsconfigPath); err != nil {
		return VerificationCheckResult{
			ToolRun: schemas.VerificationToolRun{
				Tool:     c.Name(),
				Required: true,
				Scope:    "quality",
				Status:   schemas.VerificationNotApplicable,
				Summary:  "No tsconfig.json; TypeScript type check not applicable.",
			},
		}, nil
	}

	var tsPaths []string
	for _, p := range req.Paths {
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".ts" || ext == ".tsx" {
			tsPaths = append(tsPaths, p)
		}
	}
	sort.Strings(tsPaths)
	if len(tsPaths) == 0 {
		return VerificationCheckResult{
			ToolRun: schemas.VerificationToolRun{
				Tool:     c.Name(),
				Required: true,
				Scope:    "quality",
				Status:   schemas.VerificationNotApplicable,
				Summary:  "No TypeScript files to check.",
			},
		}, nil
	}

	tscPath := filepath.Join(req.WorkDir, "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tscPath); err != nil {
		return VerificationCheckResult{
			ToolRun: schemas.VerificationToolRun{
				Tool:     c.Name(),
				Required: true,
				Scope:    "quality",
				Status:   schemas.VerificationIncomplete,
				Summary:  "TypeScript compiler not found; type check incomplete.",
			},
		}, nil
	}

	runCtx, cancel := withSubprocessTimeout(ctx)
	defer cancel()

	command := []string{tscPath, "--noEmit", "--pretty", "false"}
	var out []byte
	if req.RunTool != nil {
		res, terr := req.RunTool(runCtx, "bash", map[string]any{
			"command": command,
			"cwd":     req.WorkDir,
		})
		if terr != nil {
			return VerificationCheckResult{}, fmt.Errorf("tsc type check: %w", terr)
		}
		out = []byte(res.Output)
		if !res.OK {
			ol := strings.ToLower(res.Output)
			switch {
			case strings.Contains(ol, "permission required") || strings.Contains(ol, "permission denied"):
				return VerificationCheckResult{}, fmt.Errorf("tsc type check failed: %s", res.Output)
			case strings.Contains(ol, "command not found") || strings.Contains(ol, "no such file") || strings.Contains(ol, "not found"):
				return VerificationCheckResult{
					ToolRun: schemas.VerificationToolRun{
						Tool:     c.Name(),
						Required: true,
						Scope:    "quality",
						Status:   schemas.VerificationIncomplete,
						Summary:  "TypeScript compiler not found; type check incomplete.",
					},
				}, nil
			}
		}
	} else {
		cmd := exec.CommandContext(runCtx, command[0], command[1:]...)
		cmd.Dir = req.WorkDir
		combined, rerr := cmd.CombinedOutput()
		out = combined
		if rerr != nil {
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				return VerificationCheckResult{
					ToolRun: schemas.VerificationToolRun{
						Tool:     c.Name(),
						Required: true,
						Scope:    "quality",
						Status:   schemas.VerificationIncomplete,
						Summary:  "TypeScript type check timed out.",
					},
				}, nil
			}
			if errors.Is(runCtx.Err(), context.Canceled) {
				return VerificationCheckResult{}, runCtx.Err()
			}
		}
	}

	var findings []schemas.VerificationFinding
	for _, line := range strings.Split(string(out), "\n") {
		m := tsErrorLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rel := relPath(m[1], req.WorkDir)
		lineNo, _ := strconv.Atoi(m[2])
		var linePtr *int
		if lineNo > 0 {
			linePtr = ptrInt(lineNo)
		}
		findings = append(findings, schemas.VerificationFinding{
			Tool:      c.Name(),
			Authority: "deterministic",
			RuleID:    m[4],
			Category:  c.Category(),
			Path:      rel,
			Line:      linePtr,
			Message:   m[5],
			Severity:  schemas.SeverityHigh,
		})
	}

	status := schemas.VerificationPassed
	summary := "TypeScript type check passed."
	if len(findings) > 0 {
		status = schemas.VerificationFindings
		summary = fmt.Sprintf("TypeScript type check found %d error(s).", len(findings))
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
