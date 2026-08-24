package v2

import (
	"fmt"
	"math"
	"sort"
)

// TrialStatus is the explicit outcome class of one attempt. Invalid and
// security-invalid attempts remain in the audit but do not enter efficacy
// estimates. Incomplete means required telemetry was not available.
type TrialStatus string

const (
	TrialValid           TrialStatus = "valid"
	TrialInvalid         TrialStatus = "invalid"
	TrialSecurityInvalid TrialStatus = "security_invalid"
	TrialIncomplete      TrialStatus = "incomplete"
)

const (
	FailureRuleInvalidation   = "invalidation"
	FailureRuleRetry          = "retry"
	FailureRuleStopping       = "stopping"
	FailureRuleSecurityReview = "security-review"
)

// ValidTrialStatus reports whether status is known.
func ValidTrialStatus(status TrialStatus) bool {
	switch status {
	case TrialValid, TrialInvalid, TrialSecurityInvalid, TrialIncomplete:
		return true
	default:
		return false
	}
}

// TelemetrySource records where the numbers came from. Absent telemetry is
// never zero-filled; it makes the attempt incomplete instead.
type TelemetrySource string

const (
	TelemetrySourceTrace          TelemetrySource = "trace_store"
	TelemetrySourceStreamFallback TelemetrySource = "validated_stream_fallback"
)

// ValidTelemetrySource reports whether source is known.
func ValidTelemetrySource(source TelemetrySource) bool {
	return source == TelemetrySourceTrace || source == TelemetrySourceStreamFallback
}

// PricingCoverage states how much of the known price list covers the cost.
type PricingCoverage string

const (
	PricingFull    PricingCoverage = "full"
	PricingPartial PricingCoverage = "partial"
	PricingNone    PricingCoverage = "none"
)

// ValidPricingCoverage reports whether coverage is known.
func ValidPricingCoverage(c PricingCoverage) bool {
	return c == PricingFull || c == PricingPartial || c == PricingNone
}

// TokenUsage carries canonical usage for one provider request. Every pointer
// is required, including explicit zero values. Cached input and cache write
// are disjoint input subsets. Reasoning is an output subset.
type TokenUsage struct {
	TotalTokens       *uint64 `json:"total_tokens"`
	InputTokens       *uint64 `json:"input_tokens"`
	CachedInputTokens *uint64 `json:"cached_input_tokens"`
	CacheWriteTokens  *uint64 `json:"cache_write_tokens"`
	OutputTokens      *uint64 `json:"output_tokens"`
	ReasoningTokens   *uint64 `json:"reasoning_tokens"`
}

// Validate checks the zeroruntime.NormalizeUsage arithmetic.
func (u TokenUsage) Validate() error {
	if u.TotalTokens == nil || u.InputTokens == nil || u.CachedInputTokens == nil ||
		u.CacheWriteTokens == nil || u.OutputTokens == nil || u.ReasoningTokens == nil {
		return fmt.Errorf("all token fields are required; missing telemetry is never zero-filled")
	}
	if *u.InputTokens > math.MaxUint64-*u.OutputTokens {
		return fmt.Errorf("input plus output token total overflows uint64")
	}
	wantTotal := *u.InputTokens + *u.OutputTokens
	if *u.TotalTokens != wantTotal {
		return fmt.Errorf("total tokens %d do not equal input plus output %d", *u.TotalTokens, wantTotal)
	}
	if *u.CachedInputTokens > *u.InputTokens {
		return fmt.Errorf("cached input tokens %d exceed input %d", *u.CachedInputTokens, *u.InputTokens)
	}
	if *u.CacheWriteTokens > *u.InputTokens-*u.CachedInputTokens {
		return fmt.Errorf("cache write tokens %d plus cached input tokens %d exceed input %d",
			*u.CacheWriteTokens, *u.CachedInputTokens, *u.InputTokens)
	}
	if *u.ReasoningTokens > *u.OutputTokens {
		return fmt.Errorf("reasoning tokens %d exceed output %d", *u.ReasoningTokens, *u.OutputTokens)
	}
	return nil
}

