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
	// Work counters parsed from the run's stream-json transcript: total tool
	// calls, file reads (read-shaped tools), and discovery searches
	// (grep/glob/search-shaped tools). Zero with no transcript means
	// unknown, not free.
	ToolCalls   int
	FileReads   int
	SearchCalls int
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
	// Rollouts is the number of times each (task, arm) pair runs; at least
	// three is required for statistical claims (handoff section 34). The
	// default of one keeps the historical single-shot behavior.
	Rollouts int
}

// Run materializes one stable arm root per arm and resets both to pristine
// fixture bytes before every task pair: the stable path preserves warm-memory
// project identity while the byte reset keeps every pair independent. It
// never mutates the fixture source dir and removes both roots when it
// returns.
func (h *Harness) Run(ctx context.Context, taskset TaskSet, model, provider string) (Report, error) {
	if h.Exec == nil {
		return Report{}, fmt.Errorf("harness has no Run function")
	}
	now := h.Now
	if now == nil {
		now = time.Now
	}

	// One stable root per arm for the whole run: the stable path is the
	// memory project identity (memoryProjectRoot falls back to the work dir),
	// so per-task temp dirs would make every warm task a different project.
	coldDir, err := materializeArm(taskset)
	if err != nil {
		return Report{}, fmt.Errorf("materialize cold arm: %w", err)
	}
	defer os.RemoveAll(coldDir) //nolint:errcheck
	warmDir, err := materializeArm(taskset)
	if err != nil {
		os.RemoveAll(coldDir) //nolint:errcheck
		return Report{}, fmt.Errorf("materialize warm arm: %w", err)
	}
	defer os.RemoveAll(warmDir) //nolint:errcheck

	var cold, warm ArmStats
	rollouts := h.Rollouts
	if rollouts < 1 {
		rollouts = 1
	}
	pairs := make([]TaskPair, 0, len(taskset.Tasks))
	for _, task := range taskset.Tasks {
		for attempt := 1; attempt <= rollouts; attempt++ {
			// Reset both roots to pristine fixture bytes before every attempt:
			// attempt N's leftover edits must never leak into attempt N+1 or
			// the next task (run-7 forensics), while the stable paths keep
			// warm-memory project identity constant.
			if err := resetArm(coldDir, taskset); err != nil {
				return Report{}, fmt.Errorf("reset cold arm for %s: %w", task.Name, err)
			}
			if err := resetArm(warmDir, taskset); err != nil {
				return Report{}, fmt.Errorf("reset warm arm for %s: %w", task.Name, err)
			}

			// Session ids keep the historical shape for single-rollout runs
			// (existing trace joins and tests); multi-rollout runs suffix the
			// attempt so each rollout gets its own trace lineage.
			coldSession := sessionID(taskset, "cold", task)
			warmSession := sessionID(taskset, "warm", task)
			if rollouts > 1 {
				coldSession += fmt.Sprintf("-r%d", attempt)
				warmSession += fmt.Sprintf("-r%d", attempt)
			}
			coldOut, coldErr := h.Exec(ctx, RunInput{SessionID: coldSession, Memory: "off", Prompt: task.Prompt, Cwd: coldDir, Check: task.Check})
			warmOut, warmErr := h.Exec(ctx, RunInput{SessionID: warmSession, Memory: "on", Prompt: task.Prompt, Cwd: warmDir, Check: task.Check})
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
				Attempt:           attempt,
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
				return Report{}, fmt.Errorf("persist pair %s r%d: %w", task.Name, attempt, err)
			}
		}
	}

	decision := Decide(DecisionInput{Pairs: len(pairs), Cold: cold, Warm: warm})
	return Report{
		Contract:  ReportContractVersion,
		Taskset:   taskset.Name,
		Model:     model,
		Provider:  provider,
		Timestamp: now().Format(time.RFC3339),
		Pairs:     len(pairs),
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

// materializeArm creates a fresh arm root and populates it from the fixture.
func materializeArm(taskset TaskSet) (string, error) {
	dir, err := os.MkdirTemp("", "splice-eval-arm-")
	if err != nil {
		return "", err
	}
	if err := populateArm(dir, taskset); err != nil {
		os.RemoveAll(dir) //nolint:errcheck
		return "", err
	}
	return dir, nil
}

// resetArm restores an existing arm root to pristine fixture bytes without
// changing its path: the path is the memory project identity, so reset must
// swap contents in place. It empties the directory, re-copies the fixture,
// and re-runs setup.sh when present.
func resetArm(dir string, taskset TaskSet) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return populateArm(dir, taskset)
}

// populateArm copies the fixture into dir and runs setup.sh once. The source
// fixture is never mutated.
func populateArm(dir string, taskset TaskSet) error {
	fixture := taskset.FixtureDir()
	if info, err := os.Stat(fixture); err == nil && info.IsDir() {
		if err := copyDir(fixture, dir); err != nil {
			return fmt.Errorf("copy fixture: %w", err)
		}
		setup := filepath.Join(dir, "setup.sh")
		if info, err := os.Stat(setup); err == nil && !info.IsDir() {
			cmd := exec.Command("/bin/sh", setup)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("fixture setup.sh: %v: %s", err, out)
			}
		}
	}
	return nil
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
