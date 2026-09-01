package tui

// run_interaction_state.go (F2, stabilization directive P0): interaction and
// lane state that belongs to a RUN or a session's lifecycle must not survive
// a session switch. A pendingHandoff for the previous session's worktree, a
// diff viewport showing that worktree, or a worktree review bound to a dead
// conversation are all ways the UI outlives its own truth.
//
// Ownership rule (directive §1/§26): these are UI interaction states bound to
// session-scoped runtime truth. The runtime truth (worktree results, review
// decisions, presentation snapshots) lives in the session event log and is
// reconstructed on resume; the INTERACTION surfaces built on top of them are
// per-session and reset here. Nothing here decides runtime semantics — a
// kept worktree stays on disk regardless; only the TUI's actionable surface
// for it resets.

// resetRunInteractionState clears every interaction surface bound to the
// previous session's run. Called on a REAL session change (new session or
// resume of a different session), never on a no-op switch (/resume of the
// active session must not disturb open views).
func (m model) resetRunInteractionState() model {
	// HANDOFF surface: the pending handoff names a lane from the previous
	// session's run. The worktree itself stays on disk (runtime truth,
	// resumable via git or /lane resume); only the actionable card resets.
	m.pendingHandoff = nil
	// The diff viewport shows the previous lane's worktree diff.
	if m.diffView.active {
		m = m.exitDiffReview()
	}
	// The file drill-in shows the previous conversation's touched files.
	if m.fileView.active {
		m = m.exitFileView()
	}
	// The worktree bound to the previous run. unlockPreparedWorktree is NOT
	// called here: the review/cancel paths already own worktree cleanup, and
	// a session switch must not unlock a worktree a live run may still hold.
	m.activeWorktree = nil
	// Trajectory was (possibly) auto-revealed by the previous run's
	// regression; the new session starts with the §10 default.
	m.trajectoryVisible = false
	m.trajectoryAutoRevealed = false
	return m
}
