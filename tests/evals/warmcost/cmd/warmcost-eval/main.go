// Command warmcost-eval runs the paired cold-versus-warm measurement design in
// tests/evals/cognition-families/MEASUREMENT_DESIGN.md.
//
// It is a release-cadence tool, not a CI test. It never selects a provider or
// a model by itself: the operator supplies the exact approved binary and model.
//
// Taskset format: --tasks points at a directory with tasks/*.json
// ({"id","prompt","check","fixture","phase","precursor"}) and an optional fixture/ directory used
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
	noCommitWrites    bool
	noMemoryPrefetch  bool
	trustProject      bool
	armEnv            stringList
	runID             string
}

func main() {
	opts := options{}
	bindFlags(flag.CommandLine, &opts)
	flag.Parse()

	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "warmcost-eval:", err)
		os.Exit(1)
	}
}

// bindFlags registers every warmcost-eval flag on fs, so a test can parse
// flags without touching the global flag set.
func bindFlags(fs *flag.FlagSet, opts *options) {
	fs.StringVar(&opts.repo, "repo", ".", "repository root used for the binary revision")
	fs.StringVar(&opts.binary, "binary", "", "path to the splice binary (required)")
	fs.StringVar(&opts.model, "model", "", "model id for every attempt")
	fs.StringVar(&opts.reasoningEffort, "reasoning-effort", "", "recorded reasoning effort setting")
	fs.StringVar(&opts.maxTurns, "max-turns", "", "recorded max turns setting")
	fs.StringVar(&opts.tasks, "tasks", "", "taskset directory (required)")
	fs.StringVar(&opts.out, "out", "tests/evals/results", "output directory for the run artifacts")
	fs.IntVar(&opts.repeats, "repeats", 1, "repeats per task per arm")
	fs.StringVar(&opts.arms, "arms", "cold,warm", "comma-separated arms: cold,warm,warm-retrieval-only")
	fs.DurationVar(&opts.timeout, "timeout", 30*time.Minute, "per-attempt timeout")
	fs.IntVar(&opts.bootstrapSamples, "bootstrap-samples", 10000, "task-clustered bootstrap samples")
	fs.Int64Var(&opts.bootstrapSeed, "bootstrap-seed", 1, "bootstrap seed")
	fs.Float64Var(&opts.correctnessMargin, "correctness-margin", 0, "pre-registered acceptable success-rate drop")
	fs.StringVar(&opts.binaryRevision, "binary-revision", "", "revision of the binary under test; overrides the --repo derivation when set")
	fs.StringVar(&opts.sidecarRevision, "sidecar-revision", "", "sidecar binary revision recorded per attempt")
	fs.StringVar(&opts.retention, "retention", "fresh", "sidecar lifetime protocol: fresh or shared")
	fs.StringVar(&opts.sidecarRoot, "sidecar-root", "", "directory for per-class sidecar sockets and databases (required for shared retention)")
	fs.BoolVar(&opts.keepWorkspaces, "keep-workspaces", false, "keep per-attempt workspaces for debugging")
	fs.Var(&opts.armEnv, "arm-env", "KEY=VALUE switch that replaces the same key in every arm that sets it (repeatable)")
	fs.BoolVar(&opts.noMemoryPrefetch, "no-memory-prefetch", false, "turn memory prefetch off in every arm (the prefetch ablation)")
	fs.BoolVar(&opts.noCommitWrites, "no-commit-writes", false, "do not commit and reanchor a verified write; the read runs on its uncommitted changes")
	fs.BoolVar(&opts.trustProject, "trust-project", false, "trust each attempt workspace through SPLICE_TRUST_WORKSPACE=1 so its .splice/config.json loads (for example verify.testCommand); default leaves the trust variable unset")
	fs.StringVar(&opts.runID, "run-id", "", "run id (default: timestamp)")
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
	tasks, err := warmcost.LoadTaskset(opts.tasks)
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
		NoCommitWrites:    opts.noCommitWrites,
		NoMemoryPrefetch:  opts.noMemoryPrefetch,
		ArmEnvOverrides:   opts.armEnv,
		TrustProject:      opts.trustProject,
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

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }
