package splice

import (
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
)

// TestNodeDrawerSummaryNamesItsPass pins that a node with more than one pass
// says which pass reported the summary, since a later pass can still run.
func TestNodeDrawerSummaryNamesItsPass(t *testing.T) {
	graph := presentrun.Graph{Live: true, Tier: "standard"}
	summaryOf := func(node presentrun.GraphNode) string {
		for _, field := range NodeDrawerFields(graph, node, NodeDrawer{Tier: "standard", Live: true}) {
			if field.Key == "summary" {
				return field.Value
			}
		}
		return ""
	}
	one := presentrun.GraphNode{Name: "code_writer", Status: presentation.NodeStatusComplete, Passes: 1, Summary: "wrote a.go"}
	if got := summaryOf(one); got != "wrote a.go" {
		t.Fatalf("one pass: summary = %q", got)
	}
	two := presentrun.GraphNode{Name: "code_writer", Status: presentation.NodeStatusRunning, Iteration: 1, Passes: 2, Summary: "wrote a.go"}
	if got := summaryOf(two); got != "from pass 1: wrote a.go" {
		t.Fatalf("two passes: summary = %q, want the pass named", got)
	}
}

// TestNodeDrawerDisclosesAgentNodes pins the agent rows of the shared drawer,
// which the terminal and the web viewer both show: the tools and their reach,
// the permission rule, the turn cap, and the explicit budget.
func TestNodeDrawerDisclosesAgentNodes(t *testing.T) {
	topology := agentTopologyForWiring()
	topology.Nodes[0].Tools = []string{"read_file", "bash"}
	graph, err := presentrun.BuildGraph(topology, presentation.State{})
	if err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes[0]
	drawer := ResolveNodeDrawer(graph, node, topology, filepath.Join(t.TempDir(), "stage-models.json"))
	fields := map[string]string{}
	for _, field := range NodeDrawerFields(graph, node, drawer) {
		fields[field.Key] = field.Value
	}
	want := map[string]string{
		"type":       "agent",
		"tools":      "read_file, bash (can run commands)",
		"permission": "ask, whatever the run mode; ask prompts for every write, command, and network call",
		"turns":      "up to 3",
		"budget":     "input 0 / 1K · output 0 / 500 (configured)",
	}
	for key, value := range want {
		if fields[key] != value {
			t.Errorf("drawer %s = %q, want %q (problem %q)", key, fields[key], value, drawer.Problem)
		}
	}
}

// TestAgentNodeCountTextReadsOnEverySurface pins the agent count line in the
// trust card and the web page: a real plural, and no "above" or "below".
func TestAgentNodeCountTextReadsOnEverySurface(t *testing.T) {
	if got := AgentNodeCountText(1); got != "1 agent node runs a model with its listed tools when this topology is active" {
		t.Errorf("one: %q", got)
	}
	if got := AgentNodeCountText(2); got != "2 agent nodes run a model with their listed tools when this topology is active" {
		t.Errorf("two: %q", got)
	}
}
