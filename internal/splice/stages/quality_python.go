package stages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// pythonSyntaxCheck is a deterministic quality adapter that runs
// python -m py_compile on Python source files and reports syntax errors. It
// also runs an optional Ruff lint pass when a project Ruff config is present.
type pythonSyntaxCheck struct{}

func (pythonSyntaxCheck) Name() string     { return "python_syntax" }
func (pythonSyntaxCheck) Category() string { return "quality" }

// ruffConfigNames are the file names that signal an opt-in Ruff config.
var ruffConfigNames = []string{"ruff.toml", ".ruff.toml"}

// ruffResult is the subset of `ruff check --output-format json` we parse.
type ruffResult struct {
	Results []struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Location struct {
			Row int `json:"row"`
		} `json:"location"`
		Filename string `json:"filename"`
	} `json:"results"`
}

func (c pythonSyntaxCheck) Run(ctx context.Context, req VerificationCheckRequest) (VerificationCheckResult, error) {
	var pyPaths []string
	for _, p := range req.Paths {
		if strings.HasSuffix(p, ".py") {
			pyPaths = append(pyPaths, p)
		}
	}
	sort.Strings(pyPaths)
	if len(pyPaths) == 0 {
		return VerificationCheckResult{
			ToolRun: schemas.VerificationToolRun{
				Tool:     c.Name(),
				Required: true,
				Scope:    "quality",
				Status:   schemas.VerificationNotApplicable,
				Summary:  "No Python files to compile.",
			},
		}, nil
	}

	runCtx, cancel := withSubprocessTimeout(ctx)
	defer cancel()

	// Batched compile: one interpreter start for the whole file set. The
	// script calls compile() in memory and writes no bytecode, so a sandbox
	// that denies writes cannot fail it. It stops at the first failing file,
	// and parsePyCompileError attributes the failure from its File line.
	command := append([]string{"python", "-c", pyCompileScript}, pyPaths...)
	var compileOut string
	compileOK := true
	if req.RunTool != nil {
		res, terr := req.RunTool(runCtx, "bash", map[string]any{
			"command": shellJoin(command),
			"cwd":     req.WorkDir,
		})
		if terr != nil {
			return VerificationCheckResult{}, fmt.Errorf("python syntax check: %w", terr)
		}
		compileOut = res.Output
		compileOK = res.OK
		if !compileOK && !pyFailureIsFinding(compileOut) {
			return pythonIncomplete(pyIncompleteSummary(compileOut))
		}
	} else {
		cmd, plan, cerr := PrepareStageCommand(runCtx, req.Sandbox, req.WorkDir, command)
		if cerr != nil {
			return pythonIncomplete(fmt.Sprintf("Python syntax check refused: %v", cerr))
		}
		defer plan.Cleanup()
		out, err := cmd.CombinedOutput()
		procrun.ReportRun(runCtx, cmd, err)
		compileOut = string(out)
		compileOK = err == nil
		if err != nil {
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				return pythonIncomplete("Python syntax check timed out.")
			}
			if errors.Is(runCtx.Err(), context.Canceled) {
				return VerificationCheckResult{}, runCtx.Err()
			}
			if errors.Is(err, exec.ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "executable file not found") {
				return pythonIncomplete("Python is not installed; Python syntax check incomplete.")
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != pyCompileErrorExit {
				return pythonIncomplete(pyIncompleteSummary(compileOut))
			}
		}
	}

	var findings []schemas.VerificationFinding
	if !compileOK {
		file, line, msg := parsePyCompileError(compileOut, pyPaths)
		if file == "" {
			file = pyPaths[0]
		}
		var linePtr *int
		if line > 0 {
			linePtr = ptrInt(line)
		}
		findings = append(findings, schemas.VerificationFinding{
			Tool:      c.Name(),
			Authority: "deterministic",
			RuleID:    "PY_COMPILE",
			Category:  c.Category(),
			Path:      relPath(file, req.WorkDir),
			Line:      linePtr,
			Message:   msg,
			Severity:  schemas.SeverityHigh,
		})
	}

	// Optional Ruff pass. Ruff is opt-in via config and never required: a
	// missing executable degrades silently rather than marking the report
	// incomplete.
	if runCtx.Err() == nil && hasRuffConfig(req.WorkDir) {
		ruffFindings, _, _ := runRuff(runCtx, req, pyPaths)
		findings = append(findings, ruffFindings...)
	}

	status := schemas.VerificationPassed
	summary := fmt.Sprintf("Compiled %d Python file(s), no syntax errors.", len(pyPaths))
	if len(findings) > 0 {
		status = schemas.VerificationFindings
		summary = fmt.Sprintf("Python check found %d issue(s).", len(findings))
	}
	return VerificationCheckResult{
		ToolRun: schemas.VerificationToolRun{
			Tool:     c.Name(),
			Required: true,
			Scope:    "quality",
			Status:   status,
			Summary:  summary,
		},
		Findings: findings,
	}, nil
}

