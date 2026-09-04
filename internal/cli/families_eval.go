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
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// familiesEvalOptions is the `splice eval families` flag set: the cognition
// causal run (evaluation handoff Q3). For each family it seeds the warm
// arm's memory with the family's observation and runs the target task
// cold (memory off) versus warm (memory on), N rollouts per arm.
type familiesEvalOptions struct {
	ManifestPath string // cognition-families.json
	TasksetDir   string // taskset-v0 root holding fixture/
	OutDir       string
	Model        string
	Rollouts     int
}

func parseFamiliesEvalArgs(args []string) (familiesEvalOptions, bool, error) {
	options := familiesEvalOptions{Rollouts: 3}
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
			return options, false, execUsageError{fmt.Sprintf("unknown eval families flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected eval families argument %q", arg)}
		}
	}
	if options.ManifestPath == "" {
		return options, false, execUsageError{"--manifest requires the cognition-families.json path"}
	}
	if options.TasksetDir == "" {
		return options, false, execUsageError{"--taskset requires the taskset directory (holding fixture/)"}
	}
	return options, false, nil
}

// familyManifest mirrors tests/evals/cognition-families/cognition-families.json.
type familyManifest struct {
	Schema   string        `json:"schema"`
	Fixture  string        `json:"fixture"`
	Families []familyEntry `json:"families"`
}

type familyEntry struct {
	ID            string `json:"id"`
	Category      string `json:"category"`
	PrecursorTask string `json:"precursor_task"`
	TargetTask    string `json:"target_task"`
	TopicKey      string `json:"topic_key"`
	Anchor        string `json:"anchor"`
	// TargetCheckFile names the family's external verifier script (relative
	// to the manifest directory). Splice's own exit code is never the eval
	// verdict: only this verifier's exit code proves correctness.
	TargetCheckFile string `json:"target_check_file"`
	Observation     struct {
		Title      string `json:"title"`
		Content    string `json:"content"`
		OwnerAgent string `json:"owner_agent"`
		MemoryType string `json:"memory_type"`
		Scope      string `json:"scope"`
		Visibility string `json:"visibility"`
	} `json:"observation"`
}

