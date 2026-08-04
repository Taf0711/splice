package modelregistry

import (
	"math"
	"testing"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

// A premium cache-write rate prices cache-creation tokens separately from the
// uncached input and the discounted cache-read.
func TestCalculateCostCacheWritePremium(t *testing.T) {
	model := ModelEntry{
		ID: "test-cw", Provider: ProviderAnthropic,
		Cost: ModelCost{Currency: "USD", InputPerMillion: 10, OutputPerMillion: 30, CachedInputPerMillion: 1, CacheWritePerMillion: 12.5},
	}
	// 1M total input = 600k uncached + 300k cache-read + 100k cache-write.
	usage := zeroruntime.Usage{InputTokens: 1_000_000, CachedInputTokens: 300_000, CacheWriteTokens: 100_000, OutputTokens: 200_000}
	cost, err := CalculateCost(model, usage)
	if err != nil {
		t.Fatal(err)
	}
	want := 6.00 /*uncached 600k@10*/ + 0.30 /*read 300k@1*/ + 1.25 /*write 100k@12.5*/ + 6.00 /*out 200k@30*/
	if math.Abs(cost.TotalCost-want) > 1e-9 {
		t.Fatalf("total = %v, want %v", cost.TotalCost, want)
	}
	if cost.CacheWriteTokens != 100_000 || math.Abs(cost.CacheWriteCost-1.25) > 1e-9 {
		t.Fatalf("cacheWrite = %d tok / $%v", cost.CacheWriteTokens, cost.CacheWriteCost)
	}

	t.Run("embedded pricing with cached read", func(t *testing.T) {
		registry, err := DefaultRegistry()
		if err != nil {
			t.Fatalf("DefaultRegistry returned error: %v", err)
		}
		record, expectedOK := embeddedModelsDevRecordForTest(t, "openai", "gpt-4.1")
		model, ok := registry.Get("gpt-4.1")
		if ok != expectedOK {
			t.Fatalf("gpt-4.1 presence = %v, want %v", ok, expectedOK)
		}
		if !expectedOK {
			if !model.Cost.IsUnpriced() {
				t.Fatalf("gpt-4.1 cost = %+v, want unpriced without an embedded record", model.Cost)
			}
			return
		}
		usage := zeroruntime.Usage{
			InputTokens: 1_000_000, CachedInputTokens: 300_000,
			CacheWriteTokens: 200_000, OutputTokens: 500_000,
		}
		cost, err := registry.EstimateCost("gpt-4.1", usage)
		if err != nil {
			t.Fatalf("EstimateCost returned error: %v", err)
		}
		cachedTokens := 0
		if record.Cost.CacheRead > 0 {
			cachedTokens = usage.CachedInputTokens
		}
		cacheWriteTokens := 0
		if record.Cost.CacheWrite > 0 {
			cacheWriteTokens = usage.CacheWriteTokens
		}
		uncachedTokens := usage.InputTokens - cachedTokens - cacheWriteTokens
		want := float64(uncachedTokens)/tokensPerMillion*record.Cost.Input +
			float64(cachedTokens)/tokensPerMillion*record.Cost.CacheRead +
			float64(cacheWriteTokens)/tokensPerMillion*record.Cost.CacheWrite +
			float64(usage.OutputTokens)/tokensPerMillion*record.Cost.Output
		if math.Abs(cost.TotalCost-want) > 1e-9 {
			t.Fatalf("EstimateCost total = %v, want snapshot-derived %v", cost.TotalCost, want)
		}
	})
}

// Without a configured cache-write rate, cache-write tokens fall back to the full
// input rate. This preserves behavior for models without that rate.
func TestCalculateCostCacheWriteFallsBackToInputRate(t *testing.T) {
	model := ModelEntry{ID: "test", Provider: ProviderOpenAI, Cost: ModelCost{Currency: "USD", InputPerMillion: 10, OutputPerMillion: 30}}
	usage := zeroruntime.Usage{InputTokens: 1_000_000, CacheWriteTokens: 100_000, OutputTokens: 0}
	cost, err := CalculateCost(model, usage)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(cost.TotalCost-10.0) > 1e-9 { // all 1M input at 10/1e6
		t.Fatalf("total = %v, want 10.0 (cache-write folds into input when unpriced)", cost.TotalCost)
	}
	if cost.CacheWriteTokens != 0 {
		t.Fatalf("cache-write should not split out when unpriced, got %d", cost.CacheWriteTokens)
	}
}

func TestAnthropicModelsUseEmbeddedPricing(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	m, err := reg.Require("claude-sonnet-4.5")
	if err != nil {
		t.Fatal(err)
	}
	if m.Cost.IsUnpriced() || m.Cost.Source != modelsDevEmbeddedSource {
		t.Fatalf("sonnet cost = %+v, want embedded pricing", m.Cost)
	}
}
