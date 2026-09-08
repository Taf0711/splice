package worktrees

// review_snapshot.go: the review/apply boundary (TUI review finding 1).
//
// A diff of `<base>...HEAD` compares COMMITTED trees. A pipeline run leaves
// its work in the worktree as unstaged, staged, or untracked files, and
// MergeBack commits all of that (commitWorktreeChanges: `git add -A` then
// commit) before it merges. So a review that reads committed history and an
// apply that commits the working tree inspect different content: the user
// could approve changes they never saw, and an ordinary successful run
// could show an empty diff.
//
// ReviewSnapshot closes that gap. It builds an immutable tree object that
// contains every change class the apply path will commit, and returns its
// SHA alongside the patch. The apply path revalidates against that same SHA
// (MergeBackOptions.ExpectedTree), so work that changed after the review
// opened is refused instead of silently merged.
//
// The snapshot never mutates the worktree: the staging runs against a
// temporary GIT_INDEX_FILE, exactly like IterationRecovery.Capture. The
// real index, HEAD, and the workspace files are untouched.

import (
	"context"
	"fmt"
	"strings"
)

// ReviewSnapshotResult is one immutable review boundary. Tree is the git
// tree object holding the reviewed content; Patch is the diff of that tree
// against Base. An empty Patch with no error means the lane genuinely
// produced no changes, which is a loaded result, not a missing one.
type ReviewSnapshotResult struct {
	Tree  string `json:"tree"`
	Base  string `json:"base"`
	Patch string `json:"patch"`
}

// ReviewSnapshotOptions selects the worktree and the base ref to compare
// against. BaseRef is typically the lane's source branch.
type ReviewSnapshotOptions struct {
	WorktreePath string
	BaseRef      string
	RunGit       GitRunner
	EnvGit       envGitRunner
}

// CaptureReviewSnapshot builds the review snapshot for a worktree. The
// returned tree contains committed changes, index changes, working tree
// changes, and untracked files, which is exactly the set MergeBack commits.
func CaptureReviewSnapshot(ctx context.Context, options ReviewSnapshotOptions) (ReviewSnapshotResult, error) {
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	envGit := options.EnvGit
	if envGit == nil {
		envGit = defaultEnvRunGit
	}
	dir := strings.TrimSpace(options.WorktreePath)
	if dir == "" {
		return ReviewSnapshotResult{}, fmt.Errorf("review snapshot: empty worktree path")
	}

	indexFile, cleanup, err := tempIndexFile()
	if err != nil {
		return ReviewSnapshotResult{}, err
	}
	defer cleanup()
	env := gitIndexEnv(indexFile)

	// Seed the temporary index from HEAD, then stage everything the apply
	// path would stage. Untracked files are included by `add -A`, so the
	// resulting tree covers all four change classes.
	if _, err := commandOutput(envGit(ctx, dir, env, "read-tree", "HEAD")); err != nil {
		return ReviewSnapshotResult{}, fmt.Errorf("seed review index for %s: %w", dir, err)
	}
	if _, err := commandOutput(envGit(ctx, dir, env, "add", "-A")); err != nil {
		return ReviewSnapshotResult{}, fmt.Errorf("stage review snapshot for %s: %w", dir, err)
	}
	tree, err := commandOutput(envGit(ctx, dir, env, "write-tree"))
	if err != nil {
		return ReviewSnapshotResult{}, fmt.Errorf("write review tree for %s: %w", dir, err)
	}
	tree = strings.TrimSpace(tree)

	// Diff against the merge base, so the patch shows the lane's own work
	// and not the base branch's unrelated drift.
	base := strings.TrimSpace(options.BaseRef)
	if base == "" {
		base = "HEAD"
	}
	diffFrom := base
	if mergeBase, err := gitOutput(ctx, runGit, dir, "merge-base", base, "HEAD"); err == nil {
		if trimmed := strings.TrimSpace(mergeBase); trimmed != "" {
			diffFrom = trimmed
		}
	}
	patch, err := commandOutput(envGit(ctx, dir, env, "diff", "--no-color", diffFrom, tree))
	if err != nil {
		return ReviewSnapshotResult{}, fmt.Errorf("diff review tree %s for %s: %w", tree, dir, err)
	}
	return ReviewSnapshotResult{Tree: tree, Base: base, Patch: patch}, nil
}

// CurrentReviewTree recomputes the snapshot tree only, with no diff. The
// apply path uses it to confirm the worktree still holds the content the
// user reviewed.
func CurrentReviewTree(ctx context.Context, worktreePath string, envGit envGitRunner) (string, error) {
	result, err := CaptureReviewSnapshot(ctx, ReviewSnapshotOptions{WorktreePath: worktreePath, EnvGit: envGit})
	if err != nil {
		return "", err
	}
	return result.Tree, nil
}
