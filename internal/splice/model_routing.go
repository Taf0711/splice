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
) (
	func(string) (agent.Provider, string, string, error),
	func() (agent.Provider, string, string, error),
) {
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

	build := func(scope string, cfg schemas.StageModelConfig) (agent.Provider, string, string, error) {
		profile, ok := profilesByName[cfg.ProviderProfile]
		if !ok {
			return nil, "", "", fmt.Errorf("%s references unknown provider profile %q", scope, cfg.ProviderProfile)
		}
		cacheKey := providerCacheKey(cfg.ProviderProfile, cfg.Model, cfg.ReasoningEffort)
		if cached, ok := providerCache[cacheKey]; ok {
			return cached, cfg.Model, cfg.ReasoningEffort, nil
		}
		if newProvider == nil {
			return nil, "", "", fmt.Errorf("%s cannot build provider: provider factory is nil", scope)
		}
		cloned := profile
		cloned.Model = cfg.Model
		provider, err := newProvider(cloned)
		if err != nil {
			return nil, "", "", fmt.Errorf("build provider for %s: %w", scope, err)
		}
		providerCache[cacheKey] = provider
		return provider, cfg.Model, cfg.ReasoningEffort, nil
	}

	stageResolver := func(stageName string) (agent.Provider, string, string, error) {
		cfg, specific := stageConfig.Resolve(stageName)
		if specific || (cfg.ProviderProfile != "" && cfg.Model != "") {
			return build(fmt.Sprintf("stage %q", stageName), cfg)
		}
		// Layer 2: batteries-included tier fallback (no explicit override).
		if tierResolver != nil {
			if p, m, e, err := tierResolver(stageName); err != nil {
				return nil, "", "", err
			} else if p != nil {
				return p, m, e, nil
			}
		}
		// Layer 3: primary (caller's fallback).
		return nil, "", "", nil
	}

	escalationResolver := func() (agent.Provider, string, string, error) {
		if stageConfig.Escalation == nil {
			return nil, "", "", nil
		}
		return build("escalation", *stageConfig.Escalation)
	}

	return stageResolver, escalationResolver
}
