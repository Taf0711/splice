package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
			// The explicit budget keeps the node inside the trivial envelope. The
			// default prompt budget (3000 input) would fail the topology load.
			{Name: "summarizer", Type: schemas.NodeTypePrompt, Prompt: "summarize {{intent}}", Budget: &schemas.StageBudget{InputMax: 500, OutputMax: 500}},
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

// TestSetupPipelineShowsTopologyError pins that a malformed active topology is
// visible in the stage-model wizard. The wizard keeps the default stages, and
// it also shows one warning line that names the error.
func TestSetupPipelineShowsTopologyError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m, _ := newSetupModelForPipeline(t, "gpt-5.6-sol")
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".splice", "pipeline.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"name":"broken",`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.cwd = workspace
	m.trusted = true

	view := plainRender(t, m.setupView(100))
	if !strings.Contains(view, "pipeline topology error:") || !strings.Contains(view, "showing the default stages") {
		t.Fatalf("wizard hid the topology error:\n%s", view)
	}
	if !strings.Contains(view, "code_writer:") {
		t.Fatalf("wizard lost the default stage rows:\n%s", view)
	}
	if _, err := m.stageTierLabelsWithError(); err == nil {
		t.Fatal("stageTierLabelsWithError() returned no error for a malformed topology")
	}
}
