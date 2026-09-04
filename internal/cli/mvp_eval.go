package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/memd"
)

// mvpEvalOptions is the `splice eval mvp` flag set: the causal paired proof
// that verified cognition captured from Task A lets a related Task B finish
// with equal-or-better correctness while performing less repository
// discovery. Unlike `eval families` (which seeds observations directly), the
// MVP runner executes Task A for real on the warm arm and lets the
// verified-run capture path persist graph cognition; Task B then runs with
// that cognition as the ONLY warm advantage.
type mvpEvalOptions struct {
	ManifestPath string // cognition-mvp-families.json
	TasksetDir   string // directory holding fixture/
	OutDir       string
	Model        string
	Rollouts     int
}

func parseMvpEvalArgs(args []string) (mvpEvalOptions, bool, error) {
	options := mvpEvalOptions{Rollouts: 3}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--manifest":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.ManifestPath = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--manifest="):
			options.ManifestPath = strings.TrimSpace(strings.TrimPrefix(arg, "--manifest="))
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
		case arg == "--rollouts":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			index = next
			n, parseErr := strconv.Atoi(strings.TrimSpace(value))
			if parseErr != nil || n < 1 {
				return options, false, execUsageError{fmt.Sprintf("--rollouts requires an integer >= 1, got %q", value)}
			}
			options.Rollouts = n
		case strings.HasPrefix(arg, "--rollouts="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--rollouts="))
			n, parseErr := strconv.Atoi(value)
			if parseErr != nil || n < 1 {
				return options, false, execUsageError{fmt.Sprintf("--rollouts requires an integer >= 1, got %q", value)}
			}
			options.Rollouts = n
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown eval mvp flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected eval mvp argument %q", arg)}
		}
	}
	if options.ManifestPath == "" {
		return options, false, execUsageError{"--manifest requires the cognition-mvp-families.json path"}
	}
	if options.TasksetDir == "" {
		return options, false, execUsageError{"--taskset requires the MVP taskset directory (holding fixture/)"}
	}
	return options, false, nil
}

// mvpFamilyManifest mirrors tests/evals/mvp-families/cognition-mvp-families.json.
type mvpFamilyManifest struct {
	Schema   string           `json:"schema"`
	Fixture  string           `json:"fixture"`
	Families []mvpFamilyEntry `json:"families"`
}

type mvpFamilyEntry struct {
	ID            string `json:"id"`
	Category      string `json:"category"`
	PrecursorTask string `json:"precursor_task"`
	TargetTask    string `json:"target_task"`
	// External verifier scripts (relative to the manifest directory). Splice's
	// own exit code is never the eval verdict: only these verifiers' exit
	// codes prove correctness of Task A (precursor_check_file) and Task B
	// (target_check_file).
	PrecursorCheckFile string `json:"precursor_check_file"`
	TargetCheckFile    string `json:"target_check_file"`
}

