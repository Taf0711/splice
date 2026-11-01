package cli

import (
	"testing"

	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/providermodeldiscovery"
)

// localOllamaRegistry holds one local model the way a rebuilt stage registry
// does: a catalog entry with chat and no tool-calling yet.
func localOllamaRegistry(t *testing.T) modelregistry.Registry {
	t.Helper()
	registry, err := modelregistry.NewRegistry([]modelregistry.ModelEntry{{
		ID:            "local-llama",
		DisplayName:   "local-llama",
		APIModel:      "local-llama",
		Provider:      modelregistry.ProviderOpenAI,
		ContextLimits: modelregistry.ContextLimits{ContextWindow: 8192, MaxOutputTokens: 4096},
		Capabilities:  []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat},
		Cost:          modelregistry.ModelCost{Currency: "USD", Unit: "per_1m_tokens"},
		Aliases:       []string{"local-llama"},
		Status:        modelregistry.ModelStatusActive,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// The Ollama overlay lands on the registry the run uses. It adds the probed
// tool-calling and reasoning support, and it removes tool-calling only when the
// daemon reported a list that omits it.
func TestApplyOllamaOverlayRecordsProbedSupport(t *testing.T) {
	registry := localOllamaRegistry(t)
	applyOllamaOverlay(&registry, "local-llama", providermodeldiscovery.OllamaCapabilities{Reported: true, ToolCall: true, Reasoning: true})
	if !registry.SupportsCapability("local-llama", modelregistry.ModelCapabilityToolCalling) {
		t.Fatal("probed tool-calling was not recorded on the registry")
	}
	if len(registry.ReasoningEfforts("local-llama")) == 0 {
		t.Fatal("probed reasoning support did not record reasoning efforts")
	}

	applyOllamaOverlay(&registry, "local-llama", providermodeldiscovery.OllamaCapabilities{Reported: true, ToolCall: false})
	if registry.SupportsCapability("local-llama", modelregistry.ModelCapabilityToolCalling) {
		t.Fatal("a reported list without tool-calling must remove it")
	}
}

// A missing capability list (Reported false) never removes a working setup.
func TestApplyOllamaOverlayKeepsToolCallingWithoutReport(t *testing.T) {
	registry := localOllamaRegistry(t)
	applyOllamaOverlay(&registry, "local-llama", providermodeldiscovery.OllamaCapabilities{Reported: true, ToolCall: true})
	applyOllamaOverlay(&registry, "local-llama", providermodeldiscovery.OllamaCapabilities{Reported: false, ToolCall: false})
	if !registry.SupportsCapability("local-llama", modelregistry.ModelCapabilityToolCalling) {
		t.Fatal("an unreported capability list must keep tool-calling")
	}
}
