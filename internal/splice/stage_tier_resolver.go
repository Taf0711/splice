package splice

import (
	"fmt"
	"math"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

var stageTierLabels = map[string]string{
	"code_writer":        "medium",
	"test_generator":     "medium",
	"design_crystallize": "medium",
	"plan_critic":        "reasoning",
}

// designStageTierLabels are the design-phase stages. They are not topology
// nodes (the design pipeline is separate from the execution pipeline), so they
// are enumerated always, independent of the active topology.
var designStageTierLabels = map[string]string{
	"design_crystallize": "medium",
	"plan_critic":        "reasoning",
}

// StageTierLabels returns the labels for the embedded default topology. It is
// the compatibility entry point for a caller that has no active topology.
func StageTierLabels() map[string]string {
	return StageTierLabelsFor(nil)
}

// StageTierLabelsFor returns the model-backed stage names of one topology with
// their resolver tier labels, plus the design-phase stages. A model-backed node
// with no known label defaults to "medium", so a custom prompt node is
// selectable in the model wizard. Model-free nodes are excluded because they
// never resolve a model. A nil topology means the embedded default.
func StageTierLabelsFor(topology *schemas.PipelineTopology) map[string]string {
	out := make(map[string]string, len(stageTierLabels))
	if topology == nil {
		for name, label := range stageTierLabels {
			out[name] = label
		}
		return out
	}
	// The design stages are never topology nodes, so they are always present.
	for name, label := range designStageTierLabels {
		out[name] = label
	}
	for _, node := range topology.Nodes {
		if node.EffectiveCapabilities().ModelFree {
			continue
		}
		if label, ok := stageTierLabels[node.Name]; ok {
			out[node.Name] = label
			continue
		}
		out[node.Name] = "medium"
	}
	return out
}

// StageTierResolver resolves a stage's model tier to a concrete provider.
// Returns nil provider when no tier label exists, when the primary is on a
// custom-compatible endpoint, when the primary is unknown to the catalog, or
// when no suitable model is found (caller falls back to the primary). It is
// the middle layer: explicit stage-models.json overrides are checked first
// by the caller.
type StageTierResolver func(stageName string) (agent.ModelSelection, error)

// TierResolverConfig provides the inputs needed to construct the optional
// stage tier resolver inside BuildStageModelResolvers. A nil Registry
// disables the tier layer.
type TierResolverConfig struct {
	PrimaryProfile config.ProviderProfile
	Registry       *modelregistry.Registry
}

// ResolveStageTierModel returns the catalog entry the tier resolver would pick
// for a stage, or nil when the stage has no tier label, the primary is on a
// custom-compatible endpoint, the primary is unknown to the catalog, or no
// suitable candidate exists. Pure: it builds no provider.
func ResolveStageTierModel(stageName string, primaryProfile config.ProviderProfile, registry modelregistry.Registry) *modelregistry.ModelEntry {
	tier := stageTierLabels[stageName]
	if tier == "" {
		return nil
	}
	switch primaryProfile.ProviderKind {
	case config.ProviderKindOpenAICompatible, config.ProviderKindAnthropicCompat:
		return nil
	}
	entry, ok := registry.Resolve(primaryProfile.Model)
	if !ok {
		return nil
	}
	primaryRate := effectiveInputRate(entry.Cost)
	candidates := registry.ListByProvider(entry.Provider)
	if tier == "reasoning" {
		var best modelregistry.ModelEntry
		for _, candidate := range candidates {
			if candidate.ID == entry.ID {
				continue
			}
			if !candidate.Supports(modelregistry.ModelCapabilityToolCalling) || !candidate.Supports(modelregistry.ModelCapabilityReasoning) {
				continue
			}
			rate := effectiveInputRate(candidate.Cost)
			if rate <= primaryRate {
				continue
			}
			if best.ID == "" || effectiveInputRate(best.Cost) < rate {
				best = candidate
			}
		}
		if best.ID == "" {
			return nil
		}
		return &best
	}
	var best modelregistry.ModelEntry
	for _, candidate := range candidates {
		if candidate.ID == entry.ID {
			continue
		}
		if !candidate.Supports(modelregistry.ModelCapabilityToolCalling) {
			continue
		}
		rate := effectiveInputRate(candidate.Cost)
		if rate >= primaryRate {
			continue
		}
		if best.ID == "" || rate < effectiveInputRate(best.Cost) {
			best = candidate
		}
	}
	if best.ID == "" {
		return nil
	}
	return &best
}

// NewStageTierResolver creates a resolver that maps a hardcoded stage tier
// label to the cheapest (or strongest, for "reasoning") tool-capable model
// in the primary's provider family. The returned provider is built lazily and
// cached in providerCache so both resolver layers share one cache.
func NewStageTierResolver(
	primaryProfile config.ProviderProfile,
	registry modelregistry.Registry,
	newProvider func(config.ProviderProfile) (agent.Provider, error),
	providerCache map[string]agent.Provider,
) StageTierResolver {
	if providerCache == nil {
		providerCache = make(map[string]agent.Provider)
	}
	return func(stageName string) (agent.ModelSelection, error) {
		best := ResolveStageTierModel(stageName, primaryProfile, registry)
		if best == nil {
			return agent.ModelSelection{}, nil
		}
		return buildCachedProvider(primaryProfile, *best, newProvider, providerCache)
	}
}

func buildCachedProvider(
	primaryProfile config.ProviderProfile,
	candidate modelregistry.ModelEntry,
	newProvider func(config.ProviderProfile) (agent.Provider, error),
	providerCache map[string]agent.Provider,
) (agent.ModelSelection, error) {
	selection := agent.ModelSelection{ProviderName: primaryProfile.Name, Model: candidate.ID}
	key := providerCacheKey(primaryProfile.Name, candidate.APIModel, "")
	if cached, ok := providerCache[key]; ok {
		selection.Provider = cached
		return selection, nil
	}
	if newProvider == nil {
		return agent.ModelSelection{}, fmt.Errorf("build provider for %q: provider factory is nil", candidate.ID)
	}
	cloned := primaryProfile
	cloned.Model = candidate.APIModel
	provider, err := newProvider(cloned)
	if err != nil {
		return agent.ModelSelection{}, fmt.Errorf("build provider for %q: %w", candidate.ID, err)
	}
	providerCache[key] = provider
	selection.Provider = provider
	return selection, nil
}

func effectiveInputRate(cost modelregistry.ModelCost) float64 {
	if cost.InputPerMillion > 0 {
		return cost.InputPerMillion
	}
	if len(cost.Tiers) > 0 {
		return cost.Tiers[0].InputPerMillion
	}
	return math.Inf(1)
}
