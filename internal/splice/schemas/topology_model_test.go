package schemas

import (
	"strings"
	"testing"
)

// TestValidateRejectsModelOnModelFreeNode pins the contradiction guard: a
// model declaration on a model-free node is invalid, because the executor
// skips resolution for model-free stages and the declaration would be silently
// ignored.
func TestValidateRejectsModelOnModelFreeNode(t *testing.T) {
	cases := []struct {
		name string
		node PipelineNode
	}{
		{"builtin model-free", PipelineNode{Name: "lint", Type: "static_analyzer", Model: &StageModelConfig{ProviderProfile: "local", Model: "x"}}},
		{"explicit model-free capability", PipelineNode{Name: "lint", Type: "static_analyzer", Caps: NodeCapabilities{ModelFree: true}, Model: &StageModelConfig{ProviderProfile: "local", Model: "x"}}},
		{"command node", PipelineNode{Name: "lint", Type: NodeTypeCommand, Command: []string{"true"}, Model: &StageModelConfig{ProviderProfile: "local", Model: "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topology := PipelineTopology{
				Version: TopologySchemaVersion,
				Name:    "models",
				Nodes:   []PipelineNode{tc.node},
			}
			err := topology.Validate()
			if err == nil {
				t.Fatal("Validate accepted a model declaration on a model-free node")
			}
			if !strings.Contains(err.Error(), "model-free") {
				t.Fatalf("error = %v, want the model-free refusal", err)
			}
		})
	}
	// A model-backed node with a model declaration stays valid.
	valid := PipelineTopology{
		Version: TopologySchemaVersion,
		Name:    "models",
		Nodes: []PipelineNode{
			{Name: "code_writer", Type: "code_writer", Model: &StageModelConfig{ProviderProfile: "local", Model: "x"}},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate rejected a valid model declaration: %v", err)
	}
}
