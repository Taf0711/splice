// Command warmcost-eval runs the paired cold-versus-warm measurement design in
// tests/evals/cognition-families/MEASUREMENT_DESIGN.md.
//
// It is a release-cadence tool, not a CI test. It never selects a provider or
// a model by itself: the operator supplies the exact approved binary and model.
//
// Taskset format: --tasks points at a directory with tasks/*.json
// ({"id","prompt","check","fixture"}) and an optional fixture/ directory used
// when a task does not name its own fixture.
//
// Example (provider run needs explicit owner approval):
//
//	warmcost-eval \
//	  --repo  /path/to/repo \
//	  --binary /path/to/splice \
//	  --model  <approved-model> \
//	  --tasks  tests/evals/warmcost/taskset-example \
//	  --out    tests/evals/results \
//	  --repeats 3 \
//	  --arms cold,warm \
//	  --correctness-margin 0.05
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Taf0711/splice/tests/evals/warmcost"
)

type options struct {
	repo              string
	binary            string
	model             string
	reasoningEffort   string
	maxTurns          string
	tasks             string
	out               string
	repeats           int
	arms              string
	timeout           time.Duration
	bootstrapSamples  int
	bootstrapSeed     int64
	correctnessMargin float64
	binaryRevision    string
	sidecarRevision   string
	retention         string
	sidecarRoot       string
	keepWorkspaces    bool
	runID             string
}

func main() {
	opts := options{}
	flag.StringVar(&opts.repo, "repo", ".", "repository root used for the binary revision")
	flag.StringVar(&opts.binary, "binary", "", "path to the splice binary (required)")
	flag.StringVar(&opts.model, "model", "", "model id for every attempt")
	flag.StringVar(&opts.reasoningEffort, "reasoning-effort", "", "recorded reasoning effort setting")
	flag.StringVar(&opts.maxTurns, "max-turns", "", "recorded max turns setting")
	flag.StringVar(&opts.tasks, "tasks", "", "taskset directory (required)")
	flag.StringVar(&opts.out, "out", "tests/evals/results", "output directory for the run artifacts")
	flag.IntVar(&opts.repeats, "repeats", 1, "repeats per task per arm")
	flag.StringVar(&opts.arms, "arms", "cold,warm", "comma-separated arms: cold,warm,warm-retrieval-only")
	flag.DurationVar(&opts.timeout, "timeout", 30*time.Minute, "per-attempt timeout")
	flag.IntVar(&opts.bootstrapSamples, "bootstrap-samples", 10000, "task-clustered bootstrap samples")
	flag.Int64Var(&opts.bootstrapSeed, "bootstrap-seed", 1, "bootstrap seed")
	flag.Float64Var(&opts.correctnessMargin, "correctness-margin", 0, "pre-registered acceptable success-rate drop")
	flag.StringVar(&opts.binaryRevision, "binary-revision", "", "revision of the binary under test; overrides the --repo derivation when set")
	flag.StringVar(&opts.sidecarRevision, "sidecar-revision", "", "sidecar binary revision recorded per attempt")
	flag.StringVar(&opts.retention, "retention", "fresh", "sidecar lifetime protocol: fresh or shared")
	flag.StringVar(&opts.sidecarRoot, "sidecar-root", "", "directory for per-class sidecar sockets and databases (required for shared retention)")
	flag.BoolVar(&opts.keepWorkspaces, "keep-workspaces", false, "keep per-attempt workspaces for debugging")
	flag.StringVar(&opts.runID, "run-id", "", "run id (default: timestamp)")
	flag.Parse()

	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "warmcost-eval:", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	if strings.TrimSpace(opts.binary) == "" {
		return errors.New("--binary is required")
	}
	if strings.TrimSpace(opts.tasks) == "" {
		return errors.New("--tasks is required")
	}
	arms, err := parseArms(opts.arms)
	if err != nil {
		return err
	}
	retention, err := warmcost.ParseRetention(opts.retention)
	if err != nil {
		return err
	}
	tasks, err := loadTasks(opts.tasks)
	if err != nil {
		return err
	}
	cfg := warmcost.Config{
		RunID:             opts.runID,
		RepoDir:           opts.repo,
		Binary:            opts.binary,
		BinaryRevision:    opts.binaryRevision,
		Model:             opts.model,
		ModelSettings:     warmcost.ModelSettings{ReasoningEffort: opts.reasoningEffort, MaxTurns: opts.maxTurns},
		Tasks:             tasks,
		Arms:              arms,
		Repeats:           opts.repeats,
		OutDir:            opts.out,
		Timeout:           opts.timeout,
		BootstrapSamples:  opts.bootstrapSamples,
		BootstrapSeed:     opts.bootstrapSeed,
		CorrectnessMargin: opts.correctnessMargin,
		SidecarRevision:   opts.sidecarRevision,
		Retention:         retention,
		SidecarRoot:       opts.sidecarRoot,
		KeepWorkspaces:    opts.keepWorkspaces,
	}
	runner, err := warmcost.NewRunner(cfg, nil)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	agg, err := runner.Run(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("run %s: arms=%d tasks=%d attempts=%d\n", agg.RunID, len(agg.ArmMetrics), len(agg.PerTask), agg.Coverage.Attempts)
	fmt.Printf("total-cost claim allowed: %t (%s)\n", agg.Claim.TotalCostClaimAllowed, agg.Claim.Reason)
	fmt.Printf("artifacts: %s\n", filepath.Join(opts.out, agg.RunID))
	return nil
}

func parseArms(raw string) ([]warmcost.Arm, error) {
	var arms []warmcost.Arm
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		arm := warmcost.Arm(part)
		if !arm.Valid() {
			return nil, fmt.Errorf("unknown arm %q (want cold, warm, or warm-retrieval-only)", part)
		}
		arms = append(arms, arm)
	}
	if len(arms) == 0 {
		return nil, errors.New("--arms must name at least one arm")
	}
	return arms, nil
}