// familyPairRow is one per-attempt causal record for the attempts log.
// Telemetry fields stay absent (omitempty) when the source has no data: an
// unknown count is absent data, never a fabricated zero.
type familyPairRow struct {
	Family    string `json:"family"`
	Attempt   int    `json:"attempt"`
	Arm       string `json:"arm"` // cold | warm
	Success   bool   `json:"success"`
	Tokens    int    `json:"tokens"`
	Telemetry bool   `json:"telemetry_found"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"session_id"`

	// InfraStatus classifies the attempt's outcome: "" (ran to a verdict),
	// "timeout" (the per-run timeout cancelled the run), or an error class.
	// A timeout is an infrastructure failure, never a model correctness
	// failure, so it must stay separable from task failures.
	InfraStatus string `json:"infra_status,omitempty"`
	// LatencyMs is the wall-clock time of the exec run itself.
	LatencyMs int64 `json:"latency_ms,omitempty"`

	// Token split from the trace's stage records. Zero with
	// TelemetryFound=false means unknown, not free.
	InputTokens     int `json:"input_tokens,omitempty"`
	OutputTokens    int `json:"output_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	CachedTokens    int `json:"cached_input_tokens,omitempty"`

	// Work counters: tool calls, discovery searches, file reads (from the
	// run's stream-json transcript) and pipeline interventions (repairs)
	// from the trace.
	ToolCalls      int `json:"tool_calls,omitempty"`
	SearchCalls    int `json:"search_calls,omitempty"`
	FileReads      int `json:"file_reads,omitempty"`
	WebSearchCalls int `json:"web_search_calls,omitempty"`
	Repairs        int `json:"repair_count,omitempty"`

	// Outcome from the trace: status plus abort reason (abort_budget is the
	// pathological tail marker).
	Status      string `json:"trace_status,omitempty"`
	AbortReason string `json:"abort_reason,omitempty"`

	// Delivered memory: the post-compaction bundle the model actually saw,
	// summed across the run's stages.
	MemoryItems   int    `json:"memory_observations_delivered,omitempty"`
	MemoryChars   int    `json:"memory_chars_delivered,omitempty"`
	ExemplarItems int    `json:"exemplars_delivered,omitempty"`
	DirectHits    int    `json:"direct_hits,omitempty"`
	LookupMode    string `json:"retrieval_mode,omitempty"` // direct | search | ""
}

// familiesRunTimeout is the deterministic per-run bound: a provider stall
// cancels the attempt, records an infrastructure failure, and the evaluation
// continues. 30 minutes covers the observed 0-6 minute healthy pace with
// head room; the pathological 200-minute stall this fixes must be
// impossible, not merely rare.
const familiesRunTimeout = 30 * time.Minute

func runFamiliesEvalCommand(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseFamiliesEvalArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if _, err := fmt.Fprint(stdout, familiesEvalHelp()); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	manifestData, err := os.ReadFile(options.ManifestPath)
	if err != nil {
		return writeExecUsageError(stderr, fmt.Sprintf("read manifest: %v", err))
	}
	var manifest familyManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return writeExecUsageError(stderr, fmt.Sprintf("parse manifest: %v", err))
	}
	fixtureDir := filepath.Join(options.TasksetDir, "fixture")
	if info, err := os.Stat(fixtureDir); err != nil || !info.IsDir() {
		return writeExecUsageError(stderr, fmt.Sprintf("fixture directory not found under %s", options.TasksetDir))
	}
	// Fail loud before any spend: every family must carry an external
	// verifier file. "Splice says success" is never "eval success"; a
	// missing verifier would silently degrade the run back to exit-0
	// success, which is the bug this wiring exists to kill.
	manifestDir := filepath.Dir(options.ManifestPath)
	verifiers := make(map[string]string, len(manifest.Families))
	for _, family := range manifest.Families {
		if family.TargetCheckFile == "" {
			return writeExecUsageError(stderr, fmt.Sprintf("family %s: no target_check_file; refusing unverified runs", family.ID))
		}
		data, err := os.ReadFile(filepath.Join(manifestDir, family.TargetCheckFile))
		if err != nil {
			return writeExecUsageError(stderr, fmt.Sprintf("family %s: read target_check_file: %v", family.ID, err))
		}
		if strings.TrimSpace(string(data)) == "" {
			return writeExecUsageError(stderr, fmt.Sprintf("family %s: target_check_file %s is empty", family.ID, family.TargetCheckFile))
		}
		verifiers[family.ID] = string(data)
	}

	ctx, stop := signalContext()
	defer stop()

	runFunc := pairEvalRunFunc(deps, options.Model)
	rows := make([]familyPairRow, 0, len(manifest.Families)*options.Rollouts*2)

	for _, family := range manifest.Families {
		// One stable root per arm per family: the warm arm's memory keeps the
		// family's project identity for the whole family run.
		warmDir, err := os.MkdirTemp("", "splice-eval-warm-")
		if err != nil {
			return writeAppError(stderr, "materialize warm arm: "+err.Error(), exitCrash)
		}
		coldDir, err := os.MkdirTemp("", "splice-eval-cold-")
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

		// Seed the warm arm's memory BEFORE the target runs: ProjectPath is
		// the warm arm's stable path (the memory project identity), and
		// SourceCommit is the arm's HEAD so the freshness gate classifies
		// the anchor FRESH (the anchor is untouched between seed and run).
		seeded, seedErr := seedFamilyObservation(ctx, warmDir, family)
		if seedErr != nil {
			os.RemoveAll(warmDir)
			os.RemoveAll(coldDir)
			return writeAppError(stderr, fmt.Sprintf("seed family %s: %v", family.ID, seedErr), exitCrash)
		}
		if !seeded {
			// Memory is off (no sidecar binary): the causal comparison is
			// meaningless. Fail loud rather than measuring two cold arms.
			os.RemoveAll(warmDir)
			os.RemoveAll(coldDir)
			return writeAppError(stderr, "memory sidecar unavailable; the causal comparison needs memory ON", exitCrash)
		}

		for attempt := 1; attempt <= options.Rollouts; attempt++ {
			for _, arm := range []struct {
				name   string
				dir    string
				memory string
			}{{"cold", coldDir, "off"}, {"warm", warmDir, "on"}} {
				// Reset THIS arm to pristine bytes before the attempt. The
				// reset commits the fixture into the SAME git state every
				// time (gitCommitAll is idempotent on an unchanged tree), so
				// the arm's HEAD commit stays stable across attempts and the
				// seeded observation's SourceCommit keeps classifying FRESH.
				if err := copyFixtureTree(fixtureDir, arm.dir); err != nil {
					os.RemoveAll(warmDir)
					os.RemoveAll(coldDir)
					return writeAppError(stderr, "reset "+arm.name+" arm: "+err.Error(), exitCrash)
				}
				if arm.name == "warm" {
					// Memory-state isolation: attempt N's run may persist
					// new observations, exemplars, and traces. Reset the
					// sidecar state for this project and re-seed the family
					// observation so every warm attempt starts from the
					// EXACT same cognition (repo bytes + seeded memory),
					// never seed + experience(attempts 1..N-1).
					if resetErr := resetWarmMemory(ctx, warmDir, family); resetErr != nil {
						os.RemoveAll(warmDir)
						os.RemoveAll(coldDir)
						return writeAppError(stderr, fmt.Sprintf("reset warm memory (family %s attempt %d): %v", family.ID, attempt, resetErr), exitCrash)
					}
				}
				// Session ids embed a run-start timestamp: a session store
				// collision (a previous eval run with the same deterministic
				// id) fails the exec outright, which is a harness bug, not a
				// task outcome.
				sessionID := fmt.Sprintf("eval-fam-%s-%s-r%d-%d", family.ID, arm.name, attempt, time.Now().UnixNano())
				// Per-run timeout: a provider stall must cancel THIS run,
				// record an infrastructure failure, and let the evaluation
				// continue. The deadline context replaces the raw signal
				// context only for the exec seam; the outer loop keeps
				// honoring Ctrl-C on the parent.
				runCtx, cancel := context.WithTimeout(ctx, familiesRunTimeout)
				started := time.Now()
				var out eval.RunOutput
				var runErr error
				for try := 0; try < 2; try++ {
					// One retry for provider-side stream timeouts: the
					// transport dies mid-run and the attempt never measured
					// the agent, so a single retry keeps infra noise out of
					// the causal comparison. Session id changes per try.
					tryID := sessionID
					if try > 0 {
						tryID = fmt.Sprintf("%s-try%d", sessionID, try)
					}
					out, runErr = runFunc(runCtx, eval.RunInput{
						SessionID: tryID,
						Memory:    arm.memory,
						Prompt:    family.TargetTask,
						Cwd:       arm.dir,
						Check:     verifiers[family.ID],
					})
					if runErr == nil || ctx.Err() != nil {
						break
					}
					// The retry covers provider stream timeouts only. A
					// deadline expiry (the per-run timeout fired) is final:
					// retrying inside a dead deadline just burns the
					// remaining budget twice.
					if runCtx.Err() != nil {
						break
					}
					if !strings.Contains(fmt.Sprint(runErr), "timed out") {
						break
					}
				}
				latency := time.Since(started)
				cancel()

				row := familyPairRow{
					Family:    family.ID,
					Attempt:   attempt,
					Arm:       arm.name,
					Success:   out.Success,
					Tokens:    out.Tokens,
					Telemetry: out.TelemetryFound,
					SessionID: sessionID,
					LatencyMs: latency.Milliseconds(),
				}
				if runCtx.Err() == context.DeadlineExceeded {
					// The timeout is an infrastructure failure, not an agent
					// correctness failure. Record it as infra and continue;
					// the later attempts of both arms stay untouched.
					row.InfraStatus = "timeout"
					row.Success = false
					if runErr != nil {
						row.Error = runErr.Error()
					}
				} else if runErr != nil {
					row.Success = false
					row.Error = runErr.Error()
				}
				if row.Telemetry {
					collectRunTelemetry(ctx, deps, arm.dir, sessionID, &row)
				}
				row.ToolCalls = out.ToolCalls
				row.SearchCalls = out.SearchCalls
				row.FileReads = out.FileReads
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
			return writeAppError(stderr, "failed to write families log: "+err.Error(), exitCrash)
		}
	}

	// Inline verdict summary: per family, warm-vs-cold success counts and
	// median tokens across rollouts.
	summarizeFamilies(stdout, manifest, rows)
	return exitSuccess
}

// checkFor reads the family target's external verifier (relative to the
// manifest directory). Startup validation already proved the file exists and
// is non-empty, so a read failure here is a real race, not a fallback case:
// it errors instead of silently counting unverified runs as correct.
func checkFor(manifestDir string, family familyEntry) (string, error) {
	path := filepath.Join(manifestDir, family.TargetCheckFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read verifier for %s: %w", family.ID, err)
	}
	return string(data), nil
}

// resetWarmMemory restores the warm arm's sidecar state to "seeded only":
// every observation and trace the previous attempt persisted for this
// project is hard-deleted, then the family observation is seeded again.
// Both steps are fail-loud: a skipped reset would silently turn attempt N
// into attempt N-1 + experience, breaking the causal independence
// invariant (same treatment every attempt).
func resetWarmMemory(ctx context.Context, warmDir string, family familyEntry) error {
	client, err := memd.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve sidecar: %w", err)
	}
	if client == nil {
		return fmt.Errorf("memory sidecar unavailable")
	}
	counts, err := client.ResetProject(ctx, warmDir)
	if err != nil {
		return fmt.Errorf("reset project state: %w", err)
	}
	_ = counts // the counts are informational; the invariant is reset-then-seed
	seeded, err := seedFamilyObservation(ctx, warmDir, family)
	if err != nil {
		return fmt.Errorf("re-seed: %w", err)
	}
	if !seeded {
		return fmt.Errorf("memory sidecar dropped between reset and seed")
	}
	return nil
}

// collectRunTelemetry reads the stored trace for one attempt and fills the
// row's telemetry fields. Counts only land when the trace was found and
// parsed; absent data stays absent (omitempty), never a fabricated zero.
func collectRunTelemetry(ctx context.Context, deps appDeps, repoRoot, sessionID string, row *familyPairRow) {
	client, err := deps.resolveMemory(ctx)
	if err != nil || client == nil {
		return
	}
	results, err := client.QueryTraces(ctx, schemas.TraceQueryFilter{RepoRoot: repoRoot, Limit: 1000})
	if err != nil {
		return
	}
	var matched *schemas.TraceQueryResult
	for i := range results {
		if results[i].Trace.RunID == sessionID || results[i].Trace.SessionID == sessionID {
			matched = &results[i]
			break
		}
	}
	if matched == nil {
		return
	}
	trace := matched.Trace
	row.InputTokens = 0
	row.OutputTokens = 0
	row.ReasoningTokens = 0
	row.CachedTokens = 0
	row.ToolCalls = 0
	row.WebSearchCalls = 0
	row.MemoryItems = 0
	row.MemoryChars = 0
	row.ExemplarItems = 0
	row.DirectHits = 0
	modes := map[string]bool{}
	for _, stage := range trace.Stages {
		meta := stage.InputMeta
		row.InputTokens += stage.TokensInput
		row.OutputTokens += stage.TokensOutput
		row.ReasoningTokens += stage.TokensReasoning
		row.CachedTokens += stage.TokensCached
		row.WebSearchCalls += stage.WebSearchRequests
		row.MemoryItems += meta.MemoryItems
		row.MemoryChars += meta.MemoryChars
		row.ExemplarItems += meta.ExemplarItems
		row.DirectHits += meta.DirectHits
		if meta.MemoryLookupMode != "" {
			modes[meta.MemoryLookupMode] = true
		}
	}
	if len(modes) == 1 {
		for mode := range modes {
			row.LookupMode = mode
		}
	} else if len(modes) > 1 {
		row.LookupMode = "mixed"
	}
	row.Status = trace.Outcome.Status
	row.AbortReason = trace.Outcome.AbortReason
	row.Repairs = len(trace.Interactions)
}

// seedFamilyObservation upserts the family's observation into memory with
// ProjectPath anchored to the warm arm. It reports false when memory is off.
func seedFamilyObservation(ctx context.Context, warmDir string, family familyEntry) (bool, error) {
	client, err := memd.Resolve(ctx)
	if err != nil {
		return false, fmt.Errorf("resolve memory sidecar: %w", err)
	}
	if client == nil {
		return false, nil
	}
	project := warmDir
	commit := gitHeadCommit(warmDir)
	topicKey := family.TopicKey
	title := family.Observation.Title
	content := family.Observation.Content
	obs := schemas.MemoryObservation{
		ProjectPath: &project,
		Scope:       family.Observation.Scope,
		OwnerAgent:  family.Observation.OwnerAgent,
		Visibility:  family.Observation.Visibility,
		MemoryType:  family.Observation.MemoryType,
		Title:       title,
		Content:     content,
		TopicKey:    &topicKey,
	}
	if commit != "" {
		obs.SourceCommit = &commit
	}
	if _, err := client.Upsert(ctx, obs); err != nil {
		return false, fmt.Errorf("upsert seed: %w", err)
	}
	return true, nil
}

// gitHeadCommit returns the HEAD commit of the repo at dir, or "" when git
// fails. The families runner records it so the freshness gate can classify.
func gitHeadCommit(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// copyFixtureTree replaces dir's contents with fixtureSrc's bytes (the
// pristine-reset invariant: bytes reset, path stable).
//
// The git state must be STABLE across resets: the seeded observation's
// SourceCommit is captured once per family, and the freshness gate proves
// the anchor unchanged by diffing against it. A reset that produced a new
// HEAD each attempt would change the freshness classification per attempt
// (attempts would stop being repetitions of the same treatment). The
// commit command is therefore deterministic (fixed author, fixed message,
// fixed dates) and nothing-to-commit (an unchanged tree) is a no-op that
// leaves the previous HEAD in place.
func copyFixtureTree(fixtureSrc, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(fixtureSrc)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		src := filepath.Join(fixtureSrc, entry.Name())
		dst := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if err := copyDirTree(src, dst); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	// Re-stage and commit the pristine tree. The commit is deterministic
	// (fixed identity and dates), so the same tree always yields the same
	// HEAD and the freshness gate sees one stable commit across attempts.
	if _, err := gitCommitAll(dir); err != nil {
		return err
	}
	return nil
}

func copyDirTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// gitCommitAll stages everything and commits, returning the HEAD commit.
//
// The commit is fully deterministic: fixed author, fixed message, fixed
// dates. Two commits over the same tree produce the SAME sha, so an eval
// arm reset to pristine bytes keeps the exact HEAD commit it started with.
// The freshness gate diffs the seeded observation's SourceCommit against
// HEAD, and eval cold/warm comparisons require that classification to be
// identical across attempts; a timestamp-varying commit would change the
// per-attempt treatment. Nothing-to-commit (the tree already matches) is
// a no-op that leaves the existing HEAD, which is the same result.
func gitCommitAll(dir string) (string, error) {
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=eval",
		"GIT_AUTHOR_EMAIL=eval@splice",
		"GIT_COMMITTER_NAME=eval",
		"GIT_COMMITTER_EMAIL=eval@splice",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
	)
	commands := [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-qm", "base"},
	}
	for _, args := range commands {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			// init can fail with "already exists" on reuse; commit fails on
			// nothing-to-commit. Both leave a usable repo with HEAD intact.
			_ = out
			if args[0] == "init" || args[len(args)-1] == "base" {
				continue
			}
			return "", fmt.Errorf("git %v: %v", args, err)
		}
	}
	return gitHeadCommit(dir), nil
}

func writeFamiliesRows(outDir string, rows []familyPairRow) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	file, err := os.Create(filepath.Join(outDir, "families-attempts.jsonl"))
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck
	encoder := json.NewEncoder(file)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return file.Sync()
}

// summarizeFamilies prints the per-family paired comparison: success counts
// and token medians per arm. Statistics stay descriptive; the gates formal
// verdict belongs to the pe harness over the same data.
func summarizeFamilies(stdout io.Writer, manifest familyManifest, rows []familyPairRow) {
	byFamily := map[string][]familyPairRow{}
	for _, row := range rows {
		byFamily[row.Family] = append(byFamily[row.Family], row)
	}
	for _, family := range manifest.Families {
		frows := byFamily[family.ID]
		if len(frows) == 0 {
			continue
		}
		coldSuccess, warmSuccess := 0, 0
		var coldTokens, warmTokens []int
		for _, row := range frows {
			if row.Arm == "cold" {
				coldSuccess += boolToInt(row.Success)
				coldTokens = append(coldTokens, row.Tokens)
			} else {
				warmSuccess += boolToInt(row.Success)
				warmTokens = append(warmTokens, row.Tokens)
			}
		}
		fmt.Fprintf(stdout, "%s: cold %d/%d success (median %d tok), warm %d/%d success (median %d tok)\n",
			family.ID, coldSuccess, len(coldTokens), median(coldTokens), warmSuccess, len(warmTokens), median(warmTokens))
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted[len(sorted)/2]
}

func familiesEvalHelp() string {
	return `Usage:
  splice eval families --manifest <path> --taskset <dir> [--out <dir>] [--model <id>] [--rollouts <n>]

Cognition causal run (evaluation handoff Q3): for each family in the
manifest, seeds the warm arm's memory with the family observation and runs
the target task cold (memory off) versus warm (memory on), N rollouts per
arm. Arms reset to pristine fixture bytes per attempt; memory lives in the
sidecar and survives the reset. Warm advantage flows through the search
path (BM25 + rerank + admission), not direct topic-key hits.

Flags:
      --manifest <path>     cognition-families.json path
      --taskset <dir>       Taskset directory holding fixture/
      --out <dir>           Write families-attempts.jsonl
      --model <id>          Model id for every run
      --rollouts <n>        Rollouts per arm (default 3)
  -h, --help                Show this help
`
}
