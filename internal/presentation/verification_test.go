package presentation

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func testsCheckWithNames(outcome schemas.VerificationCheckOutcome, names ...string) schemas.VerificationCheck {
	return schemas.VerificationCheck{
		Name: "test_runner", Kind: schemas.CheckKindTests, Outcome: outcome,
		Detail:         "34 passed, 0 failed, per-test evidence, 12 already failing before this run",
		AlreadyFailing: names,
	}
}

// A verified run names the tests that failed before it. The list is capped at
// ten names, and the rest are counted.
func TestVerifiedLinesNamePreexistingFailures(t *testing.T) {
	var names []string
	for i := 0; i < 11; i++ {
		names = append(names, "pkg.TestOld"+string(rune('A'+i)))
	}
	lines := VerifiedLines([]schemas.VerificationCheck{testsCheckWithNames(schemas.CheckPassed, names...)})
	joined := strings.Join(lines, "\n")
	want := "Already failing before this run (not caused by it): pkg.TestOldA, pkg.TestOldB, pkg.TestOldC, pkg.TestOldD, pkg.TestOldE, pkg.TestOldF, pkg.TestOldG, pkg.TestOldH, pkg.TestOldI, pkg.TestOldJ, and 1 more."
	if !strings.Contains(joined, want) {
		t.Fatalf("verified lines = %q, want a line %q", joined, want)
	}
	if !strings.Contains(joined, "already failing before this run") {
		t.Fatalf("the verified line must carry the count: %q", joined)
	}
}

// A run with no pre-existing failure prints no such line.
func TestVerifiedLinesOmitAlreadyFailingWhenNone(t *testing.T) {
	check := testsCheckWithNames(schemas.CheckPassed)
	check.Detail = "34 passed, 0 failed, per-test evidence"
	joined := strings.Join(VerifiedLines([]schemas.VerificationCheck{check}), "\n")
	if strings.Contains(joined, "Already failing") {
		t.Fatalf("no pre-existing failure, but the lines say: %q", joined)
	}
}

// An unverified run still names the pre-existing failures next to the
// new ones.
func TestCheckLinesNamePreexistingFailures(t *testing.T) {
	lines := CheckLines([]schemas.VerificationCheck{testsCheckWithNames(schemas.CheckFailed, "pkg.TestOld")})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Already failing before this run (not caused by it): pkg.TestOld.") {
		t.Fatalf("check lines = %q, want the pre-existing names", joined)
	}
}
