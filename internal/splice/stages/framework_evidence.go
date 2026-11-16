package stages

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// perTestRunner names the test runners whose results Splice reads per test.
type perTestRunner int

const (
	perTestNone perTestRunner = iota
	perTestGo
	perTestPytest
	perTestJest
)

const (
	// maxPackageJSONBytes caps how much of a package.json is read to find the
	// test script.
	maxPackageJSONBytes = 1 << 20
	// maxReportBytes caps a pytest or jest report file. A larger file is not
	// parsed.
	maxReportBytes = 16 << 20
	// maxReportMessageRunes bounds one failure message. The first characters
	// are kept: the assertion is at the top of a pytest or jest message.
	maxReportMessageRunes = 400

	pytestReportFile = "report.xml"
	jestReportFile   = "report.json"
)

// detectPerTestRunner classifies the argv of a test command. workDir is the
// directory the command runs in; it is read only for package-script shapes.
func detectPerTestRunner(cmd []string, workDir string) perTestRunner {
	switch {
	case isGoTestJSONCommand(cmd) || (len(cmd) > 1 && cmd[0] == "go" && cmd[1] == "test"):
		return perTestGo
	case isPytestCommand(cmd):
		return perTestPytest
	case isJestDirectCommand(cmd) || isJestScriptCommand(cmd, workDir):
		return perTestJest
	}
	return perTestNone
}

func argvBase(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	return filepath.Base(cmd[0])
}

var pythonCommandName = regexp.MustCompile(`^python(3(\.[0-9]+)?)?$`)

// isPytestCommand reports whether argv runs pytest directly or as
// `python -m pytest`. Other wrappers (uv, poetry, tox, make) are not pytest.
func isPytestCommand(cmd []string) bool {
	base := argvBase(cmd)
	if base == "pytest" || base == "py.test" {
		return true
	}
	if !pythonCommandName.MatchString(base) {
		return false
	}
	for i := 1; i+1 < len(cmd); i++ {
		if cmd[i] == "-m" && cmd[i+1] == "pytest" {
			return true
		}
	}
	return false
}

// isJestDirectCommand reports whether argv starts jest itself, directly or
// through a package runner.
func isJestDirectCommand(cmd []string) bool {
	tool := argvBase(cmd)
	if tool == "jest" {
		return true
	}
	if len(cmd) < 2 {
		return false
	}
	switch tool {
	case "npx", "bunx", "yarn", "pnpm":
		if cmd[1] == "jest" {
			return true
		}
		return tool == "pnpm" && len(cmd) > 2 && cmd[1] == "exec" && cmd[2] == "jest"
	}
	return false
}

// isJestScriptCommand reports whether argv runs the package test script and
// that script is a plain jest command.
func isJestScriptCommand(cmd []string, workDir string) bool {
	if !isPackageTestInvocation(cmd) {
		return false
	}
	script, ok := packageTestScript(workDir)
	return ok && isJestScript(script)
}

// isPackageTestInvocation reports whether argv is `npm test`, `npm run test`,
// `yarn test` or `pnpm test`, with optional extra arguments.
func isPackageTestInvocation(cmd []string) bool {
	switch argvBase(cmd) {
	case "npm":
		return (len(cmd) >= 2 && cmd[1] == "test") || (len(cmd) >= 3 && cmd[1] == "run" && cmd[2] == "test")
	case "yarn", "pnpm":
		return len(cmd) >= 2 && cmd[1] == "test"
	}
	return false
}

