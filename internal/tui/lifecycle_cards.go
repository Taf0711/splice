package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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

// planCardTranscriptText renders the implementation-plan card body at a
// reference width and returns it tagged for the system-row render path.
// The stored body is BORDER-FREE: the render path draws exactly one border
// at the live width (parseLifecycleCardPayload). Storing a bordered block
// and re-wrapping it drew two nested boxes (the double-border bug).
func planCardTranscriptText(plan schemas.DesignPlan, critique schemas.PlanCritique) string {
	clean := !critique.MustFixBeforeExecution
	return planCardMarker + strings.Join(implementationPlanCardBody(plan, clean, 100), "\n")
}

// critiqueCardTranscriptText renders the typed critique card body tagged for
// the system-row path. Emitted only when required issues block approval.
func critiqueCardTranscriptText(plan schemas.DesignPlan, critique schemas.PlanCritique) string {
	return critiqueCardMarker + strings.Join(critiqueCardBody(plan, critique, 100), "\n")
}

// decisionsCardTranscriptText renders the pinned-decisions ledger card body
// tagged for the system-row path (§7.1, GAP-L DoD 46): decision anchors
// survive resume through decision_pinned session events, and the rehydrate
// path re-projects the whole ledger from the reconstructed state. Empty
// decisions render no card — a ledger section with nothing in it is noise.
func decisionsCardTranscriptText(decisions []splicerun.DecisionPinnedPayload) string {
	if len(decisions) == 0 {
		return ""
	}
	return decisionsCardMarker + strings.Join(decisionsCardBody(decisions, 100), "\n")
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

// designDecisions returns the pinned-decision ledger reconstructed from the
// session's decision_pinned events — the same append-only data the transcript
// ledger card projects. ok is false when reconstruction fails (malformed pins
// fail closed upstream; the renderer just skips) — the sidebar never renders
// a ledger from invented data.
func (m model) designDecisions() ([]splicerun.DecisionPinnedPayload, bool) {
	state, err := splicerun.ReconstructDesignState(m.sessionEvents)
	if err != nil {
		return nil, false
	}
	return state.Decisions, true
}

// designOpenQuestions returns the currently-open question set reconstructed
// from the session's open_question_raised/resolved events — the same
// reconstructed state the resume card projects. ok is false when
// reconstruction fails (the renderer skips, never invents).
func (m model) designOpenQuestions() ([]splicerun.OpenQuestionPayload, bool) {
	state, err := splicerun.ReconstructDesignState(m.sessionEvents)
	if err != nil {
		return nil, false
	}
	return state.OpenQuestions, true
}

// sidebarDecisionsLines renders the sidebar DECISIONS module (P1.4 delta,
// frame gEVp1 S1):
//
//	DECISIONS  2
//	  [+] retry idempotent methods
//	  [~] REVISED  backoff cap 5s
//
// Same glyph grammar as the transcript ledger card: [+] settled, [~] REVISED.
// The mock's "in progress" and open rows need decision states the runtime
// does not emit yet (DecisionPinnedPayload carries only statement/detail/
// revised) — they stay deferred-pending-runtime, never invented here.
func (m model) sidebarDecisionsLines(width int) []string {
	decisions, ok := m.designDecisions()
	if !ok || len(decisions) == 0 {
		return nil
	}
	room := maxInt(4, width-6)
	lines := []string{sidebarHeaderWithCount(
		"DECISIONS",
		fmt.Sprintf("%d", len(decisions)),
		zeroTheme.muted,
		width,
	)}
	for _, d := range decisions {
		if d.Revised {
			lines = append(lines, "  "+zeroTheme.faint.Render("[~] REVISED  "+truncateRunes(d.Statement, room-12)))
			continue
		}
		lines = append(lines, "  "+zeroTheme.green.Render("[+]")+" "+zeroTheme.ink.Render(truncateRunes(d.Statement, room)))
	}
	return lines
}

// sidebarRunLines renders the sidebar RUN module (P1.4 delta, frame gEVp1 S1):
//
//	RUN
//	  elapsed   5m 12s
//	  tokens    18.4K
//	  stage     design · thinking
//
// Present only while a run is live or design mode is active — the module is
// an event-driven projection, not a permanent fixture (frame esBzN: "every
// segment is optional and event-driven"). All three values read existing
// runtime state; nothing is invented.
func (m model) sidebarRunLines(width int) []string {
	if !m.pending && !m.designMode {
		return nil
	}
	lines := []string{sidebarHeader("RUN", width)}
	row := func(label, value string) {
		if value == "" {
			return
		}
		lines = append(lines, "  "+zeroTheme.faint.Render(label+"   ")+" "+zeroTheme.ink.Render(value))
	}
	if m.pending && !m.turnStartedAt.IsZero() {
		row("elapsed", formatRunElapsed(m.now().Sub(m.turnStartedAt)))
	}
	if tokens := m.sidebarTokenText(); tokens != "" {
		row("tokens", tokens)
	}
	row("stage", m.runStageLabel())
	return lines
}

// runStageLabel names the current phase in the frame's "stage" vocabulary:
// design · <activity> while the design turn runs, executing · <activity>
// while a run executes, plain design when idle-but-active. workingActivity
// is the canonical activity wording ("thinking"/"writing"/"running <tool>").
func (m model) runStageLabel() string {
	if m.designMode {
		if m.pending {
			return "design · " + m.workingActivity()
		}
		return "design"
	}
	if m.pending {
		pipeline := m.pipeline.presentation()
		if pipeline.active && pipeline.total > 0 {
			return "executing · " + m.workingActivity()
		}
		return "executing"
	}
	return ""
}

// formatRunElapsed renders elapsed time as the frame does: "5m 12s", "18s".
func formatRunElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %02ds", minutes, seconds)
}

