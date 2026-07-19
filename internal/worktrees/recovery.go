package worktrees

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	indexFile, cleanup, err := tempIndexFile()
	if err != nil {
		return "", err
	}
	defer cleanup()

	env := gitIndexEnv(indexFile)
	if _, err := r.gitOutputEnv(ctx, dir, env, "read-tree", "HEAD"); err != nil {
		return "", fmt.Errorf("seed recovery index: %w", err)
	}
	if _, err := r.gitOutputEnv(ctx, dir, env, "add", "-A"); err != nil {
		return "", fmt.Errorf("stage recovery snapshot: %w", err)
	}
	tree, err := r.gitOutputEnv(ctx, dir, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write recovery tree: %w", err)
	}

	head, err := r.gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}

	commitEnv := map[string]string{
		"GIT_AUTHOR_NAME":     "Splice Recovery",
		"GIT_AUTHOR_EMAIL":    "recovery@local",
		"GIT_COMMITTER_NAME":  "Splice Recovery",
		"GIT_COMMITTER_EMAIL": "recovery@local",
		"GIT_INDEX_FILE":      indexFile,
	}
	message := fmt.Sprintf("splice recovery snapshot %s/%s/%d", r.result.Name, runID, iteration)
	commitSHA, err := r.gitOutputEnv(ctx, dir, commitEnv, "commit-tree", tree, "-p", head, "-m", message)
	if err != nil {
		return "", fmt.Errorf("create recovery commit: %w", err)
	}

	if _, err := r.gitOutput(ctx, dir, "update-ref", ref, commitSHA); err != nil {
		return "", fmt.Errorf("pin recovery ref: %w", err)
	}
	return ref, nil
}

// Restore resets the worktree to targetRef after verifying the current state
// matches expectedCurrentRef. It removes later non-ignored untracked files with
// git clean -fd and verifies the resulting tree matches targetRef.
//
// All git commands run with the supplied context. A mismatch, cancellation, or
// command failure returns a named error and leaves the isolated worktree for
// inspection. A failure after reset may leave the target partially restored.
func (r *IterationRecovery) Restore(ctx context.Context, expectedCurrentRef, targetRef string) error {
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

	if result, err := r.runGit(ctx, dir, "clean", "-fd"); err != nil {
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
