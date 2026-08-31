package tui

import (
	"context"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/worktrees"
)

const (
	worktreeReviewAccept = "Accept"
	worktreeReviewReject = "Reject"
	worktreeReviewKeep   = "Keep"

	worktreeReviewDirtyNotice = "Main checkout has uncommitted changes; merge-back is unavailable."
)

const (
	worktreeRejectWrongApproach = "wrong_approach"
	worktreeRejectStillFailing  = "still_failing"
	worktreeRejectChangedMind   = "changed_mind"
	worktreeRejectOther         = "other"
	worktreeRejectUnspecified   = "unspecified"
)

type worktreeReviewResultMsg struct {
	notice   string
	kept     *worktrees.Result
	decision string
	reason   string
}

func inspectSourceDirty(wt worktrees.Result) bool {
	if strings.TrimSpace(wt.RepoRoot) == "" {
		return false
	}
	dirty, err := tuiSourceDirty(context.Background(), wt.RepoRoot, nil)
	return err == nil && dirty
}

func worktreeReviewAskRequest(wt worktrees.Result, dirty bool) agent.AskUserRequest {
	options := []string{worktreeReviewAccept, worktreeReviewReject, worktreeReviewKeep}
	descs := []string{
		"Merge into the main checkout and remove the worktree.",
		"Remove the worktree. Work remains on branch splice/" + wt.Name + ".",
		"Leave the worktree in place.",
	}
	question := "The pipeline finished in worktree " + wt.Path + ". Accept merges into the main checkout. Reject removes the worktree and keeps branch splice/" + wt.Name + ". Keep leaves the worktree in place. Esc keeps the worktree."
	if dirty {
		options = []string{worktreeReviewReject, worktreeReviewKeep}
		descs = descs[1:]
		question = "The pipeline finished in worktree " + wt.Path + ". Merge-back is unavailable because the main checkout has uncommitted changes. Reject removes the worktree and keeps branch splice/" + wt.Name + ". Keep leaves the worktree in place. Esc keeps the worktree."
	}
	return agent.AskUserRequest{
		ToolCallID: "worktree_review:" + wt.Name,
		Header:     "Worktree review",
		Questions: []agent.AskUserQuestion{{
			Question:           question,
			Header:             "Worktree",
			Options:            options,
			OptionDescriptions: descs,
			Recommended:        worktreeReviewKeep,
		}},
	}
}

func parseWorktreeReviewDecision(answers []string) string {
	if len(answers) == 0 {
		return worktreeReviewKeep
	}
	switch strings.ToLower(strings.TrimSpace(answers[0])) {
	case strings.ToLower(worktreeReviewAccept):
		return worktreeReviewAccept
	case strings.ToLower(worktreeReviewReject):
		return worktreeReviewReject
	default:
		return worktreeReviewKeep
	}
}

func worktreeRejectReasonAskRequest() agent.AskUserRequest {
	return agent.AskUserRequest{
		ToolCallID: "worktree_reject_reason:" + worktreeReviewReject,
		Header:     "Worktree reject",
		Questions: []agent.AskUserQuestion{{
			Question: "Why are you rejecting?",
			Header:   "Reason",
			Options: []string{
				worktreeRejectWrongApproach,
				worktreeRejectStillFailing,
				worktreeRejectChangedMind,
				worktreeRejectOther,
			},
			Recommended: worktreeRejectOther,
		}},
	}
}

func parseWorktreeRejectReason(answers []string) string {
	if len(answers) == 0 {
		return worktreeRejectUnspecified
	}
	reason := strings.TrimSpace(answers[0])
	if reason == "" {
		return worktreeRejectUnspecified
	}
	return reason
}

