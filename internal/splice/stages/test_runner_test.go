package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
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

func TestExpandConfiguredTestCommand(t *testing.T) {
	root := t.TempDir()
	testDir := filepath.Join(root, "tests")
	writeExpansionFixtures(t, root, "tests/test_auth.py", "tests/unit/test_more.py")
	command := []string{"python", "tests/runtests.py", "{tests}", "{test_modules}"}
	changed := map[string][]string{
		"test_generator": {"tests/test_auth.py", "tests/unit/test_more.py", "src/not_a_test.py"},
		"code_writer":    {"tests.py", "internal/foo_test.go", "web/app.spec.ts"},
	}
	got, err := expandConfiguredTestCommand(command, changed, root, testDir)
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	want := []string{"python", "tests/runtests.py", "test_auth.py", "unit/test_more.py", "test_auth", "unit.test_more"}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("expanded command = %#v, want %#v", got, want)
	}
}

// writeExpansionFixtures creates the named files under root, so the expansion
// sees them as present.
func writeExpansionFixtures(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("def test_x():\n    pass\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// A changed test file that was later removed, by a rollback or a delete, must
// not reach the command. pytest would exit 4 on a missing path.
func TestExpandConfiguredTestCommandSkipsMissingFiles(t *testing.T) {
	root := t.TempDir()
	writeExpansionFixtures(t, root, "tests/test_kept.py")
	got, err := expandConfiguredTestCommand([]string{"python", "{tests}"}, map[string][]string{
		"code_writer": {"tests/test_kept.py", "tests/test_gone.py", "tests/test_gone2.py"},
	}, root, filepath.Join(root, "tests"))
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	if !equalTestRunnerStrings(got, []string{"python", "test_kept.py"}) {
		t.Fatalf("expanded command = %#v, want only the existing test file", got)
	}
}

func TestExpandConfiguredTestCommandEmptySetAndOutsideDir(t *testing.T) {
	root := t.TempDir()
	got, err := expandConfiguredTestCommand([]string{"pytest", "{tests}"}, map[string][]string{
		"code_writer": {"src/app.py", "other_test.go"},
	}, root, filepath.Join(root, "tests"))
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	if !equalTestRunnerStrings(got, []string{"pytest"}) {
		t.Fatalf("expanded empty command = %#v, want pytest only", got)
	}
}

func TestTestRunnerUsesConfiguredCommandAndWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	testDir := filepath.Join(root, "tests")
	writeExpansionFixtures(t, root, "tests/test_x.py")
	input := newHarnessInput("verify workspace")
	input.RunChangedFiles = map[string][]string{"code_writer": {"tests/test_x.py"}}
	var got map[string]any
	var toolName string
	output, err := (TestRunner{}).Run(context.Background(), input, &fakeProvider{}, StageOptions{
		WorkDir:        root,
		Command:        []string{"python", "{tests}"},
		CommandWorkDir: testDir,
		RunTool: func(_ context.Context, name string, args map[string]any) (ToolResult, error) {
			toolName = name
			got = args
			return ToolResult{OK: true, Output: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if got["cwd"] != testDir {
		t.Fatalf("cwd = %#v, want %q", got["cwd"], testDir)
	}
	if toolName != "bash" {
		t.Fatalf("tool = %q, want bash through the existing command path", toolName)
	}
	results := output.Data["test_results"].(schemas.TestRunResults)
	if !equalTestRunnerStrings(results.Command, []string{"python", "test_x.py"}) {
		t.Fatalf("recorded command = %#v, want the changed test file relative to the test dir", results.Command)
	}
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
	// Names are package-qualified (the event names its package).
	pass, ok := byName["example.com/fixture.TestPasses"]
	if !ok || pass.Status != "passed" {
		t.Fatalf("TestPasses entry = %#v, want passed", pass)
	}
	fail, ok := byName["example.com/fixture.TestFails"]
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
	pass, hasPass := byName["example.com/testfixture.TestPasses"]
	fail, hasFail := byName["example.com/testfixture.TestFails"]
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

// goTestRunResults runs TestRunner over a canned go test -json stream with the
// given exit code and returns the stage output and its TestRunResults.
func goTestRunResults(t *testing.T, stream string, exitCode string, ok bool) (schemas.HarnessStageOutput, schemas.TestRunResults) {
	t.Helper()
	out, err := (TestRunner{}).Run(context.Background(), newHarnessInput("verify workspace"), &fakeProvider{}, StageOptions{
		WorkDir: t.TempDir(),
		Command: []string{"go", "test", "./..."},
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: ok, Output: stream, Meta: map[string]string{"exit_code": exitCode}}, nil
		},
	})
	if err != nil {
		t.Fatalf("setup: stage run: %v", err)
	}
	results, found := out.Data["test_results"].(schemas.TestRunResults)
	if !found {
		t.Fatalf("setup: test_results = %#v, want schemas.TestRunResults", out.Data["test_results"])
	}
	return out, results
}

// A zero-exit go test run that executed no tests must not report a passed
// suite at full confidence. It records one skipped "no tests ran" entry.
func TestTestRunnerZeroGoTestsNotReportedAsPassed(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
	}{
		{
			name:   "no test files",
			stdout: "ok  \texample.com/m\t0.01s [no test files]\n",
		},
		{
			name: "package-level events only",
			stdout: `{"Action":"start","Package":"example.com/m"}` + "\n" +
				`{"Action":"output","Package":"example.com/m","Output":"testing: warning: no tests to run\n"}` + "\n" +
				`{"Action":"pass","Package":"example.com/m","Elapsed":0}` + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := (TestRunner{}).Run(context.Background(), newHarnessInput("verify workspace"), &fakeProvider{}, StageOptions{
				WorkDir: t.TempDir(),
				Command: []string{"go", "test", "./..."},
				RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
					return ToolResult{OK: true, Output: tc.stdout}, nil
				},
			})
			if err != nil {
				t.Fatalf("setup: stage run: %v", err)
			}
			results, ok := out.Data["test_results"].(schemas.TestRunResults)
			if !ok {
				t.Fatalf("setup: test_results = %#v, want schemas.TestRunResults", out.Data["test_results"])
			}
			for _, entry := range results.Tests {
				if entry.Status == "passed" {
					t.Errorf("zero tests ran but stage reported passed entry %+v", entry)
				}
			}
			if out.Confidence >= 1.0 {
				t.Errorf("zero tests ran but stage confidence = %v, want < 1.0", out.Confidence)
			}
			if len(results.Tests) != 1 || results.Tests[0].Name != "suite" || results.Tests[0].Status != "skipped" || results.Tests[0].Message != "no tests ran" {
				t.Errorf("tests = %+v, want one skipped suite entry with message %q", results.Tests, "no tests ran")
			}
			if results.Evidence != schemas.TestEvidenceNone {
				t.Errorf("evidence = %q, want %q", results.Evidence, schemas.TestEvidenceNone)
			}
			if out.Summary != "Test command passed. No tests ran." || out.Confidence != 0.5 {
				t.Errorf("summary, confidence = %q, %v; want %q, 0.5", out.Summary, out.Confidence, "Test command passed. No tests ran.")
			}
			if err := results.Validate(); err != nil {
				t.Errorf("results do not validate: %v", err)
			}
		})
	}
}

// Owner decision 2026-11-11: a non-Go test command that exits 0 records
// "command succeeded, per-test evidence unavailable" and never claims that
// individual tests passed.
func TestTestRunnerNonGoExitZeroRecordsCommandOnly(t *testing.T) {
	cases := []struct {
		name    string
		command []string
		stdout  string
	}{
		{name: "pytest", command: []string{"pytest", "-q"}, stdout: "..\n2 passed in 0.01s\n"},
		{name: "npm test", command: []string{"npm", "test"}, stdout: "> m@1.0.0 test\n> node test.js\n\nall good\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := (TestRunner{}).Run(context.Background(), newHarnessInput("verify workspace"), &fakeProvider{}, StageOptions{
				WorkDir: t.TempDir(),
				Command: tc.command,
				RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
					return ToolResult{OK: true, Output: tc.stdout, Meta: map[string]string{"exit_code": "0"}}, nil
				},
			})
			if err != nil {
				t.Fatalf("setup: stage run: %v", err)
			}
			results, ok := out.Data["test_results"].(schemas.TestRunResults)
			if !ok {
				t.Fatalf("setup: test_results = %#v, want schemas.TestRunResults", out.Data["test_results"])
			}
			if results.ExitCode != 0 {
				t.Fatalf("setup: exit code = %d, want 0", results.ExitCode)
			}
			for _, entry := range results.Tests {
				if entry.Status == "passed" {
					t.Errorf("%s exited 0 with no per-test evidence but stage reported passed entry %+v", tc.name, entry)
				}
			}
			if out.Confidence >= 1.0 {
				t.Errorf("%s exited 0 with no per-test evidence but stage confidence = %v, want < 1.0", tc.name, out.Confidence)
			}
			if results.Evidence != schemas.TestEvidenceCommandOnly {
				t.Errorf("evidence = %q, want %q", results.Evidence, schemas.TestEvidenceCommandOnly)
			}
			if len(results.Tests) != 1 || results.Tests[0].Status != "skipped" || results.Tests[0].Message != "command succeeded; per-test evidence unavailable" {
				t.Errorf("tests = %+v, want one skipped suite entry that says the command succeeded", results.Tests)
			}
			if out.Summary != "Test command passed. Per-test evidence is not available." {
				t.Errorf("summary = %q", out.Summary)
			}
			if err := results.Validate(); err != nil {
				t.Errorf("results do not validate: %v", err)
			}
		})
	}
}

