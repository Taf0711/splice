package stages

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

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

// parseGoTestJSON decodes `go test -json` output into per-test results plus
// build-error entries. It returns (results, ok): ok is false when the output
// is not valid go-test JSON (empty, truncated, or a different format), so the
// caller can keep its suite-entry fallback. Build failures (compile errors)
// become one "build <pkg>" errored entry per package with the compiler error
// lines as the message, and a build failure never reports a passing suite.
func parseGoTestJSON(output string) (schemas.TestRunResults, bool) {
	var results schemas.TestRunResults
	type testState struct {
		output  strings.Builder
		elapsed float64
		done    bool
		skipped bool
		failed  bool
	}
	states := map[string]*testState{}
	var order []string
	var buildMessages []string
	anyEvent := false

	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event goTestEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		anyEvent = true
		switch event.Action {
		case "run", "pass", "fail", "skip", "output":
			if event.Test == "" {
				continue
			}
			state := states[event.Test]
			if state == nil {
				state = &testState{}
				states[event.Test] = state
				order = append(order, event.Test)
			}
			if event.Action == "output" {
				state.output.WriteString(event.Output)
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
	if !anyEvent {
		return results, false
	}

	if len(buildMessages) > 0 {
		// One errored entry per contiguous compiler-error line group, keyed
		// by the "# pkg" header when present.
		results.Tests = append(results.Tests, compileErrorEntries(buildMessages)...)
	}

	for _, name := range order {
		if len(results.Tests) >= maxPerTestEntries {
			break
		}
		state := states[name]
		status := "failed"
		switch {
		case state.skipped:
			status = "skipped"
		case !state.failed:
			status = "passed"
		}
		entry := schemas.TestCaseResult{
			Name:       name,
			Status:     status,
			DurationMs: int(state.elapsed * 1000),
		}
		if message := trimTestOutput(state.output.String()); message != "" {
			entry.Message = message
		}
		results.Tests = append(results.Tests, entry)
	}
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
			Name:    name,
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
