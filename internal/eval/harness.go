package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RunInput is one headless exec invocation plus its check command.
type RunInput struct {
	SessionID string
	Memory    string // "on" / "off"
	Prompt    string
	Cwd       string // the arm's repo copy
	Check     string // shell command; exit 0 = success
}

// RunOutput is one run's outcome.
type RunOutput struct {
	Success       bool
	Tokens        int // total tokens (input+output) from the trace
	Interventions int // weighted intervention sum from the trace
	// TelemetryFound records whether the tokens came from a matching usage
	// trace. False with Success=true means the token count is absent data,
	// not a measured zero, and the report must say so.
	TelemetryFound bool
}

// RunFunc runs one headless exec invocation in an arm copy and returns its
// outcome. Production shells out to splice exec and queries the trace; tests
// use a fake.
type RunFunc func(ctx context.Context, in RunInput) (RunOutput, error)

// Harness orchestrates the paired arms and applies the decision gates.
type Harness struct {
	Exec RunFunc
	Now  func() time.Time
	// PairLogPath optionally names a JSONL file that receives one line per
	// completed pair, appended as the pair finishes. A later crash then
	// preserves earlier work. Empty disables persistence.
	PairLogPath string
}

// Run materializes a fresh cold and warm arm copy PER TASK, runs every task
// in both arms (each pair starts from the pristine fixture; no task observes
// files written by an earlier task), aggregates, decides, and returns the
// report. It never mutates the fixture source dir. A pair's copies are
// removed only after that pair's result is persisted.
func (h *Harness) Run(ctx context.Context, taskset TaskSet, model, provider string) (Report, error) {
	if h.Exec == nil {
		return Report{}, fmt.Errorf("harness has no Run function")
	}
	now := h.Now
	if now == nil {
		now = time.Now
	}

	var cold, warm ArmStats
	pairs := make([]TaskPair, 0, len(taskset.Tasks))
	for _, task := range taskset.Tasks {
		// Fresh copies per pair: a shared arm dir let task N's leftover edits
		// leak into tasks N+1..N (run-7 forensics), so later tasks fought a
		// mutated dependency they never saw.
		coldDir, err := materializeArm(taskset)
		if err != nil {
			return Report{}, fmt.Errorf("materialize cold arm for %s: %w", task.Name, err)
		}
		warmDir, err := materializeArm(taskset)
		if err != nil {
			os.RemoveAll(coldDir) //nolint:errcheck
			return Report{}, fmt.Errorf("materialize warm arm for %s: %w", task.Name, err)
		}
		cleanupArm := func() {
			os.RemoveAll(coldDir) //nolint:errcheck
			os.RemoveAll(warmDir) //nolint:errcheck
		}
		defer cleanupArm()

		coldOut, coldErr := h.Exec(ctx, RunInput{SessionID: sessionID(taskset, "cold", task), Memory: "off", Prompt: task.Prompt, Cwd: coldDir, Check: task.Check})
		warmOut, warmErr := h.Exec(ctx, RunInput{SessionID: sessionID(taskset, "warm", task), Memory: "on", Prompt: task.Prompt, Cwd: warmDir, Check: task.Check})
		if ctx.Err() != nil {
			return Report{}, fmt.Errorf("harness interrupted: %w", ctx.Err())
		}

		// An exec failure is an outcome of that arm's run, not a harness
		// crash: the arm takes the failure, keeps whatever partial tokens the
		// seam reported, and the eval continues so one bad run cannot discard
		// every other pair's result.
		coldError := ""
		if coldErr != nil {
			coldError = coldErr.Error()
			coldOut.Success = false
		}
		warmError := ""
		if warmErr != nil {
			warmError = warmErr.Error()
			warmOut.Success = false
		}

		cold.Successes += boolToInt(coldOut.Success)
		cold.Tokens += coldOut.Tokens
		cold.WeightedInterventions += coldOut.Interventions
		warm.Successes += boolToInt(warmOut.Success)
		warm.Tokens += warmOut.Tokens
		warm.WeightedInterventions += warmOut.Interventions

		pair := TaskPair{
			Name:              task.Name,
			ColdSuccess:       coldOut.Success,
			WarmSuccess:       warmOut.Success,
			ColdTokens:        coldOut.Tokens,
			WarmTokens:        warmOut.Tokens,
			ColdInterventions: coldOut.Interventions,
			WarmInterventions: warmOut.Interventions,
			ColdError:         coldError,
			WarmError:         warmError,
			ColdTelemetry:     coldOut.TelemetryFound,
			WarmTelemetry:     warmOut.TelemetryFound,
		}
		pairs = append(pairs, pair)

		if err := appendPairLog(h.PairLogPath, pair); err != nil {
			return Report{}, fmt.Errorf("persist pair %s: %w", task.Name, err)
		}
		// The pair's result is durable: release its copies now. The deferred
		// cleanupArm above only covers early-return paths between here and
		// materialization; RemoveAll on a missing path is a no-op.
		cleanupArm()
	}

	decision := Decide(DecisionInput{Pairs: len(taskset.Tasks), Cold: cold, Warm: warm})
	return Report{
		Contract:  ReportContractVersion,
		Taskset:   taskset.Name,
		Model:     model,
		Provider:  provider,
		Timestamp: now().Format(time.RFC3339),
		Pairs:     len(taskset.Tasks),
		Cold:      cold,
		Warm:      warm,
		Tasks:     pairs,
		Gates:     decision.Gates,
		Verdict:   decision.Verdict,
		Reason:    decision.Reason,
		Constants: ReportConstants(),
	}, nil
}

// sessionID builds the deterministic per-run session id so traces and verdicts
// join: eval-<taskset>-<arm>-<task>.
func sessionID(taskset TaskSet, arm string, task Task) string {
	return "eval-" + taskset.Name + "-" + arm + "-" + task.Name
}

// materializeArm copies the fixture into a fresh temp dir and runs setup.sh
// once per copy. The source fixture is never mutated.
func materializeArm(taskset TaskSet) (string, error) {
	dir, err := os.MkdirTemp("", "splice-eval-arm-")
	if err != nil {
		return "", err
	}
	fixture := taskset.FixtureDir()
	if info, err := os.Stat(fixture); err == nil && info.IsDir() {
		if err := copyDir(fixture, dir); err != nil {
			os.RemoveAll(dir) //nolint:errcheck
			return "", err
		}
		setup := filepath.Join(dir, "setup.sh")
		if info, err := os.Stat(setup); err == nil && !info.IsDir() {
			cmd := exec.Command("/bin/sh", setup)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				os.RemoveAll(dir) //nolint:errcheck
				return "", fmt.Errorf("fixture setup.sh: %v: %s", err, out)
			}
		}
	}
	return dir, nil
}

// copyDir recursively copies src into dst (dst must exist).
func copyDir(src, dst string) error {
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
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close() //nolint:errcheck
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close() //nolint:errcheck
			return err
		}
		return out.Close()
	})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// appendPairLog appends one pair as a JSON line, creating the file on first
// use. Append mode is the contract: an interrupted run must never truncate
// pairs an earlier attempt already persisted.
func appendPairLog(path string, pair TaskPair) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck
	line, err := json.Marshal(pair)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return file.Sync()
}
