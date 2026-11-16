package stages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// The fixtures in testdata/pytest (JUnit XML) and testdata/jest (--json
// reports) are the real report files written by pytest 9.0.3 (Python 3.10.6)
// and jest 30.5.2 (node v24.18.0). pytest was run with
// `python3 -m pytest --junitxml=report.xml -o junit_family=xunit2`. jest was
// run with `npx jest --json --outputFile=report.json`. Temp project paths are
// replaced with /WORK. The jest project root is /WORK/jestproj.

func readFrameworkFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

type wantEntry struct {
	name, status, message string
}

func assertEntries(t *testing.T, got []schemas.TestCaseResult, want []wantEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Status != w.status || got[i].Message != w.message {
			t.Errorf("entry %d = {%q %q %q}, want {%q %q %q}", i, got[i].Name, got[i].Status, got[i].Message, w.name, w.status, w.message)
		}
	}
}

var pytestMixedEntries = []wantEntry{
	{"tests.test_mix::test_pass", "passed", ""},
	{"tests.test_mix::test_fail", "failed", "assert 1 == 2"},
	{"tests.test_mix::test_error", "errored", `failed on setup with "RuntimeError: fixture boom"`},
	{"tests.test_mix::test_skip", "skipped", "not ready"},
	{"tests.test_mix::test_xfail", "skipped", "known bug"},
	{"tests.test_mix::test_xpass", "passed", ""},
}

var pytestTeardownEntries = []wantEntry{
	{"tests.test_teardown::test_passes_then_teardown_error", "errored", `failed on teardown with "RuntimeError: teardown boom"`},
	{"tests.test_teardown::test_fails_then_teardown_error", "failed", "assert False"},
	{"tests.test_teardown::test_fails_then_teardown_error", "errored", `failed on teardown with "RuntimeError: teardown boom"`},
}

func TestParsePytestJUnitFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		want    []wantEntry
		passed  int
		failed  int
	}{
		{fixture: "all_pass.xml", want: []wantEntry{
			{"tests.test_ok::test_add", "passed", ""},
			{"tests.test_ok::test_upper", "passed", ""},
			{"tests.test_ok.TestGroup::test_nested", "passed", ""},
			{"tests.test_ok::test_warns", "passed", ""},
		}, passed: 4},
		{fixture: "mixed.xml", want: pytestMixedEntries, passed: 2, failed: 2},
		{fixture: "param_ids.xml", want: []wantEntry{
			{"tests.test_param::test_param[case one]", "passed", ""},
			{"tests.test_param::test_param[x - y]", "passed", ""},
			{"tests.test_param::test_param[fails - here]", "failed", "AssertionError: bad - value\nassert 'fails - here' != 'fails - here'"},
		}, passed: 2, failed: 1},
		{fixture: "no_tests.xml", want: nil},
		{fixture: "teardown_error.xml", want: pytestTeardownEntries, failed: 3},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			got, ok := parsePytestJUnit(readFrameworkFixture(t, "pytest/"+tc.fixture))
			if !ok {
				t.Fatal("ok = false, want true")
			}
			assertEntries(t, got.Tests, tc.want)
			if got.Passed() != tc.passed || got.Failed() != tc.failed {
				t.Errorf("passed, failed = %d, %d; want %d, %d", got.Passed(), got.Failed(), tc.passed, tc.failed)
			}
		})
	}
}

// Each mutation takes a real fixture and breaks it. The parser must refuse
// every one: the report counts or the report structure no longer hold.
func TestParsePytestJUnitConsistencyRule(t *testing.T) {
	allPass := string(readFrameworkFixture(t, "pytest/all_pass.xml"))
	mixed := string(readFrameworkFixture(t, "pytest/mixed.xml"))
	tests := []struct {
		name   string
		output string
	}{
		{"tests attribute changed", strings.Replace(allPass, `tests="4"`, `tests="5"`, 1)},
		{"failures attribute changed", strings.Replace(mixed, `failures="1"`, `failures="2"`, 1)},
		{"errors attribute changed", strings.Replace(mixed, `errors="1"`, `errors="0"`, 1)},
		{"skipped attribute changed", strings.Replace(mixed, `skipped="2"`, `skipped="1"`, 1)},
		{"testcase deleted", strings.Replace(allPass, `<testcase classname="tests.test_ok" name="test_add" time="0.000" />`, "", 1)},
		{"tests attribute missing", strings.Replace(allPass, ` tests="4"`, "", 1)},
		{"tests attribute not a number", strings.Replace(allPass, `tests="4"`, `tests="four"`, 1)},
		{"second outcome child on one testcase", strings.Replace(mixed, `</failure></testcase>`, `</failure><error message="x"/></testcase>`, 1)},
		{"truncated in the middle", mixed[:len(mixed)/2]},
		{"garbage appended", allPass + "junk"},
		{"element appended after the root", allPass + "<extra/>"},
		{"unknown root", `<?xml version="1.0"?><other tests="0"/>`},
		{"no testsuite element", `<testsuites></testsuites>`},
		{"empty output", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := parsePytestJUnit([]byte(tc.output)); ok {
				t.Fatal("ok = true, want false: the parser must not guess")
			}
		})
	}
}

func TestParseJestJSONFixtures(t *testing.T) {
	const work = "/WORK/jestproj"
	tests := []struct {
		fixture string
		want    []wantEntry
		passed  int
		failed  int
	}{
		{fixture: "all_pass.json", want: []wantEntry{
			{"__tests__/pass.test.js > adds numbers", "passed", ""},
			{"__tests__/pass.test.js > joins strings", "passed", ""},
		}, passed: 2},
		{fixture: "mixed.json", want: []wantEntry{
			{"__tests__/mix.test.js > math adds", "passed", ""},
			{"__tests__/mix.test.js > math fails on purpose", "failed", ""},
			{"__tests__/mix.test.js > math skipped one", "skipped", ""},
			{"__tests__/mix.test.js > math later", "skipped", ""},
		}, passed: 1, failed: 1},
		{fixture: "syntax_error.json", want: []wantEntry{
			{"__tests__/broken.test.js", "errored", ""},
			{"__tests__/pass.test.js > still passes", "passed", ""},
		}, passed: 1, failed: 1},
		{fixture: "no_tests.json", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			got, ok := parseJestJSON(readFrameworkFixture(t, "jest/"+tc.fixture), work)
			if !ok {
				t.Fatal("ok = false, want true")
			}
			if len(got.Tests) != len(tc.want) {
				t.Fatalf("entries = %+v, want %d", got.Tests, len(tc.want))
			}
			for i, w := range tc.want {
				if got.Tests[i].Name != w.name || got.Tests[i].Status != w.status {
					t.Errorf("entry %d = {%q %q}, want {%q %q}", i, got.Tests[i].Name, got.Tests[i].Status, w.name, w.status)
				}
			}
			if got.Passed() != tc.passed || got.Failed() != tc.failed {
				t.Errorf("passed, failed = %d, %d; want %d, %d", got.Passed(), got.Failed(), tc.passed, tc.failed)
			}
		})
	}
	// The file-level errored entry has the jest message.
	syntax, _ := parseJestJSON(readFrameworkFixture(t, "jest/syntax_error.json"), work)
	if msg := syntax.Tests[0].Message; !strings.Contains(msg, "Test suite failed to run") {
		t.Errorf("file-level message = %q, want the start of the jest message", msg)
	}
}

