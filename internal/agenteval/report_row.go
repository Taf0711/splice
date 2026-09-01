package agenteval

import (
	"encoding/json"
	"strings"
)

// ReportContractRowVersion is the versioned contract for the unified
// per-attempt metrics row (evaluation handoff section 30). One row per
// rollout attempt; aggregations derive from rows, never replace them
// (section 31: raw first, summaries second).
const ReportContractRowVersion = "splice.eval.row.v1"

// ReportRow is the unified per-attempt record every eval runner folds into.
// It is deliberately flat: one JSON object per attempt loads into any
// analysis tool without joins. Fields a runner cannot know stay absent
// (omitempty), never fake-zero: unknown cost must remain unknown
// (section 30).
//
// Correctness and usage are separated so a suite can pass while wasting
// context, or save tokens while failing, without either fact hiding in an
// aggregate (section 4).
type ReportRow struct {
	Contract string `json:"contract"`

	// Identity and provenance (section 31): what ran, where, on what.
	SuiteID       string `json:"suiteId"`
	SuiteRevision string `json:"suiteRevision,omitempty"`
	TaskID        string `json:"taskId"`
	Attempt       int    `json:"attempt"`
	SpliceCommit  string `json:"spliceCommit,omitempty"`
	FixtureCommit string `json:"fixtureCommit,omitempty"`
	ModelID       string `json:"modelId,omitempty"`
	ProviderRoute string `json:"providerRoute,omitempty"`
	OSArch        string `json:"osArch,omitempty"`
	TimeoutSecs   int    `json:"timeoutSecs,omitempty"`
	Autonomy      string `json:"autonomy,omitempty"`
	StartedUnix   int64  `json:"startedUnix,omitempty"`

	// Correctness (section 30, correctness group).
	VerifiedSuccess bool     `json:"verified_success"`
	Status          Status   `json:"status"`
	VerifierResults []string `json:"verifier_results,omitempty"`
	// ForbiddenModifications counts forbidden-file violations observed.
	ForbiddenModifications int `json:"forbidden_modifications,omitempty"`
	// Taxonomy classifies the failure per section 33. Empty on success.
	Taxonomy string `json:"taxonomy,omitempty"`

	// Usage (section 30, usage group). Cached tokens ride along; the
	// reasoning split is reported when the provider exposes it.
	TokensInput      int `json:"tokens_input,omitempty"`
	TokensOutput     int `json:"tokens_output,omitempty"`
	TokensReasoning  int `json:"tokens_reasoning,omitempty"`
	TokensCached     int `json:"tokens_cached,omitempty"`
	TokensCacheWrite int `json:"tokens_cache_write,omitempty"`

	// Cost (section 30, cost group). Pointer so "unknown" is
	// distinguishable from free; the zero pointer serializes as absent.
	EstimatedCostUSD *float64 `json:"estimated_cost_usd,omitempty"`
	CostCoverage     string   `json:"cost_coverage,omitempty"`

	// Trajectory (section 30, trajectory group).
	ModelCalls        int64 `json:"model_calls,omitempty"`
	ToolCalls         int64 `json:"tool_calls,omitempty"`
	SearchCalls       int64 `json:"search_calls,omitempty"`
	FileReads         int64 `json:"file_reads,omitempty"`
	ShellCommands     int64 `json:"shell_commands,omitempty"`
	RepairLoops       int64 `json:"repair_loops,omitempty"`
	OrchestratorCalls int64 `json:"orchestrator_calls,omitempty"`

	// Cognition (section 30, cognition group). Keys are counts; key text
	// never lands here.
	KeysGenerated         int64 `json:"keys_generated,omitempty"`
	DirectLookupAttempts  int64 `json:"direct_lookup_attempts,omitempty"`
	DirectHits            int64 `json:"direct_hits,omitempty"`
	FreshHits             int64 `json:"fresh_hits,omitempty"`
	StaleHits             int64 `json:"stale_hits,omitempty"`
	UnknownHits           int64 `json:"unknown_hits,omitempty"`
	Misses                int64 `json:"misses,omitempty"`
	FTSFallbacks          int64 `json:"fts_fallbacks,omitempty"`
	ObservationsRetrieved int64 `json:"observations_retrieved,omitempty"`
	ObservationsAdmitted  int64 `json:"observations_admitted,omitempty"`
	ExemplarsRetrieved    int64 `json:"exemplars_retrieved,omitempty"`
	ExemplarsAdmitted     int64 `json:"exemplars_admitted,omitempty"`
	CognitionTokens       int64 `json:"cognition_tokens_delivered,omitempty"`

	// Learning (section 30, learning group). BudgetSource is "static" or
	// "learned"; BudgetAborts counts trajectory budget aborts.
	BudgetSource    string `json:"budget_source,omitempty"`
	DefaultBudgetIn int64  `json:"default_budget,omitempty"`
	LearnedBudgetIn int64  `json:"learned_budget,omitempty"`
	FitSampleCount  int64  `json:"fit_sample_count,omitempty"`
	BudgetAborts    int64  `json:"budget_aborts,omitempty"`

	// Timing (section 30, timing group). Milliseconds.
	TotalLatencyMs        int64 `json:"total_latency_ms,omitempty"`
	VerificationLatencyMs int64 `json:"verification_latency_ms,omitempty"`
	RetrievalLatencyMs    int64 `json:"retrieval_latency_ms,omitempty"`
	FreshnessLatencyMs    int64 `json:"freshness_latency_ms,omitempty"`
}

