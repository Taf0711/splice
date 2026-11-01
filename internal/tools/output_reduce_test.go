package tools

import (
	"strings"
	"testing"
)

// realGoTestVerbose is captured verbatim from `go test -v ./...` on a fixture
// package that exercises the shapes a reducer must survive:
//
//   - plain passing tests, including one that prints output between RUN and PASS
//   - a failing parent with one passing and one failing subtest (indented)
//   - a skipped test
//   - a parallel test (=== PAUSE / === CONT)
//   - a passing test that PRINTS a line shaped like a real result line
//   - a table test whose subtests reuse names and partially fail
//
// The adversarial line is intentional: "--- PASS: NotARealTest (0.00s)" has no
// matching "=== RUN NotARealTest", so a correct reducer must keep it and the
// detail line under it.
func realGoTestVerbose() string {
	return strings.Join([]string{
		"=== RUN   TestPassOne",
		"--- PASS: TestPassOne (0.00s)",
		"=== RUN   TestPassTwo",
		"side output from a passing test",
		"--- PASS: TestPassTwo (0.00s)",
		"=== RUN   TestWithSubs",
		"=== RUN   TestWithSubs/good",
		"=== RUN   TestWithSubs/bad",
		"    thing_test.go:13: got 3, want 4",
		"--- FAIL: TestWithSubs (0.00s)",
		"    --- PASS: TestWithSubs/good (0.00s)",
		"    --- FAIL: TestWithSubs/bad (0.00s)",
		"=== RUN   TestSkipped",
		"    thing_test.go:16: needs network",
		"--- SKIP: TestSkipped (0.00s)",
		"=== RUN   TestParallel",
		"=== PAUSE TestParallel",
		"=== RUN   TestLogsFakePassMarker",
		"--- PASS: NotARealTest (0.00s)",
		"    fake detail line that must survive",
		"--- PASS: TestLogsFakePassMarker (0.00s)",
		"=== RUN   TestTable",
		"=== RUN   TestTable/a",
		"=== RUN   TestTable/b",
		"    thing_test.go:31: table case b failed",
		"--- FAIL: TestTable (0.00s)",
		"    --- PASS: TestTable/a (0.00s)",
		"    --- FAIL: TestTable/b (0.00s)",
		"=== CONT  TestParallel",
		"--- PASS: TestParallel (0.00s)",
		"FAIL",
		"FAIL\tgtfixture\t0.340s",
		"FAIL",
	}, "\n")
}

