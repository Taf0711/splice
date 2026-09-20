// Package swebenchpro runs Splice against official SWE-bench Pro instances
// and invokes the OFFICIAL Scale AI evaluator.
//
// Pinning contract (verify with `git ls-remote` and the HF API before changing):
//
//	Dataset:  huggingface.co/datasets/ScaleAI/SWE-bench_Pro (public variant)
//	Revision: PinnedDatasetRevision (split "test", single parquet shard)
//	Evaluator: github.com/scaleapi/SWE-bench_Pro-os
//	Commit:   PinnedEvaluatorCommit
//
// The runner never judges a patch itself. It writes predictions in the exact
// JSON shape the official evaluator consumes
// (swe_bench_pro_eval.py: [{"instance_id","patch","prefix"}, ...]) and runs
// the official swe_bench_pro_eval.py for pass/fail.
package swebenchpro

import (
	"fmt"
	"os/exec"
)

// SchemaVersion is the version of the JSON artifacts this package writes.
const SchemaVersion = 1

// PinnedDataset is the official public SWE-bench Pro dataset id.
// Note: the HF organization is "ScaleAI" (capital S, AI). There is no
// "scaleapi/SWE-bench_Pro-os" dataset; "-os" is the GitHub org suffix only.
const PinnedDataset = "ScaleAI/SWE-bench_Pro"

// PinnedDatasetRevision pins the dataset snapshot so any upstream edit
// invalidates old results loudly instead of silently comparing across data.
// Verified 2026-09-05 via https://huggingface.co/api/datasets/ScaleAI/SWE-bench_Pro
const PinnedDatasetRevision = "7ab5114912baf22bb098818e604c02fe7ad2c11f"

// PinnedDatasetSplit is the only split the public dataset ships.
const PinnedDatasetSplit = "test"

// PinnedEvaluatorRepo is the official evaluation repository.
const PinnedEvaluatorRepo = "https://github.com/scaleapi/SWE-bench_Pro-os"

// PinnedEvaluatorCommit pins swe_bench_pro_eval.py and its helper_code.
// Verified 2026-09-05 via api.github.com/repos/scaleapi/SWE-bench_Pro-os/commits?per_page=1
const PinnedEvaluatorCommit = "ca10a60a5fcae51e6948ffe1485d4153d421e6c5"

// PinnedDockerImages is the official prebuilt image repository.
const PinnedDockerImages = "jefzda/sweap-images"

// Config drives one benchmark run.
type Config struct {
	// RepoDir is the local clone of scaleapi/SWE-bench_Pro-os at
	// PinnedEvaluatorCommit. It must contain swe_bench_pro_eval.py,
	// helper_code/, run_scripts/, and dockerfiles/.
	RepoDir string `json:"repo_dir"`
	// RawSamplePath is the CSV with columns instance_id, before_repo_set_cmd,
	// selected_test_files_to_run, base_commit, base_dockerfile,
	// instance_dockerfile, FAIL_TO_PASS, PASS_TO_PASS (evaluator input).
	RawSamplePath string `json:"raw_sample_path"`
	// SpliceBin is the splice binary to run headlessly. Empty means "splice"
	// from PATH.
	SpliceBin string `json:"splice_bin,omitempty"`
	// WorkspaceDir holds per-instance checkouts and outputs.
	WorkspaceDir string `json:"workspace_dir"`
	// DockerHubUsername is passed to the official evaluator.
	DockerHubUsername string `json:"dockerhub_username"`
	// UseLocalDocker mirrors the official evaluator's --use_local_docker flag.
	UseLocalDocker bool `json:"use_local_docker,omitempty"`
	// NumWorkers is forwarded to the official evaluator (default 100).
	NumWorkers int `json:"num_workers,omitempty"`
	// TimeoutPerInstance bounds one Splice headless run in seconds.
	// 0 means no explicit bound beyond the parent context.
	TimeoutPerInstance int `json:"timeout_per_instance,omitempty"`
}

