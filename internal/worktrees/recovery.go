package worktrees

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/gitenv"
)

// IterationRecovery captures and restores the git-visible state of an isolated
// Splice worktree. It is constructed from a prepared worktrees.Result and only
// mutates the isolated worktree described by that result.
type IterationRecovery struct {
	result Result
	runGit GitRunner
	envGit envGitRunner
}

// envGitRunner is like GitRunner but allows per-command environment overrides.
// It is used for operations that need a temporary GIT_INDEX_FILE or explicit
// author/committer identities.
type envGitRunner func(ctx context.Context, dir string, env map[string]string, args ...string) (CommandResult, error)

// NewIterationRecovery returns a recovery implementation bound to the prepared
// worktree described by result.
func NewIterationRecovery(result Result) *IterationRecovery {
	return newIterationRecovery(result, nil, nil)
}

func newIterationRecovery(result Result, runGit GitRunner, envGit envGitRunner) *IterationRecovery {
	if runGit == nil {
		runGit = defaultRunGit
	}
	if envGit == nil {
		envGit = defaultEnvRunGit
	}
	return &IterationRecovery{
		result: result,
		runGit: runGit,
		envGit: envGit,
	}
}

// Capture snapshots the current git-visible worktree state under a hidden ref.
// It returns the hidden ref name, which is opaque to callers.
//
// Capture seeds a temporary GIT_INDEX_FILE from HEAD, stages the workspace with
// git add -A, writes a tree, builds a plumbing commit with a Splice-local
// identity, and pins it under refs/splice/recovery/<name>/<runKey>/<iteration>.
// It does not move HEAD, modify the real index, or change workspace files.
func (r *IterationRecovery) Capture(ctx context.Context, runID string, iteration int) (string, error) {
	ref, err := recoveryRefName(r.result.Name, runID, iteration)
	if err != nil {
		return "", err
	}

	dir := r.result.Path
	message := fmt.Sprintf("splice recovery snapshot %s/%s/%d", r.result.Name, runID, iteration)
	commitSHA, err := snapshotCommit(ctx, r.runGit, r.envGit, dir, "recovery", "Splice Recovery", message)
	if err != nil {
		return "", err
	}

	if _, err := r.gitOutput(ctx, dir, "update-ref", ref, commitSHA); err != nil {
		return "", fmt.Errorf("pin recovery ref: %w", err)
	}
	return ref, nil
}

// Restore resets the worktree to targetRef after verifying the current state
// matches expectedCurrentRef. It removes later non-ignored untracked files,
// nested repositories included, with git clean -ffd and verifies the resulting tree matches targetRef. On success
// HEAD moves to the parent of targetRef, never onto the snapshot commit.
//
// All git commands run with the supplied context. A mismatch, cancellation, or
// command failure returns a named error and leaves the isolated worktree for
// inspection. A failure after reset may leave the target partially restored.
func (r *IterationRecovery) Restore(ctx context.Context, expectedCurrentRef, targetRef string) (err error) {
	if strings.TrimSpace(expectedCurrentRef) == "" {
		return fmt.Errorf("expectedCurrentRef is required")
	}
	if strings.TrimSpace(targetRef) == "" {
		return fmt.Errorf("targetRef is required")
	}

	dir := r.result.Path

	expectedTree, err := r.gitOutput(ctx, dir, "rev-parse", expectedCurrentRef+"^{tree}")
	if err != nil {
		return fmt.Errorf("resolve expected current tree: %w", err)
	}
	currentTree, err := r.computeTree(ctx, dir)
	if err != nil {
		return fmt.Errorf("compute current tree: %w", err)
	}
	if currentTree != expectedTree {
		return fmt.Errorf("workspace tree mismatch: current %s, expected %s", currentTree, expectedTree)
	}

	targetTree, err := r.gitOutput(ctx, dir, "rev-parse", targetRef+"^{tree}")
	if err != nil {
		return fmt.Errorf("resolve target tree: %w", err)
	}

	if result, err := r.runGit(ctx, dir, "reset", "--hard", targetRef); err != nil {
		return fmt.Errorf("reset to target: %w", err)
	} else if result.ExitCode != 0 {
		return fmt.Errorf("reset to target: %s", strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout)))
	}
	// HEAD now sits on the snapshot. Any later error moves it back, so a
	// MergeBack after a failed restore cannot merge the snapshot.
	defer func() {
		if err != nil {
			_ = r.moveHeadToParent(context.WithoutCancel(ctx), dir, targetRef)
		}
	}()

	// Two -f flags also remove untracked directories that hold a .git.
	if result, err := r.runGit(ctx, dir, "clean", "-ffd"); err != nil {
		return fmt.Errorf("clean untracked files: %w", err)
	} else if result.ExitCode != 0 {
		return fmt.Errorf("clean untracked files: %s", strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout)))
	}

	verifyTree, err := r.computeTree(ctx, dir)
	if err != nil {
		return fmt.Errorf("verify restored tree: %w", err)
	}
	if verifyTree != targetTree {
		return fmt.Errorf("restored tree mismatch: got %s, want %s", verifyTree, targetTree)
	}

	return r.moveHeadToParent(ctx, dir, targetRef)
}

