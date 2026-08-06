package usage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

var writerOnlyPayloadKeys = map[string]string{
	"usageReported": "consumed by the CLI stream-json writer and agenteval, not this report reader",
	"usageSequence": "consumed by the CLI stream-json writer and agenteval, not this report reader",
	"costEstimated": "redundant with costProvenance for this report reader",
}

func usageEvent(t *testing.T, sessionID string, sequence int, createdAt string, prompt int, completion int) sessions.Event {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"promptTokens":     prompt,
		"completionTokens": completion,
		"totalTokens":      prompt + completion,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return sessions.Event{
		SessionID: sessionID,
		Sequence:  sequence,
		Type:      sessions.EventUsage,
		CreatedAt: createdAt,
		Payload:   payload,
	}
}

func TestBuildReportBucketsByDayAndSumsTokens(t *testing.T) {
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	events := []sessions.Event{
		usageEvent(t, "s1", 1, "2026-06-01T09:00:00Z", 1000, 200),
		usageEvent(t, "s1", 2, "2026-06-01T11:00:00Z", 500, 100),
		usageEvent(t, "s2", 1, "2026-06-02T09:00:00Z", 2000, 400),
		// Non-UTC offset event: local day is 2026-06-01, but its UTC instant is
		// 2026-06-02T06:30:00Z, so it must bucket under 2026-06-02 (the UTC day),
		// not 2026-06-01.
		usageEvent(t, "s3", 1, "2026-06-01T23:30:00-07:00", 300, 50),
	}
	meta := []sessions.Metadata{
		{SessionID: "s1", ModelID: "gpt-4.1"},
		{SessionID: "s2", ModelID: "gpt-4.1"},
		{SessionID: "s3", ModelID: "gpt-4.1"},
	}

	report, err := BuildReport(events, meta, &registry, 70)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(report.Buckets) != 2 {
		t.Fatalf("expected 2 day buckets, got %d", len(report.Buckets))
	}
	if report.Buckets[0].Date != "2026-06-01" || report.Buckets[1].Date != "2026-06-02" {
		t.Fatalf("buckets not sorted by date: %+v", report.Buckets)
	}
	if report.Buckets[0].Requests != 2 || report.Buckets[0].TotalTokens != 1800 {
		t.Fatalf("day-1 aggregation wrong: %+v", report.Buckets[0])
	}
	// Day-2 holds the UTC 2026-06-02 event (2400 tokens) plus the offset event
	// whose UTC day is also 2026-06-02 (350 tokens) = 2 requests, 2750 tokens.
	if report.Buckets[1].Requests != 2 || report.Buckets[1].TotalTokens != 2750 {
		t.Fatalf("day-2 aggregation wrong (offset event must bucket under UTC 2026-06-02): %+v", report.Buckets[1])
	}
	if report.Total.Requests != 4 || report.Total.TotalTokens != 4550 {
		t.Fatalf("totals wrong: %+v", report.Total)
	}
	if !report.LOCEstimated {
		t.Fatalf("expected LOCEstimated=true")
	}
	if report.NetLOC != 70 {
		t.Fatalf("NetLOC = %d, want 70", report.NetLOC)
	}
}

func TestBuildReportKeepsCompactionStageFromPersistedPayload(t *testing.T) {
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	payload := AttributedUsagePayload(agent.AttributedUsage{
		Usage:        zeroruntime.Usage{InputTokens: 100, OutputTokens: 20},
		ProviderName: "openai",
		Model:        "gpt-4.1",
		Stage:        "compaction",
	})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	report, err := BuildReport([]sessions.Event{{
		SessionID: "s1",
		Sequence:  1,
		Type:      sessions.EventUsage,
		CreatedAt: "2026-06-01T09:00:00Z",
		Payload:   raw,
	}}, nil, &registry, 1)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(report.WorkUnits) != 1 {
		t.Fatalf("work units = %d, want 1", len(report.WorkUnits))
	}
	got := report.WorkUnits[0]
	if got.Stage != "compaction" || got.Model != "gpt-4.1" || got.Provider != "openai" {
		t.Fatalf("work unit identity = %+v, want compaction/openai/gpt-4.1", got)
	}
}