// runMvpEvalCommand executes the paired precursor->target causal loop.
//
// Per family, one stable directory per arm keeps the sidecar project
// identity constant. Per attempt, each arm resets to pristine fixture bytes,
// resets its sidecar project state (observations, traces, AND graph nodes:
// ResetProject covers all three), then runs the causal sequence:
//
//	cold arm: Task A (memory off) -> Task B (memory off)
//	warm arm: Task A (memory on, verified, capture fires) -> Task B (memory on)
//
// Task B always runs on Task A's resulting tree, so both arms see the same
// repository state at Task B time and the ONLY difference is the cognition
// the warm arm captured from its verified Task A run. A warm attempt whose
// Task A did not verify is invalid for the causal claim: it is recorded with
// precursor="failed" and excluded from the paired-delta analysis rather
// than silently counted as a warm win or warm loss.
func runMvpEvalCommand(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseMvpEvalArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if _, err := fmt.Fprint(stdout, mvpEvalHelp()); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	manifestData, err := os.ReadFile(options.ManifestPath)
	if err != nil {
		return writeExecUsageError(stderr, fmt.Sprintf("read manifest: %v", err))
	}
	var manifest mvpFamilyManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return writeExecUsageError(stderr, fmt.Sprintf("parse manifest: %v", err))
	}
	fixtureDir := filepath.Join(options.TasksetDir, manifest.Fixture)
	if info, err := os.Stat(fixtureDir); err != nil || !info.IsDir() {
		return writeExecUsageError(stderr, fmt.Sprintf("fixture directory not found: %s", fixtureDir))
	}
	// Fail loud before any spend: every family must carry non-empty external
	// verifier files for BOTH tasks.
	manifestDir := filepath.Dir(options.ManifestPath)
	precursorChecks := make(map[string]string, len(manifest.Families))
	targetChecks := make(map[string]string, len(manifest.Families))
	for _, family := range manifest.Families {
		if family.PrecursorCheckFile == "" || family.TargetCheckFile == "" {
			return writeExecUsageError(stderr, fmt.Sprintf("family %s: both precursor_check_file and target_check_file are required", family.ID))
		}
		for name, file := range map[string]string{
			"precursor_check_file": family.PrecursorCheckFile,
			"target_check_file":    family.TargetCheckFile,
		} {
			data, err := os.ReadFile(filepath.Join(manifestDir, file))
			if err != nil {
				return writeExecUsageError(stderr, fmt.Sprintf("family %s: read %s: %v", family.ID, name, err))
			}
			if strings.TrimSpace(string(data)) == "" {
				return writeExecUsageError(stderr, fmt.Sprintf("family %s: %s %s is empty", family.ID, name, file))
			}
			if name == "precursor_check_file" {
				precursorChecks[family.ID] = string(data)
			} else {
				targetChecks[family.ID] = string(data)
			}
		}
	}

	ctx, stop := signalContext()
	defer stop()

	runFunc := pairEvalRunFunc(deps, options.Model)
	rows := make([]familyPairRow, 0, len(manifest.Families)*options.Rollouts*2)

	for _, family := range manifest.Families {
		warmDir, err := os.MkdirTemp("", "splice-mvp-warm-")
		if err != nil {
			return writeAppError(stderr, "materialize warm arm: "+err.Error(), exitCrash)
		}
		coldDir, err := os.MkdirTemp("", "splice-mvp-cold-")
		if err != nil {
			os.RemoveAll(warmDir)
			return writeAppError(stderr, "materialize cold arm: "+err.Error(), exitCrash)
		}
		copyErr := copyFixtureTree(fixtureDir, warmDir)
		if copyErr == nil {
			copyErr = copyFixtureTree(fixtureDir, coldDir)
		}
		if copyErr != nil {
			os.RemoveAll(warmDir)
			os.RemoveAll(coldDir)
			return writeAppError(stderr, "populate arms: "+copyErr.Error(), exitCrash)
		}

		for attempt := 1; attempt <= options.Rollouts; attempt++ {
			for _, arm := range []struct {
				name   string
				dir    string
				memory string
			}{{"cold", coldDir, "off"}, {"warm", warmDir, "on"}} {
				// Pristine bytes + pristine sidecar state for THIS arm's
				// attempt: the attempt measures exactly (Task A treatment ->
				// Task B outcome), never attempt N-1's leftovers. The reset
				// commits the fixture deterministically (stable HEAD), so
				// captured cognition anchors stay comparable across attempts.
				if err := copyFixtureTree(fixtureDir, arm.dir); err != nil {
					os.RemoveAll(warmDir)
					os.RemoveAll(coldDir)
					return writeAppError(stderr, "reset "+arm.name+" arm: "+err.Error(), exitCrash)
				}
				if arm.name == "warm" {
					if resetErr := resetArmMemory(ctx, arm.dir); resetErr != nil {
						os.RemoveAll(warmDir)
						os.RemoveAll(coldDir)
						return writeAppError(stderr, fmt.Sprintf("reset warm memory (family %s attempt %d): %v", family.ID, attempt, resetErr), exitCrash)
					}
				}

				sessionID := fmt.Sprintf("mvp-%s-%s-r%d-%d", family.ID, arm.name, attempt, time.Now().UnixNano())

				// Task A: the precursor. Its outcome sets the warm attempt's
				// validity; the cold arm runs it only so both arms start
				// Task B from the same repository state.
				precursorStatus, precursorErr := mvpRunOnce(deps, ctx, runCtxFor(ctx), runFunc, eval.RunInput{
					SessionID: sessionID + "-taska",
					Memory:    arm.memory,
					Prompt:    family.PrecursorTask,
					Cwd:       arm.dir,
					Check:     precursorChecks[family.ID],
				}, &rows, family.ID, attempt, arm.name, "A", options.OutDir)

				// Task B: the target. Runs on Task A's tree in BOTH arms.
				row := familyPairRow{
					Family:    family.ID,
					Attempt:   attempt,
					Arm:       arm.name,
					SessionID: sessionID,
					Precursor: precursorStatus,
				}
				if precursorStatus != "success" {
					// Task A did not verify: the attempt is infrastructure/
					// fixture noise, not a causal measurement. Record and
					// continue; the analysis excludes it.
					row.WarmSetupValid = false
					row.WarmSetupNote = "precursor did not verify; target not run"
					if precursorErr != nil {
						row.WarmSetupNote += ": " + truncateForNote(precursorErr.Error(), 200)
					}
					row.InfraStatus = "precursor_failed"
					rows = append(rows, row)
					if ctx.Err() != nil {
						os.RemoveAll(warmDir)
						os.RemoveAll(coldDir)
						return writeAppError(stderr, "interrupted", exitCrash)
					}
					continue
				}

				runCtx, cancel := context.WithTimeout(ctx, familiesRunTimeout)
				started := time.Now()
				out, runErr := runFunc(runCtx, eval.RunInput{
					SessionID: sessionID + "-taskb",
					Memory:    arm.memory,
					Prompt:    family.TargetTask,
					Cwd:       arm.dir,
					Check:     targetChecks[family.ID],
				})
				latency := time.Since(started)
				cancel()

				row.Success = out.Success
				row.Tokens = out.Tokens
				row.Telemetry = out.TelemetryFound
				row.LatencyMs = latency.Milliseconds()
				if runErr != nil {
					row.Success = false
					row.Error = truncateForNote(runErr.Error(), 300)
				}
				if out.ToolCalls > 0 || out.FileReads > 0 || out.SearchCalls > 0 {
					row.ToolCalls = out.ToolCalls
					row.FileReads = out.FileReads
					row.SearchCalls = out.SearchCalls
				}
				if row.Telemetry {
					collectRunTelemetry(ctx, deps, arm.dir, sessionID+"-taskb", &row)
				}
				rows = append(rows, row)
				if ctx.Err() != nil {
					os.RemoveAll(warmDir)
					os.RemoveAll(coldDir)
					return writeAppError(stderr, "interrupted", exitCrash)
				}
			}
		}
		os.RemoveAll(warmDir)
		os.RemoveAll(coldDir)
		if _, err := fmt.Fprintf(stdout, "family %s: %d rollouts x 2 arms done\n", family.ID, options.Rollouts); err != nil {
			return exitCrash
		}
	}

	if options.OutDir != "" {
		if err := writeFamiliesRows(options.OutDir, rows); err != nil {
			return writeAppError(stderr, "failed to write mvp attempts log: "+err.Error(), exitCrash)
		}
	}
	summarizeMvp(stdout, manifest, rows)
	return exitSuccess
}