// The jest consistency rule: a changed total, a changed count, a flipped or
// unknown status, trailing data and truncation must all fail.
func TestParseJestJSONConsistencyRule(t *testing.T) {
	allPass := string(readFrameworkFixture(t, "jest/all_pass.json"))
	mixed := string(readFrameworkFixture(t, "jest/mixed.json"))
	tests := []struct {
		name   string
		output string
	}{
		{"numPassedTests changed", strings.Replace(allPass, `"numPassedTests":2`, `"numPassedTests":3`, 1)},
		{"numFailedTests changed", strings.Replace(mixed, `"numFailedTests":1`, `"numFailedTests":2`, 1)},
		{"numTotalTests changed", strings.Replace(mixed, `"numTotalTests":4`, `"numTotalTests":5`, 1)},
		{"one passed assertion reported as failed", strings.Replace(allPass, `"status":"passed","title":"joins strings"`, `"status":"failed","title":"joins strings"`, 1)},
		{"unknown assertion status", strings.Replace(allPass, `"status":"passed","title":"joins strings"`, `"status":"unknown","title":"joins strings"`, 1)},
		{"truncated report", allPass[:len(allPass)-40]},
		{"trailing data", allPass + "x"},
		{"not a report", `{"unrelated":true}`},
		{"empty output", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := parseJestJSON([]byte(tc.output), "/WORK/jestproj"); ok {
				t.Fatal("ok = true, want false: the parser must not guess")
			}
		})
	}
}

