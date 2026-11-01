package tui

import (
	"encoding/json"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
)

func TestHydrationKeepsFailedTaskWithoutSpecialist(t *testing.T) {
	ev := func(typ sessions.EventType, payload string) sessions.Event {
		return sessions.Event{Type: typ, Payload: json.RawMessage(payload)}
	}

	// A Task that FAILED before a specialist started: tool call + error result, no
	// EventSpecialistStart. Its rows must survive resume (M10) — otherwise the
	// failed delegation silently vanishes.
	failed := transcriptRowsFromSessionEvents([]sessions.Event{
		ev(sessions.EventToolCall, `{"name":"Task","id":"call_fail","arguments":"{}"}`),
		ev(sessions.EventToolResult, `{"name":"Task","toolCallId":"call_fail","status":"error","output":"spawn failed"}`),
	})
	if !transcriptContains(failed, "tool call: Task") || !transcriptContains(failed, "tool result: Task") {
		t.Fatalf("a failed Task with no specialist must keep its rows on resume, got %#v", failed)
	}

	// A Task that DID start a specialist: the card renders it, so the raw Task
	// tool-call/result rows are skipped (no duplication).
	withSpecialist := transcriptRowsFromSessionEvents([]sessions.Event{
		ev(sessions.EventToolCall, `{"name":"Task","id":"call_ok","arguments":"{}"}`),
		ev(sessions.EventSpecialistStart, `{"childSessionId":"child-1","toolCallId":"call_ok","specialist":"explorer","status":"running"}`),
		ev(sessions.EventToolResult, `{"name":"Task","toolCallId":"call_ok","status":"ok","output":"done"}`),
	})
	if transcriptContains(withSpecialist, "tool call: Task") || transcriptContains(withSpecialist, "tool result: Task") {
		t.Fatalf("a Task with a specialist card must NOT also show raw Task rows, got %#v", withSpecialist)
	}
	// A production specialist_start persists status "running" (recordSpecialistStart
	// writes it), so the rehydrated card must render as running, not the error
	// fallback used for a missing status.
	var startCard *specialistInfo
	for i := range withSpecialist {
		if withSpecialist[i].kind == rowSpecialist && withSpecialist[i].specialistInfo != nil {
			startCard = withSpecialist[i].specialistInfo
		}
	}
	if startCard == nil || startCard.status != specialistRunning {
		t.Fatalf("production specialist start must rehydrate as running, got %#v", withSpecialist)
	}
}

// TestResumeRehydratesReasoningRows: a persisted EventReasoning rehydrates as
// a collapsed reasoning row between the user and assistant rows.
func TestResumeRehydratesReasoningRows(t *testing.T) {
	ev := func(typ sessions.EventType, payload string) sessions.Event {
		return sessions.Event{Type: typ, Payload: json.RawMessage(payload)}
	}
	rows := transcriptRowsFromSessionEvents([]sessions.Event{
		ev(sessions.EventMessage, `{"role":"user","content":"hello"}`),
		ev(sessions.EventReasoning, `{"content":"private thought"}`),
		ev(sessions.EventMessage, `{"role":"assistant","content":"public answer"}`),
	})
	var reasoningRow *transcriptRow
	var reasoningIndex int
	for i, row := range rows {
		if row.kind == rowReasoning {
			reasoningRow = &rows[i]
			reasoningIndex = i
			break
		}
	}
	if reasoningRow == nil {
		t.Fatalf("expected a reasoning row, got %#v", rows)
	}
	if reasoningRow.text != "private thought" {
		t.Fatalf("reasoning text = %q, want %q", reasoningRow.text, "private thought")
	}
	if reasoningRow.expanded {
		t.Fatalf("reasoning row must be collapsed on resume, got expanded=true")
	}
	// The reasoning row must sit between the user and assistant rows.
	var userIndex, assistantIndex int = -1, -1
	for i, row := range rows {
		if row.kind == rowUser {
			userIndex = i
		}
		if row.kind == rowAssistant {
			assistantIndex = i
		}
	}
	if userIndex < 0 || assistantIndex < 0 {
		t.Fatalf("expected user and assistant rows, got %#v", rows)
	}
	if !(userIndex < reasoningIndex && reasoningIndex < assistantIndex) {
		t.Fatalf("reasoning row at %d must be between user (%d) and assistant (%d)", reasoningIndex, userIndex, assistantIndex)
	}
}

// TestResumeKeepsSpecialistToolCalls: a specialist_stop event persisted with
// the tool snapshot (toolCount + tools tail) rehydrates the card's per-call
// list after resume, so the verbose listing survives a restart.
func TestResumeKeepsSpecialistToolCalls(t *testing.T) {
	ev := func(typ sessions.EventType, payload string) sessions.Event {
		return sessions.Event{Type: typ, Payload: json.RawMessage(payload)}
	}
	rows := transcriptRowsFromSessionEvents([]sessions.Event{
		ev(sessions.EventSpecialistStart, `{"childSessionId":"child-1","specialist":"design","description":"plan","status":"running"}`),
		ev(sessions.EventSpecialistStop, `{"childSessionId":"child-1","specialist":"design","description":"plan","status":"completed","toolCount":3,"tools":["web_search","read_file","write_file"]}`),
	})
	var card *specialistInfo
	for i := range rows {
		if rows[i].kind == rowSpecialist && rows[i].specialistInfo != nil {
			card = rows[i].specialistInfo
		}
	}
	if card == nil {
		t.Fatal("resume produced no specialist card")
	}
	if card.toolCount != 3 {
		t.Fatalf("toolCount = %d, want 3", card.toolCount)
	}
	if len(card.toolCalls) != 3 {
		t.Fatalf("toolCalls len = %d, want 3", len(card.toolCalls))
	}
	if card.toolCalls[0].name != "web_search" || card.toolCalls[1].name != "read_file" {
		t.Fatalf("toolCalls = %+v, want web_search then read_file", card.toolCalls)
	}
	// The start + stop events for the same child collapse into one card.
	if len(rows) != 1 || rows[0].kind != rowSpecialist {
		t.Fatalf("expected start+stop to reconcile into one card row, got %d rows", len(rows))
	}
}
