package decisions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/oauth"
	"github.com/Taf0711/splice/internal/providers"
)

// ClassifierCredentialLoadable reports whether a profile can authenticate the
// default trajectory classifier: its own configured key, or a stored sign-in
// login that the store reads. It uses the lookup providerOptions runs, so the
// evaluator and doctor agree on whether the classifier can start.
func ClassifierCredentialLoadable(profile config.ProviderProfile) bool {
	if profile.HasConfiguredCredential() {
		return true
	}
	candidates := profile.OAuthLoginCandidates()
	if len(candidates) == 0 {
		return false
	}
	storage, err := config.ResolveAuthStorage(config.OAuthStorageEnv)
	if err != nil {
		return false
	}
	store, err := oauth.NewStore(oauth.StoreOptions{Storage: storage})
	if err != nil {
		return false
	}
	_, _, ok, err := oauth.FirstStored(store, candidates)
	return err == nil && ok
}

func providerOptions(profile config.ProviderProfile) (providers.Options, error) {
	options := providers.Options{}
	candidates := profile.OAuthLoginCandidates()
	if len(candidates) == 0 {
		return options, nil
	}
	storage, err := config.ResolveAuthStorage(config.OAuthStorageEnv)
	if err != nil {
		return options, err
	}
	store, err := oauth.NewStore(oauth.StoreOptions{Storage: storage})
	if err != nil {
		return options, fmt.Errorf("decision provider OAuth store: %w", err)
	}
	_, key, ok, err := oauth.FirstStored(store, candidates)
	if err != nil {
		return options, err
	}
	if !ok {
		return options, nil
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{Store: store, HTTPClient: &http.Client{Timeout: 30 * time.Second}, AllowPresets: true})
	if err != nil {
		return options, err
	}
	options.OAuthLoginKey = key
	options.OAuthResolver = func(ctx context.Context, refresh bool) (string, string, bool, error) {
		var token string
		var err error
		if refresh {
			token, err = manager.Handle401(ctx, key)
		} else {
			token, err = manager.GetFresh(ctx, key)
		}
		if errors.Is(err, oauth.ErrNoToken) {
			return "", "", false, nil
		}
		if err != nil {
			return "", "", false, err
		}
		return "Authorization", "Bearer " + token, true, nil
	}
	return options, nil
}
