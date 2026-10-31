package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/worktrees"
)

// Terminal outcome receipt cards (GAP-E, v0.5 §16 receipts + P2b cell 6):
// run outcomes render as styled result cards with a next-action row —
// never a bare error line, never a raw JSON receipt leaking into the
// transcript (audit finding 3). Four receipts: VERIFIED, UNVERIFIED (the run
// completed but no check verified it), FAILED, CANCELLED. `cancelled` is not
// failure: it distinguishes staged work from applied work and always offers
// the resume path.

// receiptKind discriminates the terminal outcome cards.
type receiptKind string

const (
	receiptVerified   receiptKind = "verified"
	receiptUnverified receiptKind = "unverified"
	receiptFailed     receiptKind = "failed"
	receiptCancelled  receiptKind = "cancelled"
)

// receiptTranscriptMarker prefixes an error row whose payload is a receipt
// card (same NUL-tag pattern as the plan/command cards). The payload is
// data, not pre-rendered ANSI, so the row re-renders per width.
const receiptTranscriptMarker = "\x00receipt\x00"

// receiptTranscriptPayload serializes the card into the transcript row
// text. Fields join with NUL; list items with unit separators.
func receiptTranscriptPayload(card receiptCard) string {
	var b strings.Builder
	b.WriteString(receiptTranscriptMarker)
	b.WriteString(string(card.kind))
	b.WriteByte(0)
	b.WriteString(card.title)
	b.WriteByte(0)
	b.WriteString(card.elapsed)
	b.WriteByte(0)
	b.WriteString(strings.Join(card.lines, "\x1f"))
	b.WriteByte(0)
	for i, action := range card.actions {
		if i > 0 {
			b.WriteString("\x1f")
		}
		b.WriteString(action[0] + ":" + action[1])
	}
	return b.String()
}

// parseReceiptTranscriptPayload decodes a receipt payload row.
func parseReceiptTranscriptPayload(text string) (receiptCard, bool) {
	if !strings.HasPrefix(text, receiptTranscriptMarker) {
		return receiptCard{}, false
	}
	parts := strings.Split(strings.TrimPrefix(text, receiptTranscriptMarker), "\x00")
	if len(parts) != 5 {
		return receiptCard{}, false
	}
	kind := receiptKind(parts[0])
	switch kind {
	case receiptVerified, receiptUnverified, receiptFailed, receiptCancelled:
	default:
		return receiptCard{}, false
	}
	actions := [][2]string{}
	for _, pair := range strings.Split(parts[4], "\x1f") {
		if kv := strings.SplitN(pair, ":", 2); len(kv) == 2 {
			actions = append(actions, [2]string{kv[0], kv[1]})
		}
	}
	return receiptCard{
		kind:    kind,
		title:   parts[1],
		elapsed: parts[2],
		lines:   strings.Split(parts[3], "\x1f"),
		actions: actions,
	}, true
}

// failedExecutionCard classifies a plan-execution error into the correct
// receipt card. A user cancel (context canceled up the chain) is NOT a
// failure: it projects the CANCELLED receipt with the lane's worktree
// semantics. Everything else is FAILED with the full reason.
func failedExecutionCard(err error) receiptCard {
	if err == nil {
		return failedReceiptCard("", "unknown failure", "", "")
	}
	if errors.Is(err, context.Canceled) {
		return cancelledReceiptCard("", 0, "")
	}
	// The error text renders in full inside the card body (wrapped, never
	// truncated); the failing stage is unknown at this boundary, so no
	// stage row.
	return failedReceiptCard("", err.Error(), "", "")
}

// cancelledReceiptForWorktree builds the CANCELLED card from the result
// message's runtime worktree context, so the body states where the work
// actually lives instead of asserting a disk fact the UI never checked.
// The worktree fields are computed in the producing cmd goroutine (F1,
// §14) — no filesystem access happens here.
//
//   - Worktree present: writes landed in the ISOLATED lane; the main
//     checkout is untouched. That is the truth "nothing merged" states.
//   - Worktree absent: the lane ran in the SHARED workspace, where stage
//     tools write directly; claiming "nothing was written to disk" would be
//     false, so the card says edits may already be on disk.
//
// Neither variant asserts a staged file count: the caller has none.
func cancelledReceiptForWorktree(wt *worktrees.Result) receiptCard {
	card := cancelledReceiptCard("", 0, "")
	card.lines = nil
	if wt != nil && strings.TrimSpace(wt.Path) != "" {
		card.lines = append(card.lines,
			"stopped by you",
			"work preserved in worktree "+wt.Name+" — nothing merged into the main checkout",
		)
		return card
	}
	card.lines = append(card.lines,
		"stopped by you",
		"stopped in the shared workspace — edits may already be on disk",
	)
	// No worktree means nothing is staged: apply and discard would only
	// answer "no staged work", and a card never advertises a dead key.
	card.actions = [][2]string{{"R", "resume"}}
	return card
}

