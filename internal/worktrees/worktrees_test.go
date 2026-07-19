package worktrees

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDefaultRunGitSeparatesStdoutAndStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()

	// A successful command writes to Stdout, leaving Stderr clean.
	ok, err := defaultRunGit(context.Background(), dir, "--version")
	if err != nil {
		t.Fatalf("git --version returned error: %v", err)
	}
	if !strings.Contains(ok.Stdout, "git version") {
		t.Fatalf("Stdout = %q, want a git version line", ok.Stdout)
	}
	if strings.TrimSpace(ok.Stderr) != "" {
		t.Fatalf("Stderr should be empty on success, got %q", ok.Stderr)
	}

	// A failing command's diagnostic must land on Stderr, not Stdout — the prior
	// CombinedOutput merged them and left Stderr empty.
	bad, err := defaultRunGit(context.Background(), dir, "not-a-real-subcommand")
	if err != nil {
		t.Fatalf("a non-splice git exit must not be a runner error, got %v", err)
	}
	if bad.ExitCode == 0 {
		t.Fatalf("expected non-splice exit code for a bad subcommand")
	}
	if strings.TrimSpace(bad.Stderr) == "" {
		t.Fatalf("expected the git error on Stderr, got Stdout=%q Stderr=%q", bad.Stdout, bad.Stderr)
	}
}

func TestPrepareCreatesDetachedGitWorktree(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{},
		},
	}

	result, err := Prepare(context.Background(), Options{
		Cwd:     root,
		Name:    "review-api",
		BaseDir: base,
		Now:     fixedTime("2026-06-05T10:30:00Z"),
		RunGit:  runner.Run,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	if result.Name != "review-api" {
		t.Fatalf("Name = %q, want review-api", result.Name)
	}
	if result.RepoRoot != root || result.SourceBranch != "main" || result.SourceCommit != "abc1234" {
		t.Fatalf("unexpected result metadata: %#v", result)
	}
	if !strings.HasPrefix(result.Path, filepath.Join(base, "splice-worktree-")) {
		t.Fatalf("Path = %q, want under base %q", result.Path, base)
	}
	if got := runner.commandLine(3); got != "git worktree add --detach "+result.Path+" HEAD" {
		t.Fatalf("git worktree command = %q", got)
	}
}

func TestPrepareReusesExistingGitWorktree(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	sourceGit := filepath.Join(root, ".git")
	if err := os.MkdirAll(sourceGit, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(base, "splice-worktree-"+repoKey(root), "reuse-me")
	if err := os.MkdirAll(filepath.Join(existing, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{Stdout: sourceGit + "\n"},
			{Stdout: sourceGit + "\n"},
		},
	}

	result, err := Prepare(context.Background(), Options{
		Cwd:     root,
		Name:    "reuse-me",
		BaseDir: base,
		RunGit:  runner.Run,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	if !result.Reused {
		t.Fatalf("Reused = false, want true")
	}
	if result.Path != existing {
		t.Fatalf("Path = %q, want existing %q", result.Path, existing)
	}
	if len(runner.calls) != 5 {
		t.Fatalf("expected metadata git calls only, got %#v", runner.calls)
	}
}

func TestPrepareRejectsExistingWorktreeFromDifferentRepo(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	sourceGit := filepath.Join(root, ".git")
	otherGit := filepath.Join(t.TempDir(), ".git")
	for _, dir := range []string{sourceGit, otherGit} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	existing := filepath.Join(base, "splice-worktree-"+repoKey(root), "other-repo")
	if err := os.MkdirAll(filepath.Join(existing, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{Stdout: sourceGit + "\n"},
			{Stdout: otherGit + "\n"},
		},
	}

	_, err := Prepare(context.Background(), Options{
		Cwd:     root,
		Name:    "other-repo",
		BaseDir: base,
		RunGit:  runner.Run,
	})
	if err == nil || !strings.Contains(err.Error(), "different git repository") {
		t.Fatalf("expected different repository reuse error, got %v", err)
	}
}

func TestPrepareValidatesNameAndExistingDirectory(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
		},
	}

	if _, err := Prepare(context.Background(), Options{Cwd: root, Name: "../escape", BaseDir: base, RunGit: runner.Run}); err == nil || !strings.Contains(err.Error(), "worktree name") {
		t.Fatalf("expected invalid name error, got %v", err)
	}

	blocked := filepath.Join(base, "splice-worktree-"+repoKey(root), "blocked")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "file.txt"), []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), Options{Cwd: root, Name: "blocked", BaseDir: base, RunGit: runner.Run}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected non-empty directory error, got %v", err)
	}
}

