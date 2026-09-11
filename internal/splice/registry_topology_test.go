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

// TestRegisterTopologyNodesRejectsPromptNode pins that an unsupported custom
// node fails loud instead of silently compiling to a missing stage.
func TestRegisterTopologyNodesRejectsPromptNode(t *testing.T) {
	registry := stageRegistry{}
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "custom",
		Nodes:   []schemas.PipelineNode{{Name: "note", Type: schemas.NodeTypePrompt, Prompt: "x"}},
	}
	err := registerTopologyNodes(registry, topology)
	if err == nil {
		t.Fatal("registerTopologyNodes accepted an unsupported prompt node")
	}
	if !strings.Contains(err.Error(), "not implemented") || !strings.Contains(err.Error(), "note") {
		t.Fatalf("error = %v, want a named not-implemented error", err)
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
