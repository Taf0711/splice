package splice

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestRegisterTopologyNodesAddsCommandNode pins that a command node in the
// active topology reaches the registry keyed by node name.
func TestRegisterTopologyNodesAddsCommandNode(t *testing.T) {
	registry := stageRegistry{}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "custom",
		Nodes: []schemas.PipelineNode{
			{Name: "lint_cmd", Type: schemas.NodeTypeCommand, Command: []string{"true"}},
		},
	}
	if err := registerTopologyNodes(registry, topology); err != nil {
		t.Fatalf("registerTopologyNodes: %v", err)
	}
	stage, ok := registry["lint_cmd"]
	if !ok {
		t.Fatal("command node not registered under its node name")
	}
	if _, ok := stage.(stages.CommandStage); !ok {
		t.Fatalf("registered %T, want stages.CommandStage", stage)
	}
	if !stage.Capabilities().ModelFree {
		t.Fatal("a command node must be model-free")
	}
}

// TestRegisterTopologyNodesAddsPromptNode pins that a prompt node reaches the
// registry keyed by node name with its template.
func TestRegisterTopologyNodesAddsPromptNode(t *testing.T) {
	registry := stageRegistry{}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "custom",
		Nodes:   []schemas.PipelineNode{{Name: "note", Type: schemas.NodeTypePrompt, Prompt: "answer {{intent}}"}},
	}
	if err := registerTopologyNodes(registry, topology); err != nil {
		t.Fatalf("registerTopologyNodes: %v", err)
	}
	stage, ok := registry["note"]
	if !ok {
		t.Fatal("prompt node not registered under its node name")
	}
	prompt, ok := stage.(stages.PromptStage)
	if !ok {
		t.Fatalf("registered %T, want stages.PromptStage", stage)
	}
	if prompt.Template != "answer {{intent}}" {
		t.Fatalf("template = %q, want the node template", prompt.Template)
	}
	if prompt.Capabilities().ModelFree {
		t.Fatal("a prompt node is model-backed")
	}
}

// TestRegisterTopologyNodesAliasesRenamedBuiltin pins that a builtin node
// under a different name is still reachable by its node name.
func TestRegisterTopologyNodesAliasesRenamedBuiltin(t *testing.T) {
	registry := stageRegistry{"code_writer": stages.CodeWriter{}}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "renamed",
		Nodes: []schemas.PipelineNode{
			{Name: "writer", Type: "code_writer"},
		},
	}
	if err := registerTopologyNodes(registry, topology); err != nil {
		t.Fatalf("registerTopologyNodes: %v", err)
	}
	if _, ok := registry["writer"]; !ok {
		t.Fatal("renamed builtin not registered under its node name")
	}
}

// TestRegisterTopologyNodesRejectsUnknownBuiltin pins the fail-loud case for
// a builtin type with no implementation.
func TestRegisterTopologyNodesRejectsUnknownBuiltin(t *testing.T) {
	registry := stageRegistry{}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "renamed",
		Nodes:   []schemas.PipelineNode{{Name: "ghost", Type: "static_analyzer"}},
	}
	err := registerTopologyNodes(registry, topology)
	if err == nil || !strings.Contains(err.Error(), "no builtin stage") {
		t.Fatalf("error = %v, want the missing-builtin refusal", err)
	}
}
