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

// TestValidateRejectsInertPullFlags pins the rule that a capability requiring a
// model prompt cannot be declared on a node that has none. pull_context is read
// only by code_writer and test_generator; pull_memory only by code_writer,
// test_generator, and prompt nodes. Everywhere else the declaration is inert,
// and pull_memory still pays for a retrieval.
func TestValidateRejectsInertPullFlags(t *testing.T) {
	cases := []struct {
		name string
		node PipelineNode
		want string
	}{
		{"model-free builtin pull_context", PipelineNode{Name: "lint", Type: "static_analyzer", Caps: NodeCapabilities{PullContext: boolPtr(true)}}, "pull_context has no effect"},
		{"model-free builtin pull_memory", PipelineNode{Name: "lint", Type: "static_analyzer", Caps: NodeCapabilities{PullMemory: boolPtr(true)}}, "pull_memory has no effect"},
		{"test_runner pull_memory", PipelineNode{Name: "test_runner", Type: "test_runner", Caps: NodeCapabilities{PullMemory: boolPtr(true)}}, "pull_memory has no effect"},
		{"command pull_context", PipelineNode{Name: "lint", Type: NodeTypeCommand, Command: []string{"true"}, Caps: NodeCapabilities{PullContext: boolPtr(true)}}, "pull_context has no effect"},
		{"command pull_memory", PipelineNode{Name: "lint", Type: NodeTypeCommand, Command: []string{"true"}, Caps: NodeCapabilities{PullMemory: boolPtr(true)}}, "pull_memory has no effect"},
		{"prompt pull_context", PipelineNode{Name: "note", Type: NodeTypePrompt, Prompt: "x", Caps: NodeCapabilities{PullContext: boolPtr(true)}}, "no context variable in v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topology := PipelineTopology{Version: TopologySchemaVersion, Name: "caps", Nodes: []PipelineNode{tc.node}}
			err := topology.Validate()
			if err == nil {
				t.Fatal("Validate accepted a declaration that would be inert")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestValidateAcceptsMeaningfulAndDisabledPullFlags pins the other side: a
// prompt node's pull_memory is meaningful, an explicit false anywhere is a
// no-op, and the model-backed builtins keep both flags.
func TestValidateAcceptsMeaningfulAndDisabledPullFlags(t *testing.T) {
	valid := []struct {
		name string
		node PipelineNode
	}{
		{"prompt pull_memory", PipelineNode{Name: "note", Type: NodeTypePrompt, Prompt: "x", Caps: NodeCapabilities{PullMemory: boolPtr(true)}}},
		{"model-free explicit false", PipelineNode{Name: "lint", Type: "static_analyzer", Caps: NodeCapabilities{PullContext: boolPtr(false), PullMemory: boolPtr(false)}}},
		{"code_writer pull flags", PipelineNode{Name: "code_writer", Type: "code_writer", Caps: NodeCapabilities{PullContext: boolPtr(true), PullMemory: boolPtr(true)}}},
		{"code_writer disabled context", PipelineNode{Name: "code_writer", Type: "code_writer", Caps: NodeCapabilities{PullContext: boolPtr(false)}}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			topology := PipelineTopology{Version: TopologySchemaVersion, Name: "caps", Nodes: []PipelineNode{tc.node}}
			if err := topology.Validate(); err != nil {
				t.Fatalf("Validate rejected a meaningful declaration: %v", err)
			}
		})
	}
	// The embedded default declares no capabilities, so it still validates.
	if err := defaultTopologyValidationProbe().Validate(); err != nil {
		t.Fatalf("embedded default topology must validate: %v", err)
	}
}

// defaultTopologyValidationProbe builds a topology equivalent to the embedded
// default's node declarations. The splice package owns defaultTopology, so this
// schema test uses the same node shape.
func defaultTopologyValidationProbe() PipelineTopology {
	return PipelineTopology{
		Version: TopologySchemaVersion,
		Name:    "default",
		Nodes: []PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "test_generator", Type: "test_generator"},
			{Name: "static_analyzer", Type: "static_analyzer"},
			{Name: "security_auditor", Type: "security_auditor"},
			{Name: "test_runner", Type: "test_runner"},
			{Name: "acceptance_verifier", Type: "acceptance_verifier"},
		},
	}
}
