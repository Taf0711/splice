package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestStageTierLabelsReadsActiveTopology pins the T4b wiring: the model wizard
// enumerates the active topology's model-backed nodes, and an untrusted
// project file does not.
func TestStageTierLabelsReadsActiveTopology(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".splice", "pipeline.json")
	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "custom",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "summarizer", Type: schemas.NodeTypePrompt, Prompt: "summarize {{intent}}"},
		},
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatalf("marshal topology: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	trusted := model{cwd: workspace, trusted: true}
	labels := trusted.stageTierLabels()
	if _, ok := labels["summarizer"]; !ok {
		t.Fatalf("active topology prompt node missing from the wizard: %v", labels)
	}
	if _, ok := labels["plan_critic"]; !ok {
		t.Fatalf("design stages missing from the wizard: %v", labels)
	}

	untrusted := model{cwd: workspace, trusted: false}
	if _, ok := untrusted.stageTierLabels()["summarizer"]; ok {
		t.Fatal("an untrusted project topology reached the model wizard")
	}
}
