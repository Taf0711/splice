package splice

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// maxLocalRepairs caps the number of code_writer re-entries per pass when the
// test runner keeps reporting failures. The trajectory monitor owns any
// cross-pass recovery; this is only the in-pass repair loop.
const maxLocalRepairs = 2

// repairInstruction is the fixed re-entry directive carried in the revision
// request payload.
const repairInstruction = "The test runner reports failing tests after your implementation. Fix the implementation so these tests pass. Do not rewrite unrelated code."

// attemptLocalRepair re-enters code_writer with a focused revision request when
// test_runner reports failing tests, then re-runs test_runner and merges the
// result. It retries up to maxLocalRepairs times and returns true when a repair
// resolves the failures. Re-invocations MERGE into the existing stage record
// for the iteration (never append a second), because applyRequestLedger keys
// usage by {name, iteration} and a second record would error the run.
func attemptLocalRepair(
	ctx context.Context,
	runID string,
	iteration int,
	plan schemas.ExecutionPlan,
	registry stageRegistry,
	provider agent.Provider,
	options PipelineRunConfig,
	workDir string,
	runner ToolRunner,
	mem MemoryStore,
	tr *runTraceAccumulator,
	wallDeadline time.Time,
	records *[]schemas.StageRecord,
	outputs *[]schemas.HarnessStageOutput,
	priorSummaries *map[string]string,
	priorChangedFiles *map[string][]string,
	testOutput schemas.HarnessStageOutput,
) (repaired bool, interaction *schemas.InteractionRecord, err error) {
	stageNames := make([]string, len(plan.Stages))
	for i, s := range plan.Stages {
		stageNames[i] = s.Name
	}
	codeWriterStage, ok := registry["code_writer"]
	if !ok {
		return false, nil, fmt.Errorf("repair: code_writer stage not registered")
	}
	testRunnerStage, ok := registry["test_runner"]
	if !ok {
		return false, nil, fmt.Errorf("repair: test_runner stage not registered")
	}

	currentOutput := testOutput
	attempts := 0
	totalLatency := 0
	var lastMessage schemas.StageMessage

	for repairN := 1; repairN <= maxLocalRepairs; repairN++ {
		if ctx.Err() != nil {
			return false, nil, context.Canceled
		}
		if !wallDeadline.IsZero() && !time.Now().Before(wallDeadline) {
			return false, nil, errWallTimeExceeded
		}

		results, ok := currentOutput.Data["test_results"].(schemas.TestRunResults)
		if !ok || results.Failed() == 0 {
			// Payload absent or nothing failing: nothing to repair.
			break
		}
		evidence, names := extractFailingTests(results)

		instruction := repairInstruction
		revisionContext := instruction + "\nFailing tests:\n" + strings.Join(evidence, "\n")

		message := schemas.StageMessage{
			ID:       fmt.Sprintf("%s-r%d", runID, repairN),
			RunID:    runID,
			From:     "test_runner",
			To:       "code_writer",
			Kind:     schemas.MessageKindRevisionRequest,
			Evidence: names,
			Payload: schemas.RevisionRequest{
				FailingEvidence: evidence,
				ChangedFiles:    append([]string(nil), (*priorChangedFiles)["code_writer"]...),
				Instruction:     instruction,
			},
		}
		if err := message.Validate(); err != nil {
			return false, nil, fmt.Errorf("repair: build revision message: %w", err)
		}
		lastMessage = message
		attempts++

		emitStageEvent(options, "test_runner", "message", fmt.Sprintf("revision_request -> code_writer: %d failing tests", len(names)), 0, nil)

		// Re-enter code_writer with the focused revision context.
		writerInput := repairStageInput(runID, "code_writer", plan, stageNames, *priorSummaries, *priorChangedFiles, &revisionContext)
		writerStart := time.Now()
		writerOutput, werr := runRepairStage(ctx, wallDeadline, writerInput, codeWriterStage, iteration, repairSelection(options, provider, "code_writer", false), options, workDir, runner, mem, stageOutputMax(plan, "code_writer"), tr)
		totalLatency += int(time.Since(writerStart).Milliseconds())
		if werr != nil {
			return false, nil, fmt.Errorf("repair: code_writer re-entry: %w", werr)
		}
		mergedWriter := mergeRepairRecord(records, iteration, "code_writer", writerOutput, false)
		if tr != nil {
			tr.replaceStageRecord(mergedWriter)
			tr.persistPartial(ctx)
		}
		*outputs = append(*outputs, writerOutput)

		// Re-run test_runner (model-free: zero usage and zero selection). The
		// re-entry itself already emits running/completed through
		// runStageWithContext; this labeled note is what makes the stream show
		// WHY a second test_runner run exists mid-iteration.
		emitStageEvent(options, "test_runner", "message", fmt.Sprintf("repair re-entry %d: re-running tests", attempts), 0, nil)
		testInput := repairStageInput(runID, "test_runner", plan, stageNames, *priorSummaries, *priorChangedFiles, nil)
		testStart := time.Now()
		newTestOutput, terr := runRepairStage(ctx, wallDeadline, testInput, testRunnerStage, iteration, agent.ModelSelection{}, options, workDir, runner, mem, stageOutputMax(plan, "test_runner"), tr)
		totalLatency += int(time.Since(testStart).Milliseconds())
		if terr != nil {
			return false, nil, fmt.Errorf("repair: test_runner re-run: %w", terr)
		}
		mergedRunner := mergeRepairRecord(records, iteration, "test_runner", newTestOutput, true)
		if tr != nil {
			tr.replaceStageRecord(mergedRunner)
			tr.persistPartial(ctx)
		}
		// Outputs follow the same replace semantics as the merged records
		// above: keep exactly one test_results payload for this iteration.
		// typedPayloads counts every payload in passOutputs, so an appended
		// stale pre-repair suite would keep TestsFailing above zero forever
		// and abort a fully repaired pass on budget.
		for idx := len(*outputs) - 1; idx >= 0; idx-- {
			if _, hasResults := (*outputs)[idx].Data["test_results"]; hasResults {
				*outputs = slices.Delete(*outputs, idx, idx+1)
			}
		}
		*outputs = append(*outputs, newTestOutput)
		currentOutput = newTestOutput

		if newResults, ok := newTestOutput.Data["test_results"].(schemas.TestRunResults); ok && newResults.Failed() == 0 {
			emitStageEvent(options, "test_runner", "repaired", "revision resolved: tests pass", 100, nil)
			(*priorSummaries)["code_writer"] = *mergedWriter.OutputSummary
			(*priorSummaries)["test_runner"] = *mergedRunner.OutputSummary
			return true, &schemas.InteractionRecord{
				Message:   lastMessage,
				Iteration: iteration,
				Repairs:   attempts,
				Resolved:  true,
				LatencyMs: totalLatency,
			}, nil
		}
	}

	if attempts == 0 {
		// No repair ran (gate closed, no failing tests, or payload absent).
		return false, nil, nil
	}
	// Exhausted: the test_runner record already reflects the latest failing
	// result via the merge above; the pass continues normally. Distinguish
	// "still failing after N repairs" from "no repair attempted" in streams.
	emitStageEvent(options, "test_runner", "message", fmt.Sprintf("repair_exhausted: still failing after %d repair(s)", attempts), 0, nil)
	return false, &schemas.InteractionRecord{
		Message:   lastMessage,
		Iteration: iteration,
		Repairs:   attempts,
		Resolved:  false,
		LatencyMs: totalLatency,
	}, nil
}

