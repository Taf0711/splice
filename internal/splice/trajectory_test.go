package splice

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func iterState(opts ...func(*schemas.IterationState)) schemas.IterationState {
	state := schemas.IterationState{
		Iteration:                0,
		Timestamp:                0,
		StateHash:                "hash",
		Confidence:               1.0,
		LintIssuesBySeverity:     make(map[schemas.Severity]int),
		SecurityIssuesBySeverity: make(map[schemas.Severity]int),
	}
	for _, opt := range opts {
		opt(&state)
	}
	return state
}

func withTests(p, f, e int) func(*schemas.IterationState) {
	return func(s *schemas.IterationState) {
		s.TestsPassing = p
		s.TestsFailing = f
		s.TestsErrored = e
	}
}

func withHash(h string) func(*schemas.IterationState) {
	return func(s *schemas.IterationState) {
		s.StateHash = h
	}
}

func withConfidence(c float64) func(*schemas.IterationState) {
	return func(s *schemas.IterationState) {
		s.Confidence = c
	}
}

func withTokens(n int) func(*schemas.IterationState) {
	return func(s *schemas.IterationState) {
		s.TokensConsumed = n
	}
}

func withSeverity(lint, sec map[schemas.Severity]int) func(*schemas.IterationState) {
	return func(s *schemas.IterationState) {
		s.LintIssuesBySeverity = lint
		s.SecurityIssuesBySeverity = sec
	}
}

func harnessOutput(summary string, confidence float64, data map[string]interface{}) schemas.HarnessStageOutput {
	return schemas.HarnessStageOutput{Summary: summary, Confidence: confidence, Data: data}
}

func TestComputeIterationStateSplitsFailedAndErroredTests(t *testing.T) {
	results := schemas.TestRunResults{
		Command:    []string{"go", "test"},
		ExitCode:   1,
		DurationMs: 10,
		Tests: []schemas.TestCaseResult{
			{Name: "a", Status: "passed", DurationMs: 1},
			{Name: "b", Status: "failed", DurationMs: 1},
			{Name: "c", Status: "errored", DurationMs: 1},
			{Name: "d", Status: "skipped", DurationMs: 1},
		},
	}
	output := harnessOutput("ran tests", 0.8, map[string]interface{}{"test_results": results})
	state, err := ComputeIterationState(1, []schemas.HarnessStageOutput{output}, nil, schemas.ChangeSummary{}, Ptr(0.0))
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if state.TestsPassing != 1 || state.TestsFailing != 1 || state.TestsErrored != 1 {
		t.Fatalf("unexpected test counts: %+v", state)
	}
}

func TestComputeIterationStateGroupsLintIssuesBySeverity(t *testing.T) {
	line := 1
	fp := schemas.VerificationFingerprint("go_syntax", "GO_SYNTAX", "a.go", &line, "a")
	fp2 := schemas.VerificationFingerprint("go_syntax", "GO_SYNTAX", "b.go", &line, "b")
	fp3 := schemas.VerificationFingerprint("go_syntax", "GO_SYNTAX", "c.go", &line, "c")
	report := schemas.VerificationReport{
		Status:   schemas.VerificationFindings,
		Complete: true,
		Summary:  "found issues",
		Tools:    []schemas.VerificationToolRun{{Tool: "go_syntax", Required: true, Scope: "quality", Status: schemas.VerificationFindings, Summary: "found 3"}},
		Findings: []schemas.VerificationFinding{
			{Fingerprint: fp, Tool: "go_syntax", Authority: "deterministic", RuleID: "GO_SYNTAX", Category: "quality", Path: "a.go", Line: &line, Message: "a", Severity: schemas.SeverityHigh},
			{Fingerprint: fp2, Tool: "go_syntax", Authority: "deterministic", RuleID: "GO_SYNTAX", Category: "quality", Path: "b.go", Line: &line, Message: "b", Severity: schemas.SeverityHigh},
			{Fingerprint: fp3, Tool: "go_syntax", Authority: "deterministic", RuleID: "GO_SYNTAX", Category: "quality", Path: "c.go", Line: &line, Message: "c", Severity: schemas.SeverityLow},
		},
	}
	output := harnessOutput("analyzed", 0.7, map[string]interface{}{"static_analyzer_output": report})
	state, err := ComputeIterationState(2, []schemas.HarnessStageOutput{output}, nil, schemas.ChangeSummary{}, nil)
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if state.LintIssuesBySeverity[schemas.SeverityHigh] != 2 || state.LintIssuesBySeverity[schemas.SeverityLow] != 1 {
		t.Fatalf("unexpected lint counts: %+v", state.LintIssuesBySeverity)
	}
}

