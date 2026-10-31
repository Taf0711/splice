package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/providers/decisions"
	"github.com/Taf0711/splice/tests/evals/decisionreplay"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run replays the records with the configured backend. Its JSON result goes to
// stdout. With --evidence compare, the drift table goes to stderr.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cli := flag.NewFlagSet("decision-replay", flag.ContinueOnError)
	var paths []string
	cli.Func("records", "JSONL file or directory; repeat for multiple paths", func(path string) error { paths = append(paths, path); return nil })
	var tracePaths []string
	cli.Func("traces", "Run trace file or directory (RunOutcome JSON or JSONL), for --evidence compare; repeat for multiple paths", func(path string) error { tracePaths = append(tracePaths, path); return nil })
	backendPath := cli.String("backend-config", "", "JSON file with a decision_advisor configuration object")
	outPath := cli.String("out", "", "Output JSONL file")
	pack := cli.String("pack", "", "Override pack: traj_action_v2, traj_action_v1, or traj_v1")
	thresholds := cli.String("thresholds", "", "Optional JSON object with label thresholds")
	concurrency := cli.Int("concurrency", 2, "Maximum concurrent evaluations")
	evidence := cli.String("evidence", "recorded", "Evidence source: recorded, swap, or compare (both, with a drift summary)")
	if err := cli.Parse(args); err != nil {
		return err
	}
	paths = append(paths, cli.Args()...)
	compare := *evidence == "compare"
	if len(paths) == 0 || *backendPath == "" || (*outPath == "" && !compare) {
		return fmt.Errorf("--records, --backend-config, and --out are required (--out is not used by --evidence compare)")
	}
	if *evidence != "recorded" && *evidence != "swap" && !compare {
		return fmt.Errorf("--evidence must be recorded, swap, or compare")
	}
	if len(tracePaths) > 0 && !compare {
		return fmt.Errorf("--traces is used only by --evidence compare")
	}
	raw, err := os.ReadFile(*backendPath)
	if err != nil {
		return fmt.Errorf("backend config: %w", err)
	}
	var cfg config.DecisionAdvisorConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("backend config: %w", err)
	}
	cfg = cfg.WithDefaults()
	cfg.Mode = "active_experimental"
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	// The project config merges only when the workspace is trusted.
	userConfigPath, err := config.DefaultUserConfigPath()
	if err != nil {
		return err
	}
	trusted, err := config.ResolveProjectTrust(cwd, userConfigPath, false, false)
	if err != nil {
		return err
	}
	resolveOptions, err := config.WorkspaceResolveOptions(cwd, trusted)
	if err != nil {
		return err
	}
	resolveOptions.Overrides.Flags = map[string]bool{string(flags.DecisionAdvisorEnabled): true}
	resolved, err := config.Resolve(resolveOptions)
	if err != nil {
		return fmt.Errorf("resolve Splice config: %w", err)
	}
	if cfg.Backend == "provider" {
		for _, profile := range resolved.Providers {
			if profile.Name == cfg.ProviderProfile {
				cfg.Profile = profile.Model
				break
			}
		}
	}
	evaluator, err := decisions.NewEvaluator(cfg, resolved)
	if err != nil {
		return fmt.Errorf("decision backend: %w", err)
	}
	policyThresholds := resolved.Trajectory.Thresholds
	if *thresholds != "" {
		if err := json.Unmarshal([]byte(*thresholds), &policyThresholds); err != nil {
			return fmt.Errorf("thresholds: %w", err)
		}
	}
	records, err := decisionreplay.LoadRecords(paths)
	if err != nil {
		return err
	}
	options := decisionreplay.Options{BackendConfig: cfg, Pack: *pack, Thresholds: policyThresholds, Concurrency: *concurrency}
	if compare {
		comparison, err := decisionreplay.Compare(ctx, records, evaluator, options)
		if err != nil {
			return err
		}
		if len(tracePaths) > 0 {
			traces, err := decisionreplay.LoadTraceEvidence(tracePaths)
			if err != nil {
				return err
			}
			comparison.Traces = &traces
		}
		if err := decisionreplay.WriteTable(stderr, comparison); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(comparison)
	}
	options.Evidence = *evidence
	out, err := os.OpenFile(*outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("output: %w", err)
	}
	summary, replayErr := decisionreplay.Replay(ctx, records, evaluator, options, out)
	closeErr := out.Close()
	if replayErr != nil {
		return replayErr
	}
	if closeErr != nil {
		return fmt.Errorf("close output: %w", closeErr)
	}
	return json.NewEncoder(stdout).Encode(summary)
}