// extractFailingTests returns the "Name: Message" evidence strings (message
// truncated to 200 chars) and the bare names, capped at 10 failures.
func extractFailingTests(results schemas.TestRunResults) (evidence []string, names []string) {
	for _, tc := range results.Tests {
		if tc.Status != "failed" && tc.Status != "errored" {
			continue
		}
		if len(names) >= 10 {
			break
		}
		entry := tc.Name
		if message := strings.TrimSpace(tc.Message); message != "" {
			entry = tc.Name + ": " + truncateString(message, 200)
		}
		evidence = append(evidence, entry)
		names = append(names, tc.Name)
	}
	return evidence, names
}

// repairStageInput builds a fresh HarnessStageInput mirroring the pass input
// construction (run.go), with the given stage name and revision context.
func repairStageInput(runID, stageName string, plan schemas.ExecutionPlan, stageNames []string, priorSummaries map[string]string, priorChangedFiles map[string][]string, revisionContext *string) schemas.HarnessStageInput {
	sequence := 0
	nextStage := ""
	for i, s := range plan.Stages {
		if s.Name == stageName {
			sequence = i + 1
			if i+1 < len(plan.Stages) {
				nextStage = plan.Stages[i+1].Name
			}
			break
		}
	}
	return schemas.HarnessStageInput{
		RunID:             runID,
		StageName:         stageName,
		Sequence:          sequence,
		PlanTier:          plan.Tier,
		RequestIntent:     plan.RequestIntent,
		AcceptanceFacts:   append([]schemas.AcceptanceFact(nil), plan.AcceptanceFacts...),
		PriorSummaries:    maps.Clone(priorSummaries),
		PriorChangedFiles: cloneChangedFiles(priorChangedFiles),
		RevisionContext:   revisionContext,
		PipelineStages:    stageNames,
		NextStage:         nextStage,
	}
}

