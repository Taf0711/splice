package splice

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
			state.LintIssuesBySeverity[schemas.SeverityHigh]*3 -
			state.LintIssuesBySeverity[schemas.SeverityMedium]*1 -
			state.SecurityIssuesBySeverity[schemas.SeverityCritical]*50 -
			state.SecurityIssuesBySeverity[schemas.SeverityHigh]*20 -
			state.TypeErrors*2,
	)
}

// EvaluateTrajectory evaluates trajectory rules over an iteration-state history.
func EvaluateTrajectory(history []schemas.IterationState, maxIterations int, tokenBudget *int) schemas.TrajectoryDecision {
	var currentScore, initialScore *float64
	if len(history) > 0 {
		s := ComputeScore(history[len(history)-1])
		currentScore = &s
	}
	if len(history) > 0 {
		s := ComputeScore(history[0])
		initialScore = &s
	}

	decision := func(action schemas.TrajectoryAction, reason string, evidence []string) schemas.TrajectoryDecision {
		return schemas.TrajectoryDecision{
			Action:         action,
			Reason:         reason,
			IterationCount: len(history),
			CurrentScore:   currentScore,
			InitialScore:   initialScore,
			Evidence:       evidence,
		}
	}

	if len(history) == 0 {
		return decision(schemas.ActionContinue, "No iteration history to evaluate.", nil)
	}

	if len(history) >= maxIterations {
		return decision(schemas.ActionAbortHardLimit, "Maximum iteration count reached.",
			[]string{fmt.Sprintf("iterations=%d", len(history)), fmt.Sprintf("max_iterations=%d", maxIterations)})
	}

	tokensConsumed := 0
	for _, state := range history {
		tokensConsumed += state.TokensConsumed
	}
	if tokenBudget != nil && tokensConsumed >= *tokenBudget {
		return decision(schemas.ActionAbortBudget, "Token budget reached.",
			[]string{fmt.Sprintf("tokens_consumed=%d", tokensConsumed), fmt.Sprintf("token_budget=%d", *tokenBudget)})
	}

	stateHashes := make([]string, len(history))
	for i, state := range history {
		stateHashes[i] = state.StateHash
	}
	if detectOscillation(stateHashes) {
		return decision(schemas.ActionEscalateOscillation, "State hashes show a repeated oscillation pattern.",
			[]string{fmt.Sprintf("recent_hashes=%v", recentItems(stateHashes, 4))})
	}

	if slices.Contains(stateHashes[:len(stateHashes)-1], stateHashes[len(stateHashes)-1]) {
		return decision(schemas.ActionEscalateCycleDetected, "Current state hash was seen before.",
			[]string{fmt.Sprintf("state_hash=%s", stateHashes[len(stateHashes)-1])})
	}

	if len(history) >= 3 && currentScore != nil && initialScore != nil && *currentScore < *initialScore {
		return decision(schemas.ActionRollback, "Current score regressed below the initial score.",
			[]string{fmt.Sprintf("initial_score=%v", *initialScore), fmt.Sprintf("current_score=%v", *currentScore)})
	}

	if len(history) >= 3 && !scoreImproving(history[len(history)-3:]) {
		return decision(schemas.ActionStepBack, "Score has not improved across the last three iterations.",
			[]string{fmt.Sprintf("recent_scores=%v", scores(history[len(history)-3:]))})
	}

	recentConfidences := make([]float64, 0, 3)
	for _, state := range history[max(0, len(history)-3):] {
		recentConfidences = append(recentConfidences, state.Confidence)
	}
	if len(recentConfidences) == 3 && strictlyDecreasing(recentConfidences) {
		return decision(schemas.ActionSurfaceToUser,
			"Confidence is strictly decreasing across the last three iterations. Rolling back to best snapshot and retrying with revised context.",
			[]string{fmt.Sprintf("recent_confidences=%v", recentConfidences)})
	}

	return decision(schemas.ActionContinue, "Trajectory remains within safe bounds.", nil)
}

// ComputeIterationState computes the deterministic state vector for one pipeline pass.
func ComputeIterationState(iteration int, stageOutputs []schemas.HarnessStageOutput, stageRecords []schemas.StageRecord, changeSummary schemas.ChangeSummary, timestamp *float64) (schemas.IterationState, error) {
	testResults, err := typedPayloads[schemas.TestRunResults](stageOutputs, "test_results")
	if err != nil {
		return schemas.IterationState{}, fmt.Errorf("test_results: %w", err)
	}
	staticOutputs, err := typedPayloads[schemas.VerificationReport](stageOutputs, "static_analyzer_output")
	if err != nil {
		return schemas.IterationState{}, fmt.Errorf("static_analyzer_output: %w", err)
	}
	securityOutputs, err := typedPayloads[schemas.VerificationReport](stageOutputs, "security_auditor_output")
	if err != nil {
		return schemas.IterationState{}, fmt.Errorf("security_auditor_output: %w", err)
	}
	codeWriterOutputs, err := typedPayloads[schemas.CodeWriterOutput](stageOutputs, "code_writer_output")
	if err != nil {
		return schemas.IterationState{}, fmt.Errorf("code_writer_output: %w", err)
	}

	linesAdded, linesRemoved := countDiffLines(changeSummary.DiffText)

	ts := float64(time.Now().UnixNano()) / 1e9
	if timestamp != nil {
		ts = *timestamp
	}

	return schemas.IterationState{
		Iteration:                iteration,
		Timestamp:                float64(ts),
		TestsPassing:             countTests(testResults, "passed"),
		TestsFailing:             countTests(testResults, "failed"),
		TestsErrored:             countTests(testResults, "errored"),
		LintIssuesBySeverity:     countBySeverity(staticOutputs),
		SecurityIssuesBySeverity: countBySeverity(securityOutputs),
		TypeErrors:               0,
		CodeSizeBytes:            codeSizeBytes(codeWriterOutputs),
		StateHash:                stateHash(codeWriterOutputs),
		Confidence:               aggregateConfidence(stageOutputs),
		TokensConsumed:           tokensConsumed(stageRecords),
		VerificationIncomplete:   countStageStatus(stageRecords, schemas.StageIncomplete),
		FilesChanged:             sortedPaths(changeSummary.ChangedFiles),
		LinesAdded:               linesAdded,
		LinesRemoved:             linesRemoved,
	}, nil
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
		total += record.TokensInput + record.TokensOutput + record.TokensCached
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
