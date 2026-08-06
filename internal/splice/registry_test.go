package splice

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestBuildStageRegistryRegistersAllStages(t *testing.T) {
	registry, err := buildStageRegistry(agent.Options{}, t.TempDir())
	if err != nil {
		t.Fatalf("buildStageRegistry: %v", err)
	}
	for _, name := range []string{"code_writer", "test_generator", "test_runner", "acceptance_verifier", "static_analyzer", "security_auditor"} {
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

func TestStageOptionsEmitsAttributedUsageWithoutLegacyDuplicate(t *testing.T) {
	var attributed []agent.AttributedUsage
	legacyCalls := 0
	selection := agent.ModelSelection{ProviderName: "routed", Model: "model-a"}
	options := stageOptions("code_writer", 2, selection, agent.Options{
		OnUsage: func(agent.Usage) { legacyCalls++ },
		OnAttributedUsage: func(usage agent.AttributedUsage) {
			attributed = append(attributed, usage)
		},
	}, t.TempDir(), nil)

	events := make(chan zeroruntime.StreamEvent, 2)
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 4, OutputTokens: 2}}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(events)
	zeroruntime.CollectStreamWithOptions(context.Background(), events, options.Stream)

	if legacyCalls != 0 || len(attributed) != 1 {
		t.Fatalf("legacy calls = %d, attributed = %#v", legacyCalls, attributed)
	}
	got := attributed[0]
	if !got.UsageReported || got.ProviderName != "routed" || got.Model != "model-a" || got.Stage != "code_writer" || got.Iteration != 2 || got.Usage.InputTokens != 4 || got.Usage.OutputTokens != 2 {
		t.Fatalf("attributed usage = %+v", got)
	}
}

func TestStageOptionsEmitsMissingUsageAndPreservesLegacyFallback(t *testing.T) {
	selection := agent.ModelSelection{ProviderName: "primary", Model: "model-a"}
	var missing agent.AttributedUsage
	attributedOptions := stageOptions("test_generator", 3, selection, agent.Options{
		OnAttributedUsage: func(usage agent.AttributedUsage) { missing = usage },
	}, t.TempDir(), nil)

	noUsage := make(chan zeroruntime.StreamEvent, 1)
	noUsage <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(noUsage)
	zeroruntime.CollectStreamWithOptions(context.Background(), noUsage, attributedOptions.Stream)
	if missing.UsageReported || missing.Stage != "test_generator" || missing.Iteration != 3 || missing.Model != "model-a" {
		t.Fatalf("missing usage attribution = %+v", missing)
	}

	malformed := make(chan zeroruntime.StreamEvent, 2)
	malformed <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsageError, Error: "invalid usage"}
	malformed <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(malformed)
	zeroruntime.CollectStreamWithOptions(context.Background(), malformed, attributedOptions.Stream)
	if !missing.UsageReported || missing.UsageError != "invalid usage" {
		t.Fatalf("malformed usage attribution = %+v", missing)
	}

	legacyCalls := 0
	legacyOptions := stageOptions("code_writer", 1, selection, agent.Options{
		OnUsage: func(usage agent.Usage) {
			legacyCalls++
			if usage.InputTokens != 2 {
				t.Fatalf("legacy usage = %+v", usage)
			}
		},
	}, t.TempDir(), nil)
	withUsage := make(chan zeroruntime.StreamEvent, 2)
	withUsage <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 2}}
	withUsage <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(withUsage)
	zeroruntime.CollectStreamWithOptions(context.Background(), withUsage, legacyOptions.Stream)
	if legacyCalls != 1 {
		t.Fatalf("legacy usage calls = %d, want 1", legacyCalls)
	}
}

func TestStageOptionsPromptCacheKey(t *testing.T) {
	selection := agent.ModelSelection{ProviderName: "primary", Model: "model-a"}
	withSession := stageOptions("code_writer", 1, selection, agent.Options{SessionID: "session-1"}, t.TempDir(), nil)
	if got, want := withSession.PromptCacheKey, "session-1:code_writer"; got != want {
		t.Fatalf("PromptCacheKey = %q, want %q", got, want)
	}
	withoutSession := stageOptions("code_writer", 1, selection, agent.Options{}, t.TempDir(), nil)
	if withoutSession.PromptCacheKey != "" {
		t.Fatalf("PromptCacheKey = %q, want empty", withoutSession.PromptCacheKey)
	}
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
