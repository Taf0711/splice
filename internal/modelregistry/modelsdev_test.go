package modelregistry

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
        "cost": {"input": 9.99, "output": 9.99}
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

func TestApplyModelsDevOverrides(t *testing.T) {
	// Point the cache at a non-existent file so DefaultModelEntries returns the
	// pure curated catalog, then apply the sample snapshot explicitly.
	t.Setenv("ZERO_MODELS_CACHE_PATH", filepath.Join(t.TempDir(), "absent.json"))
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

	// Tiered pricing is curated: limits refresh, cost must NOT (gemini-2.5-pro
	// has curated tiers and the snapshot's flat 9.99 would misprice them).
	if geminiPro.ContextLimits.ContextWindow != 2_097_152 {
		t.Fatalf("gemini limits not overridden: %+v", geminiPro.ContextLimits)
	}
	if geminiPro.Cost.InputPerMillion == 9.99 || len(geminiPro.Cost.Tiers) == 0 {
		t.Fatalf("tiered cost must stay curated: %+v", geminiPro.Cost)
	}

	// Model absent from the snapshot: untouched.
	if opus.ContextLimits.ContextWindow != 200_000 || opus.Cost.InputPerMillion != 15 {
		t.Fatalf("opus must be untouched: %+v %+v", opus.ContextLimits, opus.Cost)
	}
}

func TestApplyModelsDevOverridesDerivesProviderScopedModel(t *testing.T) {
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
	if model.ContextLimits.ContextWindow != 202752 || model.ContextLimits.MaxOutputTokens != 16384 {
		t.Fatalf("derived limits = %+v, want openrouter limits", model.ContextLimits)
	}
	if model.ModelsDevProvider != "openrouter" {
		t.Fatalf("derived provider = %q, want openrouter", model.ModelsDevProvider)
	}
}

func TestApplyModelsDevOverridesSkipsInvalidDerivedRecords(t *testing.T) {
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
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
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
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDevProviderScoped), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
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
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
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

func TestDefaultRegistryRealCachedSnapshot(t *testing.T) {
	cachePath, err := modelsDevCachePath()
	if err != nil {
		t.Skipf("models.dev cache path unavailable: %v", err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Skipf("models.dev cache is absent: %v", err)
	}
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()

	openrouter, err := DefaultRegistry("openrouter")
	if err != nil {
		t.Fatalf("DefaultRegistry(openrouter): %v", err)
	}
	t.Logf("openrouter skipped models.dev records: %d", openrouter.ModelsDevSkippedRecords)
	if openrouter.ModelsDevSkippedRecords == 0 {
		t.Fatal("real openrouter snapshot should contain skipped malformed records")
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
}

func TestRefreshModelsDevCacheFetchesAndCaches(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(sampleModelsDev))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
	t.Setenv("ZERO_MODELS_URL", server.URL)
	t.Setenv("ZERO_DISABLE_MODELS_FETCH", "")

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
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
	t.Setenv("ZERO_MODELS_URL", server.URL)

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
	t.Setenv("ZERO_MODELS_CACHE_PATH", filepath.Join(t.TempDir(), "modelsdev.json"))
	t.Setenv("ZERO_MODELS_URL", server.URL)
	t.Setenv("ZERO_DISABLE_MODELS_FETCH", "1")
	if err := RefreshModelsDevCache(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCachedModelsDevProvidersIgnoresStaleCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDev), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-modelsDevMaxAge - time.Hour)
	if err := os.Chtimes(cachePath, stale, stale); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)
	EnableModelsDevOverlay()

	if providers := cachedModelsDevProviders(); providers != nil {
		t.Fatal("stale cache must be ignored")
	}
}

func TestCachedModelsDevProvidersRequiresOptIn(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDev), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
	resetModelsDevCacheForTest()
	t.Cleanup(resetModelsDevCacheForTest)

	// Without EnableModelsDevOverlay a fresh, valid cache must still be ignored:
	// library consumers and hermetic tests get the pure curated catalog.
	if providers := cachedModelsDevProviders(); providers != nil {
		t.Fatal("overlay must be opt-in")
	}
}

func TestDefaultModelEntriesAppliesFreshCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "modelsdev.json")
	if err := os.WriteFile(cachePath, []byte(sampleModelsDev), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZERO_MODELS_CACHE_PATH", cachePath)
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
