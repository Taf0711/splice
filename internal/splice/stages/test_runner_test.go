package stages

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// A typecheck exit code was previously being reported as a test result.
func TestTestCommandPrefersPackageTestOverTypecheck(t *testing.T) {
	workDir := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(workDir, "package.json"), `{"scripts":{"typecheck":"tsc --noEmit","test":"npm test"}}`)

	got, err := testCommand(workDir, "javascript")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	want := []string{"npm", "run", "test"}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("test command = %#v, want %#v", got, want)
	}
}

// Non-test checks must not be selected as a test command.
func TestTestCommandReturnsNoneForNonTestChecks(t *testing.T) {
	workDir := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(workDir, "package.json"), `{"scripts":{"typecheck":"tsc --noEmit","build":"npm run compile","lint":"eslint ."}}`)

	got, err := testCommand(workDir, "javascript")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("test command = %#v, want no command", got)
	}
}

// A non-Go workspace with no checks must not inherit the Go test default.
func TestTestCommandDoesNotDefaultNonGoWorkspaceToGo(t *testing.T) {
	workDir := t.TempDir()

	got, err := testCommand(workDir, "javascript")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("test command = %#v, want no command", got)
	}
}

// An undetectable non-Go workspace must report skipped verification, not failure.
func TestTestRunnerReportsSkippedWhenNoCommandDetected(t *testing.T) {
	workDir := t.TempDir()
	output, err := (TestRunner{}).Run(context.Background(), newHarnessInput("verify workspace"), &fakeProvider{}, StageOptions{
		WorkDir:  workDir,
		Language: "javascript",
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if !strings.Contains(output.Detail, "verification could not run") {
		t.Fatalf("detail = %q, want honest verification limitation", output.Detail)
	}
	results, ok := output.Data["test_results"].(schemas.TestRunResults)
	if !ok {
		t.Fatalf("test_results = %#v, want schemas.TestRunResults", output.Data["test_results"])
	}
	if len(results.Tests) != 1 {
		t.Fatalf("test results = %#v, want one suite result", results.Tests)
	}
	if results.Tests[0].Status != "skipped" {
		t.Fatalf("suite status = %q, want skipped", results.Tests[0].Status)
	}
}

// A Go workspace with go.mod must keep resolving the Go test command.
func TestTestCommandUsesGoTestsForGoModule(t *testing.T) {
	workDir := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(workDir, "go.mod"), "module example.com/testfixture\n\ngo 1.25\n")

	got, err := testCommand(workDir, "go")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	want := []string{"go", "test", "./..."}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("test command = %#v, want %#v", got, want)
	}
}

