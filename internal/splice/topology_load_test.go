package splice

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/projectfile"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/version"
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

	t.Run("trusted project used when no flag", func(t *testing.T) {
		writeTopologyFile(t, projectPath, "project")
		writeTopologyFile(t, filepath.Join(libraryDir, "active.json"), "library")
		topology, source, warnings, err := ResolveTopology(TopologySources{
			WorkspaceRoot: workspace, Trusted: true,
			ActiveName: "active", UserConfigDir: base,
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

	t.Run("flag wins over trusted project with a notice", func(t *testing.T) {
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
		if topology.Name != "flag" || source != TopologySourceFlag {
			t.Fatalf("resolved %q from %q, want the flag layer", topology.Name, source)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], projectPath) || !strings.Contains(warnings[0], "--pipeline") {
			t.Fatalf("warnings = %v, want one notice naming the overridden project file %s", warnings, projectPath)
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

// TestResolveTopologyIgnoresInvalidUntrustedProject pins M2: the project file
// is parsed only after trust resolution, so a malformed untrusted file is
// ignored with a warning instead of failing the run.
func TestResolveTopologyIgnoresInvalidUntrustedProject(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".splice", "pipeline.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	topology, source, warnings, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: false, UserConfigDir: t.TempDir()})
	if err != nil {
		t.Fatalf("ResolveTopology: %v", err)
	}
	if topology.Name != "default" || source != TopologySourceDefault {
		t.Fatalf("resolved %q from %q, want the embedded default", topology.Name, source)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "untrusted") {
		t.Fatalf("warnings = %v, want one untrusted-project warning", warnings)
	}
}

// TestResolveTopologyEnforcesMinSplice pins M1 at the loader: a topology that
// requires a newer Splice fails loud, an untrusted file is ignored before the
// check, and a dev build skips the check so local work is not blocked.
func TestResolveTopologyEnforcesMinSplice(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".splice", "pipeline.json")
	topology := schemas.PipelineTopology{
		Version:   schemas.TopologySchemaVersion,
		Name:      "needs-newer",
		MinSplice: "99.0.0",
		Nodes:     []schemas.PipelineNode{{Name: "code_writer", Type: "code_writer"}},
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

	original := version.Version
	t.Cleanup(func() { version.Version = original })

	t.Run("release build below the requirement fails loud", func(t *testing.T) {
		version.Version = "0.9.0"
		_, _, _, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: true, UserConfigDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "requires Splice") {
			t.Fatalf("error = %v, want a minimum-version error", err)
		}
	})
	t.Run("release build at the requirement passes", func(t *testing.T) {
		version.Version = "99.0.0"
		resolved, source, _, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: true, UserConfigDir: t.TempDir()})
		if err != nil {
			t.Fatalf("ResolveTopology: %v", err)
		}
		if resolved.Name != "needs-newer" || source != TopologySourceProject {
			t.Fatalf("resolved %q from %q, want the project topology", resolved.Name, source)
		}
	})
	t.Run("untrusted file is ignored before the check", func(t *testing.T) {
		version.Version = "0.9.0"
		resolved, source, warnings, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: false, UserConfigDir: t.TempDir()})
		if err != nil {
			t.Fatalf("ResolveTopology: %v", err)
		}
		if resolved.Name != "default" || source != TopologySourceDefault {
			t.Fatalf("resolved %q from %q, want the embedded default", resolved.Name, source)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "untrusted") {
			t.Fatalf("warnings = %v, want one untrusted warning", warnings)
		}
	})
	t.Run("dev build skips the check", func(t *testing.T) {
		version.Version = "dev"
		resolved, source, _, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: true, UserConfigDir: t.TempDir()})
		if err != nil {
			t.Fatalf("ResolveTopology: %v", err)
		}
		if resolved.Name != "needs-newer" || source != TopologySourceProject {
			t.Fatalf("resolved %q from %q, want the project topology", resolved.Name, source)
		}
	})
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
	plan, err := BuildExecutionPlanWithTopology(topology, "add a function", nil)
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
	fallback, err := BuildExecutionPlanWithTopology(nil, "add a function", nil)
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

