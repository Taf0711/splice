package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// pairEvalOptions is the `splice eval pe` flag set.
type pairEvalOptions struct {
	TasksetDir string
	OutDir     string
	Model      string
}

func parsePairEvalArgs(args []string) (pairEvalOptions, bool, error) {
	options := pairEvalOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--taskset":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.TasksetDir = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--taskset="):
			options.TasksetDir = strings.TrimSpace(strings.TrimPrefix(arg, "--taskset="))
		case arg == "--out":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.OutDir = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--out="):
			options.OutDir = strings.TrimSpace(strings.TrimPrefix(arg, "--out="))
		case arg == "--model":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.Model = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--model="):
			options.Model = strings.TrimSpace(strings.TrimPrefix(arg, "--model="))
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown eval pe flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected eval pe argument %q", arg)}
		}
	}
	if options.TasksetDir == "" {
		return options, false, execUsageError{"--taskset requires a directory path"}
	}
	return options, false, nil
}

func runPairEvalCommand(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parsePairEvalArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if _, err := fmt.Fprint(stdout, pairEvalHelp()); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	taskset, err := eval.LoadTaskSet(options.TasksetDir)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	ctx, stop := signalContext()
	defer stop()

	harness := &eval.Harness{Exec: pairEvalRunFunc(deps, options.Model)}
	report, err := harness.Run(ctx, taskset, options.Model, "")
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	if options.OutDir != "" {
		if err := writePairEvalReport(options.OutDir, report); err != nil {
			return writeAppError(stderr, "failed to write paired-eval report: "+err.Error(), exitCrash)
		}
	}

	if _, err := fmt.Fprintf(stdout, "verdict: %s\n%s\n", report.Verdict, report.Reason); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func pairEvalHelp() string {
	return `Usage:
  splice eval pe --taskset <dir> [--out <dir>] [--model <id>]

Runs a held-out task set in paired arms (cold = memory off, warm = memory on)
and applies the lexicographic decision gates. This is the only causal
instrument in the system and runs the real provider, so it costs money.

Flags:
      --taskset <dir>       Task set directory (tasks/*.json + fixture/)
      --out <dir>           Write pe-report.json and pe-report.md
      --model <id>          Model id for every run
  -h, --help                Show this help
`
}

func writePairEvalReport(outDir string, report eval.Report) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	jsonData, err := report.WriteJSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "pe-report.json"), append(jsonData, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "pe-report.md"), []byte(report.RenderMarkdown()), 0o644)
}

// pairEvalRunFunc builds the production run seam: it shells out to splice exec
// (headless) with a deterministic session id and the requested memory mode,
// runs the check command, and collects tokens and interventions from the trace.
//
// DELIBERATE SEAM (Veritas): when the semantic cache lands, eval runs must set
// X-Veritas-Bypass (skip read AND write) so eval traffic never pollutes the
// corpus. That header is not wired yet; the exec argv below is the single
// point where the bypass flag will be added.
func pairEvalRunFunc(deps appDeps, model string) eval.RunFunc {
	return func(ctx context.Context, in eval.RunInput) (eval.RunOutput, error) {
		exe, err := os.Executable()
		if err != nil {
			return eval.RunOutput{}, fmt.Errorf("resolve executable: %w", err)
		}
		args := []string{"--no-trust", "exec", "--output-format", "stream-json", "--memory", in.Memory, "--init-session-id", in.SessionID}
		if strings.TrimSpace(model) != "" {
			args = append(args, "--model", model)
		}
		args = append(args, in.Prompt)

		runCmd := exec.CommandContext(ctx, exe, args...)
		runCmd.Dir = in.Cwd
		if out, runErr := runCmd.CombinedOutput(); runErr != nil {
			return eval.RunOutput{}, fmt.Errorf("exec run %s: %v: %s", in.SessionID, runErr, out)
		}

		checkCmd := exec.CommandContext(ctx, "/bin/sh", "-c", in.Check)
		checkCmd.Dir = in.Cwd
		success := checkCmd.Run() == nil

		tokens, interventions := collectTrace(ctx, deps, in.Cwd, in.SessionID)
		return eval.RunOutput{Success: success, Tokens: tokens, Interventions: interventions}, nil
	}
}

// collectTrace resolves the trace for a deterministic session id and returns
// its total tokens and weighted interventions. Best-effort: a missing trace
// yields zeros and never fails the run.
func collectTrace(ctx context.Context, deps appDeps, repoRoot, sessionID string) (tokens, interventions int) {
	client, err := deps.resolveMemory(ctx)
	if err != nil || client == nil {
		return 0, 0
	}
	results, err := client.QueryTraces(ctx, schemas.TraceQueryFilter{RepoRoot: repoRoot, Limit: 1000})
	if err != nil {
		return 0, 0
	}
	for _, result := range results {
		if result.Trace.RunID != sessionID && result.Trace.SessionID != sessionID {
			continue
		}
		for _, stage := range result.Trace.Stages {
			tokens += stage.TokensInput + stage.TokensOutput
		}
		for _, intervention := range result.Trace.Interventions {
			interventions += intervention.Weight
		}
		return tokens, interventions
	}
	return 0, 0
}
