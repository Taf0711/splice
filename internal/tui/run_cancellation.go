package tui

// run_cancellation.go: one cancellation finalization shared by every run
// kind (review finding 3).
//
// cancelRun clears activeRunID and records the run in flushRunIDs. Only the
// stale agentResponseMsg branch used to drain that obligation and project
// the outcome. A cancelled APPROVAL run returned planExecutionResultMsg,
// and a cancelled CRYSTALLIZATION returned crystallizeResultMsg; both hit
// stale-result early returns that unlocked the worktree and returned. The
// consequences were a cancellation the user never saw a receipt for, and a
// run ID that stayed in flushRunIDs forever, so a Ctrl+C exit could wait on
// a drain no handler would ever perform.
//
// Cancellation and stale-session handling are separate concerns. A result
// for a run the user cancelled is EXPECTED and must be finalized. A result
// for a run superseded by an unrelated session switch is stale and only
// releases its resources.

import (
	"github.com/Taf0711/splice/internal/worktrees"
)

// cancelledRunFinalization describes what a terminal message carries for a
// run that was cancelled while in flight.
type cancelledRunFinalization struct {
	runID    int
	worktree *worktrees.Result
	// preserved and mergeAvailable describe the lane the cancelled run
	// leaves behind, so the handoff card states worktree truth.
	preserved      bool
	mergeAvailable bool
}

// wasCancelled reports whether runID belongs to a run the user cancelled
// and whose drain obligation is still outstanding.
func (m model) wasCancelled(runID int) bool {
	if runID == 0 || len(m.flushRunIDs) == 0 {
		return false
	}
	_, pending := m.flushRunIDs[runID]
	return pending
}

// finalizeCancelledRun projects the cancellation and discharges the drain
// obligation exactly once. It is called from every terminal-result handler
// whose message arrives for a cancelled run, so the CANCELLED receipt and
// the HANDOFF surface appear no matter which run kind was cancelled.
//
// The worktree is RETAINED, not unlocked: the user stopped the run and the
// staged work is theirs to accept or discard from the receipt's keys.
func (m model) finalizeCancelledRun(fin cancelledRunFinalization) model {
	delete(m.flushRunIDs, fin.runID)
	delete(m.liveUsageCounts, fin.runID)

	// The cancelled run's lane is the live worktree again: the receipt's
	// [A]/[D] keys act on it.
	if fin.worktree != nil {
		m.activeWorktree = fin.worktree
	}

	// CANCELLED is distinct from FAILED by contract: staged work exists,
	// the user stopped it, and nothing was applied.
	card := cancelledReceiptForWorktree(fin.worktree)
	m.lastTerminalReceipt = card.kind
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowError,
		text: receiptTranscriptPayload(card),
	})
	m.offerHandoff(fin.worktree, "cancelled", 0, 0, fin.preserved, fin.mergeAvailable)
	m.reportAgentLifecycle(herdrIdle)
	return m
}

// cancelledRunExit fires the deferred Ctrl+C quit once the last cancelled
// run has been drained. Returns true when the caller must quit.
func (m model) cancelledRunExit() bool {
	return m.exiting && len(m.flushRunIDs) == 0
}
