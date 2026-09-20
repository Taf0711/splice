package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// HANDOFF surface (GAP-F, DoD 28, §6.8, P5 cell F2). A lane exiting with
// surviving work exposes a resumable handoff: worktree kept, branch named,
// resume path. The UI distinguishes LANE DEATH from WORK LOSS — a dead lane
// with a live worktree is recoverable, and says so.

// handoffState is the normalized handoff for one exited lane. It is a
// projection of the terminal receipt plus the kept worktree result; the
// renderer never invents it.
type handoffState struct {
	lane      string // worktree name (wt-...)
	path      string // worktree path on disk
	branch    string // splice/<lane>
	outcome   string // "completed" | "failed" | "cancelled" | ""
	staged    int    // staged file count (cancelled receipt continuity)
	applied   int    // applied file count
	preserved bool   // work alive: worktree path exists on disk
}

// handoffOutcomeLabel renders the outcome word for the card's outcome line.
// cancelled is NOT failure and carries its staged-not-applied accounting.
func handoffOutcomeLabel(outcome string, staged, applied int) string {
	switch outcome {
	case "cancelled":
		return fmt.Sprintf("outcome: cancelled · %d file(s) staged, not applied", staged)
	case "failed":
		return "outcome: failed · worktree state preserved for inspection"
	case "completed":
		return fmt.Sprintf("outcome: completed · %d file(s) changed", staged)
	default:
		return "outcome: unknown"
	}
}

// renderHandoffCard renders the HANDOFF card at the given width. Two forms:
// preserved (work alive: worktree, branch, resume path, actions) and
// WORK LOST (path gone: the git recovery hint). The form is chosen from
// handoff.preserved, which the caller computes from the filesystem.
func renderHandoffCard(handoff handoffState, mergeAvailable bool, width int) string {
	if width <= 0 || handoff.lane == "" {
		return ""
	}

	var header string
	var border lipgloss.Style
	var lines []string
	if handoff.preserved {
		header = zeroTheme.amber.Render(fmt.Sprintf("[~] HANDOFF — lane %s exited, work alive", handoff.lane))
		border = zeroTheme.cardRun
		lines = append(lines, header)
		lines = append(lines,
			"  worktree kept: "+handoff.path,
			"  branch:        "+handoff.branch,
			"  "+zeroTheme.accent.Render("resume -> /resume "+handoff.lane),
			"",
			zeroTheme.faint.Render(handoffOutcomeLabel(handoff.outcome, handoff.staged, handoff.applied)),
		)
		if handoff.outcome == "cancelled" && handoff.staged > 0 {
			lines = append(lines, "  "+zeroTheme.faint.Render("staged work can be applied from the worktree"))
		}
		lines = append(lines, "")
		if mergeAvailable {
			lines = append(lines,
				zeroTheme.muted.Render("[O]")+" "+zeroTheme.ink.Render("open worktree")+"  "+
					zeroTheme.muted.Render("[D]")+" "+zeroTheme.ink.Render("review diff")+"  "+
					zeroTheme.muted.Render("[M]")+" "+zeroTheme.ink.Render("merge back now")+"  "+
					zeroTheme.muted.Render("[X]")+" "+zeroTheme.ink.Render("discard lane"))
		} else {
			lines = append(lines,
				zeroTheme.muted.Render("[O]")+" "+zeroTheme.ink.Render("open worktree")+"  "+
					zeroTheme.muted.Render("[D]")+" "+zeroTheme.ink.Render("review diff")+"  "+
					zeroTheme.faint.Render("[M] merge unavailable: main checkout dirty")+"  "+
					zeroTheme.muted.Render("[X]")+" "+zeroTheme.ink.Render("discard lane"))
		}
	} else {
		// Work lost: lane death and work loss are NOT the same (§6.8).
		header = zeroTheme.red.Render(fmt.Sprintf("[!] HANDOFF — lane %s exited, WORK LOST", handoff.lane))
		border = zeroTheme.cardErr
		lines = append(lines, header)
		lines = append(lines,
			"  worktree "+handoff.path+" no longer exists",
			"  "+zeroTheme.faint.Render("branch "+handoff.branch+" may still exist: git branch --list "+handoff.branch),
		)
	}
	return styledBlock(width, lines, border)
}

// handoffTranscriptMarker tags a system row whose payload is a handoff card
// (same NUL-tag pattern as the receipt cards); the transcript re-renders it
// per width from data.
const handoffTranscriptMarker = "\x00handoff\x00"

// handoffTranscriptPayload serializes the handoff into the row text.
// Fields join with NUL: lane, path, branch, outcome, staged, applied,
// preserved, mergeAvailable.
func handoffTranscriptPayload(h handoffState, mergeAvailable bool) string {
	var b strings.Builder
	b.WriteString(handoffTranscriptMarker)
	b.WriteString(h.lane)
	b.WriteByte(0)
	b.WriteString(h.path)
	b.WriteByte(0)
	b.WriteString(h.branch)
	b.WriteByte(0)
	b.WriteString(h.outcome)
	b.WriteByte(0)
	b.WriteString(fmt.Sprintf("%d", h.staged))
	b.WriteByte(0)
	b.WriteString(fmt.Sprintf("%d", h.applied))
	b.WriteByte(0)
	b.WriteString(fmt.Sprintf("%t", h.preserved))
	b.WriteByte(0)
	b.WriteString(fmt.Sprintf("%t", mergeAvailable))
	return b.String()
}

// parseHandoffTranscriptPayload decodes a handoff payload row.
func parseHandoffTranscriptPayload(text string) (handoffState, bool, bool) {
	if !strings.HasPrefix(text, handoffTranscriptMarker) {
		return handoffState{}, false, false
	}
	parts := strings.Split(strings.TrimPrefix(text, handoffTranscriptMarker), "\x00")
	if len(parts) != 8 {
		return handoffState{}, false, false
	}
	h := handoffState{
		lane:    parts[0],
		path:    parts[1],
		branch:  parts[2],
		outcome: parts[3],
	}
	fmt.Sscanf(parts[4], "%d", &h.staged)
	fmt.Sscanf(parts[5], "%d", &h.applied)
	h.preserved = parts[6] == "true"
	merge := parts[7] == "true"
	return h, merge, true
}