// TestTopologyRootPrefersTrustRoot pins the rule that picks the directory the
// project topology loads from: TrustRoot, then ProjectRoot, then Cwd.
func TestTopologyRootPrefersTrustRoot(t *testing.T) {
	tests := []struct {
		name    string
		options agent.Options
		want    string
	}{
		{"trust root wins", agent.Options{TrustRoot: "/trusted", ProjectRoot: "/repo", Cwd: "/repo/sub"}, "/trusted"},
		{"project root next", agent.Options{ProjectRoot: "/repo", Cwd: "/repo/sub"}, "/repo"},
		{"cwd last", agent.Options{Cwd: "/repo/sub"}, "/repo/sub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topologyRoot(tt.options); got != tt.want {
				t.Fatalf("topologyRoot() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRunTopologyIgnoresParentProjectFileWhenTrustRootIsSubfolder proves that
// trusting a subfolder does not load the parent repo's command topology. The
// control case shows the same parent file loads when the parent is trusted.
func TestRunTopologyIgnoresParentProjectFileWhenTrustRootIsSubfolder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := t.TempDir()
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	parentFile := filepath.Join(repo, ".splice", "pipeline.json")
	if err := os.MkdirAll(filepath.Dir(parentFile), 0o755); err != nil {
		t.Fatal(err)
	}
	const lintOnly = `{"version": 1, "name": "lint-only", "nodes": [{"name": "lint", "type": "command", "command": ["go", "vet", "./..."]}]}`
	if err := os.WriteFile(parentFile, []byte(lintOnly), 0o644); err != nil {
		t.Fatal(err)
	}

	control, source, _, err := resolveRunTopology(agent.Options{ProjectRoot: repo, Cwd: sub, TrustedWorkspace: true})
	if err != nil {
		t.Fatalf("control resolve: %v", err)
	}
	if control.Name != "lint-only" || source != TopologySourceProject {
		t.Fatalf("control resolved %q from %q, want lint-only from the project file", control.Name, source)
	}

	topology, source, _, err := resolveRunTopology(agent.Options{ProjectRoot: repo, TrustRoot: sub, Cwd: sub, TrustedWorkspace: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if topology.Name == "lint-only" || source != TopologySourceDefault {
		t.Fatalf("resolved %q from %q, want the embedded default", topology.Name, source)
	}
}

func TestResolveTopologyRefusesOversizedOrEscapingTrustedProject(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".splice", "pipeline.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", projectfile.MaxBytes)
	if err := os.WriteFile(path, []byte(`{"pad":"`+pad+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: true})
	if !errors.Is(err, projectfile.ErrTooLarge) {
		t.Fatalf("oversized trusted topology: want ErrTooLarge, got %v", err)
	}

	outside := filepath.Join(t.TempDir(), "pipeline.json")
	writeTopologyFile(t, outside, "escaped")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, _, _, err = ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: true})
	if !errors.Is(err, projectfile.ErrOutsideRoot) {
		t.Fatalf("escaping trusted topology: want ErrOutsideRoot, got %v", err)
	}
}

// TestResolveTopologyFailsLoudOnUnresolvedRequest pins L1: a topology that the
// user asked for by name or by path, and that does not exist, is an error. It
// must never fall back to the embedded default.
func TestResolveTopologyFailsLoudOnUnresolvedRequest(t *testing.T) {
	base := t.TempDir()
	missingPath := filepath.Join(t.TempDir(), "absent.json")

	tests := []struct {
		name    string
		sources TopologySources
		want    []string
	}{
		{"missing --pipeline name", TopologySources{FlagName: "nosuchpipe", UserConfigDir: base}, []string{"--pipeline nosuchpipe", "nosuchpipe.json"}},
		{"missing --pipeline path", TopologySources{FlagName: missingPath, UserConfigDir: base}, []string{"--pipeline " + missingPath, missingPath}},
		{"missing --pipeline flag path", TopologySources{FlagPath: missingPath, UserConfigDir: base}, []string{"--pipeline " + missingPath, missingPath}},
		{"missing active library entry", TopologySources{ActiveName: "ghost", UserConfigDir: base}, []string{"active_pipeline ghost", "ghost.json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, source, _, err := ResolveTopology(tt.sources)
			if err == nil {
				t.Fatalf("ResolveTopology fell back to %q; want an error", source)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to name %q", err, want)
				}
			}
		})
	}
}

// TestResolveTopologyFailsLoudWithoutUserConfigDir pins that a requested
// library topology is not resolved against the working directory when the
// per-user config directory cannot be found.
func TestResolveTopologyFailsLoudWithoutUserConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	workspace := t.TempDir()
	writeTopologyFile(t, filepath.Join(workspace, "team.json"), "cwd-copy")
	t.Chdir(workspace)
	_, _, _, err := ResolveTopology(TopologySources{FlagName: "team"})
	if err == nil || !strings.Contains(err.Error(), "user library") {
		t.Fatalf("error = %v, want a user-library error", err)
	}
}

