package worktrees

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
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
	Name         string       `json:"name"`
	Path         string       `json:"path"`
	RepoRoot     string       `json:"repoRoot"`
	SourceBranch string       `json:"sourceBranch,omitempty"`
	SourceCommit string       `json:"sourceCommit,omitempty"`
	Reused       bool         `json:"reused"`
	Locked       bool         `json:"locked,omitempty"`
	Prune        *PruneResult `json:"prune,omitempty"`
}

var worktreeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)

const activeWorktreeLockReason = "Splice active worktree"

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
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create worktree base directory: %w", err)
	}
	baseDir, err = filepath.EvalSymlinks(baseDir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve worktree base directory: %w", err)
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
		if err := lockWorktree(ctx, runGit, repoRoot, target); err != nil {
			return Result{}, fmt.Errorf("lock existing worktree: %w", err)
		}
		result.Locked = true
	}
	pruneResult, err := pruneRepo(ctx, runGit, repoRoot, baseDir, []string{target})
	if err != nil {
		if result.Locked {
			if unlockErr := unlockWorktree(ctx, runGit, repoRoot, target); unlockErr != nil {
				return Result{}, fmt.Errorf("prune worktrees: %w; unlock existing worktree: %v", err, unlockErr)
			}
		}
		return Result{}, fmt.Errorf("prune worktrees: %w", err)
	}
	if !pruneResult.IsEmpty() {
		result.Prune = &pruneResult
	}
	if reused {
		result.Reused = true
		return result, nil
	}
	// Parse and validate the optional seed manifest before creating the
	// worktree, so a bad seed (absolute path, "..", or a missing entry) fails
	// loudly before anything is created. A missing manifest is a no-op.
	seedEntries, err := parseWorktreeSeed(repoRoot)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create worktree directory: %w", err)
	}
	commandResult, err := runGit(ctx, repoRoot, "worktree", "add", "--detach", "--lock", "--reason", activeWorktreeLockReason, target, "HEAD")
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
	result.Locked = true
	if err := applyWorktreeSeed(repoRoot, target, seedEntries); err != nil {
		return Result{}, err
	}
	return result, nil
}

// worktreeSeedFile is the optional seed manifest inside the source repo root.
// A missing file is a no-op; no default path is invented.
const worktreeSeedFile = ".splice/worktree-seed"

// worktreeSeedEntry is one parsed seed line: a repo-root-relative path with a
// copy (Symlink false) or symlink (Symlink true) mode.
type worktreeSeedEntry struct {
	RelPath string
	Symlink bool
}

// parseWorktreeSeed reads and validates the optional seed manifest from the
// source repo root. A missing file returns a nil slice (no-op). Entries are
// one per line; "#" comments and blank lines are ignored, and a "symlink: "
// prefix selects symlink mode. Absolute paths and any ".." after cleaning are
// rejected, and a listed entry that does not exist in the source repo is a
// named error (typos must not silently no-op).
func parseWorktreeSeed(repoRoot string) ([]worktreeSeedEntry, error) {
	seedPath := filepath.Join(repoRoot, worktreeSeedFile)
	data, err := os.ReadFile(seedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read worktree seed %s: %w", seedPath, err)
	}
	entries := []worktreeSeedEntry{}
	for lineNum, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		symlink := false
		if rest, ok := strings.CutPrefix(line, "symlink: "); ok {
			symlink = true
			line = strings.TrimSpace(rest)
		}
		rel, err := validateWorktreeSeedEntry(repoRoot, seedPath, lineNum+1, line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, worktreeSeedEntry{RelPath: rel, Symlink: symlink})
	}
	return entries, nil
}

// validateWorktreeSeedEntry checks one entry and returns its cleaned
// repo-root-relative path. It rejects absolute paths, paths that escape the
// repo root via "..", and the repo root itself, and requires the entry to
// exist in the source repo.
func validateWorktreeSeedEntry(repoRoot, seedPath string, lineNum int, entry string) (string, error) {
	raw := strings.TrimSpace(entry)
	if raw == "" {
		return "", fmt.Errorf("worktree seed %s line %d: empty entry", seedPath, lineNum)
	}
	clean := filepath.Clean(raw)
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("worktree seed %s line %d: path %q escapes the repository", seedPath, lineNum, raw)
	}
	if _, err := os.Lstat(filepath.Join(repoRoot, clean)); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("worktree seed %s line %d: %q does not exist in the source repository", seedPath, lineNum, raw)
		}
		return "", fmt.Errorf("worktree seed %s line %d: inspect %q: %w", seedPath, lineNum, raw, err)
	}
	return clean, nil
}

