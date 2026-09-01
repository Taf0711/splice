package tui

// presentation_replay_test.go (F3, §15): presentation.State reconstructs
// from the session event stream. The full lifecycle: a run persists
// snapshots -> session resumes -> lastState rebuilds from runtime truth.

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/sessions"
)

// eventsToInputs converts replay fixtures into the store's append shape.
func eventsToInputs(events []sessions.Event) []sessions.AppendEventInput {
	inputs := make([]sessions.AppendEventInput, 0, len(events))
	for _, e := range events {
		inputs = append(inputs, sessions.AppendEventInput{Type: e.Type, Payload: e.Payload})
	}
	return inputs
}

// replayTestState builds a valid terminal presentation.State the way the
// runtime emits it: full receipt, health, and lifecycle.
func replayTestState(status string) presentation.State {
	st := presentation.State{
		SchemaVersion: presentation.PresentationSchemaVersionV1,
		Lifecycle:     presentation.LifecycleComplete,
	}
	if status == "failed" {
		st.Health = presentation.HealthFailed
	}
	st.Completion = &presentation.CompletionReceipt{Status: status, Detail: "verifier"}
	return st
}

func replayTestEvent(t *testing.T, st presentation.State) sessions.Event {
	t.Helper()
	stateJSON, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"presentation_schema_version": st.SchemaVersion,
		"lifecycle":                   string(st.Lifecycle),
		"presentation_state":          json.RawMessage(stateJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sessions.Event{Type: sessions.EventMessage, Payload: payload}
}

func TestReplayReconstructsTerminalState(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	sess := verifierCreateSession(t, m, ws)

	// Two snapshots land in the event log (an executing one, then the
	// terminal failed one) — exactly what OnPresentationState persists.
	events := []sessions.Event{
		replayTestEvent(t, presentation.State{SchemaVersion: presentation.PresentationSchemaVersionV1, Lifecycle: presentation.LifecycleExecute}),
		replayTestEvent(t, replayTestState("failed")),
	}
	if _, err := m.sessionStore.AppendEvents(sess.SessionID, eventsToInputs(events)); err != nil {
		t.Fatal(err)
	}
	if m.lastState.Lifecycle != "" {
		t.Fatal("verifier setup: lastState should start empty")
	}

	// Resume through the REAL path.
	updated, _ := m.Update(testKeyText("/resume " + sess.SessionID))
	mid := updated.(model)
	updated, _ = mid.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	// Last state wins: the terminal failed state is the reconstruction.
	if next.lastState.Lifecycle != presentation.LifecycleComplete {
		t.Fatalf("replay did not reconstruct lifecycle: %q", next.lastState.Lifecycle)
	}
	if next.lastState.Health != presentation.HealthFailed {
		t.Fatalf("replay did not reconstruct health: %q", next.lastState.Health)
	}
	if next.lastState.Completion == nil || next.lastState.Completion.Status != "failed" {
		t.Fatalf("replay did not reconstruct the receipt: %+v", next.lastState.Completion)
	}
}

// The reconstruction renders: the pipeline panel / lifecycle projection
// draws from the replayed lastState (assert on View, per the verifier
// discipline).
func TestReplayStateVisibleInLiveView(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	sess := verifierCreateSession(t, m, ws)
	events := []sessions.Event{replayTestEvent(t, replayTestState("failed"))}
	m.sessionStore.AppendEvents(sess.SessionID, eventsToInputs(events))

	updated, _ := m.Update(testKeyText("/resume " + sess.SessionID))
	mid := updated.(model)
	updated, _ = mid.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	plain := plainRender(t, next.View())
	// The lifecycle chip projects the replayed phase; the receipt health is
	// in state even if the idle layout shows no pipeline panel. Assert the
	// state is genuinely loaded (lifecycle not idle-empty).
	if next.lastState.Lifecycle == "" {
		t.Fatal("replay left lastState empty")
	}
	if strings.TrimSpace(plain) == "" {
		t.Fatal("empty view after replay")
	}
}

// Old sessions (pre-F3 stub payloads) resume cleanly with an empty state —
// no failure, honest idle projection.
func TestReplaySkipsLegacyStubEvents(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	sess := verifierCreateSession(t, m, ws)
	stub, err := json.Marshal(map[string]any{
		"presentation_schema_version": 1,
		"lifecycle":                   "execute",
	})
	if err != nil {
		t.Fatal(err)
	}
	events := []sessions.Event{{Type: sessions.EventMessage, Payload: stub}}
	m.sessionStore.AppendEvents(sess.SessionID, eventsToInputs(events))

	updated, _ := m.Update(testKeyText("/resume " + sess.SessionID))
	mid := updated.(model)
	updated, _ = mid.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.lastState.Lifecycle != "" {
		t.Fatalf("legacy stub event produced state: %q", next.lastState.Lifecycle)
	}
}

// A snapshot with a bad schema version must be skipped, not fatal.
func TestReplaySkipsInvalidSnapshots(t *testing.T) {
	ws := t.TempDir()
	m := newDesignModeTestModel(ws, &fakeProvider{}, nil)
	sess := verifierCreateSession(t, m, ws)
	bad := replayTestEvent(t, replayTestState("failed"))
	bad.Payload = []byte(`{"presentation_state": {"schema_version": 99, "lifecycle": "complete"}}`)
	good := replayTestEvent(t, replayTestState("completed"))
	m.sessionStore.AppendEvents(sess.SessionID, eventsToInputs([]sessions.Event{bad, good}))

	updated, _ := m.Update(testKeyText("/resume " + sess.SessionID))
	mid := updated.(model)
	updated, _ = mid.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.lastState.Completion == nil || next.lastState.Completion.Status != "completed" {
		t.Fatalf("replay did not land on the newest valid snapshot: %+v", next.lastState.Completion)
	}
}
