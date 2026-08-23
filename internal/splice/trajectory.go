package splice

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// ComputeScore scores an iteration state using the severity-weighted formula.
func ComputeScore(state schemas.IterationState) float64 {
	return float64(
		state.TestsPassing*10 -
			state.TestsFailing*8 -
			state.TestsErrored*12 -
			state.AcceptanceFactsFailing*12 +
			state.AcceptanceFactsPassing*10 -
			state.LintIssuesBySeverity[schemas.SeverityHigh]*3 -
			state.LintIssuesBySeverity[schemas.SeverityMedium]*1 -
			state.SecurityIssuesBySeverity[schemas.SeverityCritical]*50 -
			state.SecurityIssuesBySeverity[schemas.SeverityHigh]*20,
	)
}

type trajectoryRule struct {
	name     string
	evaluate func(trajectoryRuleContext) *schemas.TrajectoryDecision
}

type trajectoryRuleContext struct {
	history           []schemas.IterationState
	maxIterations     int
	tokenBudget       *int
	currentScore      *float64
	initialScore      *float64
	tokensConsumed    int
	tokensGenerated   int
	stateHashes       []string
	recentConfidences []float64
}

func (rc trajectoryRuleContext) decision(action schemas.TrajectoryAction, reason string, evidence []string) schemas.TrajectoryDecision {
	return schemas.TrajectoryDecision{
		Action:         action,
		Reason:         reason,
		IterationCount: len(rc.history),
		CurrentScore:   rc.currentScore,
		InitialScore:   rc.initialScore,
		Evidence:       evidence,
	}
}

// trajectoryRules is the ordered policy table. First non-nil decision wins.
var trajectoryRules = []trajectoryRule{
	{name: "iteration_limit", evaluate: ruleIterationLimit},
	{name: "token_budget", evaluate: ruleTokenBudget},
	{name: "oscillation", evaluate: ruleOscillation},
	{name: "cycle", evaluate: ruleCycle},
	{name: "no_progress", evaluate: ruleNoProgress},
	{name: "rollback", evaluate: ruleRollback},
	{name: "step_back", evaluate: ruleStepBack},
	{name: "confidence", evaluate: ruleConfidence},
}

// EvaluateTrajectory evaluates trajectory rules over an iteration-state history.
func EvaluateTrajectory(history []schemas.IterationState, maxIterations int, tokenBudget *int) schemas.TrajectoryDecision {
	rc := newTrajectoryRuleContext(history, maxIterations, tokenBudget)
	if len(history) == 0 {
		return rc.decision(schemas.ActionContinue, "No iteration history to evaluate.", nil)
	}
	for _, rule := range trajectoryRules {
		if decision := rule.evaluate(rc); decision != nil {
			return *decision
		}
	}
	return rc.decision(schemas.ActionContinue, "Trajectory remains within safe bounds.", nil)
}

func newTrajectoryRuleContext(history []schemas.IterationState, maxIterations int, tokenBudget *int) trajectoryRuleContext {
	rc := trajectoryRuleContext{
		history:       history,
		maxIterations: maxIterations,
		tokenBudget:   tokenBudget,
		stateHashes:   make([]string, len(history)),
	}
	if len(history) > 0 {
		current := ComputeScore(history[len(history)-1])
		initial := ComputeScore(history[0])
		rc.currentScore = &current
		rc.initialScore = &initial
	}
	for i, state := range history {
		rc.tokensConsumed += state.TokensConsumed
		rc.tokensGenerated += state.TokensGenerated
		rc.stateHashes[i] = state.StateHash
	}
	start := max(0, len(history)-3)
	rc.recentConfidences = make([]float64, 0, len(history)-start)
	for _, state := range history[start:] {
		rc.recentConfidences = append(rc.recentConfidences, state.Confidence)
	}
	return rc
}

func ruleIterationLimit(rc trajectoryRuleContext) *schemas.TrajectoryDecision {
	if len(rc.history) < rc.maxIterations {
		return nil
	}
	decision := rc.decision(schemas.ActionAbortHardLimit, "Maximum iteration count reached.",
		[]string{fmt.Sprintf("iterations=%d", len(rc.history)), fmt.Sprintf("max_iterations=%d", rc.maxIterations)})
	return &decision
}