// applyWorktreeSeed seeds the freshly created worktree from the validated
// entries: copies land as real files or directories, symlinks point back at
// the absolute source path under the repo root.
func applyWorktreeSeed(repoRoot, target string, entries []worktreeSeedEntry) error {
	for _, entry := range entries {
		src := filepath.Join(repoRoot, entry.RelPath)
		dst := filepath.Join(target, entry.RelPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("worktree seed parent dir for %q: %w", entry.RelPath, err)
		}
		if entry.Symlink {
			if err := os.Symlink(src, dst); err != nil {
				return fmt.Errorf("worktree seed symlink %q: %w", entry.RelPath, err)
			}
			continue
		}
		if err := copySeedPath(src, dst); err != nil {
			return fmt.Errorf("worktree seed copy %q: %w", entry.RelPath, err)
		}
	}
	return nil
}

// copySeedPath copies a file or directory tree from src to dst, preserving
// symlinks encountered inside a copied directory.
func copySeedPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(link, dst)
	}
	if !info.IsDir() {
		return copySeedFile(src, dst)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return copySeedFile(path, target)
	})
}

func copySeedFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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

	if err := commitWorktreeChanges(ctx, runGit, options); err != nil {
		return MergeBackResult{}, err
	}

	worktreeHead, err := gitOutput(ctx, runGit, options.WorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return MergeBackResult{}, fmt.Errorf("resolve worktree head: %w", err)
	}
	// The worktree has nothing to merge when its HEAD is already reachable from
	// the source HEAD. Plain SHA equality is not enough: the user may have made
	// unrelated commits in the source repo while the run was in flight.
	ancestor, err := isAncestor(ctx, runGit, options.RepoRoot, worktreeHead, "HEAD")
	if err != nil {
		return MergeBackResult{}, fmt.Errorf("check worktree ancestry: %w", err)
	}
	if ancestor {
		return MergeBackResult{
			Status:  MergeBackNoChanges,
			Message: "worktree has no changes to merge",
		}, nil
	}

	branch, err := pinWorktreeBranch(ctx, runGit, options.WorktreePath, options.Name)
	if err != nil {
		return MergeBackResult{}, err
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

// RemoveOptions configures Remove.
type RemoveOptions struct {
	// RepoRoot is the source repository the worktree belongs to.
	RepoRoot string
	// Path is the isolated worktree directory to remove.
	Path string
	// Force skips the clean-tree check and passes --force to git worktree remove.
	// Use it only when the caller is discarding the worktree (reject), not when
	// the work has already landed in source history.
	Force  bool
	RunGit GitRunner
}

// Remove unregisters and deletes an isolated worktree whose work is in source
// history. Without Force it refuses tracked, untracked, or ignored files. It
// does not change recovery refs and splice/* branches.
func Remove(ctx context.Context, options RemoveOptions) error {
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	if strings.TrimSpace(options.RepoRoot) == "" {
		return fmt.Errorf("remove worktree %q: repo root is required", options.Path)
	}
	if strings.TrimSpace(options.Path) == "" {
		return fmt.Errorf("remove worktree: path is required")
	}
	if !options.Force {
		status, err := gitOutput(ctx, runGit, options.Path, "status", "--porcelain", "--untracked-files=all", "--ignored=matching")
		if err != nil {
			return fmt.Errorf("inspect worktree %q before removal: %w", options.Path, err)
		}
		if strings.TrimSpace(status) != "" {
			return fmt.Errorf("remove worktree %q: tracked, untracked, or ignored files remain", options.Path)
		}
	}
	args := []string{"worktree", "remove"}
	if options.Force {
		args = append(args, "--force")
	}
	args = append(args, options.Path)
	if _, err := gitOutput(ctx, runGit, options.RepoRoot, args...); err != nil {
		return fmt.Errorf("remove worktree %q: %w", options.Path, err)
	}
	return nil
}

// Preserve commits the worktree's changes and pins splice/<name> to that commit.
// It does not merge into the source checkout. Callers use it before discarding
// a worktree so the run's work survives on the branch.
func Preserve(ctx context.Context, options MergeBackOptions) (string, error) {
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	if err := validateName(options.Name); err != nil {
		return "", err
	}
	if err := commitWorktreeChanges(ctx, runGit, options); err != nil {
		return "", err
	}
	return pinWorktreeBranch(ctx, runGit, options.WorktreePath, options.Name)
}

func commitWorktreeChanges(ctx context.Context, runGit GitRunner, options MergeBackOptions) error {
	status, err := gitOutput(ctx, runGit, options.WorktreePath, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect worktree status: %w", err)
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	if _, err := gitOutput(ctx, runGit, options.WorktreePath, "add", "-A"); err != nil {
		return fmt.Errorf("stage worktree changes: %w", err)
	}
	message := strings.TrimSpace(options.CommitMessage)
	if message == "" {
		message = "splice: worktree " + options.Name
	}
	if _, err := gitOutput(ctx, runGit, options.WorktreePath, "commit", "-m", message); err != nil {
		return fmt.Errorf("commit worktree changes: %w", err)
	}
	return nil
}

func pinWorktreeBranch(ctx context.Context, runGit GitRunner, worktreePath, name string) (string, error) {
	branch := "splice/" + name
	if _, err := gitOutput(ctx, runGit, worktreePath, "branch", "-f", branch, "HEAD"); err != nil {
		return "", fmt.Errorf("pin worktree branch: %w", err)
	}
	return branch, nil
}

// SourceDirty reports whether repoRoot has uncommitted tracked or untracked files.
func SourceDirty(ctx context.Context, repoRoot string, runGit GitRunner) (bool, error) {
	if runGit == nil {
		runGit = defaultRunGit
	}
	if strings.TrimSpace(repoRoot) == "" {
		return false, fmt.Errorf("inspect source status: repo root is required")
	}
	status, err := gitOutput(ctx, runGit, repoRoot, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, fmt.Errorf("inspect source status %q: %w", repoRoot, err)
	}
	return strings.TrimSpace(status) != "", nil
}

// UnlockOptions configures Unlock.
type UnlockOptions struct {
	RepoRoot string
	Path     string
	RunGit   GitRunner
}

// Unlock releases the active-use lock that Prepare creates.
func Unlock(ctx context.Context, options UnlockOptions) error {
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	return unlockWorktree(ctx, runGit, options.RepoRoot, options.Path)
}

func lockWorktree(ctx context.Context, runGit GitRunner, repoRoot, path string) error {
	if _, err := gitOutput(ctx, runGit, repoRoot, "worktree", "lock", "--reason", activeWorktreeLockReason, path); err != nil {
		return fmt.Errorf("lock worktree %q: %w", path, err)
	}
	return nil
}

func unlockWorktree(ctx context.Context, runGit GitRunner, repoRoot, path string) error {
	if strings.TrimSpace(repoRoot) == "" || strings.TrimSpace(path) == "" {
		return fmt.Errorf("unlock worktree: repo root and path are required")
	}
	if _, err := gitOutput(ctx, runGit, repoRoot, "worktree", "unlock", path); err != nil {
		return fmt.Errorf("unlock worktree %q: %w", path, err)
	}
	return nil
}

// PruneSkip reports a managed worktree left in place and why.
type PruneSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// PruneResult reports the managed worktrees the sweep removed and those it
// left in place with a reason.
type PruneResult struct {
	Removed []string    `json:"removed"`
	Skipped []PruneSkip `json:"skipped"`
}

// IsEmpty reports whether the sweep has nothing to report.
func (r PruneResult) IsEmpty() bool {
	return len(r.Removed) == 0 && len(r.Skipped) == 0
}

// PruneOptions configures Prune.
type PruneOptions struct {
	// Cwd is a directory inside the source repository to scan.
	Cwd string
	// BaseDir is the base directory for Splice worktrees. An empty value
	// resolves the platform default.
	BaseDir string
	RunGit  GitRunner
}

// Prune removes only unlocked, clean Splice worktrees from the managed
// directory. The HEAD must remain reachable from source HEAD or a splice/*
// branch. Prune reports all other managed worktrees and keeps them.
func Prune(ctx context.Context, options PruneOptions) (PruneResult, error) {
	cwd, err := resolveCwd(options.Cwd)
	if err != nil {
		return PruneResult{}, err
	}
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}

	repoRoot, err := gitOutput(ctx, runGit, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return PruneResult{}, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot = filepath.Clean(repoRoot)

	baseDir := strings.TrimSpace(options.BaseDir)
	if baseDir == "" {
		baseDir, err = DefaultBaseDir(nil)
		if err != nil {
			return PruneResult{}, err
		}
	}
	baseDir, err = filepath.Abs(baseDir)
	if err != nil {
		return PruneResult{}, fmt.Errorf("resolve worktree dir: %w", err)
	}
	baseDir, err = filepath.EvalSymlinks(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return PruneResult{}, nil
		}
		return PruneResult{}, fmt.Errorf("resolve worktree base directory: %w", err)
	}
	return pruneRepo(ctx, runGit, repoRoot, baseDir, nil)
}

func pruneRepo(ctx context.Context, runGit GitRunner, repoRoot, baseDir string, exclude []string) (PruneResult, error) {
	repoDir, err := filepath.Abs(filepath.Join(baseDir, "splice-worktree-"+repoKey(repoRoot)))
	if err != nil {
		return PruneResult{}, fmt.Errorf("resolve managed dir: %w", err)
	}
	info, err := os.Lstat(repoDir)
	if err != nil {
		if os.IsNotExist(err) {
			return PruneResult{}, nil
		}
		return PruneResult{}, fmt.Errorf("inspect managed dir: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return PruneResult{}, fmt.Errorf("managed dir must be a real directory: %s", repoDir)
	}

	sourceHead, err := gitOutput(ctx, runGit, repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return PruneResult{}, fmt.Errorf("resolve source HEAD: %w", err)
	}
	sourceHead = strings.TrimSpace(sourceHead)

	spliceRefs, err := listSpliceRefs(ctx, runGit, repoRoot)
	if err != nil {
		return PruneResult{}, fmt.Errorf("list splice refs: %w", err)
	}

	worktreeList, err := gitOutput(ctx, runGit, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return PruneResult{}, fmt.Errorf("list worktrees: %w", err)
	}

	excludeSet := make(map[string]struct{}, len(exclude))
	for _, path := range exclude {
		excludeSet[filepath.Clean(path)] = struct{}{}
	}

	result := PruneResult{}
	for _, entry := range parseWorktreeList(worktreeList) {
		path := filepath.Clean(entry.Path)
		if path == repoRoot {
			continue
		}
		if _, excluded := excludeSet[path]; excluded {
			continue
		}
		if filepath.Dir(path) != repoDir {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: fmt.Sprintf("inspect path: %v", err)})
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: "path is not a real directory"})
			continue
		}

		if entry.Locked {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: "locked"})
			continue
		}

		status, err := gitOutput(ctx, runGit, entry.Path, "status", "--porcelain", "--untracked-files=all", "--ignored=matching")
		if err != nil {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: fmt.Sprintf("inspect: %v", err)})
			continue
		}
		if strings.TrimSpace(status) != "" {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: "files not saved in Git"})
			continue
		}

		head, err := gitOutput(ctx, runGit, entry.Path, "rev-parse", "HEAD")
		if err != nil {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: fmt.Sprintf("resolve HEAD: %v", err)})
			continue
		}
		head = strings.TrimSpace(head)

		reachable, err := isReachable(ctx, runGit, repoRoot, head, sourceHead, spliceRefs)
		if err != nil {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: fmt.Sprintf("ancestry: %v", err)})
			continue
		}
		if !reachable {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: "not reachable from source HEAD or splice refs"})
			continue
		}

		if err := Remove(ctx, RemoveOptions{RepoRoot: repoRoot, Path: entry.Path, RunGit: runGit}); err != nil {
			result.Skipped = append(result.Skipped, PruneSkip{Path: entry.Path, Reason: fmt.Sprintf("remove: %v", err)})
			continue
		}
		result.Removed = append(result.Removed, entry.Path)
	}

	return result, nil
}

