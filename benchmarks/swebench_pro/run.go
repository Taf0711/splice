package swebenchpro

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RunResult records one headless Splice run against one instance.
type RunResult struct {
	InstanceID  string        `json:"instance_id"`
	Patch       string        `json:"patch"`
	TracePath   string        `json:"splice_trace_path,omitempty"`
	Workspace   string        `json:"workspace_artifact,omitempty"`
	TraceEvents []TraceEvent  `json:"-"`
	Err         string        `json:"error,omitempty"`
	Duration    time.Duration `json:"-"`
}

// TraceEvent is the subset of Splice's stream-json output events this runner
// consumes. Unknown fields and event types are ignored on decode (the
// protocol is additive; see docs/STREAM_JSON_PROTOCOL.md).
type TraceEvent struct {
	SchemaVersion int              `json:"schemaVersion"`
	Type          string           `json:"type"`
	Usage         *json.RawMessage `json:"usage,omitempty"`
}

// PromptForInstance builds the headless prompt from the ORIGINAL issue text.
// No rewriting, no summarizing: the benchmark contract is that the model sees
// the same issue text a human filed.
func PromptForInstance(issueText string) string {
	return strings.TrimSpace(issueText)
}

// checkoutInstance prepares a per-instance workspace: clone or reuse the
// instance repo at base_commit. The caller supplies the repo URL; SWE-bench
// Pro rows carry repo/commit columns, and the official evaluator re-checks
// out base_commit itself inside the container, so this checkout exists only
// to host the Splice run and the captured diff.
func checkoutInstance(ctx context.Context, cfg *Config, instanceID, repoURL, baseCommit string) (string, error) {
	ws := filepath.Join(cfg.WorkspaceDir, instanceID)
	if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
		return "", fmt.Errorf("swebenchpro.checkoutInstance: mkdir %s: %w", ws, err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".git")); err == nil {
		return ws, nil // reuse: deterministic re-runs keep the same checkout
	}
	clone := exec.CommandContext(ctx, "git", "clone", repoURL, ws)
	if out, err := clone.CombinedOutput(); err != nil {
		return "", fmt.Errorf("swebenchpro.checkoutInstance: clone %s: %w: %s", repoURL, err, strings.TrimSpace(string(out)))
	}
	co := exec.CommandContext(ctx, "git", "-C", ws, "checkout", baseCommit)
	if out, err := co.CombinedOutput(); err != nil {
		return "", fmt.Errorf("swebenchpro.checkoutInstance: checkout %s: %w: %s", baseCommit, err, strings.TrimSpace(string(out)))
	}
	return ws, nil
}

// capturePatch returns the working-tree diff of the instance checkout. An
// empty diff is a legitimate outcome (no changes made) and yields an empty
// patch string, which the official evaluator scores as failed.
func capturePatch(ctx context.Context, ws string) (string, error) {
	diff := exec.CommandContext(ctx, "git", "-C", ws, "diff")
	var out, errb bytes.Buffer
	diff.Stdout = &out
	diff.Stderr = &errb
	if err := diff.Run(); err != nil {
		return "", fmt.Errorf("swebenchpro.capturePatch: git diff in %s: %w: %s", ws, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// RunInstance launches Splice headlessly inside the instance checkout with
// the original issue text as the prompt, streams stream-json events to a
// trace file, and captures the final git diff as the patch.
//
// It does NOT judge the patch. Judging belongs exclusively to the official
// swe_bench_pro_eval.py.
func RunInstance(ctx context.Context, cfg *Config, instanceID, repoURL, baseCommit, issueText string) (*RunResult, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ws, err := checkoutInstance(ctx, cfg, instanceID, repoURL, baseCommit)
	if err != nil {
		return nil, err
	}
	if err := runGit(ctx, ws, "reset", "--hard", baseCommit); err != nil {
		return nil, err
	}
	if err := runGit(ctx, ws, "clean", "-fdx"); err != nil {
		return nil, err
	}

	tracePath := filepath.Join(ws, "splice_trace.jsonl")
	traceFile, err := os.Create(tracePath)
	if err != nil {
		return nil, fmt.Errorf("swebenchpro.RunInstance: create %s: %w", tracePath, err)
	}
	defer traceFile.Close()

	res := &RunResult{InstanceID: instanceID, TracePath: tracePath, Workspace: ws}
	start := time.Now()

	runCtx := ctx
	if cfg.TimeoutPerInstance > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.TimeoutPerInstance)*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(runCtx, cfg.SpliceCommand(), "exec",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
	)
	cmd.Dir = ws
	cmd.Stdin = strings.NewReader(streamJSONInput(issueText))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("swebenchpro.RunInstance: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("swebenchpro.RunInstance: start %s: %w", cfg.SpliceCommand(), err)
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	var events []TraceEvent
	for sc.Scan() {
		line := sc.Bytes()
		traceFile.Write(append(append([]byte{}, line...), '\n'))
		var ev TraceEvent
		if json.Unmarshal(line, &ev) == nil && ev.Type != "" {
			events = append(events, ev)
		}
	}
	if err := cmd.Wait(); err != nil {
		res.Err = fmt.Sprintf("splice exec exited: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	res.Duration = time.Since(start)
	res.TraceEvents = events

	patch, derr := capturePatch(ctx, ws)
	if derr != nil {
		return res, derr
	}
	res.Patch = patch
	return res, nil
}

// streamJSONInput wraps the prompt in the minimal accepted input event
// (docs/STREAM_JSON_PROTOCOL.md: one prompt event, schema version 2).
func streamJSONInput(issueText string) string {
	payload, _ := json.Marshal(PromptForInstance(issueText))
	return fmt.Sprintf("{\"schemaVersion\":2,\"type\":\"prompt\",\"content\":%s}\n", payload)
}

func runGit(ctx context.Context, dir string, args ...string) error {
	gitArgs := append([]string{"-C", dir}, args...)
	c := exec.CommandContext(ctx, "git", gitArgs...)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("swebenchpro.runGit: git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// InvokeOfficialEvaluator shells out to the OFFICIAL swe_bench_pro_eval.py
// inside the pinned evaluator checkout. It never reimplements pass/fail.
// Command shape mirrors the official README:
//
//	python swe_bench_pro_eval.py \
//	  --raw_sample_path=<csv> --patch_path=<json> --output_dir=<dir> \
//	  --scripts_dir=run_scripts --num_workers=<n> --dockerhub_username=<u>
//	  [--use_local_docker]
func InvokeOfficialEvaluator(ctx context.Context, cfg *Config, patchesPath, outputDir string) ([]byte, error) {
	if err := VerifyEvaluatorRepo(cfg.RepoDir); err != nil {
		return nil, err
	}
	args := []string{
		"--raw_sample_path=" + cfg.RawSamplePath,
		"--patch_path=" + patchesPath,
		"--output_dir=" + outputDir,
		"--scripts_dir=run_scripts",
		"--dockerhub_username=" + cfg.DockerHubUsername,
	}
	if cfg.NumWorkers > 0 {
		args = append(args, "--num_workers="+fmt.Sprint(cfg.NumWorkers))
	}
	if cfg.UseLocalDocker {
		args = append(args, "--use_local_docker")
	}
	scriptArgs := append([]string{"swe_bench_pro_eval.py"}, args...)
	cmd := exec.CommandContext(ctx, "python", scriptArgs...)
	cmd.Dir = cfg.RepoDir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("swebenchpro.InvokeOfficialEvaluator: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}