// The same test name in two packages must not collapse into one state entry.
// Entry names are package-qualified.
func TestGoTestJSONKeysTestsByPackage(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"run","Package":"example.com/a","Test":"TestX"}`,
		`{"Action":"output","Package":"example.com/a","Test":"TestX","Output":"    x_test.go:9: boom\n"}`,
		`{"Action":"fail","Package":"example.com/a","Test":"TestX","Elapsed":0}`,
		`{"Action":"run","Package":"example.com/b","Test":"TestX"}`,
		`{"Action":"pass","Package":"example.com/b","Test":"TestX","Elapsed":0}`,
		`{"Action":"fail","Package":"example.com/a","Elapsed":0}`,
		`{"Action":"pass","Package":"example.com/b","Elapsed":0}`,
	}, "\n")

	t.Run("parser", func(t *testing.T) {
		results, ok := parseGoTestJSON(stream)
		if !ok {
			t.Fatalf("setup: parseGoTestJSON rejected a valid go test -json stream")
		}
		if len(results.Tests) != 2 {
			t.Fatalf("two packages each ran TestX, got %d entries %+v, want 2 (one per package)", len(results.Tests), results.Tests)
		}
		failed := 0
		for _, entry := range results.Tests {
			if entry.Status == "failed" {
				failed++
			}
			if !strings.Contains(entry.Name, "example.com/a") && !strings.Contains(entry.Name, "example.com/b") {
				t.Errorf("entry name %q does not identify its package", entry.Name)
			}
		}
		if failed == 0 {
			t.Errorf("package example.com/a failed TestX but no entry has status failed: %+v", results.Tests)
		}
		want := []schemas.TestCaseResult{
			{Name: "example.com/a.TestX", Package: "example.com/a", Status: "failed", Message: "x_test.go:9: boom"},
			{Name: "example.com/b.TestX", Package: "example.com/b", Status: "passed"},
		}
		for i, entry := range results.Tests {
			if entry != want[i] {
				t.Errorf("entry %d = %+v, want %+v", i, entry, want[i])
			}
			if entry.TestName() != "TestX" {
				t.Errorf("entry %d bare name = %q, want TestX", i, entry.TestName())
			}
		}
	})

	t.Run("bare name without package", func(t *testing.T) {
		results, ok := parseGoTestJSON(`{"Action":"run","Test":"TestX"}` + "\n" + `{"Action":"pass","Test":"TestX"}`)
		if !ok || len(results.Tests) != 1 || results.Tests[0].Name != "TestX" || results.Tests[0].Package != "" {
			t.Fatalf("tests = %+v, want one bare TestX entry", results.Tests)
		}
	})

	t.Run("test_runner", func(t *testing.T) {
		// Exit 1 with one failing and one passing TestX: the stage must record
		// the failure against example.com/a. A generic exit-code entry does
		// not name the package, so it does not satisfy this check.
		_, results := goTestRunResults(t, stream, "1", false)
		failedA := false
		for _, entry := range results.Tests {
			if (entry.Status == "failed" || entry.Status == "errored") && strings.Contains(entry.Name, "example.com/a") && strings.Contains(entry.Name, "TestX") {
				failedA = true
			}
		}
		if !failedA {
			t.Errorf("package example.com/a failed TestX, exit 1, but no failed entry names example.com/a TestX (entries %+v)", results.Tests)
		}
	})
}

// A test that started but never reached a terminal event must not be
// reported as passed. Real triggers are a -timeout expiry or a killed test
// binary. A panic still emits a fail event for the test, so it is not a
// trigger.
func TestGoTestJSONUnfinishedTestIsErrored(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"run","Package":"example.com/p","Test":"TestX"}`,
		`{"Action":"pass","Package":"example.com/p","Test":"TestX","Elapsed":0}`,
		`{"Action":"run","Package":"example.com/p","Test":"TestY"}`,
		`{"Action":"output","Package":"example.com/p","Test":"TestY","Output":"=== RUN   TestY\n"}`,
		`{"Action":"fail","Package":"example.com/p","Elapsed":0}`,
	}, "\n")

	t.Run("parser", func(t *testing.T) {
		results, ok := parseGoTestJSON(stream)
		if !ok {
			t.Fatalf("setup: parseGoTestJSON rejected a valid go test -json stream")
		}
		found := false
		for _, entry := range results.Tests {
			if !strings.Contains(entry.Name, "TestY") {
				continue
			}
			found = true
			if entry.Status != "failed" && entry.Status != "errored" {
				t.Errorf("TestY started with no terminal event but entry status = %q (message %q); want failed or errored", entry.Status, entry.Message)
			}
			if entry.Status != "errored" || entry.Message != "test did not finish (timeout or killed)" {
				t.Errorf("TestY entry = %+v, want errored with the did-not-finish message", entry)
			}
		}
		if !found {
			t.Errorf("TestY started with no terminal event and has no entry (entries %+v); want a failed or errored entry", results.Tests)
		}
	})

	t.Run("test_runner", func(t *testing.T) {
		// The failure must be TestY's own entry. A generic exit-code entry
		// does not satisfy this check.
		_, results := goTestRunResults(t, stream, "1", false)
		failedY := false
		for _, entry := range results.Tests {
			if (entry.Status == "failed" || entry.Status == "errored") && strings.Contains(entry.Name, "TestY") {
				failedY = true
			}
		}
		if !failedY {
			t.Errorf("TestY never reached a terminal event, exit 1, but no failed or errored entry names TestY (entries %+v)", results.Tests)
		}
	})

	t.Run("cap keeps unfinished tests", func(t *testing.T) {
		var lines []string
		for i := 0; i < maxPerTestEntries+5; i++ {
			name := fmt.Sprintf("TestPass%03d", i)
			lines = append(lines,
				`{"Action":"run","Package":"example.com/p","Test":"`+name+`"}`,
				`{"Action":"pass","Package":"example.com/p","Test":"`+name+`"}`)
		}
		lines = append(lines, `{"Action":"run","Package":"example.com/p","Test":"TestHang"}`)
		results, _ := parseGoTestJSON(strings.Join(lines, "\n"))
		if results.Failed() != 1 {
			t.Fatalf("failed = %d over %d entries, want the unfinished TestHang kept past the cap", results.Failed(), len(results.Tests))
		}
	})
}