// verdictForReview maps a worktree review decision to a post-run verdict.
// accept = kept; reject = rejected with the Q1 reason; keep/Esc = nil (no
// record, so the effective verdict stays unknown).
func verdictForReview(decision, reason string) *schemas.VerdictRecord {
	switch decision {
	case worktreeReviewAccept:
		return &schemas.VerdictRecord{Verdict: schemas.VerdictKept}
	case worktreeReviewReject:
		return &schemas.VerdictRecord{Verdict: schemas.VerdictRejected, RejectReason: reason}
	default:
		return nil
	}
}

// tuiUpsertVerdict writes a post-run verdict through the trace sidecar. It is
// a seam so tests can replace it; the default resolves the memd client, which
// implements TraceStore. A nil client (memory/tracing off) skips silently.
var tuiUpsertVerdict = func(ctx context.Context, verdict schemas.VerdictRecord) error {
	client, err := tuiResolveMemory(ctx)
	if err != nil {
		return err
	}
	if client == nil {
		return nil
	}
	return client.UpsertVerdict(ctx, verdict)
}

// verdictWriteMsg surfaces a best-effort verdict write failure as a warning.
type verdictWriteMsg struct{ err error }

// writeVerdictCmd runs tuiUpsertVerdict off the UI loop and reports failures
// as a warning row.
func (m model) writeVerdictCmd(verdict schemas.VerdictRecord) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tuiUpsertVerdict(ctx, verdict); err != nil {
			return verdictWriteMsg{err: err}
		}
		return verdictWriteMsg{}
	}
}

// offerWorktreeRejectReason shows the one follow-up question after the user picks
// Reject, before the worktree is removed. The answer (or Esc/empty) feeds the
// reject reason recorded on the review decision's session event.
func (m model) offerWorktreeRejectReason(wt worktrees.Result) (model, tea.Cmd) {
	req := worktreeRejectReasonAskRequest()
	m.transcript = appendTranscriptRow(m.transcript, askUserTranscriptRow(req))
	m.pendingAskUser = &pendingAskUserPrompt{
		request:        req,
		states:         newAskUserStates(req.Questions),
		keepOnEsc:      true,
		worktree:       &wt,
		reviewDecision: worktreeReviewReject,
	}
	m.reportAgentLifecycle(herdrBlocked)
	m.clearComposer()
	m.clearSuggestions()
	return m, nil
}

func (m model) maybeOfferWorktreeReview(wt *worktrees.Result, dirty bool) (model, tea.Cmd) {
	if wt == nil || strings.TrimSpace(wt.Path) == "" {
		return m, nil
	}
	m.activeWorktree = wt
	if dirty {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: worktreeReviewDirtyNotice + " Keep the worktree and merge manually: git merge --no-ff splice/" + wt.Name})
	}
	req := worktreeReviewAskRequest(*wt, dirty)
	snapshot := *wt
	m.transcript = appendTranscriptRow(m.transcript, askUserTranscriptRow(req))
	m.pendingAskUser = &pendingAskUserPrompt{
		request:   req,
		states:    newAskUserStates(req.Questions),
		keepOnEsc: true,
		worktree:  &snapshot,
		dirtyMain: dirty,
	}
	m.reportAgentLifecycle(herdrBlocked)
	m.clearComposer()
	m.clearSuggestions()
	return m, nil
}

// offerHandoff appends the HANDOFF card for an exited lane whose work
// survived (kept worktree, or failure/cancellation before any review).
// It distinguishes lane death from work loss: preserved is the caller's
// filesystem check of the worktree path. mergeAvailable mirrors the
// review's dirty-main gate. Best-effort: the card never fails the turn.
func (m *model) offerHandoff(wt *worktrees.Result, outcome string, staged, applied int) {
	if wt == nil || strings.TrimSpace(wt.Path) == "" {
		return
	}
	preserved := tuiWorktreeExists(wt.Path)
	h := handoffState{
		lane:      wt.Name,
		path:      wt.Path,
		branch:    "splice/" + wt.Name,
		outcome:   outcome,
		staged:    staged,
		applied:   applied,
		preserved: preserved,
	}
	mergeAvailable := preserved && !inspectSourceDirty(*wt)
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowSystem,
		text: handoffTranscriptPayload(h, mergeAvailable),
	})
}

