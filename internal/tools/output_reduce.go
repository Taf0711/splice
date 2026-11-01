package tools

import "strings"

// Native output reduction.
//
// Splice reduces tool output in process: deterministic, no external binary, no
// third-party code. Every reducer follows one rule and is paired with tests
// that pin it:
//
//	Drop redundant content. Never drop a distinct signal.
//
// A reducer reports whether it changed the text. Callers keep the original for
// the spill artifact, so a reduction is deferred content, never lost content.

const (
	goTestRunPrefix  = "=== RUN"
	goTestPassPrefix = "--- PASS:"
	goTestFailPrefix = "--- FAIL:"
	goTestSkipPrefix = "--- SKIP:"

	// minRemovablePassBlocks is how many matched passing tests must be present
	// before a rewrite is worth doing. Small output is not a problem.
	minRemovablePassBlocks = 3
)

// reduceNativeOutput runs the native reducers in order and returns the first
// result that changed the text.
func reduceNativeOutput(text string) (string, bool) {
	if reduced, ok := reduceGoTestVerbose(text); ok {
		return reduced, true
	}
	return text, false
}

// reduceGoTestVerbose removes passing-test blocks from `go test -v` output.
//
// A pass result is removed only when it matches an earlier `=== RUN` line for
// the same test name. That match is the safety property: a passing test may
// print text that looks like a result line, and an unmatched result line is
// therefore kept rather than trusted. The matching `=== RUN` line is removed
// with the result, so no orphan run line is left behind.
//
// Invariant: every line that is not a matched passing-test line, its `=== RUN`
// line, or an indented detail line directly under it survives verbatim and in
// order. A failure, a skip, a panic, a pause, a summary line, and a nested
// result line can never be removed.
//
// Nested results use the same names in `=== RUN`, so subtests reduce too. An
// indented result line starts its own block and stops the detail scan, so a
// passing sibling can never swallow the failing sibling that follows it.
func reduceGoTestVerbose(text string) (string, bool) {
	lines := strings.Split(text, "\n")

	// Index every "=== RUN   Name" line by name. A test name can repeat (a
	// table test reusing a subtest name, or -count), so keep a queue.
	pending := make(map[string][]int)
	for i, line := range lines {
		if name := goTestRunName(line); name != "" {
			pending[name] = append(pending[name], i)
		}
	}

	removed := make([]bool, len(lines))
	removable := 0
	for i := 0; i < len(lines); i++ {
		name := goTestPassName(lines[i])
		if name == "" {
			continue
		}
		queue := pending[name]
		if len(queue) == 0 {
			// No matching run line: this is test output, not a result.
			continue
		}
		runAt := queue[0]
		pending[name] = queue[1:]

		removed[runAt] = true
		removed[i] = true
		removable++

		// Indented lines under a passing result are the test's own detail. Stop
		// at anything that starts its own block, so a nested result or a run
		// line is never swallowed.
		for j := i + 1; j < len(lines) && isIndented(lines[j]); j++ {
			if isGoTestResultLine(lines[j]) || goTestRunName(lines[j]) != "" {
				break
			}
			removed[j] = true
			i = j
		}
	}

	if removable < minRemovablePassBlocks {
		return text, false
	}

	kept := make([]string, 0, len(lines)-removable)
	for i, line := range lines {
		if !removed[i] {
			kept = append(kept, line)
		}
	}
	result := strings.Join(kept, "\n")
	// A reduction that removes everything would read as a failed tool call, so
	// refuse it and keep the original.
	if strings.TrimSpace(result) == "" {
		return text, false
	}
	return result, true
}

// goTestResultName parses "<lead> NAME (duration)" and returns NAME. The
// duration guard keeps ordinary prose that merely starts with the lead out.
// Leading whitespace is allowed because nested results are indented.
func goTestResultName(line, lead string) string {
	s := normalizeLine(line)
	if !strings.HasPrefix(s, lead) {
		return ""
	}
	rest := strings.TrimSpace(s[len(lead):])
	open := strings.LastIndex(rest, " (")
	if open <= 0 || !strings.HasSuffix(rest, ")") {
		return ""
	}
	return rest[:open]
}

func goTestPassName(line string) string { return goTestResultName(line, goTestPassPrefix) }
func goTestFailName(line string) string { return goTestResultName(line, goTestFailPrefix) }
func goTestSkipName(line string) string { return goTestResultName(line, goTestSkipPrefix) }

// isGoTestResultLine reports whether the line is any test result, at any
// indentation. Used to stop a detail scan at a nested result.
func isGoTestResultLine(line string) bool {
	return goTestPassName(line) != "" || goTestFailName(line) != "" || goTestSkipName(line) != ""
}

// goTestRunName parses "=== RUN   NAME" and returns NAME. The whitespace guard
// after the lead stops a longer word such as "=== RUNNING" from matching.
func goTestRunName(line string) string {
	s := normalizeLine(line)
	if !strings.HasPrefix(s, goTestRunPrefix) {
		return ""
	}
	rest := s[len(goTestRunPrefix):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return ""
	}
	return strings.TrimSpace(rest)
}

// isIndented reports whether a line is indented, which marks it as detail under
// a result line.
func isIndented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

// normalizeLine strips a trailing carriage return so CRLF output parses the
// same as LF output, then strips leading whitespace for prefix matching.
func normalizeLine(line string) string {
	return strings.TrimLeft(strings.TrimRight(line, "\r"), " \t")
}
