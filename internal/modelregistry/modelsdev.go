package modelregistry

import (
	"bytes"
	"compress/gzip"
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

	_ "embed"
)

// The embedded models.dev snapshot supplies volatile facts for the curated
// catalog. A newer disk snapshot can replace that baseline when the process
// opts into the disk overlay. Identity fields remain curated. Derived entries
// require the overlay, an explicit provider profile, and a selected snapshot.
// Registry creation never touches the network. Fetching happens only in the
// background refresh.

const (
	modelsDevDefaultURL = "https://models.dev/api.json"
	// modelsDevRefreshAfter is how old the cache may get before a background
	// refresh re-fetches it.
	modelsDevRefreshAfter = 24 * time.Hour
	modelsDevFetchLimit   = 32 << 20 // 32MiB guard on the response body
	modelsDevFetchWindow  = 15 * time.Second
)

const (
	modelsDevEmbeddedSource = "models.dev/api.json (embedded snapshot)"
	modelsDevCachedSource   = "models.dev/api.json (cached)"
)

// The embedded snapshot supplies the baseline prices in every process.
// Keep the date beside the compressed asset so the source date stays explicit.
//
//go:embed modelsdev_snapshot.json.gz
var modelsDevEmbeddedGZIP []byte

//go:embed modelsdev_snapshot_date.txt
var modelsDevEmbeddedDate []byte

// modelsDevModel is the subset of a models.dev model record the overlay uses.
type modelsDevModel struct {
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		// Input and Output are pointers so an absent cost differs from an
		// explicit zero. An explicit zero on both marks a free listing.
		Input      *float64            `json:"input"`
		Output     *float64            `json:"output"`
		CacheRead  float64             `json:"cache_read"`
		CacheWrite float64             `json:"cache_write"`
		Tiers      []modelsDevCostTier `json:"tiers"`
	} `json:"cost"`
	ReasoningOptions []struct {
		Type   string   `json:"type"`
		Values []string `json:"values"`
	} `json:"reasoning_options"`
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

// modelsDevRate reads an optional models.dev rate. ok is false when the record
// carries no cost for it, which is not the same as a listed zero.
func modelsDevRate(rate *float64) (float64, bool) {
	if rate == nil {
		return 0, false
	}
	return *rate, true
}

func modelsDevReasoningEfforts(record modelsDevModel) []ReasoningEffort {
	budgetOnly := false
	for _, option := range record.ReasoningOptions {
		if option.Type == "budget_tokens" {
			budgetOnly = true
		}
		if option.Type != "effort" {
			continue
		}
		efforts := make([]ReasoningEffort, 0, len(option.Values))
		for _, value := range option.Values {
			effort := ReasoningEffort(value)
			if ValidReasoningEffort(effort) {
				efforts = append(efforts, effort)
			}
		}
		if len(efforts) == 0 {
			return nil
		}
		return efforts
	}
	// A thinking-budget model (Claude Haiku 4.5, Sonnet 4.5) has no effort
	// enum on the wire, but the provider maps low/medium/high to budgets,
	// so the user still gets a real depth control instead of none.
	if budgetOnly {
		return []ReasoningEffort{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}
	}
	return nil
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
	inputRate, _ := modelsDevRate(record.Cost.Input)
	outputRate, _ := modelsDevRate(record.Cost.Output)
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
		// A few curated OpenAI-compatible entries use slash-qualified
		// OpenRouter ids. First-party records remain preferred.
		return []string{"openai", "openrouter"}
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
	// In-memory callers do not have a cache file. Use the call time for test and
	// helper compatibility. The registry path passes the cache mtime directly.
	return applyModelsDevOverridesWithSource(entries, providers, modelsDevCachedSource, time.Now().UTC(), providerProfile...)
}

