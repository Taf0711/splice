package usage

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type Normalized struct {
	InputTokens       int
	CachedInputTokens int
	CacheWriteTokens  int
	OutputTokens      int
	ReasoningTokens   int
	TotalTokens       int
}

// Cost status values identify the pricing state of one usage record.
const (
	CostStatusPriced   = agent.CostStatusPriced
	CostStatusUnpriced = agent.CostStatusUnpriced
	CostStatusError    = agent.CostStatusError
)

const (
	CostProvenanceRuntimeEstimate       = agent.CostProvenanceRuntimeEstimate
	CostProvenancePersistedEstimate     = agent.CostProvenancePersistedEstimate
	CostProvenanceReconstructedEstimate = agent.CostProvenanceReconstructedEstimate
	CostProvenanceReported              = agent.CostProvenanceReported
)

// Cost coverage values identify the pricing coverage of a summary.
const (
	CostCoverageComplete      = "complete"
	CostCoveragePartial       = "partial"
	CostCoverageUnavailable   = "unavailable"
	CostCoverageNotApplicable = "not_applicable"
)

// CostEstimate carries a persisted or reconstructed estimate. The embedded
// breakdown keeps the existing component-cost fields available to callers.
// CostUSD is a pointer so a priced zero is distinct from an unknown cost.
type CostEstimate struct {
	modelregistry.CostBreakdown
	CostUSD        *float64
	Status         string
	CostStatus     string
	Provenance     string
	CostProvenance string
	PricingSource  string
	PricingAsOf    string
	UnpricedReason string
	persisted      bool
}

type RecordInput struct {
	ModelID        string
	Usage          zeroruntime.Usage
	Source         string
	Cost           *CostEstimate
	PersistedCost  *CostEstimate
	CostUSD        *float64
	CostStatus     string
	CostProvenance string
	PricingSource  string
	PricingAsOf    string
	UnpricedReason string
}

type Record struct {
	ID             string
	Sequence       int
	ModelID        string
	Provider       modelregistry.ProviderKind
	Source         string
	CreatedAt      string
	Usage          Normalized
	Cost           *CostEstimate
	CostUSD        *float64
	CostStatus     string
	CostProvenance string
	PricingSource  string
	PricingAsOf    string
	UnpricedReason string
}

type ModelSummary struct {
	ModelID            string
	Provider           modelregistry.ProviderKind
	RecordCount        int
	InputTokens        int
	CachedInputTokens  int
	CacheWriteTokens   int
	OutputTokens       int
	ReasoningTokens    int
	TotalTokens        int
	InputCost          float64
	CachedInputCost    float64
	CacheWriteCost     float64
	OutputCost         float64
	TotalCost          float64
	FormattedTotalCost string
}

type Summary struct {
	RecordCount        int
	Currency           string
	InputTokens        int
	CachedInputTokens  int
	CacheWriteTokens   int
	OutputTokens       int
	ReasoningTokens    int
	TotalTokens        int
	InputCost          float64
	CachedInputCost    float64
	CacheWriteCost     float64
	OutputCost         float64
	TotalCost          float64
	FormattedTotalCost string
	PersistedCount     int
	ReconstructedCount int
	UnpricedCount      int
	ErrorCount         int
	CostCoverage       string
	ByModel            []ModelSummary
	LastRecord         *Record
}

type TrackerOptions struct {
	Now      func() time.Time
	Registry *modelregistry.Registry
}

type Tracker struct {
	now      func() time.Time
	registry modelregistry.Registry
	records  []Record
	nextSeq  int
}

