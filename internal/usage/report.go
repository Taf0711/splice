package usage

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// usageEventPayload mirrors the persisted EventUsage payload written by the exec
// runtime. Prompt/completion/total are always stored; the cache and reasoning
// breakdown is stored when present (omitempty) so cost reconstruction matches the
// live tracker instead of over-pricing cache-heavy or reasoning-heavy turns.
// Older events without those fields decode to splice and price exactly as before.
// Model is persisted only on escalation runs (the model in force can change
// mid-run only under --allow-escalation); when absent, cost is reconstructed from
// the session's Metadata.ModelID and is a labeled estimate.
type usageEventPayload struct {
	PromptTokens      int    `json:"promptTokens"`
	CompletionTokens  int    `json:"completionTokens"`
	TotalTokens       int    `json:"totalTokens"`
	CachedInputTokens int    `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens  int    `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens   int    `json:"reasoningTokens,omitempty"`
	WebSearchRequests int    `json:"webSearchRequests,omitempty"`
	Model             string `json:"model,omitempty"`
	// Provider, Stage and Iteration are written by AttributedUsagePayload but
	// were absent here, so Go silently discarded them on decode and the report
	// could not slice usage by work unit. The writer and reader are a pair: a
	// field added to one must be added to the other.
	Provider       string   `json:"provider,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	Iteration      int      `json:"iteration,omitempty"`
	CostUSD        *float64 `json:"costUsd,omitempty"`
	CostStatus     string   `json:"costStatus,omitempty"`
	CostProvenance string   `json:"costProvenance,omitempty"`
	PricingSource  string   `json:"pricingSource,omitempty"`
	PricingAsOf    string   `json:"pricingAsOf,omitempty"`
	UnpricedReason string   `json:"unpricedReason,omitempty"`
}

// EventUsagePayload builds the persisted EventUsage payload for a usage record.
// It is the single writer paired with usageEventPayload (the reader), so the JSON
// keys can never drift. Cache and reasoning counts are written only when non-splice,
// keeping payloads compact and older readers unaffected; BuildReport reads them
// back to price a turn exactly (cache discount + cache-write premium + reasoning)
// rather than estimating from prompt/completion alone. Callers add "model"
// afterward on escalation runs.
func EventUsagePayload(u zeroruntime.Usage) map[string]any {
	payload := map[string]any{
		"promptTokens":     u.EffectiveInputTokens(),
		"completionTokens": u.EffectiveOutputTokens(),
		"totalTokens":      u.TotalTokens(),
	}
	if u.CachedInputTokens > 0 {
		payload["cachedInputTokens"] = u.CachedInputTokens
	}
	if u.CacheWriteTokens > 0 {
		payload["cacheWriteTokens"] = u.CacheWriteTokens
	}
	if u.ReasoningTokens > 0 {
		payload["reasoningTokens"] = u.ReasoningTokens
	}
	if u.WebSearchRequests > 0 {
		payload["webSearchRequests"] = u.WebSearchRequests
	}
	return payload
}

// AttributedUsagePayload builds a persisted usage payload from an
// agent.AttributedUsage, including identity, sequence, usageReported, all
// token dimensions, and all estimate fields. Legacy EventUsagePayload is
// retained for callers that do not need attribution.
func AttributedUsagePayload(au agent.AttributedUsage) map[string]any {
	payload := map[string]any{
		"promptTokens":      au.Usage.EffectiveInputTokens(),
		"completionTokens":  au.Usage.EffectiveOutputTokens(),
		"totalTokens":       au.Usage.TotalTokens(),
		"cachedInputTokens": au.Usage.CachedInputTokens,
		"cacheWriteTokens":  au.Usage.CacheWriteTokens,
		"reasoningTokens":   au.Usage.ReasoningTokens,
		"provider":          au.ProviderName,
		"model":             au.Model,
		"stage":             au.Stage,
		"iteration":         au.Iteration,
		"usageReported":     au.UsageReported,
		"usageSequence":     au.Sequence,
	}
	if au.Cost.CostUSD != nil {
		payload["costUsd"] = *au.Cost.CostUSD
		payload["costEstimated"] = au.Cost.Provenance != agent.CostProvenanceReported
	}
	if au.Usage.WebSearchRequests > 0 {
		payload["webSearchRequests"] = au.Usage.WebSearchRequests
	}
	if au.Cost.Status != "" {
		payload["costStatus"] = au.Cost.Status
	}
	if au.Cost.Provenance != "" {
		payload["costProvenance"] = au.Cost.Provenance
	}
	if au.Cost.PricingSource != "" {
		payload["pricingSource"] = au.Cost.PricingSource
	}
	if au.Cost.PricingAsOf != "" {
		payload["pricingAsOf"] = au.Cost.PricingAsOf
	}
	if au.Cost.UnpricedReason != "" {
		payload["unpricedReason"] = au.Cost.UnpricedReason
	}
	return payload
}

// DayBucket aggregates usage events sharing the same UTC calendar date.
type DayBucket struct {
	Date         string  `json:"date"`
	Requests     int     `json:"requests"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TotalTokens  int     `json:"totalTokens"`
	TotalCost    float64 `json:"totalCost"`
}

