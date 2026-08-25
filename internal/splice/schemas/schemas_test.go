package schemas

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func validFileChange() FileChange {
	return FileChange{Path: "x.go", Content: "package x", ChangeType: "create"}
}

func TestFileChangeValidation(t *testing.T) {
	if err := validFileChange().Validate(); err != nil {
		t.Fatalf("valid file change: %v", err)
	}
	invalid := validFileChange()
	invalid.Path = ""
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("expected path error, got %v", err)
	}
	invalid = validFileChange()
	invalid.ChangeType = "nope"
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "change type") {
		t.Fatalf("expected change_type error, got %v", err)
	}
}

func TestCodeWriterOutputValidation(t *testing.T) {
	v := CodeWriterOutput{Files: []FileChange{validFileChange()}, Language: "go", Intent: "add x", Confidence: 0.9}
	if err := v.Validate(); err != nil {
		t.Fatalf("valid output: %v", err)
	}
	v.Confidence = 1.1
	if err := v.Validate(); err == nil {
		t.Fatal("expected confidence error")
	}
}

func TestReasoningStageInputsValidateSelectedMemory(t *testing.T) {
	item := SelectedMemory{
		ID: "observation:1", Title: "lesson", Content: "use table tests",
		MemoryType: "lesson", Scope: MemoryScopeGlobal,
	}
	tests := []struct {
		name     string
		validate func([]SelectedMemory) error
	}{
		{name: "code writer", validate: func(memory []SelectedMemory) error {
			return (CodeWriterInput{Intent: "add x", Language: "go", Memory: memory}).Validate()
		}},
		{name: "test generator", validate: func(memory []SelectedMemory) error {
			return (TestGeneratorInput{Intent: "test x", Language: "go", Memory: memory}).Validate()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.validate([]SelectedMemory{item}); err != nil {
				t.Fatalf("valid memory: %v", err)
			}
			if err := tt.validate([]SelectedMemory{item, item}); err == nil || !strings.Contains(err.Error(), "duplicate memory id") {
				t.Fatalf("duplicate memory error = %v", err)
			}
			invalid := item
			invalid.ID = "observation:0"
			if err := tt.validate([]SelectedMemory{invalid}); err == nil || !strings.Contains(err.Error(), "positive decimal") {
				t.Fatalf("invalid memory id error = %v", err)
			}
		})
	}
}

func TestVerificationReportValidation(t *testing.T) {
	line := 5
	fp := VerificationFingerprint("go_syntax", "GO_SYNTAX", "a.go", &line, "bad")
	v := VerificationReport{
		Status:   VerificationFindings,
		Complete: true,
		Summary:  "1 verification finding",
		Tools:    []VerificationToolRun{{Tool: "go_syntax", Required: true, Scope: "quality", Status: VerificationFindings, Summary: "found 1"}},
		Findings: []VerificationFinding{{Fingerprint: fp, Tool: "go_syntax", Authority: "deterministic", RuleID: "GO_SYNTAX", Category: "quality", Path: "a.go", Line: &line, Message: "bad", Severity: SeverityHigh}},
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("valid report: %v", err)
	}
	bad := v.Findings[0]
	bad.Line = ptr(0)
	bad.Fingerprint = VerificationFingerprint(bad.Tool, bad.RuleID, bad.Path, bad.Line, bad.Message)
	v.Findings[0] = bad
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "line") {
		t.Fatalf("expected line error, got %v", err)
	}
}

func TestTestRunResultsCounts(t *testing.T) {
	res := TestRunResults{
		Command: []string{"go", "test"},
		Tests: []TestCaseResult{
			{Name: "a", Status: "passed"},
			{Name: "b", Status: "failed"},
			{Name: "c", Status: "errored"},
		},
	}
	if got, want := res.Passed(), 1; got != want {
		t.Fatalf("Passed() = %d, want %d", got, want)
	}
	if got, want := res.Failed(), 2; got != want {
		t.Fatalf("Failed() = %d, want %d", got, want)
	}
}