// A real `go test -json -timeout 1s` stream (Go 1.26, testdata) has no
// terminal event for the test that timed out. It must be errored, with the
// panic output, and the finished test stays passed.
func TestGoTestJSONRealTimeoutStream(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "go_test_json_timeout.jsonl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	out, results := goTestRunResults(t, string(raw), "1", false)
	byName := map[string]schemas.TestCaseResult{}
	for _, tc := range results.Tests {
		byName[tc.Name] = tc
	}
	if quick := byName["example.com/timeoutfixture.TestQuick"]; quick.Status != "passed" {
		t.Errorf("TestQuick = %+v, want passed", quick)
	}
	slow := byName["example.com/timeoutfixture.TestSlow"]
	if slow.Status != "errored" || !strings.HasPrefix(slow.Message, "test did not finish (timeout or killed)") {
		t.Errorf("TestSlow = %+v, want errored with the did-not-finish message", slow)
	}
	if results.Failed() != 1 || len(results.Tests) != 2 {
		t.Errorf("tests = %+v, want TestQuick passed and TestSlow errored only", results.Tests)
	}
	if out.Confidence != 0.8 {
		t.Errorf("confidence = %v, want 0.8 for a failed run", out.Confidence)
	}
}

// Every test passed but the process exited 1 (for example TestMain calls
// os.Exit(1)). The stage must not report a verified pass: passVerification
// and outputHasTestFailure read TestRunResults.Failed().
func TestTestRunnerNonZeroExitWithAllPassedFails(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"run","Package":"example.com/m","Test":"TestA"}`,
		`{"Action":"pass","Package":"example.com/m","Test":"TestA","Elapsed":0}`,
		`{"Action":"fail","Package":"example.com/m","Elapsed":0}`,
	}, "\n")
	_, results := goTestRunResults(t, stream, "1", false)
	if results.Failed() == 0 {
		t.Fatalf("every test passed but exit code is 1 (TestMain os.Exit(1)); TestRunResults.Failed() = 0 and the run would count as verified (entries %+v)", results.Tests)
	}
	last := results.Tests[len(results.Tests)-1]
	if last.Name != "suite" || last.Status != "failed" || last.Message != "test command exited 1; no failed test was reported" {
		t.Fatalf("last entry = %+v, want the failed suite entry for the exit code", last)
	}
	if results.Failed() != 1 || results.Passed() != 1 {
		t.Fatalf("failed, passed = %d, %d; want 1, 1", results.Failed(), results.Passed())
	}
}

// goStreamEvent appends one go test -json event line to b.
func goStreamEvent(b *strings.Builder, action, pkg, test, output string) {
	line, _ := json.Marshal(map[string]any{"Action": action, "Package": pkg, "Test": test, "Output": output, "Elapsed": 0.01})
	b.Write(line)
	b.WriteByte('\n')
}

// goBigStream builds about 1 MiB of go test -json: 6,000 passing tests in 3
// packages. Two failing tests and one unfinished test sit in the middle of
// the stream, so a head-and-tail cut of the output loses them.
func goBigStream() string {
	const packages, perPackage = 3, 2000
	var b strings.Builder
	for p := 0; p < packages; p++ {
		pkg := fmt.Sprintf("example.com/big/p%d", p)
		for i := 0; i < perPackage; i++ {
			if p == 1 && i == 500 {
				goStreamEvent(&b, "run", pkg, "TestFail0", "")
				goStreamEvent(&b, "output", pkg, "TestFail0", "    fail_test.go:9: want 2\n")
				goStreamEvent(&b, "fail", pkg, "TestFail0", "")
				goStreamEvent(&b, "run", pkg, "TestFail1", "")
				goStreamEvent(&b, "output", pkg, "TestFail1", "    fail_test.go:12: want 3\n")
				goStreamEvent(&b, "fail", pkg, "TestFail1", "")
				goStreamEvent(&b, "run", pkg, "TestHung", "")
			}
			test := fmt.Sprintf("TestPass%d", i)
			goStreamEvent(&b, "run", pkg, test, "")
			goStreamEvent(&b, "pass", pkg, test, "")
		}
	}
	return b.String()
}

// goStub drives TestRunner through a RunTool stub for a go test run. The stub
// reads the report path from the command string, then either writes report to
// it, runs swap on it, or writes nothing. It returns output as the tool output.
type goStub struct {
	output   string
	report   []byte
	swap     func(reportPath string)
	exitCode string
	record   bool
	command  string
}

// goReportPathOf returns the path after the last " > " of a bash command
// string, unquoted. It returns "" when the command has no redirect.
func goReportPathOf(command string) string {
	i := strings.LastIndex(command, " > ")
	if i < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(command[i+3:]), "'")
}

// runGoStub runs the test runner over `go test ./...` through stub and asserts
// that the report directory is gone when Run returns.
func runGoStub(t *testing.T, stub *goStub) schemas.HarnessStageOutput {
	t.Helper()
	var reportPath string
	run := func(_ context.Context, _ string, args map[string]any) (ToolResult, error) {
		stub.command, _ = args["command"].(string)
		reportPath = goReportPathOf(stub.command)
		switch {
		case reportPath == "":
		case stub.swap != nil:
			stub.swap(reportPath)
		case stub.report != nil:
			writeTestRunnerFixture(t, reportPath, string(stub.report))
		}
		return ToolResult{OK: stub.exitCode == "0", Output: stub.output, Meta: map[string]string{"exit_code": stub.exitCode}}, nil
	}
	options := StageOptions{WorkDir: t.TempDir(), Command: []string{"go", "test", "./..."}, RunTool: run}
	if stub.record {
		options.RecordCommand = func(ctx context.Context, _ string, _ map[string]any, call func(context.Context) (ToolResult, error)) (ToolResult, error) {
			return call(ctx)
		}
	}
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, options)
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if reportPath != "" {
		if _, err := os.Lstat(filepath.Dir(reportPath)); !os.IsNotExist(err) {
			t.Errorf("report directory still exists after Run returns (lstat err %v)", err)
		}
	}
	return out
}

// The bash tool keeps 16 KiB of head and 16 KiB of tail. The stream is about
// 1 MiB, so the stdout alone misses the failures. The report file is read.
func TestGoTestLargeSuiteReadsTheReportFile(t *testing.T) {
	stream := goBigStream()
	if len(stream) < 1<<20 {
		t.Fatalf("fixture is %d bytes, want about 1 MiB", len(stream))
	}
	stub := &goStub{
		output:   stream[:16<<10] + stream[len(stream)-16<<10:],
		report:   []byte(stream),
		exitCode: "1",
	}
	out := runGoStub(t, stub)
	results := out.Data["test_results"].(schemas.TestRunResults)
	if results.Evidence != schemas.TestEvidencePerTest {
		t.Fatalf("evidence = %q, want per_test", results.Evidence)
	}
	if results.Failed() != 3 {
		t.Fatalf("Failed() = %d, want 3 (two failures and one unfinished test)", results.Failed())
	}
	for _, want := range []struct{ name, status string }{
		{"example.com/big/p1.TestFail0", "failed"},
		{"example.com/big/p1.TestFail1", "failed"},
		{"example.com/big/p1.TestHung", "errored"},
	} {
		idx := slices.IndexFunc(results.Tests, func(tc schemas.TestCaseResult) bool { return tc.Name == want.name })
		if idx < 0 || results.Tests[idx].Status != want.status {
			t.Errorf("entry %s: missing or not %s", want.name, want.status)
		}
	}
	if len(results.Tests) > maxPerTestEntries+3 {
		t.Errorf("entries = %d, want at most %d", len(results.Tests), maxPerTestEntries+3)
	}
	if len(results.Stdout) > maxStoredGoStdoutBytes {
		t.Errorf("stored stdout = %d bytes, want at most %d", len(results.Stdout), maxStoredGoStdoutBytes)
	}
}

// Without a report file the stage parses the stdout exactly as before. A
// complete stream on stdout gives the same entries and failures.
func TestGoTestWithoutReportFileParsesStdout(t *testing.T) {
	stream := goBigStream()
	out := runGoStub(t, &goStub{output: stream, exitCode: "1"})
	results := out.Data["test_results"].(schemas.TestRunResults)
	if results.Evidence != schemas.TestEvidencePerTest || results.Failed() != 3 {
		t.Fatalf("evidence = %q, failed = %d; want per_test and 3", results.Evidence, results.Failed())
	}
}

// A symbolic link in place of the report file or the report directory is not
// read. The stage falls back to the stdout, and the link target is not parsed.
func TestGoTestSymlinkedReportFallsBackToStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests are skipped on Windows")
	}
	const stdoutStream = `{"Action":"pass","Package":"example.com/s","Test":"TestFromStdout"}` + "\n"
	linked := `{"Action":"pass","Package":"example.com/s","Test":"TestFromLink"}` + "\n"
	check := func(t *testing.T, out schemas.HarnessStageOutput) {
		t.Helper()
		results := out.Data["test_results"].(schemas.TestRunResults)
		if !slices.ContainsFunc(results.Tests, func(tc schemas.TestCaseResult) bool { return tc.Name == "example.com/s.TestFromStdout" }) {
			t.Fatalf("stdout entry missing: %+v", results.Tests)
		}
		if slices.ContainsFunc(results.Tests, func(tc schemas.TestCaseResult) bool { return strings.HasSuffix(tc.Name, "TestFromLink") }) {
			t.Fatal("the symlink target was parsed")
		}
	}
	t.Run("report file is a symlink", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "host-report.jsonl")
		writeTestRunnerFixture(t, outside, linked)
		check(t, runGoStub(t, &goStub{output: stdoutStream, exitCode: "0", swap: func(path string) {
			if err := os.Symlink(outside, path); err != nil {
				t.Fatalf("setup: symlink: %v", err)
			}
		}}))
	})
	t.Run("report directory is a symlink", func(t *testing.T) {
		check(t, runGoStub(t, &goStub{output: stdoutStream, exitCode: "0", swap: func(path string) {
			dir := filepath.Dir(path)
			if err := os.RemoveAll(dir); err != nil {
				t.Fatalf("setup: remove dir: %v", err)
			}
			other := t.TempDir()
			writeTestRunnerFixture(t, filepath.Join(other, goTestReportFile), linked)
			if err := os.Symlink(other, dir); err != nil {
				t.Fatalf("setup: symlink dir: %v", err)
			}
		}}))
	})
}

// The RunTool command ends with a redirect into the report directory. The
// argv recorded in the results does not carry the redirect or the path.
func TestGoTestCommandRedirectsToReportFile(t *testing.T) {
	for _, record := range []bool{false, true} {
		t.Run(fmt.Sprintf("record=%v", record), func(t *testing.T) {
			stub := &goStub{output: goStreamOneTest(), exitCode: "0", record: record}
			out := runGoStub(t, stub)
			path := goReportPathOf(stub.command)
			if path == "" || filepath.Base(path) != goTestReportFile {
				t.Fatalf("command %q does not redirect to the report file", stub.command)
			}
			if !strings.HasPrefix(filepath.Base(filepath.Dir(path)), "splice-test-report-") {
				t.Fatalf("report path %q is not in a splice-test-report- directory", path)
			}
			if !strings.HasPrefix(stub.command, "'go' 'test' '-json' './...' > '") {
				t.Fatalf("command = %q, want the argv with -json, then the redirect", stub.command)
			}
			results := out.Data["test_results"].(schemas.TestRunResults)
			for _, arg := range results.Command {
				if strings.Contains(arg, ">") || strings.Contains(arg, path) {
					t.Fatalf("results.Command %q carries the redirect or the path", results.Command)
				}
			}
			if strings.Join(results.Command, " ") != "go test -json ./..." {
				t.Fatalf("results.Command = %q, want the argv with -json", results.Command)
			}
		})
	}
}

func goStreamOneTest() string {
	var b strings.Builder
	goStreamEvent(&b, "run", "example.com/one", "TestOne", "")
	goStreamEvent(&b, "pass", "example.com/one", "TestOne", "")
	return b.String()
}

// Direct path: runCommand writes the stream to the report file. The stdout
// capture keeps only 200,000 bytes, so a 300 KB test output would hide the
// last test's pass event. The report file keeps it, and all three tests pass.
func TestGoTestDirectPathReadsTheReportFile(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go is not available: %v", err)
	}
	if backend := sandbox.SelectBackend(sandbox.BackendOptions{}); !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	work := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(work, "go.mod"), "module example.com/direct\n\ngo 1.25\n")
	writeTestRunnerFixture(t, filepath.Join(work, "direct_test.go"), `package direct

