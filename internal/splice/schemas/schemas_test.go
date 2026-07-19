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
		{name: "model backed", budget: StageBudget{InputMax: 1, OutputMax: 1, ModelTier: "nano"}},
		{name: "zero with tier", budget: StageBudget{ModelTier: "nano"}, wantErr: true},
		{name: "zero input", budget: StageBudget{OutputMax: 1, ModelTier: "nano"}, wantErr: true},
		{name: "zero output", budget: StageBudget{InputMax: 1, ModelTier: "nano"}, wantErr: true},
		{name: "negative", budget: StageBudget{InputMax: -1, OutputMax: -1}, wantErr: true},
		{name: "positive without tier", budget: StageBudget{InputMax: 1, OutputMax: 1}, wantErr: true},
		{name: "invalid tier", budget: StageBudget{InputMax: 1, OutputMax: 1, ModelTier: "fast"}, wantErr: true},
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

func TestExecutionPlanDAGIntegrity(t *testing.T) {
	budget := StageBudget{InputMax: 100, OutputMax: 100, ModelTier: "small"}
	plan := ExecutionPlan{
		Tier:          TierStandard,
		RequestIntent: "do it",
		Stages: []ExecutionStage{
			{Name: "write", Budget: budget},
			{Name: "test", DependsOn: []string{"write", "missing"}, Budget: budget},
		},
		TokenBudget: TokenBudget{
			TotalInputBudget:  1000,
			TotalOutputBudget: 1000,
			PerStage:          map[string]StageBudget{"write": budget, "test": budget},
			OverflowPolicy:    "abort",
		},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "depends_on unknown") {
		t.Fatalf("expected DAG error, got %v", err)
	}
	plan.Stages[1].DependsOn = []string{"write"}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid plan: %v", err)
	}
}

func TestExecutionPlanRejectsDependencyCycle(t *testing.T) {
	budget := StageBudget{InputMax: 100, OutputMax: 100, ModelTier: "small"}
	plan := ExecutionPlan{
		Tier:          TierStandard,
		RequestIntent: "do it",
		Stages: []ExecutionStage{
			{Name: "a", DependsOn: []string{"b"}, Budget: budget},
			{Name: "b", DependsOn: []string{"a"}, Budget: budget},
		},
		TokenBudget: TokenBudget{
			TotalInputBudget:  1000,
			TotalOutputBudget: 1000,
			PerStage:          map[string]StageBudget{"a": budget, "b": budget},
			OverflowPolicy:    "abort",
		},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}

	// Self-dependency is a trivial cycle.
	plan2 := ExecutionPlan{
		Tier:          TierLight,
		RequestIntent: "x",
		Stages:        []ExecutionStage{{Name: "a", DependsOn: []string{"a"}, Budget: budget}},
		TokenBudget:   TokenBudget{TotalInputBudget: 10, TotalOutputBudget: 10, PerStage: map[string]StageBudget{"a": budget}, OverflowPolicy: "abort"},
	}
	if err := plan2.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected self-cycle error, got %v", err)
	}
}

func TestExecutionPlanDuplicateStageName(t *testing.T) {
	budget := StageBudget{InputMax: 1, OutputMax: 1, ModelTier: "nano"}
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
		RunID:  "run-1",
		Status: "completed",
		Tier:   TierLight,
		Stages: []StageRecord{{Name: "s", Status: StageCompleted}},
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	res.Status = "weird"
	if err := res.Validate(); err == nil {
		t.Fatal("expected status error")
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
		tier := TierStandard
		ver := "go test ./..."
		task := Task{ID: "t1", Title: "T", Intent: "do it", AcceptanceFacts: []AcceptanceFact{{Statement: "works", AutomatedVerification: true, VerificationCommand: &ver}}, EstimatedTier: &tier}
		cases := []interface{ Validate() error }{
			AcceptanceFact{Statement: "ok"},
			task,
			Critique{Category: "correctness", Severity: SeverityHigh, Issue: "i"},
			PlanCritique{OverallAssessment: "looks good", Critiques: []Critique{{Category: "security", Severity: SeverityLow, Issue: "i"}}},
			TaskRunStatus{TaskID: "t1", Status: "pending"},
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
		budget := StageBudget{InputMax: 100, OutputMax: 100, ModelTier: "nano"}
		cases := []interface{ Validate() error }{
			ComplexityClassifierInput{Request: "fix typo"},
			ComplexityClassifierOutput{Tier: TierTrivial, Rationale: "r", Confidence: 0.8, DesignIntensity: DesignNone},
			ContextQuery{QueryType: ContextReadFile, Path: &path, MaxResults: 10, MaxChars: 100},
			ContextRequest{Reason: "r", Queries: []ContextQuery{{QueryType: ContextListFiles, MaxResults: 5, MaxChars: 100}}},
			ContextItem{Query: ContextQuery{QueryType: ContextListFiles, MaxResults: 5, MaxChars: 100}, Summary: "s", Payload: map[string]interface{}{"text": "hello", "count": 1.0}},
			ContextBundle{Request: ContextRequest{Reason: "r", Queries: []ContextQuery{{QueryType: ContextListFiles, MaxResults: 5, MaxChars: 100}}}, Items: []ContextItem{{Query: ContextQuery{QueryType: ContextListFiles, MaxResults: 5, MaxChars: 100}, Summary: "s"}}},
			StageBudget{InputMax: 100, OutputMax: 100, ModelTier: "small"},
			TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, PerStage: map[string]StageBudget{"s": budget}, Reserve: 100, OverflowPolicy: "abort"},
			ExecutionStage{Name: "s", Budget: budget},
			ExecutionPlan{Tier: TierLight, RequestIntent: "x", Stages: []ExecutionStage{{Name: "s", Budget: budget}}, TokenBudget: TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, PerStage: map[string]StageBudget{"s": budget}, Reserve: 100, OverflowPolicy: "abort"}},
			StageRecord{Name: "s", Status: StageCompleted},
			HarnessStageInput{RunID: "r1", StageName: "s", Sequence: 1, PlanTier: TierLight, RequestIntent: "x"},
			HarnessStageOutput{Summary: "s", Confidence: 0.9, Data: map[string]interface{}{"k": "v", "n": 2.0}},
			PipelineResult{RunID: "r1", Status: "completed", Tier: TierLight, Stages: []StageRecord{{Name: "s", Status: StageCompleted}}, FinalOutput: map[string]interface{}{"k": "v", "n": 3.0}},
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
