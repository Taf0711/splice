package stages

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestDefaultSecurityChecksIncludesGosec(t *testing.T) {
	checks := DefaultSecurityChecks()
	found := false
	for _, c := range checks {
		if c.Name() == "gosec" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DefaultSecurityChecks() missing gosec: %#v", checks)
	}
}

func TestDefaultSecurityChecksIncludesSarif(t *testing.T) {
	checks := DefaultSecurityChecks()
	found := false
	for _, c := range checks {
		if c.Name() == "sarif" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DefaultSecurityChecks() missing sarif: %#v", checks)
	}
}

// A report passes only when at least one check ran clean. Not-applicable
// checks and a non-required incomplete check verify nothing.
func TestAggregateVerificationPassesOnlyWhenACheckPassed(t *testing.T) {
	run := func(status schemas.VerificationStatus, required bool) VerificationCheckResult {
		return VerificationCheckResult{ToolRun: schemas.VerificationToolRun{Tool: "tool", Scope: "scope", Status: status, Required: required, Summary: "s"}}
	}
	tests := []struct {
		name    string
		results []VerificationCheckResult
		want    schemas.VerificationStatus
	}{
		{name: "not applicable with a missing optional tool is not passed", results: []VerificationCheckResult{run(schemas.VerificationNotApplicable, false), run(schemas.VerificationNotApplicable, false), run(schemas.VerificationIncomplete, false)}, want: schemas.VerificationNotApplicable},
		{name: "not applicable alone", results: []VerificationCheckResult{run(schemas.VerificationNotApplicable, false)}, want: schemas.VerificationNotApplicable},
		{name: "no tools is not passed", results: nil, want: schemas.VerificationNotApplicable},
		{name: "a clean check passes", results: []VerificationCheckResult{run(schemas.VerificationNotApplicable, false), run(schemas.VerificationPassed, true)}, want: schemas.VerificationPassed},
		{name: "a clean check beside an optional miss passes", results: []VerificationCheckResult{run(schemas.VerificationPassed, true), run(schemas.VerificationIncomplete, false)}, want: schemas.VerificationPassed},
		{name: "a required miss is incomplete", results: []VerificationCheckResult{run(schemas.VerificationIncomplete, true)}, want: schemas.VerificationIncomplete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := aggregateVerificationResults(tt.results)
			if report.Status != tt.want {
				t.Fatalf("status = %q, want %q", report.Status, tt.want)
			}
			if err := report.Validate(); err != nil {
				t.Fatalf("aggregated report is invalid: %v", err)
			}
		})
	}
}