// ruleTokenBudget gates on GENERATION ONLY. Input volume is bounded per call
// by stage-input compaction (stage_input.go) and never triggers an abort:
// memory-injected runs legitimately re-charge input on every consuming stage,
// so a cumulative-input gate would veto them regardless of behavior. Output
// overflow stays fatal as runaway-generation protection.
func ruleTokenBudget(rc trajectoryRuleContext) *schemas.TrajectoryDecision {
	if rc.tokenBudget == nil || rc.tokensGenerated < *rc.tokenBudget {
		return nil
	}
	decision := rc.decision(schemas.ActionAbortBudget, "Token budget reached.",
		[]string{fmt.Sprintf("tokens_generated=%d", rc.tokensGenerated), fmt.Sprintf("output_budget=%d", *rc.tokenBudget),
			fmt.Sprintf("tokens_consumed_input_inclusive=%d", rc.tokensConsumed)})
	return &decision
}

func ruleOscillation(rc trajectoryRuleContext) *schemas.TrajectoryDecision {
	if !detectOscillation(rc.stateHashes) {
		return nil
	}
	decision := rc.decision(schemas.ActionEscalateOscillation, "State hashes show a repeated oscillation pattern.",
		[]string{fmt.Sprintf("recent_hashes=%v", recentItems(rc.stateHashes, 4))})
	return &decision
}

func ruleCycle(rc trajectoryRuleContext) *schemas.TrajectoryDecision {
	if len(rc.stateHashes) == 0 || !slices.Contains(rc.stateHashes[:len(rc.stateHashes)-1], rc.stateHashes[len(rc.stateHashes)-1]) {
		return nil
	}
	prevSig := verificationFailureSignature(rc.history[len(rc.history)-2])
	curSig := verificationFailureSignature(rc.history[len(rc.history)-1])
	reason := "Current state hash was seen before. The identical state came with changing verification failures, so model thrash is more likely."
	switch {
	case curSig == emptyVerificationFailureSignature && prevSig == emptyVerificationFailureSignature:
		reason = "Current state hash was seen before. Neither iteration reported verification failures, so this is likely a no-op pass or model thrash against a non-verifying gate."
	case curSig == prevSig:
		reason = "Current state hash was seen before. The identical state came with identical verification failures, so the environment or verifier may be stuck."
	}
	decision := rc.decision(schemas.ActionEscalateCycleDetected, reason,
		[]string{
			fmt.Sprintf("state_hash=%s", rc.stateHashes[len(rc.stateHashes)-1]),
			fmt.Sprintf("verification_failure_signature_current=%s", curSig),
			fmt.Sprintf("verification_failure_signature_previous=%s", prevSig),
		})
	return &decision
}

func ruleRollback(rc trajectoryRuleContext) *schemas.TrajectoryDecision {
	if len(rc.history) < 3 || rc.currentScore == nil || rc.initialScore == nil || *rc.currentScore >= *rc.initialScore {
		return nil
	}
	decision := rc.decision(schemas.ActionRollback, "Current score regressed below the initial score.",
		[]string{fmt.Sprintf("initial_score=%v", *rc.initialScore), fmt.Sprintf("current_score=%v", *rc.currentScore)})
	return &decision
}

func ruleStepBack(rc trajectoryRuleContext) *schemas.TrajectoryDecision {
	if len(rc.history) < 3 || scoreImproving(rc.history[len(rc.history)-3:]) {
		return nil
	}
	decision := rc.decision(schemas.ActionStepBack, "Score has not improved across the last three iterations.",
		[]string{fmt.Sprintf("recent_scores=%v", scores(rc.history[len(rc.history)-3:]))})
	return &decision
}

func ruleNoProgress(rc trajectoryRuleContext) *schemas.TrajectoryDecision {
	n := trailingNoProgressCount(rc.history)
	if n < 3 {
		return nil
	}
	evidence := []string{
		fmt.Sprintf("no_progress_iterations=%d", n),
		"files_changed=0",
		"lines_changed=0",
	}
	if noProgressAlreadyFired(rc.history) {
		decision := rc.decision(schemas.ActionAbortNoProgress, "The last three iterations produced no workspace change after a prior no-progress step-back.", evidence)
		return &decision
	}
	decision := rc.decision(schemas.ActionStepBack, "The last three iterations produced no workspace change.", evidence)
	return &decision
}

func noProgressAlreadyFired(history []schemas.IterationState) bool {
	for i := 3; i < len(history); i++ {
		if trailingNoProgressCount(history[:i]) >= 3 {
			return true
		}
	}
	return false
}

func trailingNoProgressCount(history []schemas.IterationState) int {
	n := 0
	for i := len(history) - 1; i >= 0; i-- {
		if hasWorkspaceProgress(history[i]) {
			break
		}
		n++
	}
	return n
}