// parseLifecycleCardPayload detects a tagged P4 card row and returns its
// width-aware render function. The stored body is border-free (the render
// path draws exactly one border at the live width). Legacy rows persisted
// before the border-free storage carry a full styledBlock at reference
// width; those are re-split and re-bordered ONCE at the current width —
// never wrapped again on top of their own border (the double-box bug).
func parseLifecycleCardPayload(text string) (func(int) string, bool) {
	strip := func(marker string) string { return strings.TrimPrefix(text, marker) }
	render := func(body string, borderStyle lipgloss.Style) func(int) string {
		lines := viewLines(body)
		if strings.HasPrefix(strings.TrimSpace(body), "╭") {
			// Legacy bordered body: strip the old border cells, keep the
			// content, and let styledBlock draw the single live-width border.
			lines = stripBlockBorder(lines)
		}
		return func(width int) string { return styledBlock(width, lines, borderStyle) }
	}
	switch {
	case strings.HasPrefix(text, planCardMarker):
		return render(strip(planCardMarker), zeroTheme.cardRun), true
	case strings.HasPrefix(text, critiqueCardMarker):
		return render(strip(critiqueCardMarker), zeroTheme.cardErr), true
	case strings.HasPrefix(text, decisionsCardMarker):
		return render(strip(decisionsCardMarker), zeroTheme.cardRun), true
	}
	return nil, false
}