func NewTracker(options TrackerOptions) (*Tracker, error) {
	registry := options.Registry
	if registry == nil {
		defaultRegistry, err := modelregistry.DefaultRegistry()
		if err != nil {
			return nil, fmt.Errorf("usage.NewTracker: default registry: %w", err)
		}
		registry = &defaultRegistry
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Tracker{now: now, registry: *registry, nextSeq: 1}, nil
}

func (tracker *Tracker) Record(input RecordInput) (Record, error) {
	normalized, runtimeUsage, err := Normalize(input.Usage)
	if err != nil {
		return Record{}, err
	}
	persistedCost, err := persistedInputCost(input)
	if err != nil {
		return Record{}, err
	}
	sequence := tracker.nextSeq
	tracker.nextSeq++
	record := Record{
		ID:        fmt.Sprintf("zero_usage_%d", sequence),
		Sequence:  sequence,
		ModelID:   strings.TrimSpace(input.ModelID),
		Source:    input.Source,
		CreatedAt: tracker.now().UTC().Format(time.RFC3339),
		Usage:     normalized,
	}

	if persistedCost != nil {
		persistedCost.persisted = true
		record.Cost = persistedCost
		copyCostMetadata(&record, persistedCost)
		tracker.records = append(tracker.records, record)
		return record, nil
	}

	if record.ModelID == "" {
		record.Cost = unpricedCost("model identity is missing")
		copyCostMetadata(&record, record.Cost)
		tracker.records = append(tracker.records, record)
		return record, nil
	}

	model, err := tracker.registry.Require(record.ModelID)
	if err != nil {
		record.Cost = errorCost(fmt.Sprintf("price unavailable for model %q: %v", record.ModelID, err))
		copyCostMetadata(&record, record.Cost)
		tracker.records = append(tracker.records, record)
		return record, fmt.Errorf("usage.Record model %q: %w", record.ModelID, err)
	}
	record.ModelID = model.ID
	record.Provider = model.Provider
	cost, err := modelregistry.CalculateCost(model, runtimeUsage)
	if err != nil {
		if model.Cost.IsUnpriced() {
			record.Cost = unpricedCost(fmt.Sprintf("price unavailable for model %q: %v", record.ModelID, err))
			copyCostMetadata(&record, record.Cost)
			tracker.records = append(tracker.records, record)
			return record, nil
		}
		record.Cost = errorCost(fmt.Sprintf("price calculation failed for model %q: %v", record.ModelID, err))
		copyCostMetadata(&record, record.Cost)
		tracker.records = append(tracker.records, record)
		return record, fmt.Errorf("usage.Record model %q: %w", record.ModelID, err)
	}
	record.Cost = reconstructedCost(cost, model)
	copyCostMetadata(&record, record.Cost)
	tracker.records = append(tracker.records, record)
	return record, nil
}

func (tracker *Tracker) Records() []Record {
	return append([]Record{}, tracker.records...)
}

func (tracker *Tracker) Summary() Summary {
	summary := Summary{Currency: "USD", CostCoverage: CostCoverageNotApplicable}
	models := map[string]int{}
	for _, record := range tracker.records {
		summary.RecordCount++
		addUsageToSummary(&summary, record.Usage)
		countCostStatus(&summary, record.Cost)
		addCostToSummary(&summary, record.Cost)
		index, ok := models[record.ModelID]
		if !ok {
			summary.ByModel = append(summary.ByModel, ModelSummary{
				ModelID:  record.ModelID,
				Provider: record.Provider,
			})
			index = len(summary.ByModel) - 1
			models[record.ModelID] = index
		}
		addUsageToModel(&summary.ByModel[index], record.Usage)
		addCostToModel(&summary.ByModel[index], record.Cost)
		recordCopy := record
		summary.LastRecord = &recordCopy
	}
	summary.CostCoverage = costCoverage(summary.PersistedCount, summary.ReconstructedCount, summary.UnpricedCount, summary.ErrorCount)
	summary.FormattedTotalCost = formatCost(summary.TotalCost)
	for index := range summary.ByModel {
		summary.ByModel[index].FormattedTotalCost = formatCost(summary.ByModel[index].TotalCost)
	}
	return summary
}

func (tracker *Tracker) Reset() {
	tracker.records = nil
	tracker.nextSeq = 1
}

func Normalize(usage zeroruntime.Usage) (Normalized, zeroruntime.Usage, error) {
	inputTokens, err := nonNegative(firstNonZero(usage.InputTokens, usage.PromptTokens), "inputTokens")
	if err != nil {
		return Normalized{}, zeroruntime.Usage{}, err
	}
	outputTokens, err := nonNegative(firstNonZero(usage.OutputTokens, usage.CompletionTokens), "outputTokens")
	if err != nil {
		return Normalized{}, zeroruntime.Usage{}, err
	}
	cachedInputTokens, err := nonNegative(usage.CachedInputTokens, "cachedInputTokens")
	if err != nil {
		return Normalized{}, zeroruntime.Usage{}, err
	}
	if cachedInputTokens > inputTokens {
		return Normalized{}, zeroruntime.Usage{}, fmt.Errorf("cached input tokens %d exceeds input tokens %d", cachedInputTokens, inputTokens)
	}
	cacheWriteTokens, err := nonNegative(usage.CacheWriteTokens, "cacheWriteTokens")
	if err != nil {
		return Normalized{}, zeroruntime.Usage{}, err
	}
	if cacheWriteTokens > inputTokens-cachedInputTokens {
		return Normalized{}, zeroruntime.Usage{}, fmt.Errorf("cache write tokens %d plus cached input tokens %d exceeds input tokens %d", cacheWriteTokens, cachedInputTokens, inputTokens)
	}
	reasoningTokens, err := nonNegative(usage.ReasoningTokens, "reasoningTokens")
	if err != nil {
		return Normalized{}, zeroruntime.Usage{}, err
	}
	if reasoningTokens > outputTokens {
		return Normalized{}, zeroruntime.Usage{}, fmt.Errorf("reasoning tokens %d exceeds output tokens %d", reasoningTokens, outputTokens)
	}
	normalized := Normalized{
		InputTokens:       inputTokens,
		CachedInputTokens: cachedInputTokens,
		CacheWriteTokens:  cacheWriteTokens,
		OutputTokens:      outputTokens,
		ReasoningTokens:   reasoningTokens,
		TotalTokens:       inputTokens + outputTokens,
	}
	return normalized, zeroruntime.Usage{
		InputTokens:       inputTokens,
		PromptTokens:      inputTokens,
		CachedInputTokens: cachedInputTokens,
		CacheWriteTokens:  cacheWriteTokens,
		OutputTokens:      outputTokens,
		CompletionTokens:  outputTokens,
		ReasoningTokens:   reasoningTokens,
	}, nil
}

func FormatSummary(summary Summary) string {
	requestLabel := "requests"
	if summary.RecordCount == 1 {
		requestLabel = "request"
	}
	return fmt.Sprintf("%s %s, %s tokens, %s", comma(summary.RecordCount), requestLabel, comma(summary.TotalTokens), summary.FormattedTotalCost)
}

// CostDisplay contains the cost text and an optional unpriced-record note.
type CostDisplay struct {
	Cost     string
	Unpriced string
}

// FormatCostDisplay renders a cost and keeps partial and unavailable states clear.
func FormatCostDisplay(coverage string, total float64, unpricedCount int) CostDisplay {
	display := CostDisplay{}
	switch coverage {
	case CostCoverageComplete:
		display.Cost = formatCost(total)
	case CostCoveragePartial:
		if total > 0 {
			display.Cost = "~" + formatCost(total)
			if unpricedCount > 0 {
				requestLabel := "unpriced requests"
				if unpricedCount == 1 {
					requestLabel = "unpriced request"
				}
				display.Unpriced = fmt.Sprintf("%d %s", unpricedCount, requestLabel)
			}
			break
		}
		display.Cost = "cost partial"
	case CostCoverageUnavailable:
		display.Cost = "cost unavailable"
	case CostCoverageNotApplicable:
		display.Cost = "cost n/a"
	default:
		display.Cost = "cost unavailable"
	}
	return display
}

// CacheHitRate is the fraction of input tokens served from the provider's prompt
// cache. CachedInputTokens is clamped to InputTokens in Normalize, so the result is
// always in [0,1]; it is 0 when no input has been recorded.
func (summary Summary) CacheHitRate() float64 {
	if summary.InputTokens <= 0 {
		return 0
	}
	return float64(summary.CachedInputTokens) / float64(summary.InputTokens)
}

// FormatCacheEfficiency renders the prompt-cache hit rate for display, e.g.
// "63% (8,200 cached / 13,100 input)", so a user can see whether cache reads are
// actually saving work. Returns "n/a" until some input has been recorded.
func FormatCacheEfficiency(summary Summary) string {
	if summary.InputTokens <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%% (%s cached / %s input)",
		summary.CacheHitRate()*100,
		comma(summary.CachedInputTokens),
		comma(summary.InputTokens))
}