// A Go language hint without go.mod must not resolve the Go test command.
func TestTestCommandDoesNotUseGoTestsWithoutGoModule(t *testing.T) {
	workDir := t.TempDir()

	got, err := testCommand(workDir, "go")
	if err != nil {
		t.Fatalf("testCommand returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("test command = %#v, want no command", got)
	}
}

func writeTestRunnerFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func equalTestRunnerStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// A1: a seeded go test -json stream with one passing and one failing test
// must parse into per-test entries, not a single suite entry.
func TestParseGoTestJSONPerTestResults(t *testing.T) {
	output := strings.Join([]string{
		`{"Action":"start","Package":"example.com/fixture"}`,
		`{"Action":"run","Package":"example.com/fixture","Test":"TestPasses"}`,
		`{"Action":"output","Package":"example.com/fixture","Test":"TestPasses","Output":"=== RUN   TestPasses\n"}`,
		`{"Action":"output","Package":"example.com/fixture","Test":"TestPasses","Output":"--- PASS: TestPasses (0.00s)\n"}`,
		`{"Action":"pass","Package":"example.com/fixture","Test":"TestPasses","Elapsed":0}`,
		`{"Action":"run","Package":"example.com/fixture","Test":"TestFails"}`,
		`{"Action":"output","Package":"example.com/fixture","Test":"TestFails","Output":"    math_test.go:13: got 3, want 4\n"}`,
		`{"Action":"output","Package":"example.com/fixture","Test":"TestFails","Output":"--- FAIL: TestFails (0.00s)\n"}`,
		`{"Action":"fail","Package":"example.com/fixture","Test":"TestFails","Elapsed":0}`,
		`{"Action":"output","Package":"example.com/fixture","Output":"FAIL\n"}`,
		`{"Action":"fail","Package":"example.com/fixture","Elapsed":0.284}`,
	}, "\n")

	parsed, ok := parseGoTestJSON(output)
	if !ok {
		t.Fatal("valid go test -json output must parse")
	}
	if len(parsed.Tests) != 2 {
		t.Fatalf("tests = %#v, want exactly 2 per-test entries", parsed.Tests)
	}
	byName := map[string]schemas.TestCaseResult{}
	for _, tc := range parsed.Tests {
		byName[tc.Name] = tc
	}
	pass, ok := byName["TestPasses"]
	if !ok || pass.Status != "passed" {
		t.Fatalf("TestPasses entry = %#v, want passed", pass)
	}
	fail, ok := byName["TestFails"]
	if !ok || fail.Status != "failed" {
		t.Fatalf("TestFails entry = %#v, want failed", fail)
	}
	if !strings.Contains(fail.Message, "got 3, want 4") {
		t.Fatalf("TestFails message = %q, want the assertion text", fail.Message)
	}
}

// A1: build failures in go test -json output must become one errored entry
// per compiler-error block, and a non-JSON stream must report !ok so the
// caller keeps its suite fallback.
func TestParseGoTestJSONCompileErrorsAndFallback(t *testing.T) {
	output := strings.Join([]string{
		`{"ImportPath":"example.com/fixture [example.com/fixture.test]","Action":"build-output","Output":"# example.com/fixture [example.com/fixture.test]\n"}`,
		`{"ImportPath":"example.com/fixture [example.com/fixture.test]","Action":"build-output","Output":"./missing.go:3:9: undefined: NotAMethod\n"}`,
		`{"ImportPath":"example.com/fixture [example.com/fixture.test]","Action":"build-output","Output":"./broken_test.go:9:2: undefined: undefinedSymbol\n"}`,
		`{"Action":"build-fail"}`,
		`{"Time":"2026-09-04T12:00:00Z","Action":"start","Package":"example.com/fixture"}`,
		`{"Action":"fail","Package":"example.com/fixture","Elapsed":0,"FailedBuild":"example.com/fixture [example.com/fixture.test]"}`,
	}, "\n")

	parsed, ok := parseGoTestJSON(output)
	if !ok {
		t.Fatal("build-failure JSON output must parse")
	}
	if len(parsed.Tests) != 1 {
		t.Fatalf("tests = %#v, want one compile-error entry group", parsed.Tests)
	}
	entry := parsed.Tests[0]
	if !strings.HasPrefix(entry.Name, "build ") {
		t.Fatalf("entry name = %q, want a build-prefixed errored entry", entry.Name)
	}
	if entry.Status != "errored" {
		t.Fatalf("entry status = %q, want errored", entry.Status)
	}
	for _, want := range []string{"undefined: NotAMethod", "undefined: undefinedSymbol"} {
		if !strings.Contains(entry.Message, want) {
			t.Fatalf("entry message = %q, want %q", entry.Message, want)
		}
	}

	if _, ok := parseGoTestJSON("FAIL	example.com/fixture	0.5s\nexit status 1\n"); ok {
		t.Fatal("plain non-JSON output must report !ok so the suite fallback applies")
	}
}

// A1: the runner rewrites a plain `go test` command to carry -json.
func TestWithGoTestJSONAddsFlag(t *testing.T) {
	got := withGoTestJSON([]string{"go", "test", "./..."})
	want := []string{"go", "test", "-json", "./..."}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
	// Already present: unchanged.
	already := []string{"go", "test", "-json", "./..."}
	if !equalTestRunnerStrings(withGoTestJSON(already), already) {
		t.Fatal("a command with -json must pass through unchanged")
	}
	// Non-go commands pass through untouched.
	py := []string{"python", "-m", "pytest"}
	if !equalTestRunnerStrings(withGoTestJSON(py), py) {
		t.Fatal("non-go commands must pass through untouched")
	}
}

// A1: the full stage run against a REAL fixture package (passing + failing
// test) yields per-test entries through the stage output payload. Requires
// the native sandbox backend, matching the deterministic stage profile.
func TestTestRunnerGoJSONParsesRealFixture(t *testing.T) {
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	workDir := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(workDir, "go.mod"), "module example.com/testfixture\n\ngo 1.25\n")
	writeTestRunnerFixture(t, filepath.Join(workDir, "math.go"), "package testfixture\n\nfunc Add(a, b int) int { return a + b }\n")
	writeTestRunnerFixture(t, filepath.Join(workDir, "math_test.go"), `package testfixture

import "testing"

func TestPasses(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad add")
	}
}

func TestFails(t *testing.T) {
	if Add(1, 2) != 4 {
		t.Fatalf("got %d, want 4", Add(1, 2))
	}
}
`)

	output, err := (TestRunner{}).Run(context.Background(), newHarnessInput("run tests"), &fakeProvider{}, StageOptions{
		Command:        []string{"go", "test", "./..."},
		TimeoutSeconds: 120,
		WorkDir:        workDir,
		Sandbox:        procrun.NewStageEngine(workDir),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	results, ok := output.Data["test_results"].(schemas.TestRunResults)
	if !ok {
		t.Fatalf("test_results = %#v, want schemas.TestRunResults", output.Data["test_results"])
	}
	byName := map[string]schemas.TestCaseResult{}
	for _, tc := range results.Tests {
		byName[tc.Name] = tc
	}
	pass, hasPass := byName["TestPasses"]
	fail, hasFail := byName["TestFails"]
	if !hasPass || pass.Status != "passed" {
		t.Fatalf("TestPasses = %#v, want a passing per-test entry", pass)
	}
	if !hasFail || fail.Status != "failed" {
		t.Fatalf("TestFails = %#v, want a failing per-test entry", fail)
	}
	if !strings.Contains(fail.Message, "want 4") {
		t.Fatalf("TestFails message = %q, want assertion text", fail.Message)
	}
	if results.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", results.ExitCode)
	}
}

// A1: a compile-error fixture yields build-prefixed errored entries whose
// messages carry the undefined symbols, plus the excerpt in the summary.
func TestTestRunnerGoJSONCompileErrorsReachEvidence(t *testing.T) {
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	workDir := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(workDir, "go.mod"), "module example.com/testfixture\n\ngo 1.25\n")
	writeTestRunnerFixture(t, filepath.Join(workDir, "main.go"), "package testfixture\n\nvar _ = NotDefined.Anywhere()\n")

	output, err := (TestRunner{}).Run(context.Background(), newHarnessInput("run tests"), &fakeProvider{}, StageOptions{
		Command:        []string{"go", "test", "./..."},
		TimeoutSeconds: 120,
		WorkDir:        workDir,
		Sandbox:        procrun.NewStageEngine(workDir),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	results, ok := output.Data["test_results"].(schemas.TestRunResults)
	if !ok {
		t.Fatalf("test_results = %#v, want schemas.TestRunResults", output.Data["test_results"])
	}
	if len(results.Tests) == 0 {
		t.Fatalf("tests = %#v, want at least one compile-error entry", results.Tests)
	}
	joined := ""
	for _, tc := range results.Tests {
		if tc.Status != "errored" {
			t.Fatalf("entry %q status = %q, want errored", tc.Name, tc.Status)
		}
		joined += tc.Name + " " + tc.Message + "\n"
	}
	if !strings.Contains(joined, "undefined: NotDefined") {
		t.Fatalf("compile-error entries lost the undefined symbol:\n%s", joined)
	}
	if !strings.Contains(output.Summary, "undefined: NotDefined") {
		t.Fatalf("summary lacks the compile-error evidence:\n%s", output.Summary)
	}
}
