package splice

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/usage"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// catalogPriced is a fake catalog estimate for a model priced at 1.5 USD per
// request. catalogUnpriced is a fake for a model the serving provider does
// not list.
func catalogPriced(model string, usage agent.Usage, reported bool) agent.UsageCostEstimate {
	cost := 1.5
	return agent.UsageCostEstimate{CostUSD: &cost, Status: agent.CostStatusPriced, Provenance: agent.CostProvenanceRuntimeEstimate, PricingSource: "test", PricingAsOf: "2026-10-10"}
}

func catalogUnpriced(model string, usage agent.Usage, reported bool) agent.UsageCostEstimate {
	return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "model not in registry"}
}

func attributedWith(reported *float64, tokens int) agent.AttributedUsage {
	return agent.AttributedUsage{
		Usage:           zeroruntime.Usage{InputTokens: tokens, OutputTokens: tokens},
		UsageReported:   true,
		Model:           "deepseek/deepseek-v4.1-flash",
		ReportedCostUSD: reported,
	}
}

func costValue(t *testing.T, estimate agent.UsageCostEstimate) float64 {
	t.Helper()
	if estimate.CostUSD == nil {
		t.Fatalf("status %q has no cost; reason %q", estimate.Status, estimate.UnpricedReason)
	}
	return *estimate.CostUSD
}

func TestResolveRequestCostTrustsPositiveReport(t *testing.T) {
	reported := 0.0026853
	got := ResolveRequestCost(catalogPriced, attributedWith(&reported, 500))
	if got.Status != agent.CostStatusPriced || got.Provenance != agent.CostProvenanceReported || costValue(t, got) != reported {
		t.Fatalf("positive report = %+v, want reported %v", got, reported)
	}
}

func TestResolveRequestCostFallsBackFromZeroReportOnPaidModel(t *testing.T) {
	zero := 0.0
	got := ResolveRequestCost(catalogPriced, attributedWith(&zero, 500))
	if got.Status != agent.CostStatusPriced || got.Provenance != agent.CostProvenanceRuntimeEstimate || costValue(t, got) != 1.5 {
		t.Fatalf("zero report on paid model = %+v, want catalog estimate 1.5", got)
	}
}

func TestResolveRequestCostZeroReportWithoutCatalogPriceIsUnpriced(t *testing.T) {
	zero := 0.0
	got := ResolveRequestCost(catalogUnpriced, attributedWith(&zero, 500))
	if got.Status != agent.CostStatusUnpriced || got.CostUSD != nil {
		t.Fatalf("zero report without catalog price = %+v, want unpriced", got)
	}
	if !strings.Contains(got.UnpricedReason, "zero charge") {
		t.Fatalf("reason %q should say the zero report was not trusted", got.UnpricedReason)
	}
}

func TestResolveRequestCostZeroUsageKeepsZeroReport(t *testing.T) {
	zero := 0.0
	got := ResolveRequestCost(catalogPriced, attributedWith(&zero, 0))
	if got.Status != agent.CostStatusPriced || got.Provenance != agent.CostProvenanceReported || costValue(t, got) != 0 {
		t.Fatalf("zero usage with zero report = %+v, want priced 0 reported", got)
	}
}

func TestResolveRequestCostWithoutReportUsesCatalog(t *testing.T) {
	got := ResolveRequestCost(catalogPriced, attributedWith(nil, 500))
	if got.Provenance != agent.CostProvenanceRuntimeEstimate || costValue(t, got) != 1.5 {
		t.Fatalf("no report = %+v, want catalog estimate", got)
	}
}

// A ":free" model id is free by definition, so its reported zero is the price
// even when no catalog entry exists.
func TestResolveRequestCostTrustsZeroReportOnFreeModelID(t *testing.T) {
	zero := 0.0
	attributed := attributedWith(&zero, 500)
	attributed.Model = "google/gemma-4-31b-it:free"
	got := ResolveRequestCost(catalogUnpriced, attributed)
	if got.Status != agent.CostStatusPriced || got.Provenance != agent.CostProvenanceReported || costValue(t, got) != 0 {
		t.Fatalf("zero report on :free model = %+v, want priced 0 reported", got)
	}
}

