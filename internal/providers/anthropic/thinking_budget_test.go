package anthropic

import (
	"testing"

	"github.com/Taf0711/splice/internal/modelregistry"
)

// xhigh and max are enum members this provider family is the reason for, but
// they fell through to the default budget of 0, and a zero budget leaves the
// request without thinking enabled at all. Asking for the most reasoning turned
// reasoning off.
func TestThinkingBudgetForEffortClampsTopTiers(t *testing.T) {
	for _, test := range []struct {
		effort string
		want   int
	}{
		{effort: "minimal", want: minThinkingBudget},
		{effort: "low", want: 4096},
		{effort: "medium", want: 10000},
		{effort: "high", want: 24000},
		{effort: "xhigh", want: 24000},
		{effort: "max", want: 24000},
		{effort: "XHigh", want: 24000},
		{effort: "  max  ", want: 24000},
		{effort: "none", want: 0},
		{effort: "", want: 0},
		{effort: "bogus", want: 0},
	} {
		t.Run(test.effort, func(t *testing.T) {
			if got := thinkingBudgetForEffort(test.effort); got != test.want {
				t.Fatalf("thinkingBudgetForEffort(%q) = %d, want %d", test.effort, got, test.want)
			}
		})
	}
}

// TestThinkingBudgetFitsCatalogMaxOutputTokens pins the invariant that every
// Anthropic model's thinking budget plus the reserved response fits inside the
// catalog's MaxOutputTokens for that model. resolveThinking raises max_tokens
// with no ceiling, so a budget above MaxOutputTokens minus minResponseTokens
// produces a max_tokens the Anthropic API rejects with a 400.
func TestThinkingBudgetFitsCatalogMaxOutputTokens(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	models := registry.ListByProvider(modelregistry.ProviderAnthropic)
	if len(models) == 0 {
		t.Fatal("no Anthropic models in the registry")
	}
	for _, entry := range models {
		efforts := registry.ReasoningEfforts(entry.ID)
		if len(efforts) == 0 {
			continue // model never enables thinking
		}
		for _, effort := range efforts {
			effort := effort
			t.Run(entry.ID+"/"+string(effort), func(t *testing.T) {
				budget := thinkingBudgetForEffort(string(effort))
				if budget <= 0 {
					return
				}
				required := budget + minResponseTokens
				max := entry.ContextLimits.MaxOutputTokens
				if required > max {
					t.Fatalf("model %s effort %q budget %d requires %d max_tokens but catalog MaxOutputTokens is %d; resolveThinking would send a max_tokens the API rejects", entry.ID, effort, budget, required, max)
				}
			})
		}
	}
}
