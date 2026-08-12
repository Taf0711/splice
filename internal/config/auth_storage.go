package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	CredentialStorageEnv = "SPLICE_CRED_STORAGE"
	OAuthStorageEnv      = "SPLICE_OAUTH_STORAGE"
)

// ValidateAuthStorage validates an auth storage selector. An empty value keeps
// automatic backend selection.
func ValidateAuthStorage(storage string) (string, error) {
	storage = strings.ToLower(strings.TrimSpace(storage))
	switch storage {
	case "", "keyring", "encrypted-file", "file":
		return storage, nil
	default:
		return "", fmt.Errorf("invalid auth.storage %q: expected keyring, encrypted-file, or file", storage)
	}
}

// ResolveAuthStorageAt resolves a user auth backend. The user config has
// priority over the backend-specific environment variable. An empty result
// keeps the store's automatic selection policy.
func ResolveAuthStorageAt(path, envName string) (string, error) {
	path = strings.TrimSpace(path)
	if path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			var cfg FileConfig
			if err := json.Unmarshal(data, &cfg); err != nil {
				return "", fmt.Errorf("invalid config JSON %s: %w", path, err)
			}
			if storage := strings.TrimSpace(cfg.Auth.Storage); storage != "" {
				return storage, nil
			}
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("read config %s: %w", path, err)
		}
	}
	storage := strings.ToLower(strings.TrimSpace(os.Getenv(envName)))
	if storage == "" {
		return "", nil
	}
	if _, err := ValidateAuthStorage(storage); err != nil {
		return "", fmt.Errorf("invalid %s %q: expected keyring, encrypted-file, or file", envName, storage)
	}
	return storage, nil
}

func ResolveAuthStorage(envName string) (string, error) {
	path, err := DefaultUserConfigPath()
	if err != nil {
		return "", err
	}
	return ResolveAuthStorageAt(path, envName)
}

// SetAuthStorage persists the backend used by a successful auth login.
func SetAuthStorage(path, storage string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	storage, err := ValidateAuthStorage(storage)
	if err != nil {
		return FileConfig{}, err
	}
	if storage == "" {
		return FileConfig{}, fmt.Errorf("auth.storage is required")
	}
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg.Auth.Storage = storage
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}
