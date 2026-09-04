package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/testrunner"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestRunner is the deterministic test runner pipeline stage.
type TestRunner struct{}

var _ Stage = TestRunner{}

func (TestRunner) Capabilities() Capabilities {
	return Capabilities{ModelFree: true, Description: "running tests"}
}

func (TestRunner) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	var cmd []string
	cmd = append([]string(nil), options.Command...)
	if len(cmd) == 0 {
		detected, err := testCommand(options.WorkDir, options.Language)
		if err != nil {
			return schemas.HarnessStageOutput{}, fmt.Errorf("detect test command: %w", err)
		}
		cmd = detected
	}
	if len(cmd) == 0 {
		output := skippedTestOutput(options.Language)
		options.report(output.Detail)
		return output, nil
	}

	timeout := DefaultTimeoutSeconds
	if options.TimeoutSeconds > 0 {
		timeout = options.TimeoutSeconds
	}

	options.report(fmt.Sprintf("running %s in %s", joinShell(cmd), options.WorkDir))
	// A1: run Go tests with -json so per-test results exist for repair
	// evidence and trajectory signals. The suite-entry synthesis below stays
	// only as a fallback when -json is unavailable or parsing fails.
	jsonEligible := isGoTestJSONCommand(cmd) || (len(cmd) > 1 && cmd[0] == "go" && cmd[1] == "test")
	if jsonEligible {
		cmd = withGoTestJSON(cmd)
	}
	var results schemas.TestRunResults
	if options.RunTool != nil {
		bashArgs := map[string]any{
			"command":    shellJoin(cmd),
			"cwd":        options.WorkDir,
			"timeout_ms": timeout * 1000,
		}
		start := time.Now()
		run := func(runCtx context.Context) (ToolResult, error) {
			return options.RunTool(runCtx, "bash", bashArgs)
		}
		var recorded ToolResult
		var err error
		if options.RecordCommand != nil {
			recorded, err = options.RecordCommand(ctx, "splice.test", bashArgs, run)
		} else {
			recorded, err = run(ctx)
		}
		durationMs := int(time.Since(start).Milliseconds())
		if err != nil {
			return schemas.HarnessStageOutput{}, err
		}
		results, err = testResultsFromToolResult(recorded, cmd, timeout, durationMs)
		if err != nil {
			return schemas.HarnessStageOutput{}, err
		}
	} else if options.RecordCommand != nil {
		args := map[string]any{
			"command":         cmd,
			"cwd":             options.WorkDir,
			"timeout_seconds": timeout,
		}
		recorded, err := options.RecordCommand(ctx, "splice.test", args, func(runCtx context.Context) (ToolResult, error) {
			results = runCommand(runCtx, options.Sandbox, cmd, options.WorkDir, timeout)
			payload, marshalErr := json.Marshal(results)
			if marshalErr != nil {
				return ToolResult{OK: false, Output: marshalErr.Error()}, nil
			}
			return ToolResult{OK: results.ExitCode == 0, Output: string(payload)}, nil
		})
		if err != nil {
			return schemas.HarnessStageOutput{}, err
		}
		if recorded.Output != "" {
			var decoded schemas.TestRunResults
			if err := json.Unmarshal([]byte(recorded.Output), &decoded); err == nil {
				results = decoded
			}
		}
	} else {
		results = runCommand(ctx, options.Sandbox, cmd, options.WorkDir, timeout)
	}

	// A1: when the command was JSON-eligible, try per-test parsing first.
	// The suite-entry synthesis below becomes the last-resort fallback when
	// -json output is unavailable or fails to parse.
	var parsedGoJSON bool
	var parsedResults schemas.TestRunResults
	if jsonEligible {
		parsedResults, parsedGoJSON = parseGoTestJSON(results.Stdout)
	}

	var summary string
	var confidence float64
	if results.ExitCode == 0 {
		if parsedGoJSON && len(parsedResults.Tests) > 0 {
			results.Tests = parsedResults.Tests
		} else {
			results.Tests = []schemas.TestCaseResult{{
				Name:       "suite",
				Status:     "passed",
				DurationMs: results.DurationMs,
			}}
		}
		summary = "Test command passed."
		confidence = 1.0
	} else if results.ExitCode == 124 {
		results.Tests = []schemas.TestCaseResult{{
			Name:       "suite",
			Status:     "errored",
			DurationMs: results.DurationMs,
			Message:    fmt.Sprintf("test command timed out after %ds", timeout),
		}}
		summary = fmt.Sprintf("Test command timed out after %ds.", timeout)
		confidence = 0.3
	} else {
		switch {
		case parsedGoJSON && len(parsedResults.Tests) > 0:
			// Per-test (or per-compile-error) entries from go test -json.
			results.Tests = parsedResults.Tests
		default:
			results.Tests = []schemas.TestCaseResult{{
				Name:       "suite",
				Status:     "failed",
				DurationMs: results.DurationMs,
				Message:    fmt.Sprintf("exit code %d", results.ExitCode),
			}}
		}
		summary = fmt.Sprintf("Test command failed with exit code %d.", results.ExitCode)
		evidence := failingEvidenceExcerpt(results.Stdout + "\n" + results.Stderr)
		if evidence == "" && parsedGoJSON {
			// The -json stream wraps compiler output in JSON events, so the
			// raw-line excerpt finds nothing. The parsed compile-error
			// entries are the evidence; rebuild it from them.
			var lines []string
			for _, tc := range results.Tests {
				if tc.Status == "errored" {
					lines = append(lines, tc.Name+": "+tc.Message)
				}
			}
			evidence = truncateEvidenceLines(strings.Join(lines, "\n"))
		}
		if evidence != "" {
			// Surface bounded failure evidence into the revision context: a
			// re-entered writer that only sees "exit code 1" guesses blind.
			summary += "\nFailing evidence:\n" + evidence
		}
		confidence = 0.8
	}

	detail := results.Stdout
	if len(detail) > 500 {
		detail = detail[len(detail)-500:]
	}
	if detail == "" && results.Stderr != "" {
		detail = results.Stderr
		if len(detail) > 500 {
			detail = detail[len(detail)-500:]
		}
	}

	return schemas.HarnessStageOutput{
		Summary:    summary,
		Detail:     detail,
		Confidence: confidence,
		Data:       map[string]any{"test_results": results, "test_command": cmd},
	}, nil
}

