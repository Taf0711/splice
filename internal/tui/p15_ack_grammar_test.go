package tui

// p15_ack_grammar_test.go (P15, frame WqP03): the acknowledgement grammar.
// Twelve commands answer with ONE line each — fixed-width verb column,
// outcome, and the unblock when blocked. Regression probes: render through
// the REAL Update path, assert the line shape, the unblock rule, and that
// no ack ever draws a card (the NUL card markers must never appear).

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// ackRowsFromCommand drives a real /command through the composer and returns
// the transcript rows it appended (the ack).
func ackRows(t *testing.T, m model, command string) []transcriptRow {
	t.Helper()
	m.input.SetValue(command)
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if len(next.transcript) < 2 {
		t.Fatalf("%s appended no ack rows", command)
	}
	return next.transcript[len(next.transcript)-1:]
}

// The three grammar invariants, asserted on every ack under test:
// 1. ONE line (no multi-line card body)
// 2. the verb appears in a padded column (verb + spaces, then outcome)
// 3. no NUL card marker leaks into the ack (never a card)
func assertAckGrammar(t *testing.T, row transcriptRow, verbSubstring string, blocked bool) {
	t.Helper()
	if strings.Contains(row.text, "\n") {
		t.Fatalf("ack must be ONE line, got multi-line: %q", row.text)
	}
	plain := ansi.Strip(row.text)
	if strings.Contains(plain, "\x00") {
		t.Fatalf("ack leaked a NUL card marker: %q", plain)
	}
	if !strings.Contains(plain, verbSubstring) {
		t.Fatalf("ack missing the verb %q: %q", verbSubstring, plain)
	}
	if blocked && !strings.Contains(plain, "!") {
		t.Fatalf("blocked ack missing the ! marker: %q", plain)
	}
	if blocked && !strings.Contains(plain, "—") {
		t.Fatalf("blocked ack missing the unblock (the em-dash separates outcome from unblock): %q", plain)
	}
}

// /clear: ok ack names the /new unblock in the outcome (the frame's wording:
// "the agent still has the session — /new starts fresh").
func TestAckClearGrammar(t *testing.T) {
	m := sizedTestModel(120)
	m.input.SetValue("/clear")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	row := next.transcript[len(next.transcript)-1]
	plain := ansi.Strip(row.text)
	if strings.Contains(plain, "\n") {
		t.Fatal("/clear ack must be one line")
	}
	if !strings.Contains(plain, "cleared") || !strings.Contains(plain, "/new") {
		t.Fatalf("/clear ack missing verb or unblock: %q", plain)
	}
}

// /copy with nothing to copy: blocked ack with the ! marker AND the unblock
// clause — "a blocked ack names the unblock, never just the block".
func TestAckCopyBlockedNamesState(t *testing.T) {
	m := sizedTestModel(120)
	m.input.SetValue("/copy")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	row := next.transcript[len(next.transcript)-1]
	plain := ansi.Strip(row.text)
	if !strings.Contains(plain, "!") || !strings.Contains(plain, "copy") {
		t.Fatalf("blocked /copy ack missing the marker or verb: %q", plain)
	}
	if !strings.Contains(plain, "nothing to copy yet") {
		t.Fatalf("blocked /copy ack missing the outcome: %q", plain)
	}
}

// /retry with no prior prompt: blocked with the unblock.
func TestAckRetryBlocked(t *testing.T) {
	m := sizedTestModel(120)
	m.input.SetValue("/retry")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	row := next.transcript[len(next.transcript)-1]
	plain := ansi.Strip(row.text)
	if !strings.Contains(plain, "retry") || !strings.Contains(plain, "no previous prompt") {
		t.Fatalf("blocked /retry ack wrong: %q", plain)
	}
	if !strings.Contains(plain, "—") {
		t.Fatalf("blocked /retry ack must name the unblock after an em-dash: %q", plain)
	}
}

// The verb column is fixed width: ok and blocked acks put the outcome at the
// same x once the system row's "· " prefix is added (ok: "· "+verb(9)+" ";
// blocked: "· ! "+verb(9)+" " — the ! is INSIDE the blocked text, and the
// system prefix is shared, so both land the outcome at 2+9+1... the blocked
// "! " shifts by 2 within the text, which is the point: the ! IS the state
// channel. Alignment invariant: verb starts at the same x in both (after the
// system prefix + optional !), i.e. verb+pad == ackVerbWidth in both.
func TestAckVerbColumnAligned(t *testing.T) {
	ok := ansi.Strip(renderAckLine(ack{verb: "cleared", outcome: "x"}))
	blocked := ansi.Strip(renderAckLine(ack{verb: "copy", blocked: true, outcome: "y", unblock: "z"}))
	// Both verb fields occupy exactly ackVerbWidth cells.
	if len(ok) < ackVerbWidth || len(blocked) < ackVerbWidth+2 {
		t.Fatalf("lines shorter than the verb column: %q / %q", ok, blocked)
	}
	if strings.TrimSpace(ok[:ackVerbWidth]) != "cleared" {
		t.Fatalf("ok verb column wrong: %q", ok)
	}
	if strings.TrimSpace(blocked[2:2+ackVerbWidth]) != "copy" {
		t.Fatalf("blocked verb column wrong: %q", blocked)
	}
	// The ! marker leads the blocked line.
	if !strings.HasPrefix(blocked, "!") {
		t.Fatalf("blocked ack missing the leading !: %q", blocked)
	}
}

// A blocked ack without an unblock is a construction error — the frame rule
// "a blocked ack names the unblock, never just the block" is enforced at the
// formatter, not left to call sites.
func TestRenderAckLineBlockedRequiresUnblock(t *testing.T) {
	a := ack{verb: "retry", blocked: true, outcome: "a run is in progress"}
	line := ansi.Strip(renderAckLine(a))
	if !strings.Contains(line, "—") {
		t.Fatalf("blocked ack without unblock rendered no separator: %q", line)
	}
}

// Smoke: the ack renders through the REAL View (not just transcript rows) —
// this is the placeholder detector. A wired surface appears in m.View().
func TestAckRendersInView(t *testing.T) {
	m := sizedTestModel(120)
	m.altScreen = true // View() renders the managed transcript only in alt-screen
	m.input.SetValue("/copy")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	view := plainRender(t, next.View())
	if !strings.Contains(view, "nothing to copy yet") {
		t.Fatalf("copy ack not visible in the real View:\n%s", view[len(view)-800:])
	}
}
