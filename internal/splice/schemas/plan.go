package schemas

import (
	"errors"
	"fmt"
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
	InputMax  int    `json:"input_max"`
	OutputMax int    `json:"output_max"`
	ModelTier string `json:"model_tier"`
	Skippable bool   `json:"skippable"`
}

// Validate checks the stage budget.
//
// F14a: a stage budget is valid in exactly one of two shapes:
//
//   - Deterministic zero budget: InputMax == 0, OutputMax == 0, and an empty
//     ModelTier. Used by model-free stages (static analysis, security audit,
//     test execution) so plan totals stop reserving fictional model tokens.
//   - Model-backed budget: both InputMax and OutputMax are > 0, and ModelTier
//     names a currently valid model tier.
//
// Mixed or partial-zero forms (one value zero and the other positive), or any
// zero value paired with a non-empty model tier, are invalid because they
// would reserve tokens for a stage that cannot spend them, or spend tokens on
// a stage that claims to be deterministic.
func (s StageBudget) Validate() error {
	isZero := s.InputMax == 0 && s.OutputMax == 0
	if isZero {
		if s.ModelTier != "" {
			return errors.New("deterministic zero budget must not specify a model_tier")
		}
		return nil
	}
	if s.InputMax <= 0 || s.OutputMax <= 0 {
		return errors.New("input_max and output_max must both be > 0 for a model-backed budget")
	}
	switch s.ModelTier {
	case "nano", "small", "medium", "large", "reasoning":
	default:
		return fmt.Errorf("invalid model_tier %q", s.ModelTier)
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
	Name      string      `json:"name"`
	DependsOn []string    `json:"depends_on,omitempty"`
	Budget    StageBudget `json:"budget"`
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
	RequiredKnowledgeFiles []string         `json:"required_knowledge_files,omitempty"`
}

// Validate checks the execution plan and stage DAG integrity.
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
	deps := make(map[string][]string, len(e.Stages))
	for i, stage := range e.Stages {
		for _, dep := range stage.DependsOn {
			if _, exists := stageNames[dep]; !exists {
				return fmt.Errorf("stages[%d] depends_on unknown stage %q", i, dep)
			}
			deps[stage.Name] = append(deps[stage.Name], dep)
		}
	}
	if cycle := detectCycle(deps); cycle != "" {
		return fmt.Errorf("stage dependency cycle detected involving %q", cycle)
	}
	return nil
}

// detectCycle performs a DFS over the dependency graph and returns the name of
// the first stage that participates in a cycle, or "" if the graph is a DAG.
const (
	cycleStateUnvisited = 0
	cycleStateVisiting  = 1
	cycleStateVisited   = 2
)

func detectCycle(deps map[string][]string) string {
	state := make(map[string]int, len(deps))
	var visit func(name string) string
	visit = func(name string) string {
		if state[name] == cycleStateVisiting {
			return name
		}
		if state[name] == cycleStateVisited {
			return ""
		}
		state[name] = cycleStateVisiting
		for _, dep := range deps[name] {
			if found := visit(dep); found != "" {
				return found
			}
		}
		state[name] = cycleStateVisited
		return ""
	}
	for name := range deps {
		if found := visit(name); found != "" {
			return found
		}
	}
	return ""
}

// StageRecord is a persistable summary of an executed stage.
type StageRecord struct {
	Name             string      `json:"name"`
	Status           StageStatus `json:"status"`
	Iteration        int         `json:"iteration"`
	Provider         *string     `json:"provider,omitempty"`
	Model            *string     `json:"model,omitempty"`
	Confidence       *float64    `json:"confidence,omitempty"`
	OutputSummary    *string     `json:"output_summary,omitempty"`
	Activity         *string     `json:"activity,omitempty"`
	TokensInput      int         `json:"tokens_input"`
	TokensOutput     int         `json:"tokens_output"`
	TokensCached     int         `json:"tokens_cached"`
	TokensCacheWrite int         `json:"tokens_cache_write"`
	CostUSD          float64     `json:"cost_usd"`
	LatencyMs        int         `json:"latency_ms"`
	CommitSHA        *string     `json:"commit_sha,omitempty"`
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
	if s.TokensInput < 0 || s.TokensOutput < 0 || s.TokensCached < 0 || s.TokensCacheWrite < 0 {
		return errors.New("token counts must be non-negative")
	}
	if s.CostUSD < 0 {
		return errors.New("cost_usd must be non-negative")
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
	PriorSummaries  map[string]string `json:"prior_summaries,omitempty"`
	RevisionContext *string           `json:"revision_context,omitempty"`
	Context         *ContextBundle    `json:"context,omitempty"`
	MemoryBundle    *MemoryBundle     `json:"memory_bundle,omitempty"`
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

// StageUsage is the typed token and cost ledger a stage reports to the
// orchestrator. It is the single source of truth for per-stage metering;
// the orchestrator copies it into StageRecord and sums it into PipelineResult.
// CostUSD is zero until a pricing source is wired (known gap).
type StageUsage struct {
	InputTokens       int     `json:"input_tokens"`
	OutputTokens      int     `json:"output_tokens"`
	CachedInputTokens int     `json:"cached_input_tokens"`
	CacheWriteTokens  int     `json:"cache_write_tokens"`
	CostUSD           float64 `json:"cost_usd"`
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

// PipelineResult is the final pipeline result returned by the CLI.
type PipelineResult struct {
	RunID             string                 `json:"run_id"`
	Status            string                 `json:"status"`
	Tier              PipelineTier           `json:"tier"`
	Stages            []StageRecord          `json:"stages"`
	FinalOutput       map[string]interface{} `json:"final_output,omitempty"`
	TotalCostUSD      float64                `json:"total_cost_usd"`
	TotalTokensInput  int                    `json:"total_tokens_input"`
	TotalTokensOutput int                    `json:"total_tokens_output"`
	AbortReason       *string                `json:"abort_reason,omitempty"`
	MergeStatus       *string                `json:"merge_status,omitempty"`
	MergeBranch       *string                `json:"merge_branch,omitempty"`
	MergeCommitSHA    *string                `json:"merge_commit_sha,omitempty"`
	MergeMessage      *string                `json:"merge_message,omitempty"`
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
	if p.TotalCostUSD < 0 {
		return errors.New("total_cost_usd must be non-negative")
	}
	if p.TotalTokensInput < 0 || p.TotalTokensOutput < 0 {
		return errors.New("token counts must be non-negative")
	}
	if p.MergeStatus != nil {
		switch *p.MergeStatus {
		case "not_needed", "merged", "skipped", "conflict", "error":
		default:
			return fmt.Errorf("invalid merge_status %q", *p.MergeStatus)
		}
	}
	return nil
}
