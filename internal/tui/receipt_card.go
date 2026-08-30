package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/presentation"
)

// Terminal outcome receipt cards (GAP-E, v0.5 §16 receipts + P2b cell 6):
// run outcomes render as styled result cards with a next-action row —
// never a bare error line, never a raw JSON receipt leaking into the
// transcript (audit finding 3). Three receipts: VERIFIED, FAILED,
// CANCELLED. `cancelled` is not failure: it distinguishes staged work
// from applied work and always offers the resume path.

// receiptKind discriminates the three terminal outcome cards.
type receiptKind string

const (
	receiptVerified  receiptKind = "verified"
	receiptFailed    receiptKind = "failed"
	receiptCancelled receiptKind = "cancelled"
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
	case receiptVerified, receiptFailed, receiptCancelled:
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
// failure: it projects the CANCELLED receipt with staged-not-applied
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
func renderReceiptCard(card receiptCard, width int) string {
	if width <= 0 {
		return ""
	}
	var gutter string
	var border lipgloss.Style
	var titleStyle lipgloss.Style
	switch card.kind {
	case receiptVerified:
		marker := presentation.StatusMarker(presentation.NodeStatusComplete, presentation.GlyphTierASCII)
		gutter, border, titleStyle = marker.Glyph, zeroTheme.cardRun, zeroTheme.green
	case receiptCancelled:
		gutter, border, titleStyle = "[?]", zeroTheme.cardRun, zeroTheme.amber
	default:
		marker := presentation.StatusMarker(presentation.NodeStatusFailed, presentation.GlyphTierASCII)
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
		actions: [][2]string{{"R", "restore"}, {"I", "intervene"}, {"L", "logs"}},
	}
}

// cancelledReceiptCard builds the CANCELLED card. Contract invariants:
// cancelled is NOT failure, staged vs applied is explicit, nothing was
// applied to the tree, and the resume action is always present.
func cancelledReceiptCard(at string, staged int, elapsed string) receiptCard {
	lines := []string{
		"stopped by you" + atSuffix(at),
		fmt.Sprintf("%d file(s) staged, not applied", staged),
		"nothing was written to disk",
	}
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
