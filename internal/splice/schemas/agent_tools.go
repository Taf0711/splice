package schemas

// AgentEligibleTools is the closed set of tool names an agent node may list in
// its tools allowlist. It is generated from the core tool registry. The
// generation rule is: every registered tool except host-seam tools, tools that
// start sub-agents or delegate (Task, specialists, swarm, skills), tools that
// ask the user or change permissions (ask_user, request_permissions), tools
// that load other tools (tool_search), tools the model surface never
// advertises (permission deny), and interactive or out-of-workspace tools
// that a pipeline cannot serve: browser_*, terminal_session, write_stdin, and
// capture_artifact. A pipeline has no prompt handler, and those tools keep
// long-lived sessions or write artifacts outside the workspace.
// TestAgentEligibleToolsMatchRegistry in internal/cli fails when this list
// drifts from the registry.
var AgentEligibleTools = []string{
	"apply_patch",
	"bash",
	"delete_file",
	"edit_file",
	"exec_command",
	"glob",
	"grep",
	"list_directory",
	"lsp_navigate",
	"read_file",
	"read_minified_file",
	"update_plan",
	"web_fetch",
	"write_file",
}

// IsAgentEligibleTool reports whether name may appear in an agent node's tools.
func IsAgentEligibleTool(name string) bool {
	for _, eligible := range AgentEligibleTools {
		if eligible == name {
			return true
		}
	}
	return false
}