func TestDefaultBaseDirUsesStateHome(t *testing.T) {
	home := t.TempDir()
	stateHome := filepath.Join(home, "state")
	got, err := DefaultBaseDir(map[string]string{
		"HOME":           home,
		"XDG_STATE_HOME": stateHome,
	})
	if err != nil {
		t.Fatalf("DefaultBaseDir returned error: %v", err)
	}
	if got != filepath.Join(stateHome, "splice", "worktrees") {
		t.Fatalf("DefaultBaseDir = %q", got)
	}
}

func TestDefaultBaseDirFallsBackForWindowsUserProfile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("USERPROFILE fallback is Windows-specific")
	}
	profile := `C:\Users\splice`
	got, err := DefaultBaseDir(map[string]string{"USERPROFILE": profile})
	if err != nil {
		t.Fatalf("DefaultBaseDir returned error: %v", err)
	}
	expected := filepath.Join(profile, "AppData", "Local", "splice", "worktrees")
	if filepath.Clean(got) != filepath.Clean(expected) {
		t.Fatalf("DefaultBaseDir = %q, want %q", got, expected)
	}
}

func TestMergeBackMergesCleanWorktreeIntoSource(t *testing.T) {
	root, worktree := newMergeBackFixture(t)
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("new feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeBack(context.Background(), MergeBackOptions{
		RepoRoot:     root,
		WorktreePath: worktree,
		Name:         "task-merge",
	})
	if err != nil {
		t.Fatalf("MergeBack returned error: %v", err)
	}
	if result.Status != MergeBackMerged {
		t.Fatalf("Status = %q, want merged (%s)", result.Status, result.Message)
	}
	if result.Branch != "splice/task-merge" || result.CommitSHA == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "feature.txt")); err != nil {
		t.Fatalf("merged file missing from source tree: %v", err)
	}
	log := mustGit(t, root, "log", "--oneline", "-1")
	if !strings.Contains(log, "splice: merge worktree task-merge") {
		t.Fatalf("expected merge commit at source HEAD, got %q", log)
	}
	// The recovery branch survives the merge.
	if branch := mustGit(t, root, "branch", "--list", "splice/task-merge"); strings.TrimSpace(branch) == "" {
		t.Fatal("expected splice/task-merge branch to survive")
	}
}

func TestMergeBackNoChanges(t *testing.T) {
	root, worktree := newMergeBackFixture(t)

	result, err := MergeBack(context.Background(), MergeBackOptions{
		RepoRoot:     root,
		WorktreePath: worktree,
		Name:         "task-idle",
	})
	if err != nil {
		t.Fatalf("MergeBack returned error: %v", err)
	}
	if result.Status != MergeBackNoChanges {
		t.Fatalf("Status = %q, want no_changes (%s)", result.Status, result.Message)
	}
}

func TestMergeBackNoChangesWhenSourceAdvanced(t *testing.T) {
	root, worktree := newMergeBackFixture(t)
	// The user commits in the source repo while the run is in flight; the
	// worktree still has nothing of its own to merge.
	if err := os.WriteFile(filepath.Join(root, "user.txt"), []byte("user work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-m", "user commit")

	result, err := MergeBack(context.Background(), MergeBackOptions{
		RepoRoot:     root,
		WorktreePath: worktree,
		Name:         "task-idle2",
	})
	if err != nil {
		t.Fatalf("MergeBack returned error: %v", err)
	}
	if result.Status != MergeBackNoChanges {
		t.Fatalf("Status = %q, want no_changes (%s)", result.Status, result.Message)
	}
}

func TestMergeBackSkipsDirtySourceTree(t *testing.T) {
	root, worktree := newMergeBackFixture(t)
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("new feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := MergeBack(context.Background(), MergeBackOptions{
		RepoRoot:     root,
		WorktreePath: worktree,
		Name:         "task-dirty",
	})
	if err != nil {
		t.Fatalf("MergeBack returned error: %v", err)
	}
	if result.Status != MergeBackSkippedDirty {
		t.Fatalf("Status = %q, want skipped_dirty (%s)", result.Status, result.Message)
	}
	if result.Branch != "splice/task-dirty" {
		t.Fatalf("Branch = %q, want surviving branch name", result.Branch)
	}
	if _, err := os.Stat(filepath.Join(root, "feature.txt")); err == nil {
		t.Fatal("dirty-tree merge must not modify the source tree")
	}
	// The branch exists so the user can merge manually.
	if branch := mustGit(t, root, "branch", "--list", "splice/task-dirty"); strings.TrimSpace(branch) == "" {
		t.Fatal("expected splice/task-dirty branch for manual merge")
	}
}

