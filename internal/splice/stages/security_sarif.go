package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// sarifCheck is a generic SARIF-parsing security adapter. One parser + a
// language->scanner map: a new language is one config line plus the scanner
// installed. It is additive to the hand-tuned Bandit/gosec adapters.
type sarifCheck struct {
	scanners map[string]sarifScanner
}

type sarifScanner struct {
	name string
	args []string
}

func (sarifCheck) Name() string     { return "sarif" }
func (sarifCheck) Category() string { return "security" }

func defaultSarifScanners() map[string]sarifScanner {
	return map[string]sarifScanner{
		"javascript": {name: "npx", args: []string{"--no-install", "eslint", "--format", "@microsoft/eslint-formatter-sarif", "."}},
		"typescript": {name: "npx", args: []string{"--no-install", "eslint", "--format", "@microsoft/eslint-formatter-sarif", "."}},
	}
}

func (c sarifCheck) Run(ctx context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	langs := detectSarifLanguages(req.Paths, c.scanners)
	if len(langs) == 0 {
		return VerificationCheckResult{
			ToolRun: schemas.VerificationToolRun{
				Tool:     c.Name(),
				Required: true,
				Scope:    "security",
				Status:   schemas.VerificationNotApplicable,
				Summary:  "No files to scan for configured SARIF scanners.",
			},
		}, nil
	}

	// Stable iteration order for reproducible test output.
	langOrder := make([]string, 0, len(langs))
	for lang := range langs {
		langOrder = append(langOrder, lang)
	}
	sort.Strings(langOrder)

	allFindings := make([]schemas.VerificationFinding, 0)
	scannedLangs := 0
	missingLangs := 0
	var firstErr error

	for _, lang := range langOrder {
		scanner := c.scanners[lang]
		relPaths := langs[lang]
		if len(relPaths) == 0 {
			continue
		}

		out, toolErr := c.runScanner(ctx, req, scanner, relPaths)
		if toolErr != nil {
			if c.isMissingTool(toolErr) {
				missingLangs++
				continue
			}
			if firstErr == nil {
				firstErr = toolErr
			}
			continue
		}
		scannedLangs++

		findings, parseErr := c.parseSarif(lang, out)
		if parseErr != nil {
			if firstErr == nil {
				firstErr = parseErr
			}
			continue
		}
		allFindings = append(allFindings, c.mapSarifFindings(req, lang, findings)...)
	}

	status := schemas.VerificationPassed
	summary := fmt.Sprintf("SARIF scanners scanned %d language(s), no issues.", scannedLangs)
	if len(allFindings) > 0 {
		status = schemas.VerificationFindings
		summary = fmt.Sprintf("SARIF scanners found %d security issue(s) across %d language(s).", len(allFindings), scannedLangs)
	} else if missingLangs > 0 && scannedLangs == 0 {
		status = schemas.VerificationIncomplete
		summary = "SARIF scanner(s) not installed; security scan incomplete."
	}

	result := VerificationCheckResult{
		ToolRun: schemas.VerificationToolRun{
			Tool:     c.Name(),
			Required: true,
			Scope:    "security",
			Status:   status,
			Summary:  summary,
		},
		Findings: allFindings,
	}
	if firstErr != nil {
		return result, firstErr
	}
	return result, nil
}

func (c sarifCheck) runScanner(ctx context.Context, req VerificationCheckRequest, scanner sarifScanner, relPaths []string) ([]byte, error) {
	if req.RunTool != nil {
		res, err := req.RunTool(ctx, "sarif", map[string]any{
			"command": scanner.name,
			"args":    toStringAny(scanner.args),
			"paths":   toStringAny(relPaths),
		})
		if err != nil {
			return nil, err
		}
		if !res.OK {
			return nil, fmt.Errorf("sarif tool failed: %s", res.Output)
		}
		return []byte(res.Output), nil
	}

	scannerPath, err := exec.LookPath(scanner.name)
	if err != nil {
		return nil, fmt.Errorf("SARIF scanner is not installed or not available: %w", err)
	}

	cmdArgs := append(scanner.args, relPaths...)
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
			return nil, fmt.Errorf("SARIF scanner is not installed or not available: %w", err)
		}
	}
	return out, nil
}

