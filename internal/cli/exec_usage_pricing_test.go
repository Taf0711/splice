package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/usage"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// usageLoopProvider answers a loop request with text and one usage event, so the
// run persists a usage record for the loop request.
type usageLoopProvider struct{}

func (usageLoopProvider) StreamCompletion(_ context.Context, _ zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	stream := make(chan zeroruntime.StreamEvent, 3)
	stream <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: "done"}
	stream <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}}
	stream <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(stream)
	return stream, nil
}

// runLoopUsage runs `exec --loop` against profile and returns the persisted
// usage events of that session.
func runLoopUsage(t *testing.T, profile config.ProviderProfile, sessionID string) []sessions.Event {
	t.Helper()
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	var stdout, stderr strings.Builder
	exitCode := runWithDeps([]string{"exec", "--cwd", root, "--loop", "--init-session-id", sessionID, "--output-format", "stream-json", "finish the task"}, &stdout, &stderr, appDeps{
		getwd:          func() (string, error) { return root, nil },
		userConfigPath: func() (string, error) { return root + "/config.json", nil },
		resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
			cfg := execStageResolvedConfig(profile.Model, 2)
			cfg.Provider = profile
			cfg.Providers = []config.ProviderProfile{profile}
			return cfg, nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return usageLoopProvider{}, nil
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("loop exit = %d stderr=%s stdout=%s", exitCode, stderr.String(), stdout.String())
	}
	store := sessions.NewStore(sessions.StoreOptions{RootDir: filepath.Join(dataHome, "splice", "sessions")})
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var usageEvents []sessions.Event
	for _, event := range events {
		if event.Type == sessions.EventUsage {
			usageEvents = append(usageEvents, event)
		}
	}
	if len(usageEvents) == 0 {
		t.Fatal("the loop run persisted no usage event")
	}
	return usageEvents
}

// The `splice usage` read path prices a stored event from the catalog of the
// provider that served it. A ChatGPT loop run is unpriced and totals $0, and an
// API-key run on the same model is priced.
func TestSpliceUsagePricesLoopRunsByServingProvider(t *testing.T) {
	chatgpt := config.ProviderProfile{Name: "chatgpt-61", CatalogID: "chatgpt", Model: "gpt-5.6-sol", ProviderKind: config.ProviderKindOpenAI}
	apiKey := config.ProviderProfile{Name: "openai-key", CatalogID: "openai", Model: "gpt-5.6-sol", ProviderKind: config.ProviderKindOpenAI}
	providers := []config.ProviderProfile{chatgpt, apiKey}

	read := func(events []sessions.Event) (usage.Report, usageCoverage) {
		t.Helper()
		catalogs := usage.NewProfileCostCatalogs(providers)
		price := func(provider string, model string, u zeroruntime.Usage) agent.UsageCostEstimate {
			if strings.TrimSpace(provider) == "" {
				provider = providers[0].Name
			}
			return catalogs.Estimate(provider, model, u, true)
		}
		report, err := usage.BuildReportPriced(events, nil, price, 0)
		if err != nil {
			t.Fatal(err)
		}
		coverage, err := classifyUsageCoverage(events, nil, price, providers[0].Name)
		if err != nil {
			t.Fatal(err)
		}
		return report, coverage
	}

	chatEvents := runLoopUsage(t, chatgpt, "chatgpt_loop_run")
	var stored map[string]any
	if err := json.Unmarshal(chatEvents[0].Payload, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["costStatus"] != agent.CostStatusUnpriced || !strings.Contains(stored["unpricedReason"].(string), "ChatGPT subscription") {
		t.Fatalf("stored chatgpt event = %#v, want unpriced with the subscription reason", stored)
	}
	chatReport, chatCoverage := read(chatEvents)
	if chatReport.Total.TotalCost != 0 || chatCoverage.UnpricedCount == 0 || chatCoverage.ReconstructedCount != 0 {
		t.Fatalf("chatgpt run: total=%v coverage=%+v, want $0 and unpriced", chatReport.Total.TotalCost, chatCoverage)
	}

	apiEvents := runLoopUsage(t, apiKey, "openai_key_loop_run")
	apiReport, apiCoverage := read(apiEvents)
	if apiReport.Total.TotalCost <= 0 || apiCoverage.PersistedCount+apiCoverage.ReconstructedCount == 0 || apiCoverage.UnpricedCount != 0 {
		t.Fatalf("openai API-key run: total=%v coverage=%+v, want priced", apiReport.Total.TotalCost, apiCoverage)
	}
}

// A stored event that carries no cost (written before the run priced it, or
// from a spec run) is priced on read from its own provider. A ChatGPT event is
// unpriced and totals $0. An API-key event on the same model is priced.
func TestSpliceUsageReadPricesCostlessEventsByProvider(t *testing.T) {
	providers := []config.ProviderProfile{
		{Name: "chatgpt-61", CatalogID: "chatgpt", ProviderKind: config.ProviderKindOpenAI},
		{Name: "openai-key", CatalogID: "openai", ProviderKind: config.ProviderKindOpenAI},
	}
	catalogs := usage.NewProfileCostCatalogs(providers)
	price := func(provider string, model string, u zeroruntime.Usage) agent.UsageCostEstimate {
		return catalogs.Estimate(provider, model, u, true)
	}
	costless := func(provider string) []sessions.Event {
		raw, err := json.Marshal(map[string]any{
			"provider": provider, "model": "gpt-5.6-sol", "stage": "agent_loop", "usageReported": true,
			"promptTokens": 1_000_000, "completionTokens": 1_000_000, "totalTokens": 2_000_000, "usageSequence": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return []sessions.Event{{SessionID: "s", Sequence: 1, Type: sessions.EventUsage, CreatedAt: "2026-10-10T00:00:00Z", Payload: raw}}
	}

	chat := costless("chatgpt-61")
	report, err := usage.BuildReportPriced(chat, nil, price, 0)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := classifyUsageCoverage(chat, nil, price, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.TotalCost != 0 || coverage.UnpricedCount != 1 || coverage.ReconstructedCount != 0 {
		t.Fatalf("costless chatgpt event: total=%v coverage=%+v, want $0 and unpriced", report.Total.TotalCost, coverage)
	}

	api := costless("openai-key")
	apiReport, err := usage.BuildReportPriced(api, nil, price, 0)
	if err != nil {
		t.Fatal(err)
	}
	if apiReport.Total.TotalCost <= 0 {
		t.Fatalf("costless API-key event total = %v, want priced", apiReport.Total.TotalCost)
	}
}

// A stored event that names no provider is priced from the active profile. The
// number stands, but the coverage is partial, and the reason names the profile
// that priced it.
func TestSpliceUsageCountsProviderlessEventAsAssumed(t *testing.T) {
	providers := []config.ProviderProfile{{Name: "openai-key", CatalogID: "openai", ProviderKind: config.ProviderKindOpenAI}}
	catalogs := usage.NewProfileCostCatalogs(providers)
	price := func(provider string, model string, u zeroruntime.Usage) agent.UsageCostEstimate {
		if strings.TrimSpace(provider) == "" {
			provider = providers[0].Name
		}
		return catalogs.Estimate(provider, model, u, true)
	}
	raw, err := json.Marshal(map[string]any{
		"model": "gpt-5.6-sol", "stage": "agent_loop", "usageReported": true,
		"promptTokens": 1_000_000, "completionTokens": 1_000_000, "totalTokens": 2_000_000, "usageSequence": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := []sessions.Event{{SessionID: "s", Sequence: 1, Type: sessions.EventUsage, CreatedAt: "2026-10-10T00:00:00Z", Payload: raw}}
	report, err := usage.BuildReportPriced(events, nil, price, 0)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := classifyUsageCoverage(events, nil, price, providers[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.TotalCost <= 0 {
		t.Fatalf("provider-less event total = %v, want the active profile's number", report.Total.TotalCost)
	}
	if coverage.CostCoverage != usage.CostCoveragePartial || coverage.AssumedCount != 1 {
		t.Fatalf("provider-less coverage = %+v, want partial with one assumed request", coverage)
	}
	if want := "served provider not recorded; priced as openai-key"; coverage.AssumedReason != want {
		t.Fatalf("assumed reason = %q, want %q", coverage.AssumedReason, want)
	}
}