// tuiWorktreeExists reports whether the worktree path still exists on disk
// (the lane-death vs work-loss check).
func tuiWorktreeExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func applyWorktreeReview(wt worktrees.Result, decision string, dirtyOffered bool, reason string) worktreeReviewResultMsg {
	msg := runWorktreeReview(wt, decision, dirtyOffered)
	msg.decision = decision
	msg.reason = reason
	return msg
}

func runWorktreeReview(wt worktrees.Result, decision string, dirtyOffered bool) worktreeReviewResultMsg {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	unlock := func() error {
		if !wt.Locked {
			return nil
		}
		err := tuiUnlockWorktree(ctx, worktrees.UnlockOptions{RepoRoot: wt.RepoRoot, Path: wt.Path})
		if err == nil {
			wt.Locked = false
		}
		return err
	}
	keep := func(notice string) worktreeReviewResultMsg {
		if err := unlock(); err != nil {
			notice = notice + " Unlock failed: " + err.Error()
		}
		kept := wt
		return worktreeReviewResultMsg{notice: notice, kept: &kept}
	}
	if decision == worktreeReviewAccept && dirtyOffered {
		return keep(worktreeReviewDirtyNotice + " Worktree kept at " + wt.Path)
	}
	switch decision {
	case worktreeReviewAccept:
		result, err := tuiMergeBackWorktree(ctx, worktrees.MergeBackOptions{
			RepoRoot:     wt.RepoRoot,
			WorktreePath: wt.Path,
			Name:         wt.Name,
		})
		if err != nil {
			return keep("Merge-back failed: " + err.Error() + ". Worktree kept at " + wt.Path)
		}
		if result.Status != worktrees.MergeBackMerged && result.Status != worktrees.MergeBackNoChanges {
			return keep("Merge-back refused: " + result.Message + ". Worktree kept at " + wt.Path)
		}
		if err := unlock(); err != nil {
			return worktreeReviewResultMsg{notice: result.Message + ". Unlock failed: " + err.Error() + "; worktree left at " + wt.Path, kept: &wt}
		}
		if err := tuiRemoveWorktree(ctx, worktrees.RemoveOptions{RepoRoot: wt.RepoRoot, Path: wt.Path}); err != nil {
			return worktreeReviewResultMsg{notice: result.Message + ". Cleanup failed; worktree left at " + wt.Path + ": " + err.Error(), kept: &wt}
		}
		return worktreeReviewResultMsg{notice: result.Message}
	case worktreeReviewReject:
		branch, err := tuiPreserveWorktree(ctx, worktrees.MergeBackOptions{
			RepoRoot:     wt.RepoRoot,
			WorktreePath: wt.Path,
			Name:         wt.Name,
		})
		if err != nil {
			return keep("Could not preserve worktree branch: " + err.Error() + ". Worktree kept at " + wt.Path)
		}
		if err := unlock(); err != nil {
			return worktreeReviewResultMsg{notice: "Unlock failed; worktree left at " + wt.Path + ": " + err.Error(), kept: &wt}
		}
		if err := tuiRemoveWorktree(ctx, worktrees.RemoveOptions{RepoRoot: wt.RepoRoot, Path: wt.Path, Force: true}); err != nil {
			return worktreeReviewResultMsg{notice: "Discard failed; worktree left at " + wt.Path + ": " + err.Error(), kept: &wt}
		}
		return worktreeReviewResultMsg{notice: "Worktree removed. Work remains on branch " + branch + " if you change your mind."}
	default:
		return keep("Worktree kept at " + wt.Path)
	}
}

func unlockPreparedWorktree(wt *worktrees.Result) {
	if wt == nil || !wt.Locked {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tuiUnlockWorktree(ctx, worktrees.UnlockOptions{RepoRoot: wt.RepoRoot, Path: wt.Path})
	wt.Locked = false
}