// ProviderRequestTelemetry records one model-backed request. Deterministic
// tool work is not part of usage, but its web-search requests and cost remain
// separate typed fields.
type ProviderRequestTelemetry struct {
	RequestID         string          `json:"request_id"`
	Stage             string          `json:"stage"`
	Usage             TokenUsage      `json:"usage"`
	WebSearchRequests *uint64         `json:"web_search_requests"`
	WebSearchEngine   string          `json:"web_search_engine,omitempty"`
	WallTimeMs        *int64          `json:"wall_time_ms"`
	ProviderCostUSD   *float64        `json:"provider_cost_usd"`
	WebSearchCostUSD  *float64        `json:"web_search_cost_usd"`
	PricingCoverage   PricingCoverage `json:"pricing_coverage"`
}

// Validate checks a provider request.
func (r ProviderRequestTelemetry) Validate() error {
	if r.RequestID == "" || r.Stage == "" {
		return fmt.Errorf("provider request needs request_id and stage")
	}
	if err := r.Usage.Validate(); err != nil {
		return fmt.Errorf("request %s usage: %w", r.RequestID, err)
	}
	if r.WebSearchRequests == nil || r.WallTimeMs == nil || r.ProviderCostUSD == nil || r.WebSearchCostUSD == nil {
		return fmt.Errorf("request %s is missing web-search, latency, or cost telemetry", r.RequestID)
	}
	if *r.WallTimeMs < 0 || *r.ProviderCostUSD < 0 || *r.WebSearchCostUSD < 0 ||
		!finite(*r.ProviderCostUSD) || !finite(*r.WebSearchCostUSD) {
		return fmt.Errorf("request %s has invalid latency or cost telemetry", r.RequestID)
	}
	if *r.WebSearchRequests > 0 && r.WebSearchEngine == "" {
		return fmt.Errorf("request %s needs web_search_engine when searches are nonzero", r.RequestID)
	}
	if !ValidPricingCoverage(r.PricingCoverage) {
		return fmt.Errorf("request %s has invalid pricing coverage %q", r.RequestID, r.PricingCoverage)
	}
	return nil
}

// StageFingerprints records the per-stage immutable prompt and execution
// schema identity.
type StageFingerprints struct {
	PromptSHA256   string `json:"prompt_sha256"`
	ToolSHA256     string `json:"tool_sha256"`
	SchemaSHA256   string `json:"schema_sha256"`
	TopologySHA256 string `json:"topology_sha256"`
	BudgetSHA256   string `json:"budget_sha256"`
}

// Validate checks all stage fingerprints.
func (f StageFingerprints) Validate() error {
	for name, value := range map[string]string{
		"prompt_sha256":   f.PromptSHA256,
		"tool_sha256":     f.ToolSHA256,
		"schema_sha256":   f.SchemaSHA256,
		"topology_sha256": f.TopologySHA256,
		"budget_sha256":   f.BudgetSHA256,
	} {
		if !validHash(value) {
			return fmt.Errorf("%s must be a sha256 hex digest", name)
		}
	}
	return nil
}

// StageTelemetry records all required telemetry for one stage.
type StageTelemetry struct {
	Route             StageRoute                 `json:"route"`
	Fingerprints      StageFingerprints          `json:"fingerprints"`
	Requests          []ProviderRequestTelemetry `json:"requests"`
	LatencyMs         *int64                     `json:"latency_ms"`
	ToolCallCount     *int                       `json:"tool_call_count"`
	PermissionCount   *int                       `json:"permission_count"`
	RepairCount       *int                       `json:"repair_count"`
	CompactionCalls   *int                       `json:"compaction_calls"`
	Abort             *bool                      `json:"abort"`
	InterventionCount *int                       `json:"intervention_count"`
}

// Validate checks stage completeness and unique request identities.
func (s StageTelemetry) Validate() error {
	if err := s.Route.Validate(); err != nil {
		return fmt.Errorf("route: %w", err)
	}
	if err := s.Fingerprints.Validate(); err != nil {
		return fmt.Errorf("fingerprints: %w", err)
	}
	if len(s.Requests) == 0 {
		return fmt.Errorf("stage %s has no provider requests", s.Route.Stage)
	}
	if s.LatencyMs == nil || s.ToolCallCount == nil || s.PermissionCount == nil ||
		s.RepairCount == nil || s.CompactionCalls == nil || s.Abort == nil || s.InterventionCount == nil {
		return fmt.Errorf("stage %s is missing operational telemetry", s.Route.Stage)
	}
	for name, value := range map[string]int{
		"latency_ms":         int(*s.LatencyMs),
		"tool_call_count":    *s.ToolCallCount,
		"permission_count":   *s.PermissionCount,
		"repair_count":       *s.RepairCount,
		"compaction_calls":   *s.CompactionCalls,
		"intervention_count": *s.InterventionCount,
	} {
		if value < 0 {
			return fmt.Errorf("stage %s: %s must be non-negative", s.Route.Stage, name)
		}
	}
	seen := make(map[string]bool, len(s.Requests))
	for i, request := range s.Requests {
		if err := request.Validate(); err != nil {
			return fmt.Errorf("requests[%d]: %w", i, err)
		}
		if seen[request.RequestID] {
			return fmt.Errorf("stage %s: duplicate request %q", s.Route.Stage, request.RequestID)
		}
		seen[request.RequestID] = true
		if request.Stage != s.Route.Stage {
			return fmt.Errorf("request %s stage %q does not match %q", request.RequestID, request.Stage, s.Route.Stage)
		}
	}
	return nil
}

