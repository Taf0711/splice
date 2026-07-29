package modelregistry

import (
	"math"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestRegistryEstimatesCostFromNormalizedUsage(t *testing.T) {
	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	cost, err := registry.EstimateCost("gpt-4.1", zeroruntime.Usage{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      500_000,
	})
	if err != nil {
		t.Fatalf("EstimateCost returned error: %v", err)
	}

	if cost.ModelID != "gpt-4.1" || cost.Provider != ProviderOpenAI {
		t.Fatalf("cost identity = %#v, want gpt-4.1/openai", cost)
	}
	assertClose(t, cost.InputCost, 1.8)
	assertClose(t, cost.CachedInputCost, 0.05)
	assertClose(t, cost.OutputCost, 4)
	assertClose(t, cost.TotalCost, 5.85)
}

func TestCalculateCostAddsWebSearchRequests(t *testing.T) {
	model := ModelEntry{
		ID:       "search-model",
		Provider: ProviderOpenAI,
		Cost: ModelCost{
			Currency: "USD", Unit: "per_1m_tokens",
			InputPerMillion: 2, OutputPerMillion: 8, WebSearchPerRequest: 0.01,
		},
	}
	breakdown, err := CalculateCost(model, zeroruntime.Usage{WebSearchRequests: 3})
	if err != nil {
		t.Fatalf("CalculateCost returned error: %v", err)
	}
	assertClose(t, breakdown.WebSearchCost, 0.03)
	assertClose(t, breakdown.TotalCost, 0.03)
}

func TestCalculateCostReportsUnpricedWebSearchRequests(t *testing.T) {
	model := ModelEntry{
		ID:       "search-model",
		Provider: ProviderOpenAI,
		Cost: ModelCost{
			Currency: "USD", Unit: "per_1m_tokens",
			InputPerMillion: 2, OutputPerMillion: 8,
		},
	}
	breakdown, err := CalculateCost(model, zeroruntime.Usage{WebSearchRequests: 3})
	if err == nil {
		t.Fatal("CalculateCost silently priced web searches at zero")
	}
	if breakdown.WebSearchRequests != 3 || breakdown.WebSearchCost != 0 {
		t.Fatalf("partial breakdown = %#v, want counted unpriced searches", breakdown)
	}
	if !strings.Contains(err.Error(), "web search pricing") {
		t.Fatalf("error = %q, want web search pricing status", err)
	}
}

func TestRegistryCostSupportsAliasesAndFullyCachedInput(t *testing.T) {
	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	cost, err := registry.EstimateCost("openai:gpt-4.1-mini", zeroruntime.Usage{
		InputTokens:       1_000_000,
		CachedInputTokens: 1_000_000,
	})
	if err != nil {
		t.Fatalf("EstimateCost returned error: %v", err)
	}

	if cost.InputCost != 0 {
		t.Fatalf("InputCost = %v, want 0 for fully cached input", cost.InputCost)
	}
	assertClose(t, cost.CachedInputCost, 0.1)
	assertClose(t, cost.TotalCost, 0.1)
}

func TestRegistryCostUsesPromptAndCompletionAliases(t *testing.T) {
	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	cost, err := registry.EstimateCost("haiku-3.5", zeroruntime.Usage{
		PromptTokens:     2_000,
		CompletionTokens: 1_000,
	})
	if err != nil {
		t.Fatalf("EstimateCost returned error: %v", err)
	}

	if cost.InputTokens != 2_000 || cost.OutputTokens != 1_000 {
		t.Fatalf("usage = %#v, want prompt/completion aliases", cost)
	}
	assertClose(t, cost.TotalCost, 0.0056)
}

func TestRegistryCostIgnoresCachedInputWithoutCachePricing(t *testing.T) {
	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	cost, err := registry.EstimateCost("gpt-4-turbo", zeroruntime.Usage{
		InputTokens:       1_000,
		CachedInputTokens: 1_000,
		OutputTokens:      1_000,
	})
	if err != nil {
		t.Fatalf("EstimateCost returned error: %v", err)
	}

	if cost.CachedInputTokens != 0 || cost.CachedInputCost != 0 {
		t.Fatalf("cached input should be ignored for uncached model pricing: %#v", cost)
	}
	assertClose(t, cost.InputCost, 0.01)
	assertClose(t, cost.OutputCost, 0.03)
}

func TestRegistryCostTreatsReasoningAsOutputBreakdown(t *testing.T) {
	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	withReasoning, err := registry.EstimateCost("gpt-4.1", zeroruntime.Usage{
		InputTokens:     1_000,
		OutputTokens:    1_000,
		ReasoningTokens: 400,
	})
	if err != nil {
		t.Fatalf("EstimateCost(withReasoning) returned error: %v", err)
	}
	plain, err := registry.EstimateCost("gpt-4.1", zeroruntime.Usage{
		InputTokens:  1_000,
		OutputTokens: 1_000,
	})
	if err != nil {
		t.Fatalf("EstimateCost(plain) returned error: %v", err)
	}

	if withReasoning.OutputCost != plain.OutputCost || withReasoning.TotalCost != plain.TotalCost {
		t.Fatalf("reasoning should be a breakdown of output cost, with=%#v plain=%#v", withReasoning, plain)
	}
}

func TestRegistryCostSelectsTierForLongContextPricing(t *testing.T) {
	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	shortPrompt, err := registry.EstimateCost("gemini-2.5-pro", zeroruntime.Usage{
		InputTokens:  200_000,
		OutputTokens: 1_000,
	})
	if err != nil {
		t.Fatalf("EstimateCost(shortPrompt) returned error: %v", err)
	}
	longPrompt, err := registry.EstimateCost("gemini-2.5-pro", zeroruntime.Usage{
		InputTokens:  200_001,
		OutputTokens: 1_000,
	})
	if err != nil {
		t.Fatalf("EstimateCost(longPrompt) returned error: %v", err)
	}

	if shortPrompt.PricingTier == nil || shortPrompt.PricingTier.InputPerMillion != 1.25 {
		t.Fatalf("short tier = %#v, want input rate 1.25", shortPrompt.PricingTier)
	}
	if longPrompt.PricingTier == nil || longPrompt.PricingTier.InputPerMillion != 2.5 {
		t.Fatalf("long tier = %#v, want input rate 2.5", longPrompt.PricingTier)
	}
	if longPrompt.TotalCost <= shortPrompt.TotalCost {
		t.Fatalf("long prompt cost %v should be greater than short prompt cost %v", longPrompt.TotalCost, shortPrompt.TotalCost)
	}
}

func TestCostFormattingAndValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		cost float64
		want string
	}{
		{name: "zero", cost: 0, want: "$0.0000"},
		{name: "micro", cost: 0.000004, want: "<$0.0001"},
		{name: "below threshold", cost: 0.00009999, want: "<$0.0001"},
		{name: "threshold", cost: 0.0001, want: "$0.0001"},
		{name: "small fractional", cost: 0.009927, want: "$0.0099"},
		{name: "small", cost: 0.0126, want: "$0.0126"},
		{name: "fraction", cost: 0.42, want: "$0.42"},
		{name: "half", cost: 0.5, want: "$0.5"},
		{name: "just below one", cost: 0.999, want: "$0.999"},
		{name: "one", cost: 1, want: "$1.00"},
		{name: "large fraction", cost: 1.2391, want: "$1.24"},
		{name: "large", cost: 12.3456, want: "$12.35"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := FormatCostUSD(test.cost)
			if err != nil {
				t.Fatalf("FormatCostUSD returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("FormatCostUSD(%v) = %q, want %q", test.cost, got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name string
		cost float64
	}{
		{name: "negative", cost: -1},
		{name: "nan", cost: math.NaN()},
		{name: "positive infinity", cost: math.Inf(1)},
		{name: "negative infinity", cost: math.Inf(-1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := FormatCostUSD(test.cost); err == nil {
				t.Fatal("FormatCostUSD should reject invalid values")
			}
		})
	}

	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	_, err = registry.EstimateCost("gpt-4.1", zeroruntime.Usage{InputTokens: -1})
	if err == nil {
		t.Fatal("EstimateCost should reject negative usage")
	}
	if !strings.Contains(err.Error(), "input tokens") {
		t.Fatalf("negative usage error = %q, want input tokens", err.Error())
	}
}

func assertClose(t *testing.T, got float64, want float64) {
	t.Helper()

	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.0000001 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func pricedTestRegistry(t *testing.T) (Registry, error) {
	t.Helper()
	entries := DefaultModelEntries()
	prices := map[string]ModelCost{
		"gpt-4.1":          {InputPerMillion: 2, CachedInputPerMillion: 0.5, OutputPerMillion: 8},
		"gpt-4.1-mini":     {InputPerMillion: 0.4, CachedInputPerMillion: 0.1, OutputPerMillion: 1.6},
		"gpt-4-turbo":      {InputPerMillion: 10, OutputPerMillion: 30},
		"claude-haiku-3.5": {InputPerMillion: 0.8, CachedInputPerMillion: 0.08, OutputPerMillion: 4},
		"gemini-2.5-pro": {Tiers: []ModelCostTier{
			{UpToInputTokens: 200_000, InputPerMillion: 1.25, CachedInputPerMillion: 0.125, OutputPerMillion: 10},
			{InputPerMillion: 2.5, CachedInputPerMillion: 0.25, OutputPerMillion: 15},
		}},
	}
	for index := range entries {
		if cost, ok := prices[entries[index].ID]; ok {
			cost.Currency = "USD"
			cost.Unit = "per_1m_tokens"
			cost.Source = "test"
			cost.SourceLastVerified = "2026-01-01"
			entries[index].Cost = cost
		}
	}
	return NewRegistry(entries)
}

func TestCalculateCostRejectsAllMalformedSubsets(t *testing.T) {
	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	model, err := registry.Require("gpt-4.1")
	if err != nil {
		t.Fatalf("Require returned error: %v", err)
	}
	tests := []struct {
		name    string
		usage   zeroruntime.Usage
		wantErr string
	}{
		{"cached exceeds input", zeroruntime.Usage{InputTokens: 10, CachedInputTokens: 15, OutputTokens: 5}, "cached input tokens"},
		{"cache write plus cached exceeds input", zeroruntime.Usage{InputTokens: 100, CachedInputTokens: 60, CacheWriteTokens: 50, OutputTokens: 10}, "cache write tokens"},
		{"reasoning exceeds output", zeroruntime.Usage{InputTokens: 100, OutputTokens: 10, ReasoningTokens: 20}, "reasoning tokens"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CalculateCost(model, tc.usage)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCalculateCostPricedZeroForKnownModelWithZeroUsage(t *testing.T) {
	registry, err := pricedTestRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	cost, err := registry.EstimateCost("gpt-4.1", zeroruntime.Usage{
		InputTokens:  0,
		OutputTokens: 0,
	})
	if err != nil {
		t.Fatalf("EstimateCost returned error: %v", err)
	}
	if cost.TotalCost != 0 {
		t.Fatalf("total cost = %v, want 0 for zero usage", cost.TotalCost)
	}
}