// packageTestScript returns scripts.test from <workDir>/package.json. Any
// read or parse problem reports no script.
func packageTestScript(workDir string) (string, bool) {
	if workDir == "" {
		return "", false
	}
	data, ok := readCappedFile(filepath.Join(workDir, "package.json"), maxPackageJSONBytes)
	if !ok {
		return "", false
	}
	var pkg struct {
		Scripts map[string]any `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", false
	}
	script, ok := pkg.Scripts["test"].(string)
	return script, ok
}

// isJestScript accepts `jest` and `jest <args>` only. Chained or piped
// commands, and a backgrounded jest, are not a plain jest run.
func isJestScript(script string) bool {
	s := strings.TrimSpace(script)
	if s != "jest" && !strings.HasPrefix(s, "jest ") {
		return false
	}
	return !strings.ContainsAny(s, "&|;")
}

// reportConflict reports whether the user argv already controls where the
// runner writes its report, or stops the report. Splice then injects nothing
// and reads nothing. A bare "--" in a direct pytest or jest command makes the
// added flags positional paths, so such a command is left alone. Package
// scripts keep their own "--" handling.
func reportConflict(kind perTestRunner, cmd []string) bool {
	switch kind {
	case perTestPytest:
		if slices.Contains(cmd, "--") {
			return true
		}
		for i, arg := range cmd {
			if arg == "--junitxml" || strings.HasPrefix(arg, "--junitxml=") ||
				arg == "--junit-xml" || strings.HasPrefix(arg, "--junit-xml=") ||
				arg == "-pno:junitxml" {
				return true
			}
			if arg == "-p" && i+1 < len(cmd) && cmd[i+1] == "no:junitxml" {
				return true
			}
		}
	case perTestJest:
		if isJestDirectCommand(cmd) && slices.Contains(cmd, "--") {
			return true
		}
		for _, arg := range cmd {
			if strings.HasPrefix(arg, "--outputFile") {
				return true
			}
		}
	}
	return false
}

// withReportFlags returns a copy of cmd that writes the runner report into
// dir. The caller checks reportConflict first.
//
// pytest writes a JUnit XML report. jest writes its --json document to a file.
// For a package script, the flags go after `--` (npm and pnpm). yarn needs no
// `--`.
func withReportFlags(kind perTestRunner, cmd []string, dir string) []string {
	out := append([]string(nil), cmd...)
	switch kind {
	case perTestPytest:
		return append(out, "--junitxml="+filepath.Join(dir, pytestReportFile), "-o", "junit_family=xunit2")
	case perTestJest:
		outputFile := "--outputFile=" + filepath.Join(dir, jestReportFile)
		if isJestDirectCommand(cmd) {
			if !slices.Contains(cmd, "--json") {
				out = append(out, "--json")
			}
			return append(out, outputFile)
		}
		if argvBase(cmd) != "yarn" && !slices.Contains(out, "--") {
			out = append(out, "--")
		}
		if !slices.Contains(cmd, "--json") {
			out = append(out, "--json")
		}
		return append(out, outputFile)
	}
	return out
}

// readPerTestReport reads and parses the report file that withReportFlags
// pointed the runner at. A missing, unreadable, oversized or unparseable
// report gives ok false.
func readPerTestReport(kind perTestRunner, dir string, workDir string) (schemas.TestRunResults, bool) {
	if !isRealDirectory(dir) {
		return schemas.TestRunResults{}, false
	}
	name := jestReportFile
	if kind == perTestPytest {
		name = pytestReportFile
	}
	data, ok := readCappedFile(filepath.Join(dir, name), maxReportBytes)
	if !ok {
		return schemas.TestRunResults{}, false
	}
	if kind == perTestPytest {
		return parsePytestJUnit(data)
	}
	return parseJestJSON(data, workDir)
}

// isRealDirectory reports whether dir is a directory and not a symbolic link.
// The report directory is created by Splice. The command runs in the sandbox
// and could replace it, so the host checks it again before it reads.
func isRealDirectory(dir string) bool {
	if dir == "" {
		return false
	}
	fi, err := os.Lstat(dir)
	return err == nil && fi.IsDir()
}

// readCappedFile reads a regular file of at most limit bytes. A larger file,
// a missing file, a symbolic link and any read error all give ok false.
//
// The sandboxed command can replace the report with a link to a host file.
// The path is checked with Lstat, the opened file must be the same file
// (os.SameFile), and the read is capped. A swap between the two checks opens a
// different file, so the SameFile check fails.
func readCappedFile(path string, limit int) ([]byte, bool) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, false
	}
	return data, true
}

// applyFrameworkOutcome sets evidence and the summary for a pytest or jest run
// whose report parsed. handled is false when the exit-code branches of
// TestRunner.Run must decide the run (a non-zero exit with parsed entries).
func applyFrameworkOutcome(results *schemas.TestRunResults, parsed schemas.TestRunResults, kind perTestRunner) (string, float64, bool) {
	switch {
	case results.ExitCode == 0 && parsed.Failed() > 0:
		results.Tests = parsed.Tests
		results.Evidence = schemas.TestEvidencePerTest
		return "Test command exited 0, but failed tests were reported.", 0.8, true
	case results.ExitCode == 0 && parsed.Passed() > 0:
		results.Tests = parsed.Tests
		results.Evidence = schemas.TestEvidencePerTest
		return "Test command passed.", 1.0, true
	case results.ExitCode == 0:
		return noTestsOutcome(results, parsed, "Test command passed. No tests ran.")
	case len(parsed.Tests) == 0 && kind == perTestPytest && results.ExitCode == 5:
		// pytest exit 5: no test was collected. That is not a failure.
		return noTestsOutcome(results, parsed, "Test command ran no tests.")
	}
	return "", 0, false
}

// noTestsOutcome records a run that executed no passing test. With no entries
// it gets the "no tests ran" suite entry and no evidence. Skipped entries
// are kept as per-test evidence.
func noTestsOutcome(results *schemas.TestRunResults, parsed schemas.TestRunResults, summary string) (string, float64, bool) {
	if len(parsed.Tests) == 0 {
		results.Tests = []schemas.TestCaseResult{{
			Name:       "suite",
			Status:     "skipped",
			DurationMs: results.DurationMs,
			Message:    "no tests ran",
		}}
		results.Evidence = schemas.TestEvidenceNone
	} else {
		results.Tests = parsed.Tests
		results.Evidence = schemas.TestEvidencePerTest
	}
	return summary, 0.5, true
}

// maxEntryNameRunes bounds one per-test entry name. A longer name is cut and
// ends with "...".
const maxEntryNameRunes = 300

// maxFailedEntries bounds the failed and errored entries one report keeps.
const maxFailedEntries = 200

// BoundEntryName cuts a per-test name to maxEntryNameRunes runes. It never
// cuts a rune in half, and it appends "..." when it cuts.
func BoundEntryName(name string) string {
	runes := []rune(name)
	if len(runes) <= maxEntryNameRunes {
		return name
	}
	return string(runes[:maxEntryNameRunes]) + "..."
}

// capFailedEntries keeps the first maxFailedEntries failed or errored entries.
// The rest become one errored "suite" entry that states how many were not
// listed. Passed and skipped entries are not changed.
func capFailedEntries(tests []schemas.TestCaseResult) []schemas.TestCaseResult {
	failed := 0
	for _, tc := range tests {
		if tc.Status == "failed" || tc.Status == "errored" {
			failed++
		}
	}
	if failed <= maxFailedEntries {
		return tests
	}
	kept := make([]schemas.TestCaseResult, 0, maxFailedEntries+len(tests)-failed+1)
	seen, dropped := 0, 0
	for _, tc := range tests {
		if tc.Status == "failed" || tc.Status == "errored" {
			if seen >= maxFailedEntries {
				dropped++
				continue
			}
			seen++
		}
		kept = append(kept, tc)
	}
	return append(kept, schemas.TestCaseResult{
		Name:    "suite",
		Status:  "errored",
		Message: fmt.Sprintf("%d more failed tests are not listed", dropped),
	})
}

// appendPerTestEntry applies the entry cap. It drops only passed and skipped
// entries once the cap is reached; failed and errored entries are kept.
func appendPerTestEntry(tests []schemas.TestCaseResult, entry schemas.TestCaseResult) []schemas.TestCaseResult {
	if len(tests) >= maxPerTestEntries && (entry.Status == "passed" || entry.Status == "skipped") {
		return tests
	}
	return append(tests, entry)
}

// reportMessage trims a message and keeps its first maxReportMessageRunes
// characters. It never cuts a character in half.
func reportMessage(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > maxReportMessageRunes {
		r = r[:maxReportMessageRunes]
	}
	return strings.TrimSpace(string(r))
}

// junitSuite is one <testsuite> element of a pytest JUnit report. The count
// attributes are pointers so a missing attribute is detected.
type junitSuite struct {
	Tests    *string         `xml:"tests,attr"`
	Failures *string         `xml:"failures,attr"`
	Errors   *string         `xml:"errors,attr"`
	Skipped  *string         `xml:"skipped,attr"`
	Cases    []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Failures  []junitOutput `xml:"failure"`
	Errors    []junitOutput `xml:"error"`
	Skipped   []junitOutput `xml:"skipped"`
}

// junitOutput is the message of a failure, error or skip child element.
type junitOutput struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

func (o junitOutput) text() string {
	if o.Message != "" {
		return reportMessage(o.Message)
	}
	return reportMessage(o.Text)
}

// parsePytestJUnit reads a pytest JUnit XML report (pytest --junitxml with
// -o junit_family=xunit2). The root is <testsuites> or a bare <testsuite>.
// Each <testcase> is one entry. ok is false for any XML error, trailing data,
// a missing or non-numeric count attribute, or a count that does not match
// the entries. An empty report (tests="0") is ok with zero entries.
//
// pytest writes one <testcase> per outcome. A test that fails and then errors
// in teardown gives two <testcase> elements with the same name, one with a
// <failure> child and one with an <error> child. Each <testcase> has one
// child in pytest 9 output, so the counts match. A testcase with two outcome
// children is not counted fully, so the counts do not match and the parse
// fails.
func parsePytestJUnit(data []byte) (schemas.TestRunResults, bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	root, ok := nextStartElement(dec)
	if !ok {
		return schemas.TestRunResults{}, false
	}
	var suites []junitSuite
	switch root.Name.Local {
	case "testsuites":
		var doc struct {
			Suites []junitSuite `xml:"testsuite"`
		}
		if err := dec.DecodeElement(&doc, &root); err != nil {
			return schemas.TestRunResults{}, false
		}
		suites = doc.Suites
	case "testsuite":
		var suite junitSuite
		if err := dec.DecodeElement(&suite, &root); err != nil {
			return schemas.TestRunResults{}, false
		}
		suites = []junitSuite{suite}
	default:
		return schemas.TestRunResults{}, false
	}
	if !onlyTrailingNoise(dec) || len(suites) == 0 {
		return schemas.TestRunResults{}, false
	}

	var want, got [4]int // tests, failures, errors, skipped
	var tests []schemas.TestCaseResult
	for _, s := range suites {
		for i, value := range []*string{s.Tests, s.Failures, s.Errors, s.Skipped} {
			if value == nil {
				return schemas.TestRunResults{}, false
			}
			n, err := strconv.Atoi(strings.TrimSpace(*value))
			if err != nil || n < 0 {
				return schemas.TestRunResults{}, false
			}
			want[i] += n
		}
		for _, c := range s.Cases {
			entry := junitEntry(c)
			got[0]++
			switch entry.Status {
			case "failed":
				got[1]++
			case "errored":
				got[2]++
			case "skipped":
				got[3]++
			}
			tests = appendPerTestEntry(tests, entry)
		}
	}
	if got != want {
		return schemas.TestRunResults{}, false
	}
	return schemas.TestRunResults{Tests: capFailedEntries(tests)}, true
}

// junitEntry maps one <testcase>. An <error> child wins over <failure>, and
// <failure> over <skipped>.
func junitEntry(c junitTestcase) schemas.TestCaseResult {
	name := c.Name
	if c.Classname != "" {
		name = c.Classname + "::" + c.Name
	}
	entry := schemas.TestCaseResult{Name: BoundEntryName(name), Status: "passed"}
	switch {
	case len(c.Errors) > 0:
		entry.Status, entry.Message = "errored", c.Errors[0].text()
	case len(c.Failures) > 0:
		entry.Status, entry.Message = "failed", c.Failures[0].text()
	case len(c.Skipped) > 0:
		entry.Status, entry.Message = "skipped", c.Skipped[0].text()
	}
	return entry
}

// nextStartElement returns the first element of the document. Only
// declarations, comments and whitespace may come before it.
func nextStartElement(dec *xml.Decoder) (xml.StartElement, bool) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, true
		case xml.ProcInst, xml.Comment:
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return xml.StartElement{}, false
			}
		default:
			return xml.StartElement{}, false
		}
	}
}

// onlyTrailingNoise reports whether the rest of the document is whitespace,
// comments and declarations only.
func onlyTrailingNoise(dec *xml.Decoder) bool {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		switch t := tok.(type) {
		case xml.ProcInst, xml.Comment:
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return false
			}
		default:
			return false
		}
	}
}

// jestRunDocument is the jest --json report document.
type jestRunDocument struct {
	NumTotalTests  *int              `json:"numTotalTests"`
	NumPassedTests *int              `json:"numPassedTests"`
	NumFailedTests *int              `json:"numFailedTests"`
	TestResults    *[]jestFileResult `json:"testResults"`
}

type jestFileResult struct {
	Name             string                `json:"name"`
	Status           string                `json:"status"`
	Message          string                `json:"message"`
	AssertionResults []jestAssertionResult `json:"assertionResults"`
}

type jestAssertionResult struct {
	AncestorTitles  []string `json:"ancestorTitles"`
	Title           string   `json:"title"`
	FullName        string   `json:"fullName"`
	Status          string   `json:"status"`
	FailureMessages []string `json:"failureMessages"`
}

// parseJestJSON reads a jest --json report. The whole data is one JSON
// document; trailing data fails the decode. workDir makes file names relative
// when they are inside it. ok is false unless the entry counts match the
// document totals: passed and failed against numPassedTests and
// numFailedTests, and all assertions against numTotalTests.
func parseJestJSON(data []byte, workDir string) (schemas.TestRunResults, bool) {
	var doc jestRunDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return schemas.TestRunResults{}, false
	}
	if doc.NumTotalTests == nil || doc.NumPassedTests == nil || doc.NumFailedTests == nil || doc.TestResults == nil {
		return schemas.TestRunResults{}, false
	}

	assertions := 0
	for _, file := range *doc.TestResults {
		assertions += len(file.AssertionResults)
	}
	if assertions != *doc.NumTotalTests {
		return schemas.TestRunResults{}, false
	}

	var tests []schemas.TestCaseResult
	passed, failed := 0, 0
	for _, file := range *doc.TestResults {
		fileName := jestDisplayPath(file.Name, workDir)
		if file.Status == "failed" && len(file.AssertionResults) == 0 {
			// The file did not run (for example a syntax error).
			tests = appendPerTestEntry(tests, schemas.TestCaseResult{
				Name:    BoundEntryName(fileName),
				Status:  "errored",
				Message: reportMessage(file.Message),
			})
			continue
		}
		for _, assertion := range file.AssertionResults {
			status, ok := jestStatus(assertion.Status)
			if !ok {
				return schemas.TestRunResults{}, false
			}
			switch status {
			case "passed":
				passed++
			case "failed":
				failed++
			}
			tests = appendPerTestEntry(tests, schemas.TestCaseResult{
				Name:    BoundEntryName(fileName + " > " + jestFullName(assertion)),
				Status:  status,
				Message: reportMessage(strings.Join(assertion.FailureMessages, "\n")),
			})
		}
	}
	if passed != *doc.NumPassedTests || failed != *doc.NumFailedTests {
		return schemas.TestRunResults{}, false
	}
	return schemas.TestRunResults{Tests: capFailedEntries(tests)}, true
}

func jestStatus(status string) (string, bool) {
	switch status {
	case "passed":
		return "passed", true
	case "failed":
		return "failed", true
	case "pending", "skipped", "todo", "disabled":
		return "skipped", true
	}
	return "", false
}

func jestFullName(a jestAssertionResult) string {
	if a.FullName != "" {
		return a.FullName
	}
	return strings.Join(append(append([]string(nil), a.AncestorTitles...), a.Title), " > ")
}

// jestDisplayPath returns the file path relative to workDir when the file is
// inside it. Otherwise it returns the path as jest reported it.
func jestDisplayPath(file string, workDir string) string {
	if workDir == "" {
		return file
	}
	rel, err := filepath.Rel(workDir, file)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return file
	}
	return filepath.ToSlash(rel)
}