type worktreeListEntry struct {
	Path   string
	Locked bool
}

func parseWorktreeList(output string) []worktreeListEntry {
	var entries []worktreeListEntry
	var current *worktreeListEntry
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if current != nil {
				entries = append(entries, *current)
				current = nil
			}
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			current = &worktreeListEntry{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))}
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(line, "locked") {
			current.Locked = true
		}
	}
	if current != nil {
		entries = append(entries, *current)
	}
	return entries
}

func listSpliceRefs(ctx context.Context, runGit GitRunner, repoRoot string) ([]string, error) {
	out, err := gitOutput(ctx, runGit, repoRoot, "for-each-ref", "--format=%(refname)", "refs/heads/splice/")
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

func isReachable(ctx context.Context, runGit GitRunner, repoRoot, worktreeHead, sourceHead string, spliceRefs []string) (bool, error) {
	refs := append([]string{sourceHead}, spliceRefs...)
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		reachable, err := isAncestor(ctx, runGit, repoRoot, worktreeHead, ref)
		if err != nil {
			return false, err
		}
		if reachable {
			return true, nil
		}
	}
	return false, nil
}

func isAncestor(ctx context.Context, runGit GitRunner, repoRoot, ancestor, descendant string) (bool, error) {
	result, err := runGit(ctx, repoRoot, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		_, err := commandOutput(result, nil)
		return false, err
	}
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
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect worktree path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("worktree path already exists and is not a real directory: %s", target)
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

// SameRepo reports whether two directories belong to the same git repository,
// proven by their resolved git common directory. The trust-inheritance spike
// uses it to prove a worktree was prepared from a trusted source repo.
func SameRepo(ctx context.Context, runGit GitRunner, sourceDir, targetDir string) (bool, error) {
	if runGit == nil {
		runGit = defaultRunGit
	}
	return sameGitCommonDir(ctx, runGit, sourceDir, targetDir)
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
