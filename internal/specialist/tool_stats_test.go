package specialist

import (
	"testing"

	"github.com/Taf0711/splice/internal/streamjson"
)

func TestToolCallStatsCountsAndCaps(t *testing.T) {
	events := []streamjson.Event{}
	for _, name := range []string{"web_search", "read_file", "web_search", "bash", "bash", "web_search", "grep", "ls", "glob", "write_file", "edit_file", "submit_code", "grep"} {
		events = append(events, streamjson.Event{Type: streamjson.EventToolCall, Name: name})
	}
	count, names := toolCallStats(events)
	if count != 13 {
		t.Fatalf("count = %d, want 13 (per-call, not dedup)", count)
	}
	// Distinct names in first-seen order, bounded to specialistPersistToolTail.
	if len(names) != specialistPersistToolTail {
		t.Fatalf("names len = %d, want %d", len(names), specialistPersistToolTail)
	}
	if names[0] != "web_search" || names[1] != "read_file" {
		t.Fatalf("names order = %v, want web_search, read_file first (preserved order)", names)
	}
	for _, n := range names {
		if n == "submit_code" {
			t.Fatalf("cap kept the 9th distinct name; got %v", names)
		}
	}
}

func TestToolCallStatsEmpty(t *testing.T) {
	count, names := toolCallStats(nil)
	if count != 0 || names != nil {
		t.Fatalf("toolCallStats(nil) = %d, %v, want 0, nil", count, names)
	}
}