// stripBlockBorder removes the styledBlock frame (╭─╮ / │ · │ / ╰─╯) from a
// legacy stored card, returning the inner content lines. A line that does
// not fit the border shape passes through unchanged (fail-visible, never
// silently dropped content).
func stripBlockBorder(lines []string) []string {
	if len(lines) < 2 {
		return lines
	}
	inner := make([]string, 0, len(lines)-2)
	for _, line := range lines[1 : len(lines)-1] {
		trimmed := strings.TrimRight(ansi.Strip(line), " ")
		if strings.HasPrefix(trimmed, "│ ") && strings.HasSuffix(trimmed, " │") {
			inner = append(inner, strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │"))
			continue
		}
		// Legacy tiny-tier rule rows (───) collapse to blank separators.
		if strings.TrimSpace(ansi.Strip(line)) == "" || isAllBoxRule(ansi.Strip(line)) {
			inner = append(inner, "")
			continue
		}
		inner = append(inner, line)
	}
	return inner
}

// isAllBoxRule reports whether a stripped line is only box-drawing rules.
func isAllBoxRule(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		switch r {
		case '─', '│', '╭', '╮', '╰', '╯', '-', '|':
		default:
			return false
		}
	}
	return true
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

// renderCritiqueCard renders the typed critique card (§7.4, P4 E4):
// the critique body inside exactly one border.
func renderCritiqueCard(plan schemas.DesignPlan, critique schemas.PlanCritique, width int) string {
	if width <= 0 {
		return ""
	}
	if critiqueHasRequired(critique) {
		return styledBlock(width, critiqueCardBody(plan, critique, width), zeroTheme.cardErr)
	}
	return styledBlock(width, critiqueCardBody(plan, critique, width), zeroTheme.cardRun)
}

// critiqueHasRequired reports whether the critique carries must-fix issues.
func critiqueHasRequired(critique schemas.PlanCritique) bool {
	for _, c := range critique.Critiques {
		if critiqueSeverityClass(c.Severity) == "REQUIRED" {
			return true
		}
	}
	return false
}

// critiqueCardBody renders the critique card content WITHOUT a border —
// the border belongs to the render path (one border, at the live width).
func critiqueCardBody(plan schemas.DesignPlan, critique schemas.PlanCritique, width int) []string {
	if width <= 0 {
		return nil
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

	rev := ""
	if critique.OverallAssessment != "" {
		rev = truncateRunes(critique.OverallAssessment, bodyBudget)
	}
	titleLine := zeroTheme.amber.Bold(true).Render("CRITIQUE") + "  " +
		zeroTheme.faint.Render(fmt.Sprintf("%d required · %d advisory", required, advisory))
	lines := []string{titleLine}
	if rev != "" {
		lines = append(lines, "  "+rev)
	}
	lines = append(lines, "")

	for _, c := range critique.Critiques {
		lines = append(lines, critiqueFindingLines(c, bodyBudget)...)
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
	return lines
}

// critiqueFindingLines renders one critique finding: severity-marked header,
// the issue text, and the optional fix — border-free body lines.
func critiqueFindingLines(c schemas.Critique, bodyBudget int) []string {
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
	lines := []string{zeroTheme.ink.Render(head)}
	for _, wrapped := range wrapPlainText(c.Issue, bodyBudget-2) {
		lines = append(lines, "    "+wrapped)
	}
	if fix := strings.TrimSpace(c.SuggestedMitigation); fix != "" {
		for _, wrapped := range wrapPlainText("-> fix: "+fix, bodyBudget-2) {
			lines = append(lines, "    "+zeroTheme.faint.Render(wrapped))
		}
	}
	lines = append(lines, "")
	return lines
}

// renderImplementationPlanCard renders the implementation-plan card
// (§7.3, P4 E3): the plan body inside exactly one border.
func renderImplementationPlanCard(plan schemas.DesignPlan, critiqueClean bool, width int) string {
	if width <= 0 {
		return ""
	}
	return styledBlock(width, implementationPlanCardBody(plan, critiqueClean, width), zeroTheme.cardRun)
}

// implementationPlanCardBody renders the plan card content WITHOUT a border:
// numbered tasks with targets, the acceptance-check count, the critique
// verdict, and the explicit-gesture approve row. The render path owns the
// border (one border, at the live width — never a stored one).
func implementationPlanCardBody(plan schemas.DesignPlan, critiqueClean bool, width int) []string {
	if width <= 0 {
		return nil
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
	return lines
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
// the ledger body inside exactly one border.
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
	return styledBlock(width, decisionsCardBody(decisions, width), zeroTheme.cardRun)
}

// decisionsCardBody renders the ledger content WITHOUT a border — the
// border belongs to the render path (one border, at the live width).
func decisionsCardBody(decisions []splicerun.DecisionPinnedPayload, width int) []string {
	if width <= 0 {
		return nil
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
	return lines
}
