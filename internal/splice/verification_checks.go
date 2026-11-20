package splice

import (
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// passChecks lists the verification-relevant stages of one pass in record
// order: one check per output that carries test results, acceptance results,
// or a verification report, and one check per verification stage that left
// no such output (skipped, incomplete, or failed). An output with no stage
// (a test fixture) is listed after the records under its kind name.
func passChecks(plan schemas.ExecutionPlan, records []schemas.StageRecord, outputs []schemas.HarnessStageOutput) []schemas.VerificationCheck {
	var checks []schemas.VerificationCheck
	used := make([]bool, len(outputs))
	for _, record := range records {
		matched := false
		for i, out := range outputs {
			if used[i] || out.Stage == "" || out.Stage != record.Name {
				continue
			}
			used[i] = true
			if check, ok := outputCheck(record.Name, out, record.OutputSummary); ok {
				checks = append(checks, check)
				matched = true
			}
		}
		if !matched {
			if check, ok := recordCheck(plan, record); ok {
				checks = append(checks, check)
			}
		}
	}
	for i, out := range outputs {
		if used[i] {
			continue
		}
		if check, ok := outputCheck(out.Stage, out, nil); ok {
			checks = append(checks, check)
		}
	}
	return checks
}

// outputCheck builds the check of one stage output. The kind comes from the
// typed payload: the builtin stages keep their legacy report keys, so a
// report under the canonical key only is a custom node.
func outputCheck(name string, out schemas.HarnessStageOutput, recordSummary *string) (schemas.VerificationCheck, bool) {
	check := schemas.VerificationCheck{Name: name}
	if results, ok := out.Data["test_results"].(schemas.TestRunResults); ok {
		check.Kind = schemas.CheckKindTests
		check.Outcome, check.Detail = testsCheckOutcome(results)
		check.AlreadyFailing = alreadyFailingNames(results)
	} else if results, ok := out.Data["acceptance_results"].([]schemas.TestCaseResult); ok {
		check.Kind = schemas.CheckKindAcceptance
		check.Outcome, check.Detail = acceptanceCheckOutcome(results)
	} else if report, ok := verificationReport(out); ok {
		check.Kind = reportCheckKind(out)
		check.Outcome, check.Detail = reportCheckOutcome(report, recordSummary)
	} else {
		return schemas.VerificationCheck{}, false
	}
	if check.Name == "" {
		check.Name = string(check.Kind)
	}
	check.Detail = boundCheckDetail(check.Detail)
	return check, true
}

func reportCheckKind(out schemas.HarnessStageOutput) schemas.VerificationCheckKind {
	if _, ok := out.Data["security_auditor_output"]; ok {
		return schemas.CheckKindSecurityScan
	}
	if _, ok := out.Data["static_analyzer_output"]; ok {
		return schemas.CheckKindStaticAnalysis
	}
	return schemas.CheckKindCustom
}

// testsCheckOutcome maps a test run with the same precedence as passOutcome:
// a failed test first, then a passed test, then a command that passed with
// no per-test evidence, else no test ran.
func testsCheckOutcome(results schemas.TestRunResults) (schemas.VerificationCheckOutcome, string) {
	passed, failed := results.Passed(), results.Failed()
	counts := fmt.Sprintf("%d passed, %d failed", passed, failed)
	if results.Evidence != "" {
		counts += ", " + strings.ReplaceAll(results.Evidence, "_", "-") + " evidence"
	}
	if before := results.FailedBefore(); before > 0 {
		counts += fmt.Sprintf(", %d already failing before this run", before)
	}
	switch {
	case failed > 0:
		return schemas.CheckFailed, counts
	case passed > 0:
		return schemas.CheckPassed, counts
	case results.Evidence == schemas.TestEvidenceCommandOnly && results.ExitCode == 0:
		return schemas.CheckCommandPassed, "exit code 0, per-test evidence unavailable"
	default:
		if results.FailedBefore() > 0 {
			return schemas.CheckNoTests, counts
		}
		return schemas.CheckNoTests, "no tests ran"
	}
}

// alreadyFailingNames returns the names of the tests that failed before the
// run, in report order.
func alreadyFailingNames(results schemas.TestRunResults) []string {
	var names []string
	for _, tc := range results.Tests {
		if tc.AlreadyFailing {
			names = append(names, tc.Name)
		}
	}
	return names
}

func acceptanceCheckOutcome(results []schemas.TestCaseResult) (schemas.VerificationCheckOutcome, string) {
	passed, failed, skipped := 0, 0, 0
	for _, result := range results {
		switch result.Status {
		case "passed":
			passed++
		case "failed", "errored":
			failed++
		default:
			skipped++
		}
	}
	detail := fmt.Sprintf("%d passed, %d failed, %d skipped", passed, failed, skipped)
	if len(results) == 0 {
		detail = "no acceptance facts"
	}
	switch {
	case failed > 0:
		return schemas.CheckFailed, detail
	case passed > 0:
		return schemas.CheckPassed, detail
	default:
		return schemas.CheckSkipped, detail
	}
}

func reportCheckOutcome(report schemas.VerificationReport, recordSummary *string) (schemas.VerificationCheckOutcome, string) {
	var ran, missing []string
	for _, tool := range report.Tools {
		switch tool.Status {
		case schemas.VerificationPassed, schemas.VerificationFindings:
			ran = append(ran, tool.Tool)
		case schemas.VerificationIncomplete:
			missing = append(missing, tool.Tool)
		}
	}
	switch report.Status {
	case schemas.VerificationPassed:
		return schemas.CheckPassed, strings.Join(ran, ", ")
	case schemas.VerificationFindings:
		return schemas.CheckFindings, joinDetail(strings.Join(ran, ", "), pluralDetail(len(report.Findings), "finding"))
	case schemas.VerificationIncomplete:
		return schemas.CheckIncomplete, joinDetail(strings.Join(ran, ", "), "did not run: "+strings.Join(missing, ", "))
	default:
		if recordSummary != nil && strings.TrimSpace(*recordSummary) != "" {
			return schemas.CheckNotApplicable, strings.TrimSpace(*recordSummary)
		}
		return schemas.CheckNotApplicable, "no check applied"
	}
}

// recordCheck builds the check of a verification stage that left no
// verification output. A stage that does not produce verification has none.
func recordCheck(plan schemas.ExecutionPlan, record schemas.StageRecord) (schemas.VerificationCheck, bool) {
	kind, ok := planCheckKind(plan, record.Name)
	if !ok {
		return schemas.VerificationCheck{}, false
	}
	check := schemas.VerificationCheck{Name: record.Name, Kind: kind, Detail: boundCheckDetail(strings.TrimSpace(DerefString(record.OutputSummary)))}
	switch record.Status {
	case schemas.StageSkipped:
		check.Outcome = schemas.CheckSkipped
	case schemas.StageIncomplete:
		check.Outcome = schemas.CheckIncomplete
	case schemas.StageFailed:
		check.Outcome = schemas.CheckFailed
	case schemas.StageCompleted:
		// A verification node that completed with no report verified nothing.
		check.Outcome = schemas.CheckNotApplicable
		check.Detail = "no verification report"
	default:
		return schemas.VerificationCheck{}, false
	}
	return check, true
}

// planCheckKind returns the check kind of a planned stage that produces
// verification. A compiled stage carries its capabilities; a legacy plan
// falls back to the builtin profile, as planHasVerification does.
func planCheckKind(plan schemas.ExecutionPlan, name string) (schemas.VerificationCheckKind, bool) {
	stage, ok := stageByPlanName(plan, name)
	if !ok {
		return "", false
	}
	if stage.Caps != nil {
		if !stage.Caps.ProducesVerification {
			return "", false
		}
	} else if caps, ok := stageCapabilities(stage); !ok || !caps.ProducesVerification {
		return "", false
	}
	role := stage.Type
	if role == "" {
		role = stage.Name
	}
	switch role {
	case "test_runner":
		return schemas.CheckKindTests, true
	case "acceptance_verifier":
		return schemas.CheckKindAcceptance, true
	case "security_auditor":
		return schemas.CheckKindSecurityScan, true
	case "static_analyzer":
		return schemas.CheckKindStaticAnalysis, true
	default:
		return schemas.CheckKindCustom, true
	}
}

func joinDetail(first, second string) string {
	if first == "" {
		return second
	}
	return first + "; " + second
}

func pluralDetail(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// boundCheckDetail keeps a detail inside the schema bound (200 runes).
func boundCheckDetail(detail string) string {
	const limit = 200
	runes := []rune(detail)
	if len(runes) <= limit {
		return detail
	}
	return string(runes[:limit-3]) + "..."
}
