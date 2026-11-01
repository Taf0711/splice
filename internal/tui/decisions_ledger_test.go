package tui

// decisions_ledger_test.go (P3 GAP-L rest, DoD 46): the pinned-decisions
// ledger is WIRED — the rehydrate path projects the reconstructed ledger as
// a card on resume, and a design turn that pins decisions refreshes the
// live transcript's ledger card (replacing, not stacking).

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
)

func decisionPinEvent(t *testing.T, statement string, revised bool) sessions.Event {
	t.Helper()
	payload, err := json.Marshal(splicerun.DecisionPinnedPayload{Statement: statement, Revised: revised})
	if err != nil {
		t.Fatal(err)
	}
	return sessions.Event{Type: sessions.EventDecisionPinned, Payload: payload}
}

// Resume projects the whole reconstructed ledger as one card: both pins
// visible, the revised one carrying the [~] REVISED marker, settled count
// right. This is the probe that failed before the rehydrate case existed —
// the ledger card was a dead renderer only tests called.
func TestResumeProjectsDecisionsLedgerCard(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	sess := verifierCreateSession(t, m, ws)
	events := []sessions.Event{
		decisionPinEvent(t, "retry idempotent methods only", false),
		decisionPinEvent(t, "backoff cap 5s", true),
	}
	if _, err := m.sessionStore.AppendEvents(sess.SessionID, eventsToInputs(events)); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.Update(testKeyText("/resume " + sess.SessionID))
	mid := updated.(model)
	updated, _ = mid.Update(testKey(tea.KeyEnter))
	resumed := updated.(model)

	joined := transcriptText(resumed.transcript)
	if !strings.Contains(joined, decisionsCardMarker) {
		t.Fatalf("resume did not project the decisions ledger card:\n%s", joined)
	}
	plain := plainRender(t, resumed.View())
	for _, want := range []string{"DECISIONS", "1 settled", "retry idempotent methods only", "[~] REVISED", "backoff cap 5s"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("ledger card missing %q:\n%s", want, plain)
		}
	}
}

// A design turn whose run goroutine drained decision pins refreshes the
// live transcript: the new ledger card appears and the previous one is
// replaced, not stacked. The model needs a REAL active session so
// appendSessionEvents actually persists the drained pins (that persistence
// is what m.sessionEvents — and therefore the reconstruction — reads).
func TestDesignTurnRefreshesLiveLedgerCard(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	m.activeRunID = 1
	sess := verifierCreateSession(t, m, ws)
	m.activeSession = sess

	// Seed the transcript with a first-generation ledger card (as an earlier
	// turn would have left it) and the matching session event.
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowSystem,
		text: decisionsCardMarker + renderDecisionsCard([]splicerun.DecisionPinnedPayload{{Statement: "cap 30s"}}, 100),
	})
	seed := []pendingSessionEvent{
		{Type: sessions.EventDecisionPinned, Payload: mustJSON(t, splicerun.DecisionPinnedPayload{Statement: "cap 30s", Revised: false})},
	}
	if _, rows := m.appendSessionEvents(seed); len(rows) > 0 {
		t.Fatalf("seed persist failed: %+v", rows)
	}

	msg := agentResponseMsg{
		runID: 1,
		sessionEvents: []pendingSessionEvent{
			{Type: sessions.EventDecisionPinned, Payload: mustJSON(t, splicerun.DecisionPinnedPayload{Statement: "cap 5s", Revised: true})},
		},
	}
	updated, _ := m.Update(msg)
	next := updated.(model)

	cards := 0
	for _, row := range next.transcript {
		if row.kind == rowSystem && strings.HasPrefix(row.text, decisionsCardMarker) {
			cards++
		}
	}
	if cards != 1 {
		t.Fatalf("want exactly 1 ledger card after refresh, got %d", cards)
	}
	plain := plainRender(t, next.View())
	if !strings.Contains(plain, "[~] REVISED") || !strings.Contains(plain, "cap 5s") {
		t.Fatalf("refreshed ledger missing the revised pin:\n%s", plain)
	}
	if strings.Contains(plain, "1 settled") && !strings.Contains(plain, "REVISED") {
		t.Fatalf("stale ledger survived the refresh:\n%s", plain)
	}
}

// No pins, no card: a non-design turn (or one with no decision events) must
// not fabricate an empty ledger section.
func TestNoDecisionPinsNoCard(t *testing.T) {
	m := newDesignModeTestModel(t.TempDir(), &fakeProvider{}, nil)
	m.activeRunID = 1
	before := len(m.transcript)

	msg := agentResponseMsg{runID: 1}
	updated, _ := m.Update(msg)
	next := updated.(model)
	if len(next.transcript) != before {
		t.Fatal("a turn with no decision pins must not append anything for the ledger")
	}
	if got := decisionsCardTranscriptText(nil); got != "" {
		t.Fatalf("empty ledger must render no card, got %q", got)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