// TestAttributedUsagePayloadPersistsReportedCost proves acceptance criterion
// (c): a provider-reported cost (Provenance=CostProvenanceReported) survives
// into the persisted EventUsage payload with the exact value and with
// costEstimated=false, so downstream readers (BuildReport, the CLI stream-json
// writer) can tell a reported charge apart from a registry estimate.
func TestAttributedUsagePayloadPersistsReportedCost(t *testing.T) {
	reported := 0.00054
	payload := AttributedUsagePayload(agent.AttributedUsage{
		Usage:         zeroruntime.Usage{InputTokens: 12, OutputTokens: 5},
		ProviderName:  "openai",
		Model:         "openrouter/some-model",
		Stage:         "code_writer",
		UsageReported: true,
		Cost: agent.UsageCostEstimate{
			CostUSD:       &reported,
			Status:        CostStatusPriced,
			Provenance:    CostProvenanceReported,
			PricingSource: "openrouter",
			PricingAsOf:   "live",
		},
	})
	if got, ok := payload["costUsd"].(float64); !ok || got != reported {
		t.Fatalf("payload[costUsd] = %v, want %v", payload["costUsd"], reported)
	}
	if got, ok := payload["costEstimated"].(bool); !ok || got != false {
		t.Fatalf("payload[costEstimated] = %v, want false", payload["costEstimated"])
	}
	if payload["costProvenance"] != CostProvenanceReported {
		t.Fatalf("payload[costProvenance] = %v, want %s", payload["costProvenance"], CostProvenanceReported)
	}
	if payload["pricingSource"] != "openrouter" {
		t.Fatalf("payload[pricingSource] = %v, want openrouter", payload["pricingSource"])
	}
}

func TestBuildReportReconstructsCostFromMetadataModel(t *testing.T) {
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	events := []sessions.Event{
		usageEvent(t, "s1", 1, "2026-06-01T09:00:00Z", 1000, 200),
	}
	meta := []sessions.Metadata{{SessionID: "s1", ModelID: "gpt-4.1"}}

	report, err := BuildReport(events, meta, &registry, 10)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}

	model, err := registry.Require("gpt-4.1")
	if err != nil {
		t.Fatalf("Require: %v", err)
	}
	want, err := modelregistry.CalculateCost(model, zeroruntime.Usage{InputTokens: 1000, OutputTokens: 200})
	if err != nil {
		t.Fatalf("CalculateCost: %v", err)
	}
	if report.Total.TotalCost != want.TotalCost {
		t.Fatalf("reconstructed cost = %v, want %v", report.Total.TotalCost, want.TotalCost)
	}
}