// receiptCard is the normalized content of one terminal outcome card.
type receiptCard struct {
	kind    receiptKind
	title   string
	elapsed string
	// lines are the body rows between the title and the action row.
	lines []string
	// actions are the `[k] action` recovery row.
	actions [][2]string
}

// renderReceiptCard renders the card at the given width. The gutter is the
// 3-cell ASCII marker (tier contract); the border color matches the
// outcome. Body lines wrap instead of clipping.
func renderReceiptCard(card receiptCard, width int, tier presentation.GlyphTier) string {
	if width <= 0 {
		return ""
	}
	var gutter string
	var border lipgloss.Style
	var titleStyle lipgloss.Style
	switch card.kind {
	case receiptVerified:
		marker := presentation.StatusMarker(presentation.NodeStatusComplete, tier)
		gutter, border, titleStyle = marker.Glyph, zeroTheme.cardRun, zeroTheme.green
	case receiptCancelled:
		gutter, border, titleStyle = "[?]", zeroTheme.cardRun, zeroTheme.amber
	case receiptUnverified:
		gutter, border, titleStyle = "[!]", zeroTheme.cardRun, zeroTheme.amber
	default:
		marker := presentation.StatusMarker(presentation.NodeStatusFailed, tier)
		gutter, border, titleStyle = marker.Glyph, zeroTheme.cardErr, zeroTheme.red
	}

	title := strings.ToUpper(card.title)
	if card.elapsed != "" {
		title += "  " + card.elapsed
	}

	// Body budget: card width minus the 4 border cells and the 2-cell body
	// indent. Body lines WRAP into that budget (shot-22 fix: a failure
	// reason arrives whole, wrapped, never ellipsis-truncated).
	bodyBudget := width - 6
	if bodyBudget < 12 {
		bodyBudget = 12
	}
	lines := make([]string, 0, len(card.lines)+2)
	lines = append(lines, titleStyle.Render(gutter+" "+truncateRunes(title, bodyBudget)))
	for _, line := range card.lines {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		for _, wrapped := range wrapPlainText(line, bodyBudget) {
			lines = append(lines, "  "+wrapped)
		}
	}
	if actions := renderReceiptActions(card.actions, width); actions != "" {
		lines = append(lines, "")
		lines = append(lines, actions)
	}
	return styledBlock(width, lines, border)
}

// renderReceiptActions renders the `[k] action` row. Each `[k] label`
// segment is an atom: under width pressure later segments drop WHOLE
// (never truncated mid-key, DoD 18); the first action always survives.
func renderReceiptActions(actions [][2]string, width int) string {
	if len(actions) == 0 {
		return ""
	}
	segments := make([]string, 0, len(actions))
	for _, action := range actions {
		segments = append(segments,
			zeroTheme.muted.Render("["+action[0]+"]")+" "+zeroTheme.ink.Render(action[1]))
	}
	kept := 1
	used := lipgloss.Width(segments[0])
	for i := 1; i < len(segments); i++ {
		next := used + 2 + lipgloss.Width(segments[i])
		if width > 0 && next > width {
			break
		}
		used += lipgloss.Width(segments[i])
		kept++
	}
	return strings.Join(segments[:kept], "  ")
}

// verifiedReceiptCard builds the VERIFIED card from run outcome data.
// The contract (DoD 13) requires deterministic evidence in the state
// before this card may render; the caller gates on the evidence summary.
func verifiedReceiptCard(evidence []string, files string, usage string, elapsed string) receiptCard {
	lines := make([]string, 0, len(evidence)+3)
	for _, e := range evidence {
		lines = append(lines, "[+] "+e)
	}
	if files != "" {
		lines = append(lines, "", files)
	}
	if usage != "" {
		lines = append(lines, usage)
	}
	return receiptCard{
		kind:    receiptVerified,
		title:   "VERIFIED",
		elapsed: elapsed,
		lines:   lines,
		actions: [][2]string{{"O", "open diff"}, {"E", "export receipt"}, {"R", "resume"}},
	}
}