func TestContextQueryValidation(t *testing.T) {
	cases := []struct {
		name    string
		q       ContextQuery
		wantErr string
	}{
		{"read_file without path", ContextQuery{QueryType: ContextReadFile}, "requires path"},
		{"read_file ok", ContextQuery{QueryType: ContextReadFile, Path: ptr("x.go"), MaxResults: 10, MaxChars: 100}, ""},
		{"search without pattern", ContextQuery{QueryType: ContextSearch}, "requires pattern"},
		{"find_symbol without symbol", ContextQuery{QueryType: ContextFindSymbol}, "requires symbol"},
		{"invalid max_results", ContextQuery{QueryType: ContextListFiles, MaxResults: 0}, "max_results"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.q.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestStageBudgetValidation(t *testing.T) {
	tests := []struct {
		name    string
		budget  StageBudget
		wantErr bool
	}{
		{name: "deterministic zero", budget: StageBudget{}},
		{name: "model backed", budget: StageBudget{InputMax: 1, OutputMax: 1}},
		{name: "zero input", budget: StageBudget{OutputMax: 1}, wantErr: true},
		{name: "zero output", budget: StageBudget{InputMax: 1}, wantErr: true},
		{name: "negative", budget: StageBudget{InputMax: -1, OutputMax: -1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.budget.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestExecutionPlanDuplicateStageName(t *testing.T) {
	budget := StageBudget{InputMax: 1, OutputMax: 1}
	plan := ExecutionPlan{
		Tier:          TierLight,
		RequestIntent: "x",
		Stages: []ExecutionStage{
			{Name: "a", Budget: budget},
			{Name: "a", Budget: budget},
		},
		TokenBudget: TokenBudget{TotalInputBudget: 10, TotalOutputBudget: 10, PerStage: map[string]StageBudget{"a": budget}, OverflowPolicy: "abort"},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestDesignPlanTaskGraphIntegrity(t *testing.T) {
	plan := DesignPlan{
		Epic:         "epic",
		Requirements: []string{"r1"},
		InScope:      []string{"x"},
		OutOfScope:   []string{"y"},
		SystemDesign: "design",
		Tasks: []Task{
			{ID: "a", Title: "A", Intent: "do a"},
			{ID: "b", Title: "B", Intent: "do b", DependsOn: []string{"a", "missing"}},
		},
		Source: "authored",
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "depends_on unknown") {
		t.Fatalf("expected task graph error, got %v", err)
	}
	plan.Tasks[1].DependsOn = []string{"a"}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid plan: %v", err)
	}
}

func TestDesignPlanTerseRequestValidates(t *testing.T) {
	// A short request establishes no boundaries and needs no design note. The
	// crystallizer is told not to invent scope, so a plan that honestly leaves
	// out_of_scope and system_design empty must validate. Requiring them made
	// the model choose between its instructions and a passing schema.
	plan := DesignPlan{
		Epic:         "fix the failing test in ./pkg",
		Requirements: []string{"the test in ./pkg passes"},
		InScope:      []string{"./pkg"},
		Tasks:        []Task{{ID: "t1", Title: "Fix test", Intent: "make the failing test pass"}},
		Source:       "conversation",
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("terse plan must validate, got %v", err)
	}
}

func TestDesignPlanDuplicateTaskID(t *testing.T) {
	plan := DesignPlan{
		Epic:         "epic",
		Requirements: []string{"r"},
		InScope:      []string{"x"},
		OutOfScope:   []string{"y"},
		SystemDesign: "d",
		Tasks: []Task{
			{ID: "a", Title: "A", Intent: "a"},
			{ID: "a", Title: "B", Intent: "b"},
		},
		Source: "authored",
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestComplexityClassifierOutputValidation(t *testing.T) {
	v := ComplexityClassifierOutput{Tier: TierStandard, Rationale: "r", Confidence: 0.8}
	if err := v.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	v.Rationale = strings.Repeat("x", 501)
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "rationale") {
		t.Fatalf("expected rationale length error, got %v", err)
	}
}

func TestStageModelConfigFileEscalationValidation(t *testing.T) {
	// Escalation nil is valid (no-op).
	cfg := StageModelConfigFile{
		Default: StageModelConfig{ProviderProfile: "default", Model: "gpt-4o"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("nil escalation should be valid: %v", err)
	}

	// Escalation present with valid config is valid.
	cfg.Escalation = &StageModelConfig{ProviderProfile: "escalation", Model: "claude-sonnet-4", ReasoningEffort: "high"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid escalation config: %v", err)
	}

	// Escalation with missing provider_profile is invalid.
	cfg.Escalation = &StageModelConfig{ProviderProfile: "", Model: "x"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "provider_profile") {
		t.Fatalf("expected provider_profile error, got %v", err)
	}

	// Escalation with bad reasoning_effort is invalid.
	cfg.Escalation = &StageModelConfig{ProviderProfile: "p", Model: "m", ReasoningEffort: "extreme"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "reasoning_effort") {
		t.Fatalf("expected reasoning_effort error, got %v", err)
	}
}

func TestStageModelConfigFileEscalationJSONRoundTrip(t *testing.T) {
	cfg := StageModelConfigFile{
		Default:    StageModelConfig{ProviderProfile: "default", Model: "gpt-4o"},
		Escalation: &StageModelConfig{ProviderProfile: "escalation", Model: "claude-sonnet-4", ReasoningEffort: "high"},
	}
	if err := roundTripAndValidate(t, cfg); err != nil {
		t.Fatalf("escalation round-trip: %v", err)
	}

	// Round-trip with nil Escalation.
	cfg2 := StageModelConfigFile{
		Default: StageModelConfig{ProviderProfile: "default", Model: "gpt-4o"},
	}
	if err := roundTripAndValidate(t, cfg2); err != nil {
		t.Fatalf("nil escalation round-trip: %v", err)
	}

	// JSON round-trip: marshal, then unmarshal, ensure Escalation is preserved.
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed StageModelConfigFile
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Escalation == nil {
		t.Fatal("Escalation should be non-nil after round-trip")
	}
	if parsed.Escalation.ProviderProfile != "escalation" || parsed.Escalation.Model != "claude-sonnet-4" {
		t.Fatalf("escalation fields: %+v", *parsed.Escalation)
	}
}

func TestStageModelConfigFileResolveUnchanged(t *testing.T) {
	// Resolve must still work correctly with Escalation present.
	cfg := StageModelConfigFile{
		Default:    StageModelConfig{ProviderProfile: "default", Model: "gpt-4o"},
		Escalation: &StageModelConfig{ProviderProfile: "esc", Model: "claude-sonnet-4"},
		Stages: map[string]StageModelConfig{
			"code_writer": {ProviderProfile: "default", Model: "gpt-4o"},
		},
	}
	resolved, ok := cfg.Resolve("code_writer")
	if !ok || resolved.Model != "gpt-4o" {
		t.Fatalf("Resolve should return per-stage config, got %+v", resolved)
	}
	resolved, ok = cfg.Resolve("unknown")
	if ok || resolved.Model != "gpt-4o" {
		t.Fatalf("Resolve should return default for unknown, got %+v", resolved)
	}
}

func TestPipelineResultValidation(t *testing.T) {
	res := PipelineResult{
		RunID:        "run-1",
		Status:       "completed",
		Tier:         TierLight,
		Stages:       []StageRecord{{Name: "s", Status: StageCompleted}},
		CostCoverage: CostCoverageNotApplicable,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	res.Status = "weird"
	if err := res.Validate(); err == nil {
		t.Fatal("expected status error")
	}
}

func TestPipelineUsageRecordValidation(t *testing.T) {
	zero := 0.0
	valid := PipelineUsageRecord{
		Sequence:       1,
		Stage:          "code_writer",
		Iteration:      1,
		UsageReported:  true,
		InputTokens:    100,
		OutputTokens:   50,
		CostStatus:     CostStatusPriced,
		CostUSD:        &zero,
		CostProvenance: "runtime_estimate",
		PricingSource:  "test catalog",
		PricingAsOf:    "2026-07-26",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid record: %v", err)
	}
	unpriced := PipelineUsageRecord{
		Sequence: 1, Stage: "s", Iteration: 1, UsageReported: true,
		InputTokens: 10, OutputTokens: 5,
		CostStatus: CostStatusUnpriced, UnpricedReason: "no model", CostUSD: &zero,
	}
	tests := []struct {
		name    string
		base    PipelineUsageRecord
		mutate  func(*PipelineUsageRecord)
		wantErr string
	}{
		{name: "sequence", mutate: func(r *PipelineUsageRecord) { r.Sequence = 0 }, wantErr: "sequence"},
		{name: "stage", mutate: func(r *PipelineUsageRecord) { r.Stage = "" }, wantErr: "stage"},
		{name: "iteration", mutate: func(r *PipelineUsageRecord) { r.Iteration = -1 }, wantErr: "iteration"},
		{name: "cached input tokens", mutate: func(r *PipelineUsageRecord) { r.InputTokens = 10; r.CachedTokens = 20 }, wantErr: "cached input tokens"},
		{name: "cache write tokens", mutate: func(r *PipelineUsageRecord) { r.InputTokens = 100; r.CachedTokens = 60; r.CacheWrite = 50 }, wantErr: "cache write tokens"},
		{name: "reasoning tokens", mutate: func(r *PipelineUsageRecord) { r.OutputTokens = 10; r.Reasoning = 20 }, wantErr: "reasoning tokens"},
		{name: "priced cost usd", mutate: func(r *PipelineUsageRecord) { r.CostUSD = nil }, wantErr: "cost_usd"},
		{name: "priced provenance", mutate: func(r *PipelineUsageRecord) { r.CostProvenance = "" }, wantErr: "cost_provenance"},
		{name: "unpriced cost usd", base: unpriced, wantErr: "unpriced record must not have cost_usd"},
		{name: "unpriced reason", base: unpriced, mutate: func(r *PipelineUsageRecord) { r.CostUSD = nil; r.UnpricedReason = "" }, wantErr: "unpriced_reason"},
		{name: "cost status", mutate: func(r *PipelineUsageRecord) { r.CostStatus = "unknown" }, wantErr: "cost_status"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base
			if base.Sequence == 0 {
				base = valid
			}
			if tc.mutate != nil {
				tc.mutate(&base)
			}
			if err := base.Validate(); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %s error, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestPipelineResultCoverageStates(t *testing.T) {
	one := 1.0
	priced := PipelineUsageRecord{
		Sequence: 1, Stage: "s", Iteration: 1, UsageReported: true,
		InputTokens: 10, OutputTokens: 5,
		CostStatus: CostStatusPriced, CostUSD: &one, CostProvenance: "runtime_estimate", PricingSource: "test catalog", PricingAsOf: "2026-07-26",
	}
	unpriced := PipelineUsageRecord{
		Sequence: 2, Stage: "s", Iteration: 1,
		CostStatus: CostStatusUnpriced, UnpricedReason: "no model",
	}
	errRec := PipelineUsageRecord{
		Sequence: 3, Stage: "s", Iteration: 1, UsageReported: true,
		InputTokens: 10, OutputTokens: 5,
		CostStatus: CostStatusError, UnpricedReason: "malformed",
	}
	base := func() PipelineResult {
		return PipelineResult{
			RunID: "r1", Status: "completed", Tier: TierLight,
			Stages: []StageRecord{{Name: "s", Status: StageCompleted}}, CostCoverage: CostCoverageNotApplicable,
		}
	}
	unpricedFirst := unpriced
	unpricedFirst.Sequence = 1
	errorSecond := errRec
	errorSecond.Sequence = 2
	tests := []struct {
		name      string
		configure func(*PipelineResult)
		wantErr   string
	}{
		{name: "not-applicable", configure: func(res *PipelineResult) { res.CostCoverage = CostCoverageNotApplicable }},
		{name: "not-applicable with records", configure: func(res *PipelineResult) {
			res.UsageRecords = []PipelineUsageRecord{priced}
			res.PricedRequestCount = 1
			res.TotalTokensInput = 10
			res.TotalTokensOutput = 5
			res.TotalCostUSD = 1
			res.CostCoverage = CostCoverageNotApplicable
		}, wantErr: "not_applicable"},
		{name: "complete", configure: func(res *PipelineResult) {
			res.UsageRecords = []PipelineUsageRecord{priced}
			res.CostCoverage = CostCoverageComplete
			res.PricedRequestCount = 1
			res.TotalTokensInput = 10
			res.TotalTokensOutput = 5
			res.TotalCostUSD = 1
		}},
		{name: "partial", configure: func(res *PipelineResult) {
			res.UsageRecords = []PipelineUsageRecord{priced, unpriced}
			res.CostCoverage = CostCoveragePartial
			res.PricedRequestCount = 1
			res.UnpricedRequestCount = 1
			res.TotalTokensInput = 10
			res.TotalTokensOutput = 5
			res.TotalCostUSD = 1
		}},
		{name: "unavailable", configure: func(res *PipelineResult) {
			res.UsageRecords = []PipelineUsageRecord{unpricedFirst, errorSecond}
			res.CostCoverage = CostCoverageUnavailable
			res.UnpricedRequestCount = 1
			res.ErrorRequestCount = 1
			res.TotalTokensInput = 10
			res.TotalTokensOutput = 5
		}},
		{name: "complete with unpriced", configure: func(res *PipelineResult) {
			res.UsageRecords = []PipelineUsageRecord{priced, unpriced}
			res.CostCoverage = CostCoverageComplete
			res.PricedRequestCount = 1
			res.UnpricedRequestCount = 1
			res.TotalTokensInput = 10
			res.TotalTokensOutput = 5
			res.TotalCostUSD = 1
		}, wantErr: "cost_coverage"},
		{name: "partial without priced", configure: func(res *PipelineResult) {
			res.UsageRecords = []PipelineUsageRecord{unpricedFirst}
			res.CostCoverage = CostCoveragePartial
			res.UnpricedRequestCount = 1
		}, wantErr: "cost_coverage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := base()
			tc.configure(&res)
			err := res.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("%s valid: %v", tc.name, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %s error, got %v", tc.wantErr, err)
			}
		})
	}
}

// Ensure counts must match records.
func TestPipelineResultCountsMustMatchRecords(t *testing.T) {
	one := 1.0
	rec := PipelineUsageRecord{
		Sequence: 1, Stage: "s", Iteration: 1, UsageReported: true,
		InputTokens: 10, OutputTokens: 5,
		CostStatus: CostStatusPriced, CostUSD: &one, CostProvenance: "runtime_estimate", PricingSource: "test catalog", PricingAsOf: "2026-07-26",
	}
	res := PipelineResult{
		RunID: "r1", Status: "completed", Tier: TierLight,
		Stages:             []StageRecord{{Name: "s", Status: StageCompleted}},
		UsageRecords:       []PipelineUsageRecord{rec},
		CostCoverage:       CostCoverageComplete,
		PricedRequestCount: 2,
		TotalTokensInput:   10,
		TotalTokensOutput:  5,
		TotalCostUSD:       1,
	}
	if err := res.Validate(); err == nil || !strings.Contains(err.Error(), "request counts") {
		t.Fatalf("expected count mismatch error, got %v", err)
	}
	// Token totals must match.
	res.PricedRequestCount = 1
	res.TotalTokensInput = 99
	if err := res.Validate(); err == nil || !strings.Contains(err.Error(), "token totals") {
		t.Fatalf("expected token totals error, got %v", err)
	}
	// Duplicate sequence.
	res.TotalTokensInput = 10
	rec2 := rec
	rec2.Sequence = 1
	res.UsageRecords = []PipelineUsageRecord{rec, rec2}
	res.PricedRequestCount = 2
	res.TotalTokensInput = 20
	res.TotalTokensOutput = 10
	res.TotalCostUSD = 2
	if err := res.Validate(); err == nil || !strings.Contains(err.Error(), "sequence") {
		t.Fatalf("expected sequence error, got %v", err)
	}
}

func TestTrajectoryDecisionValidation(t *testing.T) {
	d := TrajectoryDecision{Action: ActionContinue, Reason: "ok", IterationCount: 1}
	if err := d.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	d.CurrentScore = ptr(2.0)
	if err := d.Validate(); err != nil {
		t.Fatalf("score 2.0 should be valid, got %v", err)
	}
	d.InitialScore = ptr(-16.0)
	if err := d.Validate(); err != nil {
		t.Fatalf("score -16.0 should be valid, got %v", err)
	}
}

func TestFailureReportValidation(t *testing.T) {
	f := FailureReport{OriginalIntent: "x", Hypothesis: "h", Options: []string{"a"}}
	if err := f.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	f.Options = nil
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "option") {
		t.Fatalf("expected options error, got %v", err)
	}
}

func TestStepBackAnalysisValidation(t *testing.T) {
	v := StepBackAnalysis{HypothesizedRootCause: "missing edge case", Evidence: []string{"test fails on empty input"}, RecommendedApproach: "add guard clause", Confidence: 0.8}
	if err := v.Validate(); err != nil {
		t.Fatalf("valid analysis: %v", err)
	}
	v2 := StepBackAnalysis{HypothesizedRootCause: "", RecommendedApproach: "x", Confidence: 0.5}
	if err := v2.Validate(); err == nil || !strings.Contains(err.Error(), "root_cause") {
		t.Fatalf("expected root cause error, got %v", err)
	}
	v3 := StepBackAnalysis{HypothesizedRootCause: "x", RecommendedApproach: "", Confidence: 0.5}
	if err := v3.Validate(); err == nil || !strings.Contains(err.Error(), "approach") {
		t.Fatalf("expected approach error, got %v", err)
	}
	v4 := StepBackAnalysis{HypothesizedRootCause: "x", RecommendedApproach: "y", Confidence: 1.5}
	if err := v4.Validate(); err == nil || !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("expected confidence error, got %v", err)
	}
}

func TestMemoryBundleValidation(t *testing.T) {
	mb := MemoryBundle{
		RequestingAgent: "agent",
		Observations: []MemoryObservation{
			{Scope: "project", OwnerAgent: "agent", Visibility: "private", MemoryType: "decision", Title: "t", Content: "c"},
		},
	}
	if err := mb.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	mb.Observations[0].Confidence = ptr(1.5)
	if err := mb.Validate(); err == nil {
		t.Fatal("expected confidence error")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	t.Run("agents", func(t *testing.T) {
		cases := []interface{ Validate() error }{
			validFileChange(),
			AppliedFileChange{Path: "x.go", ChangeType: "modify", BytesRead: 10},
			FileChangeApplyResult{Workspace: "/tmp/w", Applied: []AppliedFileChange{{Path: "x.go", ChangeType: "create", BytesRead: 5}}},
			CodeWriterInput{Intent: "add x", Language: "go", TargetPaths: []string{"x.go"}},
			CodeWriterOutput{Files: []FileChange{validFileChange()}, Language: "go", Intent: "add x", Confidence: 0.9},
			StepBackAnalysis{HypothesizedRootCause: "wrong approach", RecommendedApproach: "try something else", Confidence: 0.7},
			TestGeneratorInput{Intent: "test x", Language: "go"},
			TestGeneratorOutput{Files: []FileChange{validFileChange()}, Language: "go", Intent: "test x", Confidence: 0.8},
			VerificationReport{Status: VerificationPassed, Complete: true, Summary: "ok", Tools: []VerificationToolRun{{Tool: "go_syntax", Required: true, Scope: "quality", Status: VerificationPassed, Summary: "clean"}}},
			TestRunnerInput{Command: []string{"pytest"}, TimeoutSeconds: 30},
			TestCaseResult{Name: "t", Status: "passed", DurationMs: 1},
			TestRunResults{Command: []string{"go", "test"}, ExitCode: 0, Tests: []TestCaseResult{{Name: "t", Status: "passed", DurationMs: 1}}},
		}
		for i, v := range cases {
			if err := roundTripAndValidate(t, v); err != nil {
				t.Fatalf("case %d (%T): %v", i, v, err)
			}
		}
	})

	t.Run("design", func(t *testing.T) {
		ver := "go test ./..."
		task := Task{ID: "t1", Title: "T", Intent: "do it", AcceptanceFacts: []AcceptanceFact{{Statement: "works", AutomatedVerification: true, VerificationCommand: &ver}}}
		cases := []interface{ Validate() error }{
			AcceptanceFact{Statement: "ok"},
			task,
			Critique{Category: "correctness", Severity: SeverityHigh, Issue: "i"},
			PlanCritique{OverallAssessment: "looks good", Critiques: []Critique{{Category: "security", Severity: SeverityLow, Issue: "i"}}},
			TaskRunStatus{TaskID: "t1", Status: "pending"},
			TaskRunStatus{TaskID: "t1", Status: "running"},
			TaskRunOutcome{TaskID: "t1", RunID: "r1", Status: "completed"},
			DesignPlanResult{PlanID: "p1", Status: "completed", CompletedTasks: []TaskRunOutcome{{TaskID: "t1", RunID: "r1", Status: "completed"}}},
			DesignPlan{Epic: "e", Requirements: []string{"r"}, InScope: []string{"x"}, OutOfScope: []string{"y"}, SystemDesign: "d", Tasks: []Task{task}, Source: "authored"},
			PlanCriticInput{Plan: DesignPlan{Epic: "e", Requirements: []string{"r"}, InScope: []string{"x"}, OutOfScope: []string{"y"}, SystemDesign: "d", Tasks: []Task{task}, Source: "authored"}},
			ConversationMessage{Role: "user", Content: "hello"},
			DesignConversationInput{History: []ConversationMessage{{Role: "assistant", Content: "hi"}}},
		}
		for i, v := range cases {
			if err := roundTripAndValidate(t, v); err != nil {
				t.Fatalf("case %d (%T): %v", i, v, err)
			}
		}
	})

	t.Run("TaskRunStatus vocabulary matches TaskRunOutcome's terminal states", func(t *testing.T) {
		// TaskRunStatus and TaskRunOutcome previously drifted: TaskRunStatus
		// accepted "succeeded" where TaskRunOutcome (and the actual runner
		// writes) use "completed". Pin the aligned vocabulary both ways so
		// they cannot drift apart again.
		for _, status := range []string{"pending", "running", "completed", "failed", "aborted"} {
			if err := (TaskRunStatus{TaskID: "t1", Status: status}).Validate(); err != nil {
				t.Errorf("TaskRunStatus rejected %q: %v", status, err)
			}
		}
		if err := (TaskRunStatus{TaskID: "t1", Status: "succeeded"}).Validate(); err == nil {
			t.Error("TaskRunStatus accepted the old drifted status \"succeeded\"; vocabulary must stay aligned to TaskRunOutcome's \"completed\"")
		}
	})

	t.Run("events", func(t *testing.T) {
		cases := []interface{ Validate() error }{
			ChangeSummary{IsRepo: true, ChangedFiles: []ChangedFile{{Path: "x.go", Status: "modified"}}},
		}
		for i, v := range cases {
			if err := roundTripAndValidate(t, v); err != nil {
				t.Fatalf("case %d (%T): %v", i, v, err)
			}
		}
	})

	t.Run("memory", func(t *testing.T) {
		conf := 0.9
		cases := []interface{ Validate() error }{
			MemoryObservation{ID: 1, OwnerAgent: "a", Title: "t", Content: "c", Scope: "project", Visibility: "private", MemoryType: "decision", Confidence: &conf, CreatedAt: 1, UpdatedAt: 1},
			MemoryQuery{RequestingAgent: "a", Query: "q", Limit: 5, Scopes: []string{"project"}},
			MemoryBundle{RequestingAgent: "a", Observations: []MemoryObservation{{OwnerAgent: "a", Title: "t", Content: "c", Scope: "project", Visibility: "private", MemoryType: "note", CreatedAt: 1, UpdatedAt: 1}}},
		}
		for i, v := range cases {
			if err := roundTripAndValidate(t, v); err != nil {
				t.Fatalf("case %d (%T): %v", i, v, err)
			}
		}
	})

	t.Run("plan", func(t *testing.T) {
		path := "x.go"
		pattern := "foo"
		symbol := "bar"
		budget := StageBudget{InputMax: 100, OutputMax: 100}
		cases := []interface{ Validate() error }{
			ComplexityClassifierInput{Request: "fix typo"},
			ComplexityClassifierOutput{Tier: TierTrivial, Rationale: "r", Confidence: 0.8, DesignIntensity: DesignNone},
			ContextQuery{QueryType: ContextReadFile, Path: &path, MaxResults: 10, MaxChars: 100},
			ContextRequest{Reason: "r", Queries: []ContextQuery{{QueryType: ContextListFiles, MaxResults: 5, MaxChars: 100}}},
			ContextItem{Query: ContextQuery{QueryType: ContextListFiles, MaxResults: 5, MaxChars: 100}, Summary: "s", Payload: map[string]interface{}{"text": "hello", "count": 1.0}},
			ContextBundle{Request: ContextRequest{Reason: "r", Queries: []ContextQuery{{QueryType: ContextListFiles, MaxResults: 5, MaxChars: 100}}}, Items: []ContextItem{{Query: ContextQuery{QueryType: ContextListFiles, MaxResults: 5, MaxChars: 100}, Summary: "s"}}},
			StageBudget{InputMax: 100, OutputMax: 100},
			TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, PerStage: map[string]StageBudget{"s": budget}, Reserve: 100, OverflowPolicy: "abort"},
			ExecutionStage{Name: "s", Budget: budget},
			ExecutionPlan{Tier: TierLight, RequestIntent: "x", Stages: []ExecutionStage{{Name: "s", Budget: budget}}, TokenBudget: TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, PerStage: map[string]StageBudget{"s": budget}, Reserve: 100, OverflowPolicy: "abort"}},
			StageRecord{Name: "s", Status: StageCompleted},
			HarnessStageInput{RunID: "r1", StageName: "s", Sequence: 1, PlanTier: TierLight, RequestIntent: "x"},
			HarnessStageInput{RunID: "r1", StageName: "s", Sequence: 1, PlanTier: TierLight, RequestIntent: "x", PipelineStages: []string{"s", "t"}, NextStage: "t"},
			HarnessStageOutput{Summary: "s", Confidence: 0.9, Data: map[string]interface{}{"k": "v", "n": 2.0}},
			PipelineResult{RunID: "r1", Status: "completed", Tier: TierLight, Stages: []StageRecord{{Name: "s", Status: StageCompleted}}, FinalOutput: map[string]interface{}{"k": "v", "n": 3.0}, CostCoverage: CostCoverageNotApplicable},
		}
		_ = pattern
		_ = symbol
		for i, v := range cases {
			if err := roundTripAndValidate(t, v); err != nil {
				t.Fatalf("case %d (%T): %v", i, v, err)
			}
		}
	})

	t.Run("trajectory", func(t *testing.T) {
		score := 42.0
		cases := []interface{ Validate() error }{
			IterationState{Iteration: 0, StateHash: "h", Confidence: 1.0, LintIssuesBySeverity: map[Severity]int{SeverityHigh: 1}},
			IterationRecord{Iteration: 1, Summary: "s", Outcome: "improvement"},
			FailureReport{OriginalIntent: "x", Hypothesis: "h", Options: []string{"a"}},
			TrajectoryDecision{Action: ActionContinue, Reason: "ok", IterationCount: 1, CurrentScore: &score, InitialScore: &score},
		}
		for i, v := range cases {
			if err := roundTripAndValidate(t, v); err != nil {
				t.Fatalf("case %d (%T): %v", i, v, err)
			}
		}
	})
}

func TestPlanCritiqueSeverityControlsBlocking(t *testing.T) {
	tests := []struct {
		name      string
		severity  Severity
		mustFix   bool
		wantValid bool
	}{
		// The critic blocked a plan repeatedly, each time on a new non-blocking
		// issue, so the plan could never be approved.
		{name: "medium does not block", severity: SeverityMedium, mustFix: false, wantValid: true},
		{name: "medium cannot claim must-fix", severity: SeverityMedium, mustFix: true, wantValid: false},
		{name: "high blocks", severity: SeverityHigh, mustFix: true, wantValid: true},
		{name: "critical blocks", severity: SeverityCritical, mustFix: true, wantValid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			critique := PlanCritique{
				Critiques:              []Critique{{Category: "correctness", Severity: tt.severity, Issue: "issue"}},
				MustFixBeforeExecution: tt.mustFix,
				OverallAssessment:      "assessment",
			}
			if got := critique.Validate() == nil; got != tt.wantValid {
				t.Fatalf("Validate success = %t, want %t", got, tt.wantValid)
			}
			if critique.MustFixBeforeExecution != tt.mustFix {
				t.Fatalf("must_fix_before_execution = %t, want %t", critique.MustFixBeforeExecution, tt.mustFix)
			}
		})
	}
}

func TestPlanCritiquePlanCompletenessGapsDoNotBlock(t *testing.T) {
	// Regression: the critic blocked a plan by rating "the plan does not mention a mutex" as high, when the workspace already had one.
	critique := PlanCritique{
		Critiques: []Critique{
			{Category: "correctness", Severity: SeverityMedium, Issue: "the plan does not mention a mutex; the fact is unverified"},
			{Category: "operability", Severity: SeverityMedium, Issue: "the plan does not specify persistence error behavior"},
		},
		MustFixBeforeExecution: false,
		OverallAssessment:      "The plan has details to resolve during implementation.",
	}
	if err := critique.Validate(); err != nil {
		t.Fatalf("plan-completeness gaps at medium severity must not block: %v", err)
	}
}

func TestPlanCritiqueGenuineHighDefectBlocks(t *testing.T) {
	critique := PlanCritique{
		Critiques:              []Critique{{Category: "security", Severity: SeverityHigh, Issue: "The plan stores credentials in plaintext, creating a security hole when executed."}},
		MustFixBeforeExecution: true,
		OverallAssessment:      "The plan must not start until credential storage is fixed.",
	}
	if err := critique.Validate(); err != nil {
		t.Fatalf("genuine high-severity defect must block: %v", err)
	}
}

func TestPlanCritiqueEmptySetDoesNotBlock(t *testing.T) {
	critique := PlanCritique{OverallAssessment: "no issues"}
	if err := critique.Validate(); err != nil {
		t.Fatalf("empty critique set: %v", err)
	}
	if critique.MustFixBeforeExecution {
		t.Fatal("empty critique set must not block execution")
	}
}

func TestPlanCritiqueRejectsMustFixWithoutQualifyingSeverity(t *testing.T) {
	critique := PlanCritique{
		Critiques:              []Critique{{Category: "correctness", Severity: SeverityMedium, Issue: "issue"}},
		MustFixBeforeExecution: true,
		OverallAssessment:      "assessment",
	}
	if err := critique.Validate(); err == nil || !strings.Contains(err.Error(), "must_fix_before_execution") {
		t.Fatalf("expected inconsistent blocking flag error, got %v", err)
	}
}

func TestPlanCriticInputAllowsLegacyPreviousCritique(t *testing.T) {
	// A session persisted before the severity rule must still load, because the
	// rule describes new output and not history.
	input := PlanCriticInput{
		Plan: DesignPlan{
			Epic:         "current plan",
			Requirements: []string{"the system must work"},
			InScope:      []string{"core flow"},
			Tasks:        []Task{{ID: "t1", Title: "build it", Intent: "implement the core flow"}},
			Source:       "authored",
		},
		PreviousCritique: &PlanCritique{
			Critiques:              []Critique{{Category: "correctness", Severity: SeverityMedium, Issue: "legacy issue"}},
			MustFixBeforeExecution: true,
			OverallAssessment:      "legacy assessment",
		},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("legacy previous critique should validate: %v", err)
	}
}

func roundTripAndValidate(t *testing.T, v interface{ Validate() error }) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	orig := reflect.ValueOf(v)
	if orig.Kind() == reflect.Ptr {
		return fmt.Errorf("value must not be a pointer")
	}
	dest := reflect.New(orig.Type()).Interface()
	if err := json.Unmarshal(b, dest); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if err := dest.(interface{ Validate() error }).Validate(); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	if !reflect.DeepEqual(v, reflect.ValueOf(dest).Elem().Interface()) {
		return fmt.Errorf("round-trip mismatch: original %+v, got %+v", v, reflect.ValueOf(dest).Elem().Interface())
	}
	return nil
}

// TestChangeSummaryRejectsDegradedReasonWithoutRepo is the T4 schema pin: a
// summary cannot set DegradedReason while IsRepo is false, because that
// combination never occurs in the pipeline.
func TestChangeSummaryRejectsDegradedReasonWithoutRepo(t *testing.T) {
	valid := ChangeSummary{IsRepo: true, DegradedReason: "git read refused: procrun: refused splice.stage spawn"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("repository summary with DegradedReason must validate: %v", err)
	}
	invalid := ChangeSummary{IsRepo: false, DegradedReason: "git read refused"}
	if err := invalid.Validate(); err == nil {
		t.Fatal("DegradedReason without IsRepo must be rejected")
	}
}

// TestChangeSummaryOldJSONStillDecodes pins the additive contract: JSON from
// before the DegradedReason field still decodes and validates.
func TestChangeSummaryOldJSONStillDecodes(t *testing.T) {
	raw := `{"is_repo": true, "changed_files": [{"path": "a.go", "status": "modified"}], "diff_text": "x", "truncated": false}`
	var summary ChangeSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("old JSON must decode: %v", err)
	}
	if !summary.IsRepo || summary.DegradedReason != "" {
		t.Fatalf("decoded = %+v, want is_repo true and empty degraded_reason", summary)
	}
	if err := summary.Validate(); err != nil {
		t.Fatalf("decoded old JSON must validate: %v", err)
	}
}
