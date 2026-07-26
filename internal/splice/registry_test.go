package splice

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/tools"
)

func TestBuildStageRegistryRegistersAllStages(t *testing.T) {
	registry, err := buildStageRegistry(agent.Options{}, t.TempDir())
	if err != nil {
		t.Fatalf("buildStageRegistry: %v", err)
	}
	for _, name := range []string{"code_writer", "test_generator", "test_runner", "static_analyzer", "security_auditor"} {
		if _, ok := registry[name]; !ok {
			t.Errorf("stage %q missing from registry", name)
		}
	}
}

func TestBuildStageRegistryRegistersDeterministicToolsExactlyOnce(t *testing.T) {
	toolRegistry := tools.NewRegistry()
	options := agent.Options{Registry: toolRegistry}
	workDir := t.TempDir()

	if _, err := buildStageRegistry(options, workDir); err != nil {
		t.Fatalf("buildStageRegistry (first call): %v", err)
	}
	if _, err := buildStageRegistry(options, workDir); err != nil {
		t.Fatalf("buildStageRegistry (second call): %v", err)
	}

	for _, name := range []string{"bandit", "gosec", "sarif"} {
		if _, ok := toolRegistry.Get(name); !ok {
			t.Errorf("tool %q not registered", name)
		}
	}
	// Get-before-Register at registry.go:38-46 means a second buildStageRegistry
	// call on the same *tools.Registry must not panic or double-register; the
	// Get checks above already prove the tools are present, this documents why
	// calling it twice (e.g. once for the pipeline, once for a design task) is safe.
}

func TestDetectLanguageMarkerFiles(t *testing.T) {
	tests := []struct {
		name   string
		marker string
		want   string
	}{
		{name: "go module", marker: "go.mod", want: "go"},
		{name: "typescript config", marker: "tsconfig.json", want: "typescript"},
		{name: "javascript package", marker: "package.json", want: "javascript"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tt.marker), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := detectLanguageUncached(dir); got != tt.want {
				t.Fatalf("detectLanguageUncached(%s marker) = %q, want %q", tt.marker, got, tt.want)
			}
		})
	}
}

func TestDetectLanguageEmptyWorkDirDefaultsToPython(t *testing.T) {
	if got := detectLanguageUncached(""); got != "python" {
		t.Fatalf("detectLanguageUncached(\"\") = %q, want %q", got, "python")
	}
}

func TestDetectLanguageCachesPerWorkDir(t *testing.T) {
	dirA := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "go.mod"), []byte("module a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectLanguage(dirA); got != "go" {
		t.Fatalf("detectLanguage(dirA) = %q, want %q", got, "go")
	}

	// Same workDir, marker removed after the first call: a correct cache
	// returns the stale cached value instead of re-walking and recomputing.
	if err := os.Remove(filepath.Join(dirA, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if got := detectLanguage(dirA); got != "go" {
		t.Fatalf("detectLanguage(dirA) after cache hit = %q, want cached %q", got, "go")
	}

	// A different workDir must not read dirA's cached value.
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirB, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectLanguage(dirB); got != "javascript" {
		t.Fatalf("detectLanguage(dirB) = %q, want %q (cache must be per-workDir, not sticky)", got, "javascript")
	}
}
