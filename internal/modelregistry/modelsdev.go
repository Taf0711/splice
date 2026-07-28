package modelregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Live models.dev overlay for the curated catalog. The hand-maintained
// DefaultModelEntries list is the source of truth for identity: ids, aliases,
// match patterns, deprecations, escalation targets. Its VOLATILE facts
// (context window, max output tokens, per-million pricing) go stale between
// releases. When a cached snapshot of https://models.dev/api.json is present,
// those fields are refreshed from it at registry construction; everything else
// stays curated. The overlay adds derived entries only with an explicit
// models.dev provider key and never touches the network on the registry hot
// path. Fetching happens only in the explicit background refresh, cached to
// disk with a TTL.

const (
	modelsDevDefaultURL = "https://models.dev/api.json"
	// modelsDevRefreshAfter is how old the cache may get before a background
	// refresh re-fetches it.
	modelsDevRefreshAfter = 24 * time.Hour
	// modelsDevMaxAge is the oldest cache still applied as an overlay. Beyond
	// this the curated catalog (updated with the binary) is likely fresher than
	// the snapshot, so a stale file is ignored rather than trusted.
	modelsDevMaxAge      = 7 * 24 * time.Hour
	modelsDevFetchLimit  = 32 << 20 // 32MiB guard on the response body
	modelsDevFetchWindow = 15 * time.Second
)

// modelsDevModel is the subset of a models.dev model record the overlay uses.
type modelsDevModel struct {
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input      float64             `json:"input"`
		Output     float64             `json:"output"`
		CacheRead  float64             `json:"cache_read"`
		CacheWrite float64             `json:"cache_write"`
		Tiers      []modelsDevCostTier `json:"tiers"`
	} `json:"cost"`
}

type modelsDevCostTier struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Tier       struct {
		Type string `json:"type"`
		Size int    `json:"size"`
	} `json:"tier"`
}

// modelsDevCostTiers converts models.dev context steps to registry tiers.
// Each step changes rates above its context size.
func modelsDevCostTiers(record modelsDevModel) []ModelCostTier {
	if len(record.Cost.Tiers) == 0 {
		return nil
	}
	steps := append([]modelsDevCostTier(nil), record.Cost.Tiers...)
	sort.SliceStable(steps, func(left, right int) bool {
		return steps[left].Tier.Size < steps[right].Tier.Size
	})

	tiers := make([]ModelCostTier, 0, len(steps)+1)
	inputRate := record.Cost.Input
	outputRate := record.Cost.Output
	cachedRate := record.Cost.CacheRead
	cacheWriteRate := record.Cost.CacheWrite
	for _, step := range steps {
		if step.Tier.Type != "context" || step.Tier.Size <= 0 || step.Input <= 0 || step.Output <= 0 {
			return nil
		}
		tiers = append(tiers, ModelCostTier{
			UpToInputTokens:       step.Tier.Size,
			InputPerMillion:       inputRate,
			OutputPerMillion:      outputRate,
			CachedInputPerMillion: cachedRate,
			CacheWritePerMillion:  cacheWriteRate,
		})
		inputRate = step.Input
		outputRate = step.Output
		// An absent or zero cache rate means the upstream record did not restate it.
		// Keep the prior rate; cache is not free above the boundary.
		if step.CacheRead > 0 {
			cachedRate = step.CacheRead
		}
		if step.CacheWrite > 0 {
			cacheWriteRate = step.CacheWrite
		}
	}
	tiers = append(tiers, ModelCostTier{
		InputPerMillion:       inputRate,
		OutputPerMillion:      outputRate,
		CachedInputPerMillion: cachedRate,
		CacheWritePerMillion:  cacheWriteRate,
	})
	if err := validateCostTiers(tiers); err != nil {
		return nil
	}
	return tiers
}

// modelsDevProvider matches one provider object in api.json.
type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

// parseModelsDev decodes an api.json document into provider-slug -> api-model
// -> record. api.json is a top-level object keyed by provider id.
func parseModelsDev(data []byte) (map[string]map[string]modelsDevModel, error) {
	var doc map[string]modelsDevProvider
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("modelregistry: parse models.dev: %w", err)
	}
	providers := make(map[string]map[string]modelsDevModel, len(doc))
	for slug, provider := range doc {
		if len(provider.Models) == 0 {
			continue
		}
		providers[slug] = provider.Models
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("modelregistry: models.dev document has no providers")
	}
	return providers, nil
}

// modelsDevSlugs maps a curated entry's provider kind to first-party provider
// ids. Derived entries use an explicit provider profile key instead.
func modelsDevSlugs(kind ProviderKind, providerKey ...string) []string {
	switch kind {
	case ProviderAnthropic:
		return []string{"anthropic"}
	case ProviderOpenAI:
		return []string{"openai"}
	case ProviderGoogle:
		return []string{"google", "google-vertex"}
	case ProviderOpenAICompatible:
		if len(providerKey) > 0 && strings.TrimSpace(providerKey[0]) != "" {
			return []string{strings.ToLower(strings.TrimSpace(providerKey[0]))}
		}
		return nil
	default:
		return nil
	}
}

