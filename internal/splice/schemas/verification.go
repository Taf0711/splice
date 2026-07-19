package schemas

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// VerificationStatus is the deterministic coverage state of one verification report.
type VerificationStatus string

const (
	// VerificationPassed means every applicable check ran and reported no findings.
	VerificationPassed VerificationStatus = "passed"
	// VerificationFindings means at least one deterministic check reported a finding.
	VerificationFindings VerificationStatus = "findings"
	// VerificationIncomplete means a required check could not run (tool missing,
	// permission granted but executable unavailable, timeout, unsupported language
	// profile). Coverage is unknown, not clean.
	VerificationIncomplete VerificationStatus = "incomplete"
	// VerificationNotApplicable means no files matched the check's scope, so there
	// is nothing to verify. This is distinct from passed: absence of files is not
	// evidence of a clean result.
	VerificationNotApplicable VerificationStatus = "not_applicable"
)

// VerificationFinding is one deterministic observation from a verification check.
// The Fingerprint is derived (never trusted) and must equal the SHA-256 over the
// finding's identity fields; the trajectory monitor and any future advisor stage
// rely on this for stable deduplication and provenance.
type VerificationFinding struct {
	Fingerprint string   `json:"fingerprint"`
	Tool        string   `json:"tool"`
	Authority   string   `json:"authority"` // "deterministic" or "advisory"
	RuleID      string   `json:"rule_id,omitempty"`
	Category    string   `json:"category"`
	Path        string   `json:"path,omitempty"`
	Line        *int     `json:"line,omitempty"`
	Column      *int     `json:"column,omitempty"`
	Message     string   `json:"message"`
	Severity    Severity `json:"severity"`
	Evidence    string   `json:"evidence,omitempty"`
}

// Validate checks the finding's required fields and fingerprint integrity.
func (f VerificationFinding) Validate() error {
	switch f.Authority {
	case "deterministic", "advisory":
	default:
		return fmt.Errorf("invalid authority %q", f.Authority)
	}
	if f.Tool == "" {
		return errors.New("tool is required")
	}
	if f.Category == "" {
		return errors.New("category is required")
	}
	if f.Message == "" {
		return errors.New("message is required")
	}
	switch f.Severity {
	case SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
	default:
		return fmt.Errorf("invalid severity %q", f.Severity)
	}
	if f.Line != nil && *f.Line < 1 {
		return errors.New("line must be >= 1")
	}
	if f.Column != nil && *f.Column < 1 {
		return errors.New("column must be >= 1")
	}
	if f.Fingerprint == "" {
		return errors.New("fingerprint is required")
	}
	if f.Fingerprint != f.fingerprintExpected() {
		return fmt.Errorf("fingerprint mismatch for tool %q rule %q path %q", f.Tool, f.RuleID, f.Path)
	}
	return nil
}

// normalizeVerificationPath is the canonical form used for fingerprinting and
// display: OS separators become forward slashes and the path is cleaned so
// equivalent paths produce identical fingerprints.
func normalizeVerificationPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// fingerprintExpected returns the SHA-256 fingerprint derived from the finding's
// identity fields, in the contract order: tool, rule_id, normalized path, line, message.
func (f VerificationFinding) fingerprintExpected() string {
	lineStr := "0"
	if f.Line != nil {
		lineStr = strconv.Itoa(*f.Line)
	}
	return verificationFingerprint(f.Tool, f.RuleID, f.Path, lineStr, f.Message)
}

// VerificationFingerprint computes the stable SHA-256 fingerprint for a finding
// from its identity fields. It is exported so adapters, the aggregator, tests,
// and a future advisor validator share one derivation.
func VerificationFingerprint(tool, ruleID, path string, line *int, message string) string {
	lineStr := "0"
	if line != nil {
		lineStr = strconv.Itoa(*line)
	}
	return verificationFingerprint(tool, ruleID, path, lineStr, message)
}

func verificationFingerprint(tool, ruleID, path, lineStr, message string) string {
	raw := strings.Join([]string{
		tool,
		ruleID,
		normalizeVerificationPath(path),
		lineStr,
		message,
	}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerificationToolRun is one check's self-reported result inside a VerificationReport.
type VerificationToolRun struct {
	Tool     string             `json:"tool"`
	Required bool               `json:"required"`
	Scope    string             `json:"scope"`
	Status   VerificationStatus `json:"status"`
	Summary  string             `json:"summary"`
}

// Validate checks the tool run's required fields.
func (t VerificationToolRun) Validate() error {
	if t.Tool == "" {
		return errors.New("tool name is required")
	}
	if t.Scope == "" {
		return errors.New("tool scope is required")
	}
	switch t.Status {
	case VerificationPassed, VerificationFindings, VerificationIncomplete, VerificationNotApplicable:
	default:
		return fmt.Errorf("invalid tool status %q", t.Status)
	}
	if t.Summary == "" {
		return errors.New("tool run summary is required")
	}
	return nil
}

// VerificationReport is the typed, fail-honest result of one deterministic
// verification stage (static analysis or security audit). It replaces the
// ambiguous StaticAnalyzerOutput/StaticIssue pair so that missing coverage is
// explicit (incomplete / not_applicable) rather than reported as clean, and so
// a future advisory stage can annotate findings without suppressing the
// deterministic evidence in this boundary.
type VerificationReport struct {
	Status   VerificationStatus    `json:"status"`
	Complete bool                  `json:"complete"`
	Summary  string                `json:"summary"`
	Tools    []VerificationToolRun `json:"tools"`
	Findings []VerificationFinding `json:"findings"`
}

// Validate enforces the typed verification contract.
func (r VerificationReport) Validate() error {
	switch r.Status {
	case VerificationPassed, VerificationFindings, VerificationIncomplete, VerificationNotApplicable:
	default:
		return fmt.Errorf("invalid verification status %q", r.Status)
	}
	if r.Summary == "" {
		return errors.New("summary is required")
	}

	// Completeness is coupled to status: passed/findings/not_applicable mean the
	// stage produced a definitive verdict; incomplete means coverage is unknown.
	switch r.Status {
	case VerificationPassed, VerificationFindings, VerificationNotApplicable:
		if !r.Complete {
			return fmt.Errorf("status %q requires complete=true", r.Status)
		}
	case VerificationIncomplete:
		if r.Complete {
			return fmt.Errorf("status incomplete requires complete=false")
		}
		hasRequiredIncomplete := false
		for _, t := range r.Tools {
			if t.Status == VerificationIncomplete && t.Required {
				hasRequiredIncomplete = true
				break
			}
		}
		if !hasRequiredIncomplete {
			return errors.New("incomplete status requires at least one required incomplete tool run")
		}
	}

	// Status and findings consistency.
	switch r.Status {
	case VerificationPassed:
		if len(r.Findings) > 0 {
			return errors.New("passed report cannot contain findings")
		}
	case VerificationFindings:
		if len(r.Findings) == 0 {
			return errors.New("findings report requires at least one finding")
		}
	}

	toolNames := make(map[string]bool, len(r.Tools))
	for i, t := range r.Tools {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("tools[%d]: %w", i, err)
		}
		toolNames[t.Tool] = true
	}

	for i, f := range r.Findings {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("findings[%d]: %w", i, err)
		}
		if !toolNames[f.Tool] {
			return fmt.Errorf("findings[%d]: references unknown tool %q", i, f.Tool)
		}
	}
	return nil
}