// Totals carries report-wide sums across every bucket.
type Totals struct {
	Requests     int     `json:"requests"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TotalTokens  int     `json:"totalTokens"`
	TotalCost    float64 `json:"totalCost"`
}

// WorkUnit aggregates usage for one stage/model pair. It answers "which part of
// the pipeline costs what", which per-day buckets cannot: a run routes different
// stages to different models, so the day total hides where the spend went.
// Records that carry no stage (legacy agent-loop turns) group under an empty
// Stage rather than being dropped.
type WorkUnit struct {
	Stage        string  `json:"stage"`
	Model        string  `json:"model,omitempty"`
	Provider     string  `json:"provider,omitempty"`
	Requests     int     `json:"requests"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TotalTokens  int     `json:"totalTokens"`
	TotalCost    float64 `json:"totalCost"`
}

// workUnitKey groups usage by the dimensions an optimizer cares about.
type workUnitKey struct {
	Stage    string
	Model    string
	Provider string
}

// Report is the aggregated usage view rendered by `splice usage report`. Cost
// uses a persisted estimate when present, and otherwise uses reconstruction.
// NetLOC is a working-tree estimate.
type Report struct {
	Buckets         []DayBucket `json:"buckets"`
	WorkUnits       []WorkUnit  `json:"workUnits,omitempty"`
	Total           Totals      `json:"total"`
	NetLOC          int         `json:"netLOC"`
	NetLOCPositive  bool        `json:"netLOCPositive"`
	TokensPerNetLOC float64     `json:"tokensPerNetLOC"`
	CostPerNetLOC   float64     `json:"costPerNetLOC"`
	LOCEstimated    bool        `json:"locEstimated"`
	CostEstimated   bool        `json:"costEstimated"`
}

