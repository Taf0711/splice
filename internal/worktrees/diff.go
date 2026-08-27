package worktrees

import "context"

// DiffQuiet runs `git diff --quiet <args>` in dir and returns the exit code.
// exit 0 means no differences, exit 1 means differences, and any other value
// is an error (mapped to -1 with the error). A nil runner uses the default.
// This is the cognition freshness gate's reuse path (C0.3): the cognition
// package calls it so the freshness check does not spin up a second exec
// path for git.
func DiffQuiet(ctx context.Context, runGit GitRunner, dir string, args ...string) (int, error) {
	if runGit == nil {
		runGit = defaultRunGit
	}
	result, err := runGit(ctx, dir, args...)
	if err != nil {
		return -1, err
	}
	return result.ExitCode, nil
}
