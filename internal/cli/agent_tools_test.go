package cli

import (
	"sort"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// agentNodeExcludedTools lists the registry tools no agent node may name, with
// the reason. The rule matches the comment on schemas.AgentEligibleTools.
var agentNodeExcludedTools = map[string]string{
	tools.RawFileReadToolName:        "host seam",
	tools.ToolSearchToolName:         "loads other tools past the allowlist",
	tools.RequestPermissionsToolName: "changes permissions",
	"ask_user":                       "asks the user; no prompt handler in a pipeline",
	"desktop_action":                 "permission deny; never advertised",
	"desktop_snapshot":               "permission deny; never advertised",
	"desktop_windows":                "permission deny; never advertised",
	"browser_action":                 "interactive session; no prompt handler in a pipeline",
	"browser_click":                  "interactive session; no prompt handler in a pipeline",
	"browser_connect":                "interactive session; no prompt handler in a pipeline",
	"browser_install":                "interactive session; no prompt handler in a pipeline",
	"browser_launch":                 "interactive session; no prompt handler in a pipeline",
	"browser_open":                   "interactive session; no prompt handler in a pipeline",
	"browser_press":                  "interactive session; no prompt handler in a pipeline",
	"browser_snapshot":               "interactive session; no prompt handler in a pipeline",
	"browser_type":                   "interactive session; no prompt handler in a pipeline",
	"terminal_session":               "long-lived interactive session",
	"write_stdin":                    "feeds a long-lived interactive session",
	"capture_artifact":               "writes artifacts outside the workspace",
}

// TestAgentEligibleToolsMatchRegistry fails when schemas.AgentEligibleTools
// drifts from the core registry. The eligible set is every registry tool that
// is not host-seam, not permission deny, and not in agentNodeExcludedTools.
func TestAgentEligibleToolsMatchRegistry(t *testing.T) {
	registry := newCoreRegistry(t.TempDir())
	var want []string
	for _, tool := range registry.All() {
		name := tool.Name()
		if _, excluded := agentNodeExcludedTools[name]; excluded {
			continue
		}
		if _, hostSeam := tool.(tools.HostSeamTool); hostSeam {
			continue
		}
		if tool.Safety().Permission == tools.PermissionDeny {
			continue
		}
		want = append(want, name)
	}
	sort.Strings(want)
	got := append([]string(nil), schemas.AgentEligibleTools...)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("AgentEligibleTools has %d names, registry-derived set has %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AgentEligibleTools[%d] = %q, registry-derived set has %q", i, got[i], want[i])
		}
	}
}

// TestAgentEligibleToolsAskClassification pins which eligible tools prompt in an
// agent node in ask mode. The loop prompts for every call to a tool that is not
// read-only (side effect read or none). This test fails when a real tool's side
// effect moves it to the other group, or when a new eligible tool is not
// classified here.
func TestAgentEligibleToolsAskClassification(t *testing.T) {
	prompts := map[string]bool{
		"apply_patch": true, "bash": true, "delete_file": true, "edit_file": true,
		"exec_command": true, "web_fetch": true, "write_file": true,
		"glob": false, "grep": false, "list_directory": false, "lsp_navigate": false,
		"read_file": false, "read_minified_file": false, "update_plan": false,
	}
	registry := newCoreRegistry(t.TempDir())
	for _, name := range schemas.AgentEligibleTools {
		want, classified := prompts[name]
		if !classified {
			t.Errorf("eligible tool %q is not classified for ask mode", name)
			continue
		}
		tool, ok := registry.Get(name)
		if !ok {
			t.Errorf("eligible tool %q is not in the core registry", name)
			continue
		}
		side := tool.Safety().SideEffect
		got := side != tools.SideEffectRead && side != tools.SideEffectNone
		if got != want {
			t.Errorf("tool %q (side effect %s): prompts in ask = %v, want %v", name, side, got, want)
		}
	}
}
