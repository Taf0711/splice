package splice

import (
	"encoding/json"
	"strings"

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
//  5. Include ask_user Q&A as conversation turns: role "ask_user" becomes an
//     assistant message carrying the questions, and the paired
//     "ask_user_answers" becomes a user message carrying the answers. The
//     design agent must remember the interview it already conducted; dropping
//     it would make a later turn re-ask every question from the start.
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
				Role      string                   `json:"role"`
				Content   string                   `json:"content"`
				Header    string                   `json:"header"`
				Questions []askUserHistoryQuestion `json:"questions"`
				Answers   []string                 `json:"answers"`
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
			case "ask_user":
				// The design agent asked questions; remember them as an assistant
				// turn so a later run does not re-ask from the beginning.
				result = append(result, schemas.ConversationMessage{
					Role:    "assistant",
					Content: renderAskUserHistory(msg.Header, msg.Questions),
				})
			case "ask_user_answers":
				// The user's answers to the preceding ask_user; remember them as a
				// user turn so a later run keeps the answers it was given.
				result = append(result, schemas.ConversationMessage{
					Role:    "user",
					Content: renderAskUserAnswersHistory(msg.Answers),
				})
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

// askUserHistoryQuestion is the decoded shape of one question in an ask_user
// event payload (mirrors askUserSessionPayload in the TUI).
type askUserHistoryQuestion struct {
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	MultiSelect bool     `json:"multiSelect"`
}

// renderAskUserHistory renders an ask_user request as an assistant turn the
// design agent can read back: the header (if any) plus each question with its
// offered options.
func renderAskUserHistory(header string, questions []askUserHistoryQuestion) string {
	var b strings.Builder
	if header != "" {
		b.WriteString(header)
	}
	for _, q := range questions {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString("Q: ")
		b.WriteString(q.Question)
		if len(q.Options) > 0 {
			b.WriteString(" (options: ")
			b.WriteString(strings.Join(q.Options, ", "))
			b.WriteString(")")
		}
	}
	return b.String()
}

// renderAskUserAnswersHistory renders the user's answers to an ask_user as a
// user turn. An unanswered question renders as "(no answer)" so the count and
// order line up with the questions the design agent asked.
func renderAskUserAnswersHistory(answers []string) string {
	if len(answers) == 0 {
		return "(no answer)"
	}
	parts := make([]string, len(answers))
	for index, answer := range answers {
		if strings.TrimSpace(answer) == "" {
			parts[index] = "(no answer)"
		} else {
			parts[index] = answer
		}
	}
	return "Answers: " + strings.Join(parts, " | ")
}
