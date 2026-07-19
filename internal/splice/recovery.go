package splice

import "context"

// WorkspaceRecovery captures and restores git-visible workspace state
// snapshots for rollback.
//
// A nil WorkspaceRecovery means recovery is not available: the pipeline
// aborts immediately on a rollback trajectory decision instead of attempting
// recovery. Capture and Restore may return context.Canceled; the pipeline
// propagates that error without further stages.
type WorkspaceRecovery interface {
	// Capture snapshots the current workspace state under an opaque ref
	// keyed by (runID, iteration).
	Capture(ctx context.Context, runID string, iteration int) (string, error)

	// Restore rewinds the workspace from expectedCurrentRef to targetRef.
	// It verifies the workspace is at expectedCurrentRef before mutating,
	// so concurrent modifications are detected.
	Restore(ctx context.Context, expectedCurrentRef, targetRef string) error
}

// snapshot pairs a captured ref with the iteration and score it represents.
type snapshot struct {
	ref   string
	iter  int
	score float64
}

// selectBestSnapshot returns the highest-scoring completed snapshot before
// currentIter, breaking ties toward the latest iteration. Iteration 0 is a
// defensive fallback only when no completed prior snapshot exists.
func selectBestSnapshot(snapshots []snapshot, currentIter int) (snapshot, bool) {
	var best snapshot
	found := false
	for _, candidate := range snapshots {
		if candidate.iter <= 0 || candidate.iter >= currentIter {
			continue
		}
		if !found || candidate.score > best.score || (candidate.score == best.score && candidate.iter > best.iter) {
			best = candidate
			found = true
		}
	}
	if found {
		return best, true
	}
	for _, candidate := range snapshots {
		if candidate.iter == 0 {
			return candidate, true
		}
	}
	return snapshot{}, false
}