func applyModelsDevOverridesWithSource(entries []ModelEntry, providers map[string]map[string]modelsDevModel, source string, sourceModTime time.Time, providerProfile ...string) ([]ModelEntry, int) {
	if len(providers) == 0 {
		return entries, 0
	}
	verifiedDate := modelsDevCacheDate(sourceModTime)
	if source == modelsDevEmbeddedSource {
		verifiedDate = strings.TrimSpace(string(modelsDevEmbeddedDate))
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
			if entry.DefaultReasoningEffort != "" && len(entry.ReasoningEfforts) == 0 {
				// A newer disk snapshot can omit a curated model. Keep its
				// name-based fallback so the curated default remains valid.
				entry.ReasoningEfforts = reasoningEffortsForModelName(entry.APIModel)
			}
			continue
		}
		if source != modelsDevEmbeddedSource {
			if record.Limit.Context > 0 {
				entry.ContextLimits.ContextWindow = record.Limit.Context
			}
			if record.Limit.Output > 0 {
				entry.ContextLimits.MaxOutputTokens = record.Limit.Output
			}
		}
		// Base rates and tiers must come from the same record. A live base rate
		// beside a curated boundary misprices every step.
		inputRate, hasInput := modelsDevRate(record.Cost.Input)
		outputRate, hasOutput := modelsDevRate(record.Cost.Output)
		if hasInput && hasOutput && inputRate > 0 && outputRate > 0 {
			entry.Cost.InputPerMillion = inputRate
			entry.Cost.OutputPerMillion = outputRate
			if record.Cost.CacheRead > 0 {
				entry.Cost.CachedInputPerMillion = record.Cost.CacheRead
			}
			if record.Cost.CacheWrite > 0 {
				entry.Cost.CacheWritePerMillion = record.Cost.CacheWrite
			}
			entry.Cost.Tiers = modelsDevCostTiers(record)
			entry.Cost.Source = source
			entry.Cost.SourceLastVerified = verifiedDate
		}
		if efforts := modelsDevReasoningEfforts(record); efforts != nil {
			entry.ReasoningEfforts = efforts
			if entry.DefaultReasoningEffort != "" {
				entry.DefaultReasoningEffort = clampEffort(efforts, entry.DefaultReasoningEffort)
			}
		} else if entry.DefaultReasoningEffort != "" && len(entry.ReasoningEfforts) == 0 {
			// A newer disk snapshot can omit a curated model. Keep its
			// name-based fallback so the curated default remains valid.
			entry.ReasoningEfforts = reasoningEffortsForModelName(entry.APIModel)
		}
	}

	if len(providerProfile) == 0 {
		return entries, 0
	}
	// Provider-scoped derived entries require explicit overlay opt-in. The
	// selected source can be either the embedded snapshot or a newer disk cache.
	if !modelsDevEnabled.Load() {
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
			// A gateway that lists a curated model with its own valid price is
			// priced by that price, for this profile only.
			if idx := curatedEntryIndex(entries, modelID); idx >= 0 {
				adoptGatewayPrice(&entries[idx], record, providerKey, source, verifiedDate)
			}
			continue
		}
		inputRate, hasInput := modelsDevRate(record.Cost.Input)
		outputRate, hasOutput := modelsDevRate(record.Cost.Output)
		// An absent cost is not a price. Both rates explicitly zero means the
		// serving provider lists the model as free, which is a real price of
		// zero for this provider only. Any other zero or negative rate is skipped.
		free := hasInput && hasOutput && inputRate == 0 && outputRate == 0
		if !free && (!hasInput || !hasOutput || inputRate <= 0 || outputRate <= 0) {
			skipped++
			continue
		}
		if free && (record.Cost.CacheRead != 0 || record.Cost.CacheWrite != 0) {
			skipped++
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
				InputPerMillion:       inputRate,
				OutputPerMillion:      outputRate,
				CachedInputPerMillion: record.Cost.CacheRead,
				CacheWritePerMillion:  record.Cost.CacheWrite,
				Free:                  free,
				Tiers:                 modelsDevCostTiers(record),
				Source:                source,
				SourceLastVerified:    verifiedDate,
			},
			Status:            ModelStatusActive,
			Aliases:           []string{modelID},
			Description:       fmt.Sprintf("Model from the %s models.dev provider.", providerKey),
			ModelsDevProvider: providerKey,
		}
		candidate.ReasoningEfforts = modelsDevReasoningEfforts(record)
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

// curatedEntryIndex finds the curated entry (no derived provider) that a model
// id names, by id, api model, or alias. It returns -1 when none matches.
func curatedEntryIndex(entries []ModelEntry, modelID string) int {
	key := strings.ToLower(strings.TrimSpace(modelID))
	for i, entry := range entries {
		if entry.ModelsDevProvider != "" {
			continue
		}
		if strings.ToLower(strings.TrimSpace(entry.ID)) == key || strings.ToLower(strings.TrimSpace(entry.APIModel)) == key {
			return i
		}
		for _, alias := range entry.Aliases {
			if strings.ToLower(strings.TrimSpace(alias)) == key {
				return i
			}
		}
	}
	return -1
}