// Taxonomy constants (handoff section 33). The mapping is derived from the
// report's own evidence, never asserted by the model.
const (
	TaxonomyVerification   = "verification_failure"
	TaxonomyTimeout        = "timeout"
	TaxonomyInfrastructure = "infrastructure_failure"
	TaxonomyPermission     = "permission_failure"
	TaxonomyAgentError     = "agent_error"
)

// DeriveTaxonomy maps a report to its section 33 failure class. A passed
// report has no taxonomy. Blocked runs are infrastructure (the agent could
// not run at all), errors with a failing verifier command are verification
// failures, and bare errors are agent errors. Infrastructure-vs-task
// separation is the point: a benchmark bug must never silently count as a
// coding failure (section 33).
func DeriveTaxonomy(report Report) string {
	if report.OK {
		return ""
	}
	if report.Status == StatusBlocked {
		return TaxonomyInfrastructure
	}
	for _, result := range report.Results {
		if result.Kind == ResultCommand && result.Status == StatusFail {
			return TaxonomyVerification
		}
		if result.Kind == ResultCommand && result.Status == StatusError {
			return TaxonomyInfrastructure
		}
	}
	if report.Status == StatusError {
		return TaxonomyAgentError
	}
	return TaxonomyVerification
}

// RowFromReport folds an agenteval Report plus its run metadata into one
// ReportRow. Values the runner did not capture stay absent; nothing is
// defaulted to a fake zero.
func RowFromReport(report Report, meta RowMeta) ReportRow {
	row := ReportRow{
		Contract:        ReportContractRowVersion,
		SuiteID:         report.SuiteID,
		SuiteRevision:   meta.SuiteRevision,
		TaskID:          report.TaskID,
		Attempt:         meta.Attempt,
		SpliceCommit:    meta.SpliceCommit,
		FixtureCommit:   meta.FixtureCommit,
		ModelID:         meta.ModelID,
		ProviderRoute:   meta.ProviderRoute,
		OSArch:          meta.OSArch,
		TimeoutSecs:     meta.TimeoutSecs,
		Autonomy:        meta.Autonomy,
		StartedUnix:     meta.StartedUnix,
		VerifiedSuccess: report.OK,
		Status:          report.Status,
		Taxonomy:        DeriveTaxonomy(report),
	}
	for _, result := range report.Results {
		row.VerifierResults = append(row.VerifierResults, string(result.Status))
		if result.Kind == ResultChangedFiles && result.Status == StatusFail &&
			strings.Contains(strings.ToLower(result.ID), "forbidden") {
			row.ForbiddenModifications += len(result.UnexpectedFiles)
		}
	}
	// Usage: fold the collected samples deterministically. Reported tokens
	// are per-event; the last sample carrying totals wins, because providers
	// emit cumulative usage in stream-json. Absent samples stay absent.
	var last *UsageSample
	for i := range meta.UsageSamples {
		s := meta.UsageSamples[i]
		if s.InputTokens > 0 || s.OutputTokens > 0 {
			sample := s
			last = &sample
		}
	}
	if last != nil {
		row.TokensInput = last.InputTokens
		row.TokensOutput = last.OutputTokens
		row.TokensReasoning = last.ReasoningTokens
		row.TokensCached = last.CachedInputTokens
		row.TokensCacheWrite = last.CacheWriteTokens
		row.EstimatedCostUSD = last.CostUSD
		// Cost provenance rides along so unknown cost stays unknown and
		// priced cost names its source (section 30).
		row.CostCoverage = last.CostProvenance
		row.ModelCalls = int64(len(meta.UsageSamples))
	}
	row.TotalLatencyMs = meta.TotalLatencyMs
	row.VerificationLatencyMs = meta.VerificationLatencyMs
	return row
}

// RowMeta carries the run-level facts RowFromReport needs that the Report
// itself does not carry.
type RowMeta struct {
	SuiteRevision         string
	Attempt               int
	SpliceCommit          string
	FixtureCommit         string
	ModelID               string
	ProviderRoute         string
	OSArch                string
	TimeoutSecs           int
	Autonomy              string
	StartedUnix           int64
	UsageSamples          []UsageSample
	TotalLatencyMs        int64
	VerificationLatencyMs int64
}

// MarshalRow serializes one row for the JSONL attempt log.
func MarshalRow(row ReportRow) ([]byte, error) {
	return json.Marshal(row)
}

// RowContractMatches reports whether a row declares the current contract.
func RowContractMatches(data []byte) bool {
	var probe struct {
		Contract string `json:"contract"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return strings.HasPrefix(probe.Contract, "splice.eval.row.v")
}
