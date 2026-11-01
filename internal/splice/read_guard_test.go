package splice

// Pairing test for the strict read-before-write guard at the exact production
// seam: the pipeline's agent tool runner is the component stage-applied
// write_file calls flow through, so the flag must survive that path.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/tools"
)

func newReadGuardConfig(t *testing.T, root string, strict bool) PipelineRunConfig {
	t.Helper()
	tracker := tools.NewFileTracker()
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreToolsScoped(root, nil) {
		switch tool.Name() {
		case "read_file", "write_file", "edit_file":
			registry.Register(tool)
		}
	}
	return PipelineRunConfig{
		Cwd:                         root,
		Registry:                    registry,
		FileTracker:                 tracker,
		StageRequireReadBeforeWrite: strict,
	}
}

func runTool(t *testing.T, config PipelineRunConfig, name string, args map[string]any) ToolResult {
	t.Helper()
	runner := newAgentToolRunner(config, config.Cwd)
	result, err := runner.RunTool(context.Background(), name, args)
	if err != nil {
		t.Fatalf("%s tool error: %v", name, err)
	}
	return result
}

func TestAgentRunnerEnforcesReadBeforeOverwriteInStrictStageMode(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "users.go")
	original := "package users\n\nfunc New() *Service { return &Service{} }\n"
	if err := os.WriteFile(existing, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	config := newReadGuardConfig(t, root, true)
	res := runTool(t, config, "write_file", map[string]any{
		"path": "users.go", "content": "package users\n\n// invented\n", "overwrite": true,
	})
	if res.OK || !strings.Contains(res.Output, "has not been read this session") {
		t.Fatalf("strict mode must refuse the unread overwrite, got %+v", res)
	}
	if got, _ := os.ReadFile(existing); string(got) != original {
		t.Fatal("refused write must not touch disk")
	}

	if r := runTool(t, config, "read_file", map[string]any{"path": "users.go"}); !r.OK {
		t.Fatalf("read_file failed: %s", r.Output)
	}
	if r := runTool(t, config, "write_file", map[string]any{
		"path": "users.go", "content": original + "\n// extended after a real read\n", "overwrite": true,
	}); !r.OK {
		t.Fatalf("write after read failed: %s", r.Output)
	}
}

func TestAgentRunnerKeepsInteractiveSemanticsWhenNotStrict(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	config := newReadGuardConfig(t, root, false)
	res := runTool(t, config, "write_file", map[string]any{
		"path": "a.go", "content": "package a\n\n// blind\n", "overwrite": true,
	})
	if !res.OK {
		t.Fatalf("non-strict (interactive default) must allow the overwrite, got %+v", res)
	}
}

// runToolOnRunner runs a tool on an existing runner so a sequence of calls
// (read then write) shares the runner's captured config and file tracker.
func runToolOnRunner(t *testing.T, runner ToolRunner, name string, args map[string]any) ToolResult {
	t.Helper()
	result, err := runner.RunTool(context.Background(), name, args)
	if err != nil {
		t.Fatalf("%s tool error: %v", name, err)
	}
	return result
}

func TestProductionRunnerConstructionEnforcesStrictReadGuard(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "users.go")
	original := "package users\n\nfunc New() *Service { return &Service{} }\n"
	if err := os.WriteFile(existing, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Strict mode is deliberately NOT preset: buildStageToolRunner must
	// enable it before the runner captures the config. This pins the
	// production construction order. If the flag moves back below
	// newAgentToolRunner, the captured copy never sees it and this unread
	// overwrite succeeds instead of being refused.
	config := newReadGuardConfig(t, root, false)
	_, runner := buildStageToolRunner(config, root)

	res := runToolOnRunner(t, runner, "write_file", map[string]any{
		"path": "users.go", "content": "package users\n\n// invented\n", "overwrite": true,
	})
	if res.OK || !strings.Contains(res.Output, "has not been read this session") {
		t.Fatalf("production runner must refuse the unread overwrite, got %+v", res)
	}
	if got, _ := os.ReadFile(existing); string(got) != original {
		t.Fatal("refused write must not touch disk")
	}
}

func TestProductionRunnerConstructionAllowsReadThenWrite(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "users.go")
	original := "package users\n\nfunc New() *Service { return &Service{} }\n"
	if err := os.WriteFile(existing, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	config := newReadGuardConfig(t, root, false)
	_, runner := buildStageToolRunner(config, root)

	if r := runToolOnRunner(t, runner, "read_file", map[string]any{"path": "users.go"}); !r.OK {
		t.Fatalf("read_file failed: %s", r.Output)
	}
	if r := runToolOnRunner(t, runner, "write_file", map[string]any{
		"path": "users.go", "content": original + "\n// extended after a real read\n", "overwrite": true,
	}); !r.OK {
		t.Fatalf("write after read must succeed through the production runner, got %+v", r)
	}
}

func TestStrictReadGuardWiringStrictSetsNonStrictDoesNot(t *testing.T) {
	// Pins the consumption site the duplicate-if removal touched: strict
	// mode must refuse an unread overwrite, non-strict mode must allow it.
	for _, tc := range []struct {
		name   string
		strict bool
		allow  bool
	}{
		{name: "strict", strict: true, allow: false},
		{name: "non-strict", strict: false, allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			config := newReadGuardConfig(t, root, tc.strict)
			res := runTool(t, config, "write_file", map[string]any{
				"path": "a.go", "content": "package a\n\n// blind\n", "overwrite": true,
			})
			if res.OK != tc.allow {
				t.Fatalf("strict=%v: expected allow=%v, got %+v", tc.strict, tc.allow, res)
			}
		})
	}
}