func addUsageToSummary(summary *Summary, usage Normalized) {
	summary.InputTokens += usage.InputTokens
	summary.CachedInputTokens += usage.CachedInputTokens
	summary.CacheWriteTokens += usage.CacheWriteTokens
	summary.OutputTokens += usage.OutputTokens
	summary.ReasoningTokens += usage.ReasoningTokens
	summary.TotalTokens += usage.TotalTokens
}

func addUsageToModel(summary *ModelSummary, usage Normalized) {
	summary.RecordCount++
	summary.InputTokens += usage.InputTokens
	summary.CachedInputTokens += usage.CachedInputTokens
	summary.CacheWriteTokens += usage.CacheWriteTokens
	summary.OutputTokens += usage.OutputTokens
	summary.ReasoningTokens += usage.ReasoningTokens
	summary.TotalTokens += usage.TotalTokens
}

func persistedInputCost(input RecordInput) (*CostEstimate, error) {
	if input.Cost != nil && input.PersistedCost != nil && input.Cost != input.PersistedCost {
		return nil, fmt.Errorf("usage.Record model %q: multiple persisted cost estimates supplied", input.ModelID)
	}
	cost := input.Cost
	if cost == nil {
		cost = input.PersistedCost
	}
	if hasDirectCostFields(input) {
		if cost != nil {
			return nil, fmt.Errorf("usage.Record model %q: multiple persisted cost estimates supplied", input.ModelID)
		}
		cost = &CostEstimate{
			CostUSD:        input.CostUSD,
			CostStatus:     input.CostStatus,
			CostProvenance: input.CostProvenance,
			PricingSource:  input.PricingSource,
			PricingAsOf:    input.PricingAsOf,
			UnpricedReason: input.UnpricedReason,
		}
	}
	if cost == nil {
		return nil, nil
	}
	clone := cloneCostEstimate(*cost)
	status, err := canonicalCostStatus(clone)
	if err != nil {
		return nil, fmt.Errorf("usage.Record model %q: %w", input.ModelID, err)
	}
	provenance, err := canonicalCostProvenance(clone)
	if err != nil {
		return nil, fmt.Errorf("usage.Record model %q: %w", input.ModelID, err)
	}
	clone.Status = status
	clone.CostStatus = status
	clone.Provenance = provenance
	clone.CostProvenance = provenance
	switch status {
	case CostStatusPriced:
		if clone.CostUSD == nil || math.IsNaN(*clone.CostUSD) || math.IsInf(*clone.CostUSD, 0) || *clone.CostUSD < 0 {
			return nil, fmt.Errorf("usage.Record model %q: priced estimate requires a finite non-negative cost_usd", input.ModelID)
		}
		if provenance == "" {
			return nil, fmt.Errorf("usage.Record model %q: priced estimate requires cost_provenance", input.ModelID)
		}
		switch provenance {
		case CostProvenanceRuntimeEstimate, CostProvenancePersistedEstimate, CostProvenanceReconstructedEstimate, CostProvenanceReported:
		default:
			return nil, fmt.Errorf("usage.Record model %q: invalid cost_provenance %q", input.ModelID, provenance)
		}
		if clone.PricingSource == "" || clone.PricingAsOf == "" {
			return nil, fmt.Errorf("usage.Record model %q: priced estimate requires pricing_source and pricing_as_of", input.ModelID)
		}
		if clone.UnpricedReason != "" {
			return nil, fmt.Errorf("usage.Record model %q: priced estimate must not have unpriced_reason", input.ModelID)
		}
		clone.TotalCost = *clone.CostUSD
	case CostStatusUnpriced, CostStatusError:
		if clone.CostUSD != nil {
			return nil, fmt.Errorf("usage.Record model %q: %s estimate must not have cost_usd", input.ModelID, status)
		}
		if provenance != "" || clone.PricingSource != "" || clone.PricingAsOf != "" {
			return nil, fmt.Errorf("usage.Record model %q: %s estimate must not have pricing provenance", input.ModelID, status)
		}
		if clone.UnpricedReason == "" {
			return nil, fmt.Errorf("usage.Record model %q: %s estimate requires unpriced_reason", input.ModelID, status)
		}
	default:
		return nil, fmt.Errorf("usage.Record model %q: invalid cost_status %q", input.ModelID, status)
	}
	return &clone, nil
}

