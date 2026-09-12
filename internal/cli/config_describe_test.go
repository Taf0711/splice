package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func writeDescribeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestConfigDescribeReportsOrigins pins the bounded describe report: the active
// pipeline and its source, the envelope and its origin, each node's resolved
// model with the ladder rung that supplied it, and the stage-models entries.
func TestConfigDescribeReportsOrigins(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	workspace := t.TempDir()

	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "team",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "team", Model: "node-model", ReasoningEffort: "high"}},
			{Name: "test_generator", Type: "test_generator"},
			{Name: "test_runner", Type: "test_runner"},
		},
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeDescribeFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), data)
	writeDescribeFile(t, filepath.Join(base, "splice", "config.json"), []byte(`{"active_pipeline":"team"}`))
	stageModels, err := json.MarshalIndent(schemas.StageModelConfigFile{
		Default: schemas.StageModelConfig{ProviderProfile: "fallback", Model: "fallback-model"},
		Stages:  map[string]schemas.StageModelConfig{"test_generator": {ProviderProfile: "stage", Model: "stage-model"}},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeDescribeFile(t, filepath.Join(base, "splice", "stage-models.json"), stageModels)

	deps := appDeps{
		getwd:          func() (string, error) { return workspace, nil },
		userConfigPath: func() (string, error) { return filepath.Join(base, "splice", "config.json"), nil },
	}
	var stdout, stderr bytes.Buffer
	if code := runConfigDescribe(nil, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"active pipeline: team (user library)",
		"envelope (standard):",
		"origin: tier envelope",
		"code_writer type=code_writer",
		"model=team/node-model (high) origin=node",
		"test_generator",
		"model=stage/stage-model origin=stage",
		"test_runner",
		"caps=model_free:true pull_context:false pull_memory:false produces_verification:true",
		"stage-models.json:",
		"default: fallback/fallback-model",
		"test_generator: stage/stage-model",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("describe output missing %q:\n%s", want, out)
		}
	}
}

// TestConfigDescribeRejectsUnknownTier pins the tier validation.
func TestConfigDescribeRejectsUnknownTier(t *testing.T) {
	deps := appDeps{
		getwd:          func() (string, error) { return t.TempDir(), nil },
		userConfigPath: func() (string, error) { return filepath.Join(t.TempDir(), "config.json"), nil },
	}
	var stdout, stderr bytes.Buffer
	if code := runConfigDescribe([]string{"--tier", "bogus"}, &stdout, &stderr, deps); code == 0 {
		t.Fatal("describe accepted an unknown tier")
	}
	if !strings.Contains(stderr.String(), "unknown pipeline tier") {
		t.Fatalf("stderr = %q, want the tier error", stderr.String())
	}
}

// TestConfigDescribeEnvelopeOrigin pins that a topology budget override is
// reported as the envelope origin.
func TestConfigDescribeEnvelopeOrigin(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "capped",
		Budget:  &schemas.BudgetOverride{TotalInput: 100000, TotalOutput: 50000},
		Nodes:   []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer"}},
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeDescribeFile(t, filepath.Join(base, "splice", "pipelines", "capped.json"), data)
	writeDescribeFile(t, filepath.Join(base, "splice", "config.json"), []byte(`{"active_pipeline":"capped"}`))

	deps := appDeps{
		getwd:          func() (string, error) { return t.TempDir(), nil },
		userConfigPath: func() (string, error) { return filepath.Join(base, "splice", "config.json"), nil },
	}
	var stdout, stderr bytes.Buffer
	if code := runConfigDescribe(nil, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "origin: topology budget.total") {
		t.Fatalf("describe did not report the budget override origin:\n%s", stdout.String())
	}
}