func TestComputeIterationStateReadsSecurityAuditorOutput(t *testing.T) {
	line := 1
	fp1 := schemas.VerificationFingerprint("bandit", "B307", "x.py", &line, "Use of eval")
	fp2 := schemas.VerificationFingerprint("bandit", "B608", "y.py", &line, "SQL injection risk")
	sec := schemas.VerificationReport{
		Status:   schemas.VerificationFindings,
		Complete: true,
		Summary:  "2 findings",
		Tools:    []schemas.VerificationToolRun{{Tool: "bandit", Required: true, Scope: "security", Status: schemas.VerificationFindings, Summary: "2 findings"}},
		Findings: []schemas.VerificationFinding{
			{Fingerprint: fp1, Tool: "bandit", Authority: "deterministic", RuleID: "B307", Category: "security", Path: "x.py", Line: &line, Message: "Use of eval", Severity: schemas.SeverityHigh},
			{Fingerprint: fp2, Tool: "bandit", Authority: "deterministic", RuleID: "B608", Category: "security", Path: "y.py", Line: &line, Message: "SQL injection risk", Severity: schemas.SeverityCritical},
		},
	}
	output := harnessOutput("security scan", 1.0, map[string]interface{}{"security_auditor_output": sec})
	state, err := ComputeIterationState(1, []schemas.HarnessStageOutput{output}, nil, schemas.ChangeSummary{}, nil)
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if state.SecurityIssuesBySeverity[schemas.SeverityHigh] != 1 || state.SecurityIssuesBySeverity[schemas.SeverityCritical] != 1 {
		t.Fatalf("unexpected security counts: %+v", state.SecurityIssuesBySeverity)
	}
}

func TestComputeIterationStateHashesGeneratedFilesDeterministically(t *testing.T) {
	code := schemas.CodeWriterOutput{
		Files: []schemas.FileChange{
			{Path: "app.go", Content: "value = 1\n", ChangeType: "modify"},
		},
		Language:   "go",
		Intent:     "Bump value.",
		Confidence: 0.9,
	}
	output := harnessOutput("wrote code", 0.9, map[string]interface{}{"code_writer_output": code})
	cs := schemas.ChangeSummary{
		IsRepo:       true,
		ChangedFiles: []schemas.ChangedFile{{Path: "app.go", Status: "modified"}},
		DiffText:     "--- a/app.go\n+++ b/app.go\n@@ -1 +1 @@\n-value = 0\n+value = 1\n",
	}
	stateA, err := ComputeIterationState(1, []schemas.HarnessStageOutput{output}, nil, cs, Ptr(0.0))
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	stateB, err := ComputeIterationState(2, []schemas.HarnessStageOutput{output}, nil, cs, Ptr(1.0))
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if stateA.StateHash != stateB.StateHash {
		t.Fatalf("same file set should produce same hash")
	}
	if stateA.CodeSizeBytes != len("value = 1\n") {
		t.Fatalf("unexpected code size: %d", stateA.CodeSizeBytes)
	}
	if len(stateA.FilesChanged) != 1 || stateA.FilesChanged[0] != "app.go" {
		t.Fatalf("unexpected files changed: %v", stateA.FilesChanged)
	}
	if stateA.LinesAdded != 1 || stateA.LinesRemoved != 1 {
		t.Fatalf("unexpected diff counts: +%d -%d", stateA.LinesAdded, stateA.LinesRemoved)
	}
}