func (c sarifCheck) isMissingTool(err error) bool {
	return isMissingScannerError(err)
}

func (c sarifCheck) parseSarif(label string, out []byte) ([]sarifResult, error) {
	return parseSarifResults(label, out)
}

func (c sarifCheck) mapSarifFindings(req VerificationCheckRequest, lang string, results []sarifResult) []schemas.VerificationFinding {
	return mapSarifFindings(req.WorkDir, c.Name(), results)
}

func detectSarifLanguages(paths []string, scanners map[string]sarifScanner) map[string][]string {
	langs := make(map[string][]string)
	for _, p := range paths {
		lang := languageForSarifPath(p)
		if _, ok := scanners[lang]; !ok {
			continue
		}
		rel := p
		langs[lang] = append(langs[lang], rel)
	}
	return langs
}

func languageForSarifPath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".go"):
		return "go"
	case strings.HasSuffix(lower, ".ts"), strings.HasSuffix(lower, ".tsx"):
		return "typescript"
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".jsx"):
		return "javascript"
	case strings.HasSuffix(lower, ".py"):
		return "python"
	default:
		return ""
	}
}

func severityForSarif(level string) schemas.Severity {
	switch strings.ToLower(level) {
	case "error":
		return schemas.SeverityHigh
	case "warning":
		return schemas.SeverityMedium
	case "note":
		return schemas.SeverityLow
	case "none":
		return schemas.SeverityInfo
	default:
		return schemas.SeverityMedium
	}
}

type sarifLog struct {
	Runs []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name string `json:"name"`
}

type sarifResult struct {
	RuleID    string     `json:"ruleId"`
	Level     string     `json:"level"`
	Message   sarifMsg   `json:"message"`
	Locations []sarifLoc `json:"locations"`
}

type sarifMsg struct {
	Text string `json:"text"`
}

type sarifLoc struct {
	Physical sarifPhys `json:"physicalLocation"`
}

type sarifPhys struct {
	Artifact sarifArt    `json:"artifactLocation"`
	Region   sarifRegion `json:"region"`
}

type sarifArt struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   *int `json:"startLine"`
	StartColumn *int `json:"startColumn"`
}

// parseSarifResults is the shared SARIF v2.1.0 parser used by sarifCheck
// (per-language lint scanners) and trivyCheck (workspace-level secret + dep
// scanning). label is used only for error context.
func parseSarifResults(label string, out []byte) ([]sarifResult, error) {
	var log sarifLog
	if err := json.Unmarshal(out, &log); err != nil {
		return nil, fmt.Errorf("parse SARIF for %s: %w", label, err)
	}
	results := make([]sarifResult, 0)
	for _, run := range log.Runs {
		results = append(results, run.Results...)
	}
	return results, nil
}

// mapSarifFindings maps parsed SARIF results to deterministic VerificationFindings.
// tool is the finding's Tool field ("sarif" or "trivy").
func mapSarifFindings(workDir string, tool string, results []sarifResult) []schemas.VerificationFinding {
	findings := make([]schemas.VerificationFinding, 0, len(results))
	for _, r := range results {
		path := ""
		line := (*int)(nil)
		if len(r.Locations) > 0 {
			loc := r.Locations[0]
			path = loc.Physical.Artifact.URI
			line = loc.Physical.Region.StartLine
			if rel, _ := filepath.Rel(workDir, path); rel != "" && !strings.HasPrefix(rel, "..") {
				path = rel
			}
		}

		msg := r.Message.Text
		if r.RuleID != "" && msg != "" {
			msg = fmt.Sprintf("%s: %s", r.RuleID, msg)
		} else if r.RuleID != "" {
			msg = r.RuleID
		}

		findings = append(findings, schemas.VerificationFinding{
			Tool:      tool,
			Authority: "deterministic",
			RuleID:    r.RuleID,
			Category:  "security",
			Path:      path,
			Line:      line,
			Message:   msg,
			Severity:  severityForSarif(r.Level),
		})
	}
	return findings
}

// isMissingScannerError reports whether an error from a scanner run indicates
// the binary is not installed (so the check degrades to incomplete, not a hard
// failure).
func isMissingScannerError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not installed") || strings.Contains(msg, "not available") || strings.Contains(msg, "not found")
}
