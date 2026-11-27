package stages

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

const (
	// goTestReportFile receives the go test -json stream when the run has a
	// private report directory. The bash tool truncates large output, so the
	// file is the complete stream.
	goTestReportFile = "report.jsonl"
	// maxGoReportBytes caps the report file that is read. A larger file is
	// not parsed and the stdout path is used.
	maxGoReportBytes = 64 << 20
	// maxStoredGoStdoutBytes bounds the report text kept in
	// TestRunResults.Stdout. Only the tail is kept.
	maxStoredGoStdoutBytes = 200_000
	// maxGoOutputEventBytes bounds the output text kept from one go test
	// output event. Only the tail of a test's output reaches its message.
	maxGoOutputEventBytes = 64 << 10
)

// readGoTestReport reads the go test -json report from the private report
// directory. ok is false when there is no directory, or when the file is
// missing, empty, a symbolic link, or over the cap.
func readGoTestReport(dir string) ([]byte, bool) {
	if !isRealDirectory(dir) {
		return nil, false
	}
	data, ok := readCappedFile(filepath.Join(dir, goTestReportFile), maxGoReportBytes)
	if !ok || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// lastBytes returns the last n bytes of s, moved forward to a rune boundary.
func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// goTestEvent is one line of `go test -json` output.
type goTestEvent struct {
	Time        string  `json:"Time"`
	Action      string  `json:"Action"`
	Package     string  `json:"Package"`
	Test        string  `json:"Test"`
	Elapsed     float64 `json:"Elapsed"`
	Output      string  `json:"Output"`
	ImportPath  string  `json:"ImportPath"`
	FailedBuild string  `json:"FailedBuild"`
}

// maxPerTestEntries bounds how many per-test entries one parse can produce.
const maxPerTestEntries = 200

// unfinishedTestMessage is the message of a test that started and did not
// finish.
const unfinishedTestMessage = "test did not finish (timeout or killed)"

// parseGoTestJSON decodes `go test -json` output into per-test results plus
// build-error entries. It returns (results, ok): ok is false when the output
// is not valid go-test JSON (empty, truncated, or a different format), so the
// caller can keep its suite-entry fallback. Build failures (compile errors)
// become one "build <pkg>" errored entry per package with the compiler error
// lines as the message, and a build failure never reports a passing suite.
func parseGoTestJSON(output string) (schemas.TestRunResults, bool) {
	var results schemas.TestRunResults
	// State is keyed by package and test name: the same test name in two
	// packages is two tests.
	type testKey struct{ pkg, test string }
	type testState struct {
		pkg     string
		name    string
		output  strings.Builder
		elapsed float64
		done    bool
		skipped bool
		failed  bool
	}
	states := map[testKey]*testState{}
	var order []testKey
	var buildMessages []string
	anyEvent := false

	// The reader has no per-line cap. The caller bounds the whole output, and
	// a long line must not stop the scan before the terminal events.
	reader := bufio.NewReader(strings.NewReader(output))
	for {
		raw, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return results, false
		}
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "{") {
			var event goTestEvent
			if err := json.Unmarshal([]byte(line), &event); err == nil {
				anyEvent = true
				switch event.Action {
				case "run", "pass", "fail", "skip", "output":
					if event.Test == "" {
						break
					}
					key := testKey{pkg: event.Package, test: event.Test}
					state := states[key]
					if state == nil {
						name := event.Test
						if event.Package != "" {
							name = event.Package + "." + event.Test
						}
						state = &testState{pkg: event.Package, name: name}
						states[key] = state
						order = append(order, key)
					}
					if event.Action == "output" {
						// One huge output event keeps only its tail. The message
						// is trimmed to its last lines anyway.
						state.output.WriteString(lastBytes(event.Output, maxGoOutputEventBytes))
					}
					if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
						state.done = true
						state.elapsed = event.Elapsed
						state.skipped = event.Action == "skip"
						state.failed = event.Action == "fail"
					}
				case "build-output":
					if event.Output != "" {
						buildMessages = append(buildMessages, event.Output)
					}
				case "build-fail":
					pkg := event.ImportPath
					if pkg == "" {
						pkg = "unknown package"
					}
					buildMessages = append(buildMessages, fmt.Sprintf("build failed: %s", pkg))
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	if !anyEvent {
		return results, false
	}

	if len(buildMessages) > 0 {
		// One errored entry per contiguous compiler-error line group, keyed
		// by the "# pkg" header when present.
		results.Tests = append(results.Tests, compileErrorEntries(buildMessages)...)
	}

	for _, key := range order {
		state := states[key]
		// The cap drops only passed and skipped entries: a failed or
		// unfinished test is the evidence and is always kept.
		if len(results.Tests) >= maxPerTestEntries && state.done && !state.failed {
			continue
		}
		message := trimTestOutput(state.output.String())
		status := "failed"
		switch {
		case !state.done:
			// A run event with no terminal pass, fail, or skip: the test
			// binary timed out (-timeout) or was killed. Never a pass.
			status = "errored"
			message = strings.TrimSpace(unfinishedTestMessage + "\n" + message)
		case state.skipped:
			status = "skipped"
		case !state.failed:
			status = "passed"
		}
		entry := schemas.TestCaseResult{
			Name:       BoundEntryName(state.name),
			Package:    state.pkg,
			Status:     status,
			DurationMs: int(state.elapsed * 1000),
			Message:    message,
		}
		results.Tests = append(results.Tests, entry)
	}
	results.Tests = capFailedEntries(results.Tests)
	return results, true
}

// compileErrorEntries groups compiler output into one errored entry per
// package block. A block starts at a "# <pkg>" header (or the first
// file:line: error line) and carries its following error lines as the message.
func compileErrorEntries(messages []string) []schemas.TestCaseResult {
	var entries []schemas.TestCaseResult
	var currentPkg string
	var currentLines []string
	flush := func() {
		if len(currentLines) == 0 {
			return
		}
		name := "build"
		if currentPkg != "" {
			name = "build " + currentPkg
		}
		entries = append(entries, schemas.TestCaseResult{
			Name:    BoundEntryName(name),
			Status:  "errored",
			Message: strings.TrimSpace(strings.Join(currentLines, "\n")),
		})
		currentLines = nil
	}
	for _, message := range messages {
		trimmed := strings.TrimRight(message, "\n")
		if strings.HasPrefix(trimmed, "# ") {
			flush()
			currentPkg = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		}
		if trimmed == "" {
			continue
		}
		currentLines = append(currentLines, trimmed)
	}
	flush()
	return entries
}

// truncateEvidenceLines bounds the rebuilt evidence text to the same budget
// the raw excerpt uses.
func truncateEvidenceLines(s string) string {
	if len(s) <= maxFailureEvidenceChars {
		return s
	}
	return s[len(s)-maxFailureEvidenceChars:]
}

// trimTestOutput keeps the failure-relevant tail of one test's captured
// output, dropping the === RUN header line.
func trimTestOutput(raw string) string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "=== RUN") {
			continue
		}
		if strings.HasPrefix(line, "--- PASS") || strings.HasPrefix(line, "--- FAIL") ||
			strings.HasPrefix(line, "--- SKIP") {
			continue
		}
		lines = append(lines, line)
	}
	joined := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(joined) > 400 {
		joined = joined[len(joined)-400:]
	}
	return joined
}

// isGoTestJSONCommand reports whether the command already asks for JSON
// output. The runner rewrites plain `go test` commands to add -json when the
// caller did not.
func isGoTestJSONCommand(cmd []string) bool {
	if len(cmd) == 0 || cmd[0] != "go" {
		return false
	}
	for _, arg := range cmd {
		if arg == "-json" {
			return true
		}
	}
	return false
}

// withGoTestJSON returns the command with -json added after the "test"
// subcommand (or a copy of the original when -json is already present).
func withGoTestJSON(cmd []string) []string {
	if isGoTestJSONCommand(cmd) {
		return append([]string(nil), cmd...)
	}
	out := make([]string, 0, len(cmd)+1)
	for i, arg := range cmd {
		out = append(out, arg)
		if i == 1 && arg == "test" {
			out = append(out, "-json")
		}
	}
	return out
}

// resultsFromGoTestJSON converts parsed per-test results into a full
// TestRunResults, preserving exit code and captured streams.
func resultsFromGoTestJSON(parsed schemas.TestRunResults, base schemas.TestRunResults) schemas.TestRunResults {
	out := base
	out.Tests = parsed.Tests
	return out
}