// unverifiedReceiptCard builds the UNVERIFIED card for a completed run that
// no check verified. The body names why, so the reader sees which checks did
// not apply. It offers the same keys as VERIFIED: the work is complete, only
// its verification is missing.
func unverifiedReceiptCard(reason string, elapsed string) receiptCard {
	if strings.TrimSpace(reason) == "" {
		reason = "no verification check passed"
	}
	return receiptCard{
		kind:    receiptUnverified,
		title:   "DONE · NOT VERIFIED",
		elapsed: elapsed,
		lines:   []string{reason},
		actions: [][2]string{{"O", "open diff"}, {"E", "export receipt"}, {"R", "resume"}},
	}
}

// unverifiedReceiptFromState builds the UNVERIFIED card from the run's
// completion receipt, which carries the reason. The reason is runtime truth,
// so the card never presents an unverified run as VERIFIED.
func unverifiedReceiptFromState(reason string, startedAt time.Time, now time.Time) receiptCard {
	elapsed := ""
	if !startedAt.IsZero() {
		elapsed = formatWorkingElapsed(now.Sub(startedAt))
	}
	return unverifiedReceiptCard(reason, elapsed)
}

// failedReceiptCard builds the FAILED card: failing stage, the full reason
// (the card body wraps it), preserved-state note, and recovery keys.
func failedReceiptCard(stage string, reason string, snapshot string, elapsed string) receiptCard {
	lines := []string{}
	if stage != "" {
		lines = append(lines, "stage  "+stage)
	}
	if reason != "" {
		lines = append(lines, reason)
	}
	if snapshot != "" {
		lines = append(lines, "", "restorable  "+snapshot)
	}
	return receiptCard{
		kind:    receiptFailed,
		title:   "FAILED",
		elapsed: elapsed,
		lines:   lines,
		// [R] opens the session picker (receiptResume), so it says resume.
		actions: [][2]string{{"R", "resume"}, {"I", "intervene"}, {"L", "logs"}},
	}
}

// cancelledReceiptCard builds the CANCELLED card. Contract invariants:
// cancelled is NOT failure, staged vs applied is explicit, nothing was
// applied to the tree, and the resume action is always present. The staged
// count renders only when the caller knows one: a staged=0 card says
// nothing about files rather than asserting a number it never checked.
func cancelledReceiptCard(at string, staged int, elapsed string) receiptCard {
	lines := []string{
		"stopped by you" + atSuffix(at),
	}
	if staged > 0 {
		lines = append(lines, fmt.Sprintf("%d file(s) staged, not applied", staged))
	}
	lines = append(lines, "nothing was written to disk")
	return receiptCard{
		kind:    receiptCancelled,
		title:   "CANCELLED",
		elapsed: elapsed,
		lines:   lines,
		actions: [][2]string{{"A", "apply staged"}, {"D", "discard"}, {"R", "resume"}},
	}
}

func atSuffix(at string) string {
	if at == "" {
		return ""
	}
	return " at " + at
}

// verifiedReceiptFromResult builds the VERIFIED card from the persisted
// plan result the runtime returned on a completed run. The FinalAnswer is
// the JSON of schemas.DesignPlanResult (design_runner.go), so the evidence
// lines are runtime truth, not invention. A parse failure returns ok=false:
// the caller appends nothing and the JSON answer row still carries the
// data (honest absence — a malformed result never renders as a fake
// success card).
func verifiedReceiptFromResult(finalAnswer string, usage presentation.UsageSummary, startedAt time.Time, now time.Time) (receiptCard, bool) {
	var planResult schemas.DesignPlanResult
	if err := json.Unmarshal([]byte(finalAnswer), &planResult); err != nil {
		return receiptCard{}, false
	}
	if planResult.Status != "completed" {
		return receiptCard{}, false
	}
	evidence := make([]string, 0, 2)
	if planResult.PlanID != "" {
		evidence = append(evidence, "design plan "+planResult.PlanID+" completed")
	}
	if n := len(planResult.CompletedTasks); n > 0 {
		line := fmt.Sprintf("%d task(s) completed", n)
		ids := make([]string, 0, n)
		for _, task := range planResult.CompletedTasks {
			ids = append(ids, task.TaskID)
		}
		if len(ids) <= 4 {
			line += ": " + strings.Join(ids, ", ")
		} else {
			line += ": " + strings.Join(ids[:3], ", ") + ", …"
		}
		evidence = append(evidence, line)
	}
	var usageLine string
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		usageLine = fmt.Sprintf("%s tok in · %s tok out", formatTokenCount(int(usage.InputTokens)), formatTokenCount(int(usage.OutputTokens)))
		if usage.CostUSD > 0 {
			usageLine += fmt.Sprintf(" · $%.2f", usage.CostUSD)
		}
	}
	elapsed := ""
	if !startedAt.IsZero() {
		elapsed = formatWorkingElapsed(now.Sub(startedAt))
	}
	return verifiedReceiptCard(evidence, "", usageLine, elapsed), true
}