func TestComputeIterationStateUsesWeakestConfidenceAndSumsTokens(t *testing.T) {
	outputs := []schemas.HarnessStageOutput{
		harnessOutput("a", 0.95, nil),
		harnessOutput("b", 0.4, nil),
	}
	records := []schemas.StageRecord{
		{Name: "a", Status: schemas.StageCompleted, TokensInput: 10, TokensOutput: 20, TokensCached: 5},
		{Name: "b", Status: schemas.StageCompleted, TokensInput: 1, TokensOutput: 2, TokensCached: 0},
	}
	state, err := ComputeIterationState(0, outputs, records, schemas.ChangeSummary{}, nil)
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if state.Confidence != 0.4 {
		t.Fatalf("expected confidence 0.4, got %v", state.Confidence)
	}
	if state.TokensConsumed != 38 {
		t.Fatalf("expected tokens 38, got %d", state.TokensConsumed)
	}
}

func TestComputeIterationStateHandlesNoStageOutputs(t *testing.T) {
	state, err := ComputeIterationState(0, nil, nil, schemas.ChangeSummary{}, Ptr(5.0))
	if err != nil {
		t.Fatalf("ComputeIterationState: %v", err)
	}
	if state.Confidence != 1.0 {
		t.Fatalf("expected default confidence 1.0, got %v", state.Confidence)
	}
	if state.StateHash != sha256HashOf(nil) {
		t.Fatalf("expected empty hash, got %s", state.StateHash)
	}
}

func sha256HashOf(entries [][3]string) string {
	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e[0] + e[1] + e[2]))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestComputeScoreImprovesWhenTestsPassAndIssuesDrop(t *testing.T) {
	before := iterState(withTests(1, 2, 0), withSeverity(map[schemas.Severity]int{schemas.SeverityHigh: 2, schemas.SeverityMedium: 1}, nil))
	after := iterState(withTests(3, 0, 0), withSeverity(map[schemas.Severity]int{schemas.SeverityHigh: 1}, nil))
	if ComputeScore(after) <= ComputeScore(before) {
		t.Fatal("expected score to improve")
	}
}

func TestComputeScorePlateausForEquivalentState(t *testing.T) {
	a := iterState(withTests(2, 0, 0), withSeverity(map[schemas.Severity]int{schemas.SeverityHigh: 1, schemas.SeverityLow: 99}, map[schemas.Severity]int{schemas.SeverityLow: 99}))
	b := iterState(withTests(2, 0, 0), withSeverity(map[schemas.Severity]int{schemas.SeverityHigh: 1}, nil))
	if ComputeScore(a) != ComputeScore(b) {
		t.Fatalf("expected equal scores, got %v vs %v", ComputeScore(a), ComputeScore(b))
	}
}

func TestComputeScoreRegressesWhenErrorsSecurityAndTypeIssuesIncrease(t *testing.T) {
	before := iterState(withTests(4, 0, 0))
	after := iterState(withTests(4, 1, 1), withSeverity(nil, map[schemas.Severity]int{schemas.SeverityCritical: 1, schemas.SeverityHigh: 1}))
	after.TypeErrors = 3
	if ComputeScore(after) >= ComputeScore(before) {
		t.Fatal("expected score to regress")
	}
	if got := ComputeScore(after); got != -56 {
		t.Fatalf("expected -56, got %v", got)
	}
}

func TestEvaluateTrajectoryContinuesForEmptyHistory(t *testing.T) {
	decision := EvaluateTrajectory(nil, 5, nil)
	if decision.Action != schemas.ActionContinue {
		t.Fatalf("expected continue, got %q", decision.Action)
	}
	if decision.IterationCount != 0 {
		t.Fatalf("expected iteration count 0")
	}
}

func TestEvaluateTrajectoryAbortsAtHardIterationLimit(t *testing.T) {
	history := []schemas.IterationState{iterState(withHash("a")), iterState(withHash("b"))}
	decision := EvaluateTrajectory(history, 2, nil)
	if decision.Action != schemas.ActionAbortHardLimit {
		t.Fatalf("expected abort hard limit, got %q", decision.Action)
	}
}

func TestEvaluateTrajectoryAbortsAtTokenBudget(t *testing.T) {
	history := []schemas.IterationState{iterState(withTokens(10), withHash("a")), iterState(withTokens(10), withHash("b"))}
	budget := 20
	decision := EvaluateTrajectory(history, 5, &budget)
	if decision.Action != schemas.ActionAbortBudget {
		t.Fatalf("expected abort budget, got %q", decision.Action)
	}
}

