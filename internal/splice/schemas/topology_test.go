package schemas

import (
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

// validTopology returns a small valid topology that tests mutate.
func validTopology() PipelineTopology {
	return PipelineTopology{
		Version: TopologySchemaVersion,
		Name:    "test-graph",
		Nodes: []PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "static_analyzer", Type: "static_analyzer"},
		},
		Edges: []PipelineEdge{{From: "code_writer", To: "static_analyzer"}},
	}
}

func TestPipelineTopologyValidateAccepts(t *testing.T) {
	cases := []struct {
		name     string
		topology PipelineTopology
	}{
		{"default shape", validTopology()},
		{"single node no edges", PipelineTopology{Version: 1, Name: "one", Nodes: []PipelineNode{{Name: "code_writer", Type: "code_writer"}}}},
		{"prompt node", PipelineTopology{Version: 1, Name: "p", Nodes: []PipelineNode{{Name: "note", Type: NodeTypePrompt, Prompt: "summarize {{intent}}"}}}},
		{"command node", PipelineTopology{Version: 1, Name: "c", Nodes: []PipelineNode{{Name: "lint", Type: NodeTypeCommand, Command: []string{"golangci-lint", "./..."}}}}},
		{"tier filtered", PipelineTopology{Version: 1, Name: "t", Nodes: []PipelineNode{{Name: "lint", Type: NodeTypeCommand, Command: []string{"true"}, Tiers: []PipelineTier{TierSubstantial, TierArchitectural}}}}},
		{"explicit zero budget on model-free", PipelineTopology{Version: 1, Name: "z", Nodes: []PipelineNode{{Name: "static_analyzer", Type: "static_analyzer", Budget: &StageBudget{}}}}},
		{"explicit model budget on model-backed", PipelineTopology{Version: 1, Name: "m", Nodes: []PipelineNode{{Name: "code_writer", Type: "code_writer", Budget: &StageBudget{InputMax: 100, OutputMax: 50}}}}},
		{"budget override", PipelineTopology{Version: 1, Name: "b", Nodes: []PipelineNode{{Name: "code_writer", Type: "code_writer"}}, Budget: &BudgetOverride{TotalInput: 1000, TotalOutput: 500}}},
		{"disconnected nodes", PipelineTopology{Version: 1, Name: "d", Nodes: []PipelineNode{{Name: "code_writer", Type: "code_writer"}, {Name: "lint", Type: NodeTypeCommand, Command: []string{"true"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.topology.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestPipelineTopologyValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*PipelineTopology)
		wantSub string
	}{
		{"wrong version", func(t *PipelineTopology) { t.Version = 2 }, "version 2 is not supported"},
		{"empty version", func(t *PipelineTopology) { t.Version = 0 }, "version 0 is not supported"},
		{"empty name", func(t *PipelineTopology) { t.Name = " " }, "topology name is required"},
		{"no nodes", func(t *PipelineTopology) { t.Nodes = nil }, "at least one node is required"},
		{"duplicate node name", func(t *PipelineTopology) {
			t.Nodes = append(t.Nodes, PipelineNode{Name: "code_writer", Type: "code_writer"})
		}, `duplicate node name "code_writer"`},
		{"empty node name", func(t *PipelineTopology) { t.Nodes[0].Name = "" }, "node name is required"},
		{"unknown node type", func(t *PipelineTopology) { t.Nodes[0].Type = "wat" }, `unknown node type "wat"`},
		{"prompt missing text", func(t *PipelineTopology) { t.Nodes[0].Type = NodeTypePrompt }, "requires a non-empty prompt"},
		{"prompt with command", func(t *PipelineTopology) {
			t.Nodes[0].Type = NodeTypePrompt
			t.Nodes[0].Prompt = "x"
			t.Nodes[0].Command = []string{"true"}
		}, "command is only valid"},
		{"command missing command", func(t *PipelineTopology) { t.Nodes[0].Type = NodeTypeCommand }, "requires a non-empty command"},
		{"command with prompt", func(t *PipelineTopology) {
			t.Nodes[0].Type = NodeTypeCommand
			t.Nodes[0].Command = []string{"true"}
			t.Nodes[0].Prompt = "x"
		}, "prompt is only valid"},
		{"builtin with prompt", func(t *PipelineTopology) { t.Nodes[0].Prompt = "x" }, "prompt is only valid"},
		{"builtin with command", func(t *PipelineTopology) { t.Nodes[0].Command = []string{"true"} }, "command is only valid"},
		{"builtin marked model free", func(t *PipelineTopology) { t.Nodes[0].Caps.ModelFree = true }, "model_free must be false"},
		{"prompt marked model free", func(t *PipelineTopology) {
			t.Nodes[0] = PipelineNode{Name: "note", Type: NodeTypePrompt, Prompt: "x", Caps: NodeCapabilities{ModelFree: true}}
		}, "model_free must be false"},
		{"invalid tier", func(t *PipelineTopology) { t.Nodes[0].Tiers = []PipelineTier{"nope"} }, "unknown pipeline tier"},
		{"edge from unknown", func(t *PipelineTopology) { t.Edges[0].From = "ghost" }, `from "ghost" is not a node`},
		{"edge to unknown", func(t *PipelineTopology) { t.Edges[0].To = "ghost" }, `to "ghost" is not a node`},
		{"edge empty from", func(t *PipelineTopology) { t.Edges[0].From = "" }, "from is required"},
		{"edge empty to", func(t *PipelineTopology) { t.Edges[0].To = "" }, "to is required"},
		{"invalid payload", func(t *PipelineTopology) { t.Edges[0].Payload = "everything" }, "edge payload must be summary, output, or none"},
		{"duplicate edge", func(t *PipelineTopology) { t.Edges = append(t.Edges, t.Edges[0]) }, "duplicate edge"},
		{"cycle", func(t *PipelineTopology) {
			t.Edges = append(t.Edges, PipelineEdge{From: "static_analyzer", To: "code_writer"})
		}, "cycle detected"},
		{"self loop", func(t *PipelineTopology) { t.Edges = []PipelineEdge{{From: "code_writer", To: "code_writer"}} }, "cycle detected"},
		{"mixed zero budget", func(t *PipelineTopology) {
			t.Nodes[1].Budget = &StageBudget{InputMax: 10}
		}, "both be > 0"},
		{"model free with budget", func(t *PipelineTopology) {
			t.Nodes[1].Budget = &StageBudget{InputMax: 10, OutputMax: 10}
		}, "model-free node has a non-zero budget"},
		{"model backed with zero budget", func(t *PipelineTopology) {
			t.Nodes[0].Budget = &StageBudget{}
		}, "model-backed node has a zero budget"},
		{"invalid model config", func(t *PipelineTopology) {
			t.Nodes[0].Model = &StageModelConfig{Model: "x"}
		}, "provider_profile is required"},
		{"invalid budget override", func(t *PipelineTopology) {
			t.Budget = &BudgetOverride{TotalInput: 0, TotalOutput: 10}
		}, "budget.total_input must be > 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topology := validTopology()
			tc.mutate(&topology)
			err := topology.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("Validate() = %q, want error containing %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestEdgePayloadEffective(t *testing.T) {
	if got := EdgePayload("").Effective(); got != EdgePayloadSummary {
		t.Fatalf("empty payload Effective() = %q, want %q", got, EdgePayloadSummary)
	}
	if got := EdgePayloadNone.Effective(); got != EdgePayloadNone {
		t.Fatalf("none payload Effective() = %q, want %q", got, EdgePayloadNone)
	}
}

func TestNodeEffectiveCapabilities(t *testing.T) {
	builtin := PipelineNode{Name: "code_writer", Type: "code_writer"}
	caps := builtin.EffectiveCapabilities()
	if caps.ModelFree {
		t.Fatalf("code_writer ModelFree = true, want false")
	}
	if !builtin.PullsContext() {
		t.Fatalf("code_writer PullsContext() = false, want true")
	}
	if !builtin.PullsMemory() {
		t.Fatalf("code_writer PullsMemory() = false, want true")
	}

	verifier := PipelineNode{Name: "test_runner", Type: "test_runner"}
	if !verifier.EffectiveCapabilities().ProducesVerification {
		t.Fatalf("test_runner ProducesVerification = false, want true")
	}
	if verifier.PullsContext() {
		t.Fatalf("test_runner PullsContext() = true, want false")
	}

	command := PipelineNode{Name: "lint", Type: NodeTypeCommand, Command: []string{"true"}}
	if !command.EffectiveCapabilities().ModelFree {
		t.Fatalf("command ModelFree = false, want true")
	}
	if command.PullsContext() || command.PullsMemory() {
		t.Fatalf("command default pull flags must be false")
	}

	prompt := PipelineNode{Name: "note", Type: NodeTypePrompt, Prompt: "x"}
	if prompt.EffectiveCapabilities().ModelFree {
		t.Fatalf("prompt ModelFree = true, want false")
	}

	override := PipelineNode{Name: "note", Type: NodeTypePrompt, Prompt: "x", Caps: NodeCapabilities{PullContext: boolPtr(true), PullMemory: boolPtr(true)}}
	if !override.PullsContext() || !override.PullsMemory() {
		t.Fatalf("explicit pull overrides were not applied")
	}

	customVerify := PipelineNode{Name: "check", Type: NodeTypeCommand, Command: []string{"true"}, Caps: NodeCapabilities{ProducesVerification: true}}
	if !customVerify.EffectiveCapabilities().ProducesVerification {
		t.Fatalf("explicit produces_verification was not applied")
	}
}

func TestNodeActiveAtTier(t *testing.T) {
	all := PipelineNode{Name: "code_writer", Type: "code_writer"}
	for _, tier := range []PipelineTier{TierTrivial, TierLight, TierStandard, TierSubstantial, TierArchitectural} {
		if !all.ActiveAtTier(tier) {
			t.Fatalf("node with empty tiers must be active at %s", tier)
		}
	}
	restricted := PipelineNode{Name: "sec", Type: "security_auditor", Tiers: []PipelineTier{TierSubstantial, TierArchitectural}}
	if restricted.ActiveAtTier(TierLight) {
		t.Fatalf("restricted node active at light, want false")
	}
	if !restricted.ActiveAtTier(TierSubstantial) {
		t.Fatalf("restricted node inactive at substantial, want true")
	}
}

func TestPipelineTierValidate(t *testing.T) {
	if err := TierSubstantial.Validate(); err != nil {
		t.Fatalf("TierSubstantial.Validate() = %v, want nil", err)
	}
	if err := PipelineTier("x").Validate(); err == nil {
		t.Fatalf("unknown tier Validate() = nil, want error")
	}
}
