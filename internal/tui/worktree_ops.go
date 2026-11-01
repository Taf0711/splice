package tui

// worktree_ops.go: one typed scheduler for every user-initiated worktree
// mutation (review finding 4). Four call sites used to run the Git work
// synchronously inside Update and then return a command that merely
// delivered an already-computed result: handoff merge, handoff discard,
// receipt actions, and diff-review approve. The closure moved message
// delivery off the loop, not the Git execution, so a slow commit, hook,
// merge, or removal stalled typing, redraw, and cancellation for up to the
// operation's 60 second deadline.
//
// Ownership rule: a worktree mutation is runtime work. The UI schedules it,
// records that it is in flight, and projects the outcome when the result
// arrives. It never performs the mutation itself and never announces a
// result it has not seen (finding 5).

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/worktrees"
)

// worktreeOpKind names the surface that scheduled an operation, so the
// result handler can restore exactly the surface that owned it.
type worktreeOpKind string

const (
	worktreeOpHandoffMerge   worktreeOpKind = "handoff_merge"
	worktreeOpHandoffDiscard worktreeOpKind = "handoff_discard"
	worktreeOpReceipt        worktreeOpKind = "receipt"
	worktreeOpDiffApprove    worktreeOpKind = "diff_approve"
)

// worktreeOpState records the single in-flight worktree mutation. A second
// action is refused while it is set, so a slow merge cannot be dispatched
// twice by an impatient keypress.
type worktreeOpState struct {
	kind    worktreeOpKind
	lane    string
	session string
	runID   int
}

// scheduleWorktreeOp arms the in-flight state and returns a command that
// runs the Git work in the command goroutine. The identity (kind, lane,
// session, run) is snapshotted at schedule time so a result that lands
// after a session switch or a new run can be recognized as stale.
func (m model) scheduleWorktreeOp(kind worktreeOpKind, wt worktrees.Result, decision string, reason string) (model, tea.Cmd) {
	m.worktreeOp = &worktreeOpState{
		kind:    kind,
		lane:    wt.Name,
		session: m.activeSession.SessionID,
		runID:   m.activeRunID,
	}
	op := *m.worktreeOp
	// The reviewed tree pins what the user saw when the action came from
	// the diff pane. Other surfaces establish no review boundary and pass
	// an empty tree, which disables revalidation rather than inventing one.
	reviewedTree := ""
	if kind == worktreeOpDiffApprove {
		reviewedTree = m.diffView.tree
	}
	return m, func() tea.Msg {
		msg := applyReviewedWorktree(wt, decision, false, reason, reviewedTree)
		msg.op = &op
		return msg
	}
}

// worktreeOpBusy reports whether a worktree mutation is already running.
func (m model) worktreeOpBusy() bool { return m.worktreeOp != nil }

// worktreeActionAllowed gates every user-initiated worktree mutation.
// Mutations are refused while a run is pending (finding 2: a preserved lane
// followed by a new run must not be merged out from under that run), while
// another mutation is in flight, and while a modal owns input.
func (m model) worktreeActionAllowed() (bool, string) {
	if m.pending {
		return false, "A run is active. Wait for it to finish before acting on the lane."
	}
	if m.worktreeOpBusy() {
		return false, "A worktree operation is already running on lane " + m.worktreeOp.lane + "."
	}
	return true, ""
}