// MemoryDisposition is the typed review record shared by all arms.
type MemoryDisposition struct {
	MemoryID string `json:"memory_id"`
	Decision string `json:"decision"` // accepted, rejected, or ignored
	Reason   string `json:"reason"`
}

// Validate checks a memory disposition.
func (d MemoryDisposition) Validate() error {
	if d.MemoryID == "" || d.Reason == "" {
		return fmt.Errorf("memory disposition needs memory_id and reason")
	}
	switch d.Decision {
	case "accepted", "rejected", "ignored":
		return nil
	default:
		return fmt.Errorf("memory %s has unknown decision %q", d.MemoryID, d.Decision)
	}
}

// TelemetryRecord is the complete per-attempt telemetry contract.
type TelemetryRecord struct {
	Source                         TelemetrySource     `json:"source"`
	RunID                          string              `json:"run_id"`
	SessionID                      string              `json:"session_id"`
	Stages                         []StageTelemetry    `json:"stages"`
	Tokens                         TokenUsage          `json:"tokens"`
	SelectedMemoryIDs              []string            `json:"selected_memory_ids"`
	DeliveredMemoryIDs             []string            `json:"delivered_memory_ids"`
	Dispositions                   []MemoryDisposition `json:"dispositions"`
	DispositionsComplete           *bool               `json:"dispositions_complete"`
	InvalidClaimCount              *int                `json:"invalid_claim_count"`
	DeterministicCheckPassed       *bool               `json:"deterministic_check_passed"`
	DeterministicCheckOutputSHA256 string              `json:"deterministic_check_output_sha256"`
	RetryCount                     *int                `json:"retry_count"`
	RepairCount                    *int                `json:"repair_count"`
	CompactionCalls                *int                `json:"compaction_calls"`
	WallTimeMs                     *int64              `json:"wall_time_ms"`
	MemoryQueryLatencyMs           *int64              `json:"memory_query_latency_ms"`
	ProviderCostUSD                *float64            `json:"provider_cost_usd"`
	WebSearchCostUSD               *float64            `json:"web_search_cost_usd"`
	WebSearchRequests              *uint64             `json:"web_search_requests"`
	WebSearchEngines               []string            `json:"web_search_engines"`
	PricingCoverage                PricingCoverage     `json:"pricing_coverage"`
}

