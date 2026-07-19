package stages

import (
	_ "embed"
)

//go:embed prompts/design_conversation.md
var designConversationSystemPrompt string

// DesignConversationPrompt returns the embedded design conversation system
// prompt text. The TUI uses this to inject the prompt via options.SystemPrompt
// when running design conversation turns through agent.Run.
func DesignConversationPrompt() string {
	return designConversationSystemPrompt
}
