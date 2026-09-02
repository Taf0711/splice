package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// P4 lifecycle cards (GAP-E): the implementation-plan card (cell E3), the
// typed critique card (cell E4), the crystallizing card (cell E2), and the
// design-decisions ledger (cell E1). Contract §7.1–§7.4: decisions are
// first-class runtime data, approval is an explicit gesture, required
// critique issues block approval, findings carry reason/fix.

// critiqueCardMarker / planCardMarker tag system rows that carry the P4
// cards. The tags let the renderer route them to the card renderers while
// /export strips them (same NUL-tag pattern as plan-card rows).
const (
	critiqueCardMarker  = "\x00critique-card\x00"
	planCardMarker      = "\x00impl-plan-card\x00"
	decisionsCardMarker = "\x00decisions-card\x00"
)

// planCardTranscriptText renders the implementation-plan card at a reference
// width and returns it tagged for the system-row render path.
func planCardTranscriptText(plan schemas.DesignPlan, critique schemas.PlanCritique) string {
	clean := !critique.MustFixBeforeExecution
	return planCardMarker + renderImplementationPlanCard(plan, clean, 100)
}

// critiqueCardTranscriptText renders the typed critique card tagged for the
// system-row path. Emitted only when required issues block approval.
func critiqueCardTranscriptText(plan schemas.DesignPlan, critique schemas.PlanCritique) string {
	return critiqueCardMarker + renderCritiqueCard(plan, critique, 100)
}

// decisionsCardTranscriptText renders the pinned-decisions ledger card
// tagged for the system-row path (§7.1, GAP-L DoD 46): decision anchors
// survive resume through decision_pinned session events, and the rehydrate
// path re-projects the whole ledger from the reconstructed state. Empty
// decisions render no card — a ledger section with nothing in it is noise.
func decisionsCardTranscriptText(decisions []splicerun.DecisionPinnedPayload) string {
	if len(decisions) == 0 {
		return ""
	}
	return decisionsCardMarker + renderDecisionsCard(decisions, 100)
}

// refreshDecisionsLedgerCard replaces the transcript's current decisions
// ledger card with a fresh projection of the reconstructed ledger. The
// ledger is append-only runtime data (§7.1): each refresh re-renders the
// WHOLE ledger — settled count, [+]' rows, [~] REVISED markers — so the
// transcript's latest card always matches the event log. When the ledger
// reconstructs empty (no pins yet, or the session predates the feature) no
// card is written.
func (m model) refreshDecisionsLedgerCard() model {
	state, err := splicerun.ReconstructDesignState(m.sessionEvents)
	if err != nil {
		// Malformed pins fail closed upstream (G2); the renderer just skips.
		return m
	}
	card := decisionsCardTranscriptText(state.Decisions)
	if card == "" {
		return m
	}
	// Drop any previous ledger card: it is a projection of the same
	// append-only data at an earlier point, and a stack of them would lie
	// about which ledger is current. Checkpoints and rewind are unaffected
	// (they snapshot the whole transcript, not the card).
	for i := len(m.transcript) - 1; i >= 0; i-- {
		row := m.transcript[i]
		if row.kind == rowSystem && strings.HasPrefix(row.text, decisionsCardMarker) {
			m.transcript = append(m.transcript[:i], m.transcript[i+1:]...)
			// The replaced row was below the flush frontier only if already
			// settled; keep the frontier honest by shrinking it when the
			// removed row was settled.
			if i < m.flushed {
				m.flushed--
			}
			break
		}
	}
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, text: card})
	return m
}

// parseLifecycleCardPayload detects a tagged P4 card row and returns its
// width-aware render function. The stored card body was produced at a
// reference width; re-splitting its lines through styledBlock reflows the
// borders at the current width while preserving the content.
func parseLifecycleCardPayload(text string) (func(int) string, bool) {
	strip := func(marker string) string { return strings.TrimPrefix(text, marker) }
	switch {
	case strings.HasPrefix(text, planCardMarker):
		body := strip(planCardMarker)
		return func(width int) string { return styledBlock(width, viewLines(body), zeroTheme.cardRun) }, true
	case strings.HasPrefix(text, critiqueCardMarker):
		body := strip(critiqueCardMarker)
		return func(width int) string { return styledBlock(width, viewLines(body), zeroTheme.cardErr) }, true
	case strings.HasPrefix(text, decisionsCardMarker):
		body := strip(decisionsCardMarker)
		return func(width int) string { return styledBlock(width, viewLines(body), zeroTheme.cardRun) }, true
	}
	return nil, false
}

