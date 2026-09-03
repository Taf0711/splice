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
	Schema   string          `json:"schema"`
	Fixture  string          `json:"fixture"`
	Families []familyEntry   `json:"families"`
}

type familyEntry struct {
	ID            string `json:"id"`
	Category      string `json:"category"`
	PrecursorTask string `json:"precursor_task"`
	TargetTask    string `json:"target_task"`
	TopicKey      string `json:"topic_key"`
	Anchor        string `json:"anchor"`
	Observation   struct {
		Title       string `json:"title"`
		Content     string `json:"content"`
		OwnerAgent  string `json:"owner_agent"`
		MemoryType  string `json:"memory_type"`
		Scope       string `json:"scope"`
		Visibility  string `json:"visibility"`
	} `json:"observation"`
}

// familyPairRow is one per-attempt causal record for the attempts log.
type familyPairRow struct {
	Family     string `json:"family"`
	Attempt    int    `json:"attempt"`
	Arm        string `json:"arm"` // cold | warm
	Success    bool   `json:"success"`
	Tokens     int    `json:"tokens"`
	Telemetry  bool   `json:"telemetry_found"`
	Error      string `json:"error,omitempty"`
	SessionID  string `json:"session_id"`
}

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
				// warm arm reset must NOT drop the seeded memory: memory
				// lives in the sidecar DB keyed by project path, not in the
				// arm directory, so a bytes reset is safe.
				if err := copyFixtureTree(fixtureDir, arm.dir); err != nil {
					os.RemoveAll(warmDir)
					os.RemoveAll(coldDir)
					return writeAppError(stderr, "reset "+arm.name+" arm: "+err.Error(), exitCrash)
				}
				// Session ids embed a run-start timestamp: a session store
				// collision (a previous eval run with the same deterministic
				// id) fails the exec outright, which is a harness bug, not a
				// task outcome.
				sessionID := fmt.Sprintf("eval-fam-%s-%s-r%d-%d", family.ID, arm.name, attempt, time.Now().UnixNano())
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
					out, runErr = runFunc(ctx, eval.RunInput{
						SessionID: tryID,
						Memory:    arm.memory,
						Prompt:    family.TargetTask,
						Cwd:       arm.dir,
						Check:     "true", // success = exec exit 0; the family check runs at analysis time
					})
					if runErr == nil || !strings.Contains(fmt.Sprint(runErr), "timed out") {
						break
					}
				}
				row := familyPairRow{
					Family:    family.ID,
					Attempt:   attempt,
					Arm:       arm.name,
					Success:   out.Success,
					Tokens:    out.Tokens,
					Telemetry: out.TelemetryFound,
					SessionID: sessionID,
				}
				if runErr != nil {
					row.Success = false
					row.Error = runErr.Error()
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
			return writeAppError(stderr, "failed to write families log: "+err.Error(), exitCrash)
		}
	}

	// Inline verdict summary: per family, warm-vs-cold success counts and
	// median tokens across rollouts.
	summarizeFamilies(stdout, manifest, rows)
	return exitSuccess
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
	obs := schemas.MemoryObservation{
		ProjectPath: &project,
		Scope:       family.Observation.Scope,
		OwnerAgent:  family.Observation.OwnerAgent,
		Visibility:  family.Observation.Visibility,
		MemoryType:  family.Observation.MemoryType,
		Title:       family.Observation.Title,
		Content:     family.Observation.Content,
		TopicKey:    &family.TopicKey,
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
	// Keep the arms as git repos at a stable HEAD so the freshness gate has
	// a commit to diff against.
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
func gitCommitAll(dir string) (string, error) {
	commands := [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=eval@splice", "-c", "user.name=eval", "commit", "-qm", "base"},
	}
	for _, args := range commands {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			// init can fail with "already exists" on reuse; commit fails on
			// nothing-to-commit. Both leave a usable repo.
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