// moveHeadToParent moves HEAD off the synthetic snapshot commit, so a later
// MergeBack never merges it. The index is reset to the parent too, so the
// restored files are not left staged.
func (r *IterationRecovery) moveHeadToParent(ctx context.Context, dir, targetRef string) error {
	if result, err := r.runGit(ctx, dir, "reset", "--mixed", targetRef+"^"); err != nil {
		return fmt.Errorf("reset HEAD to snapshot parent: %w", err)
	} else if result.ExitCode != 0 {
		return fmt.Errorf("reset HEAD to snapshot parent: %s", strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout)))
	}
	return nil
}

// computeTree builds a temporary index seeded from HEAD, adds the current
// workspace with git add -A, and returns the resulting tree SHA.
func (r *IterationRecovery) computeTree(ctx context.Context, dir string) (string, error) {
	indexFile, cleanup, err := tempIndexFile()
	if err != nil {
		return "", err
	}
	defer cleanup()

	env := gitIndexEnv(indexFile)
	if _, err := r.gitOutputEnv(ctx, dir, env, "read-tree", "HEAD"); err != nil {
		return "", fmt.Errorf("seed index: %w", err)
	}
	if _, err := r.gitOutputEnv(ctx, dir, env, "add", "-A"); err != nil {
		return "", fmt.Errorf("stage workspace: %w", err)
	}
	tree, err := r.gitOutputEnv(ctx, dir, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write tree: %w", err)
	}
	return tree, nil
}

func (r *IterationRecovery) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return commandOutput(r.runGit(ctx, dir, args...))
}

func (r *IterationRecovery) gitOutputEnv(ctx context.Context, dir string, env map[string]string, args ...string) (string, error) {
	return commandOutput(r.envGit(ctx, dir, env, args...))
}

func recoveryRefName(name, runID string, iteration int) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	if strings.TrimSpace(runID) == "" {
		return "", fmt.Errorf("run ID is required")
	}
	if iteration < 0 {
		return "", fmt.Errorf("iteration must be non-negative: %d", iteration)
	}
	sum := sha256.Sum256([]byte(runID))
	runKey := hex.EncodeToString(sum[:8])
	return fmt.Sprintf("refs/splice/recovery/%s/%s/%d", name, runKey, iteration), nil
}

// SnapshotCommit records the git-visible worktree of dir (tracked and
// untracked files, ignored files excluded) as a commit whose parent is HEAD.
// It does not move HEAD, modify the real index, or change workspace files.
// When the worktree matches HEAD, it returns HEAD and creates nothing: HEAD
// already names those bytes. A new commit is pinned under
// refs/splice/snapshots/ so git gc keeps it. It runs on the host, like
// Capture: the stage sandbox refuses the object writes that
// `git stash create` needs.
func SnapshotCommit(ctx context.Context, dir, message string) (string, error) {
	tree, err := snapshotTree(ctx, defaultEnvRunGit, dir, "snapshot")
	if err != nil {
		return "", err
	}
	head, err := commandOutput(defaultRunGit(ctx, dir, "rev-parse", "HEAD"))
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	headTree, err := commandOutput(defaultRunGit(ctx, dir, "rev-parse", "HEAD^{tree}"))
	if err != nil {
		return "", fmt.Errorf("resolve HEAD tree: %w", err)
	}
	if tree == headTree {
		return head, nil
	}
	commitSHA, err := commitTree(ctx, defaultEnvRunGit, dir, tree, head, "snapshot", "Splice Snapshot", message)
	if err != nil {
		return "", err
	}
	if _, err := commandOutput(defaultRunGit(ctx, dir, "update-ref", "refs/splice/snapshots/"+commitSHA, commitSHA)); err != nil {
		return "", fmt.Errorf("pin snapshot ref: %w", err)
	}
	return commitSHA, nil
}

