package stages

import (
	"context"
	"sort"
	"strconv"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// VerificationCheck is one deterministic quality or security check adapter.
// It is deliberately deterministic: no provider, model, prompt, or free-form
// model output participates in this interface. A future advisory stage
// consumes the completed VerificationReport through a separate typed boundary.
type VerificationCheck interface {
	Name() string
	Category() string
	Run(context.Context, VerificationCheckRequest) (VerificationCheckResult, error)
}

// VerificationCheckRequest carries the deterministic inputs a check needs.
type VerificationCheckRequest struct {
	WorkDir  string
	Language string
	Paths    []string
	Scope    string
	RunTool  func(context.Context, string, map[string]any) (ToolResult, error)
}

// VerificationCheckResult is one check's self-reported tool run plus findings.
type VerificationCheckResult struct {
	ToolRun  schemas.VerificationToolRun
	Findings []schemas.VerificationFinding
}

// aggregateVerificationResults is a pure function that composes independent
// deterministic check results into a single typed report. It validates tool
// identity, normalizes and sorts findings, assigns stable fingerprints,
// deduplicates exact findings, and derives report completeness and status.
// It never calls a provider.
func aggregateVerificationResults(results []VerificationCheckResult) schemas.VerificationReport {
	tools := make([]schemas.VerificationToolRun, 0, len(results))
	allFindings := make([]schemas.VerificationFinding, 0)
	for _, r := range results {
		tools = append(tools, r.ToolRun)
		allFindings = append(allFindings, r.Findings...)
	}

	findings := deduplicateFindings(allFindings)
	sortFindings(findings)

	hasRequiredIncomplete := false
	hasFindings := len(findings) > 0
	allNotApplicable := true
	for _, t := range tools {
		if t.Status == schemas.VerificationIncomplete && t.Required {
			hasRequiredIncomplete = true
		}
		if t.Status != schemas.VerificationNotApplicable {
			allNotApplicable = false
		}
	}

	var status schemas.VerificationStatus
	complete := true
	switch {
	case hasRequiredIncomplete:
		status = schemas.VerificationIncomplete
		complete = false
	case hasFindings:
		status = schemas.VerificationFindings
	case allNotApplicable && len(tools) > 0:
		status = schemas.VerificationNotApplicable
	default:
		status = schemas.VerificationPassed
	}

	summary := verificationSummary(status, tools, findings)
	return schemas.VerificationReport{
		Status:   status,
		Complete: complete,
		Summary:  summary,
		Tools:    tools,
		Findings: findings,
	}
}

func deduplicateFindings(findings []schemas.VerificationFinding) []schemas.VerificationFinding {
	seen := make(map[string]bool, len(findings))
	out := make([]schemas.VerificationFinding, 0, len(findings))
	for _, f := range findings {
		if f.Fingerprint == "" {
			f.Fingerprint = schemas.VerificationFingerprint(f.Tool, f.RuleID, f.Path, f.Line, f.Message)
		}
		if seen[f.Fingerprint] {
			continue
		}
		seen[f.Fingerprint] = true
		out = append(out, f)
	}
	return out
}

var severityOrder = map[schemas.Severity]int{
	schemas.SeverityCritical: 0,
	schemas.SeverityHigh:     1,
	schemas.SeverityMedium:   2,
	schemas.SeverityLow:      3,
	schemas.SeverityInfo:     4,
}

func sortFindings(findings []schemas.VerificationFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		si := severityOrder[findings[i].Severity]
		sj := severityOrder[findings[j].Severity]
		if si != sj {
			return si < sj
		}
		if findings[i].Tool != findings[j].Tool {
			return findings[i].Tool < findings[j].Tool
		}
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		li := 0
		if findings[i].Line != nil {
			li = *findings[i].Line
		}
		lj := 0
		if findings[j].Line != nil {
			lj = *findings[j].Line
		}
		if li != lj {
			return li < lj
		}
		return findings[i].RuleID < findings[j].RuleID
	})
}

func verificationSummary(status schemas.VerificationStatus, tools []schemas.VerificationToolRun, findings []schemas.VerificationFinding) string {
	switch status {
	case schemas.VerificationPassed:
		return "All applicable verification checks passed."
	case schemas.VerificationFindings:
		crit := 0
		high := 0
		for _, f := range findings {
			switch f.Severity {
			case schemas.SeverityCritical:
				crit++
			case schemas.SeverityHigh:
				high++
			}
		}
		if crit > 0 || high > 0 {
			return verificationCount(findings) + " (" + strconv.Itoa(crit) + " critical, " + strconv.Itoa(high) + " high)"
		}
		return verificationCount(findings)
	case schemas.VerificationIncomplete:
		for _, t := range tools {
			if t.Status == schemas.VerificationIncomplete && t.Required {
				return t.Summary
			}
		}
		return "verification incomplete"
	case schemas.VerificationNotApplicable:
		return "No applicable files to verify."
	default:
		return "verification report"
	}
}

func verificationCount(findings []schemas.VerificationFinding) string {
	n := len(findings)
	if n == 1 {
		return "1 verification finding"
	}
	return strconv.Itoa(n) + " verification findings"
}

// DefaultQualityChecks returns the default set of deterministic quality checks
// for production registry construction. Each check decides internally whether
// it applies to the detected language and file set.
func DefaultQualityChecks() []VerificationCheck {
	return []VerificationCheck{goSyntaxCheck{}, pythonSyntaxCheck{}, jsSyntaxCheck{}, tsTypeCheck{}}
}

// DefaultSecurityChecks returns the default set of deterministic security checks.
func DefaultSecurityChecks() []VerificationCheck {
	return []VerificationCheck{banditCheck{}, gosecCheck{}, sarifCheck{scanners: defaultSarifScanners()}, trivyCheck{}}
}

// toStringAny converts a []string path list to []any so that dtools adapters
// receive args["paths"] as the []any value their Run implementation asserts.
// The stage RunTool seam hands the map straight to the registry, so a raw
// []string would cause the tool to reject the call.
func toStringAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