func hasWorkspaceProgress(state schemas.IterationState) bool {
	return len(state.FilesChanged) > 0 || state.LinesAdded+state.LinesRemoved > 0
}

func ruleConfidence(rc trajectoryRuleContext) *schemas.TrajectoryDecision {
	if len(rc.recentConfidences) != 3 || !strictlyDecreasing(rc.recentConfidences) {
		return nil
	}
	decision := rc.decision(schemas.ActionSurfaceToUser,
		"Confidence is strictly decreasing across the last three iterations.",
		[]string{fmt.Sprintf("recent_confidences=%v", rc.recentConfidences)})
	return &decision
}

type iterationSignals struct {
	testResults       []schemas.TestRunResults
	staticOutputs     []schemas.VerificationReport
	securityOutputs   []schemas.VerificationReport
	codeWriterOutputs []schemas.CodeWriterOutput
	testGenOutputs    []schemas.TestGeneratorOutput
	acceptanceResults [][]schemas.TestCaseResult
}

type trajectoryExtractor func(outputs []schemas.HarnessStageOutput, dest *iterationSignals) error

// trajectoryExtractors maps a stage output key to the parser that feeds the
// monitor. A new trajectory-relevant stage must add exactly one entry here.
var trajectoryExtractors = map[string]trajectoryExtractor{
	"test_results": func(outputs []schemas.HarnessStageOutput, dest *iterationSignals) error {
		values, err := typedPayloads[schemas.TestRunResults](outputs, "test_results")
		if err != nil {
			return err
		}
		dest.testResults = values
		return nil
	},
	"static_analyzer_output": func(outputs []schemas.HarnessStageOutput, dest *iterationSignals) error {
		values, err := typedPayloads[schemas.VerificationReport](outputs, "static_analyzer_output")
		if err != nil {
			return err
		}
		dest.staticOutputs = values
		return nil
	},
	"security_auditor_output": func(outputs []schemas.HarnessStageOutput, dest *iterationSignals) error {
		values, err := typedPayloads[schemas.VerificationReport](outputs, "security_auditor_output")
		if err != nil {
			return err
		}
		dest.securityOutputs = values
		return nil
	},
	"code_writer_output": func(outputs []schemas.HarnessStageOutput, dest *iterationSignals) error {
		values, err := typedPayloads[schemas.CodeWriterOutput](outputs, "code_writer_output")
		if err != nil {
			return err
		}
		dest.codeWriterOutputs = values
		return nil
	},
	"test_generator_output": func(outputs []schemas.HarnessStageOutput, dest *iterationSignals) error {
		values, err := typedPayloads[schemas.TestGeneratorOutput](outputs, "test_generator_output")
		if err != nil {
			return err
		}
		dest.testGenOutputs = values
		return nil
	},
	"acceptance_results": func(outputs []schemas.HarnessStageOutput, dest *iterationSignals) error {
		values, err := typedPayloads[[]schemas.TestCaseResult](outputs, "acceptance_results")
		if err != nil {
			return err
		}
		dest.acceptanceResults = values
		return nil
	},
}

func extractIterationSignals(outputs []schemas.HarnessStageOutput) (iterationSignals, error) {
	var dest iterationSignals
	keys := make([]string, 0, len(trajectoryExtractors))
	for key := range trajectoryExtractors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := trajectoryExtractors[key](outputs, &dest); err != nil {
			return iterationSignals{}, fmt.Errorf("%s: %w", key, err)
		}
	}
	return dest, nil
}