func TestEvaluateTrajectoryDetectsCycle(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withHash("a")),
		iterState(withHash("b")),
		iterState(withHash("a")),
	}
	decision := EvaluateTrajectory(history, 5, nil)
	if decision.Action != schemas.ActionEscalateCycleDetected {
		t.Fatalf("expected cycle detected, got %q", decision.Action)
	}
}

func TestEvaluateTrajectoryDetectsOscillation(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withHash("a")),
		iterState(withHash("b")),
		iterState(withHash("a")),
		iterState(withHash("b")),
	}
	decision := EvaluateTrajectory(history, 5, nil)
	if decision.Action != schemas.ActionEscalateOscillation {
		t.Fatalf("expected oscillation, got %q", decision.Action)
	}
}

func TestEvaluateTrajectoryRollsBackWhenScoreRegressesBelowInitial(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withTests(4, 0, 0), withHash("a")),
		iterState(withTests(3, 0, 0), withHash("b")),
		iterState(withTests(2, 0, 0), withHash("c")),
	}
	decision := EvaluateTrajectory(history, 5, nil)
	if decision.Action != schemas.ActionRollback {
		t.Fatalf("expected rollback, got %q", decision.Action)
	}
	if decision.InitialScore == nil || *decision.InitialScore != 40 {
		t.Fatalf("expected initial score 40, got %v", decision.InitialScore)
	}
}

func TestEvaluateTrajectoryStepsBackWhenScorePlateaus(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withTests(1, 0, 0), withHash("a")),
		iterState(withTests(1, 0, 0), withHash("b")),
		iterState(withTests(1, 0, 0), withHash("c")),
	}
	decision := EvaluateTrajectory(history, 5, nil)
	if decision.Action != schemas.ActionStepBack {
		t.Fatalf("expected step back, got %q", decision.Action)
	}
}

func TestEvaluateTrajectorySurfacesStrictConfidenceCollapse(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withTests(1, 0, 0), withHash("a"), withConfidence(0.9)),
		iterState(withTests(2, 0, 0), withHash("b"), withConfidence(0.6)),
		iterState(withTests(3, 0, 0), withHash("c"), withConfidence(0.0)),
	}
	decision := EvaluateTrajectory(history, 5, nil)
	if decision.Action != schemas.ActionSurfaceToUser {
		t.Fatalf("expected surface to user, got %q", decision.Action)
	}
}

func TestEvaluateTrajectoryContinuesWhenScoresImprove(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withTests(1, 0, 0), withHash("a"), withConfidence(0.7)),
		iterState(withTests(2, 0, 0), withHash("b"), withConfidence(0.8)),
		iterState(withTests(3, 0, 0), withHash("c"), withConfidence(0.9)),
	}
	decision := EvaluateTrajectory(history, 5, nil)
	if decision.Action != schemas.ActionContinue {
		t.Fatalf("expected continue, got %q", decision.Action)
	}
	if decision.CurrentScore == nil || *decision.CurrentScore != 30 {
		t.Fatalf("expected current score 30, got %v", decision.CurrentScore)
	}
}

func TestComputeIterationStateReturnsErrorOnMalformedPayload(t *testing.T) {
	output := harnessOutput("bad results", 0.8, map[string]interface{}{"test_results": "not a test result object"})
	if _, err := ComputeIterationState(1, []schemas.HarnessStageOutput{output}, nil, schemas.ChangeSummary{}, nil); err == nil {
		t.Fatal("expected error for malformed test_results payload")
	}
	if _, err := ComputeIterationState(1, []schemas.HarnessStageOutput{output}, nil, schemas.ChangeSummary{}, nil); err != nil && !strings.Contains(err.Error(), "test_results") {
		t.Fatalf("expected error to name key test_results, got %v", err)
	}
}

func TestEvaluateTrajectoryEscalatesOnRepeatedEmptyHash(t *testing.T) {
	history := []schemas.IterationState{
		iterState(withHash("")),
		iterState(withHash("a")),
		iterState(withHash("")),
	}
	decision := EvaluateTrajectory(history, 5, nil)
	if decision.Action != schemas.ActionEscalateCycleDetected {
		t.Fatalf("expected cycle detected for repeated empty hash, got %q", decision.Action)
	}
}