// adoptGatewayPrice prices a curated entry from a gateway's record when the
// gateway is not one of the entry's own price catalogs and the record carries a
// valid price. The entry is then bound to that gateway, so its price serves only
// that profile's registry. The entry's own owners keep their curated price.
func adoptGatewayPrice(entry *ModelEntry, record modelsDevModel, providerKey string, source string, verifiedDate string) {
	for _, owner := range entry.PriceCatalogs() {
		if owner == providerKey {
			return
		}
	}
	inputRate, hasInput := modelsDevRate(record.Cost.Input)
	outputRate, hasOutput := modelsDevRate(record.Cost.Output)
	if !hasInput || !hasOutput || inputRate <= 0 || outputRate <= 0 {
		return
	}
	entry.Cost.InputPerMillion = inputRate
	entry.Cost.OutputPerMillion = outputRate
	entry.Cost.CachedInputPerMillion = record.Cost.CacheRead
	entry.Cost.CacheWritePerMillion = record.Cost.CacheWrite
	entry.Cost.Tiers = modelsDevCostTiers(record)
	entry.Cost.Source = source
	entry.Cost.SourceLastVerified = verifiedDate
	entry.ModelsDevProvider = providerKey
}

func modelsDevCacheDate(modTime time.Time) string {
	if modTime.IsZero() {
		return ""
	}
	return modTime.UTC().Format("2006-01-02")
}

// modelsDevCachePath returns the on-disk cache location. SPLICE_MODELS_CACHE_PATH
// overrides it (used by tests and unusual setups).
func modelsDevCachePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("SPLICE_MODELS_CACHE_PATH")); override != "" {
		return override, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "splice", "modelsdev.json"), nil
}

var (
	modelsDevEmbeddedOnce       sync.Once
	modelsDevEmbeddedProviders  map[string]map[string]modelsDevModel
	modelsDevEmbeddedCapture    time.Time
	modelsDevEmbeddedParseError error

	// modelsDevMu guards the selected snapshot. It is chosen lazily and
	// re-chosen after a refresh writes a newer cache (ReloadModelsDevCache),
	// so a running TUI picks up new models without a restart. Registries
	// already built keep their own copy of the entries.
	modelsDevMu       sync.Mutex
	modelsDevLoaded   bool
	modelsDevSelected modelsDevSource
	modelsDevEnabled  atomic.Bool
)

type modelsDevSource struct {
	providers map[string]map[string]modelsDevModel
	modTime   time.Time
	source    string
}

// EnableModelsDevOverlay opts the process into provider-scoped derived entries
// and applying a newer disk cache. The embedded snapshot remains the baseline
// with or without this setting.
// SPLICE_DISABLE_MODELS_FETCH disables the background network fetch only.
func EnableModelsDevOverlay() {
	modelsDevEnabled.Store(true)
}

func embeddedModelsDevSnapshot() modelsDevSource {
	modelsDevEmbeddedOnce.Do(func() {
		date := strings.TrimSpace(string(modelsDevEmbeddedDate))
		captureDate, err := time.Parse("2006-01-02", date)
		if err != nil {
			modelsDevEmbeddedParseError = fmt.Errorf("modelregistry: invalid embedded models.dev capture date %q: %w", date, err)
			return
		}
		reader, err := gzip.NewReader(bytes.NewReader(modelsDevEmbeddedGZIP))
		if err != nil {
			modelsDevEmbeddedParseError = fmt.Errorf("modelregistry: open embedded models.dev snapshot: %w", err)
			return
		}
		data, err := io.ReadAll(io.LimitReader(reader, modelsDevFetchLimit))
		closeErr := reader.Close()
		if err != nil {
			modelsDevEmbeddedParseError = fmt.Errorf("modelregistry: read embedded models.dev snapshot: %w", err)
			return
		}
		if closeErr != nil {
			modelsDevEmbeddedParseError = fmt.Errorf("modelregistry: close embedded models.dev snapshot: %w", closeErr)
			return
		}
		providers, err := parseModelsDev(data)
		if err != nil {
			modelsDevEmbeddedParseError = err
			return
		}
		modelsDevEmbeddedProviders = providers
		modelsDevEmbeddedCapture = captureDate.UTC()
	})
	return modelsDevSource{
		providers: modelsDevEmbeddedProviders,
		modTime:   modelsDevEmbeddedCapture,
		source:    modelsDevEmbeddedSource,
	}
}

