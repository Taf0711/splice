package cognition

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/worktrees"
)

// batchGit runs git in dir via the production capture runner and fails the
// test on error.
func batchGit(t *testing.T, dir string, args ...string) worktrees.CommandResult {
	t.Helper()
	result, err := worktrees.GitCapture(context.Background(), nil, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return result
}

// newBatchRepo builds a real git repo with one commit containing the given
// files, returning the repo root and the commit SHA.
func newBatchRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	batchGit(t, root, "init", "-b", "main")
	batchGit(t, root, "config", "user.name", "splice-test")
	batchGit(t, root, "config", "user.email", "splice-test@local")
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	batchGit(t, root, "add", "-A")
	batchGit(t, root, "commit", "-m", "base")
	out := batchGit(t, root, "rev-parse", "HEAD")
	return root, strings.TrimSpace(out.Stdout)
}

// TestChangedPathsPorcelainContract pins the exact git invocation: the batch
// must use porcelain `git diff --name-only -z --no-ext-diff --no-renames`
// (report sections 4-6), not raw diff-index, and must parse NUL-separated
// output.
func TestChangedPathsPorcelainContract(t *testing.T) {
	var gotArgs []string
	runner := func(_ context.Context, _ string, args ...string) (worktrees.CommandResult, error) {
		gotArgs = args
		return worktrees.CommandResult{Stdout: "a.go\x00b.go\x00"}, nil
	}
	changed, err := ChangedPaths(context.Background(), "/repo", "abc123", runner)
	if err != nil {
		t.Fatalf("ChangedPaths: %v", err)
	}
	want := []string{"diff", "--name-only", "-z", "--no-ext-diff", "--no-renames", "abc123"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("git args = %v, want %v", gotArgs, want)
	}
	if len(changed) != 2 || !changed["a.go"] || !changed["b.go"] {
		t.Fatalf("changed = %v, want {a.go, b.go}", changed)
	}
}

// TestChangedPathsNonZeroExitIsError pins fail-closed plumbing: a diff that
// exits non-zero (missing commit, bad repo) is an error, never an empty set.
func TestChangedPathsNonZeroExitIsError(t *testing.T) {
	runner := func(_ context.Context, _ string, _ ...string) (worktrees.CommandResult, error) {
		return worktrees.CommandResult{ExitCode: 128, Stderr: "fatal: bad object"}, nil
	}
	if _, err := ChangedPaths(context.Background(), "/repo", "deadbeef", runner); err == nil {
		t.Fatal("ChangedPaths: want error on exit 128, got nil")
	}
}

// TestChangedPathsFailClosedOnEmptyInput pins the empty-input rule.
func TestChangedPathsFailClosedOnEmptyInput(t *testing.T) {
	if _, err := ChangedPaths(context.Background(), "", "abc", nil); err == nil {
		t.Fatal("empty repoRoot: want error, got nil")
	}
	if _, err := ChangedPaths(context.Background(), "/repo", "", nil); err == nil {
		t.Fatal("empty commit: want error, got nil")
	}
}