// ComputeIterationState computes the deterministic state vector for one pipeline pass.
func ComputeIterationState(iteration int, stageOutputs []schemas.HarnessStageOutput, stageRecords []schemas.StageRecord, changeSummary schemas.ChangeSummary, timestamp *float64) (schemas.IterationState, error) {
	signals, err := extractIterationSignals(stageOutputs)
	if err != nil {
		return schemas.IterationState{}, err
	}

	linesAdded, linesRemoved := countDiffLines(changeSummary.DiffText)

	ts := float64(time.Now().UnixNano()) / 1e9
	if timestamp != nil {
		ts = *timestamp
	}

	preexisting, authored := splitTestCounts(signals.testResults, authoredTestFiles(signals.testGenOutputs))

	return schemas.IterationState{
		Iteration:                iteration,
		Timestamp:                float64(ts),
		TestsPassing:             countTests(signals.testResults, "passed"),
		TestsFailing:             countTests(signals.testResults, "failed"),
		TestsErrored:             countTests(signals.testResults, "errored"),
		Preexisting:              preexisting,
		Authored:                 authored,
		AcceptanceFactsPassing:   countAcceptanceResults(signals.acceptanceResults, "passed"),
		AcceptanceFactsFailing:   countAcceptanceResults(signals.acceptanceResults, "failed", "errored"),
		LintIssuesBySeverity:     countBySeverity(signals.staticOutputs),
		SecurityIssuesBySeverity: countBySeverity(signals.securityOutputs),
		CodeSizeBytes:            codeSizeBytes(signals.codeWriterOutputs),
		StateHash:                stateHash(signals.codeWriterOutputs),
		Confidence:               aggregateConfidence(stageOutputs),
		TokensConsumed:           tokensConsumed(stageRecords),
		TokensGenerated:          tokensGenerated(stageRecords),
		VerificationIncomplete:   countStageStatus(stageRecords, schemas.StageIncomplete),
		FilesChanged:             sortedPaths(changeSummary.ChangedFiles),
		LinesAdded:               linesAdded,
		LinesRemoved:             linesRemoved,
	}, nil
}

func countAcceptanceResults(results [][]schemas.TestCaseResult, statuses ...string) int {
	wanted := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		wanted[status] = struct{}{}
	}
	count := 0
	for _, cases := range results {
		for _, tc := range cases {
			if _, ok := wanted[tc.Status]; ok {
				count++
			}
		}
	}
	return count
}

// verificationFailureSignature is a compact fingerprint of the verification
// failures in an iteration state. It covers failing and errored tests,
// failing acceptance facts, and high-plus-critical lint and security findings.
const emptyVerificationFailureSignature = "tests_failing=0,tests_errored=0,acceptance_facts_failing=0,lint_high_critical=0,security_high_critical=0"

func verificationFailureSignature(state schemas.IterationState) string {
	return fmt.Sprintf("tests_failing=%d,tests_errored=%d,acceptance_facts_failing=%d,lint_high_critical=%d,security_high_critical=%d",
		state.TestsFailing,
		state.TestsErrored,
		state.AcceptanceFactsFailing,
		state.LintIssuesBySeverity[schemas.SeverityCritical]+state.LintIssuesBySeverity[schemas.SeverityHigh],
		state.SecurityIssuesBySeverity[schemas.SeverityCritical]+state.SecurityIssuesBySeverity[schemas.SeverityHigh],
	)
}

func detectOscillation(hashes []string) bool {
	if len(hashes) < 4 {
		return false
	}
	recent := hashes[len(hashes)-4:]
	return recent[0] == recent[2] && recent[1] == recent[3] && recent[0] != recent[1]
}

func scoreImproving(states []schemas.IterationState) bool {
	s := scores(states)
	for i := 0; i < len(s)-1; i++ {
		if s[i+1] > s[i] {
			return true
		}
	}
	return false
}

func scores(states []schemas.IterationState) []float64 {
	out := make([]float64, len(states))
	for i, state := range states {
		out[i] = ComputeScore(state)
	}
	return out
}

func strictlyDecreasing(values []float64) bool {
	for i := 0; i < len(values)-1; i++ {
		if values[i+1] >= values[i] {
			return false
		}
	}
	return true
}

func countTests(results []schemas.TestRunResults, status string) int {
	count := 0
	for _, result := range results {
		for _, tc := range result.Tests {
			if tc.Status == status {
				count++
			}
		}
	}
	return count
}

// authoredTestFiles flattens the test generator's written files. These are the
// only files whose tests count as authored this run.
func authoredTestFiles(outputs []schemas.TestGeneratorOutput) []schemas.FileChange {
	var files []schemas.FileChange
	for _, output := range outputs {
		files = append(files, output.Files...)
	}
	return files
}

// splitTestCounts partitions test results into preexisting and authored. A
// test is authored when its top-level name is declared as a function in one of
// the files the test generator wrote this run; everything else is preexisting.
// The aggregate pass/fail/errored totals are unchanged; this is additive.
func splitTestCounts(results []schemas.TestRunResults, authoredFiles []schemas.FileChange) (preexisting, authored schemas.TestCounts) {
	authoredNames := authoredFuncNames(authoredFiles)
	for _, result := range results {
		for _, tc := range result.Tests {
			name := topLevelTestName(tc.Name)
			counts := &preexisting
			if _, ok := authoredNames[name]; ok {
				counts = &authored
			}
			switch tc.Status {
			case "passed":
				counts.Pass++
			case "failed":
				counts.Fail++
			case "errored":
				counts.Errored++
			}
		}
	}
	return preexisting, authored
}