// Validate enforces the 100 percent claim telemetry contract.
func (r TelemetryRecord) Validate() error {
	if !ValidTelemetrySource(r.Source) {
		return fmt.Errorf("invalid telemetry source %q", r.Source)
	}
	if r.RunID == "" || r.SessionID == "" {
		return fmt.Errorf("run_id and session_id are required")
	}
	if len(r.Stages) == 0 {
		return fmt.Errorf("stage telemetry is required")
	}
	if err := r.Tokens.Validate(); err != nil {
		return err
	}
	if r.DispositionsComplete == nil || !*r.DispositionsComplete {
		return fmt.Errorf("dispositions_complete must be true for claim telemetry")
	}
	if r.InvalidClaimCount == nil || *r.InvalidClaimCount < 0 {
		return fmt.Errorf("invalid_claim_count is required and must be non-negative")
	}
	if r.DeterministicCheckPassed == nil || !validHash(r.DeterministicCheckOutputSHA256) {
		return fmt.Errorf("deterministic check result and output hash are required")
	}
	for name, value := range map[string][]string{
		"selected_memory_ids":  r.SelectedMemoryIDs,
		"delivered_memory_ids": r.DeliveredMemoryIDs,
	} {
		if value == nil {
			return fmt.Errorf("%s must be present, even when empty", name)
		}
		if err := uniqueStrings(name, value); err != nil {
			return err
		}
	}
	if r.Dispositions == nil {
		return fmt.Errorf("dispositions must be present, even when empty")
	}
	for i, disposition := range r.Dispositions {
		if err := disposition.Validate(); err != nil {
			return fmt.Errorf("dispositions[%d]: %w", i, err)
		}
	}
	for name, value := range map[string]*int{
		"retry_count":      r.RetryCount,
		"repair_count":     r.RepairCount,
		"compaction_calls": r.CompactionCalls,
	} {
		if value == nil || *value < 0 {
			return fmt.Errorf("%s is required and must be non-negative", name)
		}
	}
	if r.WallTimeMs == nil || *r.WallTimeMs < 0 || r.MemoryQueryLatencyMs == nil || *r.MemoryQueryLatencyMs < 0 {
		return fmt.Errorf("wall time and memory-query latency are required and must be non-negative")
	}
	if r.ProviderCostUSD == nil || r.WebSearchCostUSD == nil || r.WebSearchRequests == nil ||
		*r.ProviderCostUSD < 0 || *r.WebSearchCostUSD < 0 ||
		!finite(*r.ProviderCostUSD) || !finite(*r.WebSearchCostUSD) {
		return fmt.Errorf("provider and web-search costs and request count are required and must be finite and non-negative")
	}
	if r.WebSearchEngines == nil {
		return fmt.Errorf("web_search_engines must be present, even when empty")
	}
	if err := uniqueStrings("web_search_engines", r.WebSearchEngines); err != nil {
		return err
	}
	for _, engine := range r.WebSearchEngines {
		if engine == "" {
			return fmt.Errorf("web_search_engines contains an empty engine")
		}
	}
	if *r.WebSearchRequests == 0 && len(r.WebSearchEngines) != 0 {
		return fmt.Errorf("web_search_engines must be empty when web_search_requests is zero")
	}
	if *r.WebSearchRequests > 0 && len(r.WebSearchEngines) == 0 {
		return fmt.Errorf("web_search_engines are required when web_search_requests is nonzero")
	}
	if !ValidPricingCoverage(r.PricingCoverage) {
		return fmt.Errorf("invalid pricing coverage %q", r.PricingCoverage)
	}
	seenStages := make(map[string]bool, len(r.Stages))
	var requestTotals TokenUsage
	var requestTotal, requestInput, requestCached, requestCacheWrite, requestOutput, requestReasoning uint64
	var repairTotal, compactionTotal uint64
	var requestWebSearchRequests uint64
	var requestProviderCost, requestWebSearchCost float64
	requestEngines := make(map[string]bool)
	coverage := PricingNone
	firstRequest := true
	for i, stage := range r.Stages {
		if err := stage.Validate(); err != nil {
			return fmt.Errorf("stages[%d]: %w", i, err)
		}
		if seenStages[stage.Route.Stage] {
			return fmt.Errorf("duplicate stage telemetry %q", stage.Route.Stage)
		}
		seenStages[stage.Route.Stage] = true
		for name, value := range map[string]*int{
			"repair_count":     stage.RepairCount,
			"compaction_calls": stage.CompactionCalls,
		} {
			var total *uint64
			if name == "repair_count" {
				total = &repairTotal
			} else {
				total = &compactionTotal
			}
			if *total > math.MaxUint64-uint64(*value) {
				return fmt.Errorf("stage %s %s aggregate overflows uint64", stage.Route.Stage, name)
			}
			*total += uint64(*value)
		}
		for _, request := range stage.Requests {
			u := request.Usage
			values := []*uint64{u.TotalTokens, u.InputTokens, u.CachedInputTokens, u.CacheWriteTokens, u.OutputTokens, u.ReasoningTokens}
			sums := []*uint64{&requestTotal, &requestInput, &requestCached, &requestCacheWrite, &requestOutput, &requestReasoning}
			for j, value := range values {
				if *sums[j] > math.MaxUint64-*value {
					return fmt.Errorf("provider request token aggregate overflows uint64")
				}
				*sums[j] += *value
			}
			if !finite(requestProviderCost+*request.ProviderCostUSD) || !finite(requestWebSearchCost+*request.WebSearchCostUSD) {
				return fmt.Errorf("provider cost aggregate is non-finite")
			}
			requestProviderCost += *request.ProviderCostUSD
			requestWebSearchCost += *request.WebSearchCostUSD
			if requestWebSearchRequests > math.MaxUint64-*request.WebSearchRequests {
				return fmt.Errorf("provider request web-search count aggregate overflows uint64")
			}
			requestWebSearchRequests += *request.WebSearchRequests
			if *request.WebSearchRequests > 0 {
				requestEngines[request.WebSearchEngine] = true
			}
			if firstRequest {
				coverage = request.PricingCoverage
				firstRequest = false
			} else if coverage != request.PricingCoverage {
				coverage = PricingPartial
			}
		}
	}
	requestTotals = TokenUsage{TotalTokens: &requestTotal, InputTokens: &requestInput,
		CachedInputTokens: &requestCached, CacheWriteTokens: &requestCacheWrite,
		OutputTokens: &requestOutput, ReasoningTokens: &requestReasoning}
	if err := equalUsage(r.Tokens, requestTotals); err != nil {
		return err
	}
	if !closeEnough(*r.ProviderCostUSD, requestProviderCost) {
		return fmt.Errorf("provider cost %.9f does not equal provider request sum %.9f", *r.ProviderCostUSD, requestProviderCost)
	}
	if !closeEnough(*r.WebSearchCostUSD, requestWebSearchCost) {
		return fmt.Errorf("web-search cost %.9f does not equal provider request sum %.9f", *r.WebSearchCostUSD, requestWebSearchCost)
	}
	if *r.WebSearchRequests != requestWebSearchRequests {
		return fmt.Errorf("aggregate web-search requests %d do not equal provider request sum %d", *r.WebSearchRequests, requestWebSearchRequests)
	}
	expectedEngines := make([]string, 0, len(requestEngines))
	for engine := range requestEngines {
		expectedEngines = append(expectedEngines, engine)
	}
	sort.Strings(expectedEngines)
	gotEngines := append([]string(nil), r.WebSearchEngines...)
	sort.Strings(gotEngines)
	if len(gotEngines) != len(expectedEngines) {
		return fmt.Errorf("web-search engine set has %d entries, want %d", len(gotEngines), len(expectedEngines))
	}
	for i := range expectedEngines {
		if gotEngines[i] != expectedEngines[i] {
			return fmt.Errorf("web-search engine %q does not match provider request engine %q", gotEngines[i], expectedEngines[i])
		}
	}
	if r.PricingCoverage != coverage {
		return fmt.Errorf("pricing coverage %q does not equal provider request coverage %q", r.PricingCoverage, coverage)
	}
	if uint64(*r.RepairCount) != repairTotal {
		return fmt.Errorf("repair_count %d does not equal stage aggregate %d", *r.RepairCount, repairTotal)
	}
	if uint64(*r.CompactionCalls) != compactionTotal {
		return fmt.Errorf("compaction_calls %d does not equal stage aggregate %d", *r.CompactionCalls, compactionTotal)
	}
	return nil
}

