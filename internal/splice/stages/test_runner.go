package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/testrunner"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestRunner is the deterministic test runner pipeline stage.
type TestRunner struct{}

var _ Stage = TestRunner{}

func (TestRunner) Run(ctx context.Context, input schemas.HarnessStageInput, provider zeroruntime.Provider, options StageOptions) (schemas.HarnessStageOutput, error) {
	var cmd []string
	cmd = append([]string(nil), options.Command...)
	if len(cmd) == 0 {
		detected, err := testCommand(options.WorkDir)
		if err != nil {
			return schemas.HarnessStageOutput{}, fmt.Errorf("detect test command: %w", err)
		}
		cmd = detected
	}

	timeout := 120
	if options.TimeoutSeconds > 0 {
		timeout = options.TimeoutSeconds
	}

	options.report(fmt.Sprintf("running %s in %s", joinShell(cmd), options.WorkDir))
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
			results = runCommand(runCtx, cmd, options.WorkDir, timeout)
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
		results = runCommand(ctx, cmd, options.WorkDir, timeout)
	}

	var summary string
	var confidence float64
	if results.ExitCode == 0 {
		results.Tests = []schemas.TestCaseResult{{
			Name:       "suite",
			Status:     "passed",
			DurationMs: results.DurationMs,
		}}
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
		results.Tests = []schemas.TestCaseResult{{
			Name:       "suite",
			Status:     "failed",
			DurationMs: results.DurationMs,
			Message:    fmt.Sprintf("exit code %d", results.ExitCode),
		}}
		summary = fmt.Sprintf("Test command failed with exit code %d.", results.ExitCode)
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

func testCommand(workDir string) ([]string, error) {
	if workDir == "" {
		return nil, fmt.Errorf("no work_dir and no command provided")
	}
	checks, err := testrunner.Detect(workDir)
	if err != nil || len(checks) == 0 {
		return []string{"go", "test", "./..."}, nil
	}
	for _, c := range checks {
		if len(c.Command) > 0 {
			return c.Command, nil
		}
	}
	return nil, fmt.Errorf("no runnable test command detected")
}

func runCommand(ctx context.Context, command []string, cwd string, timeoutSeconds int) schemas.TestRunResults {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	start := time.Now()
	inner, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(inner, command[0], command[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
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
