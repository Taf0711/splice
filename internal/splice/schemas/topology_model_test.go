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

// TestValidateAcceptsExplicitModelFreeOnModelFreeBuiltin pins the profile-aware
// fix directly: the builtin capability profile decides, so an explicit
// model_free on a model-free builtin is redundant but valid. A model-backed
// builtin is still rejected with the profile message. Without this case a
// future tightening could restore the over-broad rejection silently.
func TestValidateAcceptsExplicitModelFreeOnModelFreeBuiltin(t *testing.T) {
	for _, nodeType := range []string{"static_analyzer", "security_auditor", "test_runner", "acceptance_verifier"} {
		topology := PipelineTopology{
			Version: TopologySchemaVersion,
			Name:    "caps",
			Nodes:   []PipelineNode{{Name: nodeType, Type: nodeType, Caps: NodeCapabilities{ModelFree: true}}},
		}
		if err := topology.Validate(); err != nil {
			t.Fatalf("type %s with an explicit model_free must validate, got %v", nodeType, err)
		}
	}
	modelBacked := PipelineTopology{
		Version: TopologySchemaVersion,
		Name:    "caps",
		Nodes:   []PipelineNode{{Name: "code_writer", Type: "code_writer", Caps: NodeCapabilities{ModelFree: true}}},
	}
	err := modelBacked.Validate()
	if err == nil || !strings.Contains(err.Error(), "model-backed in the builtin profile") {
		t.Fatalf("model-backed builtin error = %v, want the profile message", err)
	}
}

// TestEffectiveCapabilitiesModelFreeMatrix pins the resolution rule the
// validation above rests on: for a builtin the profile decides, so an explicit
// true is redundant and an explicit false cannot unset it (bool has no unset
// state). A prompt is model-backed, and a command is model-free regardless of
// the flag.
func TestEffectiveCapabilitiesModelFreeMatrix(t *testing.T) {
	cases := []struct {
		name string
		node PipelineNode
		want bool
	}{
		{"model-free builtin, unset", PipelineNode{Name: "test_runner", Type: "test_runner"}, true},
		{"model-free builtin, explicit true", PipelineNode{Name: "test_runner", Type: "test_runner", Caps: NodeCapabilities{ModelFree: true}}, true},
		{"model-free builtin, explicit false", PipelineNode{Name: "test_runner", Type: "test_runner", Caps: NodeCapabilities{ModelFree: false}}, true},
		{"model-backed builtin, unset", PipelineNode{Name: "code_writer", Type: "code_writer"}, false},
		{"prompt", PipelineNode{Name: "note", Type: NodeTypePrompt, Prompt: "x"}, false},
		{"command", PipelineNode{Name: "lint", Type: NodeTypeCommand, Command: []string{"true"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.node.EffectiveCapabilities().ModelFree; got != tc.want {
				t.Fatalf("ModelFree = %v, want %v", got, tc.want)
			}
		})
	}
}
