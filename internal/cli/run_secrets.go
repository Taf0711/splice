package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/config"
	splicerun "github.com/Taf0711/splice/internal/splice"
)

// secretSources reads the stored credentials of a profile. openStore opens
// the API key store. oauthToken returns the stored OAuth token that the run
// sends as a bearer for a keyless profile, or "" when the profile has none. A
// nil oauthToken reads no OAuth login. routed reports whether the run can
// route to a profile. A nil routed treats every profile as routed.
type secretSources struct {
	openStore  func() (config.APIKeyGetter, error)
	oauthToken func(config.ProviderProfile) (string, error)
	routed     func(name string) bool
}

// runSecretValues returns the credential values that a run can send. Each stored
// key and each stored OAuth token is loaded first, so the event sink redacts
// the credential the provider uses. The key store opens at most once, and only
// when a profile needs it. A read error for the primary or for a routed profile
// is returned, and the caller stops the surface. The run never sends the
// credential of a profile it does not route to, so a read error for such a
// profile is skipped.
func runSecretValues(primary config.ProviderProfile, profiles []config.ProviderProfile, src secretSources) ([]string, error) {
	var store config.APIKeyGetter
	opened := false
	hydrate := func(profile config.ProviderProfile) (config.ProviderProfile, error) {
		if strings.TrimSpace(profile.APIKey) == "" && profile.APIKeyStored {
			if !opened {
				var err error
				if store, err = src.openStore(); err != nil {
					return profile, err
				}
				opened = true
			}
			hydrated, err := config.ApplyStoredAPIKey(profile, store)
			if err != nil {
				return profile, err
			}
			profile = hydrated
		}
		if strings.TrimSpace(profile.APIKey) == "" && strings.TrimSpace(profile.APIKeyEnv) != "" {
			profile.APIKey = strings.TrimSpace(os.Getenv(profile.APIKeyEnv))
		}
		// A keyless profile sends its OAuth login as the bearer, as the
		// provider factory does (oauthLoginForProfile).
		if strings.TrimSpace(profile.APIKey) == "" && src.oauthToken != nil {
			token, err := src.oauthToken(profile)
			if err != nil {
				return profile, err
			}
			profile.APIKey = token
		}
		return profile, nil
	}
	hydratedPrimary, err := hydrate(primary)
	if err != nil {
		return nil, fmt.Errorf("load provider %q key for the event surface: %w", primary.Name, err)
	}
	hydratedProfiles := make([]config.ProviderProfile, 0, len(profiles))
	for _, profile := range profiles {
		hydrated, err := hydrate(profile)
		if err != nil {
			if src.routed != nil && !src.routed(profile.Name) {
				continue
			}
			return nil, fmt.Errorf("load provider %q key for the event surface: %w", profile.Name, err)
		}
		hydratedProfiles = append(hydratedProfiles, hydrated)
	}
	return splicerun.RunSecretValues(hydratedPrimary, hydratedProfiles), nil
}

// runRoutes returns the routed check of secretSources for a run. A run whose
// routes cannot be known treats every profile as routed.
func runRoutes(primary, advisorProfile, stageConfigPath string, sources splicerun.TopologySources) func(string) bool {
	names, known := splicerun.RunRouteProfiles(primary, advisorProfile, stageConfigPath, sources)
	if !known {
		return nil
	}
	return func(name string) bool { return names[name] }
}

// storedOAuthToken reads the OAuth login that the provider factory binds to a
// profile, and refreshes it when it expires, as a model call does.
func storedOAuthToken(ctx context.Context) func(config.ProviderProfile) (string, error) {
	return func(profile config.ProviderProfile) (string, error) {
		resolver, _, err := oauthLoginForProfile(profile)
		if err != nil || resolver == nil {
			return "", err
		}
		_, value, ok, err := resolver(ctx, false)
		if err != nil || !ok {
			return "", err
		}
		return strings.TrimSpace(strings.TrimPrefix(value, "Bearer ")), nil
	}
}

// userKeyStoreOpener opens the credential store beside the user config file.
func userKeyStoreOpener(userConfigPath func() (string, error)) func() (config.APIKeyGetter, error) {
	return func() (config.APIKeyGetter, error) {
		if userConfigPath == nil {
			return nil, fmt.Errorf("no user config path for the credential store")
		}
		path, err := userConfigPath()
		if err != nil {
			return nil, fmt.Errorf("resolve user config path for stored API key: %w", err)
		}
		store, err := config.ProviderKeyStoreAt(filepath.Dir(path))
		if err != nil {
			return nil, fmt.Errorf("open API key store: %w", err)
		}
		return store, nil
	}
}