// A catalog entry that lists the serving provider's model at zero makes the
// zero report the real price. The estimate is priced 0 only for such a listing.
func TestResolveRequestCostTrustsZeroReportWhenCatalogListsZero(t *testing.T) {
	zero := 0.0
	freeCatalog := func(model string, usage agent.Usage, reported bool) agent.UsageCostEstimate {
		cost := 0.0
		return agent.UsageCostEstimate{CostUSD: &cost, Status: agent.CostStatusPriced, Provenance: agent.CostProvenanceRuntimeEstimate, PricingSource: "test", PricingAsOf: "2026-10-10"}
	}
	got := ResolveRequestCost(freeCatalog, attributedWith(&zero, 500))
	if got.Status != agent.CostStatusPriced || got.Provenance != agent.CostProvenanceReported || costValue(t, got) != 0 {
		t.Fatalf("zero report on catalog-free model = %+v, want priced 0 reported", got)
	}
}

// multiChunkUsageProvider streams two usage chunks, a real charge then a zero
// (OpenRouter sends this shape), and then fails the stream so the stage ends
// with its usage on the ledger.
type multiChunkUsageProvider struct{ charge float64 }

func (p *multiChunkUsageProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	zero := 0.0
	events := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 900, OutputTokens: 40}, ReportedCostUSD: ptrForProof(p.charge)},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 900, OutputTokens: 40}, ReportedCostUSD: &zero},
		{Type: zeroruntime.StreamEventError, Error: "provider request error: upstream reset mid-stream"},
	}
	ch := make(chan zeroruntime.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// A multi-usage-chunk stream keeps its real charge end to end: the stream
// collector, the stage attribution, and the pipeline ledger all carry it.
func TestPipelineLedgerKeepsChargeFromMultiChunkStream(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier: schemas.TierLight, RequestIntent: "add a helper",
		Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	charge := 0.0013749
	ledger := newRequestLedger()
	var attributed []agent.AttributedUsage
	options := ledger.recordingOptions(PipelineConfigFromAgentOptions(agent.Options{
		EstimateUsageCost: func(string, agent.Usage, bool) agent.UsageCostEstimate {
			return agent.UsageCostEstimate{Status: agent.CostStatusUnpriced, UnpricedReason: "no catalog entry"}
		},
		OnAttributedUsage: func(u agent.AttributedUsage) { attributed = append(attributed, u) },
	}))
	_, err := runStageWithContext(context.Background(), schemas.HarnessStageInput{
		RunID: "multi-chunk", StageName: "code_writer", Sequence: 1, PlanTier: plan.Tier, RequestIntent: plan.RequestIntent,
	}, stages.CodeWriter{}, 1, agent.ModelSelection{Provider: &multiChunkUsageProvider{charge: charge}}, options, t.TempDir(), nil, nil, stages.Capabilities{}, 0, nil, nil, 0)
	if err == nil {
		t.Fatal("the stream error must fail the stage")
	}
	if len(attributed) == 0 || attributed[0].ReportedCostUSD == nil || *attributed[0].ReportedCostUSD != charge {
		t.Fatalf("attributed reported charge = %+v, want %v", attributed, charge)
	}
	if len(ledger.records) == 0 {
		t.Fatal("no ledger record for the failed request")
	}
	rec := ledger.records[0]
	if rec.CostStatus != agent.CostStatusPriced || rec.CostUSD == nil || *rec.CostUSD != charge || rec.CostProvenance != agent.CostProvenanceReported {
		t.Fatalf("ledger record = status %q cost %v provenance %q, want priced %v reported", rec.CostStatus, rec.CostUSD, rec.CostProvenance, charge)
	}
}

// The real shape: a registry built for zai-coding-plan lists glm-5.3 at zero.
// A request served by zai (a paid provider) must not price at $0, with or
// without a provider report. The same zero listing prices the request its own
// provider serves.
func TestFreeListingPricesOnlyItsOwnProvider(t *testing.T) {
	entry := modelregistry.ModelEntry{
		ID: "glm-5.3", DisplayName: "glm-5.3", APIModel: "glm-5.3", Provider: modelregistry.ProviderOpenAI,
		ContextLimits: modelregistry.ContextLimits{ContextWindow: 128000, MaxOutputTokens: 8192},
		Capabilities:  []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat},
		Cost: modelregistry.ModelCost{
			Currency: "USD", Unit: "per_1m_tokens", Free: true,
			Source: "models.dev/api.json", SourceLastVerified: "2026-10-10",
		},
		Status:            modelregistry.ModelStatusActive,
		Aliases:           []string{"glm-5.3"},
		ModelsDevProvider: "zai-coding-plan",
	}
	// Only zai-coding-plan's catalog lists glm-5.3. Any other provider's catalog
	// is empty, so a request it serves has no listing to price from.
	catalogs := usage.NewCostCatalogs(func(profile string) (modelregistry.Registry, error) {
		if profile == "zai-coding-plan" {
			return modelregistry.NewRegistry([]modelregistry.ModelEntry{entry})
		}
		return modelregistry.NewRegistry(nil)
	})
	usageTokens := zeroruntime.Usage{InputTokens: 500, OutputTokens: 500}
	request := func(served string, reported *float64) agent.AttributedUsage {
		return agent.AttributedUsage{Usage: usageTokens, UsageReported: true, Model: "glm-5.3", ProviderName: served, ReportedCostUSD: reported}
	}
	zero := 0.0

	paid := ResolveRequestCostFor(nil, catalogs.Estimate, request("zai", nil))
	if paid.Status == agent.CostStatusPriced {
		t.Fatalf("zai request priced from zai-coding-plan's zero listing: %+v", paid)
	}
	paidZero := ResolveRequestCostFor(nil, catalogs.Estimate, request("zai", &zero))
	if paidZero.Status == agent.CostStatusPriced {
		t.Fatalf("zai reported zero trusted because of zai-coding-plan's listing: %+v", paidZero)
	}

	own := ResolveRequestCostFor(nil, catalogs.Estimate, request("zai-coding-plan", nil))
	if own.Status != agent.CostStatusPriced || costValue(t, own) != 0 {
		t.Fatalf("zai-coding-plan request = %+v, want priced 0 from its own listing", own)
	}
}

