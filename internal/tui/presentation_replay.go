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
		// A session with no valid snapshot is UNKNOWN, not "whatever the
		// previous session showed" (review finding 7). Returning the old
		// model unchanged left the destination session displaying the
		// source session's completion, receipt, and pipeline. Clear the
		// presentation state instead: empty is the honest projection for
		// a session whose runtime snapshots were never persisted.
		return m.clearPresentationState()
	}
	return m.applyPresentationSnapshot(last)
}

// applyPresentationSnapshot is the ONE path that lands a presentation.State
// on the model, used by both live snapshots and replay (review finding 7).
// Replay used to assign lastState only, so the pipeline panel, phase trail,
// and receipt state stayed empty or stale while a live event updated them
// through a different path.
func (m model) applyPresentationSnapshot(state presentation.State) model {
	m.lastState = state
	m.pipeline.applyState(state)
	m.phaseTrail.observe(state.Lifecycle)
	// The terminal receipt is runtime truth from the snapshot's
	// completion, so a resumed session offers the same actions the live
	// run offered. A run still in flight has no terminal receipt.
	m.lastTerminalReceipt = terminalReceiptForState(state)
	return m
}

// clearPresentationState drops every projection owned by a run's
// presentation truth. Session switches and replay into a session without
// snapshots both use it, so no surface outlives the truth it came from.
func (m model) clearPresentationState() model {
	m.lastState = presentation.State{}
	m.pipeline.clear()
	m.phaseTrail.reset()
	m.lastTerminalReceipt = ""
	return m
}

// terminalReceiptForState maps a completed run's snapshot to the receipt
// card kind whose action keys should be armed. A run without a terminal
// completion arms nothing.
func terminalReceiptForState(state presentation.State) receiptKind {
	if state.Completion == nil {
		return ""
	}
	switch state.Completion.Status {
	case "cancelled":
		return receiptCancelled
	case "failed":
		return receiptFailed
	case "completed":
		return receiptVerified
	}
	return ""
}
