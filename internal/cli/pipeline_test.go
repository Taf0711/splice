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

func writePipelineFile(t *testing.T, path, name string) {
	t.Helper()
	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    name,
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "local", Model: "qwen-node", ReasoningEffort: "high"}},
			{Name: "lint_cmd", Type: schemas.NodeTypeCommand, Command: []string{"true", "--strict"}},
		},
		Edges: []schemas.PipelineEdge{{From: "code_writer", To: "lint_cmd", Payload: schemas.EdgePayloadNone}},
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPipelineCommandShowNamed pins that `pipeline show <name>` reads a library
// topology and lists its nodes, edges, and command nodes.
func TestPipelineCommandShowNamed(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writePipelineFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")

	var stdout, stderr bytes.Buffer
	code := runPipelineCommand([]string{"show", "team"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"topology: team", "code_writer", "lint_cmd", "command nodes:", "true --strict", "code_writer -> lint_cmd (none)", "model: local/qwen-node (high)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
	}
}

// TestPipelineCommandShowMissingFailsLoud pins the missing-reference case.
func TestPipelineCommandShowMissingFailsLoud(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runPipelineCommand([]string{"show", "ghost"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("show of a missing topology exited 0")
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Fatalf("stderr = %q, want a not-found error", stderr.String())
	}
}

// TestPipelineCommandListResolvesActive pins that `pipeline list` reports the
// active topology and the library entries.
func TestPipelineCommandListResolvesActive(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writePipelineFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")
	if err := os.MkdirAll(filepath.Join(base, "splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "splice", "config.json"), []byte(`{"active_pipeline":"team"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var stdout, stderr bytes.Buffer
	code := runPipelineCommand([]string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "active: team (user library)") {
		t.Fatalf("list output missing the active library topology:\n%s", out)
	}
	if !strings.Contains(out, "team (active)") {
		t.Fatalf("list output missing the active marker:\n%s", out)
	}
}

// TestPipelineCommandUnknownVerb pins the usage error.
func TestPipelineCommandUnknownVerb(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPipelineCommand([]string{"frobnicate"}, &stdout, &stderr); code == 0 {
		t.Fatal("unknown verb exited 0")
	}
	if !strings.Contains(stderr.String(), "unknown pipeline subcommand") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestParseExecArgsPipeline pins the --pipeline flag parse, both separated and
// inline, and the missing-value refusal.
func TestParseExecArgsPipeline(t *testing.T) {
	options, _, err := parseExecArgs([]string{"--pipeline", "team", "hello"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if options.pipeline != "team" {
		t.Fatalf("pipeline = %q, want team", options.pipeline)
	}
	inline, _, err := parseExecArgs([]string{"--pipeline=path/to/p.json", "hello"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if inline.pipeline != "path/to/p.json" {
		t.Fatalf("inline pipeline = %q, want path/to/p.json", inline.pipeline)
	}
	if _, _, err := parseExecArgs([]string{"--pipeline"}); err == nil {
		t.Fatal("--pipeline without a value did not error")
	}
}