func hasDirectCostFields(input RecordInput) bool {
	return input.CostUSD != nil || input.CostStatus != "" || input.CostProvenance != "" || input.PricingSource != "" || input.PricingAsOf != "" || input.UnpricedReason != ""
}

func copyCostMetadata(record *Record, cost *CostEstimate) {
	if record == nil || cost == nil {
		return
	}
	record.CostUSD = cost.CostUSD
	record.CostStatus = costStatus(*cost)
	record.CostProvenance, _ = canonicalCostProvenance(*cost)
	record.PricingSource = cost.PricingSource
	record.PricingAsOf = cost.PricingAsOf
	record.UnpricedReason = cost.UnpricedReason
}

func cloneCostEstimate(cost CostEstimate) CostEstimate {
	clone := cost
	if cost.CostUSD != nil {
		value := *cost.CostUSD
		clone.CostUSD = &value
	}
	if cost.PricingTier != nil {
		tier := *cost.PricingTier
		clone.PricingTier = &tier
	}
	return clone
}

func canonicalCostStatus(cost CostEstimate) (string, error) {
	if cost.Status != "" && cost.CostStatus != "" && cost.Status != cost.CostStatus {
		return "", fmt.Errorf("conflicting cost status values %q and %q", cost.Status, cost.CostStatus)
	}
	status := cost.CostStatus
	if status == "" {
		status = cost.Status
	}
	return status, nil
}

