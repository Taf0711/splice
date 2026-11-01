package usage

import (
	"errors"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// servedEntry lists a model with a flat price, the way a derived catalog entry
// does for one provider profile.
func servedEntry(id string, inputPerMillion float64) modelregistry.ModelEntry {
	return modelregistry.ModelEntry{
		ID: id, DisplayName: id, APIModel: id, Provider: modelregistry.ProviderOpenAI,
		ContextLimits: modelregistry.ContextLimits{ContextWindow: 128000, MaxOutputTokens: 8192},
		Capabilities:  []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat},
		Cost: modelregistry.ModelCost{
			Currency: "USD", Unit: "per_1m_tokens",
			InputPerMillion: inputPerMillion, OutputPerMillion: inputPerMillion,
			Source: "models.dev/api.json", SourceLastVerified: "2026-10-10",
		},
		Status:  modelregistry.ModelStatusActive,
		Aliases: []string{id},
	}
}

func mustRegistry(t *testing.T, entries ...modelregistry.ModelEntry) modelregistry.Registry {
	t.Helper()
	registry, err := modelregistry.NewRegistry(entries)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// A stage routed to provider B is priced at B's price, even though the active
// provider A also lists the same model at a different price.
func TestCostCatalogsPriceFromServingProvider(t *testing.T) {
	registries := map[string]modelregistry.Registry{
		"a": mustRegistry(t, servedEntry("shared-model", 1)),
		"b": mustRegistry(t, servedEntry("shared-model", 4)),
	}
	catalogs := NewCostCatalogs(func(profile string) (modelregistry.Registry, error) {
		return registries[profile], nil
	})
	catalogs.SetProfileCatalogIDs(map[string]string{"a": "openai", "b": "openai"})
	usage := zeroruntime.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got := catalogs.Estimate("b", "shared-model", usage, true)
	if got.Status != agent.CostStatusPriced || got.CostUSD == nil || *got.CostUSD != 8 {
		t.Fatalf("served by b = %+v, want B's price 4/4 per 1M tokens (total 8)", got)
	}
	active := catalogs.Estimate("a", "shared-model", usage, true)
	if active.CostUSD == nil || *active.CostUSD != 2 {
		t.Fatalf("served by a = %+v, want A's price (total 2)", active)
	}
}

// A model listed only under the active provider is unpriced for a request
// served by another provider. The active provider's price is never borrowed.
func TestCostCatalogsDoNotBorrowAnotherProvidersPrice(t *testing.T) {
	catalogs := NewCostCatalogs(func(profile string) (modelregistry.Registry, error) {
		if profile == "a" {
			return mustRegistry(t, servedEntry("only-under-a", 1)), nil
		}
		return mustRegistry(t), nil
	})
	usage := zeroruntime.Usage{InputTokens: 500, OutputTokens: 500}
	got := catalogs.Estimate("b", "only-under-a", usage, true)
	if got.Status != agent.CostStatusUnpriced || got.CostUSD != nil || got.UnpricedReason == "" {
		t.Fatalf("served by b = %+v, want unpriced with a reason", got)
	}
	if got := catalogs.Estimate("", "only-under-a", usage, true); got.Status != agent.CostStatusUnpriced {
		t.Fatalf("no served provider = %+v, want unpriced", got)
	}
}

// Each profile's registry is built once per run and then reused. A failed load
// is unpriced with the reason, and is not retried.
func TestCostCatalogsBuildEachProfileOnce(t *testing.T) {
	loads := map[string]int{}
	catalogs := NewCostCatalogs(func(profile string) (modelregistry.Registry, error) {
		loads[profile]++
		if profile == "broken" {
			return modelregistry.Registry{}, errors.New("no catalog")
		}
		return mustRegistry(t, servedEntry("m", 1)), nil
	})
	usage := zeroruntime.Usage{InputTokens: 10, OutputTokens: 10}
	for i := 0; i < 3; i++ {
		catalogs.Estimate("a", "m", usage, true)
		if got := catalogs.Estimate("broken", "m", usage, true); got.Status != agent.CostStatusUnpriced || got.UnpricedReason == "" {
			t.Fatalf("broken catalog = %+v, want unpriced with a reason", got)
		}
	}
	if loads["a"] != 1 || loads["broken"] != 1 {
		t.Fatalf("loads = %v, want each profile built once", loads)
	}
}

// gpt61Entry lists gpt-6.1-sol at $2 input and $10 output per 1M tokens.
func gpt61Entry() modelregistry.ModelEntry {
	entry := servedEntry("gpt-6.1-sol", 2)
	entry.Cost.OutputPerMillion = 10
	return entry
}

// A ChatGPT sign-in profile is a subscription: its requests are unpriced with
// the owner's reason, even though the openai catalog lists the model. An openai
// API-key profile serving the same model is still priced at $2/$10.
func TestCostCatalogsChatGPTSubscriptionIsUnpriced(t *testing.T) {
	catalogs := NewCostCatalogs(func(profile string) (modelregistry.Registry, error) {
		return mustRegistry(t, gpt61Entry()), nil
	})
	catalogs.SetSubscriptionProfiles(map[string]bool{"chatgpt-61": true})
	catalogs.SetProfileCatalogIDs(map[string]string{"openai-key": "openai"})
	usageTokens := zeroruntime.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	sub := catalogs.Estimate("chatgpt-61", "gpt-6.1-sol", usageTokens, true)
	if sub.Status != agent.CostStatusUnpriced || sub.CostUSD != nil || !sub.Subscription {
		t.Fatalf("chatgpt-served request = %+v, want unpriced subscription", sub)
	}
	if sub.UnpricedReason != "ChatGPT subscription: not billed per request" {
		t.Fatalf("reason = %q", sub.UnpricedReason)
	}

	api := catalogs.Estimate("openai-key", "gpt-6.1-sol", usageTokens, true)
	if api.Status != agent.CostStatusPriced || api.CostUSD == nil || *api.CostUSD != 12 || api.Subscription {
		t.Fatalf("openai API-key request = %+v, want priced at $2/$10 (total 12)", api)
	}
}

// The curated catalog is in every profile's registry, but its price comes from
// one provider's record. A custom OpenAI-compatible profile serving gpt-4.1 is
// not priced at OpenAI's list price. The owner catalog prices it.
func TestCuratedPriceComesOnlyFromItsOwnCatalog(t *testing.T) {
	catalogs := NewProfileCostCatalogs([]config.ProviderProfile{
		{Name: "openai", CatalogID: "openai"},
		{Name: "my-proxy", CatalogID: "openai-compatible"},
		{Name: "openrouter", CatalogID: "openrouter"},
	})
	usageTokens := zeroruntime.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	proxy := catalogs.Estimate("my-proxy", "gpt-4.1", usageTokens, true)
	if proxy.Status != agent.CostStatusUnpriced || proxy.CostUSD != nil || proxy.UnpricedReason != "no price for gpt-4.1 on my-proxy" {
		t.Fatalf("my-proxy gpt-4.1 = %+v, want unpriced with the owner reason", proxy)
	}
	openai := catalogs.Estimate("openai", "gpt-4.1", usageTokens, true)
	if openai.Status != agent.CostStatusPriced || openai.CostUSD == nil {
		t.Fatalf("openai gpt-4.1 = %+v, want priced from openai", openai)
	}
	routed := catalogs.Estimate("openrouter", "moonshotai/kimi-k3", usageTokens, true)
	if routed.Status != agent.CostStatusPriced || routed.CostUSD == nil {
		t.Fatalf("openrouter openai/gpt-4.1 = %+v, want priced from openrouter", routed)
	}
}
