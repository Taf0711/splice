package cognition

import (
	"context"
	"strings"

	"github.com/Taf0711/splice/internal/worktrees"
)

// FreshnessState classifies whether an observation's structural anchor has
// changed since the observation's source commit.
type FreshnessState string

const (
	FreshnessFresh   FreshnessState = "fresh"
	FreshnessStale   FreshnessState = "stale"
	FreshnessUnknown FreshnessState = "unknown"
)

// ClassifyFreshness reports whether the anchor path changed since the
// observation's source commit, by running `git diff --quiet <commit> -- <anchor>`
// in the repo root (exit 0 = fresh, exit 1 = stale). Any other outcome -
// missing commit, not a repository, anchor error - is unknown, and unknown
// fails closed: the caller must NOT inject the observation directly.
// Symbol-level freshness is out of scope for C0: a change to the containing
// file (or, for package keys, anything under the package directory) makes
// the observation stale.
func ClassifyFreshness(ctx context.Context, repoRoot, sourceCommit, anchorPath string) FreshnessState {
	if strings.TrimSpace(sourceCommit) == "" || strings.TrimSpace(anchorPath) == "" || strings.TrimSpace(repoRoot) == "" {
		return FreshnessUnknown
	}
	exit, err := worktrees.DiffQuiet(ctx, nil, repoRoot, "diff", "--quiet", sourceCommit, "--", anchorPath)
	if err != nil {
		return FreshnessUnknown
	}
	switch exit {
	case 0:
		return FreshnessFresh
	case 1:
		return FreshnessStale
	default:
		return FreshnessUnknown
	}
}