func equalUsage(got, want TokenUsage) error {
	for name, pair := range map[string][2]*uint64{
		"total_tokens":        {got.TotalTokens, want.TotalTokens},
		"input_tokens":        {got.InputTokens, want.InputTokens},
		"cached_input_tokens": {got.CachedInputTokens, want.CachedInputTokens},
		"cache_write_tokens":  {got.CacheWriteTokens, want.CacheWriteTokens},
		"output_tokens":       {got.OutputTokens, want.OutputTokens},
		"reasoning_tokens":    {got.ReasoningTokens, want.ReasoningTokens},
	} {
		if *pair[0] != *pair[1] {
			return fmt.Errorf("aggregate %s %d does not equal provider request sum %d", name, *pair[0], *pair[1])
		}
	}
	return nil
}

func closeEnough(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9*math.Max(1, math.Max(math.Abs(got), math.Abs(want)))
}

func validInfrastructureFailureRule(rule string) bool {
	return rule == FailureRuleInvalidation || rule == FailureRuleRetry || rule == FailureRuleStopping
}

func uniqueStrings(field string, values []string) error {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("%s contains an empty id", field)
		}
		if seen[value] {
			return fmt.Errorf("%s contains duplicate id %q", field, value)
		}
		seen[value] = true
	}
	return nil
}