// junitReportXML builds a pytest JUnit report with passed passing tests and
// failed failing tests. Used for the cap and large-suite tests.
func junitReportXML(passed, failed int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="utf-8"?><testsuites><testsuite name="pytest" errors="0" failures="%d" skipped="0" tests="%d">`, failed, passed+failed)
	for i := 0; i < passed; i++ {
		fmt.Fprintf(&b, `<testcase classname="tests.test_big" name="test_pass_%d" time="0.000"/>`, i)
	}
	for i := 0; i < failed; i++ {
		fmt.Fprintf(&b, `<testcase classname="tests.test_big" name="test_fail_%d" time="0.000"><failure message="boom %d"/></testcase>`, i, i)
	}
	b.WriteString(`</testsuite></testsuites>`)
	return []byte(b.String())
}

// jestReportJSON builds a jest --json report with passed passing and failed
// failing assertions in one file under root.
func jestReportJSON(root string, passed, failed int) []byte {
	var assertions []string
	for i := 0; i < passed; i++ {
		assertions = append(assertions, fmt.Sprintf(`{"ancestorTitles":[],"fullName":"pass %d","failureMessages":[],"status":"passed","title":"pass %d"}`, i, i))
	}
	for i := 0; i < failed; i++ {
		assertions = append(assertions, fmt.Sprintf(`{"ancestorTitles":[],"fullName":"fail %d","failureMessages":["boom"],"status":"failed","title":"fail %d"}`, i, i))
	}
	status := "passed"
	if failed > 0 {
		status = "failed"
	}
	return []byte(fmt.Sprintf(`{"numTotalTests":%d,"numPassedTests":%d,"numFailedTests":%d,"testResults":[{"name":%q,"status":%q,"message":"","assertionResults":[%s]}]}`,
		passed+failed, passed, failed, root+"/big.test.js", status, strings.Join(assertions, ",")))
}

// Entries are capped at maxPerTestEntries. Passed entries are dropped past the
// cap, failed entries are always kept, and the consistency check uses the
// full counts.
func TestPerTestEntryCap(t *testing.T) {
	const passed = maxPerTestEntries + 50
	t.Run("pytest passed past the cap", func(t *testing.T) {
		got, ok := parsePytestJUnit(junitReportXML(passed, 0))
		if !ok {
			t.Fatal("ok = false, want true: the cap must not change the consistency check")
		}
		if len(got.Tests) != maxPerTestEntries {
			t.Fatalf("entries = %d, want %d", len(got.Tests), maxPerTestEntries)
		}
	})
	t.Run("pytest failure past the cap is kept", func(t *testing.T) {
		got, ok := parsePytestJUnit(junitReportXML(passed, 1))
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if len(got.Tests) != maxPerTestEntries+1 || got.Tests[len(got.Tests)-1].Status != "failed" {
			t.Fatalf("entries = %d, want %d with the failed one last", len(got.Tests), maxPerTestEntries+1)
		}
	})
	t.Run("jest passed past the cap", func(t *testing.T) {
		got, ok := parseJestJSON(jestReportJSON("/WORK", passed, 0), "/WORK")
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if len(got.Tests) != maxPerTestEntries {
			t.Fatalf("entries = %d, want %d", len(got.Tests), maxPerTestEntries)
		}
	})
	t.Run("jest failure past the cap is kept", func(t *testing.T) {
		got, ok := parseJestJSON(jestReportJSON("/WORK", passed, 1), "/WORK")
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if len(got.Tests) != maxPerTestEntries+1 || got.Failed() != 1 {
			t.Fatalf("entries = %d, failed = %d; want %d, 1", len(got.Tests), got.Failed(), maxPerTestEntries+1)
		}
	})
}

func TestReportMessageKeepsTheFirstCharacters(t *testing.T) {
	long := strings.Repeat("é", maxReportMessageRunes+100)
	if n := len([]rune(reportMessage("  " + long + "  "))); n != maxReportMessageRunes {
		t.Fatalf("runes = %d, want %d", n, maxReportMessageRunes)
	}
	got := reportMessage("Expected: 3\n" + strings.Repeat("x", 500))
	if !strings.HasPrefix(got, "Expected: 3") || len([]rune(got)) > maxReportMessageRunes {
		t.Errorf("message = %.40q, want the first characters", got)
	}
}

// The report file size cap: a file over the cap is not read.
func TestReadCappedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.xml")
	writeTestRunnerFixture(t, path, "0123456789A")
	if _, ok := readCappedFile(path, 10); ok {
		t.Error("an 11-byte file with a 10-byte cap must not be read")
	}
	writeTestRunnerFixture(t, path, "0123456789")
	if _, ok := readCappedFile(path, 10); !ok {
		t.Error("a 10-byte file with a 10-byte cap must be read")
	}
	if _, ok := readCappedFile(filepath.Join(dir, "missing.xml"), 10); ok {
		t.Error("a missing file must not be read")
	}
}

func TestReadPerTestReportOversize(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, maxReportBytes+1)
	if err := os.WriteFile(filepath.Join(dir, pytestReportFile), big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPerTestReport(perTestPytest, dir, ""); ok {
		t.Fatal("a report over the size cap must not be parsed")
	}
	if _, ok := readPerTestReport(perTestPytest, "", ""); ok {
		t.Fatal("no report directory must not be parsed")
	}
}

func TestDetectPerTestRunner(t *testing.T) {
	noPackage := t.TempDir()
	jestPackage := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(jestPackage, "package.json"), `{"scripts":{"test":"jest --coverage"}}`)
	tests := []struct {
		name string
		cmd  []string
		dir  string
		want perTestRunner
	}{
		{"go test", []string{"go", "test", "./..."}, noPackage, perTestGo},
		{"pytest", []string{"pytest", "-q"}, noPackage, perTestPytest},
		{"py.test", []string{"py.test"}, noPackage, perTestPytest},
		{"python -m pytest", []string{"python", "-m", "pytest"}, noPackage, perTestPytest},
		{"python3 -m pytest", []string{"python3", "-m", "pytest", "-x"}, noPackage, perTestPytest},
		{"python3.11 -m pytest", []string{"/usr/bin/python3.11", "-m", "pytest"}, noPackage, perTestPytest},
		{"uv run pytest", []string{"uv", "run", "pytest"}, noPackage, perTestNone},
		{"poetry run pytest", []string{"poetry", "run", "pytest"}, noPackage, perTestNone},
		{"tox", []string{"tox"}, noPackage, perTestNone},
		{"make test", []string{"make", "test"}, noPackage, perTestNone},
		{"python unittest", []string{"python", "-m", "unittest"}, noPackage, perTestNone},
		{"python script", []string{"python", "{tests}"}, noPackage, perTestNone},
		{"jest", []string{"jest"}, noPackage, perTestJest},
		{"jest with args", []string{"jest", "--coverage"}, noPackage, perTestJest},
		{"npx jest", []string{"npx", "jest"}, noPackage, perTestJest},
		{"yarn jest", []string{"yarn", "jest"}, noPackage, perTestJest},
		{"pnpm jest", []string{"pnpm", "jest"}, noPackage, perTestJest},
		{"pnpm exec jest", []string{"pnpm", "exec", "jest"}, noPackage, perTestJest},
		{"bunx jest", []string{"bunx", "jest"}, noPackage, perTestJest},
		{"npx vitest", []string{"npx", "vitest"}, noPackage, perTestNone},
		{"npm test without package.json", []string{"npm", "test"}, noPackage, perTestNone},
		{"npm test with jest script", []string{"npm", "test"}, jestPackage, perTestJest},
		{"npm run test with jest script", []string{"npm", "run", "test"}, jestPackage, perTestJest},
		{"npm test with extra args", []string{"npm", "test", "--", "--ci"}, jestPackage, perTestJest},
		{"yarn test with jest script", []string{"yarn", "test"}, jestPackage, perTestJest},
		{"pnpm test with jest script", []string{"pnpm", "test"}, jestPackage, perTestJest},
		{"npm run build with jest script", []string{"npm", "run", "build"}, jestPackage, perTestNone},
		{"empty workdir", []string{"npm", "test"}, "", perTestNone},
		{"empty command", nil, noPackage, perTestNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectPerTestRunner(tc.cmd, tc.dir); got != tc.want {
				t.Fatalf("runner = %v, want %v", got, tc.want)
			}
		})
	}
}

// package.json script shapes. Only a plain jest script counts; any read or
// parse problem means "not jest".
func TestDetectJestPackageScriptShapes(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"jest", `{"scripts":{"test":"jest"}}`, true},
		{"jest with flags", `{"scripts":{"test":"jest --coverage"}}`, true},
		{"chained with &&", `{"scripts":{"test":"jest && eslint ."}}`, false},
		{"chained with ||", `{"scripts":{"test":"jest || true"}}`, false},
		{"semicolon", `{"scripts":{"test":"jest; echo done"}}`, false},
		{"pipe", `{"scripts":{"test":"jest | tee out.txt"}}`, false},
		{"background", `{"scripts":{"test":"jest & eslint ."}}`, false},
		{"vitest", `{"scripts":{"test":"vitest run"}}`, false},
		{"node script", `{"scripts":{"test":"node test.js"}}`, false},
		{"no test script", `{"scripts":{"build":"tsc"}}`, false},
		{"script is not a string", `{"scripts":{"test":["jest"]}}`, false},
		{"invalid json", `{"scripts":`, false},
		{"not an object", `[]`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestRunnerFixture(t, filepath.Join(dir, "package.json"), tc.content)
			if got := isJestScriptCommand([]string{"npm", "test"}, dir); got != tc.want {
				t.Fatalf("isJestScriptCommand = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		if isJestScriptCommand([]string{"npm", "test"}, t.TempDir()) {
			t.Fatal("a missing package.json must not count as jest")
		}
	})
	t.Run("over the size cap", func(t *testing.T) {
		dir := t.TempDir()
		big := `{"scripts":{"test":"jest"},"pad":"` + strings.Repeat("x", maxPackageJSONBytes) + `"}`
		writeTestRunnerFixture(t, filepath.Join(dir, "package.json"), big)
		if isJestScriptCommand([]string{"npm", "test"}, dir) {
			t.Fatal("a package.json over the size cap must not count as jest")
		}
	})
}

// The injection rules. The caller's argv must never be modified.
func TestReportConflictAndInjection(t *testing.T) {
	const dir = "/REPORT"
	tests := []struct {
		name     string
		kind     perTestRunner
		cmd      []string
		conflict bool
		want     []string
	}{
		{"pytest gets the junit report", perTestPytest, []string{"pytest", "-q"}, false,
			[]string{"pytest", "-q", "--junitxml=/REPORT/report.xml", "-o", "junit_family=xunit2"}},
		{"python -m pytest gets the junit report", perTestPytest, []string{"python3", "-m", "pytest"}, false,
			[]string{"python3", "-m", "pytest", "--junitxml=/REPORT/report.xml", "-o", "junit_family=xunit2"}},
		{"pytest with -r flags is not a conflict", perTestPytest, []string{"pytest", "-rf"}, false,
			[]string{"pytest", "-rf", "--junitxml=/REPORT/report.xml", "-o", "junit_family=xunit2"}},
		{"user --junitxml=x", perTestPytest, []string{"pytest", "--junitxml=mine.xml"}, true, nil},
		{"user --junitxml x", perTestPytest, []string{"pytest", "--junitxml", "mine.xml"}, true, nil},
		{"user --junit-xml=x", perTestPytest, []string{"pytest", "--junit-xml=mine.xml"}, true, nil},
		{"user -p no:junitxml", perTestPytest, []string{"pytest", "-p", "no:junitxml"}, true, nil},
		{"user -pno:junitxml", perTestPytest, []string{"pytest", "-pno:junitxml"}, true, nil},
		{"jest direct gets json and outputFile", perTestJest, []string{"npx", "jest"}, false,
			[]string{"npx", "jest", "--json", "--outputFile=/REPORT/report.json"}},
		{"jest with --json adds only outputFile", perTestJest, []string{"jest", "--json"}, false,
			[]string{"jest", "--json", "--outputFile=/REPORT/report.json"}},
		{"user --outputFile", perTestJest, []string{"jest", "--outputFile=mine.json"}, true, nil},
		{"user --outputFile with a space", perTestJest, []string{"jest", "--outputFile", "mine.json"}, true, nil},
		{"npm test gets -- --json and outputFile", perTestJest, []string{"npm", "test"}, false,
			[]string{"npm", "test", "--", "--json", "--outputFile=/REPORT/report.json"}},
		{"npm test with -- keeps user args first", perTestJest, []string{"npm", "test", "--", "--coverage"}, false,
			[]string{"npm", "test", "--", "--coverage", "--json", "--outputFile=/REPORT/report.json"}},
		{"npm test with -- --json adds only outputFile", perTestJest, []string{"npm", "test", "--", "--json"}, false,
			[]string{"npm", "test", "--", "--json", "--outputFile=/REPORT/report.json"}},
		{"pnpm test gets -- --json and outputFile", perTestJest, []string{"pnpm", "test"}, false,
			[]string{"pnpm", "test", "--", "--json", "--outputFile=/REPORT/report.json"}},
		{"yarn test gets --json and outputFile", perTestJest, []string{"yarn", "test"}, false,
			[]string{"yarn", "test", "--json", "--outputFile=/REPORT/report.json"}},
		{"pytest with a bare --", perTestPytest, []string{"pytest", "--", "tests/x.py"}, true, nil},
		{"direct jest with a bare --", perTestJest, []string{"npx", "jest", "--", "foo.test.js"}, true, nil},
		{"npm test -- --ci keeps the user args", perTestJest, []string{"npm", "test", "--", "--ci"}, false,
			[]string{"npm", "test", "--", "--ci", "--json", "--outputFile=/REPORT/report.json"}},
		{"npm test with a user outputFile", perTestJest, []string{"npm", "test", "--", "--outputFile=x.json"}, true, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.cmd)
			if got := reportConflict(tc.kind, tc.cmd); got != tc.conflict {
				t.Fatalf("conflict = %v, want %v", got, tc.conflict)
			}
			if tc.conflict {
				return
			}
			got := withReportFlags(tc.kind, tc.cmd, dir)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("withReportFlags = %#v, want %#v", got, tc.want)
			}
			if !slices.Equal(tc.cmd, original) {
				t.Fatal("the input command was modified")
			}
		})
	}
}

var reportArgPattern = regexp.MustCompile(`(?:--junitxml=|--outputFile=)([^'\s]+)`)

// reportPathOf finds the report path in a command the stage sends: the
// shell-quoted string of the RunTool path, or the argv of the RecordCommand
// path.
func reportPathOf(command any) string {
	switch c := command.(type) {
	case string:
		if m := reportArgPattern.FindStringSubmatch(c); m != nil && filepath.IsAbs(m[1]) {
			return m[1]
		}
	case []string:
		for _, arg := range c {
			for _, prefix := range []string{"--junitxml=", "--outputFile="} {
				if p := strings.TrimPrefix(arg, prefix); strings.HasPrefix(arg, prefix) && filepath.IsAbs(p) {
					return p
				}
			}
		}
	}
	return ""
}

// reportRun describes one stubbed test run. report is written to the report
// path the command names, when it is not nil. The stub never writes stdout
// that matters: Splice reads the report file.
type reportRun struct {
	command  []string
	workDir  string
	report   []byte
	stdout   string
	exitCode int
	// executed, when set, receives the command string the RunTool path sends.
	executed *string
}

// runReportStage runs the test runner through a stubbed RunTool. It fails the
// test when the report directory is still on disk after Run returns.
func runReportStage(t *testing.T, rr reportRun) (schemas.HarnessStageOutput, schemas.TestRunResults) {
	t.Helper()
	var reportPath string
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		WorkDir: rr.workDir,
		Command: rr.command,
		RunTool: func(_ context.Context, _ string, args map[string]any) (ToolResult, error) {
			reportPath = reportPathOf(args["command"])
			if rr.executed != nil {
				*rr.executed, _ = args["command"].(string)
			}
			if rr.report != nil && reportPath != "" {
				writeTestRunnerFixture(t, reportPath, string(rr.report))
			}
			return ToolResult{OK: true, Output: rr.stdout, Meta: map[string]string{"exit_code": strconv.Itoa(rr.exitCode)}}, nil
		},
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results, ok := out.Data["test_results"].(schemas.TestRunResults)
	if !ok {
		t.Fatalf("test_results = %#v, want schemas.TestRunResults", out.Data["test_results"])
	}
	if err := results.Validate(); err != nil {
		t.Fatalf("results do not validate: %v", err)
	}
	if reportPath != "" {
		if _, err := os.Lstat(filepath.Dir(reportPath)); !os.IsNotExist(err) {
			t.Errorf("report directory %s still exists after Run returns (stat err %v)", filepath.Dir(reportPath), err)
		}
	}
	return out, results
}

// jestReportFor returns a jest fixture with the /WORK/jestproj project root
// replaced by work, so the file names come out relative to work.
func jestReportFor(t *testing.T, fixture string, work string) []byte {
	t.Helper()
	return bytes.ReplaceAll(readFrameworkFixture(t, "jest/"+fixture), []byte("/WORK/jestproj"), []byte(work))
}

func TestTestRunnerPytestRuns(t *testing.T) {
	junitAll := readFrameworkFixture(t, "pytest/all_pass.xml")
	t.Run("all pass is per-test and verifies", func(t *testing.T) {
		out, results := runReportStage(t, reportRun{command: []string{"python3", "-m", "pytest"}, workDir: t.TempDir(), report: junitAll, exitCode: 0})
		if results.Evidence != schemas.TestEvidencePerTest || results.Passed() != 4 || results.Failed() != 0 {
			t.Fatalf("evidence = %q, passed = %d, failed = %d", results.Evidence, results.Passed(), results.Failed())
		}
		if out.Confidence != 1.0 || out.Summary != "Test command passed." {
			t.Errorf("confidence, summary = %v, %q", out.Confidence, out.Summary)
		}
		if !slices.Equal(results.Command, []string{"python3", "-m", "pytest"}) {
			t.Errorf("recorded command = %#v, want the user argv without the report flags", results.Command)
		}
	})
	t.Run("failure keeps the real node id", func(t *testing.T) {
		out, results := runReportStage(t, reportRun{command: []string{"pytest", "-q"}, workDir: t.TempDir(), report: readFrameworkFixture(t, "pytest/mixed.xml"), exitCode: 1})
		if results.Evidence != schemas.TestEvidencePerTest || results.Failed() != 2 {
			t.Fatalf("evidence = %q, failed = %d; want per_test and 2 (failed + errored)", results.Evidence, results.Failed())
		}
		found := false
		for _, tc := range results.Tests {
			if tc.Name == "tests.test_mix::test_fail" && tc.Status == "failed" {
				found = true
			}
		}
		if !found {
			t.Errorf("tests = %+v, want the failed node id", results.Tests)
		}
		if out.Confidence != 0.8 || !strings.HasPrefix(out.Summary, "Test command failed with exit code 1.") {
			t.Errorf("confidence, summary = %v, %q", out.Confidence, out.Summary)
		}
	})
	t.Run("teardown error is a second entry", func(t *testing.T) {
		_, results := runReportStage(t, reportRun{command: []string{"pytest"}, workDir: t.TempDir(), report: readFrameworkFixture(t, "pytest/teardown_error.xml"), exitCode: 1})
		if results.Failed() != 3 {
			t.Errorf("failed = %d, want 3", results.Failed())
		}
	})
	t.Run("exit 5 with no tests is not a failure", func(t *testing.T) {
		out, results := runReportStage(t, reportRun{command: []string{"pytest"}, workDir: t.TempDir(), report: readFrameworkFixture(t, "pytest/no_tests.xml"), exitCode: 5})
		if results.Failed() != 0 || results.Evidence != schemas.TestEvidenceNone {
			t.Fatalf("failed = %d, evidence = %q; want 0 and none", results.Failed(), results.Evidence)
		}
		if len(results.Tests) != 1 || results.Tests[0].Name != "suite" || results.Tests[0].Status != "skipped" || results.Tests[0].Message != "no tests ran" {
			t.Errorf("tests = %+v, want one skipped suite entry", results.Tests)
		}
		if out.Confidence != 0.5 || out.Summary != "Test command ran no tests." {
			t.Errorf("confidence, summary = %v, %q", out.Confidence, out.Summary)
		}
	})
	t.Run("exit 0 with a failed entry is not a pass", func(t *testing.T) {
		out, results := runReportStage(t, reportRun{command: []string{"pytest"}, workDir: t.TempDir(), report: readFrameworkFixture(t, "pytest/mixed.xml"), exitCode: 0})
		if results.Failed() == 0 || out.Summary != "Test command exited 0, but failed tests were reported." {
			t.Errorf("failed = %d, summary = %q", results.Failed(), out.Summary)
		}
	})
	t.Run("exit 1 with only passed entries gets a failed suite entry", func(t *testing.T) {
		_, results := runReportStage(t, reportRun{command: []string{"pytest"}, workDir: t.TempDir(), report: junitAll, exitCode: 1})
		last := results.Tests[len(results.Tests)-1]
		if results.Passed() != 4 || last.Name != "suite" || last.Status != "failed" || last.Message != "test command exited 1; no failed test was reported" {
			t.Errorf("tests = %+v, want 4 passed and a failed suite entry", results.Tests)
		}
	})
	t.Run("timeout path is unchanged", func(t *testing.T) {
		out, results := runReportStage(t, reportRun{command: []string{"pytest"}, workDir: t.TempDir(), report: junitAll, exitCode: 124})
		if results.Evidence != schemas.TestEvidenceNone || results.Tests[0].Status != "errored" || out.Confidence != 0.3 {
			t.Errorf("evidence = %q, tests = %+v, confidence = %v", results.Evidence, results.Tests, out.Confidence)
		}
	})
	t.Run("no report written stays command_only", func(t *testing.T) {
		out, results := runReportStage(t, reportRun{command: []string{"pytest"}, workDir: t.TempDir(), report: nil, exitCode: 0})
		if results.Evidence != schemas.TestEvidenceCommandOnly || out.Confidence != 0.7 {
			t.Fatalf("evidence = %q, confidence = %v; want command_only and 0.7", results.Evidence, out.Confidence)
		}
		if out.Summary != "Test command passed. Per-test evidence is not available." {
			t.Errorf("summary = %q", out.Summary)
		}
	})
	t.Run("unparseable report stays command_only", func(t *testing.T) {
		_, results := runReportStage(t, reportRun{command: []string{"pytest"}, workDir: t.TempDir(), report: []byte("<testsuites>"), exitCode: 0})
		if results.Evidence != schemas.TestEvidenceCommandOnly {
			t.Errorf("evidence = %q, want command_only", results.Evidence)
		}
	})
	t.Run("user --junitxml is not injected or read", func(t *testing.T) {
		_, results := runReportStage(t, reportRun{command: []string{"pytest", "--junitxml=mine.xml"}, workDir: t.TempDir(), report: junitAll, exitCode: 0})
		if results.Evidence != schemas.TestEvidenceCommandOnly || !slices.Equal(results.Command, []string{"pytest", "--junitxml=mine.xml"}) {
			t.Errorf("evidence = %q, command = %#v; want command_only and the command unchanged", results.Evidence, results.Command)
		}
	})
	t.Run("bare -- is left alone", func(t *testing.T) {
		_, results := runReportStage(t, reportRun{command: []string{"pytest", "--", "tests/x.py"}, workDir: t.TempDir(), report: junitAll, exitCode: 0})
		if results.Evidence != schemas.TestEvidenceCommandOnly || !slices.Equal(results.Command, []string{"pytest", "--", "tests/x.py"}) {
			t.Errorf("evidence = %q, command = %#v; want command_only and the command unchanged", results.Evidence, results.Command)
		}
	})
	t.Run("record-command path injects the flags and reads the report", func(t *testing.T) {
		var sent map[string]any
		out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
			WorkDir: t.TempDir(),
			Command: []string{"pytest"},
			RecordCommand: func(_ context.Context, _ string, args map[string]any, _ func(context.Context) (ToolResult, error)) (ToolResult, error) {
				sent = args
				writeTestRunnerFixture(t, reportPathOf(args["command"]), string(junitAll))
				payload, err := json.Marshal(schemas.TestRunResults{Command: args["command"].([]string), Stdout: "ok"})
				if err != nil {
					return ToolResult{}, err
				}
				return ToolResult{OK: true, Output: string(payload)}, nil
			},
		})
		if err != nil {
			t.Fatalf("stage run: %v", err)
		}
		if reportPathOf(sent["command"]) == "" {
			t.Fatalf("recorded args command = %#v, want the report flag", sent["command"])
		}
		results := out.Data["test_results"].(schemas.TestRunResults)
		if results.Evidence != schemas.TestEvidencePerTest || out.Confidence != 1.0 {
			t.Errorf("evidence = %q, confidence = %v", results.Evidence, out.Confidence)
		}
	})
}

func TestTestRunnerJestRuns(t *testing.T) {
	t.Run("all pass is per-test", func(t *testing.T) {
		work := t.TempDir()
		out, results := runReportStage(t, reportRun{command: []string{"npx", "jest"}, workDir: work, report: jestReportFor(t, "all_pass.json", work), exitCode: 0})
		if results.Evidence != schemas.TestEvidencePerTest || results.Passed() != 2 {
			t.Fatalf("evidence = %q, passed = %d", results.Evidence, results.Passed())
		}
		if results.Tests[0].Name != "__tests__/pass.test.js > adds numbers" {
			t.Errorf("name = %q, want the path relative to the work directory", results.Tests[0].Name)
		}
		if out.Confidence != 1.0 {
			t.Errorf("confidence = %v", out.Confidence)
		}
	})
	t.Run("failure keeps the real name", func(t *testing.T) {
		work := t.TempDir()
		out, results := runReportStage(t, reportRun{command: []string{"jest"}, workDir: work, report: jestReportFor(t, "mixed.json", work), exitCode: 1})
		want := "__tests__/mix.test.js > math fails on purpose"
		if results.Failed() != 1 || results.Tests[1].Name != want || results.Tests[1].Status != "failed" {
			t.Fatalf("tests = %+v, want the failed test %q", results.Tests, want)
		}
		if out.Confidence != 0.8 {
			t.Errorf("confidence = %v, want 0.8", out.Confidence)
		}
	})
	t.Run("zero tests is no tests ran", func(t *testing.T) {
		work := t.TempDir()
		out, results := runReportStage(t, reportRun{command: []string{"npx", "jest", "--passWithNoTests"}, workDir: work, report: jestReportFor(t, "no_tests.json", work), exitCode: 0})
		if results.Evidence != schemas.TestEvidenceNone || results.Failed() != 0 || out.Confidence != 0.5 {
			t.Errorf("evidence = %q, failed = %d, confidence = %v", results.Evidence, results.Failed(), out.Confidence)
		}
		if out.Summary != "Test command passed. No tests ran." {
			t.Errorf("summary = %q", out.Summary)
		}
	})
	t.Run("file-level failure is an errored entry", func(t *testing.T) {
		work := t.TempDir()
		_, results := runReportStage(t, reportRun{command: []string{"npx", "jest"}, workDir: work, report: jestReportFor(t, "syntax_error.json", work), exitCode: 1})
		if results.Tests[0].Name != "__tests__/broken.test.js" || results.Tests[0].Status != "errored" {
			t.Errorf("tests = %+v, want the errored file", results.Tests)
		}
	})
	t.Run("npm test with a jest script gets -- --json and reads the report", func(t *testing.T) {
		work := t.TempDir()
		writeTestRunnerFixture(t, filepath.Join(work, "package.json"), `{"scripts":{"test":"jest"}}`)
		out, results := runReportStage(t, reportRun{command: []string{"npm", "test"}, workDir: work, report: jestReportFor(t, "all_pass.json", work), exitCode: 0})
		if !slices.Equal(results.Command, []string{"npm", "test"}) {
			t.Errorf("recorded command = %#v, want the user argv", results.Command)
		}
		if results.Evidence != schemas.TestEvidencePerTest || out.Confidence != 1.0 {
			t.Errorf("evidence = %q, confidence = %v", results.Evidence, out.Confidence)
		}
	})
	t.Run("yarn test with a jest script gets --json", func(t *testing.T) {
		work := t.TempDir()
		writeTestRunnerFixture(t, filepath.Join(work, "package.json"), `{"scripts":{"test":"jest"}}`)
		_, results := runReportStage(t, reportRun{command: []string{"yarn", "test"}, workDir: work, report: jestReportFor(t, "all_pass.json", work), exitCode: 0})
		if !slices.Equal(results.Command, []string{"yarn", "test"}) || results.Evidence != schemas.TestEvidencePerTest {
			t.Errorf("command = %#v, evidence = %q", results.Command, results.Evidence)
		}
	})
	t.Run("npm test with a non-jest script is untouched", func(t *testing.T) {
		work := t.TempDir()
		writeTestRunnerFixture(t, filepath.Join(work, "package.json"), `{"scripts":{"test":"node test.js"}}`)
		_, results := runReportStage(t, reportRun{command: []string{"npm", "test"}, workDir: work, report: nil, stdout: "all good\n", exitCode: 0})
		if results.Evidence != schemas.TestEvidenceCommandOnly || !slices.Equal(results.Command, []string{"npm", "test"}) {
			t.Errorf("evidence = %q, command = %#v", results.Evidence, results.Command)
		}
	})
	t.Run("direct jest with a bare -- is left alone", func(t *testing.T) {
		work := t.TempDir()
		_, results := runReportStage(t, reportRun{command: []string{"npx", "jest", "--", "foo.test.js"}, workDir: work, report: jestReportFor(t, "all_pass.json", work), exitCode: 0})
		if results.Evidence != schemas.TestEvidenceCommandOnly || !slices.Equal(results.Command, []string{"npx", "jest", "--", "foo.test.js"}) {
			t.Errorf("evidence = %q, command = %#v; want command_only and the command unchanged", results.Evidence, results.Command)
		}
	})
	t.Run("npm test -- --ci still gets the report flags", func(t *testing.T) {
		work := t.TempDir()
		writeTestRunnerFixture(t, filepath.Join(work, "package.json"), `{"scripts":{"test":"jest"}}`)
		_, results := runReportStage(t, reportRun{command: []string{"npm", "test", "--", "--ci"}, workDir: work, report: jestReportFor(t, "all_pass.json", work), exitCode: 0})
		if !slices.Equal(results.Command, []string{"npm", "test", "--", "--ci"}) {
			t.Errorf("recorded command = %#v, want the user argv", results.Command)
		}
		if results.Evidence != schemas.TestEvidencePerTest {
			t.Errorf("evidence = %q, want per_test", results.Evidence)
		}
	})
	t.Run("user outputFile stays command_only", func(t *testing.T) {
		work := t.TempDir()
		_, results := runReportStage(t, reportRun{command: []string{"jest", "--outputFile=r.json"}, workDir: work, report: jestReportFor(t, "all_pass.json", work), exitCode: 0})
		if results.Evidence != schemas.TestEvidenceCommandOnly {
			t.Errorf("evidence = %q, want command_only", results.Evidence)
		}
	})
	t.Run("missing report stays command_only", func(t *testing.T) {
		out, results := runReportStage(t, reportRun{command: []string{"npx", "jest"}, workDir: t.TempDir(), report: nil, stdout: "PASS a.test.js\n", exitCode: 0})
		if results.Evidence != schemas.TestEvidenceCommandOnly || out.Confidence != 0.7 {
			t.Errorf("evidence = %q, confidence = %v", results.Evidence, out.Confidence)
		}
	})
}

// The stdout is not read for pytest or jest. A large suite gives a truncated
// stdout (as the bash tool emits for big output) and a full report file. The
// report counts, the failing names and the entry cap must all hold.
func TestLargeSuiteReadsTheReportNotStdout(t *testing.T) {
	const truncated = "....... [output truncated: 32 KiB budget, head and tail kept] ........"
	const passed, failed = 5000, 3
	wantEntries := maxPerTestEntries + failed
	t.Run("pytest", func(t *testing.T) {
		_, results := runReportStage(t, reportRun{command: []string{"python3", "-m", "pytest"}, workDir: t.TempDir(), report: junitReportXML(passed, failed), stdout: truncated, exitCode: 1})
		if results.Evidence != schemas.TestEvidencePerTest || results.Failed() != failed {
			t.Fatalf("evidence = %q, failed = %d", results.Evidence, results.Failed())
		}
		if len(results.Tests) != wantEntries {
			t.Errorf("entries = %d, want %d (cap plus the failures)", len(results.Tests), wantEntries)
		}
		for i := 0; i < failed; i++ {
			want := fmt.Sprintf("tests.test_big::test_fail_%d", i)
			if !slices.ContainsFunc(results.Tests, func(tc schemas.TestCaseResult) bool { return tc.Name == want && tc.Status == "failed" }) {
				t.Errorf("failure %s is missing", want)
			}
		}
	})
	t.Run("jest", func(t *testing.T) {
		work := t.TempDir()
		_, results := runReportStage(t, reportRun{command: []string{"npx", "jest"}, workDir: work, report: jestReportJSON(work, passed, failed), stdout: truncated, exitCode: 1})
		if results.Evidence != schemas.TestEvidencePerTest || results.Failed() != failed {
			t.Fatalf("evidence = %q, failed = %d", results.Evidence, results.Failed())
		}
		if len(results.Tests) != wantEntries {
			t.Errorf("entries = %d, want %d", len(results.Tests), wantEntries)
		}
		if !slices.ContainsFunc(results.Tests, func(tc schemas.TestCaseResult) bool {
			return tc.Name == "big.test.js > fail 2" && tc.Status == "failed"
		}) {
			t.Error("the failing test name is missing")
		}
	})
}

// The real production path: the registry bash tool runs python3 -m pytest
// through the stage sandbox, the report is written into the system temp
// directory, and Splice reads it back. Skipped when pytest or the native
// sandbox backend is not available.
func TestTestRunnerPytestReportThroughRealBash(t *testing.T) {
	if err := exec.Command("python3", "-m", "pytest", "--version").Run(); err != nil {
		t.Skipf("python3 -m pytest is not available: %v", err)
	}
	if backend := sandbox.SelectBackend(sandbox.BackendOptions{}); !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
	work := t.TempDir()
	writeTestRunnerFixture(t, filepath.Join(work, "test_ok.py"), "def test_a():\n    assert True\n\ndef test_b():\n    assert 1 + 1 == 2\n\ndef test_c():\n    assert \"a\" != \"b\"\n")
	engine := procrun.NewStageEngine(work)
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(work))
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		WorkDir: work,
		Command: []string{"python3", "-m", "pytest"},
		Sandbox: engine,
		RunTool: func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			res := registry.RunWithOptions(ctx, name, args, tools.RunOptions{Sandbox: engine, PermissionGranted: true})
			return ToolResult{OK: res.Status == tools.StatusOK, Output: res.Output, Truncated: res.Truncated, Meta: res.Meta}, nil
		},
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	results := out.Data["test_results"].(schemas.TestRunResults)
	if results.ExitCode != 0 {
		t.Fatalf("exit code = %d, output = %q", results.ExitCode, results.Stdout)
	}
	if results.Evidence != schemas.TestEvidencePerTest || results.Passed() != 3 || results.Failed() != 0 {
		t.Fatalf("evidence = %q, passed = %d, failed = %d, tests = %+v", results.Evidence, results.Passed(), results.Failed(), results.Tests)
	}
}

