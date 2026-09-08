package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	// MatchedSnapshots runs Task A once per family, freezes the verified
	// tree and the naturally captured cognition, and materializes the same
	// tree for every Task B attempt in both arms (with a starting-tree
	// hash assertion). This isolates the memory/context policy effect on
	// an identical coding task. Off by default: the default mode measures
	// the full independent A->B workflow per arm.
	MatchedSnapshots bool
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
		case arg == "--matched-snapshots":
			options.MatchedSnapshots = true
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

// trueValue backs the WarmSetupValid pointer for rows whose precursor
// verified (the pointer keeps false, true, and absent distinguishable).
var trueValue = true

// falseValue backs the WarmSetupValid pointer so the serialized field can
// distinguish false from absent (a plain &false literal would work, but a
// named variable keeps the intent explicit at every use site).
var falseValue = false

// reviewAggregatesQuiet silences the per-family medians block in
// summarizeMvp for tests that only assert on the setup-failure line. The
// production path leaves it false: the report keeps its full shape.
var reviewAggregatesQuiet = false

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

	if options.MatchedSnapshots {
		if err := runMvpMatchedSnapshots(ctx, deps, options, manifest, manifestDir, fixtureDir, runFunc, stderr, &rows); err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		if options.OutDir != "" {
			if err := writeFamiliesRows(options.OutDir, rows); err != nil {
				return writeAppError(stderr, "failed to write mvp attempts log: "+err.Error(), exitCrash)
			}
		}
		summarizeMvp(stdout, manifest, rows)
		return exitSuccess
	}

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
				name      string
				dir       string
				memory    string
				treatment string
			}{{"cold", coldDir, "off", "cold"}, {"warm", warmDir, "on", "full"}} {
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
					SessionID:   sessionID + "-taska",
					Memory:      arm.memory,
					Treatment:   arm.treatment,
					Prompt:      family.PrecursorTask,
					Cwd:         arm.dir,
					Check:       precursorChecks[family.ID],
					OutputPath:  mvpDebugPath(options.OutDir, family.ID, attempt, arm.name, "a"),
					ArtifactDir: mvpArtifactDir(options.OutDir, family.ID, attempt, arm.name, "a"),
				}, &rows, family.ID, attempt, arm.name, "A", options.OutDir)

				// Task B: the target. Runs on Task A's tree in BOTH arms.
				row := familyPairRow{
					Family:    family.ID,
					Task:      "B",
					Attempt:   attempt,
					Arm:       arm.name,
					SessionID: sessionID,
					Precursor: precursorStatus,
				}
				if precursorStatus != "success" {
					// Task A did not verify: the attempt is infrastructure/
					// fixture noise, not a causal measurement. Record and
					// continue; the analysis excludes it.
					row.WarmSetupValid = &falseValue
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
				// Commit the VERIFIED Task A tree before Task B (identically
				// in both arms). Captured cognition anchors at the snapshot
				// revision when the stage sandbox can create one; the stage
				// sandbox refuses the write-shaped `git stash create`
				// (index write), so in-run capture anchors at the pre-verify
				// HEAD. The harness commits the verified tree, then
				// reanchors the project's nodes from that pre-verify HEAD
				// to the post-verify commit: the commit does not change
				// what the run verified, only the revision naming those
				// bytes, so the freshness contract is preserved exactly.
				// Applied identically to both arms; the only warm/cold
				// difference stays the cognition store.
				preCommitHead := gitHeadCommit(arm.dir)
				if _, commitErr := gitCommitAll(arm.dir); commitErr != nil {
					os.RemoveAll(warmDir)
					os.RemoveAll(coldDir)
					return writeAppError(stderr, fmt.Sprintf(
						"commit verified task A tree (family %s attempt %d arm %s): %v",
						family.ID, attempt, arm.name, commitErr), exitCrash)
				}
				if arm.name == "warm" {
					if reanchorErr := reanchorVerifiedCognition(ctx, arm.dir, preCommitHead, sessionID+"-taska"); reanchorErr != nil {
						os.RemoveAll(warmDir)
						os.RemoveAll(coldDir)
						return writeAppError(stderr, fmt.Sprintf(
							"reanchor captured cognition (family %s attempt %d): %v",
							family.ID, attempt, reanchorErr), exitCrash)
					}
				}

				runCtx, cancel := context.WithTimeout(ctx, familiesRunTimeout)
				started := time.Now()
				out, runErr := runFunc(runCtx, eval.RunInput{
					SessionID:   sessionID + "-taskb",
					Memory:      arm.memory,
					Treatment:   arm.treatment,
					Prompt:      family.TargetTask,
					Cwd:         arm.dir,
					Check:       targetChecks[family.ID],
					OutputPath:  mvpDebugPath(options.OutDir, family.ID, attempt, arm.name, "b"),
					ArtifactDir: mvpArtifactDir(options.OutDir, family.ID, attempt, arm.name, "b"),
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

// mvpArtifactDir returns the reconstruction-artifact directory for one
// attempt (exec transcript, verifier output, final patch, tree hash), or ""
// when SPLICE_MVP_DEBUG is unset. Artifacts make every failed attempt
// explainable: the JSONL boolean alone cannot distinguish a wrong signature
// from a failed compile from a behavioral miss.
func mvpArtifactDir(outDir, family string, attempt int, arm, task string) string {
	if os.Getenv("SPLICE_MVP_DEBUG") == "" || outDir == "" {
		return ""
	}
	dir := filepath.Join(outDir, "debug", fmt.Sprintf("%s-%s-r%d-%s", family, arm, attempt, task))
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// mvpDebugPath returns the debug transcript path for one attempt, or "" when
// SPLICE_MVP_DEBUG is unset.
func mvpDebugPath(outDir, family string, attempt int, arm, task string) string {
	if os.Getenv("SPLICE_MVP_DEBUG") == "" || outDir == "" {
		return ""
	}
	if err := os.MkdirAll(filepath.Join(outDir, "debug"), 0o755); err != nil {
		return ""
	}
	return filepath.Join(outDir, "debug", fmt.Sprintf("%s-%s-r%d-%s.log", family, arm, attempt, task))
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
// verdict ("success"/"failed"/"timeout") and error. Task A rows are
// appended by the caller through the returned row pointer so the causal
// chain is reconstructable (the caller owns checkpointing).
func mvpRunOnce(deps appDeps, ctx context.Context, runCtx context.Context, runFunc eval.RunFunc, in eval.RunInput, rows *[]familyPairRow, family string, attempt int, arm, task string, outDir string) (string, error) {
	_, err, row := mvpRunOnceTracked(deps, ctx, runCtx, runFunc, in, "", mvpEvalOptions{}, harnessProvenance(mvpEvalOptions{}, ""), family, attempt, arm, task, outDir)
	appendRowWithCheckpoint(rows, outDir, row)
	if runCtx.Err() == context.DeadlineExceeded {
		return "timeout", err
	}
	if err != nil {
		return "failed", err
	}
	if !row.Success {
		return "failed", fmt.Errorf("task %s verifier failed for %s attempt %d", task, arm, attempt)
	}
	return "success", nil
}

// mvpRunOnceTracked runs one headless exec invocation for the matched
// snapshot runner and returns the verdict, the error, and the fully
// populated row (identity, provenance, timing, evidence, failure class).
// It measures elapsed agent time on success, failure, and timeout alike.
func mvpRunOnceTracked(deps appDeps, ctx context.Context, runCtx context.Context, runFunc eval.RunFunc, in eval.RunInput, experimentID string, options mvpEvalOptions, prov harnessProvenanceInfo, family string, attempt int, arm, task string, outDir string) (string, error, familyPairRow) {
	started := time.Now()
	out, runErr := runFunc(runCtx, in)
	latency := time.Since(started)
	row := fillAttemptRow(familyPairRow{
		Family:    family,
		Task:      task,
		Attempt:   attempt,
		Arm:       arm,
		SessionID: in.SessionID,
	}, out, runErr, latency, prov, options, in)
	row.ExperimentID = experimentID
	row.PipelineRunID = experimentID
	row.Executed = true
	row.AgentTimeMs = latency.Milliseconds()
	if runCtx.Err() == context.DeadlineExceeded {
		row.InfraStatus = "timeout"
		row.FailureCategory = "harness_timeout"
		row.Success = false
		return "timeout", runErr, row
	}
	if row.Telemetry {
		collectRunTelemetry(ctx, deps, in.Cwd, in.SessionID, &row)
	}
	if runErr != nil {
		return "failed", runErr, row
	}
	if !out.Success {
		return "failed", fmt.Errorf("task %s verifier failed for %s attempt %d", task, arm, attempt), row
	}
	return "success", nil, row
}

// mvpRunTracked is mvpRunOnceTracked for Task B attempts: same row fill,
// without the verdict mapping (the caller classifies the attempt).
func mvpRunTracked(deps appDeps, ctx context.Context, runCtx context.Context, runFunc eval.RunFunc, in eval.RunInput, outDir string) (eval.RunOutput, error, familyPairRow) {
	started := time.Now()
	out, runErr := runFunc(runCtx, in)
	latency := time.Since(started)
	row := fillAttemptRow(familyPairRow{}, out, runErr, latency, harnessProvenance(mvpEvalOptions{}, ""), mvpEvalOptions{}, in)
	row.Executed = true
	row.AgentTimeMs = latency.Milliseconds()
	if runCtx.Err() == context.DeadlineExceeded {
		row.InfraStatus = "timeout"
		row.FailureCategory = "harness_timeout"
		row.Success = false
		if runErr != nil {
			row.Error = truncateForNote(runErr.Error(), 300)
		}
	}
	if row.Telemetry {
		collectRunTelemetry(ctx, deps, in.Cwd, in.SessionID, &row)
	}
	return out, runErr, row
}

// fillAttemptRow copies the seam output into the row: verdict, spend, work
// counters, evidence status, digests, timing, and the failure category.
// Values the seam did not produce stay absent: an unknown is never a zero.
func fillAttemptRow(row familyPairRow, out eval.RunOutput, runErr error, latency time.Duration, prov harnessProvenanceInfo, options mvpEvalOptions, in eval.RunInput) familyPairRow {
	row.Success = out.Success
	row.Tokens = out.Tokens
	row.Telemetry = out.TelemetryFound
	row.LatencyMs = latency.Milliseconds()
	if out.ToolCalls > 0 || out.FileReads > 0 || out.SearchCalls > 0 {
		row.ToolCalls = out.ToolCalls
		row.FileReads = out.FileReads
		row.SearchCalls = out.SearchCalls
	}
	if runErr != nil {
		row.Success = false
		row.Error = truncateForNote(runErr.Error(), 300)
	}
	if out.FailureCategory != "" {
		row.FailureCategory = out.FailureCategory
	}
	row.ManifestDigest = out.ManifestDigest
	row.ProposedDigest = out.ProposedDigest
	row.VerifierTimeMs = out.VerifierTimeMs
	row.EvidenceStatus = out.EvidenceStatus
	row.ArtifactError = out.ArtifactError
	// Artifact references reach the rows (F5-residual): the verifier
	// output and patch paths the seam captured are copied verbatim, and
	// the artifact dir is appended only when the seam paths could not
	// name it.
	refs := make([]string, 0, 3)
	if out.VerifierOutputPath != "" {
		refs = append(refs, out.VerifierOutputPath)
	}
	if out.PatchPath != "" {
		refs = append(refs, out.PatchPath)
	}
	if len(refs) == 0 && in.ArtifactDir != "" {
		refs = append(refs, in.ArtifactDir)
	}
	if len(refs) > 0 {
		row.ArtifactRefs = refs
	}
	row.SpliceCommit = prov.SpliceCommit
	row.SpliceDirty = prov.SpliceDirty
	row.SpliceBinary = prov.SpliceBinary
	if options.Model != "" {
		row.ConfiguredModel = options.Model
	}
	if in.Prompt != "" {
		row.PromptHash = sha256Hex([]byte(in.Prompt))
	}
	if in.Check != "" {
		row.VerifierHash = sha256Hex([]byte(in.Check))
	}
	return row
}

// harnessProvenanceInfo is the harness-side identity stamped onto every
// row: which splice build ran, from what commit, and whether the working
// tree was dirty. Unknown values stay empty, never fake zeros.
type harnessProvenanceInfo struct {
	SpliceCommit string
	SpliceDirty  bool
	SpliceBinary string
}

// harnessProvenance resolves the harness identity once per run. The commit
// and dirty stamp come from git; the binary identity is the resolved
// executable path (a content-addressed identity would need a build stamp,
// which this build does not carry). Failures leave the fields empty.
func harnessProvenance(options mvpEvalOptions, manifestDir string) harnessProvenanceInfo {
	prov := harnessProvenanceInfo{}
	if out, err := exec.Command("git", "-C", ".", "rev-parse", "HEAD").Output(); err == nil {
		prov.SpliceCommit = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", ".", "status", "--porcelain").Output(); err != nil {
		prov.SpliceDirty = true // unknown counts as dirty, never clean-by-assumption
	} else {
		prov.SpliceDirty = len(strings.TrimSpace(string(out))) > 0
	}
	if exe, err := os.Executable(); err == nil {
		prov.SpliceBinary = exe
	}
	return prov
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

// reanchorVerifiedCognition advances every active node of the arm project
// from the pre-verify HEAD (the revision captured cognition anchored at:
// the stage sandbox refuses the write-shaped stash create, so in-run
// capture anchors at HEAD) to the post-verify HEAD. The commit does not
// change what the run verified, only the revision naming those bytes, so
// the freshness contract is preserved exactly. Cold arms capture nothing,
// so a 0-node reanchor is valid. Fail-loud on sidecar errors: a skipped
// reanchor silently turns Task B's warm arm cold.
//
// producerRunID qualifies the capture set to the run that persisted the
// nodes (F9) when it is known; an empty id keeps the project+revision
// behavior for runs whose producer identity is unavailable.
func reanchorVerifiedCognition(ctx context.Context, armDir, preHead, producerRunID string) error {
	if preHead == "" {
		return fmt.Errorf("reanchor: no pre-commit HEAD for %s", armDir)
	}
	client, err := memd.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve sidecar: %w", err)
	}
	if client == nil {
		return fmt.Errorf("memory sidecar unavailable")
	}
	postHead := gitHeadCommit(armDir)
	if postHead == "" {
		return fmt.Errorf("reanchor: no post-commit HEAD for %s", armDir)
	}
	if postHead == preHead {
		// Nothing to commit: the anchor already names the verified bytes.
		return nil
	}
	// Scope the reanchor to the verified run's CAPTURE SET: only nodes
	// this run persisted (anchored at the pre-verify HEAD) advance.
	// Project-wide reanchoring would also advance nodes other runs
	// captured, which the review flagged as broader provenance than the
	// evidence supports. The producer-run filter (F9) keeps two runs that
	// verified the same revision in separate capture sets.
	captureSet, err := client.CaptureSetIDsForRun(ctx, armDir, preHead, producerRunID)
	if err != nil {
		return fmt.Errorf("resolve capture set: %w", err)
	}
	if len(captureSet) == 0 {
		if producerRunID != "" {
			// The run qualified by id captured nothing retrievable:
			// record that honestly instead of silently reanchoring a
			// wider set. Callers decide whether an empty set is fatal.
			return fmt.Errorf("reanchor: no capture set for run %s at revision %s in %s", producerRunID, preHead[:10], armDir)
		}
		// Nothing captured (cold-equivalent run): nothing to advance.
		return nil
	}
	if _, err := client.ReanchorGraphByIDs(ctx, armDir, captureSet, preHead, postHead); err != nil {
		return fmt.Errorf("reanchor %d node(s) %s -> %s: %w", len(captureSet), preHead[:10], postHead[:10], err)
	}
	return nil
}

// truncateForNote bounds an error string for the attempts log.

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
		setupFailed := 0
		failedSnapshots := map[string]bool{}
		for _, row := range frows {
			if row.Task != "B" {
				continue
			}
			if row.InfraStatus == "precursor_failed" {
				// A failed precursor is a real outcome of the full
				// workflow: report it, exclude only from the conditional
				// Task B analysis. One failed snapshot yields ONE setup
				// failure, not one per skipped target slot, so the count
				// is over unique snapshots (by snapshot attempt row when
				// present, otherwise family+attempt).
				setupFailed++
				if row.SnapshotID != "" {
					failedSnapshots[row.SnapshotID] = true
				} else {
					failedSnapshots[fmt.Sprintf("%s-r%d", row.Family, row.Attempt)] = true
				}
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
		if setupFailed > 0 {
			fmt.Fprintf(stdout, "  setup failures excluded from target analysis: %d (failed snapshots: %d, skipped target slots: %d)\n",
				setupFailed, len(failedSnapshots), setupFailed)
		}
		if !reviewAggregatesQuiet {
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
      --matched-snapshots   Run Task A once per family, freeze the verified
                            tree and captured cognition, and run Task B
                            from identical trees in both arms (starting
                            tree hashes asserted)
  -h, --help                Show this help

Environment (treatment matrix, applies to the exec children):
  SPLICE_SCOPE_MODE=on|off       off keeps the model's context acquisition
                                 and host context operations identical to
                                 cold while retrieval still runs and is
                                 recorded. Memory text delivery is NOT
                                 affected: scope-off with prompt memory on
                                 is the delivery-only condition, not cold.
  SPLICE_EXEMPLAR_MODE=...       memory-class delivery ablation (C1c).
                                 retrieve-no-prompt retrieves and records
                                 but delivers no cognition prose.
  SPLICE_TREATMENT=...           typed shorthand that sets both knobs and
                                 the memory flag: cold, retrieval-only,
                                 delivery-only, scope-only, or full.
`
}

// precursorChecksFor and targetChecksFor resolve the verifier scripts the
// same way the main flow does (relative to the manifest directory).
func precursorChecksFor(manifestDir string, family mvpFamilyEntry) string {
	data, err := os.ReadFile(filepath.Join(manifestDir, family.PrecursorCheckFile))
	if err != nil {
		return ""
	}
	return string(data)
}

func targetChecksFor(manifestDir string, family mvpFamilyEntry) string {
	data, err := os.ReadFile(filepath.Join(manifestDir, family.TargetCheckFile))
	if err != nil {
		return ""
	}
	return string(data)
}

// materializeFromCommit checks out the snapshot commit's tree into dst and
// initializes git there with the SAME commit hash (so tree hashes and
// anchored revisions are comparable across arms). Uses git worktree-free
// plumbing: clone the snapshot repo at that commit.
func materializeFromCommit(snapDir, commit, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if out, err := exec.Command("git", "-C", snapDir, "worktree", "add", "--detach", dst, commit).CombinedOutput(); err != nil {
		// A worktree may already exist from a previous attempt; remove and
		// retry once.
		_ = exec.Command("git", "-C", snapDir, "worktree", "remove", "--force", dst).Run()
		if out2, err2 := exec.Command("git", "-C", snapDir, "worktree", "add", "--detach", dst, commit).CombinedOutput(); err2 != nil {
			return fmt.Errorf("worktree add: %v: %s: %s", err2, out, out2)
		}
	}
	// The worktree shares the snapshot repo's object store; the sidecar's
	// freshness diffs run inside dst and resolve the same commit.
	return nil
}
