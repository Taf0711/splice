package modelregistry

import (
	"strings"
	"testing"
)

func TestModelEntryValidatesPRDShape(t *testing.T) {
	model := ModelEntry{
		ID:          "gpt-4.1-mini",
		DisplayName: "GPT-4.1 mini",
		APIModel:    "gpt-4.1-mini",
		Provider:    ProviderOpenAI,
		APIProviders: []ProviderKind{
			ProviderOpenAI,
			ProviderOpenAICompatible,
		},
		ContextLimits: ContextLimits{
			ContextWindow:   1_047_576,
			MaxOutputTokens: 32_768,
		},
		Capabilities: ModelCapabilities{
			ModelCapabilityChat,
			ModelCapabilityStreaming,
			ModelCapabilityToolCalling,
			ModelCapabilitySystemPrompt,
			ModelCapabilityLongContext,
		},
		Cost: ModelCost{
			Currency:              "USD",
			Unit:                  "per_1m_tokens",
			InputPerMillion:       0.4,
			OutputPerMillion:      1.6,
			CachedInputPerMillion: 0.1,
			Source:                "https://platform.openai.com/docs/pricing/",
			SourceLastVerified:    "2026-06-02",
		},
		Status:      ModelStatusActive,
		Aliases:     []string{"openai:gpt-4.1-mini"},
		Description: "OpenAI lower-cost long-context model for frequent edit loops.",
	}

	if err := model.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if !model.Supports(ModelCapabilityToolCalling) {
		t.Fatal("model should support tool calling")
	}
	if !model.AllowsProvider(ProviderOpenAICompatible) {
		t.Fatal("model should allow OpenAI-compatible runtime adapter")
	}
}