// runSwapStage runs the test runner. The stub calls swap with the report
// path the command names, so a test can replace the report or its directory
// before the host reads it. It fails the test when the report directory
// remains after Run returns.
func runSwapStage(t *testing.T, command []string, swap func(reportPath string)) schemas.TestRunResults {
	t.Helper()
	var reportPath string
	out, err := (TestRunner{}).Run(context.Background(), schemas.HarnessStageInput{}, nil, StageOptions{
		WorkDir: t.TempDir(),
		Command: command,
		RunTool: func(_ context.Context, _ string, args map[string]any) (ToolResult, error) {
			reportPath = reportPathOf(args["command"])
			if reportPath != "" {
				swap(reportPath)
			}
			return ToolResult{OK: true, Meta: map[string]string{"exit_code": "0"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("stage run: %v", err)
	}
	if reportPath != "" {
		if _, err := os.Lstat(filepath.Dir(reportPath)); !os.IsNotExist(err) {
			t.Errorf("report directory still exists after Run returns (lstat err %v)", err)
		}
	}
	return out.Data["test_results"].(schemas.TestRunResults)
}

// A report swapped for a symbolic link is never read: the host would read a
// file outside the report directory. The symlink tests do not run on Windows.
func TestSymlinkedReportIsNotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests are skipped on Windows")
	}
	fixture := readFrameworkFixture(t, "pytest/all_pass.xml")
	t.Run("report file is a symlink to a valid report", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "host-report.xml")
		writeTestRunnerFixture(t, outside, string(fixture))
		results := runSwapStage(t, []string{"pytest"}, func(path string) {
			if err := os.Symlink(outside, path); err != nil {
				t.Fatalf("setup: symlink: %v", err)
			}
		})
		if results.Evidence != schemas.TestEvidenceCommandOnly {
			t.Fatalf("evidence = %q, want command_only: a symlinked report must not be read", results.Evidence)
		}
	})
	t.Run("report directory is a symlink to a directory with a valid report", func(t *testing.T) {
		results := runSwapStage(t, []string{"pytest"}, func(path string) {
			dir := filepath.Dir(path)
			if err := os.RemoveAll(dir); err != nil {
				t.Fatalf("setup: remove dir: %v", err)
			}
			other := t.TempDir()
			writeTestRunnerFixture(t, filepath.Join(other, pytestReportFile), string(fixture))
			if err := os.Symlink(other, dir); err != nil {
				t.Fatalf("setup: symlink dir: %v", err)
			}
		})
		if results.Evidence != schemas.TestEvidenceCommandOnly {
			t.Fatalf("evidence = %q, want command_only: a symlinked report directory must not be read", results.Evidence)
		}
	})
	t.Run("readCappedFile refuses a symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.xml")
		writeTestRunnerFixture(t, target, "0123456789")
		link := filepath.Join(dir, "link.xml")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("setup: symlink: %v", err)
		}
		if _, ok := readCappedFile(link, 100); ok {
			t.Fatal("a symlink must not be read")
		}
		if _, ok := readCappedFile(target, 100); !ok {
			t.Fatal("the regular file must still be read")
		}
	})
}