// runCtxFor returns the parent context (the per-run timeout is applied by the
// caller for Task B; Task A runs under the outer signal context plus the
// shared familiesRunTimeout).
func runCtxFor(parent context.Context) context.Context {
	ctx, cancel := context.WithTimeout(parent, familiesRunTimeout)
	go func() {
		<-parent.Done()
		cancel()
	}()
	return ctx
}

// mvpRunOnce runs one headless exec invocation and returns its verifier
// verdict ("success"/"failed") and error. Task A rows are also appended to
// the attempts log (task="A") so the causal chain is reconstructable.
func mvpRunOnce(deps appDeps, ctx context.Context, runCtx context.Context, runFunc eval.RunFunc, in eval.RunInput, rows *[]familyPairRow, family string, attempt int, arm, task string, outDir string) (string, error) {
	started := time.Now()
	out, runErr := runFunc(runCtx, in)
	latency := time.Since(started)
	row := familyPairRow{
		Family:    family,
		Attempt:   attempt,
		Arm:       arm,
		SessionID: in.SessionID,
	}
	// Task A rows carry the precursor verdict in Status; task identity rides
	// the session id suffix (-taska / -taskb).
	if out.Success {
		row.Status = "success"
	} else {
		row.Status = "failed"
	}
	row.Success = out.Success
	row.Tokens = out.Tokens
	row.Telemetry = out.TelemetryFound
	row.LatencyMs = latency.Milliseconds()
	if runErr != nil {
		row.Error = truncateForNote(runErr.Error(), 300)
	}
	if out.ToolCalls > 0 || out.FileReads > 0 || out.SearchCalls > 0 {
		row.ToolCalls = out.ToolCalls
		row.FileReads = out.FileReads
		row.SearchCalls = out.SearchCalls
	}
	if row.Telemetry {
		collectRunTelemetry(ctx, deps, in.Cwd, in.SessionID, &row)
	}
	*rows = append(*rows, row)
	if outDir != "" {
		_ = writeFamiliesRows(outDir, *rows) // best-effort checkpoint
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return "timeout", runErr
	}
	if runErr != nil {
		return "failed", runErr
	}
	if !out.Success {
		return "failed", fmt.Errorf("task %s verifier failed for %s attempt %d", task, arm, attempt)
	}
	return "success", nil
}

