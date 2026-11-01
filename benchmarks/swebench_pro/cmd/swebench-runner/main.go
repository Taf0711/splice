// Command swebench-runner runs the Splice SWE-bench Pro adapter end to end:
//
//  1. run Splice headlessly on each dev-subset instance,
//  2. write predictions in the OFFICIAL evaluator format,
//  3. invoke the OFFICIAL swe_bench_pro_eval.py.
//
// It performs no scoring of its own. See benchmarks/swebench_pro/dev_subset.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Taf0711/splice/benchmarks/swebench_pro"
)

func main() {
	cfgPath := flag.String("config", "", "JSON config file (see swebenchpro.Config)")
	outDir := flag.String("out", "swebench_out", "output directory for predictions and evaluator results")
	dryRun := flag.Bool("dry-run", false, "validate config, verify evaluator pin, write the dev-subset manifest, then exit")
	flag.Parse()

	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "swebench-runner: -config is required")
		os.Exit(2)
	}
	data, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: read config: %v\n", err)
		os.Exit(2)
	}
	var cfg swebenchpro.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: parse config: %v\n", err)
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: %v\n", err)
		os.Exit(2)
	}
	if err := swebenchpro.VerifyEvaluatorRepo(cfg.RepoDir); err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: %v\n", err)
		os.Exit(2)
	}
	manifestPath := filepath.Join(*outDir, "dev_subset_manifest.json")
	if err := swebenchpro.WriteDevSubsetManifest(manifestPath); err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: %v\n", err)
		os.Exit(2)
	}
	if *dryRun {
		fmt.Printf("swebench-runner: dry run OK (dataset %s@%s, evaluator %s)\n",
			swebenchpro.PinnedDataset, swebenchpro.PinnedDatasetRevision, swebenchpro.PinnedEvaluatorCommit)
		return
	}

	// Full runs require instance rows (repo URL, base commit, issue text).
	// Rows come from the pinned dataset snapshot; a companion loader is
	// planned (see dev_subset.md, "Blocked"). Until it lands, the runner
	// errors instead of guessing row fields.
	rowsPath := os.Getenv("SWEBENCH_PRO_ROWS_JSON")
	if rowsPath == "" {
		fmt.Fprintln(os.Stderr, "swebench-runner: SWEBENCH_PRO_ROWS_JSON (path to dataset rows JSON for the dev subset) is required for full runs")
		os.Exit(2)
	}
	rows, err := swebenchpro.LoadRows(rowsPath, swebenchpro.DevSubset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: %v\n", err)
		os.Exit(2)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: %v\n", err)
		os.Exit(2)
	}
	var preds []swebenchpro.Prediction
	prefix := "splice"
	for _, row := range rows {
		res, err := swebenchpro.RunInstance(context.Background(), &cfg, row.InstanceID, row.RepoURL, row.BaseCommit, row.IssueText)
		if err != nil {
			fmt.Fprintf(os.Stderr, "swebench-runner: %s: %v\n", row.InstanceID, err)
			os.Exit(2)
		}
		preds = append(preds, swebenchpro.Prediction{InstanceID: row.InstanceID, Patch: res.Patch, Prefix: prefix})
	}
	patchesPath := filepath.Join(*outDir, "patches.json")
	if err := swebenchpro.WritePredictions(patchesPath, prefix, preds); err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: %v\n", err)
		os.Exit(2)
	}
	outputDir := filepath.Join(*outDir, "evaluator_output")
	if _, err := swebenchpro.InvokeOfficialEvaluator(context.Background(), &cfg, patchesPath, outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "swebench-runner: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("swebench-runner: official evaluator output in %s\n", outputDir)
}
