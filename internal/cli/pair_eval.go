package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// pairEvalOptions is the `splice eval pe` flag set.
type pairEvalOptions struct {
	TasksetDir string
	OutDir     string
	Model      string
	Rollouts   int
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

	harness := &eval.Harness{Exec: pairEvalRunFunc(deps, options.Model), Rollouts: options.Rollouts}
	if options.OutDir != "" {
		// Create the out dir before the first pair so incremental persistence
		// has somewhere to append from the start.
		if err := os.MkdirAll(options.OutDir, 0o755); err != nil {
			return writeAppError(stderr, "failed to create report dir: "+err.Error(), exitCrash)
		}
		harness.PairLogPath = filepath.Join(options.OutDir, "pe-pairs.jsonl")
	}
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
  splice eval pe --taskset <dir> [--out <dir>] [--model <id>] [--rollouts <n>]

Runs a held-out task set in paired arms (cold = memory off, warm = memory on)
and applies the lexicographic decision gates. This is the only causal
instrument in the system and runs the real provider, so it costs money.

Flags:
      --taskset <dir>       Task set directory (tasks/*.json + fixture/)
      --out <dir>           Write pe-report.json and pe-report.md
      --model <id>          Model id for every run
      --rollouts <n>        Run each (task, arm) n times (>=3 for statistical
                            claims; default 1). Attempts suffix session ids
                            with -r<n> and reset arms to pristine bytes.
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
		if in.Treatment != "" {
			// The treatment is the authority for the memory flag when set:
			// resolving knobs from two sources invites a treatment whose
			// declared and realized memory dimensions disagree.
			spec, terr := splice.ResolveTreatment(in.Treatment)
			if terr != nil {
				return eval.RunOutput{}, terr
			}
			args[6] = spec.PromptMemory
		}
		if strings.TrimSpace(model) != "" {
			args = append(args, "--model", model)
		}
		args = append(args, in.Prompt)

		runCmd := exec.CommandContext(ctx, exe, args...)
		runCmd.Dir = in.Cwd
		if in.Treatment != "" {
			// Resolve the treatment's knob environment fresh per child:
			// ResolveTreatment is pure, so concurrent arms cannot observe
			// or corrupt each other's settings. The spec was already
			// resolved for the memory flag above; reusing the same call
			// shape keeps the two resolution points textually adjacent.
			spec, terr := splice.ResolveTreatment(in.Treatment)
			if terr != nil {
				return eval.RunOutput{}, terr
			}
			runCmd.Env = append(os.Environ(), spec.Environment()...)
		}
		out, runErr := runCmd.CombinedOutput()
		if in.OutputPath != "" {
			_ = os.WriteFile(in.OutputPath, out, 0o644) // best-effort debug tee
		}

		// Cold arms (--memory off) never create a trace by design: no sidecar
		// client means no tracer. When the trace join finds nothing, fall back
		// to the run's own stream-json usage records so both arms carry real
		// measured cost and the comparison stays symmetric.
		tokens, interventions, found := collectTrace(ctx, deps, in.Cwd, in.SessionID)
		if !found {
			tokens = sumStreamJSONTokens(out)
			found = tokens > 0
		}
		toolCalls, fileReads, searchCalls := sumStreamJSONWork(out)

		// Capture the agent's proposal FIRST, before any verifier touches
		// the tree: verifier probes write, rename, and delete files, and a
		// patch taken afterwards shows the verifier's debris instead of the
		// agent's work. The capture happens on the agent-failure path too,
		// so a failed run keeps its partial proposal.
		proposal := captureProposal(in.Cwd)
		if in.ArtifactDir != "" {
			writeProposalArtifact(in.ArtifactDir, in.SessionID, out, proposal)
		}

		if runErr != nil {
			return eval.RunOutput{Success: false, Tokens: tokens, TelemetryFound: found,
					ToolCalls: toolCalls, FileReads: fileReads, SearchCalls: searchCalls,
					FailureCategory: "agent_noncompletion",
					ManifestDigest:  proposal.ManifestDigest, ProposedDigest: proposal.ProposedDigest,
					VerifierOutputPath: artifactPath(in.ArtifactDir, in.SessionID, "verifier.txt"),
					PatchPath:          artifactPath(in.ArtifactDir, in.SessionID, "patch.diff")},
				fmt.Errorf("exec run %s: %v: %s", in.SessionID, runErr, out)
		}

		// The verdict comes from ONE direct verifier invocation whose exit
		// status is the authority. Output text (including any VERIFIER_EXIT=
		// marker the transcript contains) never overrides the process exit.
		verifierStart := time.Now()
		verifierOut, verdict, verdictErr := runVerifier(ctx, in)
		verifierElapsed := time.Since(verifierStart)
		if in.ArtifactDir != "" {
			writeVerifierArtifact(in.ArtifactDir, in.SessionID, verifierOut)
		}
		success := verdictErr == nil && verdict == verifierVerdictPass
		failureCategory := ""
		if !success {
			failureCategory = classifyVerifierFailure(ctx, verdictErr, verdict)
		}

		result := eval.RunOutput{Success: success, Tokens: tokens, Interventions: interventions,
			TelemetryFound: found, ToolCalls: toolCalls, FileReads: fileReads, SearchCalls: searchCalls,
			VerifierOutputPath: artifactPath(in.ArtifactDir, in.SessionID, "verifier.txt"),
			PatchPath:          artifactPath(in.ArtifactDir, in.SessionID, "patch.diff"),
			ManifestDigest:     proposal.ManifestDigest, ProposedDigest: proposal.ProposedDigest,
			VerifierTimeMs: verifierElapsed.Milliseconds(),
		}
		if !success {
			result.FailureCategory = failureCategory
		}
		// Evidence completeness is recorded on the result, never folded into
		// the verdict: an artifact write failure must not convert a real
		// correctness result into a model failure (or the reverse).
		if in.ArtifactDir != "" {
			if err := writeAttemptEvidence(in.ArtifactDir, in.SessionID, proposal); err != nil {
				result.EvidenceStatus = "incomplete"
				result.ArtifactError = truncateForNote(err.Error(), 300)
			}
		}
		return result, nil
	}
}

// runVerifier runs the check command once and classifies the outcome from
// the process exit status alone. Captured output is returned for the
// evidence record; nothing in the output text can flip the verdict.
func runVerifier(ctx context.Context, in eval.RunInput) (output []byte, verdict verifierVerdict, err error) {
	if strings.TrimSpace(in.Check) == "" {
		return nil, verifierVerdictInfra, fmt.Errorf("verifier command for %s is empty", in.SessionID)
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", in.Check)
	cmd.Dir = in.Cwd
	out, runErr := cmd.Output()
	verdict, err = classifyVerifierExit(ctx, runErr)
	return out, verdict, err
}

// verifierVerdict is the classification of one verifier invocation.
type verifierVerdict string

const (
	verifierVerdictPass    verifierVerdict = "pass"    // process exited 0
	verifierVerdictReject  verifierVerdict = "reject"  // process exited nonzero
	verifierVerdictInfra   verifierVerdict = "infra"   // verifier could not run or report
	verifierVerdictTimeout verifierVerdict = "timeout" // the caller's deadline fired
)

// classifyVerifierExit maps a verifier process error to a verdict. The
// process exit status is authoritative: exit 0 passes, any other exit is a
// rejection, and only a failure to LAUNCH the verifier or a caller deadline
// is an infrastructure/timeout class.
func classifyVerifierExit(ctx context.Context, runErr error) (verifierVerdict, error) {
	if runErr == nil {
		return verifierVerdictPass, nil
	}
	if ctx.Err() != nil {
		return verifierVerdictTimeout, fmt.Errorf("verifier interrupted by context: %w", runErr)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return verifierVerdictReject, fmt.Errorf("verifier exited %d", exitErr.ExitCode())
	}
	return verifierVerdictInfra, fmt.Errorf("verifier launch failed: %w", runErr)
}

// classifyVerifierFailure turns a verdict into the row-level failure
// category. A harness timeout is its own class: it is NOT automatically
// infrastructure, because the deadline may have cut off a run that would
// have finished either way.
func classifyVerifierFailure(ctx context.Context, verdictErr error, verdict verifierVerdict) string {
	switch verdict {
	case verifierVerdictTimeout:
		return "harness_timeout"
	case verifierVerdictInfra:
		return "verifier_infra"
	case verifierVerdictReject:
		return "verifier_rejected"
	case verifierVerdictPass:
		// A passing verifier with a non-nil error only happens when the
		// caller's context died; classify from the context.
		if ctx.Err() != nil {
			return "harness_timeout"
		}
		return "provider_failure"
	}
	_ = verdictErr
	return "provider_failure"
}

// sumStreamJSONWork counts the run's work events from a captured exec
// transcript: every tool_call event, plus file-read and discovery-search
// classifications by tool name. Read-shaped names count as file reads;
// grep/glob/search-shaped names count as discovery searches. Unknown names
// only add to the total, never to a class. Unparseable lines are skipped.
func sumStreamJSONWork(out []byte) (toolCalls, fileReads, searchCalls int) {
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "\"type\":\"tool_call\"") {
			continue
		}
		var record struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(trimmed), &record) != nil || record.Name == "" {
			continue
		}
		toolCalls++
		switch {
		case isFileReadTool(record.Name):
			fileReads++
		case isSearchTool(record.Name):
			searchCalls++
		}
	}
	return toolCalls, fileReads, searchCalls
}

// isFileReadTool reports whether a tool name reads file contents.
func isFileReadTool(name string) bool {
	switch strings.ToLower(name) {
	case "read_file", "read", "view", "cat":
		return true
	}
	return false
}

// isSearchTool reports whether a tool name searches the repo or the web.
func isSearchTool(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"grep", "glob", "search", "find"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// sumStreamJSONTokens sums totalTokens across stream-json usage records in a
// captured exec transcript. It is best-effort: unparseable lines are skipped,
// and an empty transcript yields zero.
func sumStreamJSONTokens(out []byte) int {
	total := 0
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "\"type\":\"usage\"") {
			continue
		}
		var record struct {
			TotalTokens int `json:"totalTokens"`
		}
		if json.Unmarshal([]byte(trimmed), &record) == nil {
			total += record.TotalTokens
		}
	}
	return total
}

// collectTrace resolves the trace for a deterministic session id and returns
// its total tokens and weighted interventions plus whether a matching trace
// was found at all. Best-effort: a missing trace yields zeros and never fails
// the run, but the found flag lets the report tell absent data from zero.
func collectTrace(ctx context.Context, deps appDeps, repoRoot, sessionID string) (tokens, interventions int, found bool) {
	client, err := deps.resolveMemory(ctx)
	if err != nil || client == nil {
		return 0, 0, false
	}
	// The stored repo_root is whatever string exec recorded verbatim (the raw
	// temp-dir path on macOS), so the raw form is tried first. The symlink-
	// resolved form covers callers whose cwd arrived pre-resolved.
	for _, candidate := range repoRootQueryCandidates(repoRoot) {
		results, err := client.QueryTraces(ctx, schemas.TraceQueryFilter{RepoRoot: candidate, Limit: 1000})
		if err != nil {
			return 0, 0, false
		}
		if tokens, interventions, found := matchTraceTokens(results, sessionID); found {
			return tokens, interventions, true
		}
	}
	return 0, 0, false
}

// repoRootQueryCandidates lists the lookup keys for one repo path, most
// likely first: the path as given, then its symlink-resolved form when that
// differs. macOS temp dirs live behind /var -> /private/var, and either side
// of the join may hold either form.
func repoRootQueryCandidates(path string) []string {
	candidates := []string{path}
	if resolved := resolveRepoRoot(path); resolved != path {
		candidates = append(candidates, resolved)
	}
	return candidates
}

// resolveRepoRoot canonicalizes a repository path before it becomes a trace
// lookup key. macOS serves temp dirs through /var symlinks to /private/var,
// so an unresolved query path matches no stored trace and telemetry dies
// silently. Resolution failures fall back to the raw path.
func resolveRepoRoot(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// matchTraceTokens sums the first trace matching the session id.
func matchTraceTokens(results []schemas.TraceQueryResult, sessionID string) (tokens, interventions int, found bool) {
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
		return tokens, interventions, true
	}
	return 0, 0, false
}

// verifierExitFromMarker is retained only for log forensics: the eval
// verdict no longer derives from the marker (the process exit status is
// authoritative), but old transcripts still carry it.
func verifierExitFromMarker(out []byte) error {
	marker := "VERIFIER_EXIT="
	idx := strings.LastIndex(string(out), marker)
	if idx < 0 {
		return fmt.Errorf("verifier output missing exit marker")
	}
	rest := strings.TrimSpace(string(out[idx+len(marker):]))
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	code, err := strconv.Atoi(rest)
	if err != nil {
		return fmt.Errorf("verifier exit marker %q: %w", rest, err)
	}
	if code != 0 {
		return fmt.Errorf("verifier exit %d", code)
	}
	return nil
}

// artifactPath joins an artifact directory and filename, or "" when the
// directory is empty.
func artifactPath(dir, sessionID, name string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, sessionID+"-"+name)
}

// proposalSnapshot is the complete change manifest of one agent attempt:
// tracked modifications/deletions/renames AND untracked files, with modes,
// so the captured bytes reconstruct the proposal. Captured BEFORE the
// verifier runs, so verifier probe files never pollute the evidence.
type proposalSnapshot struct {
	// Patch holds `git diff HEAD` plus untracked file contents appended as
	// diff-style sections, so the proposal is reconstructable from this one
	// artifact.
	Patch []byte
	// Manifest is the machine-readable change manifest JSON (one entry per
	// changed or untracked path, with the change kind and file mode).
	Manifest []byte
	// ManifestDigest and ProposedDigest are sha256 hex digests of Manifest
	// and Patch respectively. Empty means the digest was not computed.
	ManifestDigest string
	ProposedDigest string
	// Commit and Tree are the starting revision (HEAD commit) and its tree
	// hash at capture time. Separate fields: a commit hash is never
	// relabeled as a tree hash. Either may be "" when git could not read
	// the repo.
	Commit string
	Tree   string
	// Entries counts manifest entries (0 means no changes captured).
	Entries int
	// Error records the first capture failure, if any. A capture failure
	// never changes the verdict; it only marks the evidence incomplete.
	Error error
}

// captureProposal records the agent's source changes in cwd: a tracked diff
// (including modifications, deletions, renames, and mode changes), untracked
// file contents, a machine-readable manifest, and the starting commit and
// tree. Read-only over the user's git state: it never touches the index,
// so nothing here can disturb the agent's or the user's in-progress work.
func captureProposal(cwd string) proposalSnapshot {
	snap := proposalSnapshot{}
	if cwd == "" {
		snap.Error = fmt.Errorf("no cwd for proposal capture")
		return snap
	}
	snap.Commit = gitHeadCommit(cwd)
	snap.Tree = gitTreeHash(cwd)

	patch, patchErr := exec.Command("git", "-C", cwd, "diff", "HEAD").Output()
	if patchErr != nil {
		snap.Error = fmt.Errorf("capture tracked diff: %w", patchErr)
	}

	untracked, untrackedErr := untrackedFiles(cwd)
	if untrackedErr != nil && snap.Error == nil {
		snap.Error = fmt.Errorf("list untracked files: %w", untrackedErr)
	}

	var manifest []changeManifestEntry
	appendTrackedManifestEntries(cwd, &manifest, &snap.Error)
	for _, path := range untracked {
		content, err := os.ReadFile(filepath.Join(cwd, path))
		if err != nil {
			if snap.Error == nil {
				snap.Error = fmt.Errorf("read untracked file %s: %w", path, err)
			}
			continue
		}
		manifest = append(manifest, changeManifestEntry{Path: path, Kind: "untracked", Mode: fileModeString(filepath.Join(cwd, path))})
		patch = append(patch, untrackedDiffSection(path, content)...)
	}

	manifestData, manifestErr := json.Marshal(changeManifest{
		BaseCommit: snap.Commit,
		BaseTree:   snap.Tree,
		Entries:    manifest,
	})
	if manifestErr != nil {
		if snap.Error == nil {
			snap.Error = fmt.Errorf("encode change manifest: %w", manifestErr)
		}
	}
	if manifestErr == nil {
		snap.Manifest = manifestData
		snap.ManifestDigest = sha256Hex(manifestData)
		snap.Entries = len(manifest)
	}
	snap.Patch = patch
	snap.ProposedDigest = sha256Hex(patch)
	return snap
}

// changeManifest is the machine-readable record of one attempt's source
// changes. Digesting this JSON pins the evidence identity: the same digest
// in the row and on disk proves the row's evidence is the evidence read.
type changeManifest struct {
	BaseCommit string                `json:"base_commit"`
	BaseTree   string                `json:"base_tree"`
	Entries    []changeManifestEntry `json:"entries"`
}

type changeManifestEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // modified | deleted | renamed | mode_change | untracked
	Mode string `json:"mode,omitempty"`
}

// untrackedFiles lists untracked (and unignored) files relative to cwd.
func untrackedFiles(cwd string) ([]string, error) {
	out, err := exec.Command("git", "-C", cwd, "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// appendTrackedManifestEntries records every tracked change (modification,
// deletion, rename, mode change) from porcelain status.
func appendTrackedManifestEntries(cwd string, manifest *[]changeManifestEntry, firstErr *error) {
	out, err := exec.Command("git", "-C", cwd, "status", "--porcelain").Output()
	if err != nil {
		if *firstErr == nil {
			*firstErr = fmt.Errorf("read git status: %w", err)
		}
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		status, path := line[:2], strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		kind := "modified"
		switch {
		case strings.Contains(line, " -> "):
			kind = "renamed"
			path = path[strings.Index(path, " -> ")+4:]
		case strings.HasPrefix(status, "D"):
			kind = "deleted"
		case status == " M" || status == "M ":
			kind = "modified"
		}
		if strings.HasPrefix(status, "??") {
			// Untracked entries are added by the caller with file contents.
			continue
		}
		*manifest = append(*manifest, changeManifestEntry{Path: path, Kind: kind, Mode: fileModeString(filepath.Join(cwd, path))})
	}
}

// fileModeString renders the octal permission bits of path, or "" when the
// stat fails. Modes ride the manifest so a chmod-only change is visible.
func fileModeString(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.FormatUint(uint64(info.Mode().Perm()), 8)
}

// untrackedDiffSection renders one untracked file as a diff-style section so
// the patch artifact alone reconstructs the full proposal.
func untrackedDiffSection(path string, content []byte) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\n--- /dev/null\n+++ %s\n@@ -0,0 +1,%d @@\n", path, bytes.Count(content, []byte("\n")))
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		if line == "" && strings.HasSuffix(string(content), "\n") && len(content) == 1 {
			continue
		}
		buf.WriteString("+" + line + "\n")
	}
	return buf.Bytes()
}

// sha256Hex returns the hex sha256 of data, or "" on no data. An empty
// digest means "not captured", never "digest of nothing".
func sha256Hex(data []byte) string {
	if data == nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeProposalArtifact persists the exec transcript, the proposal patch,
// and the change manifest. Best-effort per file, but a failure is returned
// to the caller by writeAttemptEvidence; these helpers never panic and
// never change the verdict.
func writeProposalArtifact(dir, sessionID string, execOut []byte, snap proposalSnapshot) {
	_ = os.MkdirAll(dir, 0o755)
	if len(execOut) > 0 {
		_ = os.WriteFile(artifactPath(dir, sessionID, "exec.jsonl"), execOut, 0o644)
	}
	if len(snap.Patch) > 0 {
		_ = os.WriteFile(artifactPath(dir, sessionID, "patch.diff"), snap.Patch, 0o644)
	}
	if len(snap.Manifest) > 0 {
		_ = os.WriteFile(artifactPath(dir, sessionID, "manifest.json"), snap.Manifest, 0o644)
	}
	if snap.Commit != "" {
		_ = os.WriteFile(artifactPath(dir, sessionID, "commit.txt"), []byte(snap.Commit), 0o644)
	}
	if snap.Tree != "" {
		_ = os.WriteFile(artifactPath(dir, sessionID, "tree.txt"), []byte(snap.Tree), 0o644)
	}
}

// writeVerifierArtifact persists the verifier's captured output, redacted
// with the central redactor so secrets never land in the evidence tree.
func writeVerifierArtifact(dir, sessionID string, verifierOut []byte) {
	_ = os.MkdirAll(dir, 0o755)
	if len(verifierOut) > 0 {
		_ = os.WriteFile(artifactPath(dir, sessionID, "verifier.txt"), []byte(redactCLIString(string(verifierOut))), 0o644)
	}
}

// writeAttemptEvidence verifies the artifact files written for one attempt
// actually exist and are non-empty. It is the completeness check behind
// EvidenceStatus: a missing or empty artifact is an explicit evidence
// failure, never a silent gap and never a correctness change.
func writeAttemptEvidence(dir, sessionID string, snap proposalSnapshot) error {
	if dir == "" {
		return fmt.Errorf("no artifact dir for session %s", sessionID)
	}
	if snap.Error != nil {
		return fmt.Errorf("proposal capture failed: %w", snap.Error)
	}
	for _, name := range []string{"exec.jsonl", "patch.diff", "manifest.json", "commit.txt", "tree.txt"} {
		info, err := os.Stat(artifactPath(dir, sessionID, name))
		if err != nil {
			return fmt.Errorf("artifact %s missing: %w", name, err)
		}
		if info.Size() == 0 {
			return fmt.Errorf("artifact %s is empty", name)
		}
	}
	return nil
}