// critiqueSeverityClass maps a critique severity onto the card's two
// display classes: REQUIRED (blocks approval) or ADVISORY (does not).
// The split mirrors PlanCritique's own invariant: high/critical must-fix,
// everything else advisory.
func critiqueSeverityClass(severity schemas.Severity) string {
	switch severity {
	case schemas.SeverityHigh, schemas.SeverityCritical:
		return "REQUIRED"
	default:
		return "ADVISORY"
	}
}

// renderCritiqueCard renders the typed critique card (§7.4, P4 E4).
func renderCritiqueCard(plan schemas.DesignPlan, critique schemas.PlanCritique, width int) string {
	if width <= 0 {
		return ""
	}
	bodyBudget := width - 6
	if bodyBudget < 12 {
		bodyBudget = 12
	}

	required, advisory := 0, 0
	for _, c := range critique.Critiques {
		if critiqueSeverityClass(c.Severity) == "REQUIRED" {
			required++
		} else {
			advisory++
		}
	}

	var border lipgloss.Style
	if required > 0 {
		border = zeroTheme.cardErr
	} else {
		border = zeroTheme.cardRun
	}

	rev := ""
	if critique.OverallAssessment != "" {
		rev = truncateRunes(critique.OverallAssessment, bodyBudget)
	}
	titleLine := zeroTheme.amber.Bold(true).Render("CRITIQUE")
	count := zeroTheme.faint.Render(fmt.Sprintf("%d required · %d advisory", required, advisory))
	lines := []string{titleLine + "  " + count}
	if rev != "" {
		lines = append(lines, "  "+rev)
	}
	lines = append(lines, "")

	for _, c := range critique.Critiques {
		class := critiqueSeverityClass(c.Severity)
		var glyph, markerStyle string
		if class == "REQUIRED" {
			glyph = presentation.StatusMarker(presentation.NodeStatusFailed, presentation.GlyphTierASCII).Glyph
			markerStyle = "REQUIRED"
		} else {
			glyph = presentation.StatusMarker(presentation.NodeStatusDegraded, presentation.GlyphTierASCII).Glyph
			markerStyle = "ADVISORY"
		}
		head := glyph + " " + markerStyle + "  " + c.Category
		lines = append(lines, zeroTheme.ink.Render(head))
		for _, wrapped := range wrapPlainText(c.Issue, bodyBudget-2) {
			lines = append(lines, "    "+wrapped)
		}
		if fix := strings.TrimSpace(c.SuggestedMitigation); fix != "" {
			for _, wrapped := range wrapPlainText("-> fix: "+fix, bodyBudget-2) {
				lines = append(lines, "    "+zeroTheme.faint.Render(wrapped))
			}
		}
		lines = append(lines, "")
	}

	// Verdict line: the approval gate is runtime truth (DoD 8), not style.
	if required > 0 {
		lines = append(lines, zeroTheme.red.Render("approval  BLOCKED by required issues"))
		lines = append(lines, "")
		lines = append(lines,
			zeroTheme.muted.Render("[F]")+" "+zeroTheme.ink.Render("fold required fixes")+"  "+
				zeroTheme.muted.Render("[R]")+" "+zeroTheme.ink.Render("revise"))
	} else {
		lines = append(lines, zeroTheme.green.Render("critique clean — ready to approve"))
		lines = append(lines, "")
		lines = append(lines,
			zeroTheme.muted.Render("[A]")+" "+zeroTheme.ink.Render("approve")+"  "+
				zeroTheme.muted.Render("[R]")+" "+zeroTheme.ink.Render("revise"))
	}
	return styledBlock(width, lines, border)
}

