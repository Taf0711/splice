package splice

import (
	"fmt"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func providerCacheKey(profile, model, effort string) string {
	return profile + "\x00" + model + "\x00" + effort
}

// Model route origin labels: which rung of the ladder supplied a stage's model.
const (
	ModelOriginNode    = "node"
	ModelOriginStage   = "stage"
	ModelOriginDefault = "default"
	ModelOriginTier    = "tier"
	ModelOriginPrimary = "primary"
)

// ResolveModelRoute reports the effective model declaration for one stage and
// the rung that supplied it, without building a provider. It mirrors the
// executor precedence exactly: a node model, then a stage-models.json entry for
// the stage (even an incomplete one, which the executor then fails to build),
// then the Default entry, then the tier resolver when the stage has a tier
// label, then the primary model.
func ResolveModelRoute(stageName string, nodeModel *schemas.StageModelConfig, stageConfig schemas.StageModelConfigFile, hasTierLabel bool) (schemas.StageModelConfig, string) {
	if nodeModel != nil {
		return *nodeModel, ModelOriginNode
	}
	if specific, ok := stageConfig.Stages[stageName]; ok {
		return specific, ModelOriginStage
	}
	if stageConfig.Default.ProviderProfile != "" && stageConfig.Default.Model != "" {
		return stageConfig.Default, ModelOriginDefault
	}
	if hasTierLabel {
		return schemas.StageModelConfig{}, ModelOriginTier
	}
	return schemas.StageModelConfig{}, ModelOriginPrimary
}

// ModelResolvers bundles the routing hooks built from one stage-model config
// and one provider factory. All three share the profile map and the provider
// cache, so a node override and a stage override of the same model reuse one
// provider instance.
type ModelResolvers struct {
	Stage      agent.StageModelResolver
	Escalation agent.EscalationModelResolver
	Node       agent.NodeModelResolver
}

// BuildStageModelResolvers constructs the per-node, per-stage, and escalation
// routing hooks shared by headless exec and the interactive TUI. Providers are
// built lazily and cached for the lifetime of one pipeline run.
func BuildStageModelResolvers(
	stageConfig schemas.StageModelConfigFile,
	profiles []config.ProviderProfile,
	newProvider func(config.ProviderProfile) (agent.Provider, error),
	tierResolverConfig TierResolverConfig,
) ModelResolvers {
	profilesByName := make(map[string]config.ProviderProfile, len(profiles))
	for _, profile := range profiles {
		profilesByName[profile.Name] = profile
	}
	providerCache := make(map[string]agent.Provider)

	var tierResolver StageTierResolver
	if tierResolverConfig.Registry != nil {
		tierResolver = NewStageTierResolver(
			tierResolverConfig.PrimaryProfile,
			*tierResolverConfig.Registry,
			newProvider,
			providerCache,
		)
	}

	build := func(scope string, cfg schemas.StageModelConfig) (agent.ModelSelection, error) {
		profile, ok := profilesByName[cfg.ProviderProfile]
		if !ok {
			return agent.ModelSelection{}, fmt.Errorf("%s references unknown provider profile %q", scope, cfg.ProviderProfile)
		}
		selection := agent.ModelSelection{
			ProviderName:    cfg.ProviderProfile,
			Model:           cfg.Model,
			ReasoningEffort: cfg.ReasoningEffort,
		}
		cacheKey := providerCacheKey(cfg.ProviderProfile, cfg.Model, cfg.ReasoningEffort)
		if cached, ok := providerCache[cacheKey]; ok {
			selection.Provider = cached
			return selection, nil
		}
		if newProvider == nil {
			return agent.ModelSelection{}, fmt.Errorf("%s cannot build provider: provider factory is nil", scope)
		}
		cloned := profile
		cloned.Model = cfg.Model
		provider, err := newProvider(cloned)
		if err != nil {
			return agent.ModelSelection{}, fmt.Errorf("build provider for %s: %w", scope, err)
		}
		providerCache[cacheKey] = provider
		selection.Provider = provider
		return selection, nil
	}

	stageResolver := func(stageName string) (agent.ModelSelection, error) {
		cfg, specific := stageConfig.Resolve(stageName)
		if specific || (cfg.ProviderProfile != "" && cfg.Model != "") {
			return build(fmt.Sprintf("stage %q", stageName), cfg)
		}
		// Layer 2: batteries-included tier fallback (no explicit override).
		if tierResolver != nil {
			if selection, err := tierResolver(stageName); err != nil {
				return agent.ModelSelection{}, err
			} else if selection.Provider != nil {
				return selection, nil
			}
		}
		// Layer 3: primary (caller's fallback).
		return agent.ModelSelection{}, nil
	}

	escalationResolver := func() (agent.ModelSelection, error) {
		if stageConfig.Escalation == nil {
			return agent.ModelSelection{}, nil
		}
		return build("escalation", *stageConfig.Escalation)
	}

	// nodeResolver is the strongest rung: a topology node's explicit model. It
	// reuses the same build path, profile map, and cache as the stage resolver.
	nodeResolver := func(nodeName string, override agent.ModelOverride) (agent.ModelSelection, error) {
		return build(fmt.Sprintf("node %q", nodeName), schemas.StageModelConfig{
			ProviderProfile: override.ProviderProfile,
			Model:           override.Model,
			ReasoningEffort: override.ReasoningEffort,
		})
	}

	return ModelResolvers{Stage: stageResolver, Escalation: escalationResolver, Node: nodeResolver}
}
