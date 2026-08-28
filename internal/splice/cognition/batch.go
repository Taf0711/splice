package cognition

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/worktrees"
)

// batch.go implements the C1b layer 1 (batched freshness): instead of one
// `git diff --quiet` subprocess per observation, one porcelain
// `git diff --name-only -z --no-ext-diff --no-renames <commit>` per UNIQUE
// source commit returns the changed-path set, and each observation's anchor
// is classified in-process against that set. Porcelain diff (not raw
// diff-index) matches the C0 `git diff --quiet` semantics: it refreshes index
// stat metadata, so a bare touch that leaves content identical does not read
// as a change. --no-renames keeps a renamed original path stale, which is the
// correct freshness verdict: the structural anchor no longer exists.
//
// Exactness contract: batching changes NOTHING about freshness semantics.
// ClassifyFreshness stays the single-key path; ClassifyBatch must agree with
// it on every input (proved by the exactness-invariant test in batch_test.go).
// Any spawn error is unknown, and unknown fails closed to the broad-search
// fallback, byte-identically to C0.

// gitCapture is the injected git runner seam for tests: it returns the full
// command result so the changed-path query can read stdout. Nil callers use
// the production runner, which is worktrees.GitCapture on the package's
// single git exec path.
type gitCapture func(ctx context.Context, dir string, args ...string) (worktrees.CommandResult, error)

// productionCapture is the package-level default runner. It lives behind an
// indirection so SetGitCaptureForTest can swap it for a counting fake in the
// splice-package integration tests, and the swap is restored by deferral.
var productionCapture gitCapture = func(ctx context.Context, dir string, args ...string) (worktrees.CommandResult, error) {
	return worktrees.GitCapture(ctx, nil, dir, args...)
}

// SetGitCaptureForTest swaps the production changed-path runner and returns
// the previous value. Callers must restore it (defer). It exists so the
// splice-package integration tests can count real git process spawns through
// prepareStageInput without changing any production signature.
func SetGitCaptureForTest(run gitCapture) gitCapture {
	prev := productionCapture
	productionCapture = run
	return prev
}

// ChangedPaths returns the repo-relative paths changed between sourceCommit
// and the working tree, as one porcelain spawn. Empty/blank entries are
// skipped. A spawn error or non-zero exit is an error (the caller fails
// closed to unknown), and the error names the failing input.
func ChangedPaths(ctx context.Context, repoRoot, sourceCommit string, run gitCapture) (map[string]bool, error) {
	if strings.TrimSpace(repoRoot) == "" || strings.TrimSpace(sourceCommit) == "" {
		return nil, fmt.Errorf("cognition: changed-path query needs a repo root and a source commit, got root=%q commit=%q", repoRoot, sourceCommit)
	}
	if run == nil {
		run = productionCapture
	}
	result, err := run(ctx, repoRoot, "diff", "--name-only", "-z", "--no-ext-diff", "--no-renames", sourceCommit)
	if err != nil {
		return nil, fmt.Errorf("cognition: changed-path diff for commit %s in %s: %w", sourceCommit, repoRoot, err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("cognition: changed-path diff for commit %s in %s: exit %d", sourceCommit, repoRoot, result.ExitCode)
	}
	changed := make(map[string]bool)
	for _, path := range strings.Split(result.Stdout, "\x00") {
		if strings.TrimSpace(path) != "" {
			changed[path] = true
		}
	}
	return changed, nil
}

// ClassifyBatch classifies one anchor path in-process against a ChangedPaths
// set. Semantics mirror ClassifyFreshness exactly:
//
//   - empty inputs: unknown (fail closed, same as the single-key path);
//   - file/symbol anchors: present in the set is stale, absent is fresh;
//   - package-directory anchors: any changed path under the directory is
//     stale (prefix match on path segments, so "pkg" does not match
//     "pkg-other/file.go");
//   - the caller turns any spawn error into unknown before calling this.
func ClassifyBatch(anchorPath string, changed map[string]bool) FreshnessState {
	if strings.TrimSpace(anchorPath) == "" {
		return FreshnessUnknown
	}
	if changed == nil {
		// A nil set carries no information: fail closed rather than reading
		// every path as fresh.
		return FreshnessUnknown
	}
	if _, ok := changed[anchorPath]; ok {
		return FreshnessStale
	}
	prefix := anchorPath + "/"
	for path := range changed {
		if strings.HasPrefix(path, prefix) {
			return FreshnessStale
		}
	}
	return FreshnessFresh
}

// GroupAnchorsByCommit returns the unique source commits in the given
// (commit, anchor) pairs, in sorted order. The caller derives one batch query
// per unique commit instead of one per observation or per invocation; the
// report's correction makes grouping BY COMMIT authoritative.
func GroupAnchorsByCommit(pairs [][2]string) []string {
	seen := make(map[string]bool)
	commits := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		commit := strings.TrimSpace(pair[0])
		if commit == "" || seen[commit] {
			continue
		}
		seen[commit] = true
		commits = append(commits, commit)
	}
	sort.Strings(commits)
	return commits
}
