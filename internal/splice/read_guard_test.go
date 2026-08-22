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
