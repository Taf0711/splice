// Package decisions constructs the configured decision evaluator.
package decisions

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/providers"
	"github.com/Taf0711/splice/internal/providers/httpdecisions"
	"github.com/Taf0711/splice/internal/providers/llmdecisions"
	"github.com/Taf0711/splice/internal/providers/openrouterdecisions"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// defaultCredentialLoadable finds the default section's profile among the
// resolved providers and checks its credential. A missing profile fails.
func defaultCredentialLoadable(cfg config.DecisionAdvisorConfig, resolved config.ResolvedConfig) bool {
	for _, profile := range resolved.Providers {
		if profile.Name == cfg.ProviderProfile {
			return ClassifierCredentialLoadable(profile)
		}
	}
	return false
}

// NewEvaluator constructs the configured backend when a flag permits it. The
// default trajectory section needs the classifier flag. A default section that
// cannot be built falls back to the rules: no evaluator, and no error. A
// configured section needs the decision advisor flag, and the classifier flag
// does not enable it.
func NewEvaluator(cfg config.DecisionAdvisorConfig, resolved config.ResolvedConfig) (zeroruntime.DecisionEvaluator, error) {
	if cfg.TrajectoryDefault {
		if !resolved.Flags.Enabled(flags.TrajectoryClassifierEnabled) {
			return nil, nil
		}
		// Without a usable ChatGPT credential the default cannot start. The run
		// then falls back to the rules, the same state doctor reports.
		if !defaultCredentialLoadable(cfg, resolved) {
			return nil, nil
		}
		evaluator, err := newEvaluator(cfg, resolved)
		if err != nil {
			return nil, nil
		}
		return evaluator, nil
	}
	if !resolved.Flags.Enabled(flags.DecisionAdvisorEnabled) {
		return nil, nil
	}
	return newEvaluator(cfg, resolved)
}

func newEvaluator(cfg config.DecisionAdvisorConfig, resolved config.ResolvedConfig) (zeroruntime.DecisionEvaluator, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	limits := cfg.Limits
	defaults := config.DefaultDecisionAdvisorLimits()
	if limits.MaxRequestBytes == 0 {
		limits.MaxRequestBytes = defaults.MaxRequestBytes
	}
	if limits.MaxResponseBytes == 0 {
		limits.MaxResponseBytes = defaults.MaxResponseBytes
	}
	if limits.TimeoutMS == 0 {
		limits.TimeoutMS = defaults.TimeoutMS
	}
	timeout := time.Duration(limits.TimeoutMS) * time.Millisecond
	switch cfg.Backend {
	case "", "openrouter_decisions":
		key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
		if key == "" {
			if store, err := config.ProviderKeyStore(); err == nil && store != nil {
				if value, ok, err := store.Get("openrouter"); err == nil && ok {
					key = value
				}
			}
		}
		return openrouterdecisions.NewClient(key,
			openrouterdecisions.WithHTTPClient(&http.Client{Timeout: timeout}),
			openrouterdecisions.WithMaxRequestBytes(limits.MaxRequestBytes),
			openrouterdecisions.WithMaxResponseBytes(limits.MaxResponseBytes),
		), nil
	case "http":
		return httpdecisions.NewClient(httpdecisions.Options{
			Endpoint:         cfg.Endpoint,
			APIKey:           os.Getenv(cfg.APIKeyEnv),
			MaxRequestBytes:  limits.MaxRequestBytes,
			MaxResponseBytes: limits.MaxResponseBytes,
			Timeout:          timeout,
		}), nil
	case "provider":
		for _, profile := range resolved.Providers {
			if profile.Name != cfg.ProviderProfile {
				continue
			}
			if profile.APIKeyStored && profile.APIKey == "" {
				store, err := config.ProviderKeyStore()
				if err != nil {
					return nil, err
				}
				profile, err = config.ApplyStoredAPIKey(profile, store)
				if err != nil {
					return nil, err
				}
			}
			profile = decisionProfile(cfg, profile)
			options, err := providerOptions(profile)
			if err != nil {
				return nil, err
			}
			provider, err := providers.New(profile, options)
			if err != nil {
				return nil, fmt.Errorf("decision provider %q: %w", profile.Name, err)
			}
			return llmdecisions.NewClient(provider, llmdecisions.Options{
				Model:            profile.Model,
				ReasoningEffort:  cfg.ReasoningEffort,
				MaxRequestBytes:  limits.MaxRequestBytes,
				MaxResponseBytes: limits.MaxResponseBytes,
				Timeout:          timeout,
			}), nil
		}
		return nil, fmt.Errorf("decision_advisor.provider_profile: unknown profile %q", cfg.ProviderProfile)
	default:
		return nil, fmt.Errorf("decision_advisor.backend: unknown backend %q", cfg.Backend)
	}
}

// decisionProfile returns the profile the decision client runs on. The default
// classifier keeps its validated model. The profile supplies only its endpoint
// and credentials.
func decisionProfile(cfg config.DecisionAdvisorConfig, profile config.ProviderProfile) config.ProviderProfile {
	if cfg.TrajectoryDefault && cfg.Profile != "" {
		profile.Model = cfg.Profile
	}
	return profile
}
