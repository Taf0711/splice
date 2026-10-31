package splice

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
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
			return agent.ModelSelection{}, &providerBuildError{err: fmt.Errorf("build provider for %s: %w", scope, err)}
		}
		providerCache[cacheKey] = provider
		selection.Provider = provider
		return selection, nil
	}

	stageResolver := func(stageName string) (agent.ModelSelection, error) {
		cfg, specific := stageConfig.Resolve(stageName)
		if specific || (cfg.ProviderProfile != "" && cfg.Model != "") {
			return stageConfigRoute(build(fmt.Sprintf("stage %q", stageName), cfg))
		}
		// Layer 2: batteries-included tier fallback (no explicit override).
		if tierResolver != nil {
			if selection, err := tierResolver(stageName); err != nil {
				// The tier layer is a convenience. Its failure falls back to
				// the run model with a warning; a stage-models.json route does not.
				return agent.ModelSelection{}, &stageTierFallback{err: err}
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
		return stageConfigRoute(build("escalation", *stageConfig.Escalation))
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

// stageConfigRoute names the fix when a stage-models.json route picks a model
// that the profile's endpoint family cannot serve. The operator wrote that
// route, so the error is invalid input, not a provider failure.
func stageConfigRoute(selection agent.ModelSelection, err error) (agent.ModelSelection, error) {
	var family *modelregistry.EndpointFamilyError
	if err != nil && errors.As(err, &family) {
		// %v drops the providerBuildError mark, so preflight reports invalid
		// input (exit 2).
		return agent.ModelSelection{}, fmt.Errorf("%v; stage-models.json routes it to that profile: edit stage-models.json or re-run onboarding (splice setup) to pick a model the endpoint serves", err)
	}
	return selection, err
}

// stageTierFallback marks a failure of the automatic tier layer. The caller
// keeps the run model and warns. Every other stage resolver error fails the
// run, because the operator wrote that route.
type stageTierFallback struct {
	err error
}

func (e *stageTierFallback) Error() string { return e.err.Error() }

func (e *stageTierFallback) Unwrap() error { return e.err }

// providerBuildError marks a route that names a known profile whose provider
// cannot start (a missing API key, a missing OAuth token, a keychain
// failure). The route is valid input, so Run keeps this a provider failure.
type providerBuildError struct {
	err error
}

func (e *providerBuildError) Error() string { return e.err.Error() }

func (e *providerBuildError) Unwrap() error { return e.err }

// PinProfileGaps returns one message per node pin whose provider profile is
// not in profiles. profiles is the provider list of the user's config. A
// profile can differ per machine, so the caller reports these as warnings;
// the run itself still fails at preflight when the pin cannot resolve.
func PinProfileGaps(topology *schemas.PipelineTopology, profiles []config.ProviderProfile) []string {
	if topology == nil {
		return nil
	}
	known := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		known[profile.Name] = true
	}
	var gaps []string
	for _, node := range topology.Nodes {
		if node.Model == nil || known[node.Model.ProviderProfile] {
			continue
		}
		gaps = append(gaps, fmt.Sprintf("node %s pins provider profile %q, which this config does not define. A run stops before its first request until the profile exists.", node.Name, node.Model.ProviderProfile))
	}
	return gaps
}

// RunRouteProfiles returns the provider profile names that a run can route
// to: the primary, the decision advisor profile (the trajectory classifier
// route), each stage-models.json route (a stage, the default, and
// escalation), and each node pin of the active topology. An empty
// advisorProfile or stageConfigPath adds nothing. known is false when the
// stage model file or the topology does not load. The caller then treats
// every profile as routed.
func RunRouteProfiles(primary, advisorProfile, stageConfigPath string, sources TopologySources) (names map[string]bool, known bool) {
	names = map[string]bool{primary: true, advisorProfile: true}
	known = true
	if stageConfigPath != "" {
		cfg, err := schemas.LoadStageModelConfig(stageConfigPath)
		if err != nil {
			known = false
		}
		names[cfg.Default.ProviderProfile] = true
		if cfg.Escalation != nil {
			names[cfg.Escalation.ProviderProfile] = true
		}
		for _, route := range cfg.Stages {
			names[route.ProviderProfile] = true
		}
	}
	topology, _, _, err := ResolveTopology(sources)
	if err != nil {
		known = false
	} else {
		for _, node := range topology.Nodes {
			if node.Model != nil {
				names[node.Model.ProviderProfile] = true
			}
		}
	}
	delete(names, "")
	return names, known
}

// RunSecretValues returns the credential values that a run can send to a
// provider: each profile's API key, auth header value, and custom header values.
// Every route (stage, node pin, and escalation) names a profile from profiles,
// and so does the primary. The caller must hydrate stored keys first. The list is
// deduplicated and skips empty values. Build it once per run.
func RunSecretValues(primary config.ProviderProfile, profiles []config.ProviderProfile) []string {
	seen := make(map[string]bool)
	var values []string
	add := func(value string) {
		if strings.TrimSpace(value) == "" || seen[value] {
			return
		}
		seen[value] = true
		values = append(values, value)
	}
	for _, profile := range append([]config.ProviderProfile{primary}, profiles...) {
		add(profile.APIKey)
		add(profile.AuthHeaderValue)
		names := make([]string, 0, len(profile.CustomHeaders))
		for name := range profile.CustomHeaders {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			add(profile.CustomHeaders[name])
		}
	}
	return values
}
