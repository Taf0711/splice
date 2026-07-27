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

// BuildStageModelResolvers constructs the per-stage and escalation routing
// hooks shared by headless exec and the interactive TUI. Providers are built
// lazily and cached for the lifetime of one pipeline run.
func BuildStageModelResolvers(
	stageConfig schemas.StageModelConfigFile,
	profiles []config.ProviderProfile,
	newProvider func(config.ProviderProfile) (agent.Provider, error),
	tierResolverConfig TierResolverConfig,
) (agent.StageModelResolver, agent.EscalationModelResolver) {
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

	return stageResolver, escalationResolver
}
