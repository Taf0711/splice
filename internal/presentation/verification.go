package presentation

import (
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// The receipt and the headless summary share these lines, so both name the
// checks with the same words.

// NestedSandboxAcceptedLine is the receipt and summary line of a run whose
// stage commands ran under an outer sandbox that the user accepted
// (PipelineResult.NestedSandboxAccepted). It shows in every run state and
// never has success styling.
const NestedSandboxAcceptedLine = "Stage sandbox not enforced: nested run accepted by the user."

// checkLabel is the reader name of a check: its kind for a builtin check,
// its node name for a custom one.
func checkLabel(check schemas.VerificationCheck) string {
	switch check.Kind {
	case schemas.CheckKindTests:
		return "tests"
	case schemas.CheckKindAcceptance:
		return "acceptance"
	case schemas.CheckKindSecurityScan:
		return "security scan"
	case schemas.CheckKindStaticAnalysis:
		return "static analysis"
	default:
		return check.Name
	}
}

func checkOutcomeText(outcome schemas.VerificationCheckOutcome) string {
	switch outcome {
	case schemas.CheckCommandPassed:
		return "command passed"
	case schemas.CheckNoTests:
		return "no tests ran"
	case schemas.CheckNotApplicable:
		return "not applicable"
	default:
		return string(outcome)
	}
}

// checkPhrase is "label: outcome (detail)". The detail is left out when it
// only repeats the outcome.
func checkPhrase(check schemas.VerificationCheck) string {
	outcome := checkOutcomeText(check.Outcome)
	phrase := checkLabel(check) + ": " + outcome
	if check.Detail != "" && check.Detail != outcome {
		phrase += " (" + check.Detail + ")"
	}
	return phrase
}

// maxNamedFailures bounds the test names that one receipt line lists.
const maxNamedFailures = 10

// alreadyFailingLine names the tests that failed before the run and that the
// run did not cause. The list is capped, and the rest are counted.
func alreadyFailingLine(names []string) string {
	shown, more := names, 0
	if len(names) > maxNamedFailures {
		shown, more = names[:maxNamedFailures], len(names)-maxNamedFailures
	}
	line := "Already failing before this run (not caused by it): " + strings.Join(shown, ", ")
	if more > 0 {
		line += fmt.Sprintf(", and %d more", more)
	}
	return line + "."
}

// CheckLines returns one line per check, for a run that is not verified.
func CheckLines(checks []schemas.VerificationCheck) []string {
	lines := make([]string, 0, len(checks))
	for _, check := range checks {
		lines = append(lines, checkPhrase(check))
		if check.Kind == schemas.CheckKindTests && len(check.AlreadyFailing) > 0 {
			lines = append(lines, alreadyFailingLine(check.AlreadyFailing))
		}
	}
	return lines
}

// VerifiedLines returns the lines of a verified run. The first line names the
// checks that passed. When no test check passed, a second line says what the
// tests did, so a verification without tests never reads like a tested
// change. The checks that did not count follow on one line.
func VerifiedLines(checks []schemas.VerificationCheck) []string {
	var passed, other []string
	var tests *schemas.VerificationCheck
	for i, check := range checks {
		if check.Outcome == schemas.CheckPassed {
			label := checkLabel(check)
			if check.Detail != "" {
				label += " (" + check.Detail + ")"
			}
			passed = append(passed, label)
		} else if check.Kind != schemas.CheckKindTests {
			other = append(other, checkPhrase(check))
		}
		if check.Kind == schemas.CheckKindTests && (tests == nil || check.Outcome == schemas.CheckPassed) {
			tests = &checks[i]
		}
	}
	if len(passed) == 0 {
		return nil
	}
	lines := []string{"Verified by: " + strings.Join(passed, ", ")}
	switch {
	case tests == nil:
		lines = append(lines, "Tests: no tests ran.")
	case tests.Outcome == schemas.CheckCommandPassed:
		lines = append(lines, "Tests: the test command passed, but per-test evidence is not available.")
	case tests.Outcome != schemas.CheckPassed:
		lines = append(lines, "Tests: "+strings.TrimPrefix(checkPhrase(*tests), "tests: ")+".")
	}
	if tests != nil && len(tests.AlreadyFailing) > 0 {
		lines = append(lines, alreadyFailingLine(tests.AlreadyFailing))
	}
	if len(other) > 0 {
		lines = append(lines, "Other checks: "+strings.Join(other, "; "))
	}
	return lines
}