// A request served by B is priced at B's price for a model that A also lists at
// another price. A model listed only under A is unpriced for a request served
// by B. Reported charges and the zero-report rules are unchanged.
func TestRequestPricedFromServingProviderCatalog(t *testing.T) {
	flat := func(id string, perMillion float64) modelregistry.ModelEntry {
		return modelregistry.ModelEntry{
			ID: id, DisplayName: id, APIModel: id, Provider: modelregistry.ProviderOpenAI,
			ContextLimits: modelregistry.ContextLimits{ContextWindow: 128000, MaxOutputTokens: 8192},
			Capabilities:  []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat},
			Cost: modelregistry.ModelCost{
				Currency: "USD", Unit: "per_1m_tokens", InputPerMillion: perMillion, OutputPerMillion: perMillion,
				Source: "models.dev/api.json", SourceLastVerified: "2026-10-10",
			},
			Status: modelregistry.ModelStatusActive, Aliases: []string{id},
		}
	}
	catalogs := usage.NewCostCatalogs(func(profile string) (modelregistry.Registry, error) {
		switch profile {
		case "a":
			return modelregistry.NewRegistry([]modelregistry.ModelEntry{flat("shared-model", 1), flat("only-under-a", 1)})
		case "b":
			return modelregistry.NewRegistry([]modelregistry.ModelEntry{flat("shared-model", 4)})
		}
		return modelregistry.NewRegistry(nil)
	})
	catalogs.SetProfileCatalogIDs(map[string]string{"a": "openai", "b": "openai"})
	tokens := zeroruntime.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	onB := ResolveRequestCostFor(nil, catalogs.Estimate, agent.AttributedUsage{Usage: tokens, UsageReported: true, Model: "shared-model", ProviderName: "b"})
	if onB.Status != agent.CostStatusPriced || costValue(t, onB) != 8 {
		t.Fatalf("shared model served by b = %+v, want B's price (total 8)", onB)
	}
	borrowed := ResolveRequestCostFor(nil, catalogs.Estimate, agent.AttributedUsage{Usage: tokens, UsageReported: true, Model: "only-under-a", ProviderName: "b"})
	if borrowed.Status != agent.CostStatusUnpriced || borrowed.CostUSD != nil {
		t.Fatalf("model listed only under a, served by b = %+v, want unpriced", borrowed)
	}
	reported := 0.25
	charge := ResolveRequestCostFor(nil, catalogs.Estimate, agent.AttributedUsage{Usage: tokens, UsageReported: true, Model: "only-under-a", ProviderName: "b", ReportedCostUSD: &reported})
	if charge.Status != agent.CostStatusPriced || costValue(t, charge) != reported || charge.Provenance != agent.CostProvenanceReported {
		t.Fatalf("reported charge = %+v, want the reported charge to win", charge)
	}
}

