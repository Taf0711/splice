package tui

// presentation_replay.go (F3, stabilization §15): presentation.State is
// reconstructable from the session event stream. Pipeline runs persist the
// full canonical state JSON in each presentation event; resume replays the
// persisted states so the pipeline panel, lifecycle, health, and receipt
// rebuild from runtime truth without touching the runtime.
//
// Replay is LAST-STATE-WINS by design: each persisted snapshot is a
// complete presentation.State (the runtime emits full snapshots, not
// deltas), so the correct reconstruction is the newest valid snapshot.
// Feeding snapshots back through presentation.Apply would be wrong twice
// over — Apply expects events, not states, and inventing a "replay" event
// kind would blur the reducer's contract (§2: Apply projects events; it
// does not consume its own output).

import (
	"encoding/json"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/sessions"
)

// replayPresentationState rebuilds model.lastState from the persisted
// presentation snapshots in the resumed session's events. Old sessions
// (pre-F3) carry only the stub payload without "presentation_state"; those
// events are skipped and the state stays empty — the UI renders its idle
// projection, which is the honest rendering for a session whose runtime
// snapshots predate persistence.
func (m model) replayPresentationState(events []sessions.Event) model {
	var last presentation.State
	found := false
	for _, event := range events {
		if event.Type != sessions.EventMessage {
			continue
		}
		raw := event.Payload
		if len(raw) == 0 {
			continue
		}
		var payload struct {
			PresentationState json.RawMessage `json:"presentation_state"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		if len(payload.PresentationState) == 0 {
			continue
		}
		var st presentation.State
		if err := json.Unmarshal(payload.PresentationState, &st); err != nil {
			continue
		}
		if st.SchemaVersion != presentation.PresentationSchemaVersionV1 {
			continue
		}
		if err := st.Validate(); err != nil {
			// A persisted state that no longer validates is skipped, not
			// fatal: resume must never fail because of a stale snapshot.
			continue
		}
		last = st
		found = true
	}
	if !found {
		return m
	}
	m.lastState = last
	return m
}