// TestReduceGoTestVerboseRealOutputGolden pins the exact reduced form of real
// `go test -v` output. It is the strongest single test here: it fails on any
// change to which lines are removed.
func TestReduceGoTestVerboseRealOutputGolden(t *testing.T) {
	got, ok := reduceGoTestVerbose(realGoTestVerbose())
	if !ok {
		t.Fatalf("reducer did not fire on real go test verbose output")
	}
	want := strings.Join([]string{
		"side output from a passing test",
		"=== RUN   TestWithSubs",
		"=== RUN   TestWithSubs/bad",
		"    thing_test.go:13: got 3, want 4",
		"--- FAIL: TestWithSubs (0.00s)",
		"    --- FAIL: TestWithSubs/bad (0.00s)",
		"=== RUN   TestSkipped",
		"    thing_test.go:16: needs network",
		"--- SKIP: TestSkipped (0.00s)",
		"=== PAUSE TestParallel",
		"--- PASS: NotARealTest (0.00s)",
		"    fake detail line that must survive",
		"=== RUN   TestTable",
		"=== RUN   TestTable/b",
		"    thing_test.go:31: table case b failed",
		"--- FAIL: TestTable (0.00s)",
		"    --- FAIL: TestTable/b (0.00s)",
		"=== CONT  TestParallel",
		"FAIL",
		"FAIL\tgtfixture\t0.340s",
		"FAIL",
	}, "\n")
	if got != want {
		t.Fatalf("reduced output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestReduceGoTestVerboseRealOutputPreservesEveryFailureSignal asserts the
// invariant directly on the real log: no failure, skip, pause, or summary is
// ever removed, whatever else the reducer does.
func TestReduceGoTestVerboseRealOutputPreservesEveryFailureSignal(t *testing.T) {
	got, ok := reduceGoTestVerbose(realGoTestVerbose())
	if !ok {
		t.Fatalf("reducer did not fire")
	}
	for _, needle := range []string{
		"--- FAIL: TestWithSubs (0.00s)",
		"    --- FAIL: TestWithSubs/bad (0.00s)",
		"--- FAIL: TestTable (0.00s)",
		"    --- FAIL: TestTable/b (0.00s)",
		"    thing_test.go:13: got 3, want 4",
		"    thing_test.go:31: table case b failed",
		"--- SKIP: TestSkipped (0.00s)",
		"    thing_test.go:16: needs network",
		"=== PAUSE TestParallel",
		"=== CONT  TestParallel",
		"FAIL\tgtfixture\t0.340s",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("signal %q was dropped", needle)
		}
	}
	if n := strings.Count(got, "--- FAIL"); n != 4 {
		t.Fatalf("FAIL lines = %d, want 4", n)
	}
}

// TestReduceGoTestVerboseKeepsUnmatchedPassLine is the adversarial guard. A
// passing test printed "--- PASS: NotARealTest (0.00s)" with no matching run
// line. Deleting it (and the detail line under it) would destroy real test
// output, so the reducer must keep both.
func TestReduceGoTestVerboseKeepsUnmatchedPassLine(t *testing.T) {
	got, ok := reduceGoTestVerbose(realGoTestVerbose())
	if !ok {
		t.Fatalf("reducer did not fire")
	}
	if !strings.Contains(got, "--- PASS: NotARealTest (0.00s)") {
		t.Fatalf("an unmatched pass-shaped line printed by a test was removed:\n%s", got)
	}
	if !strings.Contains(got, "fake detail line that must survive") {
		t.Fatalf("detail under an unmatched pass-shaped line was removed:\n%s", got)
	}
	// The test's own real result line has a matching run line and must go.
	if strings.Contains(got, "--- PASS: TestLogsFakePassMarker") {
		t.Fatalf("real pass result survived:\n%s", got)
	}
}

// TestReduceGoTestVerboseRemovesRunWithItsPass pins that no orphan "=== RUN"
// line is left behind for a passing test.
func TestReduceGoTestVerboseRemovesRunWithItsPass(t *testing.T) {
	got, ok := reduceGoTestVerbose(realGoTestVerbose())
	if !ok {
		t.Fatalf("reducer did not fire")
	}
	for _, orphan := range []string{
		"=== RUN   TestPassOne",
		"=== RUN   TestPassTwo",
		"=== RUN   TestWithSubs/good",
		"=== RUN   TestLogsFakePassMarker",
		"=== RUN   TestTable/a",
		"=== RUN   TestParallel",
	} {
		if strings.Contains(got, orphan) {
			t.Fatalf("orphan run line %q survived", orphan)
		}
	}
	// Run lines for tests that did not pass must stay.
	for _, keep := range []string{
		"=== RUN   TestWithSubs",
		"=== RUN   TestWithSubs/bad",
		"=== RUN   TestSkipped",
		"=== RUN   TestTable",
		"=== RUN   TestTable/b",
	} {
		if !strings.Contains(got, keep) {
			t.Fatalf("run line %q for a non-passing test was dropped", keep)
		}
	}
}

// TestReduceGoTestVerboseNestedFailSurvivesPassParent targets the detail-scan
// boundary: an indented FAIL directly after an indented PASS must not be
// swallowed as detail.
func TestReduceGoTestVerboseNestedFailSurvivesPassParent(t *testing.T) {
	in := strings.Join([]string{
		"=== RUN   TestA", "--- PASS: TestA (0.00s)",
		"=== RUN   TestB", "--- PASS: TestB (0.00s)",
		"=== RUN   TestC", "--- PASS: TestC (0.00s)",
		"=== RUN   TestParent",
		"=== RUN   TestParent/ok",
		"=== RUN   TestParent/bad",
		"    --- PASS: TestParent/ok (0.00s)",
		"    --- FAIL: TestParent/bad (0.00s)",
		"    nested_test.go:9: exploded",
		"FAIL",
		"FAIL\tpkg\t0.1s",
	}, "\n")
	got, ok := reduceGoTestVerbose(in)
	if !ok {
		t.Fatalf("reducer did not fire")
	}
	if !strings.Contains(got, "--- FAIL: TestParent/bad (0.00s)") {
		t.Fatalf("nested FAIL was swallowed by the preceding PASS detail scan:\n%s", got)
	}
	if !strings.Contains(got, "nested_test.go:9: exploded") {
		t.Fatalf("nested failure detail was dropped:\n%s", got)
	}
	if strings.Contains(got, "--- PASS: TestParent/ok") {
		t.Fatalf("nested PASS survived:\n%s", got)
	}
}

// TestReduceGoTestVerboseSubsequenceOrder pins that the reducer only removes
// lines. It never reorders, rewrites, or duplicates one.
func TestReduceGoTestVerboseSubsequenceOrder(t *testing.T) {
	in := strings.Split(realGoTestVerbose(), "\n")
	got, ok := reduceGoTestVerbose(strings.Join(in, "\n"))
	if !ok {
		t.Fatalf("reducer did not fire")
	}
	out := strings.Split(got, "\n")
	i := 0
	for _, line := range out {
		found := false
		for i < len(in) {
			if in[i] == line {
				found = true
				i++
				break
			}
			i++
		}
		if !found {
			t.Fatalf("output line %q is not an in-order subsequence of the input", line)
		}
	}
}

// TestReduceGoTestVerboseIsIdempotent pins that a second pass reports no change,
// which the registry relies on when output is reduced more than once.
func TestReduceGoTestVerboseIsIdempotent(t *testing.T) {
	once, ok := reduceGoTestVerbose(realGoTestVerbose())
	if !ok {
		t.Fatalf("first pass did not fire")
	}
	twice, ok := reduceGoTestVerbose(once)
	if ok {
		t.Fatalf("second pass fired on already-reduced output")
	}
	if twice != once {
		t.Fatalf("second pass changed the text")
	}
}

// TestReduceGoTestVerboseRefusesWhenNotApplicable pins the scoping guards. None
// of these shapes may be rewritten.
func TestReduceGoTestVerboseRefusesWhenNotApplicable(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "compile error", text: strings.Join([]string{
			"# github.com/x/pkg",
			"./main.go:12:2: undefined: foo",
			"FAIL\tgithub.com/x/pkg [build failed]",
		}, "\n")},
		{name: "benchmarks only", text: strings.Join([]string{
			"goos: darwin",
			"BenchmarkFoo-8   \t 1000\t  1234 ns/op",
			"PASS",
			"ok  \tpkg\t0.4s",
		}, "\n")},
		{name: "below threshold", text: strings.Join([]string{
			"=== RUN   TestA", "--- PASS: TestA (0.00s)",
			"=== RUN   TestB", "--- PASS: TestB (0.00s)",
			"PASS", "ok  pkg  0.1s",
		}, "\n")},
		{name: "pass without run line", text: strings.Join([]string{
			"--- PASS: TestGhost (0.00s)",
			"--- PASS: TestGhost2 (0.00s)",
			"--- PASS: TestGhost3 (0.00s)",
			"ok  pkg  0.1s",
		}, "\n")},
		{name: "wrong result shape", text: strings.Join([]string{
			"=== RUN   TestA", "--- PASS: TestA without a duration",
			"=== RUN   TestB", "--- PASS: TestB without a duration",
			"=== RUN   TestC", "--- PASS: TestC without a duration",
		}, "\n")},
		{name: "empty", text: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := reduceGoTestVerbose(tc.text)
			if ok {
				t.Fatalf("reducer fired when it should refuse:\n%s", got)
			}
			if got != tc.text {
				t.Fatalf("refused reducer changed text:\n%q", got)
			}
		})
	}
}