// TestDecodeTopologyRefusesUnknownFields pins L3: a misspelled key anywhere in
// a topology fails loud and names the field. Before the fix, a dropped pin was
// accepted with no error.
func TestDecodeTopologyRefusesUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"top-level edges typo", `{"version":1,"name":"x","nodes":[{"name":"code_writer","type":"code_writer"}],"edgez":[]}`, "edgez"},
		{"node model typo", `{"version":1,"name":"x","nodes":[{"name":"code_writer","type":"code_writer","modle":{"provider":"p"}}]}`, "modle"},
		{"node tiers typo", `{"version":1,"name":"x","nodes":[{"name":"code_writer","type":"code_writer","teirs":["light"]}]}`, "teirs"},
		{"trailing data", `{"version":1,"name":"x","nodes":[{"name":"code_writer","type":"code_writer"}]} {}`, "unexpected data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeTopology([]byte(tt.doc))
			if err == nil {
				t.Fatal("DecodeTopology accepted the document")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to name %q", err, tt.want)
			}
		})
	}
}

// TestDecodeTopologyNamesMinVersionBeforeUnknownFields pins D1: a file written
// for a newer Splice that uses a new field reports the required version, not
// the unknown field. A file with no minimum still refuses the unknown field.
func TestDecodeTopologyNamesMinVersionBeforeUnknownFields(t *testing.T) {
	original := version.Version
	t.Cleanup(func() { version.Version = original })
	version.Version = "0.3.0"

	t.Run("unknown field without a minimum is refused by name", func(t *testing.T) {
		doc := `{"version":1,"name":"x","nodes":[{"name":"code_writer","type":"code_writer","future":true}]}`
		_, err := DecodeTopology([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), "future") {
			t.Fatalf("error = %v, want the unknown field named", err)
		}
	})
	t.Run("unknown field with a newer minimum names the required version", func(t *testing.T) {
		doc := `{"version":1,"name":"x","splice_min_version":"9.0.0","nodes":[{"name":"code_writer","type":"code_writer","future":true}]}`
		_, err := DecodeTopology([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), "requires Splice 9.0.0") || !strings.Contains(err.Error(), "0.3.0") {
			t.Fatalf("error = %v, want a minimum-version error naming 9.0.0 and 0.3.0", err)
		}
		if strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("error = %v, must not report the unknown field first", err)
		}
	})
	t.Run("typo inside a nested node object is still refused", func(t *testing.T) {
		doc := `{"version":1,"name":"x","splice_min_version":"0.1.0","nodes":[{"name":"code_writer","type":"code_writer","modle":{"provider":"p"}}]}`
		_, err := DecodeTopology([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), "modle") {
			t.Fatalf("error = %v, want the nested typo named", err)
		}
	})
}