// The ChatGPT subscription rule holds on the pipeline and decision paths. A
// request served by a ChatGPT sign-in profile is unpriced with the owner's
// reason, even with a reported charge. The same model served through an openai
// API-key profile is priced at $2/$10.
func TestChatGPTSubscriptionUnpricedOnEveryPath(t *testing.T) {
	gpt61 := modelregistry.ModelEntry{
		ID: "gpt-6.1-sol", DisplayName: "gpt-6.1-sol", APIModel: "gpt-6.1-sol", Provider: modelregistry.ProviderOpenAI,
		ContextLimits: modelregistry.ContextLimits{ContextWindow: 128000, MaxOutputTokens: 8192},
		Capabilities:  []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat},
		Cost: modelregistry.ModelCost{
			Currency: "USD", Unit: "per_1m_tokens", InputPerMillion: 2, OutputPerMillion: 10,
			Source: "models.dev/api.json", SourceLastVerified: "2026-10-10",
		},
		Status: modelregistry.ModelStatusActive, Aliases: []string{"gpt-6.1-sol"},
	}
	catalogs := usage.NewCostCatalogs(func(string) (modelregistry.Registry, error) {
		return modelregistry.NewRegistry([]modelregistry.ModelEntry{gpt61})
	})
	catalogs.SetSubscriptionProfiles(map[string]bool{"chatgpt-61": true})
	catalogs.SetProfileCatalogIDs(map[string]string{"openai-key": "openai"})
	tokens := zeroruntime.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	reason := "ChatGPT subscription: not billed per request"
	reported := 0.5

	stage := ResolveRequestCostFor(nil, catalogs.Estimate, agent.AttributedUsage{Usage: tokens, UsageReported: true, Model: "gpt-6.1-sol", ProviderName: "chatgpt-61"})
	if stage.Status != agent.CostStatusUnpriced || stage.CostUSD != nil || stage.UnpricedReason != reason {
		t.Fatalf("chatgpt stage = %+v, want unpriced with the subscription reason", stage)
	}
	stageReported := ResolveRequestCostFor(nil, catalogs.Estimate, agent.AttributedUsage{Usage: tokens, UsageReported: true, Model: "gpt-6.1-sol", ProviderName: "chatgpt-61", ReportedCostUSD: &reported})
	if stageReported.Status != agent.CostStatusUnpriced || stageReported.CostUSD != nil {
		t.Fatalf("chatgpt stage with a report = %+v, want still unpriced", stageReported)
	}

	classifier := decisionUsageRecordFor("traj_action_v2", 1, zeroruntime.DecisionResult{ResolvedModel: "gpt-6.1-sol", InputTokens: 1_000_000, OutputTokens: 1_000_000}, "chatgpt-61", catalogs.Estimate)
	if classifier.CostStatus != agent.CostStatusUnpriced || classifier.CostUSD != nil || classifier.UnpricedReason != reason {
		t.Fatalf("chatgpt classifier = %+v, want unpriced with the subscription reason", classifier)
	}
	classifierReported := decisionUsageRecordFor("traj_action_v2", 1, zeroruntime.DecisionResult{ResolvedModel: "gpt-6.1-sol", InputTokens: 1_000_000, OutputTokens: 1_000_000, ReportedCostUSD: &reported}, "chatgpt-61", catalogs.Estimate)
	if classifierReported.CostStatus != agent.CostStatusUnpriced || classifierReported.CostUSD != nil {
		t.Fatalf("chatgpt classifier with a report = %+v, want still unpriced", classifierReported)
	}

	api := ResolveRequestCostFor(nil, catalogs.Estimate, agent.AttributedUsage{Usage: tokens, UsageReported: true, Model: "gpt-6.1-sol", ProviderName: "openai-key"})
	if api.Status != agent.CostStatusPriced || costValue(t, api) != 12 {
		t.Fatalf("openai API-key stage = %+v, want priced at $2/$10 (total 12)", api)
	}
	apiClassifier := decisionUsageRecordFor("traj_action_v2", 1, zeroruntime.DecisionResult{ResolvedModel: "gpt-6.1-sol", InputTokens: 1_000_000, OutputTokens: 1_000_000}, "openai-key", catalogs.Estimate)
	if apiClassifier.CostStatus != agent.CostStatusPriced || apiClassifier.CostUSD == nil || *apiClassifier.CostUSD != 12 {
		t.Fatalf("openai API-key classifier = %+v, want priced at $2/$10 (total 12)", apiClassifier)
	}
}