// TestReduceGoTestVerboseRefusesWhenNothingWouldRemain pins the empty-result
// guard: a reduction that removes every line would read as a failed tool call.
func TestReduceGoTestVerboseRefusesWhenNothingWouldRemain(t *testing.T) {
	in := strings.Join([]string{
		"=== RUN   TestRepeat", "--- PASS: TestRepeat (0.00s)",
		"=== RUN   TestRepeat", "--- PASS: TestRepeat (0.00s)",
		"=== RUN   TestRepeat", "--- PASS: TestRepeat (0.00s)",
	}, "\n")
	got, ok := reduceGoTestVerbose(in)
	if ok {
		t.Fatalf("reducer emptied the output instead of refusing:\n%q", got)
	}
	if got != in {
		t.Fatalf("refused reducer changed text")
	}
}

// TestReduceGoTestVerboseRepeatedNamesConsumeInOrder pins the queue: repeated
// test names pair one run line with one pass line, in order.
func TestReduceGoTestVerboseRepeatedNamesConsumeInOrder(t *testing.T) {
	in := strings.Join([]string{
		"=== RUN   TestRepeat", "--- PASS: TestRepeat (0.00s)",
		"=== RUN   TestRepeat", "--- PASS: TestRepeat (0.00s)",
		"=== RUN   TestRepeat", "--- PASS: TestRepeat (0.00s)",
		"PASS", "ok  pkg  0.1s",
	}, "\n")
	got, ok := reduceGoTestVerbose(in)
	if !ok {
		t.Fatalf("reducer did not fire")
	}
	if strings.Contains(got, "=== RUN   TestRepeat") || strings.Contains(got, "--- PASS: TestRepeat") {
		t.Fatalf("repeated pairs not fully consumed:\n%s", got)
	}
	if !strings.Contains(got, "ok  pkg  0.1s") {
		t.Fatalf("summary line was dropped:\n%s", got)
	}
}