// TestResolveTopologyRefusesUnknownFieldInProject pins that a trusted project
// file with a misspelled key fails the run instead of loading without the key.
func TestResolveTopologyRefusesUnknownFieldInProject(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".splice", "pipeline.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"version":1,"name":"typo","nodes":[{"name":"code_writer","type":"code_writer","modle":{"provider":"p"}}]}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ResolveTopology(TopologySources{WorkspaceRoot: workspace, Trusted: true, UserConfigDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "modle") {
		t.Fatalf("error = %v, want the unknown field named", err)
	}
}

// TestDecodeTopologyChecksEveryTier pins L4: a topology whose nodes exceed one
// tier's envelope is refused at load, before any tokens are spent, and the
// error names the tier and the real budget field.
func TestDecodeTopologyChecksEveryTier(t *testing.T) {
	light, err := BudgetForTier(schemas.TierLight)
	if err != nil {
		t.Fatal(err)
	}
	over := light.TotalInputBudget + 1
	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "fat-light",
		Nodes: []schemas.PipelineNode{
			{Name: "code_writer", Type: "code_writer"},
			{Name: "fat", Type: "code_writer", Tiers: []schemas.PipelineTier{schemas.TierLight},
				Budget: &schemas.StageBudget{InputMax: over, OutputMax: 1}},
		},
	}
	data, err := json.Marshal(topology)
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeTopology(data)
	if err == nil {
		t.Fatal("DecodeTopology accepted a topology over the light envelope")
	}
	if !strings.Contains(err.Error(), "tier light") || !strings.Contains(err.Error(), "budget.total_input") {
		t.Fatalf("error = %v, want the light tier and budget.total_input named", err)
	}
	// The message must also state the node budget total, the envelope, and the
	// two ways out: a node "tiers" list or a larger envelope.
	for _, want := range []string{
		fmt.Sprintf("node budgets total %d input tokens", stageBudgets(schemas.TierLight)["code_writer"].InputMax+over),
		fmt.Sprintf("over the %d input envelope", light.TotalInputBudget),
		`"tiers" on the nodes`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to contain %q", err, want)
		}
	}
}

// TestDecodeTopologyRefusesTierWithNoActiveNode pins that a topology with no
// active node at one tier fails at load, not at the first run at that tier.
func TestDecodeTopologyRefusesTierWithNoActiveNode(t *testing.T) {
	topology := schemas.PipelineTopology{
		Version: schemas.TopologySchemaVersion,
		Name:    "substantial-only",
		Nodes: []schemas.PipelineNode{
			{Name: "sec", Type: "security_auditor", Tiers: []schemas.PipelineTier{schemas.TierSubstantial}},
		},
	}
	data, err := json.Marshal(topology)
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeTopology(data)
	if err == nil || !strings.Contains(err.Error(), "tier trivial") {
		t.Fatalf("error = %v, want the trivial tier named", err)
	}
}

// TestDefaultNameSelectsEmbeddedTopology pins the reserved name. A library
// entry named default.json never shadows the embedded topology, whether the
// name comes from --pipeline or from active_pipeline.
func TestDefaultNameSelectsEmbeddedTopology(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writeTopologyFile(t, filepath.Join(base, "splice", "pipelines", "default.json"), "library-default")
	embedded := defaultTopology()

	for name, sources := range map[string]TopologySources{
		"--pipeline default":   {FlagName: "default", UserConfigDir: base, WorkspaceRoot: t.TempDir()},
		"active_pipeline":      {ActiveName: "default", UserConfigDir: base, WorkspaceRoot: t.TempDir()},
		"active with no dirs":  {ActiveName: "default", WorkspaceRoot: t.TempDir()},
		"flag with trusted ws": {FlagName: "default", UserConfigDir: base, WorkspaceRoot: t.TempDir(), Trusted: true},
	} {
		t.Run(name, func(t *testing.T) {
			topology, source, _, err := ResolveTopology(sources)
			if err != nil {
				t.Fatalf("ResolveTopology: %v", err)
			}
			if source != TopologySourceDefault || topology.Name != embedded.Name || len(topology.Nodes) != len(embedded.Nodes) {
				t.Fatalf("resolved %q from %q with %d nodes, want the embedded default", topology.Name, source, len(topology.Nodes))
			}
		})
	}

	topology, source, err := LoadNamedTopology("default")
	if err != nil {
		t.Fatalf("LoadNamedTopology(default): %v", err)
	}
	if source != TopologySourceDefault || len(topology.Nodes) != len(embedded.Nodes) {
		t.Fatalf("LoadNamedTopology(default) = %q from %q, want the embedded default", topology.Name, source)
	}
}

