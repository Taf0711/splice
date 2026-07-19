package stages

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// trivyCheck is a workspace-level security adapter that runs Trivy for secret
// and dependency-vulnerability scanning. Unlike sarifCheck (per-language lint
// scanners), trivy scans the whole workspace once: lockfiles for known CVEs
// (vuln) and all files for hardcoded credentials (secret). It emits SARIF
// 2.1.0, parsed by the shared parseSarifResults helper.
//
// Missing trivy degrades to incomplete (not clean), mirroring the Bandit/gosec
// contract. Trivy's vulnerability database requires a network fetch to update;
// a stale or absent DB is trivy's responsibility, not Splice's.
type trivyCheck struct{}

func (trivyCheck) Name() string     { return "trivy" }
func (trivyCheck) Category() string { return "security" }

// trivyCommand is the scanner invocation. paths:["."] scopes trivy to the
// workspace root (resolved by the sarif dtool to an absolute path).
var trivyScanner = sarifScanner{
	name: "trivy",
	args: []string{"fs", "--format", "sarif", "--scanners", "vuln,secret"},
}

func (c trivyCheck) Run(ctx context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	workDir := req.WorkDir
	if workDir == "" {
		return VerificationCheckResult{}, fmt.Errorf("trivy check requires work_dir")
	}

	out, err := c.runTrivy(ctx, req)
	if err != nil {
		if isMissingScannerError(err) {
			return VerificationCheckResult{
				ToolRun: schemas.VerificationToolRun{
					Tool:     c.Name(),
					Required: false,
					Scope:    "security",
					Status:   schemas.VerificationIncomplete,
					Summary:  "Trivy is not installed; secret and dependency scan incomplete.",
				},
			}, nil
		}
		return VerificationCheckResult{}, err
	}

	results, err := parseSarifResults("trivy", out)
	if err != nil {
		return VerificationCheckResult{}, err
	}
	findings := mapSarifFindings(workDir, c.Name(), results)

	status := schemas.VerificationPassed
	summary := "Trivy scanned the workspace, no secrets or vulnerabilities found."
	if len(findings) > 0 {
		status = schemas.VerificationFindings
		summary = fmt.Sprintf("Trivy found %d security issue(s) (secrets + vulnerabilities).", len(findings))
	}
	return VerificationCheckResult{
		ToolRun: schemas.VerificationToolRun{
			Tool:     c.Name(),
			Required: false,
			Scope:    "security",
			Status:   status,
			Summary:  summary,
		},
		Findings: findings,
	}, nil
}

func (c trivyCheck) runTrivy(ctx context.Context, req VerificationCheckRequest) ([]byte, error) {
	if req.RunTool != nil {
		res, err := req.RunTool(ctx, "sarif", map[string]any{
			"command": trivyScanner.name,
			"args":    toStringAny(trivyScanner.args),
			"paths":   toStringAny([]string{"."}),
		})
		if err != nil {
			return nil, err
		}
		if !res.OK {
			return nil, fmt.Errorf("trivy tool failed: %s", res.Output)
		}
		return []byte(res.Output), nil
	}

	scannerPath, err := exec.LookPath(trivyScanner.name)
	if err != nil {
		return nil, fmt.Errorf("Trivy is not installed or not available: %w", err)
	}
	cmdArgs := append(append([]string{}, trivyScanner.args...), ".")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, scannerPath, cmdArgs...)
	cmd.Dir = req.WorkDir
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			return nil, fmt.Errorf("Trivy is not installed or not available: %w", err)
		}
	}
	return out, nil
}