// cachedModelsDevProviders returns the selected snapshot. DefaultRegistry is
// called on hot paths, so the cache is stat-ed only when (re)loading.
func cachedModelsDevProviders() map[string]map[string]modelsDevModel {
	return cachedModelsDevSnapshotInfo().providers
}

func cachedModelsDevSnapshotInfo() modelsDevSource {
	modelsDevMu.Lock()
	defer modelsDevMu.Unlock()
	if !modelsDevLoaded {
		modelsDevLoaded = true
		modelsDevSelected = selectModelsDevSource()
	}
	return modelsDevSelected
}

// ReloadModelsDevCache makes the next registry build re-select between the
// embedded snapshot and the on-disk cache. A refresh calls it after writing.
func ReloadModelsDevCache() {
	modelsDevMu.Lock()
	modelsDevLoaded = false
	modelsDevMu.Unlock()
}

func selectModelsDevSource() modelsDevSource {
	// Local on purpose: the selection body below assigns this name, and the
	// caller publishes the result under modelsDevMu.
	var modelsDevSelected modelsDevSource
	func() {
		selected := embeddedModelsDevSnapshot()
		if modelsDevEmbeddedParseError != nil {
			modelsDevSelected = selected
			return
		}
		if !modelsDevEnabled.Load() {
			modelsDevSelected = selected
			return
		}
		path, err := modelsDevCachePath()
		if err != nil {
			modelsDevSelected = selected
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.ModTime().After(selected.modTime) {
			modelsDevSelected = selected
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			modelsDevSelected = selected
			return
		}
		providers, err := parseModelsDev(data)
		if err != nil {
			modelsDevSelected = selected
			return
		}
		modelsDevSelected = modelsDevSource{
			providers: providers,
			modTime:   info.ModTime(),
			source:    modelsDevCachedSource,
		}
	}()
	return modelsDevSelected
}

// resetModelsDevCacheForTest clears the process-level cache memoization and
// disables the overlay.
func resetModelsDevCacheForTest() {
	modelsDevMu.Lock()
	modelsDevLoaded = false
	modelsDevSelected = modelsDevSource{}
	modelsDevMu.Unlock()
	modelsDevEnabled.Store(false)
}

// RefreshModelsDevCache fetches models.dev/api.json into the on-disk cache
// when the cache is missing or older than modelsDevRefreshAfter. It reports
// whether a new cache was written; the next DefaultRegistry call then uses
// it (rebuild registries to pick it up). Disabled entirely by
// SPLICE_DISABLE_MODELS_FETCH. The URL can be overridden with SPLICE_MODELS_URL.
func RefreshModelsDevCache(ctx context.Context) (bool, error) {
	return refreshModelsDevCache(ctx, false)
}

// ForceRefreshModelsDevCache fetches regardless of the cache age: the
// user asked for fresh models now.
func ForceRefreshModelsDevCache(ctx context.Context) (bool, error) {
	return refreshModelsDevCache(ctx, true)
}

func refreshModelsDevCache(ctx context.Context, force bool) (bool, error) {
	if strings.TrimSpace(os.Getenv("SPLICE_DISABLE_MODELS_FETCH")) != "" {
		return false, nil
	}
	path, err := modelsDevCachePath()
	if err != nil {
		return false, err
	}
	if info, err := os.Stat(path); !force && err == nil && time.Since(info.ModTime()) < modelsDevRefreshAfter {
		return false, nil
	}

	url := strings.TrimSpace(os.Getenv("SPLICE_MODELS_URL"))
	if url == "" {
		url = modelsDevDefaultURL
	}
	fetchCtx, cancel := context.WithTimeout(ctx, modelsDevFetchWindow)
	defer cancel()
	request, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("User-Agent", "splice-models-refresh/0.1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("modelregistry: models.dev fetch: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, modelsDevFetchLimit))
	if err != nil {
		return false, err
	}
	// Validate before persisting: a bad body must never clobber a good cache.
	if _, err := parseModelsDev(data); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "modelsdev-*.json")
	if err != nil {
		return false, err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return false, err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(temp.Name())
		return false, err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return false, err
	}
	ReloadModelsDevCache()
	return true, nil
}