var modelsDevProviderAliases = map[string]string{
	"chatgpt": "openai",
}

func modelsDevProviderKey(profileName string, providers map[string]map[string]modelsDevModel) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(profileName))
	if name == "" {
		return "", false
	}
	if key, ok := modelsDevProviderAliases[name]; ok {
		name = key
	}
	if _, ok := providers[name]; !ok {
		return "", false
	}
	return name, true
}

func modelsDevEntryProviders(providerKey string) (ProviderKind, []ProviderKind) {
	switch providerKey {
	case "anthropic":
		return ProviderAnthropic, []ProviderKind{ProviderAnthropic}
	case "google", "google-vertex":
		return ProviderGoogle, []ProviderKind{ProviderGoogle}
	case "openai":
		return ProviderOpenAI, []ProviderKind{ProviderOpenAI, ProviderOpenAICompatible}
	default:
		return ProviderOpenAI, []ProviderKind{ProviderOpenAI, ProviderOpenAICompatible}
	}
}

// applyModelsDevOverrides refreshes curated volatile facts and appends derived
// entries from one explicit models.dev provider profile.
func applyModelsDevOverrides(entries []ModelEntry, providers map[string]map[string]modelsDevModel, providerProfile ...string) []ModelEntry {
	entries, _ = applyModelsDevOverridesWithStats(entries, providers, providerProfile...)
	return entries
}

// applyModelsDevOverridesWithStats also counts derived records rejected by
// ModelEntry validation. Base rates and tiers come from the same record, so
// live pricing does not misprice curated tier boundaries. Without provider
// context, no derived entries are added because prices can differ by provider.
func applyModelsDevOverridesWithStats(entries []ModelEntry, providers map[string]map[string]modelsDevModel, providerProfile ...string) ([]ModelEntry, int) {
	if len(providers) == 0 {
		return entries, 0
	}
	for i := range entries {
		entry := &entries[i]
		var record modelsDevModel
		found := false
		for _, slug := range modelsDevSlugs(entry.Provider) {
			if models, ok := providers[slug]; ok {
				if candidate, ok := models[strings.TrimSpace(entry.APIModel)]; ok {
					record = candidate
					found = true
					break
				}
			}
		}
		if !found {
			continue
		}
		if record.Limit.Context > 0 {
			entry.ContextLimits.ContextWindow = record.Limit.Context
		}
		if record.Limit.Output > 0 {
			entry.ContextLimits.MaxOutputTokens = record.Limit.Output
		}
		// Base rates and tiers must come from the same record. A live base rate
		// beside a curated boundary misprices every step.
		if record.Cost.Input > 0 && record.Cost.Output > 0 {
			entry.Cost.InputPerMillion = record.Cost.Input
			entry.Cost.OutputPerMillion = record.Cost.Output
			if record.Cost.CacheRead > 0 {
				entry.Cost.CachedInputPerMillion = record.Cost.CacheRead
			}
			if record.Cost.CacheWrite > 0 {
				entry.Cost.CacheWritePerMillion = record.Cost.CacheWrite
			}
			entry.Cost.Tiers = modelsDevCostTiers(record)
			entry.Cost.Source = "models.dev/api.json (cached)"
		}
	}

	if len(providerProfile) == 0 {
		return entries, 0
	}
	providerKey, ok := modelsDevProviderKey(providerProfile[0], providers)
	if !ok {
		return entries, 0
	}
	curatedModels := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		curatedModels[strings.ToLower(strings.TrimSpace(entry.ID))] = struct{}{}
		curatedModels[strings.ToLower(strings.TrimSpace(entry.APIModel))] = struct{}{}
		for _, alias := range entry.Aliases {
			curatedModels[strings.ToLower(strings.TrimSpace(alias))] = struct{}{}
		}
	}
	modelIDs := make([]string, 0, len(providers[providerKey]))
	for apiModel := range providers[providerKey] {
		modelIDs = append(modelIDs, apiModel)
	}
	sort.Strings(modelIDs)
	skipped := 0
	derivedModels := make(map[string]struct{}, len(modelIDs))
	for _, apiModel := range modelIDs {
		record := providers[providerKey][apiModel]
		modelID := strings.TrimSpace(apiModel)
		if modelID == "" {
			skipped++
			continue
		}
		if _, exists := curatedModels[strings.ToLower(modelID)]; exists {
			continue
		}
		primaryProvider, apiProviders := modelsDevEntryProviders(providerKey)
		candidate := ModelEntry{
			ID:            modelID,
			DisplayName:   modelID,
			APIModel:      modelID,
			Provider:      primaryProvider,
			APIProviders:  apiProviders,
			ContextLimits: ContextLimits{ContextWindow: record.Limit.Context, MaxOutputTokens: record.Limit.Output},
			Capabilities:  withBaseCapabilities(),
			Cost: ModelCost{
				Currency:              "USD",
				Unit:                  "per_1m_tokens",
				InputPerMillion:       record.Cost.Input,
				OutputPerMillion:      record.Cost.Output,
				CachedInputPerMillion: record.Cost.CacheRead,
				CacheWritePerMillion:  record.Cost.CacheWrite,
				Tiers:                 modelsDevCostTiers(record),
				Source:                "models.dev/api.json (cached)",
				SourceLastVerified:    sourceLastVerified,
			},
			Status:            ModelStatusActive,
			Aliases:           []string{modelID},
			Description:       fmt.Sprintf("Model from the %s models.dev provider.", providerKey),
			ModelsDevProvider: providerKey,
		}
		if err := candidate.Validate(); err != nil {
			skipped++
			continue
		}
		normalizedID := strings.ToLower(strings.TrimSpace(candidate.ID))
		if _, exists := derivedModels[normalizedID]; exists {
			skipped++
			continue
		}
		derivedModels[normalizedID] = struct{}{}
		entries = append(entries, candidate)
	}
	return entries, skipped
}

