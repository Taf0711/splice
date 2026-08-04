package modelregistry

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

const sampleModelsDev = `{
  "anthropic": {
    "id": "anthropic",
    "models": {
      "claude-sonnet-4-5-20250929": {
        "limit": {"context": 1000000, "output": 64000},
        "cost": {"input": 3.5, "output": 17.5, "cache_read": 0.35, "cache_write": 4.4}
      }
    }
  },
  "google": {
    "id": "google",
    "models": {
      "gemini-2.5-pro": {
        "limit": {"context": 2097152, "output": 65536},
        "cost": {
          "input": 1.25, "output": 10, "cache_read": 0.125,
          "tiers": [{"input": 2.5, "output": 15, "cache_read": 0.25, "tier": {"type": "context", "size": 200000}}]
        }
      }
    }
  }
}`

const sampleModelsDevProviderScoped = `{
  "openrouter": {
    "models": {
      "z-ai/glm-5.2": {
        "limit": {"context": 202752, "output": 16384},
        "cost": {"input": 0.6692, "output": 2.1032, "cache_read": 0.12428}
      },
      "gpt-4.1-mini": {
        "limit": {"context": 999, "output": 99},
        "cost": {"input": 99, "output": 99}
      }
    }
  },
  "crossmodel": {
    "models": {
      "z-ai/glm-5.2": {
        "limit": {"context": 131072, "output": 8192},
        "cost": {"input": 1.2, "output": 4.4}
      }
    }
  },
  "nvidia": {
    "models": {
      "z-ai/glm-5.2": {
        "limit": {"context": 131072, "output": 8192},
        "cost": {"input": 0, "output": 0}
      }
    }
  }
}`

func embeddedModelsDevDateForTest(t *testing.T) time.Time {
	t.Helper()
	date, err := time.Parse("2006-01-02", strings.TrimSpace(string(modelsDevEmbeddedDate)))
	if err != nil {
		t.Fatalf("parse embedded models.dev date: %v", err)
	}
	return date.UTC()
}

func embeddedModelsDevRecordForTest(t *testing.T, provider, model string) (modelsDevModel, bool) {
	t.Helper()
	compressed, err := os.ReadFile("modelsdev_snapshot.json.gz")
	if err != nil {
		t.Fatalf("read embedded models.dev snapshot: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open embedded models.dev snapshot: %v", err)
	}
	data, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil {
		t.Fatalf("read embedded models.dev snapshot: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close embedded models.dev snapshot: %v", closeErr)
	}
	var providers map[string]modelsDevProvider
	if err := json.Unmarshal(data, &providers); err != nil {
		t.Fatalf("parse embedded models.dev snapshot: %v", err)
	}
	providerData, ok := providers[provider]
	if !ok {
		return modelsDevModel{}, false
	}
	record, ok := providerData.Models[model]
	return record, ok
}

func freshModelsDevCacheTimeForTest(t *testing.T) time.Time {
	t.Helper()
	return embeddedModelsDevDateForTest(t).Add(24 * time.Hour)
}

func TestParseModelsDev(t *testing.T) {
	providers, err := parseModelsDev([]byte(sampleModelsDev))
	if err != nil {
		t.Fatal(err)
	}
	record, ok := providers["anthropic"]["claude-sonnet-4-5-20250929"]
	if !ok {
		t.Fatal("expected anthropic sonnet record")
	}
	if record.Limit.Context != 1_000_000 || record.Cost.Input != 3.5 {
		t.Fatalf("unexpected record: %+v", record)
	}
	if _, err := parseModelsDev([]byte(`{}`)); err == nil {
		t.Fatal("empty document must be rejected")
	}
	if _, err := parseModelsDev([]byte(`not json`)); err == nil {
		t.Fatal("malformed document must be rejected")
	}
}