// repairSelection resolves a repair stage's model selection with the same
// precedence as the pass loop: default run selection, then the per-stage
// resolver for model-backed stages; model-free stages get a zero selection.
func repairSelection(options PipelineRunConfig, provider agent.Provider, stageName string, modelFree bool) agent.ModelSelection {
	selection := agent.ModelSelection{
		Provider:        provider,
		ProviderName:    options.ProviderName,
		Model:           options.Model,
		ReasoningEffort: options.ReasoningEffort,
	}
	if options.StageModelResolver != nil && !modelFree {
		if resolved, rerr := options.StageModelResolver(stageName); rerr == nil && resolved.Provider != nil {
			selection = resolved
		}
	}
	if modelFree {
		selection = agent.ModelSelection{}
	}
	return selection
}

// runRepairStage runs a stage under the pass wall deadline, mirroring the pass
// loop's deadline scoping.
func runRepairStage(ctx context.Context, wallDeadline time.Time, input schemas.HarnessStageInput, stage stages.Stage, iteration int, selection agent.ModelSelection, options PipelineRunConfig, workDir string, runner ToolRunner, mem MemoryStore, outputMax int, tr *runTraceAccumulator) (schemas.HarnessStageOutput, error) {
	stageCtx := ctx
	var cancel context.CancelFunc
	if !wallDeadline.IsZero() {
		stageCtx, cancel = context.WithDeadline(ctx, wallDeadline)
	}
	if cancel != nil {
		defer cancel()
	}
	return runStageWithContext(stageCtx, input, stage, iteration, selection, options, workDir, runner, mem, outputMax, tr)
}

// stageOutputMax returns the output token cap for a named stage, or 0 for the
// provider default.
func stageOutputMax(plan schemas.ExecutionPlan, stageName string) int {
	for _, s := range plan.Stages {
		if s.Name == stageName {
			return s.Budget.OutputMax
		}
	}
	return 0
}

// mergeRepairRecord merges a re-invocation output into the existing record for
// {stageName, iteration}. For a model-backed stage the usage is summed; for a
// model-free stage (zero usage) only status/summary/confidence update. It
// never appends a duplicate record for the iteration.
func mergeRepairRecord(records *[]schemas.StageRecord, iteration int, stageName string, output schemas.HarnessStageOutput, modelFree bool) schemas.StageRecord {
	for i := range *records {
		rec := &(*records)[i]
		if rec.Name != stageName || rec.Iteration != iteration {
			continue
		}
		if !modelFree {
			existing := &schemas.StageUsage{
				InputTokens:       rec.TokensInput,
				OutputTokens:      rec.TokensOutput,
				CachedInputTokens: rec.TokensCached,
				CacheWriteTokens:  rec.TokensCacheWrite,
				ReasoningTokens:   rec.TokensReasoning,
				WebSearchRequests: rec.WebSearchRequests,
				WebSearchEngine:   rec.WebSearchEngine,
			}
			applyStageUsage(rec, mergeStageUsage(existing, output.Usage))
		}
		summary := SummarizeStageOutput(stageName, output)
		rec.OutputSummary = &summary
		rec.Status = schemas.StageCompleted
		rec.Confidence = &output.Confidence
		return *rec
	}
	summary := SummarizeStageOutput(stageName, output)
	rec := schemas.StageRecord{
		Name:          stageName,
		Status:        schemas.StageCompleted,
		Iteration:     iteration,
		OutputSummary: &summary,
		Confidence:    &output.Confidence,
	}
	if !modelFree {
		applyStageUsage(&rec, output.Usage)
	}
	*records = append(*records, rec)
	return rec
}