// loadTasks reads tasks/*.json plus an optional fixture/ directory. The "id"
// field is required; "name" is accepted as an alias for compatibility with the
// existing eval tasksets.
func loadTasks(dir string) ([]warmcost.Task, error) {
	taskDir := filepath.Join(dir, "tasks")
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return nil, fmt.Errorf("read tasks dir %s: %w", taskDir, err)
	}
	defaultFixture := filepath.Join(dir, "fixture")
	if _, statErr := os.Stat(defaultFixture); statErr != nil {
		defaultFixture = ""
	}
	var tasks []warmcost.Task
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(taskDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read task %s: %w", path, err)
		}
		var raw struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Prompt  string `json:"prompt"`
			Check   string `json:"check"`
			Fixture string `json:"fixture"`
			Phase   string `json:"phase"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse task %s: %w", path, err)
		}
		id := strings.TrimSpace(raw.ID)
		if id == "" {
			id = strings.TrimSpace(raw.Name)
		}
		if id == "" {
			return nil, fmt.Errorf("task %s: id is required", path)
		}
		if strings.TrimSpace(raw.Prompt) == "" {
			return nil, fmt.Errorf("task %s: prompt is required", path)
		}
		if strings.TrimSpace(raw.Check) == "" {
			return nil, fmt.Errorf("task %s: check is required", path)
		}
		fixture := raw.Fixture
		if fixture != "" && !filepath.IsAbs(fixture) {
			fixture = filepath.Join(dir, fixture)
		}
		if fixture == "" {
			fixture = defaultFixture
		}
		tasks = append(tasks, warmcost.Task{ID: id, Prompt: raw.Prompt, Check: raw.Check, Fixture: fixture, Phase: strings.TrimSpace(raw.Phase)})
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("taskset %s has no tasks under tasks/*.json", dir)
	}
	return tasks, nil
}
