package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestStageTierLabelsDefaults pins the compatibility entry point: with no
// active topology the roster is exactly the historical one.
func TestStageTierLabelsDefaults(t *testing.T) {
	labels := StageTierLabels()
	want := map[string]string{
		"code_writer":        "medium",
		"test_generator":     "medium",
		"design_crystallize": "medium",
		"plan_critic":        "reasoning",
	}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for name, label := range want {
		if labels[name] != label {
			t.Fatalf("labels[%s] = %q, want %q", name, labels[name], label)
		}
	}
}

// TestStageTierLabelsForTopology pins that the enumeration reads the active
// topology: a custom model-backed node appears, a model-free node does not,
// and the design stages survive because they are never topology nodes.
func TestStageTierLabelsForTopology(t *testing.T) {
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "custom",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "summarizer", Type: schemas.NodeTypePrompt, Prompt: "summarize {{intent}}"},
			{Name: "lint", Type: "static_analyzer"},
		},
	}
	labels := StageTierLabelsFor(topology)
	if labels["summarizer"] != "medium" {
		t.Fatalf("custom prompt node label = %q, want medium", labels["summarizer"])
	}
	if labels["code_writer"] != "medium" {
		t.Fatalf("known node label = %q, want medium", labels["code_writer"])
	}
	if _, ok := labels["lint"]; ok {
		t.Fatalf("model-free node lint was enumerated: %v", labels)
	}
	if labels["design_crystallize"] != "medium" || labels["plan_critic"] != "reasoning" {
		t.Fatalf("design stages missing from labels: %v", labels)
	}
}