// modelsDevCachePath returns the on-disk cache location. ZERO_MODELS_CACHE_PATH
// overrides it (used by tests and unusual setups).
func modelsDevCachePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ZERO_MODELS_CACHE_PATH")); override != "" {
		return override, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "splice", "modelsdev.json"), nil
}

var (
	modelsDevOnce    sync.Once
	modelsDevCached  map[string]map[string]modelsDevModel
	modelsDevEnabled atomic.Bool
)

// EnableModelsDevOverlay opts the process into applying the cached models.dev
// snapshot on top of the curated catalog. The CLI entrypoint calls it; library
// consumers and tests that never do get the curated catalog byte-identical to
// before, so hermetic tests can't be perturbed by a cache file on the machine.
// ZERO_DISABLE_MODELS_FETCH disables both the overlay and the fetch.
func EnableModelsDevOverlay() {
	modelsDevEnabled.Store(true)
}

// cachedModelsDevProviders loads the cached snapshot once per process. Not
// enabled, missing, stale (> modelsDevMaxAge), or malformed all yield nil and
// the curated catalog is used untouched. Read once deliberately:
// DefaultRegistry is called on hot paths (pickers, cost views) and must not
// re-stat the file every time; a background refresh benefits the NEXT process.
func cachedModelsDevProviders() map[string]map[string]modelsDevModel {
	if !modelsDevEnabled.Load() || strings.TrimSpace(os.Getenv("ZERO_DISABLE_MODELS_FETCH")) != "" {
		return nil
	}
	modelsDevOnce.Do(func() {
		path, err := modelsDevCachePath()
		if err != nil {
			return
		}
		info, err := os.Stat(path)
		if err != nil || time.Since(info.ModTime()) > modelsDevMaxAge {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		providers, err := parseModelsDev(data)
		if err != nil {
			return
		}
		modelsDevCached = providers
	})
	return modelsDevCached
}

// resetModelsDevCacheForTest clears the process-level cache memoization and
// disables the overlay.
func resetModelsDevCacheForTest() {
	modelsDevOnce = sync.Once{}
	modelsDevCached = nil
	modelsDevEnabled.Store(false)
}

// RefreshModelsDevCache fetches models.dev/api.json into the on-disk cache
// when the cache is missing or older than modelsDevRefreshAfter. It is safe to
// call fire-and-forget from startup (use a goroutine); it never affects the
// current process's registry (see cachedModelsDevProviders). Disabled entirely
// by ZERO_DISABLE_MODELS_FETCH. The URL can be overridden with ZERO_MODELS_URL.
func RefreshModelsDevCache(ctx context.Context) error {
	if strings.TrimSpace(os.Getenv("ZERO_DISABLE_MODELS_FETCH")) != "" {
		return nil
	}
	path, err := modelsDevCachePath()
	if err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < modelsDevRefreshAfter {
		return nil
	}

	url := strings.TrimSpace(os.Getenv("ZERO_MODELS_URL"))
	if url == "" {
		url = modelsDevDefaultURL
	}
	fetchCtx, cancel := context.WithTimeout(ctx, modelsDevFetchWindow)
	defer cancel()
	request, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "splice-models-refresh/0.1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("modelregistry: models.dev fetch: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, modelsDevFetchLimit))
	if err != nil {
		return err
	}
	// Validate before persisting: a bad body must never clobber a good cache.
	if _, err := parseModelsDev(data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "modelsdev-*.json")
	if err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(temp.Name())
		return err
	}
	return os.Rename(temp.Name(), path)
}