// resetArmMemory clears one arm's sidecar state (observations, traces, and
// graph nodes) for the arm's project path. Fail-loud: a skipped reset breaks
// the per-attempt causal isolation invariant.
func resetArmMemory(ctx context.Context, dir string) error {
	client, err := memd.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve sidecar: %w", err)
	}
	if client == nil {
		return fmt.Errorf("memory sidecar unavailable")
	}
	if _, err := client.ResetProject(ctx, dir); err != nil {
		return fmt.Errorf("reset project state: %w", err)
	}
	return nil
}

// truncateForNote bounds an error string for the attempts log.
func truncateForNote(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max]
	}
	return s
}

// summarizeMvp prints the paired cold-vs-warm comparison per family: success
// counts, medians for tokens/discovery work, and cognition telemetry so the
// causal chain (captured -> retrieved -> applied -> skipped) is visible in
// the run output itself.
func summarizeMvp(stdout io.Writer, manifest mvpFamilyManifest, rows []familyPairRow) {
	byFamily := map[string][]familyPairRow{}
	for _, row := range rows {
		byFamily[row.Family] = append(byFamily[row.Family], row)
	}
	fmt.Fprintf(stdout, "\nMVP paired proof (task B outcomes; precursor-failed attempts excluded)\n")
	for _, family := range manifest.Families {
		frows := byFamily[family.ID]
		if len(frows) == 0 {
			continue
		}
		var cold, warm []familyPairRow
		for _, row := range frows {
			if row.Status != "" && row.InfraStatus == "precursor_failed" {
				continue
			}
			if row.Arm == "cold" {
				cold = append(cold, row)
			} else {
				warm = append(warm, row)
			}
		}
		coldS, warmS := 0, 0
		var coldTok, warmTok, coldSearch, warmSearch, coldReads, warmReads, warmAvoid, warmCog []int
		for _, row := range cold {
			coldS += boolToInt(row.Success)
			coldTok = append(coldTok, row.Tokens)
			coldSearch = append(coldSearch, row.SearchCalls)
			coldReads = append(coldReads, row.FileReads)
		}
		for _, row := range warm {
			warmS += boolToInt(row.Success)
			warmTok = append(warmTok, row.Tokens)
			warmSearch = append(warmSearch, row.SearchCalls)
			warmReads = append(warmReads, row.FileReads)
			warmAvoid = append(warmAvoid, row.DiscoveryReadsAvoided)
			warmCog = append(warmCog, row.DiscoveryResolvedCog)
		}
		fmt.Fprintf(stdout, "\n%s\n", family.ID)
		fmt.Fprintf(stdout, "  cold: success %d/%d, tokens med %s, searches med %s, reads med %s\n",
			coldS, len(cold), medStr(coldTok), medStr(coldSearch), medStr(coldReads))
		fmt.Fprintf(stdout, "  warm: success %d/%d, tokens med %s, searches med %s, reads med %s\n",
			warmS, len(warm), medStr(warmTok), medStr(warmSearch), medStr(warmReads))
		fmt.Fprintf(stdout, "  cognition: resolved_by_cognition med %s, avoided_ops med %s (per Task B run)\n",
			medStr(warmCog), medStr(warmAvoid))
		for _, row := range warm {
			if row.DiscoveryResolvedCog > 0 {
				fmt.Fprintf(stdout, "    attempt %d: %d question(s) resolved by cognition, %d anchor(s) validated, semantic hits %d\n",
					row.Attempt, row.DiscoveryResolvedCog, row.AnchorsValidated, row.SemanticHits)
			}
		}
	}
}

// medStr renders a median or "n/a" when no samples exist.
func medStr(values []int) string {
	if len(values) == 0 {
		return "n/a"
	}
	return strconv.Itoa(median(values))
}

func mvpEvalHelp() string {
	return `Usage:
  splice eval mvp --manifest <path> --taskset <dir> [--out <dir>] [--model <id>] [--rollouts <n>]

MVP causal paired proof: for each family, runs precursor Task A then target
Task B on one stable directory per arm. Cold arm runs with memory off; warm
arm runs with memory on and the verified-run capture path persists Task A
cognition into the graph. Task B always runs on Task A's resulting tree, so
the only arm difference is captured cognition. Warm attempts whose Task A
did not verify are recorded as precursor_failed and excluded from the
paired-delta analysis.

Flags:
      --manifest <path>     cognition-mvp-families.json path
      --taskset <dir>       Taskset directory holding fixture/
      --out <dir>           Write mvp-attempts.jsonl
      --model <id>          Model id for every run
      --rollouts <n>        Rollouts per arm (default 3)
  -h, --help                Show this help
`
}
