package splice

import (
	"encoding/json"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// MapDesignHistory converts raw session events into the ConversationMessage
// list for crystallization. It implements the 8-step contract:
//  1. Iterate events in sequence order.
//  2. Start AFTER the latest design_mode_entered event (that event begins a
//     fresh design epoch; only turns after it belong to this crystallization).
//  3. For EventMessage, decode {role, content}.
//  4. Map role "user" -> {Role:"user"}, "assistant" -> {Role:"assistant"}.
//  5. Skip ask_user events (role "ask_user") and ask_user_answers — they are
//     tool interactions, not conversation turns.
//  6. Exclude system messages (role "system"), tool_call, tool_result,
//     permission, usage, error, checkpoint, rewind, compaction, fork, child,
//     specialist, spec, and all design lifecycle events.
//  7. Handle compaction: if a session_compaction event appears in the epoch,
//     include its summary as a synthetic user message
//     {Role:"user", Content:"(Previous conversation summarized: <summary>"}).
//  8. Return the messages; the caller validates DesignConversationInput.
//
// Returns nil when there are no conversation messages in the current epoch
// (e.g. design_mode_entered exists but no user/assistant turns followed).
func MapDesignHistory(events []sessions.Event) []schemas.ConversationMessage {
	if len(events) == 0 {
		return nil
	}

	startIdx := -1
	for i, event := range events {
		if event.Type == sessions.EventDesignModeEntered {
			startIdx = i
		}
	}
	if startIdx == -1 {
		return nil
	}

	var result []schemas.ConversationMessage
	for _, event := range events[startIdx+1:] {
		switch event.Type {
		case sessions.EventMessage:
			var msg struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(event.Payload, &msg); err != nil {
				continue
			}
			switch msg.Role {
			case "user", "assistant":
				if msg.Content != "" {
					result = append(result, schemas.ConversationMessage{
						Role:    msg.Role,
						Content: msg.Content,
					})
				}
			}
		case sessions.EventCompaction:
			var payload sessions.CompactionPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			if payload.Summary != "" {
				result = append(result, schemas.ConversationMessage{
					Role:    "user",
					Content: "(Previous conversation summarized: " + payload.Summary + ")",
				})
			}
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}