// ReleaseSnapshot deletes the pin that SnapshotCommit made for commit. It
// leaves the commit object alone, and it does nothing when the pin is absent.
func ReleaseSnapshot(ctx context.Context, dir, commit string) error {
	_, err := commandOutput(defaultRunGit(ctx, dir, "update-ref", "-d", "refs/splice/snapshots/"+commit, commit))
	return err
}

// SnapshotTree writes the worktree's tree (tracked and untracked files, the
// same view as SnapshotCommit) and returns its SHA. It creates no commit and
// no ref: the tree only needs to live for one run, as the base of a diff.
func SnapshotTree(ctx context.Context, dir string) (string, error) {
	return snapshotTree(ctx, defaultEnvRunGit, dir, "snapshot")
}

// CheckoutHome is the managed directory that holds the baseline checkouts of
// the repository whose top-level path is top. Each repository has its own.
func CheckoutHome(top string) string {
	sum := sha256.Sum256([]byte(top))
	return filepath.Join(os.TempDir(), "splice-baseline", hex.EncodeToString(sum[:6]))
}

// CheckoutAt checks commit out in a new detached worktree of the repository
// that holds dir. It returns the path inside that checkout that matches dir.
// The cleanup function removes the worktree and its directory. The caller
// runs it when done, also after an error or a cancelled context. It runs on
// the host, like SnapshotCommit.
func CheckoutAt(ctx context.Context, dir, commit string) (string, func(), error) {
	top, err := repoTop(ctx, dir)
	if err != nil {
		return "", nil, err
	}
	absDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve directory %q: %w", dir, err)
	}
	rel, err := filepath.Rel(top, absDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("directory %q is outside repository %q", dir, top)
	}
	home := CheckoutHome(top)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", nil, fmt.Errorf("create checkout home: %w", err)
	}
	checkout, err := os.MkdirTemp(home, "checkout-")
	if err != nil {
		return "", nil, fmt.Errorf("create checkout dir: %w", err)
	}
	cleanup := func() { removeCheckout(top, checkout) }
	if _, err := commandOutput(defaultRunGit(ctx, top, "worktree", "add", "--detach", checkout, commit)); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("check out %s: %w", commit, err)
	}
	return filepath.Join(checkout, rel), cleanup, nil
}

// PruneStaleCheckouts removes the checkouts of the repository that holds dir
// when their directory is older than maxAge. A run that crashed before its
// cleanup leaves such a checkout. It returns the number removed.
func PruneStaleCheckouts(ctx context.Context, dir string, maxAge time.Duration) (int, error) {
	top, err := repoTop(ctx, dir)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(CheckoutHome(top))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read checkout home: %w", err)
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		path := filepath.Join(CheckoutHome(top), entry.Name())
		info, err := entry.Info()
		if err != nil || !info.IsDir() || info.ModTime().After(cutoff) {
			continue
		}
		removeCheckout(top, path)
		removed++
	}
	return removed, nil
}

// repoTop returns the top-level path of the repository that holds dir.
func repoTop(ctx context.Context, dir string) (string, error) {
	top, err := commandOutput(defaultRunGit(ctx, dir, "rev-parse", "--show-toplevel"))
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	return top, nil
}

// removeCheckout removes one checkout directory and its worktree
// registration. Prune runs only when git could not remove the worktree and
// its registration is still there. A normal removal leaves other worktrees
// alone.
func removeCheckout(top, checkout string) {
	cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A second --force removes a worktree that git still holds locked.
	if _, err := commandOutput(defaultRunGit(cleanCtx, top, "worktree", "remove", "--force", "--force", checkout)); err == nil {
		_ = os.RemoveAll(checkout)
		return
	}
	registered := registeredWorktree(cleanCtx, top, checkout)
	_ = os.RemoveAll(checkout)
	if !registered {
		return
	}
	// A checkout cancelled during "worktree add" keeps a lock and its
	// registration. Unlock and prune drop it once the directory is gone.
	_, _ = defaultRunGit(cleanCtx, top, "worktree", "unlock", checkout)
	_, _ = defaultRunGit(cleanCtx, top, "worktree", "prune")
}

