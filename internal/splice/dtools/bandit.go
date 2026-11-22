package dtools

import (
	"context"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
	"github.com/Taf0711/splice/internal/tools"
)

// banditTool runs Bandit security analysis on Python files.
type banditTool struct {
	workspaceRoot string
	// sandbox scopes the scanner subprocess to the workspace with network
	// denied. It is built once at construction from the workspace root.
	sandbox *sandbox.Engine
}

// NewBanditTool returns a Tool that runs Bandit security analysis on Python files.
func NewBanditTool(workspaceRoot string) tools.Tool {
	return banditTool{workspaceRoot: workspaceRoot, sandbox: procrun.NewStageEngine(workspaceRoot)}
}

func (t banditTool) Name() string {
	return "bandit"
}

func (t banditTool) Description() string {
	return "Run Bandit security analysis on Python files and return JSON output."
}

func (t banditTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"paths": {
				Type:        "array",
				Description: "Relative paths of Python files to scan.",
				Items:       &tools.PropertySchema{Type: "string"},
			},
		},
		Required:             []string{"paths"},
		AdditionalProperties: false,
	}
}

func (t banditTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect: tools.SideEffectShell,
		Permission: tools.PermissionPrompt,
		Reason:     "Runs the Bandit security scanner as a subprocess.",
	}
}

func (t banditTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return t.RunWithOptions(ctx, args, tools.RunOptions{})
}

// RunWithOptions runs the scanner under the engine the call carries, so the
// user's read and write denials reach the spawn (see spawnEngine).
func (t banditTool) RunWithOptions(ctx context.Context, args map[string]any, options tools.RunOptions) tools.Result {
	rawPaths, ok := args["paths"]
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: "paths is required"}
	}
	pathsArg, ok := rawPaths.([]any)
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: "paths must be an array of strings"}
	}
	if len(pathsArg) == 0 {
		return tools.Result{Status: tools.StatusError, Output: "paths must not be empty"}
	}

	resolvedPaths := make([]string, 0, len(pathsArg))
	for _, item := range pathsArg {
		p, ok := item.(string)
		if !ok {
			return tools.Result{Status: tools.StatusError, Output: "paths must be strings"}
		}
		abs, err := resolveWorkspacePath(t.workspaceRoot, p)
		if err != nil {
			return tools.Result{Status: tools.StatusError, Output: err.Error()}
		}
		resolvedPaths = append(resolvedPaths, abs)
	}

	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
		if err != nil {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Bandit is not installed or not available: " + err.Error(),
			}
		}
	}

	command := append([]string{python, "-m", "bandit", "-f", "json"}, resolvedPaths...)
	prepared, cerr := procrun.Prepare(ctx, procrun.Request{
		ProfileID:       procrun.ProfileSpliceDTools,
		AllowedBinaries: procrun.StageBinaries,
		Engine:          spawnEngine(t.sandbox, t.workspaceRoot, options),
		Spec:            sandbox.CommandSpec{Name: command[0], Args: command[1:], Dir: t.workspaceRoot},
	})
	if cerr != nil {
		return tools.Result{
			Status: tools.StatusError,
			Output: "Bandit is not installed or not available: " + cerr.Error(),
		}
	}
	defer prepared.Plan.Cleanup()
	cmd := prepared.Cmd

	out, err := cmd.Output()
	procrun.ReportRun(ctx, cmd, err)
	if ctx.Err() != nil {
		return tools.Result{
			Status: tools.StatusError,
			Output: ctx.Err().Error(),
		}
	}
	if err != nil {
		exitErr, isExit := err.(*exec.ExitError)
		if !isExit {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Bandit is not installed or not available: " + err.Error(),
			}
		}
		stderr := string(exitErr.Stderr)
		if strings.Contains(stderr, "No module named") || strings.Contains(stderr, "ModuleNotFoundError") {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Bandit is not installed or not available: bandit module not found",
			}
		}
		// Bandit ran and exited non-zero, usually because it found issues.
		// The JSON output is still useful to the caller.
		if len(out) == 0 {
			return tools.Result{
				Status: tools.StatusError,
				Output: stderr,
			}
		}
	}

	return tools.Result{
		Status: tools.StatusOK,
		Output: string(compactBanditJSON(out)),
	}
}

// banditOutputBudget is the largest compact Bandit report, in bytes, that
// the tool returns. It stays under the tool output cap.
const banditOutputBudget = 48000

// banditFinding holds the Bandit result fields that the security check reads.
// Bandit also emits metrics and code snippets. The check does not read them.
type banditFinding struct {
	Filename      string `json:"filename"`
	LineRange     []int  `json:"line_range,omitempty"`
	IssueText     string `json:"issue_text"`
	IssueSeverity string `json:"issue_severity"`
	TestID        string `json:"test_id"`
}

// banditCompact is the reduced report. OmittedResults counts the results
// removed to fit the budget.
type banditCompact struct {
	Results        []banditFinding `json:"results"`
	OmittedResults int             `json:"omitted_results,omitempty"`
}

// compactBanditJSON reduces Bandit's JSON report to the fields the security
// check reads. When the reduced report is still over banditOutputBudget, it
// keeps the highest severity results first and counts the rest as omitted.
// Output that is not a Bandit report is returned unchanged, so the check
// still reports the real error.
func compactBanditJSON(raw []byte) []byte {
	var full struct {
		Results []banditFinding `json:"results"`
	}
	if err := json.Unmarshal(raw, &full); err != nil || full.Results == nil {
		return raw
	}
	encode := func(kept int) []byte {
		c := banditCompact{Results: full.Results[:kept]}
		if kept < len(full.Results) {
			c.OmittedResults = len(full.Results) - kept
		}
		b, _ := json.Marshal(c)
		return b
	}
	whole := encode(len(full.Results))
	if len(whole) <= banditOutputBudget {
		return whole
	}
	sort.SliceStable(full.Results, func(i, j int) bool {
		return banditSeverityRank(full.Results[i].IssueSeverity) < banditSeverityRank(full.Results[j].IssueSeverity)
	})
	// Binary search for the largest kept count that fits. lo always fits
	// (or is zero) and hi never fits (the whole report does not).
	lo, hi := 0, len(full.Results)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if len(encode(mid)) <= banditOutputBudget {
			lo = mid
		} else {
			hi = mid
		}
	}
	return encode(lo)
}

func banditSeverityRank(sev string) int {
	switch strings.ToUpper(sev) {
	case "HIGH":
		return 0
	case "MEDIUM":
		return 1
	case "LOW":
		return 2
	default:
		return 3
	}
}
