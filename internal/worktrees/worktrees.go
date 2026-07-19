package worktrees

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type GitRunner func(context.Context, string, ...string) (CommandResult, error)

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type Options struct {
	Cwd     string
	Name    string
	BaseDir string
	Now     func() time.Time
	RunGit  GitRunner
}

type Result struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	RepoRoot     string `json:"repoRoot"`
	SourceBranch string `json:"sourceBranch,omitempty"`
	SourceCommit string `json:"sourceCommit,omitempty"`
	Reused       bool   `json:"reused"`
}

var worktreeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)

func Prepare(ctx context.Context, options Options) (Result, error) {
	cwd, err := resolveCwd(options.Cwd)
	if err != nil {
		return Result{}, err
	}
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	name := strings.TrimSpace(options.Name)
	if name == "" {
		name = defaultWorktreeName(now())
	}
	if err := validateName(name); err != nil {
		return Result{}, err
	}

	repoRoot, err := gitOutput(ctx, runGit, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Result{}, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot = filepath.Clean(repoRoot)
	branch, _ := gitOutput(ctx, runGit, repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	commit, _ := gitOutput(ctx, runGit, repoRoot, "rev-parse", "--short", "HEAD")

	baseDir := strings.TrimSpace(options.BaseDir)
	if baseDir == "" {
		baseDir, err = DefaultBaseDir(nil)
		if err != nil {
			return Result{}, err
		}
	}
	baseDir, err = filepath.Abs(baseDir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve worktree dir: %w", err)
	}

	repoDir := filepath.Join(baseDir, "splice-worktree-"+repoKey(repoRoot))
	target := filepath.Join(repoDir, name)
	result := Result{
		Name:         name,
		Path:         target,
		RepoRoot:     repoRoot,
		SourceBranch: branch,
		SourceCommit: commit,
	}
	reused, err := inspectTarget(target)
	if err != nil {
		return Result{}, err
	}
	if reused {
		sameRepo, err := sameGitCommonDir(ctx, runGit, repoRoot, target)
		if err != nil {
			return Result{}, fmt.Errorf("inspect existing worktree repository: %w", err)
		}
		if !sameRepo {
			return Result{}, fmt.Errorf("worktree path already exists for a different git repository: %s", target)
		}
		result.Reused = true
		return result, nil
	}
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create worktree directory: %w", err)
	}
	commandResult, err := runGit(ctx, repoRoot, "worktree", "add", "--detach", target, "HEAD")
	if err != nil {
		return Result{}, fmt.Errorf("create git worktree: %w", err)
	}
	if commandResult.ExitCode != 0 {
		message := strings.TrimSpace(firstNonEmpty(commandResult.Stderr, commandResult.Stdout))
		if message == "" {
			message = fmt.Sprintf("git worktree add exited with code %d", commandResult.ExitCode)
		}
		return Result{}, fmt.Errorf("create git worktree: %s", message)
	}
	return result, nil
}

// MergeBackStatus classifies the outcome of a merge-back attempt.
type MergeBackStatus string

const (
	// MergeBackMerged means the worktree branch was merged into the source repo.
	MergeBackMerged MergeBackStatus = "merged"
	// MergeBackNoChanges means the worktree had nothing to commit or merge.
	MergeBackNoChanges MergeBackStatus = "no_changes"
	// MergeBackSkippedDirty means the source working tree had uncommitted
	// changes, so the merge was not attempted.
	MergeBackSkippedDirty MergeBackStatus = "skipped_dirty"
	// MergeBackConflict means the merge produced conflicts and was aborted.
	MergeBackConflict MergeBackStatus = "conflict"
)

// MergeBackOptions configures MergeBack.
type MergeBackOptions struct {
	// RepoRoot is the source repository working tree the merge lands in.
	RepoRoot string
	// WorktreePath is the isolated worktree containing the run's changes.
	WorktreePath string
	// Name is the worktree name; the recovery branch is splice/<name>.
	Name string
	// CommitMessage is used for the worktree commit; a default is derived
	// from Name when empty.
	CommitMessage string
	RunGit        GitRunner
}

// MergeBackResult reports what happened, including the surviving branch name
// so skipped or conflicted merges can be finished manually.
type MergeBackResult struct {
	Status        MergeBackStatus `json:"status"`
	Branch        string          `json:"branch,omitempty"`
	CommitSHA     string          `json:"commitSha,omitempty"`
	ConflictFiles []string        `json:"conflictFiles,omitempty"`
	Message       string          `json:"message"`
}

// MergeBack commits the worktree's changes on its detached HEAD, pins the
// branch splice/<name> to that commit, and merges it into the source repo
// with an explicit merge commit. The source working tree must be clean; a
// dirty tree or a conflicting merge is reported, never forced. The branch
// always survives so the user can merge manually.
func MergeBack(ctx context.Context, options MergeBackOptions) (MergeBackResult, error) {
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	if err := validateName(options.Name); err != nil {
		return MergeBackResult{}, err
	}
	branch := "splice/" + options.Name

	status, err := gitOutput(ctx, runGit, options.WorktreePath, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return MergeBackResult{}, fmt.Errorf("inspect worktree status: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		if _, err := gitOutput(ctx, runGit, options.WorktreePath, "add", "-A"); err != nil {
			return MergeBackResult{}, fmt.Errorf("stage worktree changes: %w", err)
		}
		message := strings.TrimSpace(options.CommitMessage)
		if message == "" {
			message = "splice: worktree " + options.Name
		}
		if _, err := gitOutput(ctx, runGit, options.WorktreePath, "commit", "-m", message); err != nil {
			return MergeBackResult{}, fmt.Errorf("commit worktree changes: %w", err)
		}
	}

	worktreeHead, err := gitOutput(ctx, runGit, options.WorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return MergeBackResult{}, fmt.Errorf("resolve worktree head: %w", err)
	}
	// The worktree has nothing to merge when its HEAD is already reachable from
	// the source HEAD. Plain SHA equality is not enough: the user may have made
	// unrelated commits in the source repo while the run was in flight.
	ancestorCheck, err := runGit(ctx, options.RepoRoot, "merge-base", "--is-ancestor", worktreeHead, "HEAD")
	if err != nil {
		return MergeBackResult{}, fmt.Errorf("check worktree ancestry: %w", err)
	}
	if ancestorCheck.ExitCode == 0 {
		return MergeBackResult{
			Status:  MergeBackNoChanges,
			Message: "worktree has no changes to merge",
		}, nil
	}

	// Pin the recovery branch to the worktree commit. -f moves it on reuse: the
	// branch is namespaced to this worktree and always means its latest state.
	if _, err := gitOutput(ctx, runGit, options.WorktreePath, "branch", "-f", branch, "HEAD"); err != nil {
		return MergeBackResult{}, fmt.Errorf("pin worktree branch: %w", err)
	}

	sourceStatus, err := gitOutput(ctx, runGit, options.RepoRoot, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return MergeBackResult{}, fmt.Errorf("inspect source status: %w", err)
	}
	if strings.TrimSpace(sourceStatus) != "" {
		return MergeBackResult{
			Status:  MergeBackSkippedDirty,
			Branch:  branch,
			Message: fmt.Sprintf("source working tree has uncommitted changes; merge manually: git merge --no-ff %s", branch),
		}, nil
	}

	mergeMessage := "splice: merge worktree " + options.Name
	mergeResult, err := runGit(ctx, options.RepoRoot, "merge", "--no-ff", "-m", mergeMessage, branch)
	if err != nil {
		return MergeBackResult{}, fmt.Errorf("merge worktree branch: %w", err)
	}
	if mergeResult.ExitCode != 0 {
		conflictFiles := []string{}
		if unmerged, err := gitOutput(ctx, runGit, options.RepoRoot, "diff", "--name-only", "--diff-filter=U"); err == nil && strings.TrimSpace(unmerged) != "" {
			conflictFiles = strings.Split(strings.TrimSpace(unmerged), "\n")
		}
		if _, err := gitOutput(ctx, runGit, options.RepoRoot, "merge", "--abort"); err != nil {
			return MergeBackResult{}, fmt.Errorf("abort conflicted merge: %w", err)
		}
		return MergeBackResult{
			Status:        MergeBackConflict,
			Branch:        branch,
			ConflictFiles: conflictFiles,
			Message:       fmt.Sprintf("merge produced conflicts and was aborted; resolve manually: git merge --no-ff %s", branch),
		}, nil
	}

	mergedSHA, err := gitOutput(ctx, runGit, options.RepoRoot, "rev-parse", "HEAD")
	if err != nil {
		return MergeBackResult{}, fmt.Errorf("resolve merge commit: %w", err)
	}
	return MergeBackResult{
		Status:    MergeBackMerged,
		Branch:    branch,
		CommitSHA: mergedSHA,
		Message:   fmt.Sprintf("merged %s (commit %.10s)", branch, mergedSHA),
	}, nil
}

func DefaultBaseDir(env map[string]string) (string, error) {
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(envValue(env, "LOCALAPPDATA")); localAppData != "" {
			return filepath.Join(localAppData, "splice", "worktrees"), nil
		}
		if profile := strings.TrimSpace(envValue(env, "USERPROFILE")); profile != "" {
			return filepath.Join(profile, "AppData", "Local", "splice", "worktrees"), nil
		}
	}

	if stateHome := strings.TrimSpace(envValue(env, "XDG_STATE_HOME")); stateHome != "" {
		return filepath.Join(stateHome, "splice", "worktrees"), nil
	}
	home := strings.TrimSpace(firstNonEmpty(envValue(env, "HOME"), envValue(env, "USERPROFILE")))
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
	}
	return filepath.Join(home, ".local", "state", "splice", "worktrees"), nil
}