func TestBuildReportPricesFromEventModelWhenPresent(t *testing.T) {
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	// An escalation-run event carries its own model. The session metadata has no
	// model id, so cost can only be reconstructed from the event's model. This proves
	// the report prefers payload.Model rather than ignoring it.
	payload, err := json.Marshal(map[string]any{
		"promptTokens":     1000,
		"completionTokens": 200,
		"totalTokens":      1200,
		"model":            "gpt-4.1",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	events := []sessions.Event{{
		SessionID: "s1",
		Sequence:  1,
		Type:      sessions.EventUsage,
		CreatedAt: "2026-06-01T09:00:00Z",
		Payload:   payload,
	}}
	meta := []sessions.Metadata{{SessionID: "s1", ModelID: ""}} // no session-level model

	report, err := BuildReport(events, meta, &registry, 10)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	model, err := registry.Require("gpt-4.1")
	if err != nil {
		t.Fatalf("Require: %v", err)
	}
	want, err := modelregistry.CalculateCost(model, zeroruntime.Usage{InputTokens: 1000, OutputTokens: 200})
	if err != nil {
		t.Fatalf("CalculateCost: %v", err)
	}
	if want.TotalCost <= 0 {
		t.Fatalf("precondition: expected non-splice cost for the event model")
	}
	if report.Total.TotalCost != want.TotalCost {
		t.Fatalf("cost = %v, want %v (must price from the event's model, not the empty session model)", report.Total.TotalCost, want.TotalCost)
	}
}

func TestBuildReportPrefersPersistedCostEstimate(t *testing.T) {
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	persisted := 3.75
	payload, err := json.Marshal(map[string]any{
		"promptTokens":     1000,
		"completionTokens": 200,
		"totalTokens":      1200,
		"model":            "gpt-4.1",
		"costUsd":          persisted,
		"costStatus":       CostStatusPriced,
		"costProvenance":   CostProvenancePersistedEstimate,
		"pricingSource":    "persisted-catalog",
		"pricingAsOf":      "2026-06-01",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	events := []sessions.Event{{
		SessionID: "s1",
		Sequence:  1,
		Type:      sessions.EventUsage,
		CreatedAt: "2026-06-01T09:00:00Z",
		Payload:   payload,
	}}
	report, err := BuildReport(events, nil, &registry, 10)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.Total.TotalTokens != 1200 || report.Total.TotalCost != persisted {
		t.Fatalf("report = %#v, want persisted cost %v", report.Total, persisted)
	}
}

func TestBuildReportRatiosGuardNetZero(t *testing.T) {
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	events := []sessions.Event{usageEvent(t, "s1", 1, "2026-06-01T09:00:00Z", 1000, 200)}
	meta := []sessions.Metadata{{SessionID: "s1", ModelID: "gpt-4.1"}}

	report, err := BuildReport(events, meta, &registry, 0)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.TokensPerNetLOC != 0 || report.CostPerNetLOC != 0 {
		t.Fatalf("expected zeroed ratios for net<=0, got tokens=%v cost=%v", report.TokensPerNetLOC, report.CostPerNetLOC)
	}
	if report.NetLOCPositive {
		t.Fatalf("expected NetLOCPositive=false for net=0")
	}

	report, err = BuildReport(events, meta, &registry, 600)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if !report.NetLOCPositive || report.TokensPerNetLOC != 2 {
		t.Fatalf("expected tokens/net = 1200/600 = 2, got %v (positive=%v)", report.TokensPerNetLOC, report.NetLOCPositive)
	}
}

func TestBuildReportIgnoresNonUsageEvents(t *testing.T) {
	registry, err := testPricedRegistry(t)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	events := []sessions.Event{
		{SessionID: "s1", Sequence: 1, Type: sessions.EventMessage, CreatedAt: "2026-06-01T09:00:00Z"},
		usageEvent(t, "s1", 2, "2026-06-01T09:30:00Z", 1000, 200),
	}
	meta := []sessions.Metadata{{SessionID: "s1", ModelID: "gpt-4.1"}}

	report, err := BuildReport(events, meta, &registry, 10)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.Total.Requests != 1 {
		t.Fatalf("expected non-usage event ignored, got %d requests", report.Total.Requests)
	}
}

// The writer (AttributedUsagePayload) emits provider/stage/iteration, but the
// reader struct omitted them, so Go silently dropped them on decode and the
// report could not slice usage by work unit. This pins the writer/reader pair.
func TestBuildReportGroupsByWorkUnit(t *testing.T) {
	cost := 0.25
	payload := AttributedUsagePayload(agent.AttributedUsage{
		Usage:         zeroruntime.Usage{InputTokens: 100, OutputTokens: 50},
		ProviderName:  "openrouter",
		Model:         "z-ai/glm-5.2",
		Stage:         "code_writer",
		Iteration:     1,
		UsageReported: true,
		Cost:          agent.UsageCostEstimate{CostUSD: &cost, Status: CostStatusPriced},
	})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(
		[]sessions.Event{{Type: sessions.EventUsage, Payload: raw, CreatedAt: "2026-07-31T00:00:00Z", Sequence: 1}},
		nil, nil, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.WorkUnits) != 1 {
		t.Fatalf("work units = %d, want 1", len(report.WorkUnits))
	}
	got := report.WorkUnits[0]
	if got.Stage != "code_writer" || got.Model != "z-ai/glm-5.2" || got.Provider != "openrouter" {
		t.Fatalf("work unit lost identity on decode: %+v", got)
	}
	if got.Requests != 1 || got.TotalCost != cost {
		t.Fatalf("work unit = %+v, want 1 request costing %v", got, cost)
	}
}

// The reader once lacked provider, stage, and iteration, so encoding/json
// dropped them and usage could not be sliced by work unit. This pins the pair.
func TestAttributedUsagePayloadKeysAreRead(t *testing.T) {
	costUSD := 1.25
	payload := AttributedUsagePayload(agent.AttributedUsage{
		Sequence: 42,
		Usage: zeroruntime.Usage{
			InputTokens:       100,
			OutputTokens:      80,
			PromptTokens:      90,
			CompletionTokens:  70,
			CachedInputTokens: 10,
			CacheWriteTokens:  5,
			ReasoningTokens:   20,
			WebSearchRequests: 3,
		},
		UsageReported: true,
		ProviderName:  "openai",
		Model:         "gpt-4.1",
		Stage:         "code_writer",
		Iteration:     2,
		Cost: agent.UsageCostEstimate{
			CostUSD:        &costUSD,
			Status:         agent.CostStatusPriced,
			Provenance:     agent.CostProvenanceRuntimeEstimate,
			PricingSource:  "test-catalog",
			PricingAsOf:    "2026-07-31",
			UnpricedReason: "not applicable",
		},
	})

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal attributed payload: %v", err)
	}
	var encoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("unmarshal attributed payload: %v", err)
	}

	readerKeys := map[string]struct{}{}
	typ := reflect.TypeOf(usageEventPayload{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		key := strings.Split(tag, ",")[0]
		if key != "" && key != "-" {
			readerKeys[key] = struct{}{}
		}
	}

	for key := range encoded {
		if _, ok := readerKeys[key]; ok {
			continue
		}
		if reason, ok := writerOnlyPayloadKeys[key]; ok && reason != "" {
			continue
		}
		t.Fatalf("payload key %q is not read; add it to the reader, or add it to writerOnlyPayloadKeys with a reason", key)
	}
}