// The recorded command is the user's argv. The report flags are added only to
// the command that runs, so no temp path reaches the results or the memory.
func TestRecordedTestCommandIsUserArgv(t *testing.T) {
	npmJest := func(t *testing.T) (string, []byte) {
		work := t.TempDir()
		writeTestRunnerFixture(t, filepath.Join(work, "package.json"), `{"scripts":{"test":"jest"}}`)
		return work, jestReportFor(t, "all_pass.json", work)
	}
	pytestReport := func(t *testing.T) (string, []byte) {
		return t.TempDir(), readFrameworkFixture(t, "pytest/all_pass.xml")
	}
	jestReport := func(t *testing.T) (string, []byte) {
		work := t.TempDir()
		return work, jestReportFor(t, "all_pass.json", work)
	}
	cases := []struct {
		name    string
		command []string
		report  func(t *testing.T) (string, []byte)
		// flags are the injected parts that must reach the executed command only.
		flags []string
	}{
		{"pytest direct", []string{"pytest", "-q"}, pytestReport, []string{"--junitxml=", "junit_family"}},
		{"python -m pytest", []string{"python3", "-m", "pytest"}, pytestReport, []string{"--junitxml=", "junit_family"}},
		{"jest direct", []string{"npx", "jest"}, jestReport, []string{"--json", "--outputFile="}},
		{"npm test with a jest script", []string{"npm", "test"}, npmJest, []string{"--json", "--outputFile="}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work, report := tc.report(t)
			var executed string
			out, results := runReportStage(t, reportRun{command: tc.command, workDir: work, report: report, exitCode: 0, executed: &executed})
			if results.Evidence != schemas.TestEvidencePerTest {
				t.Fatalf("evidence = %q, want per_test so the report was read", results.Evidence)
			}
			if !slices.Equal(results.Command, tc.command) {
				t.Errorf("results.Command = %#v, want the user argv %#v", results.Command, tc.command)
			}
			recorded, ok := out.Data["test_command"].([]string)
			if !ok || !slices.Equal(recorded, tc.command) {
				t.Errorf("Data[test_command] = %#v, want the user argv %#v", out.Data["test_command"], tc.command)
			}
			for _, flag := range tc.flags {
				if !strings.Contains(executed, flag) {
					t.Errorf("executed command %q lacks %q", executed, flag)
				}
			}
			if reportPathOf(executed) == "" {
				t.Errorf("executed command %q carries no report path", executed)
			}
			for _, field := range []string{strings.Join(results.Command, " "), strings.Join(recorded, " ")} {
				for _, banned := range append([]string{reportPathOf(executed)}, tc.flags...) {
					if banned != "" && strings.Contains(field, banned) {
						t.Errorf("recorded command %q carries %q", field, banned)
					}
				}
			}
		})
	}
}