func resolveCwd(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
	}
	absolute, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("cwd must be an existing directory: %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

func validateName(name string) error {
	if !worktreeNamePattern.MatchString(name) || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid worktree name %q: use letters, numbers, dots, dashes, or underscores", name)
	}
	return nil
}

func inspectTarget(target string) (bool, error) {
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect worktree path: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("worktree path already exists and is not a directory: %s", target)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		return true, nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return false, fmt.Errorf("inspect worktree directory: %w", err)
	}
	if len(entries) != 0 {
		return false, fmt.Errorf("worktree path already exists and is not empty: %s", target)
	}
	return false, nil
}

func gitOutput(ctx context.Context, runGit GitRunner, dir string, args ...string) (string, error) {
	return commandOutput(runGit(ctx, dir, args...))
}

func sameGitCommonDir(ctx context.Context, runGit GitRunner, sourceDir string, targetDir string) (bool, error) {
	sourceCommonDir, err := gitCommonDir(ctx, runGit, sourceDir)
	if err != nil {
		return false, err
	}
	targetCommonDir, err := gitCommonDir(ctx, runGit, targetDir)
	if err != nil {
		return false, err
	}
	return sourceCommonDir == targetCommonDir, nil
}

func gitCommonDir(ctx context.Context, runGit GitRunner, dir string) (string, error) {
	value, err := gitOutput(ctx, runGit, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(dir, value)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve git common dir: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve git common dir: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func defaultRunGit(ctx context.Context, dir string, args ...string) (CommandResult, error) {
	return defaultEnvRunGit(ctx, dir, nil, args...)
}

func defaultWorktreeName(now time.Time) string {
	return "task-" + now.UTC().Format("20060102-150405")
}

func repoKey(repoRoot string) string {
	sum := sha1.Sum([]byte(filepath.Clean(repoRoot)))
	hash := hex.EncodeToString(sum[:])[:10]
	base := filepath.Base(repoRoot)
	base = strings.ToLower(base)
	base = strings.Trim(regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(base, "-"), "-._")
	if base == "" {
		base = "repo"
	}
	return base + "-" + hash
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