// renderImplementationPlanCard renders the implementation-plan card
// (§7.3, P4 E3): numbered tasks with targets, the acceptance-check count,
// the critique verdict, and the explicit-gesture approve row.
func renderImplementationPlanCard(plan schemas.DesignPlan, critiqueClean bool, width int) string {
	if width <= 0 {
		return ""
	}
	bodyBudget := width - 6
	if bodyBudget < 12 {
		bodyBudget = 12
	}
	checks := 0
	for _, task := range plan.Tasks {
		checks += len(task.AcceptanceFacts)
	}

	lines := []string{
		zeroTheme.amber.Bold(true).Render("IMPLEMENTATION PLAN") + "  " +
			zeroTheme.faint.Render(fmt.Sprintf("%d tasks · %d acceptance checks", len(plan.Tasks), checks)),
	}
	if critiqueClean {
		lines = append(lines, zeroTheme.green.Render("critique clean"))
	} else {
		lines = append(lines, zeroTheme.red.Render("critique: required issues outstanding"))
	}
	lines = append(lines, "")
	for i, task := range plan.Tasks {
		target := ""
		if len(task.TargetPaths) > 0 {
			target = "  " + zeroTheme.faint.Render(task.TargetPaths[0])
		}
		lines = append(lines, fmt.Sprintf("%02d", i+1)+"  "+
			zeroTheme.ink.Render(truncateRunes(task.Title, bodyBudget-8))+target)
	}
	lines = append(lines, "")
	lines = append(lines,
		zeroTheme.muted.Render("[A]")+" "+zeroTheme.ink.Render("approve (explicit)")+"  "+
			zeroTheme.muted.Render("[R]")+" "+zeroTheme.ink.Render("revise"))
	return styledBlock(width, lines, zeroTheme.cardRun)
}

// renderCrystallizingCard renders the in-progress card (§7.2, P4 E2):
// the five stage markers plus the mandatory "not a contract yet" line.
// Approval is unavailable in this state (DoD 7).
func renderCrystallizingCard(settled, scope bool, drafting bool, taskCount int, width int) string {
	if width <= 0 {
		return ""
	}
	bodyBudget := width - 6
	if bodyBudget < 12 {
		bodyBudget = 12
	}
	marker := func(done bool) string {
		if done {
			return presentation.StatusMarker(presentation.NodeStatusComplete, presentation.GlyphTierASCII).Glyph
		}
		return presentation.StatusMarker(presentation.NodeStatusPending, presentation.GlyphTierASCII).Glyph
	}
	draftMark := marker(false)
	if drafting {
		draftMark = presentation.StatusMarker(presentation.NodeStatusRunning, presentation.GlyphTierASCII).Glyph
	}
	lines := []string{
		zeroTheme.amber.Bold(true).Render("CRYSTALLIZING"),
		"",
		"  " + marker(settled) + " settled",
		"  " + marker(scope) + " scope",
		"  " + draftMark + " drafting",
		"  " + marker(false) + " critique",
		"  " + marker(false) + " acceptance",
		"",
		zeroTheme.amber.Render("not a contract yet"),
		"",
		zeroTheme.faint.Render(fmt.Sprintf("drafting %d tasks from settled decisions", taskCount)),
	}
	return styledBlock(width, lines, zeroTheme.cardRun)
}

// renderDecisionsCard renders the design-decisions ledger (§7.1, P4 E1):
//
//	DECISIONS                3 settled
//	  [+] retry idempotent methods only
//	  [~] REVISED  backoff cap 5s (was: cap 30s)
//
// Decisions are first-class runtime data projected from
// DesignState.Decisions. A revised decision renders with the revision
// marker; history is never silently rewritten.
func renderDecisionsCard(decisions []splicerun.DecisionPinnedPayload, width int) string {
	if width <= 0 {
		return ""
	}
	bodyBudget := width - 6
	if bodyBudget < 12 {
		bodyBudget = 12
	}
	settled := 0
	for _, d := range decisions {
		if !d.Revised {
			settled++
		}
	}
	lines := []string{
		zeroTheme.amber.Bold(true).Render("DECISIONS") + "  " +
			zeroTheme.faint.Render(fmt.Sprintf("%d settled", settled)),
	}
	for _, d := range decisions {
		if d.Revised {
			lines = append(lines, "  "+zeroTheme.faint.Render("[~] REVISED  "+truncateRunes(d.Statement, bodyBudget-14)))
			continue
		}
		lines = append(lines, "  "+zeroTheme.green.Render("[+]")+" "+zeroTheme.ink.Render(truncateRunes(d.Statement, bodyBudget-6)))
		if detail := strings.TrimSpace(d.Detail); detail != "" {
			for _, wrapped := range wrapPlainText(detail, bodyBudget-8) {
				lines = append(lines, "      "+zeroTheme.faint.Render(wrapped))
			}
		}
	}
	if len(decisions) == 0 {
		lines = append(lines, zeroTheme.faint.Render("no decisions pinned yet"))
	}
	return styledBlock(width, lines, zeroTheme.cardRun)
}
