package config

import (
	"strings"

	"github.com/Taf0711/splice/internal/flags"
)

// The default trajectory classifier. These are the settings validated in
// campaign A1 (2026-10-09, all five gates passed). The model is fixed. The
// chatgpt profile supplies only its endpoint and credentials. A1 sent no
// reasoning effort, so the default sends none. The 120 s timeout is the
// timeout A1 ran with, from its campaign config. No p99 measurement backs it.
const (
	trajectoryDefaultCatalogID  = "chatgpt"
	trajectoryDefaultModel      = "gpt-6.1-sol"
	trajectoryDefaultMode       = "active_experimental"
	trajectoryDefaultPack       = "traj_action_v2"
	trajectoryDefaultEffort     = ""
	trajectoryDefaultTimeoutMS  = 120000
	trajectoryDefaultBodyBytes  = 65536
	trajectoryDefaultCostCapUSD = 1.0
)

// TrajectoryClassifierProfile returns the provider profile the default
// trajectory classifier runs on. ok is false when the section is not the
// default, or when its profile is not among providers.
func TrajectoryClassifierProfile(section DecisionAdvisorConfig, providers []ProviderProfile) (ProviderProfile, bool) {
	if !section.TrajectoryDefault {
		return ProviderProfile{}, false
	}
	for _, profile := range providers {
		if profile.Name == section.ProviderProfile {
			return profile, true
		}
	}
	return ProviderProfile{}, false
}

// Environment names that set the advisor's run mode and model. The splice
// package reads the same names. It imports this package, so the names live here.
const (
	DecisionAdvisorModeEnv  = "SPLICE_DECISION_ADVISOR_MODE"
	DecisionAdvisorModelEnv = "SPLICE_DECISION_ADVISOR_MODEL"
)

// envDefinedAdvisor reports whether the decision advisor selector is on and
// the environment sets the advisor's mode or model. That advisor is defined
// by the environment, so the default classifier section must not replace it.
func envDefinedAdvisor(flagSet flags.Set, env map[string]string) bool {
	if !flagSet.Enabled(flags.DecisionAdvisorEnabled) {
		return false
	}
	return strings.TrimSpace(envValue(env, DecisionAdvisorModeEnv)) != "" ||
		strings.TrimSpace(envValue(env, DecisionAdvisorModelEnv)) != ""
}

// trajectoryClassifierDefault returns the decision_advisor section for the
// default trajectory classifier. An active explicit section (any mode set, and
// pipeline.decision_advisor on) wins and is returned unchanged. A dormant
// explicit section (pipeline.decision_advisor off) is inactive, so the default
// replaces it exactly as if no section existed. Its settings are not used.
// With the flag off, or with no configured provider whose catalog id is
// chatgpt, the section is returned unchanged and the rules decide.
func trajectoryClassifierDefault(section DecisionAdvisorConfig, providers []ProviderProfile, flagSet flags.Set) DecisionAdvisorConfig {
	if !flagSet.Enabled(flags.TrajectoryClassifierEnabled) {
		return section
	}
	// mode "off" is the user's opt-out: no default classifier, flag or not.
	if section.Mode == "off" {
		return section
	}
	dormant := section.Mode != ""
	if dormant && flagSet.Enabled(flags.DecisionAdvisorEnabled) {
		return section
	}
	for _, profile := range providers {
		if strings.TrimSpace(profile.CatalogID) != trajectoryDefaultCatalogID {
			continue
		}
		return DecisionAdvisorConfig{
			ExplicitInactive: dormant,
			Backend:          "provider",
			ProviderProfile:  profile.Name,
			Profile:          trajectoryDefaultModel,
			Mode:             trajectoryDefaultMode,
			QuestionPack:     trajectoryDefaultPack,
			ReasoningEffort:  trajectoryDefaultEffort,
			Limits: DecisionAdvisorLimits{
				MaxRequestBytes:   trajectoryDefaultBodyBytes,
				MaxResponseBytes:  trajectoryDefaultBodyBytes,
				TimeoutMS:         trajectoryDefaultTimeoutMS,
				MaxAdvisorCostUSD: trajectoryDefaultCostCapUSD,
			},
			TrajectoryDefault: true,
		}
	}
	return section
}