// Validate enforces the documented minimum configuration.
func (c *Config) Validate() error {
	if c.RepoDir == "" {
		return fmt.Errorf("swebenchpro.Config: repo_dir is required (clone of %s at %s)", PinnedEvaluatorRepo, PinnedEvaluatorCommit)
	}
	if c.RawSamplePath == "" {
		return fmt.Errorf("swebenchpro.Config: raw_sample_path is required (CSV with instance_id, before_repo_set_cmd, selected_test_files_to_run, base_commit, base_dockerfile, instance_dockerfile, FAIL_TO_PASS, PASS_TO_PASS)")
	}
	if c.WorkspaceDir == "" {
		return fmt.Errorf("swebenchpro.Config: workspace_dir is required")
	}
	if c.NumWorkers < 0 {
		return fmt.Errorf("swebenchpro.Config: num_workers must be >= 0, got %d", c.NumWorkers)
	}
	if c.TimeoutPerInstance < 0 {
		return fmt.Errorf("swebenchpro.Config: timeout_per_instance must be >= 0, got %d", c.TimeoutPerInstance)
	}
	return nil
}

// SpliceCommand returns the splice binary path from config or PATH default.
func (c *Config) SpliceCommand() string {
	if c.SpliceBin != "" {
		return c.SpliceBin
	}
	return "splice"
}

// Prediction is one entry in the patch file the OFFICIAL evaluator consumes.
// Shape is pinned by swe_bench_pro_eval.py (--patch_path JSON):
//
//	[{"instance_id": "...", "patch": "diff --git ...", "prefix": "..."}]
//
// prefix is required by the official gather/eval flow; Patch may be empty
// (the evaluator treats an empty patch as a failed instance, never a crash).
type Prediction struct {
	InstanceID string `json:"instance_id"`
	Patch      string `json:"patch"`
	Prefix     string `json:"prefix"`
}

// VerifyEvaluatorRepo checks the evaluator checkout is present, is the
// official repository, and is at the pinned commit. A drift here must fail
// loudly: silently evaluating against a different evaluator breaks every
// reported number.
func VerifyEvaluatorRepo(repoDir string) error {
	if repoDir == "" {
		return fmt.Errorf("swebenchpro.VerifyEvaluatorRepo: repoDir is empty")
	}
	git := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD")
	out, err := git.Output()
	if err != nil {
		return fmt.Errorf("swebenchpro.VerifyEvaluatorRepo: git rev-parse in %s: %w", repoDir, err)
	}
	commit := trimSpace(string(out))
	if commit != PinnedEvaluatorCommit {
		return fmt.Errorf("swebenchpro.VerifyEvaluatorRepo: evaluator commit drift: have %s, pin is %s", commit, PinnedEvaluatorCommit)
	}
	gitURL := exec.Command("git", "-C", repoDir, "config", "--get", "remote.origin.url")
	out, err = gitURL.Output()
	if err != nil {
		return fmt.Errorf("swebenchpro.VerifyEvaluatorRepo: remote.origin.url missing in %s: %w", repoDir, err)
	}
	url := trimSpace(string(out))
	if !isOfficialRepoURL(url) {
		return fmt.Errorf("swebenchpro.VerifyEvaluatorRepo: remote %s is not the official evaluator repo %s", url, PinnedEvaluatorRepo)
	}
	for _, f := range []string{"swe_bench_pro_eval.py", "helper_code", "run_scripts", "dockerfiles"} {
		if !fileExists(repoDir + "/" + f) {
			return fmt.Errorf("swebenchpro.VerifyEvaluatorRepo: %s missing from %s (pinned commit %s)", f, repoDir, PinnedEvaluatorCommit)
		}
	}
	return nil
}

// isOfficialRepoURL accepts the canonical URL and its common git variants.
func isOfficialRepoURL(url string) bool {
	switch url {
	case PinnedEvaluatorRepo,
		"git@github.com:scaleapi/SWE-bench_Pro-os.git",
		"ssh://git@github.com/scaleapi/SWE-bench_Pro-os.git",
		"https://github.com/scaleapi/SWE-bench_Pro-os.git":
		return true
	}
	return false
}