// TestReduceGoTestVerboseHandlesCRLF pins that Windows line endings parse the
// same as Unix ones and that the carriage returns survive in kept lines.
func TestReduceGoTestVerboseHandlesCRLF(t *testing.T) {
	in := strings.ReplaceAll(realGoTestVerbose(), "\n", "\r\n")
	got, ok := reduceGoTestVerbose(in)
	if !ok {
		t.Fatalf("reducer did not fire on CRLF input")
	}
	if !strings.Contains(got, "--- FAIL: TestWithSubs (0.00s)") {
		t.Fatalf("failure signal lost on CRLF input:\n%s", got)
	}
	if strings.Contains(got, "--- PASS: TestPassOne") {
		t.Fatalf("pass result survived on CRLF input:\n%s", got)
	}
	if !strings.Contains(got, "\r\n") {
		t.Fatalf("carriage returns were not preserved in kept lines")
	}
}

// TestReduceGoTestVerbosePreservesMissingTrailingNewline pins that the split and
// join round-trips an output that does not end in a newline.
func TestReduceGoTestVerbosePreservesMissingTrailingNewline(t *testing.T) {
	got, ok := reduceGoTestVerbose(realGoTestVerbose())
	if !ok {
		t.Fatalf("reducer did not fire")
	}
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("reducer introduced a trailing newline")
	}
}

// TestReduceNativeOutputNoop pins the entry point on text no native reducer
// recognises.
func TestReduceNativeOutputNoop(t *testing.T) {
	text := "plain output\nwith lines\n"
	got, ok := reduceNativeOutput(text)
	if ok || got != text {
		t.Fatalf("native reducer changed unrelated text: %q", got)
	}
}

// TestReduceGoTestVerboseShrinksRealOutput records the measured payoff so a
// regression in removal rate is visible.
func TestReduceGoTestVerboseShrinksRealOutput(t *testing.T) {
	in := realGoTestVerbose()
	got, ok := reduceGoTestVerbose(in)
	if !ok {
		t.Fatalf("reducer did not fire")
	}
	inLines := strings.Count(in, "\n") + 1
	outLines := strings.Count(got, "\n") + 1
	if outLines >= inLines {
		t.Fatalf("no lines removed: in=%d out=%d", inLines, outLines)
	}
	if outLines > 21 {
		t.Fatalf("removed fewer lines than expected: in=%d out=%d want<=21", inLines, outLines)
	}
	t.Logf("real go test -v: %d lines -> %d lines (%d removed)", inLines, outLines, inLines-outLines)
}
