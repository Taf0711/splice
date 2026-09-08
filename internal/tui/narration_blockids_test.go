package tui

// narration_blockids_test.go (P3 GAP-L rest, DoD 43/45): stable narration
// block identity ACROSS resume. The live path mints reasoning_N ids and now
// persists the ordinal in the session event payload; rehydration must
// rebuild the SAME ids, not empty ones. Probes walk the real /resume path.

import (
	"encoding/json"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/sessions"
)

// resumeReasoningFixture persists a session whose events carry two reasoning
// payloads with explicit seqs (the new format the live path writes).
func resumeReasoningFixture(t *testing.T, m model, seqs ...int) string {
	t.Helper()
	sess := verifierCreateSession(t, m, t.TempDir())
	var events []sessions.Event
	for _, seq := range seqs {
		payload, err := json.Marshal(map[string]any{
			"content": "thinking block " + string(rune('a'+seq)),
			"seq":     seq,
		})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, sessions.Event{Type: sessions.EventReasoning, Payload: payload})
	}
	if _, err := m.sessionStore.AppendEvents(sess.SessionID, eventsToInputs(events)); err != nil {
		t.Fatal(err)
	}
	return sess.SessionID
}

func resumeAndReturn(t *testing.T, m model, id string) model {
	t.Helper()
	updated, _ := m.Update(testKeyText("/resume " + id))
	mid := updated.(model)
	updated, _ = mid.Update(testKey(tea.KeyEnter))
	return updated.(model)
}

// Rehydration rebuilds the live path's exact block ids: reasoning_1,
// reasoning_2 — the transcript rows carry the same identity they had before
// the session ended (DoD 43: streaming redraws into durable blocks; DoD 45:
// resume reconstructs the narrative).
func TestResumeRebuildsReasoningBlockIDs(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	id := resumeReasoningFixture(t, m, 1, 2)

	resumed := resumeAndReturn(t, m, id)
	var got []string
	for _, row := range resumed.transcript {
		if row.kind == rowReasoning {
			got = append(got, row.id)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rehydrated reasoning rows, got %d (%v)", len(got), got)
	}
	if got[0] != "reasoning_1" || got[1] != "reasoning_2" {
		t.Fatalf("block ids not stable across resume: %v", got)
	}
}

// Legacy sessions (payloads without a seq — everything persisted before this
// change) still rehydrate, with ordinal ids filled in so dedup keys stay
// well-formed. Resume must never regress for old logs.
func TestResumeLegacyReasoningGetsOrdinalIDs(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	sess := verifierCreateSession(t, m, ws)
	var events []sessions.Event
	for _, content := range []string{"legacy one", "legacy two"} {
		payload, err := json.Marshal(map[string]any{"content": content})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, sessions.Event{Type: sessions.EventReasoning, Payload: payload})
	}
	if _, err := m.sessionStore.AppendEvents(sess.SessionID, eventsToInputs(events)); err != nil {
		t.Fatal(err)
	}

	resumed := resumeAndReturn(t, m, sess.SessionID)
	var got []string
	for _, row := range resumed.transcript {
		if row.kind == rowReasoning {
			got = append(got, row.id)
		}
	}
	if len(got) != 2 || got[0] != "reasoning_1" || got[1] != "reasoning_2" {
		t.Fatalf("legacy reasoning rows should gain ordinal ids: %v", got)
	}
}