// TestDocsExampleTopologiesLoad keeps the published examples loadable: every
// docs/examples/pipeline-*.json file must pass the same decode check that
// import and the run loader use. The check compiles the topology at every tier.
func TestDocsExampleTopologiesLoad(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "docs", "examples", "pipeline-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no docs/examples/pipeline-*.json files found")
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeTopology(data); err != nil {
			t.Errorf("DecodeTopology(%s): %v", path, err)
		}
	}
}

// TestResolveTopologyFailsLoudOnUnparsableConfig pins that a config.json
// that exists but does not parse never drops active_pipeline silently: the
// resolve fails and names the file. A --pipeline flag still outranks it.
func TestResolveTopologyFailsLoudOnUnparsableConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	configPath := filepath.Join(base, "splice", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"active_pipeline": "te`), 0o600); err != nil {
		t.Fatal(err)
	}
	sources := TopologySourcesFor(t.TempDir(), true)
	if _, _, _, err := ResolveTopology(sources); err == nil || !strings.Contains(err.Error(), configPath) {
		t.Fatalf("err = %v, want a failure naming %s", err, configPath)
	}
	sources.FlagName = DefaultTopologyName
	if topology, _, _, err := ResolveTopology(sources); err != nil || topology.Name != DefaultTopologyName {
		t.Fatalf("--pipeline default with a broken config: topology %v err %v, want the default", topology, err)
	}
}

// TestSetActiveLibraryNameWritesAtomically pins the config write: other keys
// and the file mode survive, and no temporary file is left behind.
func TestSetActiveLibraryNameWritesAtomically(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	dir := filepath.Join(base, "splice")
	configPath := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"model":"x"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := setActiveLibraryName("team"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"active_pipeline": "team"`) || !strings.Contains(string(data), `"model": "x"`) {
		t.Fatalf("config.json = %s, want both keys", data)
	}
	if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v (err %v), want 0640 kept", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries, want only config.json", len(entries))
	}
}

// TestPipelineDocAgentNodeSectionMatchesSchema keeps the agent node section of
// docs/PIPELINE.md in step with the schema: every eligible tool is listed, in
// the schema's order, and every agent field is named in the field table.
func TestPipelineDocAgentNodeSectionMatchesSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "PIPELINE.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start := strings.Index(doc, "### Agent nodes")
	end := strings.Index(doc, "### Capabilities")
	if start < 0 || end < start {
		t.Fatal("docs/PIPELINE.md has no agent node section between Agent nodes and Capabilities")
	}
	section := doc[start:end]
	for _, tool := range schemas.AgentEligibleTools {
		if !strings.Contains(section, "- `"+tool+"`\n") {
			t.Errorf("agent node section does not list eligible tool %q", tool)
		}
	}
	for _, field := range []string{"`tools`", "`prompt`", "`max_turns`", "`permission`"} {
		if !strings.Contains(section, field) {
			t.Errorf("agent node section does not document field %s", field)
		}
	}
	if !strings.Contains(section, "`splice_min_version` to `"+schemas.AgentNodeMinSplice+"`") {
		t.Errorf("agent node section does not state the minimum version %s", schemas.AgentNodeMinSplice)
	}
}