func TestCheapestPricingTierContextWindow(t *testing.T) {
	tests := []struct {
		name  string
		tiers []ModelCostTier
		want  int
	}{
		{
			name:  "one tier below window",
			tiers: []ModelCostTier{{UpToInputTokens: 272_000}},
			want:  272_000,
		},
		{
			name: "unsorted tiers choose lowest positive ceiling",
			tiers: []ModelCostTier{
				{UpToInputTokens: 500_000},
				{UpToInputTokens: 0},
				{UpToInputTokens: -1},
				{UpToInputTokens: 200_000},
			},
			want: 200_000,
		},
		{
			name: "no tiers",
			want: 1_050_000,
		},
		{
			name:  "fallback tier only",
			tiers: []ModelCostTier{{UpToInputTokens: 0}},
			want:  1_050_000,
		},
		{
			name:  "tier exceeds window",
			tiers: []ModelCostTier{{UpToInputTokens: 2_000_000}},
			want:  1_050_000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := ModelEntry{ContextLimits: ContextLimits{ContextWindow: 1_050_000}, Cost: ModelCost{Tiers: tt.tiers}}
			if got := CheapestPricingTierContextWindow(model); got != tt.want {
				t.Fatalf("CheapestPricingTierContextWindow() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestModelEntryRejectsInvalidContextLimits(t *testing.T) {
	model := validModelEntry()
	model.ContextLimits.ContextWindow = 1_000
	model.ContextLimits.MaxOutputTokens = 2_000

	err := model.Validate()
	if err == nil {
		t.Fatal("expected context limit validation error")
	}
	if !strings.Contains(err.Error(), "max output tokens") {
		t.Fatalf("error = %q, want max output tokens", err.Error())
	}
}

func TestModelEntryRejectsOpenAICompatibleAsPrimaryProvider(t *testing.T) {
	model := validModelEntry()
	model.Provider = ProviderOpenAICompatible

	err := model.Validate()
	if err == nil {
		t.Fatal("expected primary provider validation error")
	}
	if !strings.Contains(err.Error(), "primary provider") {
		t.Fatalf("error = %q, want primary provider", err.Error())
	}
}

func TestModelEntryRejectsProviderNotInAPIProviders(t *testing.T) {
	model := validModelEntry()
	model.APIProviders = []ProviderKind{ProviderAnthropic}

	err := model.Validate()
	if err == nil {
		t.Fatal("expected provider/api providers validation error")
	}
	if !strings.Contains(err.Error(), "api providers") {
		t.Fatalf("error = %q, want api providers", err.Error())
	}
}

func TestModelEntryRejectsUnknownContractEnums(t *testing.T) {
	model := validModelEntry()
	model.Capabilities = ModelCapabilities{"telepathy"}

	err := model.Validate()
	if err == nil {
		t.Fatal("expected capability validation error")
	}
	if !strings.Contains(err.Error(), "model capability") {
		t.Fatalf("error = %q, want model capability", err.Error())
	}

	model = validModelEntry()
	model.ReasoningEfforts = []ReasoningEffort{"warp"}

	err = model.Validate()
	if err == nil {
		t.Fatal("expected reasoning effort validation error")
	}
	if !strings.Contains(err.Error(), "reasoning effort") {
		t.Fatalf("error = %q, want reasoning effort", err.Error())
	}

	model = validModelEntry()
	model.Status = "retired"

	err = model.Validate()
	if err == nil {
		t.Fatal("expected model status validation error")
	}
	if !strings.Contains(err.Error(), "model status") {
		t.Fatalf("error = %q, want model status", err.Error())
	}
}

func TestModelEntryRejectsInvalidSourceLastVerifiedDate(t *testing.T) {
	model := validModelEntry()
	model.Cost.SourceLastVerified = "2026/06/02"

	err := model.Validate()
	if err == nil {
		t.Fatal("expected source last verified date validation error")
	}
	if !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Fatalf("error = %q, want YYYY-MM-DD", err.Error())
	}
}

func TestModelCostRequiresCompleteBasePricing(t *testing.T) {
	model := validModelEntry()
	model.Cost.InputPerMillion = 0

	err := model.Validate()
	if err == nil {
		t.Fatal("expected incomplete base pricing validation error")
	}
	if !strings.Contains(err.Error(), "base input and output rates") {
		t.Fatalf("error = %q, want base input and output rates", err.Error())
	}

	model = validModelEntry()
	model.Cost.OutputPerMillion = 0

	err = model.Validate()
	if err == nil {
		t.Fatal("expected incomplete base pricing validation error")
	}
	if !strings.Contains(err.Error(), "base input and output rates") {
		t.Fatalf("error = %q, want base input and output rates", err.Error())
	}
}

func TestModelCostAcceptsWhollyUnpriced(t *testing.T) {
	model := validModelEntry()
	model.Cost = ModelCost{Currency: "USD", Unit: "per_1m_tokens"}
	if err := model.Validate(); err != nil {
		t.Fatalf("unpriced model should validate: %v", err)
	}
}

func TestModelCostRejectsPricedWithoutSource(t *testing.T) {
	model := validModelEntry()
	model.Cost.Source = ""
	if err := model.Validate(); err == nil || !strings.Contains(err.Error(), "source is required") {
		t.Fatalf("priced model without source error = %v", err)
	}
}

func TestValidateCostTiersChecksCacheWriteRate(t *testing.T) {
	tier := ModelCostTier{InputPerMillion: 1, OutputPerMillion: 2, CacheWritePerMillion: -0.1}
	if err := validateCostTiers([]ModelCostTier{tier}); err == nil || !strings.Contains(err.Error(), "cache-write") {
		t.Fatalf("negative tier cache-write rate error = %v, want a validation error", err)
	}

	tier.CacheWritePerMillion = 0
	if err := validateCostTiers([]ModelCostTier{tier}); err != nil {
		t.Fatalf("zero tier cache-write rate must validate: %v", err)
	}
}

func TestModelRegistryResolvesStablePatterns(t *testing.T) {
	registry, err := NewRegistry([]ModelEntry{validModelEntry()})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}

	model, ok := registry.Get("OPENAI:GPT-4.1-MINI")
	if !ok {
		t.Fatal("expected model to resolve from case-insensitive match pattern")
	}
	if model.ID != "gpt-4.1-mini" {
		t.Fatalf("model ID = %q, want gpt-4.1-mini", model.ID)
	}
}

func TestModelRegistryClonesLookupEntriesOnConstruction(t *testing.T) {
	entries := []ModelEntry{validModelEntry()}
	entries[0].Cost.Notes = []string{"original note"}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}

	entries[0].Aliases[0] = "mutated-alias"
	entries[0].Capabilities[0] = ModelCapabilityVision
	entries[0].Cost.Notes[0] = "caller-owned mutation"

	model, ok := registry.Get("openai:gpt-4.1-mini")
	if !ok {
		t.Fatal("expected original alias to remain registered")
	}
	if model.Aliases[0] != "openai:gpt-4.1-mini" {
		t.Fatalf("alias = %q, want original alias", model.Aliases[0])
	}
	if model.Capabilities[0] != ModelCapabilityChat {
		t.Fatalf("capability = %q, want original capability", model.Capabilities[0])
	}
	if len(model.Cost.Notes) != 1 || model.Cost.Notes[0] != "original note" {
		t.Fatalf("cost notes = %#v, want original note", model.Cost.Notes)
	}
}

func TestRegistryRejectsInvalidModelEntries(t *testing.T) {
	model := validModelEntry()
	model.ContextLimits.MaxOutputTokens = model.ContextLimits.ContextWindow + 1

	_, err := NewRegistry([]ModelEntry{model})
	if err == nil {
		t.Fatal("expected invalid model validation error")
	}
	if !strings.Contains(err.Error(), "max output tokens") {
		t.Fatalf("error = %q, want max output tokens", err.Error())
	}
}

func TestRegistryDetectsDuplicateNormalizedLookupKeys(t *testing.T) {
	first := validModelEntry()
	second := validModelEntry()
	second.ID = "other-model"
	second.DisplayName = "Other model"
	second.APIModel = "other-model"
	second.Aliases = []string{"GPT-4.1-MINI"}

	_, err := NewRegistry([]ModelEntry{first, second})
	if err == nil {
		t.Fatal("expected duplicate lookup key validation error")
	}
	if !strings.Contains(err.Error(), "duplicate model lookup key") {
		t.Fatalf("error = %q, want duplicate model lookup key", err.Error())
	}
}

func TestRegistryDetectsDuplicateModelIDs(t *testing.T) {
	first := validModelEntry()
	second := validModelEntry()
	second.APIModel = "other-api-model"
	second.Aliases = []string{"other:model"}

	_, err := NewRegistry([]ModelEntry{first, second})
	if err == nil {
		t.Fatal("expected duplicate model id validation error")
	}
	if !strings.Contains(err.Error(), "duplicate model id") {
		t.Fatalf("error = %q, want duplicate model id", err.Error())
	}
}

func validModelEntry() ModelEntry {
	return ModelEntry{
		ID:          "gpt-4.1-mini",
		DisplayName: "GPT-4.1 mini",
		APIModel:    "gpt-4.1-mini",
		Provider:    ProviderOpenAI,
		ContextLimits: ContextLimits{
			ContextWindow:   1_047_576,
			MaxOutputTokens: 32_768,
		},
		Capabilities: ModelCapabilities{ModelCapabilityChat},
		Cost: ModelCost{
			Currency:           "USD",
			Unit:               "per_1m_tokens",
			InputPerMillion:    0.4,
			OutputPerMillion:   1.6,
			Source:             "https://platform.openai.com/docs/pricing/",
			SourceLastVerified: "2026-06-02",
		},
		Status:  ModelStatusActive,
		Aliases: []string{"openai:gpt-4.1-mini"},
	}
}