// A decision request that reports no tokens (a failed or empty call) has no
// usage to price. It is not priced $0, on a catalog-backed profile.
func TestDecisionWithoutUsageIsNotPricedZero(t *testing.T) {
	entry := modelregistry.ModelEntry{
		ID: "gpt-6.1-sol", DisplayName: "gpt-6.1-sol", APIModel: "gpt-6.1-sol", Provider: modelregistry.ProviderOpenAI,
		ContextLimits: modelregistry.ContextLimits{ContextWindow: 128000, MaxOutputTokens: 8192},
		Capabilities:  []modelregistry.ModelCapability{modelregistry.ModelCapabilityChat},
		Cost:          modelregistry.ModelCost{Currency: "USD", Unit: "per_1m_tokens", InputPerMillion: 2, OutputPerMillion: 10, Source: "models.dev/api.json", SourceLastVerified: "2026-10-10"},
		Status:        modelregistry.ModelStatusActive, Aliases: []string{"gpt-6.1-sol"},
	}
	catalogs := usage.NewCostCatalogs(func(string) (modelregistry.Registry, error) {
		return modelregistry.NewRegistry([]modelregistry.ModelEntry{entry})
	})
	catalogs.SetProfileCatalogIDs(map[string]string{"openai-key": "openai"})
	rec := decisionUsageRecordFor("traj_action_v2", 1, zeroruntime.DecisionResult{ResolvedModel: "gpt-6.1-sol", Provider: "provider"}, "openai-key", catalogs.Estimate)
	if rec.CostStatus == agent.CostStatusPriced || rec.CostUSD != nil {
		t.Fatalf("decision with no usage = status %q cost %v, want not priced", rec.CostStatus, rec.CostUSD)
	}
	if rec.UsageReported {
		t.Fatal("a decision with no tokens must not record its usage as reported")
	}
}
