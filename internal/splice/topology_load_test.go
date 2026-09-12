package splice

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func writeTopologyFile(t *testing.T, path, name string) {
	t.Helper()
	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    name,
		Nodes:   []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer"}},
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		t.Fatalf("marshal topology: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write topology: %v", err)
	}
}

func TestResolveTopologyPrecedence(t *testing.T) {
	base := t.TempDir()
	workspace := t.TempDir()
	libraryDir := filepath.Join(base, "splice", "pipelines")
	projectPath := filepath.Join(workspace, ".splice", "pipeline.json")
	flagPath := filepath.Join(t.TempDir(), "flag.json")

	t.Run("trusted project wins", func(t *testing.T) {
		writeTopologyFile(t, projectPath, "project")
		writeTopologyFile(t, flagPath, "flag")
		writeTopologyFile(t, filepath.Join(libraryDir, "active.json"), "library")
		topology, source, warnings, err := ResolveTopology(TopologySources{
			WorkspaceRoot: workspace, Trusted: true,
			FlagPath: flagPath, ActiveName: "active", UserConfigDir: base,
		})
		if err != nil {
			t.Fatalf("ResolveTopology: %v", err)
		}
		if topology.Name != "project" || source != TopologySourceProject {
			t.Fatalf("resolved %q from %q, want project", topology.Name, source)
		}
		if len(warnings) != 0 {
			t.Fatalf("warnings = %v, want none", warnings)
		}
	})

	t.Run("untrusted project is ignored with a warning", func(t *testing.T) {
		writeTopologyFile(t, projectPath, "project")
		writeTopologyFile(t, flagPath, "flag")
		topology, source, warnings, err := ResolveTopology(TopologySources{
			WorkspaceRoot: workspace, Trusted: false, FlagPath: flagPath, UserConfigDir: base,
		})
		if err != nil {
			t.Fatalf("ResolveTopology: %v", err)
		}
		if topology.Name != "flag" || source != TopologySourceFlag {
			t.Fatalf("resolved %q from %q, want the flag layer", topology.Name, source)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "untrusted") {
			t.Fatalf("warnings = %v, want one untrusted-project warning", warnings)
		}
	})

	t.Run("active library used when no project or flag", func(t *testing.T) {
		writeTopologyFile(t, filepath.Join(libraryDir, "active.json"), "library")
		topology, source, _, err := ResolveTopology(TopologySources{
			WorkspaceRoot: t.TempDir(), ActiveName: "active", UserConfigDir: base,
		})
		if err != nil {
			t.Fatalf("ResolveTopology: %v", err)
		}
		if topology.Name != "library" || source != TopologySourceLibrary {
			t.Fatalf("resolved %q from %q, want the library layer", topology.Name, source)
		}
	})

	t.Run("embedded default when nothing is set", func(t *testing.T) {
		topology, source, _, err := ResolveTopology(TopologySources{
			WorkspaceRoot: t.TempDir(), UserConfigDir: base,
		})
		if err != nil {
			t.Fatalf("ResolveTopology: %v", err)
		}
		if topology.Name != "default" || source != TopologySourceDefault {
			t.Fatalf("resolved %q from %q, want the embedded default", topology.Name, source)
		}
	})
}

func TestResolveTopologyFailsLoudOnInvalidTrustedProject(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".splice", "pipeline.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: true})
	if err == nil {
		t.Fatal("ResolveTopology accepted a malformed trusted project file")
	}
	if !strings.Contains(err.Error(), "project topology") {
		t.Fatalf("error = %v, want the project file named", err)
	}
}

func TestResolveTopologyRejectsInvalidTopology(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".splice", "pipeline.json")
	data := []byte(`{"version":1,"name":"bad","nodes":[]}`)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: true}); err == nil {
		t.Fatal("ResolveTopology accepted a topology with no nodes")
	}
}

// TestTopologySourcesForReadsActivePipeline pins that config.json's
// active_pipeline pointer reaches the resolver.
func TestTopologySourcesForReadsActivePipeline(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	configPath := filepath.Join(base, "splice", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"active_pipeline":"team"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := TopologySourcesFor(t.TempDir(), true)
	if sources.ActiveName != "team" {
		t.Fatalf("active name = %q, want team", sources.ActiveName)
	}
}

// TestBuildExecutionPlanWithTopologyCarriesName pins that the resolved
// topology reaches the plan and the result.
func TestBuildExecutionPlanWithTopologyCarriesName(t *testing.T) {
	topology := &schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "custom",
		Nodes:   []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer"}},
	}
	plan, err := BuildExecutionPlanWithTopology(topology, "add a function")
	if err != nil {
		t.Fatalf("BuildExecutionPlanWithTopology: %v", err)
	}
	if plan.TopologyName != "custom" {
		t.Fatalf("plan topology name = %q, want custom", plan.TopologyName)
	}
	if len(plan.Stages) != 1 || plan.Stages[0].Name != "code_writer" {
		t.Fatalf("plan stages = %+v, want only code_writer", plan.Stages)
	}
	// A nil topology keeps the embedded default.
	fallback, err := BuildExecutionPlanWithTopology(nil, "add a function")
	if err != nil {
		t.Fatalf("BuildExecutionPlanWithTopology(nil): %v", err)
	}
	if fallback.TopologyName != "default" {
		t.Fatalf("fallback topology name = %q, want default", fallback.TopologyName)
	}
}

// TestResolveTopologyFlagName pins that --pipeline resolves to the user library
// by name and directly when it is a path.
func TestResolveTopologyFlagName(t *testing.T) {
	base := t.TempDir()
	writeTopologyFile(t, filepath.Join(base, "splice", "pipelines", "team.json"), "team")
	topology, source, _, err := ResolveTopology(TopologySources{FlagName: "team", UserConfigDir: base})
	if err != nil {
		t.Fatalf("ResolveTopology: %v", err)
	}
	if topology.Name != "team" || source != TopologySourceFlag {
		t.Fatalf("resolved %q from %q, want team from the flag layer", topology.Name, source)
	}
	path := filepath.Join(t.TempDir(), "path-team.json")
	writeTopologyFile(t, path, "path-team")
	topology, _, _, err = ResolveTopology(TopologySources{FlagName: path, UserConfigDir: base})
	if err != nil {
		t.Fatalf("ResolveTopology(path): %v", err)
	}
	if topology.Name != "path-team" {
		t.Fatalf("resolved %q, want path-team", topology.Name)
	}
}

// TestLoadNamedTopologyMissingFailsLoud pins the missing-reference error.
func TestLoadNamedTopologyMissingFailsLoud(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _, err := LoadNamedTopology("ghost")
	if err == nil {
		t.Fatal("LoadNamedTopology accepted a missing reference")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v, want a not-found error", err)
	}
}
