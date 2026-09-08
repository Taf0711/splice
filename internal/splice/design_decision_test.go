package splice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestDecisionPinnedReconstruction pins the §7.1 ledger contract: pinned
// decisions reconstruct in pin order and survive a crystallization reset
// (only a new design-mode epoch clears them).
func TestDecisionPinnedReconstruction(t *testing.T) {
	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Sequence: 1},
		{Type: sessions.EventDecisionPinned, Sequence: 2, Payload: mustJSON(DecisionPinnedPayload{
			Statement: "retry idempotent methods only",
			Detail:    "GET HEAD PUT DELETE | not POST",
		})},
		{Type: sessions.EventDecisionPinned, Sequence: 3, Payload: mustJSON(DecisionPinnedPayload{
			Statement: "preserve caller deadline", Detail: "retry must respect ctx deadline",
		})},
		{Type: sessions.EventPlanCrystallized, Sequence: 3, Payload: mustJSON(PlanCrystallizedPayload{
			PlanID: "p1", Revision: 1, Plan: mustJSON(schemas.DesignPlan{Epic: "e", Requirements: []string{"r"}}),
		})},
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(state.Decisions) != 2 {
		t.Fatalf("decisions = %d, want 2 (ledger survives crystallization)", len(state.Decisions))
	}
	if state.Decisions[0].Statement != "retry idempotent methods only" {
		t.Fatalf("first decision = %+v", state.Decisions[0])
	}
}

// TestDecisionLedgerAppendOnly pins the history rule: a second pin event
// never replaces the ledger; both decisions remain in order, and a revised
// decision keeps its predecessor.
func TestDecisionLedgerAppendOnly(t *testing.T) {
	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Sequence: 1},
		{Type: sessions.EventDecisionPinned, Sequence: 2, Payload: mustJSON(DecisionPinnedPayload{Statement: "first"})},
		{Type: sessions.EventDecisionPinned, Sequence: 3, Payload: mustJSON(DecisionPinnedPayload{Statement: "revised take", Revised: true})},
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(state.Decisions) != 2 {
		t.Fatalf("ledger has %d decisions, want 2 (revised keeps predecessor)", len(state.Decisions))
	}
	if state.Decisions[1].Statement != "revised take" || !state.Decisions[1].Revised {
		t.Fatalf("revised decision not preserved: %+v", state.Decisions)
	}
}

// TestDecisionPinnedRejectsEmptyStatement pins the fail-closed rule: a
// decision without a statement is a named error, never a silent default.
func TestDecisionPinnedRejectsEmptyStatement(t *testing.T) {
	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Sequence: 1},
		{Type: sessions.EventDecisionPinned, Sequence: 2, Payload: mustJSON(DecisionPinnedPayload{Statement: "   "})},
	}
	if _, err := ReconstructDesignState(events); err == nil {
		t.Fatal("empty statement accepted")
	} else if !strings.Contains(err.Error(), "statement is required") {
		t.Fatalf("error does not name the offender: %v", err)
	}
}

// TestPinDesignDecisionToolRoundTrip proves the tool is WIRED end to end:
// Run records into the recorder, Take drains it, and the appender's event
// payload is exactly the shape ReconstructDesignState consumes.
func TestPinDesignDecisionToolRoundTrip(t *testing.T) {
	rec := NewDecisionRecorder()
	tool := NewPinDesignDecisionTool(rec)
	result := tool.Run(context.Background(), map[string]any{
		"statement": "retry idempotent methods only",
		"detail":    "POST has no retry contract",
	})
	if result.Status != tools.StatusOK {
		t.Fatalf("pin failed: %s", result.Output)
	}
	drained := rec.Take()
	if len(drained) != 1 || drained[0].Statement != "retry idempotent methods only" {
		t.Fatalf("drained ledger = %+v", drained)
	}
	if len(rec.Take()) != 0 {
		t.Fatal("Take did not clear the ledger")
	}
	// A malformed call fails closed and records nothing.
	if err := rec.Record("   ", ""); err == nil {
		t.Fatal("empty statement accepted by the recorder")
	}
	// The event the drain appends reconstructs into the ledger.
	event := DecisionPinnedAppender(drained[0].Statement, drained[0].Detail, "retry semantics")
	events := []sessions.Event{
		{Type: sessions.EventDesignModeEntered, Sequence: 1},
		{Type: sessions.EventDecisionPinned, Sequence: 2, Payload: mustJSON(event.Payload)},
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(state.Decisions) != 1 || state.Decisions[0].Statement != "retry idempotent methods only" {
		t.Fatalf("decisions not reconstructed: %+v", state.Decisions)
	}
}

// TestPinDesignDecisionToolRejectsBlank pins the tool-level fail-closed rule.
func TestPinDesignDecisionToolRejectsEmpty(t *testing.T) {
	rec := NewDecisionRecorder()
	tool := NewPinDesignDecisionTool(rec)
	result := tool.Run(context.Background(), map[string]any{"statement": "   "})
	if result.Status != tools.StatusError {
		t.Fatalf("empty statement accepted: %+v", result)
	}
	if len(rec.decisions) != 0 {
		t.Fatal("rejected pin still recorded")
	}
}