func canonicalCostProvenance(cost CostEstimate) (string, error) {
	if cost.Provenance != "" && cost.CostProvenance != "" && cost.Provenance != cost.CostProvenance {
		return "", fmt.Errorf("conflicting cost provenance values %q and %q", cost.Provenance, cost.CostProvenance)
	}
	provenance := cost.CostProvenance
	if provenance == "" {
		provenance = cost.Provenance
	}
	return provenance, nil
}

func reconstructedCost(cost modelregistry.CostBreakdown, model modelregistry.ModelEntry) *CostEstimate {
	costUSD := cost.TotalCost
	return &CostEstimate{
		CostBreakdown:  cost,
		CostUSD:        &costUSD,
		Status:         CostStatusPriced,
		CostStatus:     CostStatusPriced,
		Provenance:     CostProvenanceReconstructedEstimate,
		CostProvenance: CostProvenanceReconstructedEstimate,
		PricingSource:  model.Cost.Source,
		PricingAsOf:    model.Cost.SourceLastVerified,
	}
}

func unpricedCost(reason string) *CostEstimate {
	return &CostEstimate{Status: CostStatusUnpriced, CostStatus: CostStatusUnpriced, UnpricedReason: reason}
}

func errorCost(reason string) *CostEstimate {
	return &CostEstimate{Status: CostStatusError, CostStatus: CostStatusError, UnpricedReason: reason}
}

func costStatus(cost CostEstimate) string {
	status, _ := canonicalCostStatus(cost)
	return status
}

func countCostStatus(summary *Summary, cost *CostEstimate) {
	if cost == nil {
		summary.UnpricedCount++
		return
	}
	switch costStatus(*cost) {
	case CostStatusPriced:
		if cost.persisted {
			summary.PersistedCount++
		} else {
			summary.ReconstructedCount++
		}
	case CostStatusUnpriced:
		summary.UnpricedCount++
	case CostStatusError:
		summary.ErrorCount++
	default:
		summary.ErrorCount++
	}
}

