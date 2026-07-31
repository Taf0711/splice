package stages

import (
	_ "embed"
)

//go:embed prompts/design_conversation.md
var designConversationSystemPrompt string

// DesignConversationPrompt returns the design conversation system prompt: the
// shared Splice overview followed by the design-phase prompt. The TUI injects
// it via options.SystemPrompt when running design turns through agent.Run.
//
// It deliberately does NOT include pipelineMetaPrompt. That prompt states the
// execution-phase contract (one typed tool, no file access), which is false
// here: the design conversation is free-form and holds real read tools.
func DesignConversationPrompt() string {
	return spliceOverviewPrompt + "\n\n" + designConversationSystemPrompt
}
