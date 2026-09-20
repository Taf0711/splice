package tui

// p15_ack_grammar_test.go (P15, frame WqP03): the acknowledgement grammar.
// Twelve commands answer with ONE line each — fixed-width verb column,
// outcome, and the unblock when blocked. Regression probes: render through
// the REAL Update path, assert the line shape, the unblock rule, and that
// no ack ever draws a card (the NUL card markers must never appear).

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/tools"
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

// /rewind with no session: blocked, the ! marker, and the unblock after the
// em-dash ("a blocked ack names the unblock, never just the block").
func TestAckRewindBlockedNoSession(t *testing.T) {
	m := sizedTestModel(120)
	rows := ackRows(t, m, "/rewind")
	assertAckGrammar(t, rows[0], "rewind", true)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "no active session") {
		t.Fatalf("blocked /rewind ack missing the outcome: %q", plain)
	}
}

// /rewind while a run is in progress: the frame's exact shape —
// `! rewind  a run is in progress — esc esc to cancel, then rewind`.
func TestAckRewindBlockedMidRun(t *testing.T) {
	m := rewindAckModel(t)
	m.pending = true
	rows := ackRows(t, m, "/rewind")
	assertAckGrammar(t, rows[0], "rewind", true)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "a run is in progress") || !strings.Contains(plain, "esc esc to cancel, then rewind") {
		t.Fatalf("blocked /rewind ack missing outcome or unblock: %q", plain)
	}
}

// /rewind while a cancelled run is still flushing: blocked, with the retry
// unblock.
func TestAckRewindBlockedFlushing(t *testing.T) {
	m := rewindAckModel(t)
	m.flushRunIDs = map[int]string{1: "stale"}
	rows := ackRows(t, m, "/rewind")
	assertAckGrammar(t, rows[0], "rewind", true)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "still flushing") || !strings.Contains(plain, "retry in a moment") {
		t.Fatalf("blocked /rewind flush ack missing outcome or unblock: %q", plain)
	}
}

// rewindAckModel builds a model with an active session (one checkpoint event
// logged) so /rewind passes the no-session gate and reaches the busy gates.
func rewindAckModel(t *testing.T) model {
	t.Helper()
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "Rewind blocked", Cwd: t.TempDir(), ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	appendTestEvent(t, store, session.SessionID, sessions.EventSessionCheckpoint, map[string]any{"tool": "write_file", "files": []any{}})

	m := sizedTestModel(120)
	m.sessionStore = store
	m.activeSession = session
	return m
}

// A successful /rewind carries the frame's evidence rule IN the ok ack:
// evidence is invalidated, verification must run again.
func TestAckRewindOKStatesEvidenceInvalidation(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "Rewind ack", Cwd: t.TempDir(), ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	appendTestEvent(t, store, session.SessionID, sessions.EventMessage, map[string]any{"role": "user", "content": "first request"})
	appendTestEvent(t, store, session.SessionID, sessions.EventSessionCheckpoint, map[string]any{"tool": "write_file", "files": []any{}})

	m := newModel(context.Background(), Options{SessionStore: store})
	m.input.SetValue("/resume " + session.SessionID)
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	rows := ackRows(t, m, "/rewind latest")
	assertAckGrammar(t, rows[0], "rewind", false)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "evidence was invalidated") || !strings.Contains(plain, "verification must run again") {
		t.Fatalf("ok /rewind ack missing the evidence-invalidation warning: %q", plain)
	}
}

// /selfcorrect with a bad argument: blocked, and the unblock names the valid
// arguments (usage).
func TestAckSelfCorrectUsageBlocked(t *testing.T) {
	m := sizedTestModel(120)
	rows := ackRows(t, m, "/selfcorrect banana")
	assertAckGrammar(t, rows[0], "selfcorrect", true)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "banana") || !strings.Contains(plain, "/selfcorrect [status|on|off|tests|full|lsp]") {
		t.Fatalf("blocked /selfcorrect ack missing arg or usage unblock: %q", plain)
	}
}

// /stop with a bogus session id: blocked usage ack with the unblock.
func TestAckStopBlockedUsage(t *testing.T) {
	m := stopAckModel(t)
	rows := ackRows(t, m, "/stop abc")
	assertAckGrammar(t, rows[0], "stop", true)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "invalid session id: abc") || !strings.Contains(plain, "usage: /stop [session_id]") {
		t.Fatalf("blocked /stop ack missing outcome or usage unblock: %q", plain)
	}
}

// /stop with nothing running: ok ack, no card, no ! marker.
func TestAckStopOKNothingRunning(t *testing.T) {
	m := stopAckModel(t)
	rows := ackRows(t, m, "/stop")
	assertAckGrammar(t, rows[0], "stop", false)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "no background terminals running") {
		t.Fatalf("ok /stop ack missing the nothing-running outcome: %q", plain)
	}
}

// stopAckModel registers the fake exec-session tool so /stop reaches its own
// gates (without a controller the ack is the unavailable refusal).
func stopAckModel(t *testing.T) model {
	t.Helper()
	m := sizedTestModel(120)
	m.registry = tools.NewRegistry()
	m.registry.Register(&fakeExecSessionTool{})
	return m
}

// /image with a missing file: blocked ack naming the file, with the unblock.
func TestAckImageBlockedMissingFile(t *testing.T) {
	root := t.TempDir()
	m := sizedTestModel(120)
	m.cwd = root
	m.modelName = "gpt-4.1" // a vision model, so the refusal is the file, not the gate
	rows := ackRows(t, m, "/image nope.png")
	assertAckGrammar(t, rows[0], "image", true)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "nope.png") {
		t.Fatalf("blocked /image ack must name the missing file: %q", plain)
	}
}

// /image clear: ok ack.
func TestAckImageClearOK(t *testing.T) {
	m := sizedTestModel(120)
	rows := ackRows(t, m, "/image clear")
	assertAckGrammar(t, rows[0], "image", false)
	plain := ansi.Strip(rows[0].text)
	if !strings.Contains(plain, "cleared pending attachments") {
		t.Fatalf("ok /image clear ack missing the outcome: %q", plain)
	}
}