// authoredFuncNames returns the set of function names declared in the given
// files, so a test runner result can be matched back to the file that declared
// it. Matching is declaration-based (func Name( ... )), which is deterministic
// and framework-agnostic for Go-style test functions.
func authoredFuncNames(files []schemas.FileChange) map[string]struct{} {
	names := make(map[string]struct{})
	for _, file := range files {
		for _, match := range funcDeclRe.FindAllStringSubmatch(file.Content, -1) {
			if len(match) >= 2 {
				names[match[1]] = struct{}{}
			}
		}
	}
	return names
}

// topLevelTestName strips a subtest suffix (Go's TestXxx/subtest) so a
// subtest result attributes to its declaring function.
func topLevelTestName(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		return name[:i]
	}
	return name
}

// funcDeclRe matches a top-level function declaration name like
// "func TestAdd(t *testing.T)". It does not match methods or non-func uses.
var funcDeclRe = regexp.MustCompile(`func\s+(\w+)\s*\(`)

func countBySeverity(outputs []schemas.VerificationReport) map[schemas.Severity]int {
	counts := make(map[schemas.Severity]int)
	for _, report := range outputs {
		for _, finding := range report.Findings {
			counts[finding.Severity]++
		}
	}
	return counts
}

func countStageStatus(records []schemas.StageRecord, status schemas.StageStatus) int {
	count := 0
	for _, r := range records {
		if r.Status == status {
			count++
		}
	}
	return count
}

func codeSizeBytes(outputs []schemas.CodeWriterOutput) int {
	size := 0
	for _, output := range outputs {
		for _, file := range output.Files {
			if file.ChangeType != "delete" {
				size += len([]byte(file.Content))
			}
		}
	}
	return size
}

func stateHash(outputs []schemas.CodeWriterOutput) string {
	digest := sha256.New()
	var entries [][3]string
	for _, output := range outputs {
		for _, file := range output.Files {
			entries = append(entries, [3]string{file.Path, file.ChangeType, file.Content})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i][0] != entries[j][0] {
			return entries[i][0] < entries[j][0]
		}
		if entries[i][1] != entries[j][1] {
			return entries[i][1] < entries[j][1]
		}
		return entries[i][2] < entries[j][2]
	})
	for _, entry := range entries {
		digest.Write([]byte(entry[0]))
		digest.Write([]byte(entry[1]))
		digest.Write([]byte(entry[2]))
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func aggregateConfidence(outputs []schemas.HarnessStageOutput) float64 {
	if len(outputs) == 0 {
		return 1.0
	}
	confidence := outputs[0].Confidence
	for _, output := range outputs[1:] {
		if output.Confidence < confidence {
			confidence = output.Confidence
		}
	}
	return confidence
}

func tokensConsumed(records []schemas.StageRecord) int {
	total := 0
	for _, record := range records {
		// TokensCached and TokensCacheWrite are subsets of TokensInput; TokensReasoning
		// is a subset of TokensOutput. Summing the subsets would double-count.
		total += record.TokensInput + record.TokensOutput
	}
	return total
}

// tokensGenerated sums output-side spend only (completion plus reasoning;
// reasoning is already a subset of TokensOutput). The trajectory budget gates
// on this, not on input-inclusive consumption.
func tokensGenerated(records []schemas.StageRecord) int {
	total := 0
	for _, record := range records {
		total += record.TokensOutput
	}
	return total
}

func countDiffLines(diffText string) (added, removed int) {
	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			added++
		} else if strings.HasPrefix(line, "-") {
			removed++
		}
	}
	return added, removed
}

func sortedPaths(files []schemas.ChangedFile) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	sort.Strings(paths)
	return paths
}

func recentItems(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[len(items)-n:]
}

func typedPayloads[T any](outputs []schemas.HarnessStageOutput, key string) ([]T, error) {
	var results []T
	for i, output := range outputs {
		if output.Data == nil {
			continue
		}
		raw, ok := output.Data[key]
		if !ok || raw == nil {
			continue
		}
		var target T
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("stage output %d, key %q: marshal: %w", i, key, err)
		}
		if err := json.Unmarshal(b, &target); err != nil {
			return nil, fmt.Errorf("stage output %d, key %q: unmarshal: %w", i, key, err)
		}
		results = append(results, target)
	}
	return results, nil
}