import (
	"strings"
	"testing"
)

func TestA(t *testing.T) {}

func TestBig(t *testing.T) {
	for i := 0; i < 3000; i++ {
		t.Log(strings.Repeat("x", 100))
	}
}

func TestB(t *testing.T) {}
`)
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		WorkDir:        work,
		Command:        []string{"go", "test", "./..."},
		TimeoutSeconds: 120,
		Sandbox:        procrun.NewStageEngine(work),
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results := out.Data["test_results"].(schemas.TestRunResults)
	if results.ExitCode != 0 || results.Evidence != schemas.TestEvidencePerTest || results.Passed() != 3 {
		t.Fatalf("exit = %d, evidence = %q, passed = %d, tests = %+v", results.ExitCode, results.Evidence, results.Passed(), results.Tests)
	}
}

// The real production path: the registry bash tool runs go test under the
// stage sandbox with the redirect into the report directory, and Splice reads
// the report back. Skipped when go or the native sandbox backend is missing.
func TestTestRunnerGoReportThroughRealBash(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go is not available: %v", err)
	}
	if backend := sandbox.SelectBackend(sandbox.BackendOptions{}); !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	work := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(work, "go.mod"), "module example.com/bash\n\ngo 1.25\n")
	writeTestRunnerFixture(t, filepath.Join(work, "bash_test.go"), "package bash\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\nfunc TestC(t *testing.T) {}\n")
	engine := procrun.NewStageEngine(work)
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(work))
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		WorkDir:        work,
		Command:        []string{"go", "test", "./..."},
		TimeoutSeconds: 120,
		Sandbox:        engine,
		RunTool: func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			res := registry.RunWithOptions(ctx, name, args, tools.RunOptions{Sandbox: engine, PermissionGranted: true})
			return ToolResult{OK: res.Status == tools.StatusOK, Output: res.Output, Truncated: res.Truncated, Meta: res.Meta}, nil
		},
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results := out.Data["test_results"].(schemas.TestRunResults)
	if results.ExitCode != 0 || results.Evidence != schemas.TestEvidencePerTest || results.Passed() != 3 {
		t.Fatalf("exit = %d, evidence = %q, passed = %d, output = %q", results.ExitCode, results.Evidence, results.Passed(), results.Stdout)
	}
}

// A line of go test -json output can be larger than any scanner buffer. The
// parser must still read the terminal events that follow it.
func TestGoTestJSONReadsEventsAfterOversizedLine(t *testing.T) {
	huge := strings.Repeat("x", 2<<20)
	stream := strings.Join([]string{
		`{"Action":"run","Package":"example.com/p","Test":"TestA"}`,
		`{"Action":"output","Package":"example.com/p","Test":"TestA","Output":"` + huge + `"}`,
		`{"Action":"pass","Package":"example.com/p","Test":"TestA","Elapsed":0}`,
		`{"Action":"run","Package":"example.com/p","Test":"TestB"}`,
		`{"Action":"pass","Package":"example.com/p","Test":"TestB","Elapsed":0}`,
		`{"Action":"pass","Package":"example.com/p","Elapsed":0}`,
	}, "\n")

	parsed, ok := parseGoTestJSON(stream)
	if !ok {
		t.Fatal("valid go test -json stream with a large line must parse")
	}
	if got := len(parsed.Tests); got != 2 {
		t.Fatalf("tests = %d, want 2 passed entries; got %+v", got, parsed.Tests)
	}
	for _, tc := range parsed.Tests {
		if tc.Status != "passed" {
			t.Fatalf("entry %q status = %q, want passed", tc.Name, tc.Status)
		}
	}
	if parsed.Failed() != 0 {
		t.Fatalf("Failed() = %d, want 0", parsed.Failed())
	}
}

// A symbolic link planted at the report path must not be written through. The
// run keeps the head capture and the link target stays empty.
func TestRunCommandDoesNotWriteThroughReportSymlink(t *testing.T) {
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	reportPath := filepath.Join(dir, "report.jsonl")
	if err := os.Symlink(target, reportPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	engine := procrun.NewStageEngine(dir)
	results := runCommand(context.Background(), engine, []string{"git", "--version"}, dir, 30, reportPath)
	if results.ExitCode != 0 || !strings.Contains(results.Stdout, "git version") {
		t.Fatalf("run = exit %d stdout %q stderr %q, want the head capture to keep the output", results.ExitCode, results.Stdout, results.Stderr)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("symlink target was written through: %q", data)
	}
}

// goJSONFailureStream is go test -json output with one failing test, one
// passing test, and one more failing test, for package example.com/fixture.
func goJSONStream(failing ...string) string {
	lines := []string{`{"Action":"run","Package":"example.com/fixture","Test":"TestPass"}`,
		`{"Action":"pass","Package":"example.com/fixture","Test":"TestPass","Elapsed":0}`}
	for _, name := range failing {
		lines = append(lines,
			`{"Action":"run","Package":"example.com/fixture","Test":"`+name+`"}`,
			`{"Action":"output","Package":"example.com/fixture","Test":"`+name+`","Output":"    fixture_test.go:9: boom\n"}`,
			`{"Action":"fail","Package":"example.com/fixture","Test":"`+name+`","Elapsed":0}`)
	}
	lines = append(lines, `{"Action":"fail","Package":"example.com/fixture","Elapsed":0.1}`)
	return strings.Join(lines, "\n")
}

// A run marks the failures that the pre-run baseline already has. Only the
// new failure is counted, and the summary names the one that was there before.
func TestTestRunnerMarksFailuresAlreadyInBaseline(t *testing.T) {
	var baselineArgv []string
	calls := 0
	options := StageOptions{
		WorkDir: t.TempDir(),
		Command: []string{"go", "test", "./..."},
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: false, Output: goJSONStream("TestOld", "TestNew"), Meta: map[string]string{"exit_code": "1"}}, nil
		},
		TestBaseline: func(_ context.Context, _ StageOptions, argv []string, _ string) (schemas.TestRunResults, bool) {
			calls++
			baselineArgv = argv
			base, _ := parseGoTestJSON(goJSONStream("TestOld"))
			return base, true
		},
	}
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, options)
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results := out.Data["test_results"].(schemas.TestRunResults)
	if calls != 1 {
		t.Fatalf("baseline calls = %d, want 1", calls)
	}
	if !equalTestRunnerStrings(baselineArgv, results.Command) {
		t.Fatalf("baseline argv = %v, want the recorded argv %v", baselineArgv, results.Command)
	}
	if results.Failed() != 1 || results.FailedBefore() != 1 {
		t.Fatalf("Failed = %d, FailedBefore = %d, want 1 and 1: %+v", results.Failed(), results.FailedBefore(), results.Tests)
	}
	if !strings.Contains(out.Summary, "already failing") || !strings.Contains(out.Summary, "TestOld") {
		t.Fatalf("summary must name the pre-existing failure: %q", out.Summary)
	}
	if !strings.Contains(out.Summary, "TestNew") {
		t.Fatalf("summary must keep the new failure as evidence: %q", out.Summary)
	}
	if strings.Contains(out.Summary, "Failing evidence:\n") && strings.Contains(out.Summary, "TestOld: ") {
		t.Fatalf("failing evidence must not list the pre-existing failure: %q", out.Summary)
	}
}

// With no baseline, every failure stays new and the summary is unchanged.
func TestTestRunnerNoBaselineKeepsEveryFailureNew(t *testing.T) {
	options := StageOptions{
		WorkDir: t.TempDir(),
		Command: []string{"go", "test", "./..."},
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: false, Output: goJSONStream("TestOld", "TestNew"), Meta: map[string]string{"exit_code": "1"}}, nil
		},
		TestBaseline: func(context.Context, StageOptions, []string, string) (schemas.TestRunResults, bool) {
			return schemas.TestRunResults{}, false
		},
	}
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, options)
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results := out.Data["test_results"].(schemas.TestRunResults)
	if results.Failed() != 2 || results.FailedBefore() != 0 {
		t.Fatalf("Failed = %d, FailedBefore = %d, want 2 and 0", results.Failed(), results.FailedBefore())
	}
	if strings.Contains(out.Summary, "already failing") {
		t.Fatalf("summary must not mention baseline failures: %q", out.Summary)
	}
}

// Command-only evidence has no per-test failures to compare, so the baseline
// never runs and the result is unchanged.
func TestTestRunnerCommandOnlyNeverRunsBaseline(t *testing.T) {
	calls := 0
	options := StageOptions{
		WorkDir: t.TempDir(),
		Command: []string{"make", "check"},
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: false, Output: "make: failed", Meta: map[string]string{"exit_code": "2"}}, nil
		},
		TestBaseline: func(context.Context, StageOptions, []string, string) (schemas.TestRunResults, bool) {
			calls++
			return schemas.TestRunResults{}, true
		},
	}
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, options)
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results := out.Data["test_results"].(schemas.TestRunResults)
	if calls != 0 {
		t.Fatalf("baseline ran %d time(s) for command_only evidence", calls)
	}
	if results.Evidence != schemas.TestEvidenceCommandOnly || results.FailedBefore() != 0 {
		t.Fatalf("evidence = %q, FailedBefore = %d", results.Evidence, results.FailedBefore())
	}
}

// A command that exits 0 but whose per-test report has a failed test is still
// compared with the baseline. The failure that predates the run is marked.
func TestTestRunnerMarksFailuresOnPassingExitCode(t *testing.T) {
	calls := 0
	options := StageOptions{
		WorkDir: t.TempDir(),
		Command: []string{"go", "test", "./..."},
		RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
			return ToolResult{OK: true, Output: goJSONStream("TestOld")}, nil
		},
		TestBaseline: func(context.Context, StageOptions, []string, string) (schemas.TestRunResults, bool) {
			calls++
			base, _ := parseGoTestJSON(goJSONStream("TestOld"))
			return base, true
		},
	}
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, options)
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results := out.Data["test_results"].(schemas.TestRunResults)
	if results.ExitCode != 0 || calls != 1 {
		t.Fatalf("exit = %d, baseline calls = %d; want exit 0 and one baseline call", results.ExitCode, calls)
	}
	if results.FailedBefore() != 1 || results.Failed() != 0 {
		t.Fatalf("FailedBefore = %d, Failed = %d; want 1 and 0", results.FailedBefore(), results.Failed())
	}
}

// Related test files are found by naming convention. Each case builds the
// test files that exist, runs the expansion, and checks the argv it returns.
func TestExpandConfiguredTestCommandRelatedTests(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		changed map[string][]string
		testDir string
		command []string
		want    []string
	}{
		{
			name:    "python source finds its test in a tests dir",
			files:   []string{"sympy/core/tests/test_function.py"},
			changed: map[string][]string{"code_writer": {"sympy/core/function.py"}},
			command: []string{"python", "{tests}"},
			want:    []string{"python", "sympy/core/tests/test_function.py"},
		},
		{
			name: "python source finds every naming convention",
			files: []string{
				"pkg/tests/test_mod.py", "pkg/test_mod.py", "pkg/mod_test.py", "pkg/tests/mod_test.py",
			},
			changed: map[string][]string{"code_writer": {"pkg/mod.py"}},
			command: []string{"python", "{tests}"},
			want: []string{
				"python", "pkg/mod_test.py", "pkg/test_mod.py", "pkg/tests/mod_test.py", "pkg/tests/test_mod.py",
			},
		},
		{
			name:    "changed test file and related test file form a union",
			files:   []string{"tests/test_auth.py", "pkg/test_mod.py"},
			changed: map[string][]string{"test_generator": {"tests/test_auth.py"}, "code_writer": {"pkg/mod.py"}},
			command: []string{"python", "{tests}"},
			want:    []string{"python", "tests/test_auth.py", "pkg/test_mod.py"},
		},
		{
			name:    "go source names its package directory",
			files:   []string{"pkg/name_test.go"},
			changed: map[string][]string{"code_writer": {"pkg/name.go"}},
			command: []string{"go", "test", "{tests}"},
			want:    []string{"go", "test", "./pkg"},
		},
		{
			name:    "changed go test file names its package directory",
			files:   []string{"pkg/name_test.go"},
			changed: map[string][]string{"test_generator": {"pkg/name_test.go"}},
			command: []string{"go", "test", "{tests}"},
			want:    []string{"go", "test", "./pkg"},
		},
		{
			name:    "go files in one package give one package argument",
			files:   []string{"pkg/name_test.go", "pkg/other_test.go"},
			changed: map[string][]string{"code_writer": {"pkg/name.go", "pkg/other.go"}},
			command: []string{"go", "test", "{tests}"},
			want:    []string{"go", "test", "./pkg"},
		},
		{
			name:    "go package at the root is dot",
			files:   []string{"name_test.go"},
			changed: map[string][]string{"code_writer": {"name.go"}},
			command: []string{"go", "test", "{tests}"},
			want:    []string{"go", "test", "."},
		},
		{
			name:    "ts source finds same-extension test files",
			files:   []string{"web/app.test.tsx", "web/app.spec.tsx", "web/__tests__/app.tsx", "web/app.test.js"},
			changed: map[string][]string{"code_writer": {"web/app.tsx"}},
			command: []string{"npx", "jest", "{tests}"},
			want: []string{
				"npx", "jest", "web/__tests__/app.tsx", "web/app.spec.tsx", "web/app.test.tsx",
			},
		},
		{
			name:    "test_modules maps the same related list",
			files:   []string{"sympy/core/tests/test_function.py"},
			changed: map[string][]string{"code_writer": {"sympy/core/function.py"}},
			command: []string{"python", "{test_modules}"},
			want:    []string{"python", "sympy.core.tests.test_function"},
		},
		{
			name:    "related path that does not exist is not added",
			files:   []string{"pkg/tests/test_other.py"},
			changed: map[string][]string{"code_writer": {"pkg/mod.py"}},
			command: []string{"python", "{tests}"},
			want:    []string{"python"},
		},
		{
			name:    "related test outside the test dir is not added",
			files:   []string{"pkg/test_mod.py"},
			changed: map[string][]string{"code_writer": {"pkg/mod.py"}},
			testDir: "tests",
			command: []string{"python", "{tests}"},
			want:    []string{"python"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			fixtures := append([]string(nil), tc.files...)
			for _, list := range tc.changed {
				fixtures = append(fixtures, list...)
			}
			writeExpansionFixtures(t, root, fixtures...)
			testDir := root
			if tc.testDir != "" {
				testDir = filepath.Join(root, tc.testDir)
			}
			got, err := expandConfiguredTestCommand(tc.command, tc.changed, root, testDir)
			if err != nil {
				t.Fatalf("expandConfiguredTestCommand: %v", err)
			}
			if !equalTestRunnerStrings(got, tc.want) {
				t.Fatalf("expanded command = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// The related list holds at most 20 paths, in sorted order.
func TestExpandConfiguredTestCommandCapsRelatedTests(t *testing.T) {
	root := t.TempDir()
	changed := map[string][]string{"code_writer": {}}
	var tests []string
	for i := 0; i < 25; i++ {
		src := fmt.Sprintf("pkg/m%02d.py", i)
		tests = append(tests, fmt.Sprintf("pkg/test_m%02d.py", i))
		changed["code_writer"] = append(changed["code_writer"], src)
	}
	writeExpansionFixtures(t, root, append(tests, changed["code_writer"]...)...)
	got, err := expandConfiguredTestCommand([]string{"python", "{tests}"}, changed, root, root)
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	want := []string{"python"}
	for i := 0; i < 20; i++ {
		want = append(want, fmt.Sprintf("pkg/test_m%02d.py", i))
	}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("expanded command = %#v, want the first 20 sorted paths", got)
	}
}

// A changed README with a {tests} command must not run the command. The
// run records no tests ran and no evidence, and it names the placeholder.
func TestTestRunnerSkipsConfiguredCommandWithoutRelatedTests(t *testing.T) {
	cases := []struct {
		name        string
		command     []string
		placeholder string
	}{
		{name: "tests", command: []string{"python", "-m", "pytest", "-q", "{tests}"}, placeholder: "{tests}"},
		{name: "test_modules", command: []string{"python", "-m", "unittest", "{test_modules}"}, placeholder: "{test_modules}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeExpansionFixtures(t, root, "README.md")
			calls := 0
			input := newHarnessInput("update the docs")
			input.RunChangedFiles = map[string][]string{"code_writer": {"README.md"}}
			var reported []string
			out, err := (TestRunner{}).Run(context.Background(), input, &fakeProvider{}, StageOptions{
				WorkDir:        root,
				Command:        tc.command,
				CommandWorkDir: root,
				ReportActivity: func(message string) { reported = append(reported, message) },
				RunTool: func(context.Context, string, map[string]any) (ToolResult, error) {
					calls++
					return ToolResult{OK: true, Output: "1 passed"}, nil
				},
			})
			if err != nil {
				t.Fatalf("stage run: %v", err)
			}
			if calls != 0 {
				t.Fatalf("configured command ran %d time(s), want 0", calls)
			}
			results, ok := out.Data["test_results"].(schemas.TestRunResults)
			if !ok {
				t.Fatalf("test_results = %#v, want schemas.TestRunResults", out.Data["test_results"])
			}
			if results.Evidence != schemas.TestEvidenceNone {
				t.Errorf("evidence = %q, want %q", results.Evidence, schemas.TestEvidenceNone)
			}
			if results.Passed() != 0 || results.Failed() != 0 {
				t.Errorf("passed, failed = %d, %d; want 0, 0 (a failure would start a repair)", results.Passed(), results.Failed())
			}
			if len(results.Tests) != 1 || results.Tests[0].Status != "skipped" || results.Tests[0].Message != "no tests ran" {
				t.Errorf("tests = %+v, want one skipped suite entry with message %q", results.Tests, "no tests ran")
			}
			want := fmt.Sprintf("No test file matches the change, so the configured test command did not run (it uses %s).", tc.placeholder)
			if out.Summary != want || out.Detail != want {
				t.Errorf("summary = %q, detail = %q; want %q", out.Summary, out.Detail, want)
			}
			if out.Confidence != 0 {
				t.Errorf("confidence = %v, want 0", out.Confidence)
			}
			if !slices.Contains(reported, want) {
				t.Errorf("progress reports = %q, want the no-match summary %q", reported, want)
			}
			if err := results.Validate(); err != nil {
				t.Errorf("results do not validate: %v", err)
			}
		})
	}
}

// A command without placeholders is not changed by the no-match rule. It runs.
func TestTestRunnerRunsCommandWithoutPlaceholdersWhenNoTestChanged(t *testing.T) {
	root := t.TempDir()
	writeExpansionFixtures(t, root, "README.md")
	calls := 0
	var gotCommand string
	input := newHarnessInput("update the docs")
	input.RunChangedFiles = map[string][]string{"code_writer": {"README.md"}}
	out, err := (TestRunner{}).Run(context.Background(), input, &fakeProvider{}, StageOptions{
		WorkDir:        root,
		Command:        []string{"python", "-m", "pytest", "-q"},
		CommandWorkDir: root,
		RunTool: func(_ context.Context, _ string, args map[string]any) (ToolResult, error) {
			calls++
			gotCommand, _ = args["command"].(string)
			return ToolResult{OK: true, Output: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if calls != 1 || !strings.Contains(gotCommand, "'-q'") {
		t.Fatalf("calls = %d, command = %q; want the command to run once", calls, gotCommand)
	}
	if got, _ := out.Data["test_command"].([]string); !equalTestRunnerStrings(got, []string{"python", "-m", "pytest", "-q"}) {
		t.Fatalf("test_command = %#v, want the configured argv", out.Data["test_command"])
	}
}

// A related test file reaches the command that runs and the argv that the
// results and the pre-run baseline record.
func TestTestRunnerRunsRelatedTestFile(t *testing.T) {
	root := t.TempDir()
	writeExpansionFixtures(t, root, "sympy/core/function.py", "sympy/core/tests/test_function.py")
	calls := 0
	var gotCommand string
	input := newHarnessInput("fix the function")
	input.RunChangedFiles = map[string][]string{"code_writer": {"sympy/core/function.py"}}
	out, err := (TestRunner{}).Run(context.Background(), input, &fakeProvider{}, StageOptions{
		WorkDir:        root,
		Command:        []string{"python", "-m", "pytest", "-q", "{tests}"},
		CommandWorkDir: root,
		RunTool: func(_ context.Context, _ string, args map[string]any) (ToolResult, error) {
			calls++
			gotCommand, _ = args["command"].(string)
			return ToolResult{OK: true, Output: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	const path = "sympy/core/tests/test_function.py"
	if calls != 1 || !strings.Contains(gotCommand, path) {
		t.Fatalf("calls = %d, command = %q; want one run that names %s", calls, gotCommand, path)
	}
	got, _ := out.Data["test_command"].([]string)
	if !slices.Contains(got, path) {
		t.Fatalf("test_command = %#v, want it to name %s", got, path)
	}
}

// Changed test files come before related files, so the cap never drops a
// changed test file in favor of a related one.
func TestExpandConfiguredTestCommandChangedFilesSurviveCap(t *testing.T) {
	root := t.TempDir()
	var sources, related []string
	for i := 0; i < 25; i++ {
		sources = append(sources, fmt.Sprintf("pkg/m%02d.py", i))
		related = append(related, fmt.Sprintf("pkg/test_m%02d.py", i))
	}
	changedTests := []string{"tests/test_zz_one.py", "tests/test_zz_two.py"}
	writeExpansionFixtures(t, root, append(append(append([]string(nil), sources...), related...), changedTests...)...)
	changed := map[string][]string{"code_writer": sources, "test_generator": changedTests}
	got, err := expandConfiguredTestCommand([]string{"python", "{tests}"}, changed, root, root)
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	want := []string{"python", "tests/test_zz_one.py", "tests/test_zz_two.py"}
	for i := 0; i < 18; i++ {
		want = append(want, fmt.Sprintf("pkg/test_m%02d.py", i))
	}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("expanded command = %#v, want both changed test files first, then 18 related", got)
	}
}

// When the changed test files alone exceed the cap, the first 20 sorted
// changed files win and no related file is added.
func TestExpandConfiguredTestCommandCapKeepsFirstChangedFiles(t *testing.T) {
	root := t.TempDir()
	var changedTests []string
	for i := 0; i < 22; i++ {
		changedTests = append(changedTests, fmt.Sprintf("tests/test_c%02d.py", i))
	}
	writeExpansionFixtures(t, root, append([]string{"pkg/mod.py", "pkg/test_mod.py"}, changedTests...)...)
	changed := map[string][]string{"test_generator": changedTests, "code_writer": {"pkg/mod.py"}}
	got, err := expandConfiguredTestCommand([]string{"python", "{tests}"}, changed, root, root)
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	want := []string{"python"}
	for i := 0; i < 20; i++ {
		want = append(want, fmt.Sprintf("tests/test_c%02d.py", i))
	}
	if !equalTestRunnerStrings(got, want) {
		t.Fatalf("expanded command = %#v, want the first 20 changed test files", got)
	}
}

// go test on a single _test.go file cannot build its package. A changed Go
// source file must run its whole package, and this test runs the real
// command in a temporary module, so it fails when the argument is not a
// buildable package.
func TestTestRunnerGoTestPackageRunsForRealModule(t *testing.T) {
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	root := t.TempDir()
	writeExpansionFixtures(t, root, "pkg/add.go", "pkg/add_test.go")
	writeTestRunnerFixture(t, filepath.Join(root, "go.mod"), "module example.com/fixture\n\ngo 1.25\n")
	writeTestRunnerFixture(t, filepath.Join(root, "pkg", "add.go"), "package pkg\n\nfunc Add(a, b int) int { return a + b }\n")
	writeTestRunnerFixture(t, filepath.Join(root, "pkg", "add_test.go"), "package pkg\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad sum\")\n\t}\n}\n")
	input := newHarnessInput("add a helper")
	input.RunChangedFiles = map[string][]string{"code_writer": {"pkg/add.go"}}
	out, err := (TestRunner{}).Run(context.Background(), input, &fakeProvider{}, StageOptions{
		WorkDir:        root,
		Command:        []string{"go", "test", "{tests}"},
		CommandWorkDir: root,
		TimeoutSeconds: 300,
		Sandbox:        procrun.NewStageEngine(root),
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results := out.Data["test_results"].(schemas.TestRunResults)
	if results.ExitCode != 0 || results.Failed() != 0 || results.Passed() != 1 {
		t.Fatalf("exit = %d, passed = %d, failed = %d, want exit 0 and one passed test; output: %s %s",
			results.ExitCode, results.Passed(), results.Failed(), results.Stdout, results.Stderr)
	}
}

// A src or lib layout keeps its tests in a tests directory at the workspace
// root. A changed source file finds tests/test_<name>.py there.
func TestExpandConfiguredTestCommandPythonRootTestsDir(t *testing.T) {
	root := t.TempDir()
	writeExpansionFixtures(t, root, "src/pkg/mod.py", "tests/test_mod.py")
	got, err := expandConfiguredTestCommand([]string{"python", "{tests}"}, map[string][]string{
		"code_writer": {"src/pkg/mod.py"},
	}, root, root)
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	if !equalTestRunnerStrings(got, []string{"python", "tests/test_mod.py"}) {
		t.Fatalf("expanded command = %#v, want the root tests dir file", got)
	}
}

// A changed Jest test under __tests__ is a test file, even without a .test.
// or .spec. name. It must reach the command.
func TestExpandConfiguredTestCommandChangedJestTestsDirFile(t *testing.T) {
	root := t.TempDir()
	writeExpansionFixtures(t, root, "web/__tests__/helper.js", "web/__tests__/setup.ts")
	got, err := expandConfiguredTestCommand([]string{"npx", "jest", "{tests}"}, map[string][]string{
		"test_generator": {"web/__tests__/helper.js", "web/__tests__/setup.ts"},
	}, root, root)
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	if !equalTestRunnerStrings(got, []string{"npx", "jest", "web/__tests__/helper.js", "web/__tests__/setup.ts"}) {
		t.Fatalf("expanded command = %#v, want both changed __tests__ files", got)
	}
}

// A TypeScript source finds the test under __tests__ that keeps the name.test
// form, and it does not find a test of another extension.
func TestExpandConfiguredTestCommandJestTestsDirCandidate(t *testing.T) {
	root := t.TempDir()
	writeExpansionFixtures(t, root, "web/app.tsx", "web/__tests__/app.test.tsx", "web/__tests__/app.test.js")
	got, err := expandConfiguredTestCommand([]string{"npx", "jest", "{tests}"}, map[string][]string{
		"code_writer": {"web/app.tsx"},
	}, root, root)
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	if !equalTestRunnerStrings(got, []string{"npx", "jest", "web/__tests__/app.test.tsx"}) {
		t.Fatalf("expanded command = %#v, want only the same-extension __tests__ test", got)
	}
}

// A changed test file is never a source for related tests. Its own name
// would otherwise find a test that the change did not touch.
func TestExpandConfiguredTestCommandChangedTestIsNotRelatedSource(t *testing.T) {
	root := t.TempDir()
	writeExpansionFixtures(t, root, "pkg/mod_test.py", "pkg/test_mod_test.py")
	got, err := expandConfiguredTestCommand([]string{"python", "{tests}"}, map[string][]string{
		"test_generator": {"pkg/mod_test.py"},
	}, root, root)
	if err != nil {
		t.Fatalf("expandConfiguredTestCommand: %v", err)
	}
	if !equalTestRunnerStrings(got, []string{"python", "pkg/mod_test.py"}) {
		t.Fatalf("expanded command = %#v, want only the changed test file", got)
	}
}
