package tui

import "github.com/Taf0711/splice/internal/flags"

// usesPipeline reports whether an interactive turn should run through the
// deterministic pipeline instead of the plain agent loop.
//
// Spec-draft and design-conversation turns always use the agent loop: they are
// conversational surfaces, not execution runs. An explicit /exec run always
// uses the pipeline, whatever the flag, because /exec promises CLI-exec parity.
// Every other turn follows the tui.pipeline.enabled flag, which defaults on.
// With the zero flag set the result is identical to the pre-flag behavior.
func usesPipeline(runKind tuiRunKind, set flags.Set, execDirect bool) bool {
	if runKind == tuiRunSpecDraft || runKind == tuiRunDesignConversation {
		return false
	}
	if execDirect {
		return true
	}
	return set.Enabled(flags.TUIPipelineEnabled)
}