// BuildReport aggregates persisted EventUsage events into per-day buckets and a
// report-wide total. It honors a persisted priced estimate before it attempts
// reconstruction from the owning session's Metadata.ModelID. Sessions whose
// model id is empty or unknown contribute token counts but no cost. The
// per-net-LOC ratios are guarded against a non-positive netLOC.
func BuildReport(events []sessions.Event, meta []sessions.Metadata, registry *modelregistry.Registry, netLOC int) (Report, error) {
	modelBySession := map[string]string{}
	for _, m := range meta {
		modelBySession[m.SessionID] = m.ModelID
	}

	buckets := map[string]*DayBucket{}
	workUnits := map[workUnitKey]*WorkUnit{}
	report := Report{
		NetLOC:        netLOC,
		LOCEstimated:  true,
		CostEstimated: true,
	}

	for _, event := range events {
		if event.Type != sessions.EventUsage {
			continue
		}
		var payload usageEventPayload
		if len(event.Payload) > 0 {
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return Report{}, err
			}
		}

		date := utcDayBucket(event.CreatedAt)
		bucket, ok := buckets[date]
		if !ok {
			bucket = &DayBucket{Date: date}
			buckets[date] = bucket
		}

		bucket.Requests++
		bucket.InputTokens += payload.PromptTokens
		bucket.OutputTokens += payload.CompletionTokens
		bucket.TotalTokens += payload.TotalTokens

		report.Total.Requests++
		report.Total.InputTokens += payload.PromptTokens
		report.Total.OutputTokens += payload.CompletionTokens
		report.Total.TotalTokens += payload.TotalTokens

		// Accumulate the work unit before the cost branches below: several of
		// them `continue`, and an event with no priceable cost still spent
		// tokens that belong in the stage/model breakdown.
		unitModel := payload.Model
		if unitModel == "" {
			unitModel = modelBySession[event.SessionID]
		}
		unitKey := workUnitKey{Stage: payload.Stage, Model: unitModel, Provider: payload.Provider}
		unit, ok := workUnits[unitKey]
		if !ok {
			unit = &WorkUnit{Stage: payload.Stage, Model: unitModel, Provider: payload.Provider}
			workUnits[unitKey] = unit
		}
		unit.Requests++
		unit.InputTokens += payload.PromptTokens
		unit.OutputTokens += payload.CompletionTokens
		unit.TotalTokens += payload.TotalTokens

		// A persisted priced estimate is authoritative. Do not rewrite it from
		// the current registry.
		if payload.CostStatus == CostStatusPriced {
			if payload.CostUSD == nil || math.IsNaN(*payload.CostUSD) || math.IsInf(*payload.CostUSD, 0) || *payload.CostUSD < 0 {
				return Report{}, fmt.Errorf("usage event %d: priced cost must be a finite non-negative cost_usd", event.Sequence)
			}
			bucket.TotalCost += *payload.CostUSD
			report.Total.TotalCost += *payload.CostUSD
			unit.TotalCost += *payload.CostUSD
			continue
		}
		if payload.CostStatus == CostStatusUnpriced || payload.CostStatus == CostStatusError {
			continue
		}

		// Prefer the model the event itself recorded (set on escalation runs, where
		// the model changed mid-run) so that usage is priced at the model actually
		// used; fall back to the session's model otherwise.
		modelID := payload.Model
		if modelID == "" {
			modelID = modelBySession[event.SessionID]
		}
		if modelID == "" || registry == nil {
			continue
		}
		model, err := registry.Require(modelID)
		if err != nil {
			continue
		}
		cost, err := modelregistry.CalculateCost(model, zeroruntime.Usage{
			InputTokens:       payload.PromptTokens,
			OutputTokens:      payload.CompletionTokens,
			CachedInputTokens: payload.CachedInputTokens,
			CacheWriteTokens:  payload.CacheWriteTokens,
			ReasoningTokens:   payload.ReasoningTokens,
			WebSearchRequests: payload.WebSearchRequests,
		})
		if err != nil {
			continue
		}
		bucket.TotalCost += cost.TotalCost
		report.Total.TotalCost += cost.TotalCost
		unit.TotalCost += cost.TotalCost
	}

	report.WorkUnits = make([]WorkUnit, 0, len(workUnits))
	for _, unit := range workUnits {
		report.WorkUnits = append(report.WorkUnits, *unit)
	}
	sort.SliceStable(report.WorkUnits, func(left int, right int) bool {
		if report.WorkUnits[left].TotalCost != report.WorkUnits[right].TotalCost {
			return report.WorkUnits[left].TotalCost > report.WorkUnits[right].TotalCost
		}
		if report.WorkUnits[left].Stage != report.WorkUnits[right].Stage {
			return report.WorkUnits[left].Stage < report.WorkUnits[right].Stage
		}
		return report.WorkUnits[left].Model < report.WorkUnits[right].Model
	})

	report.Buckets = make([]DayBucket, 0, len(buckets))
	for _, bucket := range buckets {
		report.Buckets = append(report.Buckets, *bucket)
	}
	sort.SliceStable(report.Buckets, func(left int, right int) bool {
		return report.Buckets[left].Date < report.Buckets[right].Date
	})

	if netLOC > 0 {
		report.NetLOCPositive = true
		report.TokensPerNetLOC = float64(report.Total.TotalTokens) / float64(netLOC)
		report.CostPerNetLOC = report.Total.TotalCost / float64(netLOC)
	}
	return report, nil
}

// utcDayBucket maps an RFC3339 timestamp to its UTC calendar date (YYYY-MM-DD).
// Normalizing to UTC first keeps an offset timestamp (e.g. ...T23:30:00-07:00,
// which is the next UTC day) bucketed by its true UTC day. On a parse failure it
// falls back to the leading-10 slice so malformed timestamps still bucket
// defensively rather than collapsing into one empty-string bucket.
func utcDayBucket(createdAt string) string {
	if parsed, err := time.Parse(time.RFC3339, createdAt); err == nil {
		return parsed.UTC().Format("2006-01-02")
	}
	if len(createdAt) >= 10 {
		return createdAt[:10]
	}
	return createdAt
}
