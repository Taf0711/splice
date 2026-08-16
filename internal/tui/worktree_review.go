package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/worktrees"
)

const (
	worktreeReviewAccept = "Accept"
	worktreeReviewReject = "Reject"
	worktreeReviewKeep   = "Keep"

	worktreeReviewDirtyNotice = "Main checkout has uncommitted changes; merge-back is unavailable."
)

type worktreeReviewResultMsg struct {
	notice string
	kept   *worktrees.Result
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
	case strings.ToLower(worktreeReviewAccept), "merge":
		return worktreeReviewAccept
	case strings.ToLower(worktreeReviewReject), "discard":
		return worktreeReviewReject
	default:
		return worktreeReviewKeep
	}
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
	copy := *wt
	m.transcript = appendTranscriptRow(m.transcript, askUserTranscriptRow(req))
	m.pendingAskUser = &pendingAskUserPrompt{
		request:   req,
		states:    newAskUserStates(req.Questions),
		keepOnEsc: true,
		worktree:  &copy,
		dirtyMain: dirty,
	}
	m.reportAgentLifecycle(herdrBlocked)
	m.clearComposer()
	m.clearSuggestions()
	return m, nil
}

func applyWorktreeReview(wt worktrees.Result, decision string, dirtyOffered bool) worktreeReviewResultMsg {
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
		branch, err := tuiPreserveWorktree(ctx, worktrees.PreserveOptions{
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
