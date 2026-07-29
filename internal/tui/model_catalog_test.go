package tui

import (
	"context"
	"os"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/providermodeldiscovery"
	"github.com/Taf0711/splice/internal/usage"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestNewModelCatalogUsesProviderProfileForDerivedModels(t *testing.T) {
	modelregistry.EnableModelsDevOverlay()
	m := newModel(context.Background(), Options{
		Cwd:             t.TempDir(),
		ModelName:       "z-ai/glm-5.2",
		ProviderProfile: config.ProviderProfile{Name: "openrouter"},
	})
	entry, ok := m.modelCatalog.Resolve("z-ai/glm-5.2")
	if !ok {
		t.Fatal("provider-scoped derived model is missing from the TUI catalog")
	}
	if entry.Cost.IsUnpriced() {
		t.Fatalf("derived model has no price: %#v", entry.Cost)
	}
}

func TestTUIUsageTrackerPricesDerivedModel(t *testing.T) {
	modelregistry.EnableModelsDevOverlay()
	m := newModel(context.Background(), Options{
		Cwd:             t.TempDir(),
		ProviderProfile: config.ProviderProfile{Name: "openrouter"},
	})
	if _, err := m.usageTracker.Record(usageRecordInput("z-ai/glm-5.2")); err != nil {
		t.Fatalf("record derived model usage: %v", err)
	}
	summary := m.usageTracker.Summary()
	if summary.CostCoverage != usage.CostCoverageComplete || summary.TotalCost <= 0 {
		t.Fatalf("derived model usage summary = %#v, want complete non-zero cost", summary)
	}
}

func TestProviderProfileRefreshUpdatesCatalogAndUsageTracker(t *testing.T) {
	modelregistry.EnableModelsDevOverlay()
	previousProvider, hadProvider := os.LookupEnv("SPLICE_PROVIDER")
	t.Cleanup(func() {
		if hadProvider {
			_ = os.Setenv("SPLICE_PROVIDER", previousProvider)
		} else {
			_ = os.Unsetenv("SPLICE_PROVIDER")
		}
	})
	m := newModel(context.Background(), Options{
		Cwd:             t.TempDir(),
		Provider:        &fakeProvider{},
		ProviderProfile: config.ProviderProfile{Name: "openrouter", CatalogID: "openrouter"},
		SavedProviders: []config.ProviderProfile{
			{Name: "openrouter", CatalogID: "openrouter", APIKey: "test-key"},
			{Name: "openai", CatalogID: "openai", APIKey: "test-key"},
		},
		NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return &fakeProvider{}, nil
		},
	})
	if _, err := m.usageTracker.Record(usageRecordInput("z-ai/glm-5.2")); err != nil {
		t.Fatalf("record initial derived model usage: %v", err)
	}
	next, _, _ := m.switchProviderModel("openai", "gpt-5.5")
	if next.providerProfile.Name != "openai" {
		t.Fatalf("provider profile = %q, want openai", next.providerProfile.Name)
	}
	if _, ok := next.modelCatalog.Resolve("z-ai/glm-5.2"); ok {
		t.Fatal("openrouter-derived model remained after switching to openai")
	}
	record, err := next.usageTracker.Record(usageRecordInput("z-ai/glm-5.2"))
	if err != nil {
		t.Fatalf("record unpriced model after provider switch: %v", err)
	}
	if record.Cost == nil || record.Cost.CostStatus != usage.CostStatusUnpriced {
		t.Fatalf("post-switch usage cost = %#v, want unpriced", record.Cost)
	}
}

func usageRecordInput(modelID string) usage.RecordInput {
	return usage.RecordInput{
		ModelID: modelID,
		Usage:   zeroruntime.Usage{InputTokens: 100, OutputTokens: 20},
	}
}

func TestModelContextWindowUsesCachedCatalog(t *testing.T) {
	registry := mustTestModelRegistry(t, testModelEntry("custom-long-context", 12345, []modelregistry.ModelCapability{
		modelregistry.ModelCapabilityChat,
	}))
	m := model{modelCatalog: registry}

	if got := m.modelContextWindow("custom-long-context"); got != 12345 {
		t.Fatalf("modelContextWindow = %d, want 12345", got)
	}
}

func TestModelSupportsVisionTUITrustsCachedCatalog(t *testing.T) {
	registry := mustTestModelRegistry(t, testModelEntry("gpt-5-text-only", 12345, []modelregistry.ModelCapability{
		modelregistry.ModelCapabilityChat,
	}))
	m := model{modelName: "gpt-5-text-only", modelCatalog: registry}

	if m.modelSupportsVisionTUI() {
		t.Fatal("catalog-known text-only model must not fall through to vision name heuristic")
	}
}

func TestModelSupportsVisionTUIChecksDiscoveredBeforeHeuristic(t *testing.T) {
	registry := mustTestModelRegistry(t, testModelEntry("custom-known", 12345, []modelregistry.ModelCapability{
		modelregistry.ModelCapabilityChat,
	}))
	m := model{
		modelName:    "gpt-5-text-only",
		modelCatalog: registry,
		modelPickerLiveByProvider: map[string][]providermodeldiscovery.Model{
			"custom": {{ID: "gpt-5-text-only", InputModalities: []string{"text"}}},
		},
	}

	if m.modelSupportsVisionTUI() {
		t.Fatal("discovered text-only model must not fall through to vision name heuristic")
	}
}

func BenchmarkModelContextWindowLookup(b *testing.B) {
	cachedRegistry, err := modelregistry.DefaultRegistry()
	if err != nil {
		b.Fatalf("load default registry: %v", err)
	}

	b.Run("default_registry_each_call", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			registry, err := modelregistry.DefaultRegistry()
			if err != nil {
				b.Fatalf("load default registry: %v", err)
			}
			entry, ok := registry.Resolve("gpt-4.1")
			if !ok || entry.ContextLimits.ContextWindow <= 0 {
				b.Fatal("expected gpt-4.1 context window")
			}
		}
	})

	b.Run("cached_registry", func(b *testing.B) {
		m := model{modelName: "gpt-4.1", modelCatalog: cachedRegistry}
		b.ReportAllocs()
		for b.Loop() {
			if got := m.modelContextWindow(m.modelName); got <= 0 {
				b.Fatal("expected gpt-4.1 context window")
			}
		}
	})
}

func mustTestModelRegistry(t *testing.T, entries ...modelregistry.ModelEntry) modelregistry.Registry {
	t.Helper()
	registry, err := modelregistry.NewRegistry(entries)
	if err != nil {
		t.Fatalf("create test model registry: %v", err)
	}
	return registry
}

func testModelEntry(id string, contextWindow int, capabilities []modelregistry.ModelCapability) modelregistry.ModelEntry {
	return modelregistry.ModelEntry{
		ID:            id,
		DisplayName:   id,
		APIModel:      id,
		Provider:      modelregistry.ProviderOpenAI,
		APIProviders:  []modelregistry.ProviderKind{modelregistry.ProviderOpenAI},
		ContextLimits: modelregistry.ContextLimits{ContextWindow: contextWindow, MaxOutputTokens: 1024},
		Capabilities:  capabilities,
		Cost: modelregistry.ModelCost{
			Currency:           "USD",
			Unit:               "per_1m_tokens",
			InputPerMillion:    1,
			OutputPerMillion:   1,
			Source:             "test",
			SourceLastVerified: "2026-01-01",
		},
		Status:  modelregistry.ModelStatusActive,
		Aliases: []string{id},
	}
}