// TestClassifyBatchMatchesClassifyFreshness is the exactness invariant: for
// one fixture repo driven through fresh/edited/stale-then-fully-committed/
// reverted transitions, ClassifyBatch (against the batched set) must return
// the identical verdict ClassifyFreshness (one git spawn per call) returns.
// This is the C1b proof that batching changes NOTHING about semantics.
func TestClassifyBatchMatchesClassifyFreshness(t *testing.T) {
	ctx := context.Background()
	root, commit := newBatchRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
		"internal/auth/token.go":   "package auth\n",
		"docs/readme.md":           "docs\n",
	})
	anchor := "internal/auth/session.go"
	pkg := "internal/auth"

	// Step 1: pristine tree (fresh for file and package anchors).
	checkExactness(t, ctx, root, commit, anchor)
	checkExactness(t, ctx, root, commit, pkg)

	// Step 2: edit the anchored file (stale for the file anchor; the package
	// anchor also goes stale through the same file).
	if err := os.WriteFile(filepath.Join(root, anchor), []byte("package auth\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkExactness(t, ctx, root, commit, anchor)
	checkExactness(t, ctx, root, commit, pkg)

	// Step 3: add a NEW file under the package directory (package stale,
	// file anchor untouched and therefore fresh again for the original file).
	if err := os.WriteFile(filepath.Join(root, "internal/auth/new.go"), []byte("package auth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	batchGit(t, root, "add", "-A")
	checkExactness(t, ctx, root, commit, anchor)
	checkExactness(t, ctx, root, commit, pkg)

	// Step 4: commit everything, then revert to the base commit's tree. The
	// file content becomes identical to the source commit again (fresh), and
	// the package directory no longer differs (fresh).
	batchGit(t, root, "checkout", "--", ".")
	batchGit(t, root, "clean", "-fd", "internal/auth")
	checkExactness(t, ctx, root, commit, anchor)
	checkExactness(t, ctx, root, commit, pkg)

	// Step 5: unknown inputs still fail closed in the batch path.
	if got := ClassifyBatch("", map[string]bool{anchor: true}); got != FreshnessUnknown {
		t.Fatalf("empty anchor = %q, want unknown", got)
	}
	if got := ClassifyBatch(anchor, nil); got != FreshnessUnknown {
		t.Fatalf("nil set = %q, want unknown", got)
	}
}

// checkExactness asserts batch and single-key classification agree for one
// anchor right now, and includes the package-directory false-prefix case.
func checkExactness(t *testing.T, ctx context.Context, root, commit, anchor string) {
	t.Helper()
	changed, err := ChangedPaths(ctx, root, commit, nil)
	if err != nil {
		t.Fatalf("ChangedPaths(%s): %v", anchor, err)
	}
	batch := ClassifyBatch(anchor, changed)
	single := ClassifyFreshness(ctx, root, commit, anchor)
	if batch != single {
		t.Fatalf("exactness violated for anchor %q: batch=%q single=%q", anchor, batch, single)
	}
}

// TestClassifyBatchPackagePrefixSegmentBoundary pins the prefix rule: a
// changed sibling directory with a shared prefix must NOT stale a package.
func TestClassifyBatchPackagePrefixSegmentBoundary(t *testing.T) {
	changed := map[string]bool{"internal/auth-other/file.go": true}
	if got := ClassifyBatch("internal/auth", changed); got != FreshnessFresh {
		t.Fatalf("sibling prefix = %q, want fresh", got)
	}
	changed = map[string]bool{"internal/auth/session.go": true}
	if got := ClassifyBatch("internal/auth", changed); got != FreshnessStale {
		t.Fatalf("inside package = %q, want stale", got)
	}
}

// TestGroupAnchorsByCommit pins the grouping rule: one entry per unique
// commit, empty commits dropped, sorted output.
func TestGroupAnchorsByCommit(t *testing.T) {
	pairs := [][2]string{
		{"c1", "a.go"},
		{"", "b.go"},
		{"c2", "c.go"},
		{"c1", "d.go"},
		{"c1", "d.go"},
	}
	got := GroupAnchorsByCommit(pairs)
	want := []string{"c1", "c2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("groups = %v, want %v", got, want)
	}
}

// TestClassifyFreshnessUnchanged guards the C0 exported surface: the single
// key path still exists and still fails closed on unknown inputs (spec
// fence: no signature or semantic change).
func TestClassifyFreshnessUnchanged(t *testing.T) {
	root, commit := newBatchRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
	})
	if got := ClassifyFreshness(context.Background(), root, commit, "internal/auth/session.go"); got != FreshnessFresh {
		t.Fatalf("fresh = %q, want fresh", got)
	}
	if got := ClassifyFreshness(context.Background(), root, "deadbeef00000000000000000000000000000000", "internal/auth/session.go"); got != FreshnessUnknown {
		t.Fatalf("bad commit = %q, want unknown", got)
	}
}
