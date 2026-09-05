package tui

// receipt_keys.go wires the advertised receipt and lifecycle-card action
// keys (GAP-E receipt cards, P4 plan/critique cards). Frame j3ZQBu cell 4:
// a dead key is never advertised. The dispatch state is runtime truth —
// lastTerminalReceipt for the terminal outcome cards, pendingPlan /
// pendingCritique for the design-lane cards — never transcript scraping.
//
// Key shapes follow handleHandoffKey (worktree_review.go): the advertised
// keys are CAPITAL letters, and ultraviolet lowercases Key.Code for every
// letter, so dispatch matches keyText (shift+letter arrives as Code 'a'
// with Text "A"). The uppercase-Code branch covers test seams that build a
// bare Key{Code:'A'} with no text. Plain lowercase letters fall through to
// the composer.

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// handleReceiptKey dispatches the advertised action keys of the live
// terminal receipt or design-lane card. Returns handled=false when no such
// surface owns input, so the main switch keeps processing. Callers gate on
// noBlockingModal() and the released run; the handoff's own [M]/[X]/[O]/[D]
// keys are checked by updateModel BEFORE this handler and take precedence.
func (m model) handleReceiptKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	var key string
	if text := keyText(msg); text != "" {
		if strings.ToUpper(text) != text {
			return false, m, nil // plain letter: belongs to the composer
		}
		key = strings.ToLower(text)
	} else if code := keyCode(msg); unicode.IsUpper(code) {
		key = string(unicode.ToLower(code))
	} else {
		return false, m, nil
	}

	// Design-lane cards first: a pending plan/critique card's [A]/[R]/[F]
	// belong to the plan while it is pending, even after a prior failed run.
	if m.pendingPlan != nil {
		return m.dispatchPlanCardKey(key)
	}
	if m.lastTerminalReceipt == "" {
		return false, m, nil
	}
	return m.dispatchTerminalReceiptKey(key)
}

// dispatchPlanCardKey handles the implementation-plan and critique cards'
// action rows (lifecycle_cards.go): [A] approve, [R] revise, [F] fold
// required fixes. startApproval already refuses loudly when the critique
// still carries must-fix issues (DoD 8) — [A] on a blocked card lands on
// that refusal rather than starting a run.
func (m model) dispatchPlanCardKey(key string) (bool, tea.Model, tea.Cmd) {
	switch key {
	case "a":
		next, cmd := m.handleApproveCommand()
		return true, next, cmd
	case "r":
		m.input.SetValue("Revise the plan: ")
		return true, m, nil
	case "f":
		m.input.SetValue("Fold required fixes: ")
		return true, m, nil
	}
	return false, m, nil
}

// dispatchTerminalReceiptKey handles the CANCELLED and FAILED receipt
// cards' action rows (receipt_card.go). The [A]/[D] worktree actions reuse
// the review's tested runtime seams (applyWorktreeReview) via a background
// command, exactly as the handoff keys do.
func (m model) dispatchTerminalReceiptKey(key string) (bool, tea.Model, tea.Cmd) {
	switch m.lastTerminalReceipt {
	case receiptCancelled:
		switch key {
		case "a":
			return m.receiptWorktreeAction(worktreeReviewAccept, "receipt apply staged")
		case "d":
			return m.receiptWorktreeAction(worktreeReviewReject, "receipt discard")
		case "r":
			return m.receiptResume()
		}
	case receiptFailed:
		switch key {
		case "r":
			return m.receiptResume()
		case "i":
			text := ""
			m, text = m.handleSelfCorrectCommand("full")
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
			return true, m, nil
		case "l":
			return true, m.openDetailView(), nil
		}
	}
	return false, m, nil
}

// receiptWorktreeAction runs the worktree merge-back (accept) or
// preserve-then-remove (reject) seam off the UI loop, mirroring the
// handoff's dispatch shape. When the failed/cancelled lane left no
// worktree, the key says so instead of silently doing nothing.
func (m model) receiptWorktreeAction(decision string, reason string) (bool, tea.Model, tea.Cmd) {
	if m.activeWorktree == nil || strings.TrimSpace(m.activeWorktree.Path) == "" {
		return true, m.appendSystemNotice("No staged work to act on — the lane left no worktree."), nil
	}
	msg := applyWorktreeReview(*m.activeWorktree, decision, false, reason)
	next := m
	next.transcript = appendTranscriptRow(next.transcript, transcriptRow{
		kind: rowSystem,
		text: "Receipt action queued: " + reason + " on lane " + m.activeWorktree.Name,
	})
	return true, next, tea.Batch(func() tea.Msg { return msg })
}

// receiptResume opens the session picker (the same surface bare /resume
// opens). The picker falls back to the text list when there is nothing to
// resume, so the key never dead-ends.
func (m model) receiptResume() (bool, tea.Model, tea.Cmd) {
	if next, ok := m.openSessionPicker(); ok {
		return true, next, nil
	}
	text := ""
	m, text = m.handleResumeCommand("")
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	return true, m, nil
}