func testCommand(workDir string, language string) ([]string, error) {
	if workDir == "" {
		return nil, fmt.Errorf("no work_dir and no command provided")
	}
	checks, err := testrunner.Detect(workDir)
	if err != nil {
		return nil, nil
	}
	// Detect returns checks in a stable order, so the first runnable test check is deterministic.
	for _, c := range checks {
		if c.Kind == testrunner.KindTest && len(c.Command) > 0 {
			return c.Command, nil
		}
	}
	// Keep defaults to runners with a clear workspace convention.
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "go", "golang":
		if _, statErr := os.Stat(filepath.Join(workDir, "go.mod")); statErr == nil {
			return []string{"go", "test", "./..."}, nil
		}
	case "python", "python3":
		return []string{"python", "-m", "pytest"}, nil
	}
	return nil, nil
}

func skippedTestOutput(language string) schemas.HarnessStageOutput {
	reason := "No test command could be detected; verification could not run."
	if language != "" {
		reason = fmt.Sprintf("No test command could be detected for language %q; verification could not run.", language)
	}
	results := schemas.TestRunResults{
		Command: []string{"<no test command detected>"},
		Tests: []schemas.TestCaseResult{{
			Name:    "suite",
			Status:  "skipped",
			Message: reason,
		}},
	}
	return schemas.HarnessStageOutput{
		Summary:    "Verification skipped: no test command could be detected.",
		Detail:     reason,
		Confidence: 0.0,
		Data: map[string]any{
			"test_results":      results,
			"test_command":      nil,
			"known_limitations": []string{reason},
		},
	}
}

func runCommand(ctx context.Context, sandboxEngine *sandbox.Engine, command []string, cwd string, timeoutSeconds int) schemas.TestRunResults {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	start := time.Now()
	inner, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	dir := cwd
	if dir == "" {
		// Preserve the historical default: inherit the process working
		// directory when the caller passes no explicit one.
		dir, _ = os.Getwd()
	}
	cmd, plan, cerr := PrepareStageCommand(inner, sandboxEngine, dir, command)
	if cerr != nil {
		return schemas.TestRunResults{
			Command:  command,
			ExitCode: 1,
			Stderr:   cerr.Error(),
		}
	}
	defer plan.Cleanup()
	stdout, stderr := &limitedWriter{limit: 200_000}, &limitedWriter{limit: 200_000}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	durationMs := int(time.Since(start).Milliseconds())
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else if inner.Err() == context.DeadlineExceeded {
			exitCode = 124
		} else {
			exitCode = 1
		}
	}
	return schemas.TestRunResults{
		Command:    command,
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: durationMs,
	}
}

type limitedWriter struct {
	buf   []byte
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(w.buf) < w.limit {
		w.buf = append(w.buf, p...)
		if len(w.buf) > w.limit {
			w.buf = w.buf[:w.limit]
		}
	}
	return len(p), nil
}

func (w *limitedWriter) String() string {
	return string(w.buf)
}

func joinShell(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	return fmt.Sprintf("%#v", cmd)
}

