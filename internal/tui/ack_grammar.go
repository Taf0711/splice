package tui

// ack_grammar.go (P15, frame WqP03): the acknowledgement grammar. Twelve
// commands answer with ONE line each — no card, no title row, no status
// field. The line has three slots: a fixed-width VERB column so acks scan
// as a column rather than prose, the OUTCOME, and — when the command is
// blocked — the UNBLOCK: the action that gets the user moving again.
// "A blocked ack names the unblock, never just the block."
//
//	ok      cleared   412 rows removed from the view
//	!       retry     a run is in progress — esc esc to cancel, then retry

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// ackVerbWidth is the fixed width of the verb column. "Fixed width so acks
// scan as a column, not prose" (frame note): the eye lands on the verb at
// the same x across a run of acks.
const ackVerbWidth = 9

// ackKind marks whether the command succeeded or was refused. Blocked acks
// carry the `!` marker and MUST name the unblock; a block without a way
// forward is a dead end rendered as text.
type ackKind int

const (
	ackOK ackKind = iota
	ackBlocked
)

// ack is one acknowledgement. Verb is the single word naming the command's
// action; outcome is what happened; unblock is the escape hatch, REQUIRED
// when blocked is true.
type ack struct {
	verb    string
	outcome string
	blocked bool
	unblock string
}

// renderAckLine formats one ack line: the `!` block marker (reserved on ok
// acks too, so the outcome column lands at the same x across every ack —
// scan-as-a-column), the verb padded to the shared column, then the outcome
// (and unblock).
// renderAckLine renders the verb column + outcome. The SYSTEM row adds the
// "· " prefix and TrimSpaces the text, so leading blanks never survive: the
// blocked ack leads with a real "!" character, and the ok ack pads its verb
// to the blocked width (2 wider: the ! slot) so BOTH outcomes land at the
// same x — the frame's scan-as-a-column rule.
func renderAckLine(a ack) string {
	verb := a.verb
	pad := ackVerbWidth - lipgloss.Width(verb)
	if a.blocked {
		if pad > 0 {
			verb += strings.Repeat(" ", pad)
		}
		return zeroTheme.amber.Render("!") + " " + zeroTheme.ink.Render(verb) + " " +
			zeroTheme.muted.Render(a.outcome+" — "+a.unblock)
	}
	// Ok: pad 2 wider to occupy the ! slot the blocked ack uses.
	if pad+2 > 0 {
		verb += strings.Repeat(" ", pad+2)
	}
	return zeroTheme.ink.Render(verb) + " " + zeroTheme.muted.Render(a.outcome)
}

// ackSystemText wraps a rendered ack line as a system-row transcript entry.
// Acks are the ONE grammar: they never draw a card ("if it needs a card it
// is not an ack").
func ackSystemText(a ack) string {
	return renderAckLine(a)
}

// ackf is a convenience for outcome strings built with fmt.
func ackf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
