package schemas

import (
	"errors"
	"fmt"
	"math"
)

// PipelineTier is a request complexity tier.
type PipelineTier string

const (
	TierTrivial       PipelineTier = "trivial"
	TierLight         PipelineTier = "light"
	TierStandard      PipelineTier = "standard"
	TierSubstantial   PipelineTier = "substantial"
	TierArchitectural PipelineTier = "architectural"
)

// StageStatus is the execution status of a stage.
type StageStatus string

const (
	StagePending   StageStatus = "pending"
	StageRunning   StageStatus = "running"
	StageCompleted StageStatus = "completed"
	StageFailed    StageStatus = "failed"
	StageSkipped   StageStatus = "skipped"
	// StageIncomplete marks a deterministic verification stage that ran and
	// produced a valid VerificationReport with status incomplete: a required
	// check could not execute (tool missing, unsupported language profile,
	// timeout). It is not a failure, so it does not trigger another iteration,
	// but the completion summary and TUI surface the missing coverage.
	StageIncomplete StageStatus = "incomplete"
)

// ContextQueryType is a deterministic context retrieval operation.
type ContextQueryType string

const (
	ContextListFiles  ContextQueryType = "list_files"
	ContextReadFile   ContextQueryType = "read_file"
	ContextOutline    ContextQueryType = "outline"
	ContextSearch     ContextQueryType = "search"
	ContextFindSymbol ContextQueryType = "find_symbol"
	ContextGetSymbol  ContextQueryType = "get_symbol"
)

// RiskDomain is a risk area detected during classification.
type RiskDomain string

const (
	RiskAuth           RiskDomain = "auth"
	RiskData           RiskDomain = "data"
	RiskDependencies   RiskDomain = "dependencies"
	RiskDocumentation  RiskDomain = "documentation"
	RiskInfrastructure RiskDomain = "infrastructure"
	RiskSecurity       RiskDomain = "security"
	RiskTests          RiskDomain = "tests"
	RiskUI             RiskDomain = "ui"
	RiskUnknown        RiskDomain = "unknown"
)

// DesignIntensity describes how much design-phase attention a request needs.
type DesignIntensity string

const (
	DesignNone  DesignIntensity = "none"
	DesignLight DesignIntensity = "light"
	DesignFull  DesignIntensity = "full"
)

// ComplexityClassifierInput is typed input for request complexity classification.
type ComplexityClassifierInput struct {
	Request string `json:"request"`
}

// Validate checks the classifier input.
func (c ComplexityClassifierInput) Validate() error {
	if c.Request == "" {
		return errors.New("request is required")
	}
	return nil
}

// ComplexityClassifierOutput is an auditable classification used by the planner.
type ComplexityClassifierOutput struct {
	Tier                PipelineTier    `json:"tier"`
	Rationale           string          `json:"rationale"`
	Confidence          float64         `json:"confidence"`
	DetectedRiskDomains []RiskDomain    `json:"detected_risk_domains,omitempty"`
	DesignIntensity     DesignIntensity `json:"design_intensity"`
}

// Validate checks the classifier output.
func (c ComplexityClassifierOutput) Validate() error {
	switch c.Tier {
	case TierTrivial, TierLight, TierStandard, TierSubstantial, TierArchitectural:
	default:
		return fmt.Errorf("invalid tier %q", c.Tier)
	}
	if c.Rationale == "" {
		return errors.New("rationale is required")
	}
	if len(c.Rationale) > 500 {
		return errors.New("rationale must be <= 500 chars")
	}
	if err := validateConfidence(c.Confidence); err != nil {
		return err
	}
	switch c.DesignIntensity {
	case "", DesignNone, DesignLight, DesignFull:
	default:
		return fmt.Errorf("invalid design_intensity %q", c.DesignIntensity)
	}
	return nil
}