// pyCompileScript checks the syntax of each path in argv without writing
// bytecode. It prints a File line and an error line for the first compile
// error, and exits pyCompileErrorExit. Any other failure prints a line that
// starts with "check unavailable:" and exits 2.
const pyCompileScript = `import sys
for path in sys.argv[1:]:
    try:
        with open(path, "rb") as f:
            source = f.read()
        compile(source, path, "exec")
    except (SyntaxError, ValueError) as e:
        print('File "%s", line %d' % (path, getattr(e, "lineno", None) or 0))
        print("%s: %s" % (type(e).__name__, getattr(e, "msg", None) or e))
        sys.exit(1)
    except Exception as e:
        print("check unavailable: %s: %s" % (type(e).__name__, e))
        sys.exit(2)
`

// pyCompileErrorExit is the exit code of pyCompileScript for a code error.
const pyCompileErrorExit = 1

var pyExitCodeRE = regexp.MustCompile(`exit_code: (\d+)`)

// pyFailureIsFinding reports whether a failed run found an error in the code.
// Only the script's code-error exit code counts. A missing interpreter, a
// denied read or any other failure leaves the check incomplete.
func pyFailureIsFinding(output string) bool {
	m := pyExitCodeRE.FindStringSubmatch(output)
	return m != nil && m[1] == strconv.Itoa(pyCompileErrorExit)
}

// pyIncompleteSummary names the first line of a failed run that did not find
// a code error.
func pyIncompleteSummary(output string) string {
	if strings.Contains(strings.ToLower(output), "not found") {
		return "Python is not installed; Python syntax check incomplete."
	}
	detail := strings.TrimSpace(strings.SplitN(output, "\n", 2)[0])
	if detail == "" {
		return "Python syntax check did not run."
	}
	return "Python syntax check incomplete: " + detail
}

func pythonIncomplete(summary string) (VerificationCheckResult, error) {
	return VerificationCheckResult{
		ToolRun: schemas.VerificationToolRun{
			Tool:     "python_syntax",
			Required: true,
			Scope:    "quality",
			Status:   schemas.VerificationIncomplete,
			Summary:  summary,
		},
	}, nil
}

// parsePyCompileError extracts the offending file and line from a py_compile
// traceback. py_compile stops at the first error, so the innermost `File`
// frame names the broken source file. When the traceback is absent (as with a
// mocked tool), it falls back to the first path in the set.
func parsePyCompileError(output string, pyPaths []string) (file string, line int, msg string) {
	re := regexp.MustCompile(`File "([^"]+)", line (\d+)`)
	matches := re.FindAllStringSubmatch(output, -1)
	msg = strings.TrimSpace(output)
	if msg == "" {
		msg = "py_compile failed"
	}
	if len(matches) == 0 {
		if len(pyPaths) > 0 {
			file = pyPaths[0]
		}
		return
	}
	last := matches[len(matches)-1]
	candidate := last[1]
	if n, e := strconv.Atoi(last[2]); e == nil {
		line = n
	}
	for _, p := range pyPaths {
		if p == candidate || strings.HasSuffix(candidate, p) || strings.HasSuffix(candidate, filepath.Base(p)) {
			file = p
			return
		}
	}
	file = candidate
	return
}