// TrialResult is the recorded outcome of exactly one scheduled attempt.
type TrialResult struct {
	Key                            TrialKey         `json:"key"`
	Status                         TrialStatus      `json:"status"`
	FailureRuleID                  string           `json:"failure_rule_id,omitempty"`
	CheckPassed                    bool             `json:"check_passed"`
	DeterministicCheckOutputSHA256 string           `json:"deterministic_check_output_sha256"`
	Telemetry                      *TelemetryRecord `json:"telemetry,omitempty"`
	ChangedFileHashes              []NamedHash      `json:"changed_file_hashes"`
}

// ValidateFor checks the result against one protocol and its status class.
func (t TrialResult) ValidateFor(p Protocol) error {
	if err := t.Key.ValidateFor(p); err != nil {
		return err
	}
	return t.validate()
}

// Validate checks the result against its status class. It does not infer a
// protocol from the arm name, so use ValidateFor when protocol context exists.
func (t TrialResult) Validate() error {
	return t.validate()
}

func (t TrialResult) validate() error {
	if err := t.Key.Validate(); err != nil {
		return err
	}
	if !ValidTrialStatus(t.Status) {
		return fmt.Errorf("invalid trial status %q", t.Status)
	}
	if t.ChangedFileHashes == nil {
		return fmt.Errorf("changed_file_hashes must be present, even when empty")
	}
	seenFiles := make(map[string]bool, len(t.ChangedFileHashes))
	for i, h := range t.ChangedFileHashes {
		if err := h.Validate(); err != nil {
			return fmt.Errorf("changed_file_hashes[%d]: %w", i, err)
		}
		normalized, ok := normalizePath(h.Name)
		if !ok || normalized != h.Name {
			return fmt.Errorf("changed_file_hashes[%d] name %q must be a canonical relative workspace path", i, h.Name)
		}
		if seenFiles[normalized] {
			return fmt.Errorf("duplicate changed file hash %q", normalized)
		}
		seenFiles[normalized] = true
	}
	if t.Status != TrialValid && t.Telemetry != nil {
		if err := t.Telemetry.Validate(); err != nil {
			return fmt.Errorf("trial %s telemetry: %w", t.Key.String(), err)
		}
	}
	switch t.Status {
	case TrialInvalid:
		if !validInfrastructureFailureRule(t.FailureRuleID) {
			return fmt.Errorf("trial %s: invalid result needs an invalidation, retry, or stopping failure rule", t.Key.String())
		}
		if t.Telemetry != nil && !*t.Telemetry.DeterministicCheckPassed {
			return fmt.Errorf("trial %s: deterministic check failure is a valid product outcome, not infrastructure invalidity", t.Key.String())
		}
	case TrialIncomplete:
		if t.FailureRuleID != FailureRuleInvalidation {
			return fmt.Errorf("trial %s: incomplete result needs the invalidation failure rule", t.Key.String())
		}
		if t.Telemetry != nil && !*t.Telemetry.DeterministicCheckPassed {
			return fmt.Errorf("trial %s: deterministic check failure is a valid product outcome, not incompleteness", t.Key.String())
		}
	case TrialSecurityInvalid:
		if t.FailureRuleID != FailureRuleSecurityReview {
			return fmt.Errorf("trial %s: security-invalid result needs the security-review rule id", t.Key.String())
		}
		if t.Telemetry != nil && !*t.Telemetry.DeterministicCheckPassed {
			return fmt.Errorf("trial %s: deterministic check failure is a valid product outcome, not security invalidity", t.Key.String())
		}
	case TrialValid:
		if t.Telemetry == nil {
			return fmt.Errorf("trial %s: a valid result requires complete telemetry", t.Key.String())
		}
		if t.FailureRuleID != "" {
			return fmt.Errorf("trial %s: a valid result cannot carry a failure rule id", t.Key.String())
		}
		if !validHash(t.DeterministicCheckOutputSHA256) {
			return fmt.Errorf("trial %s: deterministic check output hash is required", t.Key.String())
		}
		if err := t.Telemetry.Validate(); err != nil {
			return fmt.Errorf("trial %s telemetry: %w", t.Key.String(), err)
		}
		if t.CheckPassed != *t.Telemetry.DeterministicCheckPassed {
			return fmt.Errorf("trial %s check_passed does not match telemetry deterministic_check_passed", t.Key.String())
		}
	}
	return nil
}