// boundedNameRunes is the longest entry name the report keeps, plus "...".
const boundedNameRunes = 303

func junitWithCases(cases []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><testsuites><testsuite name="s" tests="%d" failures="%d" errors="0" skipped="0">`, len(cases), len(cases))
	for i, name := range cases {
		fmt.Fprintf(&b, `<testcase classname="pkg" name="%s%d"><failure message="boom">x</failure></testcase>`, name, i)
	}
	b.WriteString(`</testsuite></testsuites>`)
	return []byte(b.String())
}

func jestWithFailures(count int, title string) []byte {
	var assertions []string
	for i := 0; i < count; i++ {
		assertions = append(assertions, fmt.Sprintf(`{"status":"failed","title":"%s%d","fullName":"%s%d","ancestorTitles":[],"failureMessages":["boom"]}`, title, i, title, i))
	}
	return []byte(fmt.Sprintf(`{"numTotalTests":%d,"numPassedTests":0,"numFailedTests":%d,"testResults":[{"name":"/w/a.test.js","status":"failed","assertionResults":[%s]}]}`,
		count, count, strings.Join(assertions, ",")))
}

// failedAndErrored counts the entries that a report keeps as failures.
func failedAndErrored(results schemas.TestRunResults) (int, string) {
	count, suite := 0, ""
	for _, tc := range results.Tests {
		if tc.Status == "failed" || tc.Status == "errored" {
			count++
		}
		if tc.Name == "suite" {
			suite = tc.Message
		}
	}
	return count, suite
}

func TestPerTestNamesAreBounded(t *testing.T) {
	long := strings.Repeat("n", 3<<20)
	t.Run("pytest", func(t *testing.T) {
		results, ok := parsePytestJUnit(junitWithCases([]string{long}))
		if !ok || len(results.Tests) != 1 {
			t.Fatalf("parsePytestJUnit ok = %v, tests = %d", ok, len(results.Tests))
		}
		if n := utf8.RuneCountInString(results.Tests[0].Name); n > boundedNameRunes {
			t.Fatalf("name is %d runes, want at most %d", n, boundedNameRunes)
		}
		if !strings.HasSuffix(results.Tests[0].Name, "...") {
			t.Fatalf("cut name %q must end with ...", results.Tests[0].Name[len(results.Tests[0].Name)-10:])
		}
	})
	t.Run("jest", func(t *testing.T) {
		results, ok := parseJestJSON(jestWithFailures(1, long), "/w")
		if !ok || len(results.Tests) != 1 {
			t.Fatalf("parseJestJSON ok = %v, tests = %d", ok, len(results.Tests))
		}
		if n := utf8.RuneCountInString(results.Tests[0].Name); n > boundedNameRunes {
			t.Fatalf("name is %d runes, want at most %d", n, boundedNameRunes)
		}
	})
	t.Run("go", func(t *testing.T) {
		stream := strings.Join([]string{
			`{"Action":"run","Package":"example.com/p","Test":"` + long + `"}`,
			`{"Action":"fail","Package":"example.com/p","Test":"` + long + `","Elapsed":0}`,
		}, "\n")
		results, ok := parseGoTestJSON(stream)
		if !ok || len(results.Tests) != 1 {
			t.Fatalf("parseGoTestJSON ok = %v, tests = %d", ok, len(results.Tests))
		}
		if n := utf8.RuneCountInString(results.Tests[0].Name); n > boundedNameRunes {
			t.Fatalf("name is %d runes, want at most %d", n, boundedNameRunes)
		}
	})
}

// The failed-entry cap keeps 200 failures and one suite entry with the count.
// The consistency check still passes on the true count of 1,000.
func TestPerTestFailedEntriesAreCapped(t *testing.T) {
	const total = 1000
	names := make([]string, total)
	for i := range names {
		names[i] = "case"
	}
	pytest, ok := parsePytestJUnit(junitWithCases(names))
	if !ok {
		t.Fatal("parsePytestJUnit must accept 1,000 failures: the consistency check runs before the cap")
	}
	jest, ok := parseJestJSON(jestWithFailures(total, "case"), "/w")
	if !ok {
		t.Fatal("parseJestJSON must accept 1,000 failures: the consistency check runs before the cap")
	}
	stream := make([]string, 0, 2*total)
	for i := 0; i < total; i++ {
		test := fmt.Sprintf("TestCase%d", i)
		stream = append(stream,
			`{"Action":"run","Package":"example.com/p","Test":"`+test+`"}`,
			`{"Action":"fail","Package":"example.com/p","Test":"`+test+`","Elapsed":0}`)
	}
	gor, ok := parseGoTestJSON(strings.Join(stream, "\n"))
	if !ok {
		t.Fatal("parseGoTestJSON rejected 1,000 failing tests")
	}
	for label, results := range map[string]schemas.TestRunResults{"pytest": pytest, "jest": jest, "go": gor} {
		count, suite := failedAndErrored(results)
		if count != 201 {
			t.Errorf("%s: failed or errored entries = %d, want 201 (200 kept and one suite entry)", label, count)
		}
		// 1,000 failures, 200 kept: 800 are not listed. The count is exact.
		if suite != "800 more failed tests are not listed" {
			t.Errorf("%s: suite message = %q, want the count of the dropped tests", label, suite)
		}
	}
}