// hasRuffConfig reports whether the workspace opts into Ruff via a config file.
func hasRuffConfig(workDir string) bool {
	for _, name := range ruffConfigNames {
		if _, err := os.Stat(filepath.Join(workDir, name)); err == nil {
			return true
		}
	}
	data, err := os.ReadFile(filepath.Join(workDir, "pyproject.toml"))
	if err == nil && strings.Contains(string(data), "[tool.ruff]") {
		return true
	}
	return false
}

// runRuff runs `ruff check --output-format json` over the given files. It
// returns false (ruffRan) when Ruff is not installed so the caller can treat
// Ruff as an optional, non-blocking pass.
func runRuff(ctx context.Context, req VerificationCheckRequest, pyPaths []string) (findings []schemas.VerificationFinding, ruffRan bool, err error) {
	command := append([]string{"ruff", "check", "--output-format", "json"}, pyPaths...)
	var out []byte
	if req.RunTool != nil {
		res, terr := req.RunTool(ctx, "bash", map[string]any{
			"command": shellJoin(command),
			"cwd":     req.WorkDir,
		})
		if terr != nil {
			return nil, false, fmt.Errorf("ruff check: %w", terr)
		}
		out = []byte(res.Output)
		if !res.OK {
			ol := strings.ToLower(res.Output)
			if strings.Contains(ol, "command not found") || strings.Contains(ol, "no such file") ||
				strings.Contains(ol, "not found") || strings.Contains(ol, "not installed") {
				return nil, false, nil
			}
			// Any other non-JSON failure is treated as Ruff being unavailable.
			return nil, false, nil
		}
	} else {
		cmd, plan, cerr := PrepareStageCommand(ctx, req.Sandbox, req.WorkDir, command)
		if cerr != nil {
			// Ruff is an optional pass: a refused binary degrades to
			// unavailable exactly like a missing one.
			return nil, false, nil
		}
		defer plan.Cleanup()
		combined, rerr := cmd.CombinedOutput()
		procrun.ReportRun(ctx, cmd, rerr)
		out = combined
		if rerr != nil {
			if errors.Is(rerr, exec.ErrNotFound) ||
				strings.Contains(strings.ToLower(rerr.Error()), "executable file not found") {
				return nil, false, nil
			}
			// ruff exits non-zero when it finds issues; a non-JSON payload means
			// a real failure rather than findings, so skip the optional pass.
			if !json.Valid(out) {
				return nil, false, nil
			}
		}
	}

	var report ruffResult
	if jerr := json.Unmarshal(out, &report); jerr != nil {
		return nil, false, nil
	}
	findings = make([]schemas.VerificationFinding, 0, len(report.Results))
	for _, r := range report.Results {
		rel := relPath(r.Filename, req.WorkDir)
		line := 0
		if r.Location.Row > 0 {
			line = r.Location.Row
		}
		var linePtr *int
		if line > 0 {
			linePtr = ptrInt(line)
		}
		findings = append(findings, schemas.VerificationFinding{
			Tool:      "python_syntax",
			Authority: "deterministic",
			RuleID:    r.Code,
			Category:  "quality",
			Path:      rel,
			Line:      linePtr,
			Message:   fmt.Sprintf("%s: %s", r.Code, r.Message),
			Severity:  ruffSeverity(r.Code),
		})
	}
	return findings, true, nil
}

// ruffSeverity maps a Ruff rule code to a verification severity. E rules are
// pycodestyle errors (high), W rules are warnings (low), F rules are pyflakes
// (medium), and everything else defaults to medium.
func ruffSeverity(code string) schemas.Severity {
	switch {
	case strings.HasPrefix(code, "E"):
		return schemas.SeverityHigh
	case strings.HasPrefix(code, "W"):
		return schemas.SeverityLow
	default:
		return schemas.SeverityMedium
	}
}

func ptrInt(v int) *int { return &v }