// ContextQuery is one bounded context query requested by a harness agent.
type ContextQuery struct {
	QueryType  ContextQueryType `json:"query_type"`
	Path       *string          `json:"path,omitempty"`
	Pattern    *string          `json:"pattern,omitempty"`
	Symbol     *string          `json:"symbol,omitempty"`
	Regex      bool             `json:"regex"`
	MaxResults int              `json:"max_results"`
	MaxChars   int              `json:"max_chars"`
}

// Validate checks that the query type has the required fields.
func (c ContextQuery) Validate() error {
	if c.Path != nil && *c.Path == "" {
		return errors.New("path must not be empty when provided")
	}
	if c.Pattern != nil && *c.Pattern == "" {
		return errors.New("pattern must not be empty when provided")
	}
	if c.Symbol != nil && *c.Symbol == "" {
		return errors.New("symbol must not be empty when provided")
	}
	switch c.QueryType {
	case ContextReadFile, ContextOutline:
		if c.Path == nil || *c.Path == "" {
			return errors.New("read_file/outline requires path")
		}
	case ContextSearch:
		if c.Pattern == nil || *c.Pattern == "" {
			return errors.New("search requires pattern")
		}
	case ContextFindSymbol, ContextGetSymbol:
		if c.Symbol == nil || *c.Symbol == "" {
			return errors.New("find_symbol/get_symbol requires symbol")
		}
	case ContextListFiles:
	default:
		return fmt.Errorf("invalid query type %q", c.QueryType)
	}
	if c.MaxResults < 1 || c.MaxResults > 200 {
		return errors.New("max_results must be between 1 and 200")
	}
	if c.MaxChars < 1 || c.MaxChars > 20000 {
		return errors.New("max_chars must be between 1 and 20000")
	}
	return nil
}

// ContextRequest is a bounded pull-channel request emitted by an agent.
type ContextRequest struct {
	Reason  string         `json:"reason"`
	Queries []ContextQuery `json:"queries"`
}

// Validate checks the context request.
func (c ContextRequest) Validate() error {
	if c.Reason == "" {
		return errors.New("reason is required")
	}
	if len(c.Queries) == 0 {
		return errors.New("at least one query is required")
	}
	for i, q := range c.Queries {
		if err := q.Validate(); err != nil {
			return fmt.Errorf("queries[%d]: %w", i, err)
		}
	}
	return nil
}