func costCoverage(persisted, reconstructed, unpriced, errors int) string {
	priced := persisted + reconstructed
	total := priced + unpriced + errors
	switch {
	case total == 0:
		return CostCoverageNotApplicable
	case priced == total:
		return CostCoverageComplete
	case priced > 0:
		return CostCoveragePartial
	default:
		return CostCoverageUnavailable
	}
}

func addCostToSummary(summary *Summary, cost *CostEstimate) {
	if cost == nil || costStatus(*cost) != CostStatusPriced || cost.CostUSD == nil {
		return
	}
	summary.InputCost += cost.InputCost
	summary.CachedInputCost += cost.CachedInputCost
	summary.CacheWriteCost += cost.CacheWriteCost
	summary.OutputCost += cost.OutputCost
	summary.TotalCost += *cost.CostUSD
}

func addCostToModel(summary *ModelSummary, cost *CostEstimate) {
	if cost == nil || costStatus(*cost) != CostStatusPriced || cost.CostUSD == nil {
		return
	}
	summary.InputCost += cost.InputCost
	summary.CachedInputCost += cost.CachedInputCost
	summary.CacheWriteCost += cost.CacheWriteCost
	summary.OutputCost += cost.OutputCost
	summary.TotalCost += *cost.CostUSD
}

func formatCost(value float64) string {
	formatted, err := modelregistry.FormatCostUSD(value)
	if err != nil {
		return "$0.0000"
	}
	return formatted
}

func nonNegative(value int, label string) (int, error) {
	if value < 0 {
		return 0, fmt.Errorf("expected %s to be non-negative", label)
	}
	return value, nil
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func comma(value int) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	digits := fmt.Sprintf("%d", value)
	if len(digits) <= 3 {
		return sign + digits
	}
	var out []byte
	prefix := len(digits) % 3
	if prefix == 0 {
		prefix = 3
	}
	out = append(out, digits[:prefix]...)
	for index := prefix; index < len(digits); index += 3 {
		out = append(out, ',')
		out = append(out, digits[index:index+3]...)
	}
	return sign + string(out)
}

// NewCostEstimator returns an agent.EstimateUsageCost callback that prices
// LLM requests using the provided model registry. Missing provider usage,
// unknown models, and missing rates produce unpriced records. Malformed
// reported usage produces error records. Known models including reported zero
// usage produce priced records with CostUSD set.
func NewCostEstimator(registry *modelregistry.Registry) func(model string, usage zeroruntime.Usage, reported bool) agent.UsageCostEstimate {
	return func(model string, usage zeroruntime.Usage, reported bool) agent.UsageCostEstimate {
		if !reported {
			return agent.UsageCostEstimate{
				Status:         agent.CostStatusUnpriced,
				UnpricedReason: "usage not reported by provider",
			}
		}
		_, normalizedUsage, err := Normalize(usage)
		if err != nil {
			return agent.UsageCostEstimate{
				Status:         agent.CostStatusError,
				UnpricedReason: err.Error(),
			}
		}
		if model == "" {
			return agent.UsageCostEstimate{
				Status:         agent.CostStatusUnpriced,
				UnpricedReason: "model unknown",
			}
		}
		if registry == nil {
			return agent.UsageCostEstimate{
				Status:         agent.CostStatusUnpriced,
				UnpricedReason: "model registry unavailable",
			}
		}
		entry, err := registry.Require(model)
		if err != nil {
			return agent.UsageCostEstimate{
				Status:         agent.CostStatusUnpriced,
				UnpricedReason: fmt.Sprintf("model %q not in registry", model),
			}
		}
		breakdown, err := modelregistry.CalculateCost(entry, normalizedUsage)
		if err != nil {
			return agent.UsageCostEstimate{
				Status:         agent.CostStatusUnpriced,
				UnpricedReason: err.Error(),
			}
		}
		costUSD := breakdown.TotalCost
		return agent.UsageCostEstimate{
			CostUSD:       &costUSD,
			Status:        agent.CostStatusPriced,
			Provenance:    agent.CostProvenanceRuntimeEstimate,
			PricingSource: entry.Cost.Source,
			PricingAsOf:   entry.Cost.SourceLastVerified,
		}
	}
}
