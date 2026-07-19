package splice

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func messageEvent(role, content string) sessions.Event {
	payload, _ := json.Marshal(map[string]string{
		"role":    role,
		"content": content,
	})
	return sessions.Event{Type: sessions.EventMessage, Payload: payload}
}

func designModeEnteredEvent() sessions.Event {
	return sessions.Event{Type: sessions.EventDesignModeEntered, Payload: json.RawMessage("{}")}
}

func compactionEvent(summary string) sessions.Event {
	payload, _ := json.Marshal(sessions.CompactionPayload{Summary: summary})
	return sessions.Event{Type: sessions.EventCompaction, Payload: payload}
}

func TestMapDesignHistory_NilOrEmptyEvents(t *testing.T) {
	if got := MapDesignHistory(nil); got != nil {
		t.Fatalf("MapDesignHistory(nil) = %v, want nil", got)
	}
	if got := MapDesignHistory([]sessions.Event{}); got != nil {
		t.Fatalf("MapDesignHistory(empty) = %v, want nil", got)
	}
}

func TestMapDesignHistory_NoDesignModeEntered(t *testing.T) {
	events := []sessions.Event{
		messageEvent("user", "hello"),
		messageEvent("assistant", "hi"),
	}
	if got := MapDesignHistory(events); got != nil {
		t.Fatalf("MapDesignHistory(no design entry) = %v, want nil", got)
	}
}

func TestMapDesignHistory_UserAndAssistant(t *testing.T) {
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "hello"),
		messageEvent("assistant", "hi there"),
	}
	want := []schemas.ConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	got := MapDesignHistory(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MapDesignHistory() = %+v, want %+v", got, want)
	}
}

func TestMapDesignHistory_ExcludesMessagesBeforeLastEntry(t *testing.T) {
	events := []sessions.Event{
		messageEvent("user", "before first entry"),
		designModeEnteredEvent(),
		messageEvent("assistant", "after first entry"),
		designModeEnteredEvent(),
		messageEvent("user", "after second entry"),
	}
	want := []schemas.ConversationMessage{
		{Role: "user", Content: "after second entry"},
	}
	got := MapDesignHistory(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MapDesignHistory() = %+v, want %+v", got, want)
	}
}

func TestMapDesignHistory_SkipsAskUserAndSystem(t *testing.T) {
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "hi"),
		messageEvent("ask_user", "what?"),
		messageEvent("ask_user_answers", "ok"),
		messageEvent("system", "you are helpful"),
		messageEvent("assistant", "hello"),
	}
	want := []schemas.ConversationMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	got := MapDesignHistory(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MapDesignHistory() = %+v, want %+v", got, want)
	}
}

func TestMapDesignHistory_SkipsToolAndSystemLikeEvents(t *testing.T) {
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "run it"),
		{Type: sessions.EventToolCall, Payload: json.RawMessage(`{}`)},
		{Type: sessions.EventToolResult, Payload: json.RawMessage(`{}`)},
		{Type: sessions.EventProviderUsage, Payload: json.RawMessage(`{}`)},
		{Type: sessions.EventError, Payload: json.RawMessage(`{"error":"fail"}`)},
		messageEvent("assistant", "done"),
	}
	want := []schemas.ConversationMessage{
		{Role: "user", Content: "run it"},
		{Role: "assistant", Content: "done"},
	}
	got := MapDesignHistory(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MapDesignHistory() = %+v, want %+v", got, want)
	}
}

func TestMapDesignHistory_CompactionAsSyntheticUser(t *testing.T) {
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "hello"),
		compactionEvent("we agreed on Go"),
		messageEvent("assistant", "yes"),
	}
	want := []schemas.ConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "user", Content: "(Previous conversation summarized: we agreed on Go)"},
		{Role: "assistant", Content: "yes"},
	}
	got := MapDesignHistory(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MapDesignHistory() = %+v, want %+v", got, want)
	}
}

func TestMapDesignHistory_SecondDesignModeEnteredResets(t *testing.T) {
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", "first epoch"),
		designModeEnteredEvent(),
		messageEvent("assistant", "second epoch"),
	}
	want := []schemas.ConversationMessage{
		{Role: "assistant", Content: "second epoch"},
	}
	got := MapDesignHistory(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MapDesignHistory() = %+v, want %+v", got, want)
	}
}

func TestMapDesignHistory_EmptyContentSkipped(t *testing.T) {
	events := []sessions.Event{
		designModeEnteredEvent(),
		messageEvent("user", ""),
		messageEvent("user", "non-empty"),
		messageEvent("assistant", ""),
	}
	want := []schemas.ConversationMessage{
		{Role: "user", Content: "non-empty"},
	}
	got := MapDesignHistory(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MapDesignHistory() = %+v, want %+v", got, want)
	}
}