func TestModelsDevCostTiers(t *testing.T) {
	step := func(input, output, cacheRead, cacheWrite float64, size int, tierType string) modelsDevCostTier {
		return modelsDevCostTier{
			Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
			Tier: struct {
				Type string `json:"type"`
				Size int    `json:"size"`
			}{Type: tierType, Size: size},
		}
	}
	record := func(input, output, cacheRead, cacheWrite float64, tiers ...modelsDevCostTier) modelsDevModel {
		var model modelsDevModel
		model.Cost.Input = input
		model.Cost.Output = output
		model.Cost.CacheRead = cacheRead
		model.Cost.CacheWrite = cacheWrite
		model.Cost.Tiers = tiers
		return model
	}
	tests := []struct {
		name   string
		record modelsDevModel
		want   []ModelCostTier
	}{
		{
			name:   "one step",
			record: record(1, 2, 0.1, 0.2, step(1.5, 3, 0.15, 0.3, 16000, "context")),
			want: []ModelCostTier{
				{UpToInputTokens: 16000, InputPerMillion: 1, OutputPerMillion: 2, CachedInputPerMillion: 0.1, CacheWritePerMillion: 0.2},
				{InputPerMillion: 1.5, OutputPerMillion: 3, CachedInputPerMillion: 0.15, CacheWritePerMillion: 0.3},
			},
		},
		{
			name: "two unsorted steps",
			record: record(0.19, 0.63, 0, 0,
				step(0.32, 1.25, 0.125, 0.32, 32000, "context"),
				step(0.25, 1, 0.094, 0.25, 16000, "context")),
			want: []ModelCostTier{
				{UpToInputTokens: 16000, InputPerMillion: 0.19, OutputPerMillion: 0.63},
				{UpToInputTokens: 32000, InputPerMillion: 0.25, OutputPerMillion: 1, CachedInputPerMillion: 0.094, CacheWritePerMillion: 0.25},
				{InputPerMillion: 0.32, OutputPerMillion: 1.25, CachedInputPerMillion: 0.125, CacheWritePerMillion: 0.32},
			},
		},
		{
			name:   "302ai gpt-5.4 inherits omitted cache rates",
			record: record(2.5, 15, 0.25, 0, step(5, 22.5, 0, 0, 272000, "context")),
			want: []ModelCostTier{
				{UpToInputTokens: 272000, InputPerMillion: 2.5, OutputPerMillion: 15, CachedInputPerMillion: 0.25, CacheWritePerMillion: 0},
				{InputPerMillion: 5, OutputPerMillion: 22.5, CachedInputPerMillion: 0.25, CacheWritePerMillion: 0},
			},
		},
		{
			name: "cache rates override and chain",
			record: record(1, 2, 0.1, 0.2,
				step(1.5, 3, 0.3, 0.4, 16000, "context"),
				step(2, 4, 0, 0, 32000, "context")),
			want: []ModelCostTier{
				{UpToInputTokens: 16000, InputPerMillion: 1, OutputPerMillion: 2, CachedInputPerMillion: 0.1, CacheWritePerMillion: 0.2},
				{UpToInputTokens: 32000, InputPerMillion: 1.5, OutputPerMillion: 3, CachedInputPerMillion: 0.3, CacheWritePerMillion: 0.4},
				{InputPerMillion: 2, OutputPerMillion: 4, CachedInputPerMillion: 0.3, CacheWritePerMillion: 0.4},
			},
		},
		{
			name:   "non-context type",
			record: record(1, 2, 0, 0, step(2, 3, 0, 0, 16000, "output")),
		},
		{
			name:   "zero size",
			record: record(1, 2, 0, 0, step(2, 3, 0, 0, 0, "context")),
		},
		{
			name:   "zero step input",
			record: record(1, 2, 0, 0, step(0, 3, 0, 0, 16000, "context")),
		},
		{
			name:   "no tiers",
			record: record(1, 2, 0, 0),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := modelsDevCostTiers(test.record); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("modelsDevCostTiers() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestModelsDevCostTiersCalculateCostUsesInheritedCacheRate(t *testing.T) {
	var record modelsDevModel
	record.Cost.Input = 2.5
	record.Cost.Output = 15
	record.Cost.CacheRead = 0.25
	record.Cost.Tiers = []modelsDevCostTier{{
		Input:  5,
		Output: 22.5,
		Tier: struct {
			Type string `json:"type"`
			Size int    `json:"size"`
		}{Type: "context", Size: 272000},
	}}
	tiers := modelsDevCostTiers(record)
	model := ModelEntry{
		ID: "302ai/gpt-5.4",
		Cost: ModelCost{
			InputPerMillion:       record.Cost.Input,
			OutputPerMillion:      record.Cost.Output,
			CachedInputPerMillion: record.Cost.CacheRead,
			Tiers:                 tiers,
		},
	}
	if len(tiers) != 2 {
		t.Fatalf("modelsDevCostTiers() = %+v, want one boundary and a fallback tier", tiers)
	}
	fallbackTier := tiers[len(tiers)-1]
	got, err := CalculateCost(model, zeroruntime.Usage{
		InputTokens:       300000,
		CachedInputTokens: 250000,
		OutputTokens:      0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.PricingTier == nil || got.PricingTier.InputPerMillion != 5 || got.PricingTier.CachedInputPerMillion != 0.25 {
		t.Fatalf("selected fallback tier = %+v, want input 5 and cached input 0.25", got.PricingTier)
	}
	want := (float64(50000) / tokensPerMillion * fallbackTier.InputPerMillion) +
		(float64(250000) / tokensPerMillion * fallbackTier.CachedInputPerMillion)
	if math.Abs(got.TotalCost-want) > 1e-12 {
		t.Fatalf("CalculateCost total = %.12f, want %.12f", got.TotalCost, want)
	}
}

func TestApplyModelsDevOverrides(t *testing.T) {
	// Point the cache at a non-existent file so the embedded baseline is used,
	// then apply the sample snapshot explicitly.
	t.Setenv("SPLICE_MODELS_CACHE_PATH", filepath.Join(t.TempDir(), "absent.json"))
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)

	providers, err := parseModelsDev([]byte(sampleModelsDev))
	if err != nil {
		t.Fatal(err)
	}
	entries := applyModelsDevOverrides(DefaultModelEntries(), providers)

	var sonnet, geminiPro, opus ModelEntry
	for _, entry := range entries {
		switch entry.ID {
		case "claude-sonnet-4.5":
			sonnet = entry
		case "gemini-2.5-pro":
			geminiPro = entry
		case "claude-opus-4.1":
			opus = entry
		}
	}

	// Known model: limits and base pricing refreshed from the snapshot.
	if sonnet.ContextLimits.ContextWindow != 1_000_000 || sonnet.ContextLimits.MaxOutputTokens != 64_000 {
		t.Fatalf("sonnet limits not overridden: %+v", sonnet.ContextLimits)
	}
	if sonnet.Cost.InputPerMillion != 3.5 || sonnet.Cost.OutputPerMillion != 17.5 || sonnet.Cost.CacheWritePerMillion != 4.4 {
		t.Fatalf("sonnet cost not overridden: %+v", sonnet.Cost)
	}
	if sonnet.Cost.Source != "models.dev/api.json (cached)" {
		t.Fatalf("sonnet cost source not marked: %q", sonnet.Cost.Source)
	}

	// Tiered pricing uses the matching models.dev base rates and boundary.
	if geminiPro.ContextLimits.ContextWindow != 2_097_152 {
		t.Fatalf("gemini limits not overridden: %+v", geminiPro.ContextLimits)
	}
	if geminiPro.Cost.InputPerMillion != 1.25 || geminiPro.Cost.OutputPerMillion != 10 || len(geminiPro.Cost.Tiers) != 2 {
		t.Fatalf("tiered cost mismatch: %+v", geminiPro.Cost)
	}
	if geminiPro.Cost.Tiers[0].UpToInputTokens != 200_000 || geminiPro.Cost.Tiers[0].InputPerMillion != 1.25 || geminiPro.Cost.Tiers[1].InputPerMillion != 2.5 {
		t.Fatalf("tiered cost boundary mismatch: %+v", geminiPro.Cost.Tiers)
	}

	// Model absent from the sample: the embedded baseline remains in place.
	if opus.ContextLimits.ContextWindow != 200_000 || opus.Cost.IsUnpriced() || opus.Cost.Source != modelsDevEmbeddedSource {
		t.Fatalf("opus must be untouched: %+v %+v", opus.ContextLimits, opus.Cost)
	}
}

func TestApplyModelsDevOverridesDerivesProviderScopedModel(t *testing.T) {
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()
	providers, err := parseModelsDev([]byte(sampleModelsDevProviderScoped))
	if err != nil {
		t.Fatal(err)
	}
	entries := applyModelsDevOverrides(DefaultModelEntries(), providers, "openrouter")
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry with derived entry: %v", err)
	}
	model, ok := registry.Get("z-ai/glm-5.2")
	if !ok {
		t.Fatal("provider-scoped derived model did not resolve")
	}
	if model.Cost.InputPerMillion != 0.6692 || model.Cost.OutputPerMillion != 2.1032 || model.Cost.CachedInputPerMillion != 0.12428 {
		t.Fatalf("derived pricing = %+v, want openrouter pricing", model.Cost)
	}
	if len(model.Cost.Tiers) != 0 {
		t.Fatalf("flat derived pricing must not create tiers: %+v", model.Cost.Tiers)
	}
	if model.ContextLimits.ContextWindow != 202752 || model.ContextLimits.MaxOutputTokens != 16384 {
		t.Fatalf("derived limits = %+v, want openrouter limits", model.ContextLimits)
	}
	if model.ModelsDevProvider != "openrouter" {
		t.Fatalf("derived provider = %q, want openrouter", model.ModelsDevProvider)
	}
}

func TestApplyModelsDevOverridesSkipsInvalidDerivedRecords(t *testing.T) {
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()
	providers, err := parseModelsDev([]byte(`{
  "openrouter": {
    "models": {
      "bad-limits": {
        "limit": {"context": 16384, "output": 65536},
        "cost": {"input": 1, "output": 2}
      },
      "bad-cost": {
        "limit": {"context": 16384, "output": 8192},
        "cost": {"input": 0, "output": 0}
      },
      "good": {
        "limit": {"context": 16384, "output": 8192},
        "cost": {"input": 1, "output": 2}
      }
    }
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	entries, skipped := applyModelsDevOverridesWithStats(DefaultModelEntries(), providers, "openrouter")
	if skipped != 2 {
		t.Fatalf("skipped records = %d, want 2", skipped)
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("invalid derived record made registry construction fail: %v", err)
	}
	if _, ok := registry.Get("bad-limits"); ok {
		t.Fatal("derived record with invalid limits was not skipped")
	}
	if _, ok := registry.Get("bad-cost"); ok {
		t.Fatal("derived record with invalid cost was not skipped")
	}
	if _, ok := registry.Get("good"); !ok {
		t.Fatal("valid derived record was skipped")
	}
}

func TestApplyModelsDevOverridesKeepsMalformedCuratedRecordsStrict(t *testing.T) {
	providers, err := parseModelsDev([]byte(`{
  "openai": {
    "models": {
      "gpt-4.1": {
        "limit": {"context": 1024, "output": 2048},
        "cost": {"input": 1, "output": 2}
      }
    }
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	entries := applyModelsDevOverrides(DefaultModelEntries(), providers)
	if _, err := NewRegistry(entries); err == nil {
		t.Fatal("malformed curated override must remain fatal")
	}
}

func TestDefaultRegistryReportsSkippedModelsDevRecords(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")
	data := []byte(`{
  "openrouter": {
    "models": {
      "bad-limits": {
        "limit": {"context": 16384, "output": 65536},
        "cost": {"input": 1, "output": 2}
      }
    }
  }

}`)
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDate := freshModelsDevCacheTimeForTest(t)
	if err := os.Chtimes(cachePath, cacheDate, cacheDate); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()
	registry, err := DefaultRegistry("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if registry.ModelsDevSkippedRecords != 1 {
		t.Fatalf("reported skipped records = %d, want 1", registry.ModelsDevSkippedRecords)
	}
}

func TestDefaultRegistryResolvesProviderScopedDerivedModel(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDevProviderScoped), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDate := freshModelsDevCacheTimeForTest(t)
	if err := os.Chtimes(cachePath, cacheDate, cacheDate); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()
	registry, err := DefaultRegistry("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	model, ok := registry.Get("z-ai/glm-5.2")
	if !ok || model.Cost.InputPerMillion != 0.6692 || model.Cost.OutputPerMillion != 2.1032 || model.Cost.CachedInputPerMillion != 0.12428 {
		t.Fatalf("DefaultRegistry openrouter model = %+v/%v", model.Cost, ok)
	}
}

func TestApplyModelsDevOverridesUsesRequestedProviderNotFirstMatch(t *testing.T) {
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()
	providers, err := parseModelsDev([]byte(sampleModelsDevProviderScoped))
	if err != nil {
		t.Fatal(err)
	}
	entries := applyModelsDevOverrides(DefaultModelEntries(), providers, "crossmodel")
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatal(err)
	}
	model, ok := registry.Get("z-ai/glm-5.2")
	if !ok || model.Cost.InputPerMillion != 1.2 || model.Cost.OutputPerMillion != 4.4 {
		t.Fatalf("crossmodel pricing = %+v, want 1.2/4.4", model.Cost)
	}
}

func TestApplyModelsDevOverridesLeavesAmbiguousModelUnpricedWithoutProvider(t *testing.T) {
	providers, err := parseModelsDev([]byte(sampleModelsDevProviderScoped))
	if err != nil {
		t.Fatal(err)
	}
	entries := applyModelsDevOverrides(DefaultModelEntries(), providers)
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("z-ai/glm-5.2"); ok {
		t.Fatal("model with no provider context must not guess a price")
	}
	unknownRegistry, err := NewRegistry(applyModelsDevOverrides(DefaultModelEntries(), providers, "unknown"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := unknownRegistry.Get("z-ai/glm-5.2"); ok {
		t.Fatal("unknown provider must not guess a price")
	}
}

func TestApplyModelsDevOverridesDoesNotOverwriteCuratedIdentityOrUpgradeTarget(t *testing.T) {
	providers, err := parseModelsDev([]byte(sampleModelsDevProviderScoped))
	if err != nil {
		t.Fatal(err)
	}
	entries := applyModelsDevOverrides(DefaultModelEntries(), providers, "openrouter")
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatal(err)
	}
	model, ok := registry.Get("gpt-4.1-mini")
	if !ok {
		t.Fatal("curated model not found")
	}
	if model.ID != "gpt-4.1-mini" || model.APIModel != "gpt-4.1-mini" || model.UpgradeTargetID != "gpt-4.1" {
		t.Fatalf("curated identity changed: %+v", model)
	}
	if model.ModelsDevProvider != "" {
		t.Fatalf("curated model marked as derived: %q", model.ModelsDevProvider)
	}
}

func TestModelsDevProviderAliases(t *testing.T) {
	providers, err := parseModelsDev([]byte(`{"openai":{"models":{"x":{"limit":{"context":1,"output":1},"cost":{"input":1,"output":1}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := modelsDevProviderKey("chatgpt", providers); !ok || got != "openai" {
		t.Fatalf("chatgpt provider key = %q/%v, want openai/true", got, ok)
	}
	if _, ok := modelsDevProviderKey("unknown", providers); ok {
		t.Fatal("unknown provider must not resolve")
	}
}

func TestDefaultModelEntriesOverlayDisabledIsCuratedOnly(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDevProviderScoped), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	want := DefaultModelEntries()
	got := DefaultModelEntries("openrouter")
	if !reflect.DeepEqual(got, want) {
		t.Fatal("overlay-disabled registry differs from curated catalog")
	}
	registry, err := NewRegistry(got)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("z-ai/glm-5.2"); ok {
		t.Fatal("overlay-disabled registry must not add derived models")
	}
}

// TestDefaultRegistryRealCachedSnapshot runs the overlay against a real
// models.dev response (openrouter + openai, captured 2026-07-27) so the
// malformed records the live API carries stay exercised. The snapshot is
// copied into a temp dir so the test controls its cache mtime.
func TestDefaultRegistryRealCachedSnapshot(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")
	snapshot, err := os.ReadFile(filepath.Join("testdata", "modelsdev_snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, snapshot, 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDate := freshModelsDevCacheTimeForTest(t)
	if err := os.Chtimes(cachePath, cacheDate, cacheDate); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()

	openrouter, err := DefaultRegistry("openrouter")
	if err != nil {
		t.Fatalf("DefaultRegistry(openrouter): %v", err)
	}
	t.Logf("openrouter skipped models.dev records: %d", openrouter.ModelsDevSkippedRecords)
	if openrouter.ModelsDevSkippedRecords < 1 {
		t.Fatalf("real openrouter snapshot skipped records = %d, want at least 1", openrouter.ModelsDevSkippedRecords)
	}
	glm, ok := openrouter.Get("z-ai/glm-5.2")
	if !ok {
		t.Fatal("openrouter registry did not resolve z-ai/glm-5.2")
	}
	if glm.Cost.InputPerMillion != 0.6692 || glm.Cost.OutputPerMillion != 2.1032 || glm.Cost.CachedInputPerMillion != 0.12428 {
		t.Fatalf("openrouter z-ai/glm-5.2 pricing = %+v", glm.Cost)
	}

	chatgpt, err := DefaultRegistry("chatgpt")
	if err != nil {
		t.Fatalf("DefaultRegistry(chatgpt): %v", err)
	}
	gpt, ok := chatgpt.Get("gpt-5.5")
	if !ok {
		t.Fatal("chatgpt registry did not resolve gpt-5.5")
	}
	if gpt.Cost.InputPerMillion != 5 || gpt.Cost.OutputPerMillion != 30 || gpt.Cost.CachedInputPerMillion != 0.5 {
		t.Fatalf("chatgpt gpt-5.5 pricing = %+v", gpt.Cost)
	}
	if len(gpt.Cost.Tiers) != 2 || gpt.Cost.Tiers[0].UpToInputTokens != 272_000 {
		t.Fatalf("chatgpt gpt-5.5 tiers = %+v, want a 272k boundary and fallback", gpt.Cost.Tiers)
	}
	below, err := selectCostTier(gpt.Cost, 272_000)
	if err != nil {
		t.Fatal(err)
	}
	above, err := selectCostTier(gpt.Cost, 272_001)
	if err != nil {
		t.Fatal(err)
	}
	if below.InputPerMillion != 5 || below.OutputPerMillion != 30 || below.CachedInputPerMillion != 0.5 {
		t.Fatalf("gpt-5.5 rates at boundary = %+v", below)
	}
	if above.InputPerMillion != 10 || above.OutputPerMillion != 45 || above.CachedInputPerMillion != 1 {
		t.Fatalf("gpt-5.5 rates above boundary = %+v", above)
	}
}

func TestRefreshModelsDevCacheFetchesAndCaches(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(sampleModelsDev))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	t.Setenv("SPLICE_MODELS_URL", server.URL)
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")

	if err := RefreshModelsDevCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("expected 1 fetch, got %d", hits)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}
	// Fresh cache: second call must not re-fetch.
	if err := RefreshModelsDevCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("fresh cache must skip the fetch, got %d hits", hits)
	}
}

func TestRefreshModelsDevCacheRejectsBadBodyWithoutClobbering(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDev), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(cachePath, stale, stale); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	t.Setenv("SPLICE_MODELS_URL", server.URL)

	if err := RefreshModelsDevCache(t.Context()); err == nil {
		t.Fatal("bad body must return an error")
	}
	content, err := os.ReadFile(cachePath)
	if err != nil || string(content) != sampleModelsDev {
		t.Fatal("bad fetch must not clobber the existing cache")
	}
}

func TestRefreshModelsDevCacheDisabledByEnv(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("fetch must not happen when disabled")
	}))
	defer server.Close()
	t.Setenv("SPLICE_MODELS_CACHE_PATH", filepath.Join(t.TempDir(), "modelsdev.json"))
	t.Setenv("SPLICE_MODELS_URL", server.URL)
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "1")
	if err := RefreshModelsDevCache(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCachedModelsDevProvidersUsesEmbeddedForOlderCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDev), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := embeddedModelsDevDateForTest(t).Add(-12 * time.Hour)
	if err := os.Chtimes(cachePath, stale, stale); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()

	if providers := cachedModelsDevProviders(); providers == nil {
		t.Fatal("older cache must fall back to the embedded snapshot")
	}
}

func TestCachedModelsDevProvidersRequiresOptIn(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDev), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)

	// Without EnableModelsDevOverlay the embedded baseline still applies.
	if providers := cachedModelsDevProviders(); providers == nil {
		t.Fatal("embedded snapshot must apply without disk overlay opt-in")
	}
}

func TestDefaultModelEntriesAppliesFreshCache(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDev), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDate := freshModelsDevCacheTimeForTest(t)
	if err := os.Chtimes(cachePath, cacheDate, cacheDate); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()

	for _, entry := range DefaultModelEntries() {
		if entry.ID == "claude-sonnet-4.5" {
			if entry.ContextLimits.ContextWindow != 1_000_000 {
				t.Fatalf("fresh cache must overlay the registry: %+v", entry.ContextLimits)
			}
			return
		}
	}
	t.Fatal("claude-sonnet-4.5 not found")
}

func TestModelsDevPricingAsOfUsesCacheMtime(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	var doc map[string]map[string]any
	if err := json.Unmarshal([]byte(sampleModelsDev), &doc); err != nil {
		t.Fatal(err)
	}
	var scoped map[string]map[string]any
	if err := json.Unmarshal([]byte(sampleModelsDevProviderScoped), &scoped); err != nil {
		t.Fatal(err)
	}
	doc["openrouter"] = scoped["openrouter"]
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	known := embeddedModelsDevDateForTest(t).Add(28*time.Hour + 5*time.Minute + 6*time.Second)
	if err := os.Chtimes(cachePath, known, known); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()

	registry, err := DefaultRegistry("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	curated, ok := registry.Get("claude-sonnet-4.5")
	if !ok || curated.Cost.SourceLastVerified != modelsDevCacheDate(known) {
		t.Fatalf("curated pricing date = %q/%v, want cache mtime date", curated.Cost.SourceLastVerified, ok)
	}
	derived, ok := registry.Get("z-ai/glm-5.2")
	if !ok || derived.Cost.SourceLastVerified != modelsDevCacheDate(known) {
		t.Fatalf("derived pricing date = %q/%v, want cache mtime date", derived.Cost.SourceLastVerified, ok)
	}
}

func TestDefaultRegistryUsesEmbeddedPricingForDerivedProviderModel(t *testing.T) {
	t.Setenv("SPLICE_MODELS_CACHE_PATH", filepath.Join(t.TempDir(), "missing.json"))
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()

	registry, err := DefaultRegistry("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	glm, ok := registry.Get("z-ai/glm-5.2")
	expected, expectedOK := embeddedModelsDevRecordForTest(t, "openrouter", "z-ai/glm-5.2")
	if ok != expectedOK {
		t.Fatalf("embedded z-ai/glm-5.2 presence = %v, want %v", ok, expectedOK)
	}
	if !expectedOK {
		return
	}
	if glm.Cost.InputPerMillion != expected.Cost.Input || glm.Cost.OutputPerMillion != expected.Cost.Output || glm.Cost.CachedInputPerMillion != expected.Cost.CacheRead {
		t.Fatalf("embedded z-ai/glm-5.2 pricing = %+v, want input %.12g/output %.12g/cache-read %.12g", glm.Cost, expected.Cost.Input, expected.Cost.Output, expected.Cost.CacheRead)
	}
	if glm.Cost.Source != modelsDevEmbeddedSource || glm.Cost.SourceLastVerified != strings.TrimSpace(string(modelsDevEmbeddedDate)) {
		t.Fatalf("embedded z-ai/glm-5.2 source = %q/%q, want embedded %s", glm.Cost.Source, glm.Cost.SourceLastVerified, strings.TrimSpace(string(modelsDevEmbeddedDate)))
	}
}

func TestDefaultRegistryUsesNewerCachePricingForDerivedProviderModel(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	cache := []byte(`{"openrouter":{"models":{"z-ai/glm-5.2":{"limit":{"context":999999,"output":65536},"cost":{"input":9,"output":10,"cache_read":1}}}}}`)
	if err := os.WriteFile(cachePath, cache, 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDate := embeddedModelsDevDateForTest(t).Add(28*time.Hour + 5*time.Minute + 6*time.Second)
	if err := os.Chtimes(cachePath, cacheDate, cacheDate); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()

	registry, err := DefaultRegistry("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	glm, ok := registry.Get("z-ai/glm-5.2")
	if !ok {
		t.Fatal("openrouter registry did not resolve z-ai/glm-5.2 from the newer cache")
	}
	if glm.Cost.InputPerMillion != 9 || glm.Cost.OutputPerMillion != 10 || glm.Cost.CachedInputPerMillion != 1 {
		t.Fatalf("cached z-ai/glm-5.2 pricing = %+v, want cache pricing", glm.Cost)
	}
	if glm.Cost.Source != modelsDevCachedSource || glm.Cost.SourceLastVerified != modelsDevCacheDate(cacheDate) {
		t.Fatalf("cached z-ai/glm-5.2 source = %q/%q, want cached %s", glm.Cost.Source, glm.Cost.SourceLastVerified, modelsDevCacheDate(cacheDate))
	}
}

func TestDefaultRegistrySelectsEmbeddedAndNewerDiskPricing(t *testing.T) {
	t.Setenv("SPLICE_DISABLE_MODELS_FETCH", "")
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	t.Setenv("SPLICE_MODELS_CACHE_PATH", cachePath)
	t.Cleanup(resetModelsDevCacheForTest)

	check := func(t *testing.T, modTime time.Time, wantInput float64, wantSource string, wantDate string) {
		t.Helper()
		if err := os.WriteFile(cachePath, []byte(`{"openai":{"models":{"gpt-5.6-sol":{"limit":{"context":1050000,"output":128000},"cost":{"input":99,"output":100,"cache_read":9,"tiers":[{"input":199,"output":200,"cache_read":19,"tier":{"type":"context","size":272000}}]}}}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(cachePath, modTime, modTime); err != nil {
			t.Fatal(err)
		}
		resetModelsDevCacheForTest()
		EnableModelsDevOverlay()
		registry, err := DefaultRegistry()
		if err != nil {
			t.Fatal(err)
		}
		model, ok := registry.Get("gpt-5.6-sol")
		if !ok {
			t.Fatal("gpt-5.6-sol is missing")
		}
		if model.Cost.InputPerMillion != wantInput || model.Cost.Source != wantSource || model.Cost.SourceLastVerified != wantDate {
			t.Fatalf("gpt-5.6-sol cost = %+v, want input %v from %s on %s", model.Cost, wantInput, wantSource, wantDate)
		}
	}

	t.Run("older cache loses", func(t *testing.T) {
		record, ok := embeddedModelsDevRecordForTest(t, "openai", "gpt-5.6-sol")
		wantInput, wantSource, wantDate := 0.0, "", ""
		if ok {
			wantInput = record.Cost.Input
			wantSource = modelsDevEmbeddedSource
			wantDate = strings.TrimSpace(string(modelsDevEmbeddedDate))
		}
		check(t, embeddedModelsDevDateForTest(t).Add(-time.Minute), wantInput, wantSource, wantDate)
	})
	t.Run("newer cache wins", func(t *testing.T) {
		cacheDate := embeddedModelsDevDateForTest(t).Add(24*time.Hour + time.Minute)
		check(t, cacheDate, 99, modelsDevCachedSource, modelsDevCacheDate(cacheDate))
	})
}

func TestDefaultRegistryUsesEmbeddedPricingWithoutDiskCache(t *testing.T) {
	t.Setenv("SPLICE_MODELS_CACHE_PATH", filepath.Join(t.TempDir(), "missing.json"))
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)

	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	gpt, ok := registry.Get("gpt-5.6-sol")
	expected, expectedOK := embeddedModelsDevRecordForTest(t, "openai", "gpt-5.6-sol")
	if ok != expectedOK {
		t.Fatalf("gpt-5.6-sol presence = %v, want %v", ok, expectedOK)
	}
	if !expectedOK {
		return
	}
	if gpt.Cost.Source != modelsDevEmbeddedSource || gpt.Cost.SourceLastVerified != strings.TrimSpace(string(modelsDevEmbeddedDate)) {
		t.Fatalf("gpt-5.6-sol cost = %+v/%v, want embedded pricing", gpt.Cost, ok)
	}
	if gpt.Cost.InputPerMillion != expected.Cost.Input || gpt.Cost.OutputPerMillion != expected.Cost.Output || gpt.Cost.CachedInputPerMillion != expected.Cost.CacheRead || gpt.Cost.CacheWritePerMillion != expected.Cost.CacheWrite {
		t.Fatalf("gpt-5.6-sol pricing = %+v, want embedded record rates", gpt.Cost)
	}
	contextSteps := make([]modelsDevCostTier, 0, len(expected.Cost.Tiers))
	for _, step := range expected.Cost.Tiers {
		if step.Tier.Type == "context" && step.Tier.Size > 0 {
			contextSteps = append(contextSteps, step)
		}
	}
	if len(contextSteps) == 0 {
		t.Log("embedded gpt-5.6-sol record has no context steps; registry must report zero tiers")
		if len(gpt.Cost.Tiers) != 0 {
			t.Fatalf("gpt-5.6-sol tiers = %+v, want zero tiers", gpt.Cost.Tiers)
		}
	} else {
		sort.Slice(contextSteps, func(left, right int) bool {
			return contextSteps[left].Tier.Size < contextSteps[right].Tier.Size
		})
		if len(gpt.Cost.Tiers) != len(contextSteps)+1 {
			t.Fatalf("gpt-5.6-sol tiers = %+v, want %d tiers from %d raw context steps", gpt.Cost.Tiers, len(contextSteps)+1, len(contextSteps))
		}
		for index, step := range contextSteps {
			if gpt.Cost.Tiers[index].UpToInputTokens != step.Tier.Size {
				t.Fatalf("gpt-5.6-sol tier %d boundary = %d, want raw context size %d", index, gpt.Cost.Tiers[index].UpToInputTokens, step.Tier.Size)
			}
		}
	}
	haiku, ok := registry.Get("claude-haiku-3.5")
	if !ok || !haiku.Cost.IsUnpriced() {
		t.Fatalf("claude-haiku-3.5 cost = %+v/%v, want unpriced", haiku.Cost, ok)
	}
	if err := haiku.Cost.Validate(); err != nil {
		t.Fatalf("unpriced Claude Haiku cost must validate: %v", err)
	}
}