func shellJoin(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	parts := make([]string, len(cmd))
	for i, arg := range cmd {
		parts[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(parts, " ")
}

func testResultsFromToolResult(res ToolResult, cmd []string, timeoutSeconds, durationMs int) (schemas.TestRunResults, error) {
	exitCode := 0
	if v, ok := res.Meta["exit_code"]; ok {
		if parsed, err := strconv.Atoi(v); err == nil {
			exitCode = parsed
		}
	}
	if !res.OK {
		out := strings.ToLower(res.Output)
		if strings.Contains(out, "permission required") || strings.Contains(out, "permission denied") {
			return schemas.TestRunResults{}, fmt.Errorf("test command denied: %s", res.Output)
		}
		if strings.Contains(res.Output, "timed out") {
			exitCode = 124
		} else if exitCode == 0 {
			// A non-OK result with no exit code (e.g. tool not found) is an
			// execution failure, not a success.
			exitCode = 1
		}
	}
	return schemas.TestRunResults{
		Command:    cmd,
		ExitCode:   exitCode,
		Stdout:     res.Output,
		Stderr:     "",
		DurationMs: durationMs,
	}, nil
}

// maxFailureEvidenceChars bounds the whole excerpt so prompts stay small.
// maxFailureBlocks caps how many distinct failures appear, and
// maxContinuationLinesPerMarker caps the assertion lines that follow each
// failure marker. maxFailureEvidenceTailBias is prepended when earlier
// failures were dropped for space, so the writer knows the list is partial.
const (
	maxFailureEvidenceChars        = 800
	maxFailureBlocks               = 8
	maxContinuationLinesPerMarker  = 4
	failureEvidenceTruncatedNotice = "... earlier failures omitted; see full command output."
)

// failureMarkerPrefixes are the line prefixes runtimes print to announce a
// failed test: Go ("--- FAIL:"), pytest ("FAILED", "E   assertion"), generic
// runners ("FAIL."), and Go build failures ("# pkg" headers plus their
// file:line: compiler error lines).
var failureMarkerPrefixes = []string{"--- FAIL:", "FAILED ", "FAIL.", "E   ", "# ", "./"}

func isFailureMarkerLine(line string) bool {
	// "# pkg" marks a Go build-failure block only when the following lines
	// carry compiler errors; a bare "#" comment line must not match. The
	// "./" prefix matches Go compiler error lines (./file.go:12: undefined: X).
	for _, prefix := range failureMarkerPrefixes {
		if strings.HasPrefix(line, prefix) {
			// A line that is exactly "#" or "# comment" is not a build block.
			if prefix == "# " && !goCompilerErrorLineRe.MatchString(line) {
				continue
			}
			return true
		}
	}
	return false
}

// goCompilerErrorLineRe matches Go compiler diagnostic lines: file.go:12:34:
// message or # pkg headers followed by diagnostics.
var goCompilerErrorLineRe = regexp.MustCompile(`^# \S|\.go:\d+`)

// isEvidenceContinuation reports whether line belongs to the failure block
// above it: indented output, diff lines, or runtime assertion detail.
func isEvidenceContinuation(line string) bool {
	if line == "" || !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") &&
		!strings.HasPrefix(line, "+ ") && !strings.HasPrefix(line, "- ") {
		return false
	}
	return true
}

// failingEvidenceExcerpt extracts the failing-test markers plus their
// following assertion lines from captured runner output. Selection is
// tail-biased because Go prints failures near the end of the run, and the
// whole excerpt is hard-capped so prompts stay small. Empty output yields an
// empty excerpt.
func failingEvidenceExcerpt(output string) string {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.Split(output, "\n")
	var blocks []string
	for i := 0; i < len(lines); {
		if !isFailureMarkerLine(lines[i]) {
			i++
			continue
		}
		block := []string{lines[i]}
		j := i + 1
		for j < len(lines) && len(block) <= maxContinuationLinesPerMarker &&
			!isFailureMarkerLine(lines[j]) && isEvidenceContinuation(lines[j]) {
			block = append(block, lines[j])
			j++
		}
		blocks = append(blocks, strings.Join(block, "\n"))
		i = j
	}
	var kept []string
	used := 0
	for idx := len(blocks) - 1; idx >= 0 && len(kept) < maxFailureBlocks; idx-- {
		cost := len(blocks[idx]) + 1
		if used+cost > maxFailureEvidenceChars {
			continue
		}
		used += cost
		kept = append([]string{blocks[idx]}, kept...)
	}
	if len(kept) < len(blocks) {
		// Any dropped block means the writer sees partial evidence; say so.
		kept = append([]string{failureEvidenceTruncatedNotice}, kept...)
	}
	return strings.Join(kept, "\n")
}