func TestMergeBackConflictAbortsAndKeepsBranch(t *testing.T) {
	root, worktree := newMergeBackFixture(t)
	// Both sides edit the same line of the same file.
	if err := os.WriteFile(filepath.Join(worktree, "shared.txt"), []byte("worktree version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shared.txt"), []byte("source version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-m", "source edit")

	result, err := MergeBack(context.Background(), MergeBackOptions{
		RepoRoot:     root,
		WorktreePath: worktree,
		Name:         "task-conflict",
	})
	if err != nil {
		t.Fatalf("MergeBack returned error: %v", err)
	}
	if result.Status != MergeBackConflict {
		t.Fatalf("Status = %q, want conflict (%s)", result.Status, result.Message)
	}
	if len(result.ConflictFiles) != 1 || result.ConflictFiles[0] != "shared.txt" {
		t.Fatalf("ConflictFiles = %v, want [shared.txt]", result.ConflictFiles)
	}
	// The merge was aborted: the source tree is clean and unchanged.
	if status := mustGit(t, root, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("source tree not clean after abort: %q", status)
	}
	if data, err := os.ReadFile(filepath.Join(root, "shared.txt")); err != nil || string(data) != "source version\n" {
		t.Fatalf("source file modified after abort: %q, %v", data, err)
	}
	if branch := mustGit(t, root, "branch", "--list", "splice/task-conflict"); strings.TrimSpace(branch) == "" {
		t.Fatal("expected splice/task-conflict branch for manual resolution")
	}
}

func TestMergeBackRejectsInvalidName(t *testing.T) {
	if _, err := MergeBack(context.Background(), MergeBackOptions{Name: "../escape"}); err == nil || !strings.Contains(err.Error(), "worktree name") {
		t.Fatalf("expected invalid name error, got %v", err)
	}
}

// newMergeBackFixture creates a real git repo with one commit, a shared file,
// and a detached worktree, both with committer identity configured.
func newMergeBackFixture(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	mustGit(t, root, "init", "-b", "main")
	mustGit(t, root, "config", "user.name", "splice-test")
	mustGit(t, root, "config", "user.email", "splice-test@local")
	if err := os.WriteFile(filepath.Join(root, "shared.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-m", "base commit")

	worktree := filepath.Join(t.TempDir(), "wt")
	mustGit(t, root, "worktree", "add", "--detach", worktree, "HEAD")
	mustGit(t, worktree, "config", "user.name", "splice-test")
	mustGit(t, worktree, "config", "user.email", "splice-test@local")
	return root, worktree
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	result, err := defaultRunGit(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("git %s exited %d: %s%s", strings.Join(args, " "), result.ExitCode, result.Stdout, result.Stderr)
	}
	return result.Stdout
}

type fakeRunner struct {
	calls   []gitCall
	results []CommandResult
}

func (runner *fakeRunner) Run(ctx context.Context, dir string, args ...string) (CommandResult, error) {
	runner.calls = append(runner.calls, gitCall{dir: dir, args: append([]string{}, args...)})
	if len(runner.results) == 0 {
		return CommandResult{}, nil
	}
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result, nil
}

func (runner *fakeRunner) commandLine(index int) string {
	if index >= len(runner.calls) {
		return ""
	}
	return "git " + strings.Join(runner.calls[index].args, " ")
}

type gitCall struct {
	dir  string
	args []string
}

func fixedTime(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}

// newRecoveryFixture creates a real git repo with a tracked file, executable
// script, and .gitignore, plus a detached worktree for recovery tests.
func newRecoveryFixture(t *testing.T) (root, worktree string, result Result) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root = t.TempDir()
	mustGit(t, root, "init", "-b", "main")
	mustGit(t, root, "config", "user.name", "splice-test")
	mustGit(t, root, "config", "user.email", "splice-test@local")

	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("tracked base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "exec.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-m", "base commit")

	worktree = filepath.Join(t.TempDir(), "wt")
	mustGit(t, root, "worktree", "add", "--detach", worktree, "HEAD")
	mustGit(t, worktree, "config", "user.name", "splice-test")
	mustGit(t, worktree, "config", "user.email", "splice-test@local")

	result = Result{
		Name:     "task-recovery",
		Path:     worktree,
		RepoRoot: root,
	}
	return root, worktree, result
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestRecoveryRefNameHashesLongRunID(t *testing.T) {
	runID := strings.Repeat("session-segment-", 12)
	ref, err := recoveryRefName("task-recovery", runID, 3)
	if err != nil {
		t.Fatalf("recoveryRefName: %v", err)
	}
	if strings.Contains(ref, runID) || len(ref) > 120 {
		t.Fatalf("ref = %q, want bounded ref without raw run ID", ref)
	}
	if !strings.HasSuffix(ref, "/3") {
		t.Fatalf("ref = %q, want iteration suffix", ref)
	}
}

func TestRecoveryCaptureDoesNotMutateWorkspace(t *testing.T) {
	_, worktree, result := newRecoveryFixture(t)

	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headBefore := mustGit(t, worktree, "rev-parse", "HEAD")
	indexPath := strings.TrimSpace(mustGit(t, worktree, "rev-parse", "--git-path", "index"))
	indexBefore := fileSHA256(t, indexPath)

	rec := NewIterationRecovery(result)
	ref, err := rec.Capture(context.Background(), "run-123", 0)
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}

	headAfter := mustGit(t, worktree, "rev-parse", "HEAD")
	indexAfter := fileSHA256(t, indexPath)
	if headBefore != headAfter {
		t.Fatalf("HEAD moved: %s -> %s", headBefore, headAfter)
	}
	if indexBefore != indexAfter {
		t.Fatalf("real index changed")
	}
	if got := readFileString(t, filepath.Join(worktree, "tracked.txt")); got != "modified\n" {
		t.Fatalf("workspace file changed: %q", got)
	}

	tree := strings.TrimSpace(mustGit(t, worktree, "rev-parse", ref+"^{tree}"))
	got := mustGit(t, worktree, "cat-file", "-p", tree+":tracked.txt")
	if got != "modified\n" {
		t.Fatalf("snapshot tracked.txt = %q, want modified", got)
	}
}

func TestRecoveryCaptureIncludesUntrackedFiles(t *testing.T) {
	_, worktree, result := newRecoveryFixture(t)
	if err := os.WriteFile(filepath.Join(worktree, "untracked.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := NewIterationRecovery(result)
	ref, err := rec.Capture(context.Background(), "run-123", 0)
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}

	tree := strings.TrimSpace(mustGit(t, worktree, "rev-parse", ref+"^{tree}"))
	got := mustGit(t, worktree, "cat-file", "-p", tree+":untracked.txt")
	if got != "untracked\n" {
		t.Fatalf("snapshot untracked.txt = %q, want untracked", got)
	}
}

func TestRecoveryRestoreResetsWorkspace(t *testing.T) {
	_, worktree, result := newRecoveryFixture(t)

	rec := NewIterationRecovery(result)

	// Baseline untracked file is captured with iteration 0.
	if err := os.WriteFile(filepath.Join(worktree, "baseline-untracked.txt"), []byte("baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseRef, err := rec.Capture(context.Background(), "run-123", 0)
	if err != nil {
		t.Fatalf("Capture baseline returned error: %v", err)
	}

	// Make a collection of changes.
	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(worktree, "exec.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(worktree, "tracked.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "later-untracked.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "ignored.txt"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	currentRef, err := rec.Capture(context.Background(), "run-123", 1)
	if err != nil {
		t.Fatalf("Capture current returned error: %v", err)
	}

	if err := rec.Restore(context.Background(), currentRef, baseRef); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}

	if got := readFileString(t, filepath.Join(worktree, "tracked.txt")); got != "tracked base\n" {
		t.Fatalf("tracked.txt = %q, want tracked base", got)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(worktree, "exec.sh"))
		if err != nil {
			t.Fatalf("stat exec.sh: %v", err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("exec.sh is not executable after restore")
		}
	}

	if got := readFileString(t, filepath.Join(worktree, "baseline-untracked.txt")); got != "baseline\n" {
		t.Fatalf("baseline-untracked.txt = %q, want baseline", got)
	}

	if _, err := os.Stat(filepath.Join(worktree, "later-untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("later-untracked.txt should have been removed: %v", err)
	}

	if got := readFileString(t, filepath.Join(worktree, "ignored.txt")); got != "ignored\n" {
		t.Fatalf("ignored.txt = %q, want ignored", got)
	}
}

func TestRecoveryRestoreRefusesMismatch(t *testing.T) {
	_, worktree, result := newRecoveryFixture(t)

	rec := NewIterationRecovery(result)
	baseRef, err := rec.Capture(context.Background(), "run-123", 0)
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = rec.Restore(context.Background(), baseRef, baseRef)
	if err == nil || !strings.Contains(err.Error(), "workspace tree mismatch") {
		t.Fatalf("expected mismatch error, got %v", err)
	}

	if got := readFileString(t, filepath.Join(worktree, "tracked.txt")); got != "changed\n" {
		t.Fatalf("workspace was mutated despite mismatch: %q", got)
	}
}

func TestRecoveryCaptureCancellation(t *testing.T) {
	_, _, result := newRecoveryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := NewIterationRecovery(result)
	_, err := rec.Capture(ctx, "run-123", 0)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRecoveryRestoreCancellation(t *testing.T) {
	_, _, result := newRecoveryFixture(t)
	rec := NewIterationRecovery(result)
	baseRef, err := rec.Capture(context.Background(), "run-123", 0)
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = rec.Restore(ctx, baseRef, baseRef)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRecoveryCapturePropagatesGitFailure(t *testing.T) {
	_, _, result := newRecoveryFixture(t)
	fakeEnvGit := func(ctx context.Context, dir string, env map[string]string, args ...string) (CommandResult, error) {
		return CommandResult{ExitCode: 1, Stderr: "synthetic git failure"}, nil
	}
	rec := newIterationRecovery(result, nil, fakeEnvGit)
	_, err := rec.Capture(context.Background(), "run-123", 0)
	if err == nil || !strings.Contains(err.Error(), "synthetic git failure") {
		t.Fatalf("expected synthetic git failure, got %v", err)
	}
}

func TestRecoveryRestorePropagatesGitFailure(t *testing.T) {
	_, _, result := newRecoveryFixture(t)
	rec := NewIterationRecovery(result)
	baseRef, err := rec.Capture(context.Background(), "run-123", 0)
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}

	fakeRunGit := func(ctx context.Context, dir string, args ...string) (CommandResult, error) {
		if len(args) > 0 && args[0] == "reset" {
			return CommandResult{ExitCode: 1, Stderr: "synthetic reset failure"}, nil
		}
		return defaultRunGit(ctx, dir, args...)
	}
	rec = newIterationRecovery(result, fakeRunGit, nil)
	err = rec.Restore(context.Background(), baseRef, baseRef)
	if err == nil || !strings.Contains(err.Error(), "synthetic reset failure") {
		t.Fatalf("expected synthetic reset failure, got %v", err)
	}
}

func TestRecoveryMergeBackAfterRestore(t *testing.T) {
	root, worktree, result := newRecoveryFixture(t)
	rec := NewIterationRecovery(result)

	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("iter0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	iter0Ref, err := rec.Capture(context.Background(), "run-123", 0)
	if err != nil {
		t.Fatalf("Capture iter0 returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("iter1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	iter1Ref, err := rec.Capture(context.Background(), "run-123", 1)
	if err != nil {
		t.Fatalf("Capture iter1 returned error: %v", err)
	}

	if err := rec.Restore(context.Background(), iter1Ref, iter0Ref); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}

	mergeResult, err := MergeBack(context.Background(), MergeBackOptions{
		RepoRoot:     root,
		WorktreePath: worktree,
		Name:         result.Name,
	})
	if err != nil {
		t.Fatalf("MergeBack returned error: %v", err)
	}
	if mergeResult.Status != MergeBackMerged {
		t.Fatalf("MergeBack status = %q, want merged (%s)", mergeResult.Status, mergeResult.Message)
	}

	if got := readFileString(t, filepath.Join(root, "tracked.txt")); got != "iter0\n" {
		t.Fatalf("merged tracked.txt = %q, want iter0", got)
	}

	if ref := mustGit(t, worktree, "rev-parse", iter0Ref); ref == "" {
		t.Fatal("iter0 recovery ref missing after merge")
	}
	if ref := mustGit(t, worktree, "rev-parse", iter1Ref); ref == "" {
		t.Fatal("iter1 recovery ref missing after merge")
	}
}