// verifiedReceiptCardFor picks the card for a completed run. An unverified
// completion projects the UNVERIFIED card; a verified one projects VERIFIED
// from the persisted result, and ok is false when that result carries no
// evidence to show.
func verifiedReceiptCardFor(completion *presentation.CompletionReceipt, finalAnswer string, usage presentation.UsageSummary, startedAt time.Time, now time.Time) (receiptCard, bool) {
	if completion.Unverified {
		return unverifiedReceiptFromState(completion.Detail, startedAt, now), true
	}
	return verifiedReceiptFromResult(finalAnswer, usage, startedAt, now)
}

// pipelineTurnReceipt builds the terminal receipt card for a finished /exec or
// pipeline turn from the run's result or error. The run goroutine calls it, so
// the card and its persisted payload come from runtime truth, not from the
// presentation state, which can land after the turn's result message. It
// reuses the card builders above. It does no I/O.
func pipelineTurnReceipt(finalAnswer, incompleteReason string, runErr error, worktree *worktrees.Result, started, now time.Time) (receiptCard, bool) {
	elapsed := ""
	if !started.IsZero() {
		elapsed = formatWorkingElapsed(now.Sub(started))
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			card := cancelledReceiptForWorktree(worktree)
			card.elapsed = elapsed
			return card, true
		}
		return failedReceiptCard("", runErr.Error(), "", elapsed), true
	}
	var pr schemas.PipelineResult
	if err := json.Unmarshal([]byte(finalAnswer), &pr); err != nil {
		return failedReceiptCard("", "pipeline result could not be read: "+err.Error(), "", elapsed), true
	}
	// Status mapping matches finishPresentation in internal/splice/run.go:
	// completed projects VERIFIED or NOT VERIFIED, a user abort projects
	// CANCELLED, and every other status projects FAILED with its reason.
	if pr.Status == "completed" {
		if !pr.Verified {
			return unverifiedReceiptFromState(pr.UnverifiedReason, started, now), true
		}
		return verifiedReceiptCard(pipelineEvidence(pr), "", "", elapsed), true
	}
	if pr.Status == "aborted" && pr.UserAborted {
		card := cancelledReceiptForWorktree(worktree)
		card.elapsed = elapsed
		return card, true
	}
	reason := incompleteReason
	if pr.AbortReason != nil && strings.TrimSpace(*pr.AbortReason) != "" {
		reason = *pr.AbortReason
	}
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("the pipeline run ended with status %q and no recorded reason", pr.Status)
	}
	return failedReceiptCard("", reason, "", elapsed), true
}

// pipelineEvidence lists the checks a verified pipeline result records: the
// tests and acceptance facts that passed in the final state, and the test
// command that ran. A result that records none says so, so a VERIFIED card
// never shows an empty body.
func pipelineEvidence(pr schemas.PipelineResult) []string {
	var evidence []string
	if state := pr.FinalState; state != nil {
		if state.TestsPassing > 0 {
			evidence = append(evidence, fmt.Sprintf("%d test(s) passed", state.TestsPassing))
		}
		if state.AcceptanceFactsPassing > 0 {
			evidence = append(evidence, fmt.Sprintf("%d acceptance fact(s) passed", state.AcceptanceFactsPassing))
		}
	}
	for i := len(pr.Stages) - 1; i >= 0; i-- {
		if command := strings.TrimSpace(pr.Stages[i].TestCommand); command != "" && pr.Stages[i].Status == schemas.StageCompleted {
			evidence = append(evidence, "test command: "+command)
			break
		}
	}
	if len(evidence) == 0 {
		evidence = append(evidence, "the pipeline result records no check detail")
	}
	return evidence
}

// appendTurnReceipt adds a pipeline turn's receipt card to the transcript and
// arms its action keys. row.text is the card payload. The other row fields
// (hint, turn tools, turn elapsed) stay on the row, so a FAILED card keeps the
// next step and the done-line metadata of the error row it replaces.
func (m model) appendTurnReceipt(row transcriptRow) model {
	if card, ok := parseReceiptTranscriptPayload(row.text); ok {
		m.lastTerminalReceipt = card.kind
	}
	row.kind = rowError
	m.transcript = appendTranscriptRow(m.transcript, row)
	return m
}