// registeredWorktree reports whether git lists a worktree at checkout. Git
// may print the real path, so the match is on the unique directory name.
func registeredWorktree(ctx context.Context, top, checkout string) bool {
	out, err := commandOutput(defaultRunGit(ctx, top, "worktree", "list", "--porcelain"))
	if err != nil {
		return false
	}
	base := filepath.Base(checkout)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") && strings.HasSuffix(line, string(filepath.Separator)+base) {
			return true
		}
	}
	return false
}

// snapshotCommit is the shared plumbing for Capture: the worktree tree
// (snapshotTree) committed with HEAD as its parent. label names the caller
// in error messages.
func snapshotCommit(ctx context.Context, runGit GitRunner, envGit envGitRunner, dir, label, author, message string) (string, error) {
	tree, err := snapshotTree(ctx, envGit, dir, label)
	if err != nil {
		return "", err
	}
	head, err := commandOutput(runGit(ctx, dir, "rev-parse", "HEAD"))
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	return commitTree(ctx, envGit, dir, tree, head, label, author, message)
}

// snapshotTree writes the git-visible worktree as a tree object through a
// temporary index seeded from HEAD, so the real index never changes.
func snapshotTree(ctx context.Context, envGit envGitRunner, dir, label string) (string, error) {
	indexFile, cleanup, err := tempIndexFile()
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := gitIndexEnv(indexFile)
	if _, err := commandOutput(envGit(ctx, dir, env, "read-tree", "HEAD")); err != nil {
		return "", fmt.Errorf("seed %s index: %w", label, err)
	}
	if _, err := commandOutput(envGit(ctx, dir, env, "add", "-A")); err != nil {
		return "", fmt.Errorf("stage %s snapshot: %w", label, err)
	}
	tree, err := commandOutput(envGit(ctx, dir, env, "write-tree"))
	if err != nil {
		return "", fmt.Errorf("write %s tree: %w", label, err)
	}
	return tree, nil
}

// commitTree creates a commit for tree with parent head and a Splice-local
// identity.
func commitTree(ctx context.Context, envGit envGitRunner, dir, tree, head, label, author, message string) (string, error) {
	email := strings.ToLower(label) + "@local"
	commitEnv := map[string]string{
		"GIT_AUTHOR_NAME":     author,
		"GIT_AUTHOR_EMAIL":    email,
		"GIT_COMMITTER_NAME":  author,
		"GIT_COMMITTER_EMAIL": email,
	}
	commitSHA, err := commandOutput(envGit(ctx, dir, commitEnv, "commit-tree", tree, "-p", head, "-m", message))
	if err != nil {
		return "", fmt.Errorf("create %s commit: %w", label, err)
	}
	return commitSHA, nil
}

func tempIndexFile() (string, func(), error) {
	file, err := os.CreateTemp("", "splice-recovery-index-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp index: %w", err)
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", nil, fmt.Errorf("close temp index: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(name)
	}
	return name, cleanup, nil
}

func gitIndexEnv(indexFile string) map[string]string {
	return map[string]string{"GIT_INDEX_FILE": indexFile}
}

// commandOutput converts a git command result into trimmed stdout, mapping a
// non-zero exit code to an error preferring stderr over stdout.
func commandOutput(result CommandResult, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout))
		if message == "" {
			message = fmt.Sprintf("git exited with code %d", result.ExitCode)
		}
		return "", fmt.Errorf("%s", message)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// Capture stdout and stderr separately: callers parse Stdout for values
// (rev-parse output) and prefer Stderr for error messages. CombinedOutput
// merged the two, letting git's stderr warnings pollute parsed output and
// leaving CommandResult.Stderr always empty.
func defaultEnvRunGit(ctx context.Context, dir string, env map[string]string, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = mergeEnv(os.Environ(), env)
	if err := gitenv.Harden(command); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
			err = nil
		}
	}
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, err
}

func mergeEnv(base []string, overrides map[string]string) []string {
	merged := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		if index := strings.IndexByte(entry, '='); index >= 0 {
			merged[entry[:index]] = entry[index+1:]
		} else {
			merged[entry] = ""
		}
	}
	for key, value := range overrides {
		merged[key] = value
	}
	result := make([]string, 0, len(merged))
	for key, value := range merged {
		result = append(result, key+"="+value)
	}
	return result
}