// ContextItem is one deterministic context result returned to a harness agent.
type ContextItem struct {
	Query     ContextQuery           `json:"query"`
	Summary   string                 `json:"summary"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
	Truncated bool                   `json:"truncated"`
	Error     *string                `json:"error,omitempty"`
}

// Validate checks the context item.
func (c ContextItem) Validate() error {
	if err := c.Query.Validate(); err != nil {
		return err
	}
	if c.Summary == "" {
		return errors.New("summary is required")
	}
	return nil
}

// ContextBundle is fulfilled context request injected into the next stage invocation.
type ContextBundle struct {
	Request ContextRequest `json:"request"`
	Items   []ContextItem  `json:"items,omitempty"`
}

// Validate checks the context bundle.
func (c ContextBundle) Validate() error {
	if err := c.Request.Validate(); err != nil {
		return err
	}
	for i, item := range c.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
	}
	return nil
}

// StageBudget is a token budget for a single stage.
type StageBudget struct {
	InputMax  int  `json:"input_max"`
	OutputMax int  `json:"output_max"`
	Skippable bool `json:"skippable"`
}

// Validate checks the stage budget.
//
// F14a: a stage budget is valid in exactly one of two shapes:
//
//   - Deterministic zero budget: InputMax == 0 and OutputMax == 0. Used by
//     model-free stages (static analysis, security audit, test execution) so
//     plan totals stop reserving fictional model tokens.
//   - Model-backed budget: both InputMax and OutputMax are > 0.
//
// Mixed or partial-zero forms (one value zero and the other positive) are
// invalid because they would reserve tokens for a stage that cannot spend
// them, or spend tokens on a stage that claims to be deterministic.
func (s StageBudget) Validate() error {
	isZero := s.InputMax == 0 && s.OutputMax == 0
	if isZero {
		return nil
	}
	if s.InputMax <= 0 || s.OutputMax <= 0 {
		return errors.New("input_max and output_max must both be > 0 for a model-backed budget")
	}
	return nil
}

// TokenBudget is the whole-run token budget.
type TokenBudget struct {
	TotalInputBudget  int                    `json:"total_input_budget"`
	TotalOutputBudget int                    `json:"total_output_budget"`
	PerStage          map[string]StageBudget `json:"per_stage"`
	Reserve           int                    `json:"reserve"`
	OverflowPolicy    string                 `json:"overflow_policy"`
}

// Validate checks the token budget.
func (t TokenBudget) Validate() error {
	if t.TotalInputBudget <= 0 {
		return errors.New("total_input_budget must be > 0")
	}
	if t.TotalOutputBudget <= 0 {
		return errors.New("total_output_budget must be > 0")
	}
	if t.Reserve < 0 {
		return errors.New("reserve must be >= 0")
	}
	switch t.OverflowPolicy {
	case "abort", "downgrade", "trim":
	default:
		return fmt.Errorf("invalid overflow_policy %q", t.OverflowPolicy)
	}
	for name, budget := range t.PerStage {
		if err := budget.Validate(); err != nil {
			return fmt.Errorf("per_stage[%s]: %w", name, err)
		}
	}
	return nil
}

// ExecutionStage is one planned pipeline stage.
type ExecutionStage struct {
	Name   string      `json:"name"`
	Budget StageBudget `json:"budget"`
}

// Validate checks the execution stage.
func (e ExecutionStage) Validate() error {
	if e.Name == "" {
		return errors.New("stage name is required")
	}
	return e.Budget.Validate()
}

// ExecutionPlan is the DAG plan produced by the orchestrator.
type ExecutionPlan struct {
	Tier                   PipelineTier     `json:"tier"`
	RequestIntent          string           `json:"request_intent"`
	Stages                 []ExecutionStage `json:"stages"`
	TokenBudget            TokenBudget      `json:"token_budget"`
	AcceptanceFacts        []AcceptanceFact `json:"acceptance_facts,omitempty"`
	RequiredKnowledgeFiles []string         `json:"required_knowledge_files,omitempty"`
}

// Validate checks the execution plan.
func (e ExecutionPlan) Validate() error {
	if err := e.TokenBudget.Validate(); err != nil {
		return err
	}
	if e.RequestIntent == "" {
		return errors.New("request_intent is required")
	}
	switch e.Tier {
	case TierTrivial, TierLight, TierStandard, TierSubstantial, TierArchitectural:
	default:
		return fmt.Errorf("invalid tier %q", e.Tier)
	}
	if len(e.Stages) == 0 {
		return errors.New("at least one stage is required")
	}
	for i, fact := range e.AcceptanceFacts {
		if err := fact.Validate(); err != nil {
			return fmt.Errorf("acceptance_facts[%d]: %w", i, err)
		}
	}
	stageNames := make(map[string]struct{}, len(e.Stages))
	for i, stage := range e.Stages {
		if err := stage.Validate(); err != nil {
			return fmt.Errorf("stages[%d]: %w", i, err)
		}
		if _, exists := stageNames[stage.Name]; exists {
			return fmt.Errorf("duplicate stage name %q", stage.Name)
		}
		stageNames[stage.Name] = struct{}{}
	}
	return nil
}

// StageRecord is a persistable summary of an executed stage.
type StageRecord struct {
	Name              string      `json:"name"`
	Status            StageStatus `json:"status"`
	Iteration         int         `json:"iteration"`
	Provider          *string     `json:"provider,omitempty"`
	Model             *string     `json:"model,omitempty"`
	Confidence        *float64    `json:"confidence,omitempty"`
	OutputSummary     *string     `json:"output_summary,omitempty"`
	Activity          *string     `json:"activity,omitempty"`
	TokensInput       int         `json:"tokens_input"`
	TokensOutput      int         `json:"tokens_output"`
	TokensCached      int         `json:"tokens_cached"`
	TokensCacheWrite  int         `json:"tokens_cache_write"`
	TokensReasoning   int         `json:"tokens_reasoning"`
	WebSearchRequests int         `json:"web_search_requests"`
	WebSearchEngine   string      `json:"web_search_engine,omitempty"`
	CostUSD           float64     `json:"cost_usd"`
	LatencyMs         int         `json:"latency_ms"`
	CommitSHA         *string     `json:"commit_sha,omitempty"`
}

// Validate checks the stage record.
func (s StageRecord) Validate() error {
	if s.Name == "" {
		return errors.New("name is required")
	}
	switch s.Status {
	case StagePending, StageRunning, StageCompleted, StageFailed, StageSkipped, StageIncomplete:
	default:
		return fmt.Errorf("invalid status %q", s.Status)
	}
	if s.Iteration < 0 {
		return errors.New("iteration must be >= 0")
	}
	if s.Confidence != nil {
		if err := validateConfidence(*s.Confidence); err != nil {
			return err
		}
	}
	if s.TokensInput < 0 || s.TokensOutput < 0 || s.TokensCached < 0 || s.TokensCacheWrite < 0 || s.TokensReasoning < 0 {
		return errors.New("token counts must be non-negative")
	}
	if s.WebSearchRequests < 0 {
		return errors.New("web search requests must be non-negative")
	}
	if s.TokensCached > s.TokensInput || s.TokensCacheWrite > s.TokensInput-s.TokensCached {
		return errors.New("cached and cache-write tokens must be disjoint input subsets")
	}
	if s.TokensReasoning > s.TokensOutput {
		return errors.New("reasoning tokens must be an output subset")
	}
	if math.IsNaN(s.CostUSD) || math.IsInf(s.CostUSD, 0) || s.CostUSD < 0 {
		return errors.New("cost_usd must be finite and non-negative")
	}
	if s.LatencyMs < 0 {
		return errors.New("latency_ms must be non-negative")
	}
	return nil
}

// HarnessStageInput is minimal typed input passed through the harness runner.
type HarnessStageInput struct {
	RunID           string            `json:"run_id"`
	StageName       string            `json:"stage_name"`
	Sequence        int               `json:"sequence"`
	PlanTier        PipelineTier      `json:"plan_tier"`
	RequestIntent   string            `json:"request_intent"`
	AcceptanceFacts []AcceptanceFact  `json:"acceptance_facts,omitempty"`
	PriorSummaries  map[string]string `json:"prior_summaries,omitempty"`
	// PriorChangedFiles carries structured paths from completed earlier stages.
	// It complements PriorSummaries, which remains the prose hand-off channel.
	PriorChangedFiles map[string][]string `json:"prior_changed_files,omitempty"`
	RevisionContext   *string             `json:"revision_context,omitempty"`
	Context           *ContextBundle      `json:"context,omitempty"`
	MemoryBundle      *MemoryBundle       `json:"memory_bundle,omitempty"`
	// PipelineStages is the full ordered roster of stage names for this run,
	// so a stage can see what will (and will not) run after it. Empty outside
	// a tier pipeline (e.g. design-phase stages), where no roster applies.
	PipelineStages []string `json:"pipeline_stages,omitempty"`
	// NextStage is the stage that consumes this stage's output, derivable as
	// PipelineStages[Sequence] (0-indexed) — included directly so the common
	// case needs no lookup. Empty when this is the last stage, or when
	// PipelineStages is empty.
	NextStage string `json:"next_stage,omitempty"`
}

// Validate checks the harness stage input.
func (h HarnessStageInput) Validate() error {
	if h.RunID == "" {
		return errors.New("run_id is required")
	}
	if h.StageName == "" {
		return errors.New("stage_name is required")
	}
	if h.Sequence < 1 {
		return errors.New("sequence must be >= 1")
	}
	if h.RequestIntent == "" {
		return errors.New("request_intent is required")
	}
	for i, fact := range h.AcceptanceFacts {
		if err := fact.Validate(); err != nil {
			return fmt.Errorf("acceptance_facts[%d]: %w", i, err)
		}
	}
	switch h.PlanTier {
	case TierTrivial, TierLight, TierStandard, TierSubstantial, TierArchitectural:
	default:
		return fmt.Errorf("invalid plan_tier %q", h.PlanTier)
	}
	if h.Context != nil {
		if err := h.Context.Validate(); err != nil {
			return err
		}
	}
	if h.MemoryBundle != nil {
		if err := h.MemoryBundle.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// StageUsage is the typed token ledger a stage reports to the orchestrator.
// The orchestrator copies it into StageRecord. Token values also contribute to
// PipelineResult totals.
type StageUsage struct {
	InputTokens       int    `json:"input_tokens"`
	OutputTokens      int    `json:"output_tokens"`
	CachedInputTokens int    `json:"cached_input_tokens"`
	CacheWriteTokens  int    `json:"cache_write_tokens"`
	ReasoningTokens   int    `json:"reasoning_tokens"`
	WebSearchRequests int    `json:"web_search_requests"`
	WebSearchEngine   string `json:"web_search_engine,omitempty"`
}

// HarnessStageOutput is minimal typed output returned by harness agents.
type HarnessStageOutput struct {
	Summary        string                 `json:"summary"`
	Detail         string                 `json:"detail"`
	Confidence     float64                `json:"confidence"`
	Data           map[string]interface{} `json:"data,omitempty"`
	ContextRequest *ContextRequest        `json:"context_request,omitempty"`
	Usage          *StageUsage            `json:"-"`
}

// Validate checks the harness stage output.
func (h HarnessStageOutput) Validate() error {
	if h.Summary == "" {
		return errors.New("summary is required")
	}
	if err := validateConfidence(h.Confidence); err != nil {
		return err
	}
	if h.ContextRequest != nil {
		if err := h.ContextRequest.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Cost coverage states for aggregate request pricing.
const (
	CostCoverageComplete      = "complete"
	CostCoveragePartial       = "partial"
	CostCoverageUnavailable   = "unavailable"
	CostCoverageNotApplicable = "not_applicable"

	CostStatusPriced   = "priced"
	CostStatusUnpriced = "unpriced"
	CostStatusError    = "error"
)

// PipelineUsageRecord is one provider request priced at the orchestrator ledger.
type PipelineUsageRecord struct {
	Sequence          int      `json:"sequence"`
	Provider          string   `json:"provider,omitempty"`
	Model             string   `json:"model,omitempty"`
	Stage             string   `json:"stage"`
	Iteration         int      `json:"iteration"`
	UsageReported     bool     `json:"usage_reported"`
	InputTokens       int      `json:"input_tokens"`
	OutputTokens      int      `json:"output_tokens"`
	CachedTokens      int      `json:"cached_input_tokens"`
	CacheWrite        int      `json:"cache_write_tokens"`
	Reasoning         int      `json:"reasoning_tokens"`
	WebSearchRequests int      `json:"web_search_requests"`
	WebSearchEngine   string   `json:"web_search_engine,omitempty"`
	CostUSD           *float64 `json:"cost_usd,omitempty"`
	CostStatus        string   `json:"cost_status"`
	CostProvenance    string   `json:"cost_provenance,omitempty"`
	PricingSource     string   `json:"pricing_source,omitempty"`
	PricingAsOf       string   `json:"pricing_as_of,omitempty"`
	UnpricedReason    string   `json:"unpriced_reason,omitempty"`
}

// Validate checks the pipeline usage record.
func (r PipelineUsageRecord) Validate() error {
	if r.Sequence < 1 {
		return errors.New("sequence must be >= 1")
	}
	if r.Stage == "" {
		return errors.New("stage is required")
	}
	if r.Iteration < 1 {
		return errors.New("iteration must be >= 1")
	}
	if r.InputTokens < 0 || r.OutputTokens < 0 || r.CachedTokens < 0 || r.CacheWrite < 0 || r.Reasoning < 0 {
		return errors.New("token counts must be non-negative")
	}
	if r.WebSearchRequests < 0 {
		return errors.New("web search requests must be non-negative")
	}
	if r.CachedTokens > r.InputTokens {
		return fmt.Errorf("cached input tokens %d exceeds input tokens %d", r.CachedTokens, r.InputTokens)
	}
	if r.CacheWrite > r.InputTokens-r.CachedTokens {
		return fmt.Errorf("cache write tokens %d plus cached input tokens %d exceeds input tokens %d", r.CacheWrite, r.CachedTokens, r.InputTokens)
	}
	if r.Reasoning > r.OutputTokens {
		return fmt.Errorf("reasoning tokens %d exceeds output tokens %d", r.Reasoning, r.OutputTokens)
	}
	if !r.UsageReported {
		if r.InputTokens != 0 || r.OutputTokens != 0 || r.CachedTokens != 0 || r.CacheWrite != 0 || r.Reasoning != 0 || r.WebSearchRequests != 0 {
			return errors.New("usage_reported false requires zero usage counts")
		}
		if r.CostStatus != CostStatusUnpriced {
			return errors.New("usage_reported false requires unpriced cost status")
		}
	}
	switch r.CostStatus {
	case CostStatusPriced:
		if !r.UsageReported {
			return errors.New("priced record requires reported usage")
		}
		if r.CostUSD == nil || math.IsNaN(*r.CostUSD) || math.IsInf(*r.CostUSD, 0) || *r.CostUSD < 0 {
			return errors.New("priced record requires a finite non-negative cost_usd")
		}
		switch r.CostProvenance {
		case "runtime_estimate", "persisted_estimate", "reconstructed_estimate", "reported":
		default:
			return fmt.Errorf("invalid cost_provenance %q", r.CostProvenance)
		}
		if r.PricingSource == "" || r.PricingAsOf == "" {
			return errors.New("priced record requires pricing_source and pricing_as_of")
		}
		if r.UnpricedReason != "" {
			return errors.New("priced record must not have unpriced_reason")
		}
	case CostStatusUnpriced, CostStatusError:
		if r.CostUSD != nil {
			return fmt.Errorf("%s record must not have cost_usd", r.CostStatus)
		}
		if r.CostProvenance != "" || r.PricingSource != "" || r.PricingAsOf != "" {
			return fmt.Errorf("%s record must not have pricing provenance", r.CostStatus)
		}
		if r.UnpricedReason == "" {
			return fmt.Errorf("%s record must have unpriced_reason", r.CostStatus)
		}
	default:
		return fmt.Errorf("invalid cost_status %q", r.CostStatus)
	}
	return nil
}

// PipelineResult is the final pipeline result returned by the CLI.
type PipelineResult struct {
	RunID                 string                 `json:"run_id"`
	Status                string                 `json:"status"`
	Tier                  PipelineTier           `json:"tier"`
	Stages                []StageRecord          `json:"stages"`
	FinalOutput           map[string]interface{} `json:"final_output,omitempty"`
	TotalCostUSD          float64                `json:"total_cost_usd"`
	TotalTokensInput      int                    `json:"total_tokens_input"`
	TotalTokensOutput     int                    `json:"total_tokens_output"`
	TotalTokensCached     int                    `json:"total_tokens_cached"`
	TotalTokensCacheWrite int                    `json:"total_tokens_cache_write"`
	TotalTokensReasoning  int                    `json:"total_tokens_reasoning"`
	UsageRecords          []PipelineUsageRecord  `json:"usage_records,omitempty"`
	CostCoverage          string                 `json:"cost_coverage,omitempty"`
	PricedRequestCount    int                    `json:"priced_request_count"`
	UnpricedRequestCount  int                    `json:"unpriced_request_count"`
	ErrorRequestCount     int                    `json:"error_request_count"`
	AbortReason           *string                `json:"abort_reason,omitempty"`
	MergeStatus           *string                `json:"merge_status,omitempty"`
	MergeBranch           *string                `json:"merge_branch,omitempty"`
	MergeCommitSHA        *string                `json:"merge_commit_sha,omitempty"`
	MergeMessage          *string                `json:"merge_message,omitempty"`
}

// Validate checks the pipeline result.
func (p PipelineResult) Validate() error {
	if p.RunID == "" {
		return errors.New("run_id is required")
	}
	switch p.Status {
	case "completed", "failed", "aborted":
	default:
		return fmt.Errorf("invalid status %q", p.Status)
	}
	switch p.Tier {
	case TierTrivial, TierLight, TierStandard, TierSubstantial, TierArchitectural:
	default:
		return fmt.Errorf("invalid tier %q", p.Tier)
	}
	for i, stage := range p.Stages {
		if err := stage.Validate(); err != nil {
			return fmt.Errorf("stages[%d]: %w", i, err)
		}
	}
	if math.IsNaN(p.TotalCostUSD) || math.IsInf(p.TotalCostUSD, 0) || p.TotalCostUSD < 0 {
		return errors.New("total_cost_usd must be finite and non-negative")
	}
	if p.TotalTokensInput < 0 || p.TotalTokensOutput < 0 || p.TotalTokensCached < 0 || p.TotalTokensCacheWrite < 0 || p.TotalTokensReasoning < 0 {
		return errors.New("token counts must be non-negative")
	}
	if p.MergeStatus != nil {
		switch *p.MergeStatus {
		case "not_needed", "merged", "skipped", "conflict", "error":
		default:
			return fmt.Errorf("invalid merge_status %q", *p.MergeStatus)
		}
	}
	if p.PricedRequestCount < 0 || p.UnpricedRequestCount < 0 || p.ErrorRequestCount < 0 {
		return errors.New("request counts must be non-negative")
	}
	var input, output, cached, cacheWrite, reasoning int
	var cost float64
	var priced, unpriced, costErrors int
	for i, record := range p.UsageRecords {
		if err := record.Validate(); err != nil {
			return fmt.Errorf("usage_records[%d]: %w", i, err)
		}
		if record.Sequence != i+1 {
			return fmt.Errorf("usage_records[%d] sequence %d must be %d", i, record.Sequence, i+1)
		}
		input += record.InputTokens
		output += record.OutputTokens
		cached += record.CachedTokens
		cacheWrite += record.CacheWrite
		reasoning += record.Reasoning
		if record.CostUSD != nil {
			cost += *record.CostUSD
		}
		switch record.CostStatus {
		case CostStatusPriced:
			priced++
		case CostStatusUnpriced:
			unpriced++
		case CostStatusError:
			costErrors++
		}
	}
	if priced != p.PricedRequestCount || unpriced != p.UnpricedRequestCount || costErrors != p.ErrorRequestCount || len(p.UsageRecords) != priced+unpriced+costErrors {
		return errors.New("request counts do not match usage records")
	}
	if input != p.TotalTokensInput || output != p.TotalTokensOutput || cached != p.TotalTokensCached || cacheWrite != p.TotalTokensCacheWrite || reasoning != p.TotalTokensReasoning {
		return errors.New("token totals do not match usage record sums")
	}
	if math.Abs(cost-p.TotalCostUSD) > 1e-12 {
		return errors.New("total_cost_usd does not match priced usage records")
	}
	wantCoverage := CostCoverageUnavailable
	switch {
	case len(p.UsageRecords) == 0:
		wantCoverage = CostCoverageNotApplicable
	case priced == len(p.UsageRecords):
		wantCoverage = CostCoverageComplete
	case priced > 0:
		wantCoverage = CostCoveragePartial
	}
	if p.CostCoverage != wantCoverage {
		return fmt.Errorf("cost_coverage %q must be %q", p.CostCoverage, wantCoverage)
	}
	return nil
}
