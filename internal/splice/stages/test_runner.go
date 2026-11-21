package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
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
	configuredCommand := len(cmd) > 0
	commandWorkDir := options.WorkDir
	if len(cmd) > 0 && options.CommandWorkDir != "" {
		commandWorkDir = options.CommandWorkDir
		var err error
		configured := cmd
		cmd, err = expandConfiguredTestCommand(cmd, input.RunChangedFiles, options.WorkDir, commandWorkDir)
		if err != nil {
			return schemas.HarnessStageOutput{}, err
		}
		if !expandsToTests(configured, cmd) {
			output := unmatchedTestOutput(configured)
			options.report(output.Detail)
			return output, nil
		}
	}
	if len(cmd) == 0 && !configuredCommand {
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

	options.report(fmt.Sprintf("running %s in %s", joinShell(cmd), commandWorkDir))
	// A1: run Go tests with -json so per-test results exist for repair
	// evidence and trajectory signals. The suite-entry synthesis below stays
	// only as a fallback when -json is unavailable or parsing fails.
	// pytest and jest runs write a report file into a private directory
	// instead. Splice reads the file after the run, never the stdout.
	runner := detectPerTestRunner(cmd, commandWorkDir)
	jsonEligible := runner == perTestGo
	// recordedArgv is the argv that results and memory report. It is the
	// user's argv, plus the Go -json flag as the Go path has always recorded
	// it. execArgv is the argv that runs: recordedArgv plus any report flags.
	// The report flags never reach recordedArgv.
	recordedArgv := cmd
	execArgv := cmd
	var reportDir string
	defer func() {
		if reportDir != "" {
			_ = os.RemoveAll(reportDir)
		}
	}()
	switch runner {
	case perTestGo:
		recordedArgv = withGoTestJSON(cmd)
		execArgv = recordedArgv
		// The -json stream goes to a report file. The bash tool keeps only the
		// head and tail of a large output. If the directory cannot be made, the
		// run keeps the stdout path.
		if dir, err := os.MkdirTemp("", "splice-test-report-"); err == nil {
			reportDir = dir
		}
	case perTestPytest, perTestJest:
		if !reportConflict(runner, cmd) {
			// If the directory cannot be made, the run keeps command_only evidence.
			if dir, err := os.MkdirTemp("", "splice-test-report-"); err == nil {
				reportDir = dir
				execArgv = withReportFlags(runner, cmd, dir)
			}
		}
	}
	// goReport is the file the go test -json stream is written to. Empty when
	// the run has no report directory.
	var goReport string
	if runner == perTestGo && reportDir != "" {
		goReport = filepath.Join(reportDir, goTestReportFile)
	}
	var results schemas.TestRunResults
	if options.RunTool != nil {
		bashCommand := shellJoin(execArgv)
		if goReport != "" {
			// stderr stays on the tool output. Only the stream goes to the file.
			bashCommand += " > " + shellJoin([]string{goReport})
		}
		bashArgs := map[string]any{
			"command":    bashCommand,
			"cwd":        commandWorkDir,
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
		results, err = testResultsFromToolResult(recorded, recordedArgv, timeout, durationMs)
		if err != nil {
			return schemas.HarnessStageOutput{}, err
		}
	} else if options.RecordCommand != nil {
		args := map[string]any{
			"command":         execArgv,
			"cwd":             commandWorkDir,
			"timeout_seconds": timeout,
		}
		recorded, err := options.RecordCommand(ctx, "splice.test", args, func(runCtx context.Context) (ToolResult, error) {
			results = runCommand(runCtx, options.Sandbox, execArgv, commandWorkDir, timeout, goReport)
			results.Command = recordedArgv
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
		results = runCommand(ctx, options.Sandbox, execArgv, commandWorkDir, timeout, goReport)
		results.Command = recordedArgv
	}

	// A1: when the command was JSON-eligible, try per-test parsing first.
	// The suite-entry synthesis below becomes the last-resort fallback when
	// -json output is unavailable or fails to parse.
	var parsedPerTest bool
	var parsedResults schemas.TestRunResults
	if jsonEligible {
		// The report file is the complete stream. Without it (missing, unsafe
		// or oversized) the stdout that the run captured is parsed as before.
		stream := results.Stdout
		if data, ok := readGoTestReport(reportDir); ok {
			stream = string(data)
			results.Stdout = lastBytes(stream, maxStoredGoStdoutBytes)
		}
		parsedResults, parsedPerTest = parseGoTestJSON(stream)
	} else if results.ExitCode != 124 {
		parsedResults, parsedPerTest = readPerTestReport(runner, reportDir, commandWorkDir)
	}

	var summary string
	var confidence float64
	frameworkSummary, frameworkConfidence, handled := "", 0.0, false
	if parsedPerTest && (runner == perTestPytest || runner == perTestJest) && results.ExitCode != 124 {
		frameworkSummary, frameworkConfidence, handled = applyFrameworkOutcome(&results, parsedResults, runner)
	}
	if handled {
		summary, confidence = frameworkSummary, frameworkConfidence
	} else if results.ExitCode == 0 {
		// An exit code proves only that the command succeeded. A "passed"
		// entry comes only from per-test evidence (go test -json).
		switch {
		case parsedPerTest && len(parsedResults.Tests) > 0:
			results.Tests = parsedResults.Tests
			results.Evidence = schemas.TestEvidencePerTest
			summary = "Test command passed."
			confidence = 1.0
		case jsonEligible:
			results.Tests = []schemas.TestCaseResult{{
				Name:       "suite",
				Status:     "skipped",
				DurationMs: results.DurationMs,
				Message:    "no tests ran",
			}}
			results.Evidence = schemas.TestEvidenceNone
			summary = "Test command passed. No tests ran."
			confidence = 0.5
		default:
			results.Tests = []schemas.TestCaseResult{{
				Name:       "suite",
				Status:     "skipped",
				DurationMs: results.DurationMs,
				Message:    "command succeeded; per-test evidence unavailable",
			}}
			results.Evidence = schemas.TestEvidenceCommandOnly
			summary = "Test command passed. Per-test evidence is not available."
			confidence = 0.7
		}
	} else if results.ExitCode == 124 {
		results.Evidence = schemas.TestEvidenceNone
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
		case parsedPerTest && len(parsedResults.Tests) > 0:
			// Per-test (or per-compile-error) entries from go test -json.
			results.Tests = parsedResults.Tests
			results.Evidence = schemas.TestEvidencePerTest
			if results.Failed() == 0 {
				// Every reported test passed but the command failed (for
				// example TestMain calls os.Exit(1)). The exit code wins.
				results.Tests = append(results.Tests, schemas.TestCaseResult{
					Name:       "suite",
					Status:     "failed",
					DurationMs: results.DurationMs,
					Message:    fmt.Sprintf("test command exited %d; no failed test was reported", results.ExitCode),
				})
			}
		default:
			results.Tests = []schemas.TestCaseResult{{
				Name:       "suite",
				Status:     "failed",
				DurationMs: results.DurationMs,
				Message:    fmt.Sprintf("exit code %d", results.ExitCode),
			}}
			results.Evidence = schemas.TestEvidenceCommandOnly
		}
		marked := markAlreadyFailing(ctx, &results, options, recordedArgv, commandWorkDir)
		summary = fmt.Sprintf("Test command failed with exit code %d.", results.ExitCode)
		var evidence string
		if marked {
			// The raw output also holds the tests that failed before the run.
			// The evidence keeps the new failures only.
			evidence = newFailureEvidence(results)
		} else {
			evidence = failingEvidenceExcerpt(results.Stdout + "\n" + results.Stderr)
			if evidence == "" && parsedPerTest {
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
		}
		if evidence != "" {
			// Surface bounded failure evidence into the revision context: a
			// re-entered writer that only sees "exit code 1" guesses blind.
			summary += "\nFailing evidence:\n" + evidence
		}
		if note := alreadyFailingNote(results); note != "" {
			summary += "\n" + note
		}
		confidence = 0.8
	}

	if results.ExitCode == 0 {
		// A passing command can still report failed tests (a pytest or jest
		// report with failures). Those are compared with the baseline too.
		markAlreadyFailing(ctx, &results, options, recordedArgv, commandWorkDir)
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
		Data:       map[string]any{"test_results": results, "test_command": recordedArgv},
	}, nil
}

// markAlreadyFailing compares the run's failing tests with the pre-run
// baseline. It runs only for per-test evidence with a failing test, and it
// returns true when at least one failure was marked as already failing.
func markAlreadyFailing(ctx context.Context, results *schemas.TestRunResults, options StageOptions, argv []string, dir string) bool {
	if options.TestBaseline == nil || results.Evidence != schemas.TestEvidencePerTest || results.Failed() == 0 {
		return false
	}
	baseline, ok := options.TestBaseline(ctx, options, argv, dir)
	if !ok {
		return false
	}
	return schemas.MarkAlreadyFailing(results, baseline) > 0
}

// newFailureEvidence lists the new failing tests as "name: message" lines,
// bounded like the other failure evidence.
func newFailureEvidence(results schemas.TestRunResults) string {
	var lines []string
	for _, tc := range results.Tests {
		if tc.Status == "failed" || tc.Status == "errored" {
			if !tc.AlreadyFailing {
				lines = append(lines, tc.Name+": "+tc.Message)
			}
		}
	}
	return truncateEvidenceLines(strings.Join(lines, "\n"))
}

// alreadyFailingNote names the failures that were already in the baseline.
// It returns "" when none were.
func alreadyFailingNote(results schemas.TestRunResults) string {
	n := results.FailedBefore()
	if n == 0 {
		return ""
	}
	var names []string
	for _, tc := range results.Tests {
		if tc.AlreadyFailing && len(names) < 10 {
			names = append(names, tc.Name)
		}
	}
	list := strings.Join(names, ", ")
	if n > len(names) {
		list += fmt.Sprintf(", and %d more", n-len(names))
	}
	return fmt.Sprintf("%d test(s) already failing before this run (not caused by it): %s", n, list)
}

// maxConfiguredTestPaths caps the paths that the test placeholders expand to.
const maxConfiguredTestPaths = 20

// expandConfiguredTestCommand expands whole-argument placeholders from the
// test files for this run. See configuredTestPaths for the selection.
func expandConfiguredTestCommand(command []string, changedByStage map[string][]string, workspaceRoot, testDir string) ([]string, error) {
	if testDir == "" {
		testDir = workspaceRoot
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	base, err := filepath.Abs(testDir)
	if err != nil {
		return nil, fmt.Errorf("resolve test directory: %w", err)
	}
	relBase, err := filepath.Rel(root, base)
	if err != nil || relBase == ".." || strings.HasPrefix(relBase, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("verify test directory %q is outside workspace %q", testDir, workspaceRoot)
	}

	paths := configuredTestPaths(changedByStage, root, base)
	tests := make([]string, len(paths))
	modules := make([]string, len(paths))
	for i, file := range paths {
		tests[i] = file
		modules[i] = moduleArg(file)
	}

	expanded := make([]string, 0, len(command)+len(paths))
	for _, arg := range command {
		switch arg {
		case "{tests}":
			expanded = append(expanded, tests...)
		case "{test_modules}":
			expanded = append(expanded, modules...)
		default:
			expanded = append(expanded, arg)
		}
	}
	return expanded, nil
}

// expandsToTests reports whether a command with a test placeholder still has
// test paths after expansion. A command without a placeholder always does.
func expandsToTests(configured, expanded []string) bool {
	placeholders := 0
	for _, arg := range configured {
		if arg == "{tests}" || arg == "{test_modules}" {
			placeholders++
		}
	}
	return placeholders == 0 || len(expanded) > len(configured)-placeholders
}

// unmatchedTestOutput is the result of a configured test command that does not
// run because no test file matches the change. It records no evidence and no
// tests ran, so the pass is not verified and no repair starts.
func unmatchedTestOutput(command []string) schemas.HarnessStageOutput {
	placeholder := "{test_modules}"
	if slices.Contains(command, "{tests}") {
		placeholder = "{tests}"
	}
	summary := fmt.Sprintf("No test file matches the change, so the configured test command did not run (it uses %s).", placeholder)
	results := schemas.TestRunResults{
		Command:  append([]string(nil), command...),
		Evidence: schemas.TestEvidenceNone,
		Tests: []schemas.TestCaseResult{{
			Name:    "suite",
			Status:  "skipped",
			Message: "no tests ran",
		}},
	}
	return schemas.HarnessStageOutput{
		Summary:    summary,
		Detail:     summary,
		Confidence: 0.0,
		Data: map[string]any{
			"test_results":      results,
			"test_command":      nil,
			"known_limitations": []string{summary},
		},
	}
}

// configuredTestPaths lists the changed test files (sorted), then the existing
// test files that match a changed source file by naming convention (sorted).
// Duplicates are dropped, and the list is capped at maxConfiguredTestPaths, so
// the cap never drops a changed test file for a related one.
func configuredTestPaths(changedByStage map[string][]string, workspaceRoot, testDir string) []string {
	seen := map[string]bool{}
	paths := make([]string, 0)
	for _, group := range [][]string{
		changedTestFiles(changedByStage, workspaceRoot, testDir),
		relatedTestFiles(changedByStage, workspaceRoot, testDir),
	} {
		for _, file := range group {
			if seen[file] {
				continue
			}
			seen[file] = true
			paths = append(paths, file)
		}
	}
	if len(paths) > maxConfiguredTestPaths {
		paths = paths[:maxConfiguredTestPaths]
	}
	return paths
}

// changedWorkspaceFiles returns the changed files that exist as regular files
// inside the workspace, as slash paths relative to it. Each file appears once.
func changedWorkspaceFiles(changedByStage map[string][]string, workspaceRoot string) []string {
	stageNames := make([]string, 0, len(changedByStage))
	for stage := range changedByStage {
		stageNames = append(stageNames, stage)
	}
	sort.Strings(stageNames)
	seen := map[string]bool{}
	files := make([]string, 0)
	for _, stage := range stageNames {
		for _, changed := range changedByStage[stage] {
			relPath := filepath.ToSlash(filepath.Clean(changed))
			if relPath == "." || filepath.IsAbs(changed) || strings.HasPrefix(relPath, "../") || relPath == ".." {
				continue
			}
			absolute := filepath.Join(workspaceRoot, filepath.FromSlash(relPath))
			// A file removed by a rollback or a delete must not reach the command.
			if info, serr := os.Stat(absolute); serr != nil || !info.Mode().IsRegular() {
				continue
			}
			if seen[relPath] {
				continue
			}
			seen[relPath] = true
			files = append(files, relPath)
		}
	}
	return files
}

// insideTestDir returns the slash path of absolute relative to testDir. ok is
// false when absolute lies outside testDir.
func insideTestDir(testDir, absolute string) (string, bool) {
	rel, err := filepath.Rel(testDir, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func changedTestFiles(changedByStage map[string][]string, workspaceRoot, testDir string) []string {
	paths := make([]string, 0)
	for _, relPath := range changedWorkspaceFiles(changedByStage, workspaceRoot) {
		if !isTestFilePath(relPath) {
			continue
		}
		relToTestDir, ok := insideTestDir(testDir, filepath.Join(workspaceRoot, filepath.FromSlash(relPath)))
		if !ok {
			continue
		}
		paths = append(paths, testArg(relToTestDir))
	}
	sort.Strings(paths)
	return paths
}

// testArg is the command argument for one test file, relative to testDir. A Go
// file names its package directory: go test on a single _test.go file cannot
// build the package, because the package sources are missing from the build.
func testArg(relToTestDir string) string {
	if path.Ext(relToTestDir) != ".go" {
		return relToTestDir
	}
	dir := path.Dir(relToTestDir)
	if dir == "." {
		return "."
	}
	return "./" + dir
}

// moduleArg is the {test_modules} form of one argument. A Python file becomes
// a dotted module name. A Go package argument stays as it is.
func moduleArg(arg string) string {
	if arg == "." || strings.HasPrefix(arg, "./") {
		return arg
	}
	module := strings.TrimSuffix(arg, ".py")
	return strings.ReplaceAll(module, "/", ".")
}

// relatedTestFiles returns the existing test files that match a changed source
// file by naming convention. Each candidate must be a regular file inside
// testDir.
func relatedTestFiles(changedByStage map[string][]string, workspaceRoot, testDir string) []string {
	seen := map[string]bool{}
	paths := make([]string, 0)
	for _, relPath := range changedWorkspaceFiles(changedByStage, workspaceRoot) {
		if isTestFilePath(relPath) {
			continue
		}
		for _, candidate := range relatedTestCandidates(relPath) {
			absolute := filepath.Join(workspaceRoot, filepath.FromSlash(candidate))
			if info, err := os.Stat(absolute); err != nil || !info.Mode().IsRegular() {
				continue
			}
			relToTestDir, ok := insideTestDir(testDir, absolute)
			if !ok || seen[relToTestDir] {
				continue
			}
			seen[relToTestDir] = true
			paths = append(paths, testArg(relToTestDir))
		}
	}
	sort.Strings(paths)
	return paths
}

// relatedTestCandidates lists the test paths that the naming convention gives
// for one source file, as slash paths. An unknown extension has none.
func relatedTestCandidates(relPath string) []string {
	dir, base := path.Split(relPath)
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext)
	switch ext {
	case ".py":
		return []string{
			path.Join(dir, "tests", "test_"+name+".py"),
			path.Join(dir, "test_"+name+".py"),
			path.Join(dir, name+"_test.py"),
			path.Join(dir, "tests", name+"_test.py"),
			// A src or lib layout keeps its tests at the workspace root.
			path.Join("tests", "test_"+name+".py"),
		}
	case ".go":
		return []string{path.Join(dir, name+"_test.go")}
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return []string{
			path.Join(dir, name+".test"+ext),
			path.Join(dir, name+".spec"+ext),
			path.Join(dir, "__tests__", name+".test"+ext),
			path.Join(dir, "__tests__", name+ext),
		}
	}
	return nil
}

func isTestFilePath(path string) bool {
	path = filepath.ToSlash(path)
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(base, ".py"):
		if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || base == "tests.py" {
			return true
		}
		for _, part := range strings.Split(path, "/") {
			if part == "tests" {
				return true
			}
		}
	case strings.Contains(base, ".test.") || strings.Contains(base, ".spec."):
		return true
	case isJSSourceExt(base) && hasPathPart(path, "__tests__"):
		return true
	}
	return false
}

// isJSSourceExt reports whether base has a JavaScript or TypeScript extension.
func isJSSourceExt(base string) bool {
	switch path.Ext(base) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return true
	}
	return false
}

// hasPathPart reports whether one segment of the slash path equals part.
func hasPathPart(slashPath, part string) bool {
	return slices.Contains(strings.Split(slashPath, "/"), part)
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
		Command:  []string{"<no test command detected>"},
		Evidence: schemas.TestEvidenceNone,
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

// runCommand runs command under the sandbox. A non-empty stdoutPath also
// receives the whole stdout. The in-memory capture keeps only its head.
func runCommand(ctx context.Context, sandboxEngine *sandbox.Engine, command []string, cwd string, timeoutSeconds int, stdoutPath string) schemas.TestRunResults {
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
	if stdoutPath != "" {
		// O_EXCL refuses a path that already exists, including a symbolic link,
		// so the write never follows a link. If the file cannot be made, the
		// run keeps the head capture only.
		if f, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
			defer f.Close()
			cmd.Stdout = io.MultiWriter(f, stdout)
		}
	}
	cmd.Stderr = stderr
	err := cmd.Run()
	procrun.ReportRun(inner, cmd, err)
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
		// The tool's own timeout has no real exit code. A command that exited
		// non-zero keeps its code, even when its output says "timed out"
		// (go test -timeout prints "panic: test timed out").
		if exitCode <= 0 && strings.Contains(res.Output, "timed out") {
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
