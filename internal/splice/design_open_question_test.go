package splice

// open_question lifecycle probes (§7.1): raised questions reconstruct as the
// open set, resolution removes them, malformed payloads and unpaired resolves
// fail closed (G2). No provider, no model, milliseconds — the reconstruction
// is a pure function over typed events.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/tools"
)

func openQuestionEvent(seq int, question, detail string) sessions.Event {
	raw, _ := json.Marshal(OpenQuestionPayload{Question: question, Detail: detail})
	return sessions.Event{Sequence: seq, Type: sessions.EventOpenQuestionRaised, Payload: raw}
}

func openQuestionResolvedEvent(seq int, question, resolution string) sessions.Event {
	raw, _ := json.Marshal(OpenQuestionResolvedPayload{Question: question, Resolution: resolution})
	return sessions.Event{Sequence: seq, Type: sessions.EventOpenQuestionResolved, Payload: raw}
}

func TestOpenQuestionReconstruction(t *testing.T) {
	events := []sessions.Event{
		openQuestionEvent(1, "are streamed bodies idempotent?", "blocks the retry decision"),
		openQuestionEvent(2, "do worktrees share the go cache?", ""),
		openQuestionResolvedEvent(3, "are streamed bodies idempotent?", "settled"),
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(state.OpenQuestions) != 1 {
		t.Fatalf("open questions = %d, want 1 (the resolved one left the set)", len(state.OpenQuestions))
	}
	if state.OpenQuestions[0].Question != "do worktrees share the go cache?" {
		t.Fatalf("open question = %q, want the unresolved one", state.OpenQuestions[0].Question)
	}
	if state.OpenQuestions[0].Sequence != 2 {
		t.Fatalf("open question sequence = %d, want 2 (stamped from the raise event)", state.OpenQuestions[0].Sequence)
	}
}

// A resolve naming no currently-open question is a reconstruction error, not
// a silent no-op: an unpaired resolve means the log is inconsistent.
func TestOpenQuestionResolveWithoutOpenFailsClosed(t *testing.T) {
	events := []sessions.Event{
		openQuestionResolvedEvent(1, "never asked", "settled"),
	}
	if _, err := ReconstructDesignState(events); err == nil {
		t.Fatal("unpaired resolve reconstructed without error (fail-closed violated)")
	} else if !strings.Contains(err.Error(), "no open question") {
		t.Fatalf("error should name the unpaired question, got: %v", err)
	}
}

// Re-raising an identical question while it is already open fails closed:
// the open set is append-only and never silently rewrites.
func TestOpenQuestionDoubleRaiseFailsClosed(t *testing.T) {
	events := []sessions.Event{
		openQuestionEvent(1, "same question?", ""),
		openQuestionEvent(2, "same question?", ""),
	}
	if _, err := ReconstructDesignState(events); err == nil {
		t.Fatal("double raise reconstructed without error (append-only violated)")
	} else if !strings.Contains(err.Error(), "already open") {
		t.Fatalf("error should name the duplicate, got: %v", err)
	}
}

// Malformed payloads fail closed with the event named (G2).
func TestOpenQuestionMalformedPayloadFailsClosed(t *testing.T) {
	broken := sessions.Event{Sequence: 1, Type: sessions.EventOpenQuestionRaised, Payload: json.RawMessage(`{"broken"`)}
	if _, err := ReconstructDesignState([]sessions.Event{broken}); err == nil {
		t.Fatal("malformed raise payload reconstructed without error")
	}
	empty := sessions.Event{Sequence: 1, Type: sessions.EventOpenQuestionRaised, Payload: json.RawMessage(`{"question":"  "}`)}
	if _, err := ReconstructDesignState([]sessions.Event{empty}); err == nil {
		t.Fatal("blank question reconstructed without error")
	}
	badResolution := openQuestionResolvedEvent(2, "q", "maybe")
	if _, err := ReconstructDesignState([]sessions.Event{openQuestionEvent(1, "q", ""), badResolution}); err == nil {
		t.Fatal("invalid resolution value reconstructed without error")
	}
}

// A new design-mode epoch resets the open set along with the rest of the
// state (the log keeps the audit trail; the state moves on).
func TestOpenQuestionClearedByNewEpoch(t *testing.T) {
	events := []sessions.Event{
		openQuestionEvent(1, "old question?", ""),
		{Sequence: 2, Type: sessions.EventDesignModeEntered},
	}
	state, err := ReconstructDesignState(events)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(state.OpenQuestions) != 0 {
		t.Fatalf("open questions after new epoch = %d, want 0", len(state.OpenQuestions))
	}
}

// The recorders queue and take cleanly; the raise tool surfaces duplicates.
func TestOpenQuestionRecorderRaiseAndTake(t *testing.T) {
	rec := NewOpenQuestionRecorder()
	if err := rec.Raise("first?", "detail"); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if err := rec.Raise("", ""); err == nil {
		t.Fatal("blank question raised without error")
	}
	if err := rec.Raise("first?", ""); err == nil {
		t.Fatal("duplicate question raised without error")
	}
	got := rec.Take()
	if len(got) != 1 || got[0].Question != "first?" {
		t.Fatalf("Take() = %+v, want one 'first?'", got)
	}
	if again := rec.Take(); len(again) != 0 {
		t.Fatalf("second Take() = %+v, want empty", again)
	}
}

// The appenders produce typed, valid session events (the drain path's shape).
func TestOpenQuestionAppenders(t *testing.T) {
	raise, err := OpenQuestionRaisedAppender("q?", "why")
	if err != nil {
		t.Fatalf("raise appender: %v", err)
	}
	if raise.Type != sessions.EventOpenQuestionRaised {
		t.Fatalf("raise event type = %q", raise.Type)
	}
	resolve, err := OpenQuestionResolvedAppender("q?", "settled")
	if err != nil {
		t.Fatalf("resolve appender: %v", err)
	}
	if resolve.Type != sessions.EventOpenQuestionResolved {
		t.Fatalf("resolve event type = %q", resolve.Type)
	}
	if _, err := OpenQuestionRaisedAppender("  ", ""); err == nil {
		t.Fatal("blank question accepted by the raise appender")
	}
	if _, err := OpenQuestionResolvedAppender("q?", "vibes"); err == nil {
		t.Fatal("invalid resolution accepted by the resolve appender")
	}
}

// The raise tool queues through the real tool Run path and rejects duplicates
// with a tool error result (never a silent ok).
func TestRaiseOpenQuestionToolRun(t *testing.T) {
	rec := NewOpenQuestionRecorder()
	tool := NewRaiseOpenQuestionTool(rec)
	result := tool.Run(t.Context(), map[string]any{"question": "are streamed bodies idempotent?", "detail": "blocks retry"})
	if result.Status != tools.StatusOK {
		t.Fatalf("tool status = %v, want OK", result.Status)
	}
	dup := tool.Run(t.Context(), map[string]any{"question": "are streamed bodies idempotent?"})
	if dup.Status != tools.StatusError {
		t.Fatalf("duplicate raise status = %v, want Error", dup.Status)
	}
	if got := rec.Take(); len(got) != 1 {
		t.Fatalf("recorder holds %d questions, want 1", len(got))
	}
}
