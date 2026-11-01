package cli

import (
	"fmt"

	"github.com/Taf0711/splice/internal/agent"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// loopUsageLedger adapts plain agent-loop usage to the pipeline's authoritative
// request ledger shape. The warmcost harness can therefore consume both modes
// without a second accounting format.
type loopUsageLedger struct {
	records []schemas.PipelineUsageRecord
	served  agent.ServedUsageCostEstimator
}

func newLoopUsageLedger(served agent.ServedUsageCostEstimator) *loopUsageLedger {
	return &loopUsageLedger{served: served}
}

func (l *loopUsageLedger) append(attributed agent.AttributedUsage) {
	if l == nil {
		return
	}
	usage := attributed.Usage
	if attributed.UsageError != "" {
		usage = zeroruntime.Usage{}
	}
	if attributed.UsageReported && attributed.UsageError == "" {
		normalized, err := zeroruntime.NormalizeUsage(zeroruntime.TokenUsage{
			InputTokens: usage.InputTokens, PromptTokens: usage.PromptTokens,
			CachedInputTokens: usage.CachedInputTokens, CacheWriteTokens: usage.CacheWriteTokens,
			OutputTokens: usage.OutputTokens, CompletionTokens: usage.CompletionTokens,
			ReasoningTokens: usage.ReasoningTokens, WebSearchRequests: usage.WebSearchRequests,
			WebSearchEngine: usage.WebSearchEngine,
		})
		if err != nil {
			attributed.UsageError = err.Error()
			usage = zeroruntime.Usage{}
		} else {
			usage = normalized
		}
	}
	cost := agent.UsageCostEstimate{}
	if attributed.UsageError != "" {
		cost = agent.UsageCostEstimate{Status: agent.CostStatusError, UnpricedReason: attributed.UsageError}
	} else {
		// Same cost rule as the pipeline ledger (splicerun.ResolveRequestCost),
		// so one request gets one cost on both paths.
		priced := attributed
		priced.Usage = usage
		cost = splicerun.ResolveRequestCostFor(nil, l.served, priced)
	}
	if cost.Status == "" {
		cost.Status = agent.CostStatusUnpriced
		cost.UnpricedReason = "cost estimator returned no status"
	}
	record := schemas.PipelineUsageRecord{
		Sequence:          len(l.records) + 1,
		Provider:          attributed.ProviderName,
		Model:             attributed.Model,
		Stage:             "agent_loop",
		Iteration:         maxInt(attributed.Iteration, 1),
		SpendSource:       attributed.SpendSource,
		UsageReported:     attributed.UsageReported,
		InputTokens:       usage.EffectiveInputTokens(),
		OutputTokens:      usage.EffectiveOutputTokens(),
		CachedTokens:      usage.CachedInputTokens,
		CacheWrite:        usage.CacheWriteTokens,
		Reasoning:         usage.ReasoningTokens,
		WebSearchRequests: usage.WebSearchRequests,
		WebSearchEngine:   usage.WebSearchEngine,
		CostStatus:        cost.Status,
		CostProvenance:    cost.Provenance,
		PricingSource:     cost.PricingSource,
		PricingAsOf:       cost.PricingAsOf,
		UnpricedReason:    cost.UnpricedReason,
		RequestID:         attributed.RequestID,
		PromptLayoutHash:  attributed.PromptLayoutHash,
		MemoryPosition:    schemas.MemoryPositionAfterPrefix,
	}
	if record.SpendSource == "" {
		record.SpendSource = schemas.SpendSourceGeneration
	}
	if cost.CostUSD != nil {
		value := *cost.CostUSD
		record.CostUSD = &value
	}
	l.records = append(l.records, record)
}

func (l *loopUsageLedger) result(runID, model, provider, answer, memoryStatus string, status string) schemas.PipelineResult {
	if status == "" {
		status = "completed"
	}
	result := schemas.PipelineResult{
		RunID:        runID,
		Status:       status,
		Tier:         schemas.TierStandard,
		Stages:       []schemas.StageRecord{{Name: "agent_loop", Status: stageStatus(status), Iteration: 1, Provider: stringPtr(provider), Model: stringPtr(model), OutputSummary: stringPtr(answer)}},
		UsageRecords: append([]schemas.PipelineUsageRecord(nil), l.records...),
		FinalOutput: map[string]interface{}{
			"answer":        answer,
			"MemoryStatus":  memoryStatus,
			"memory_status": memoryStatus,
		},
	}
	for _, record := range l.records {
		result.TotalTokensInput += record.InputTokens
		result.TotalTokensOutput += record.OutputTokens
		result.TotalTokensCached += record.CachedTokens
		result.TotalTokensCacheWrite += record.CacheWrite
		result.TotalTokensReasoning += record.Reasoning
		result.Stages[0].TokensInput += record.InputTokens
		result.Stages[0].TokensOutput += record.OutputTokens
		result.Stages[0].TokensCached += record.CachedTokens
		result.Stages[0].TokensCacheWrite += record.CacheWrite
		result.Stages[0].TokensReasoning += record.Reasoning
		if record.CostUSD != nil {
			result.TotalCostUSD += *record.CostUSD
			result.Stages[0].CostUSD += *record.CostUSD
		}
		switch record.CostStatus {
		case schemas.CostStatusPriced:
			result.PricedRequestCount++
		case schemas.CostStatusUnpriced:
			result.UnpricedRequestCount++
		case schemas.CostStatusError:
			result.ErrorRequestCount++
		}
	}
	switch {
	case len(l.records) == 0:
		result.CostCoverage = schemas.CostCoverageNotApplicable
	case result.PricedRequestCount == len(l.records):
		result.CostCoverage = schemas.CostCoverageComplete
	case result.PricedRequestCount > 0:
		result.CostCoverage = schemas.CostCoveragePartial
	default:
		result.CostCoverage = schemas.CostCoverageUnavailable
	}
	return result
}

func stageStatus(status string) schemas.StageStatus {
	if status == "completed" {
		return schemas.StageCompleted
	}
	return schemas.StageFailed
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func validateLoopResult(result schemas.PipelineResult) error {
	if err := result.Validate(); err != nil {
		return fmt.Errorf("loop pipeline result: %w", err)
	}
	return nil
}
