package splice

import (
	"context"
	"fmt"
	"maps"
	"regexp"
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

// repairNoProgressReason is the typed abort reason surfaced when the repair
// no-progress guard stops the loop.
const repairNoProgressReason = "repair_no_progress"

// repairInstruction is the fixed re-entry directive carried in the revision
// request payload.
const repairInstruction = "The test runner reports failing tests after your implementation. Fix the implementation so these tests pass. Do not rewrite unrelated code."

// repairTestFileInstruction replaces the generic instruction when every
// failing test attributes to a file the writer authored this run: the failing
// code is the test file, not the implementation.
const repairTestFileInstruction = "The tests you wrote fail. Fix the test file, or the implementation if the tests exposed a real defect. Do not rewrite unrelated code."

// repairNoProgressConstraint is appended to the revision context when the
// guard fired on the final attempt.
const repairNoProgressConstraint = "No-progress guard: the previous attempts produced the same failure with no new evidence. Do not repeat a change you already made. Read the failing files and change the approach."

// repairProgressState is the per-repair-trajectory no-progress guard state.
// It lives across attemptLocalRepair's loop (passed by pointer) and tracks
// failure fingerprints, evidence, attempted approaches, and applied content
// hashes so a stuck loop is detected deterministically.
type repairProgressState struct {
	fingerprints    map[string]struct{}
	evidenceSeen    map[string]struct{}
	approaches      []string
	lastWriteHashes map[string]string
	stalled         int
}

func newRepairProgressState() *repairProgressState {
	return &repairProgressState{
		fingerprints:    map[string]struct{}{},
		evidenceSeen:    map[string]struct{}{},
		lastWriteHashes: map[string]string{},
	}
}

// observe records one failing-payload observation and reports the two guard
// inputs: repeatedNoEvidence is true when this fingerprint was seen before
// with no new evidence (the directive's stop condition after one
// evidence-informed retry), and stalled is the consecutive no-progress input
// count, where a byte-identical repeat write counts even when the evidence
// text changed. Any real change resets the count.
func (s *repairProgressState) observe(fingerprintHash string, evidenceHashes []string, writeHashes map[string]string) (repeatedNoEvidence bool, stalledCount int) {
	newEvidence := false
	for _, hash := range evidenceHashes {
		if _, ok := s.evidenceSeen[hash]; !ok {
			newEvidence = true
		}
		s.evidenceSeen[hash] = struct{}{}
	}
	_, fingerprintSeen := s.fingerprints[fingerprintHash]
	s.fingerprints[fingerprintHash] = struct{}{}
	repeatWrites := len(writeHashes) > 0 && sameStringMap(writeHashes, s.lastWriteHashes)
	for path, hash := range writeHashes {
		s.lastWriteHashes[path] = hash
	}

	if (fingerprintSeen && !newEvidence) || repeatWrites {
		s.stalled++
	} else {
		s.stalled = 0
	}
	return fingerprintSeen && !newEvidence, s.stalled
}

// recordApproach appends one attempted-approach summary (bounded).
func (s *repairProgressState) recordApproach(summary string) {
	if summary == "" {
		return
	}
	if len(s.approaches) >= 5 {
		s.approaches = s.approaches[1:]
	}
	s.approaches = append(s.approaches, summary)
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, av := range a {
		if bv, ok := b[key]; !ok || bv != av {
			return false
		}
	}
	return true
}

// evidenceHashes derives the per-evidence hash list the guard compares.
func evidenceHashes(evidence []string) []string {
	hashes := make([]string, 0, len(evidence))
	for _, item := range evidence {
		hashes = append(hashes, NewFailureFingerprint(FailureKindTest, "", 0, item, nil, nil).Hash())
	}
	return hashes
}

// writerContentHashes hashes the files a repair re-entry applied, keyed by
// path, from the writer output payload.
func writerContentHashes(output schemas.HarnessStageOutput) map[string]string {
	cwo, ok := output.Data["code_writer_output"].(schemas.CodeWriterOutput)
	if !ok {
		return nil
	}
	hashes := map[string]string{}
	for _, file := range cwo.Files {
		hashes[file.Path] = NewFailureFingerprint(FailureKindCommand, "", 0, file.Content, nil, nil).Hash()
	}
	if len(hashes) == 0 {
		return nil
	}
	return hashes
}

// repairInstructionDirection picks the revision instruction deterministically.
// When every failing test attributes to a test file the writer authored this
// run (its own code_writer output), the writer must fix its test file;
// otherwise the generic implementation instruction applies. Attribution
// matches the failing test function name against declarations in the
// authored file contents, then falls back to the file mentions in the
// diagnostics for compile errors.
func repairInstructionDirection(failingNames []string, diagnostics string, writerOutput schemas.HarnessStageOutput) string {
	authoredFiles := writerAuthoredTestFiles(writerOutput)
	if len(authoredFiles) == 0 || (len(failingNames) == 0 && goFileMentions(diagnostics) == nil) {
		return repairInstruction
	}
	attributed := false
	for _, name := range failingNames {
		topLevel := topLevelTestName(name)
		inAuthored := false
		for path, content := range authoredFiles {
			if authoredFileDeclares(content, topLevel) {
				inAuthored = true
				_ = path
				break
			}
		}
		switch {
		case inAuthored:
			attributed = true
		case strings.Contains(diagnostics, name):
			// The failure names content that is not in the writer's files.
			return repairInstruction
		}
	}
	// File-mention fallback for compile errors that name no test function.
	if !attributed {
		for _, file := range goFileMentions(diagnostics) {
			if _, ok := authoredFiles[strings.TrimPrefix(file, "./")]; !ok {
				return repairInstruction
			}
			attributed = true
		}
	}
	if attributed {
		return repairTestFileInstruction
	}
	return repairInstruction
}

// writerAuthoredTestFiles returns the .go test files (path to content) from
// one code_writer output payload. Files whose declared functions include at
// least one TestXxx name count as test files.
func writerAuthoredTestFiles(writerOutput schemas.HarnessStageOutput) map[string]string {
	cwo, ok := writerOutput.Data["code_writer_output"].(schemas.CodeWriterOutput)
	if !ok {
		return nil
	}
	files := map[string]string{}
	for _, file := range cwo.Files {
		if !strings.HasSuffix(file.Path, "_test.go") {
			continue
		}
		files[strings.TrimPrefix(file.Path, "./")] = file.Content
	}
	return files
}

// authoredFileDeclares reports whether file content declares the given
// top-level test function (func TestXxx( ... ).
func authoredFileDeclares(content, testName string) bool {
	return regexp.MustCompile(`func\s+` + regexp.QuoteMeta(testName) + `\s*\(`).MatchString(content)
}

// Focused repair payload construction (A5) lives in buildFocusedRevisionContext.

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
	progress := newRepairProgressState()
	var lastWriterOutput schemas.HarnessStageOutput

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

		// A3: the no-progress guard. Every new failing payload is observed;
		// the first observation is the baseline, so a repair that reproduces
		// the identical failure with no new evidence marks a stall on
		// re-entry and the second consecutive stall stops the loop.
		evidence, names := extractFailingTests(results)
		fingerprint := FingerprintFromTestResults(results)
		fingerprintHash := fingerprint.Hash()
		var diagnostics strings.Builder
		for _, tc := range results.Tests {
			if tc.Status == "failed" || tc.Status == "errored" {
				diagnostics.WriteString(tc.Name + ": " + tc.Message + "\n")
			}
		}
		if raw := strings.TrimSpace(results.Stdout); raw != "" {
			diagnostics.WriteString(raw + "\n")
		}

		repeatedNoEvidence, stalledCount := progress.observe(fingerprintHash, evidenceHashes(evidence), writerContentHashes(lastWriterOutput))
		// Stop condition: the identical failure with no new evidence after
		// one evidence-informed retry, or two consecutive no-progress inputs
		// (repeated fingerprint, byte-identical repeat writes, or both).
		if repairN > 1 && (repeatedNoEvidence || stalledCount >= 2) {
			// Same fingerprint and no new evidence after the evidence-informed
			// retry: stop writing and surface the typed outcome.
			emitStageEvent(options, "test_runner", "message", fmt.Sprintf("%s: identical failure with no new evidence after %d repair(s); stopping repair loop", repairNoProgressReason, attempts), 0, nil)
			return false, &schemas.InteractionRecord{
				Message:   lastMessage,
				Iteration: iteration,
				Repairs:   attempts,
				Resolved:  false,
				LatencyMs: totalLatency,
			}, nil
		}

		// A4: deterministic diagnostic resolution over the workspace.
		resolverEvidence := ResolveGoDiagnostics(workDir, diagnostics.String())

		// A5: focused payload. Original intent, change summary, exact
		// failure, fingerprint, resolver evidence, attempted approaches, and
		// (on the final attempt) the no-progress constraint. No transcript.
		instruction := repairInstructionDirection(names, diagnostics.String(), lastWriterOutput)
		revisionContext := buildFocusedRevisionContext(focusedRepairPayload{
			Intent:           plan.RequestIntent,
			ChangeSummary:    changeSummaryForStage(*priorSummaries, "code_writer"),
			FailureText:      strings.Join(evidence, "\n"),
			Fingerprint:      fingerprint,
			Evidence:         resolverEvidence,
			Attempted:        progress.approaches,
			NoProgressStalls: progress.stalled,
			FinalAttempt:     repairN == maxLocalRepairs,
		}, instruction)

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
		writerOutput, werr := runRepairStage(ctx, wallDeadline, writerInput, codeWriterStage, iteration, repairSelection(options, provider, "code_writer", false), options, workDir, runner, mem, stageBudgetByName(plan, "code_writer"), plan.Tier, tr)
		totalLatency += int(time.Since(writerStart).Milliseconds())
		if werr != nil {
			return false, nil, fmt.Errorf("repair: code_writer re-entry: %w", werr)
		}
		mergedWriter := mergeRepairRecord(records, iteration, "code_writer", writerOutput, false)
		if tr != nil {
			tr.replaceStageRecord(mergedWriter)
			tr.persistPartial(ctx)
			// C1b: the repair re-entry just applied writer-stage mutations to
			// the working tree. Bump the freshness cache's worktree
			// generation explicitly (the writer output's changed-file record
			// is the mutation evidence), so the next stage input's freshness
			// classification re-proves everything against the new tree
			// instead of trusting a memoized set from before the repair.
			tr.noteSpliceMutation(stageChangedFilesMap(writerOutput))
		}
		*outputs = append(*outputs, writerOutput)
		lastWriterOutput = writerOutput
		progress.recordApproach(DerefString(mergedWriter.OutputSummary))

		// Re-run test_runner (model-free: zero usage and zero selection). The
		// re-entry itself already emits running/completed through
		// runStageWithContext; this labeled note is what makes the stream show
		// WHY a second test_runner run exists mid-iteration.
		emitStageEvent(options, "test_runner", "message", fmt.Sprintf("repair re-entry %d: re-running tests", attempts), 0, nil)
		testInput := repairStageInput(runID, "test_runner", plan, stageNames, *priorSummaries, *priorChangedFiles, nil)
		testStart := time.Now()
		newTestOutput, terr := runRepairStage(ctx, wallDeadline, testInput, testRunnerStage, iteration, agent.ModelSelection{}, options, workDir, runner, mem, stageBudgetByName(plan, "test_runner"), plan.Tier, tr)
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

		newResults, resultsOK := newTestOutput.Data["test_results"].(schemas.TestRunResults)
		if resultsOK && newResults.Failed() == 0 {
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
		// The next loop pass observes the new failing payload through the
		// guard at its top, with this re-entry's applied content hashes as
		// no-progress evidence (byte-identical repeat writes count).
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

// focusedRepairPayload is the A5 focused revision payload: everything the
// re-entered writer needs and nothing else. No transcript.
type focusedRepairPayload struct {
	Intent           string
	ChangeSummary    string
	FailureText      string
	Fingerprint      FailureFingerprint
	Evidence         FocusedEvidence
	Attempted        []string
	NoProgressStalls int
	FinalAttempt     bool
}

// maxPayloadApproaches bounds how many attempted-approach summaries ride in
// one payload.
const maxPayloadApproaches = 3

// buildFocusedRevisionContext renders the focused payload as the revision
// context string carried into the re-entered code_writer.
func buildFocusedRevisionContext(payload focusedRepairPayload, instruction string) string {
	var lines []string
	lines = append(lines, instruction, "")
	if payload.Intent != "" {
		lines = append(lines, "Original intent: "+payload.Intent, "")
	}
	if payload.ChangeSummary != "" {
		lines = append(lines, "Current change summary:", payload.ChangeSummary, "")
	}
	lines = append(lines, "Exact failure:", payload.FailureText, "")
	lines = append(lines, fmt.Sprintf("Failure fingerprint: %s (kind=%s)", payload.Fingerprint.Hash(), payload.Fingerprint.Kind))
	if len(payload.Fingerprint.Symbols) > 0 {
		lines = append(lines, "Fingerprint symbols: "+strings.Join(payload.Fingerprint.Symbols, ", "))
	}
	if len(payload.Fingerprint.Files) > 0 {
		lines = append(lines, "Fingerprint files: "+strings.Join(payload.Fingerprint.Files, ", "))
	}
	lines = append(lines, "")

	if len(payload.Evidence.Symbols) > 0 {
		lines = append(lines, "Resolved symbols:")
		for _, symbol := range payload.Evidence.Symbols {
			lines = append(lines, "  "+symbol)
		}
	}
	if len(payload.Evidence.Lookups) > 0 {
		lines = append(lines, "Workspace lookups:")
		for _, lookup := range payload.Evidence.Lookups {
			lines = append(lines, "  "+truncateString(lookup, 200))
		}
	}
	if len(payload.Evidence.Files) > 0 {
		lines = append(lines, "Named files: "+strings.Join(payload.Evidence.Files, ", "))
	}
	if len(payload.Evidence.Facts) > 0 {
		lines = append(lines, "Diagnostic facts:")
		for _, fact := range payload.Evidence.Facts {
			lines = append(lines, "  "+truncateString(fact, 200))
		}
	}
	if len(payload.Attempted) > 0 {
		lines = append(lines, "", "Attempted approaches:")
		attempted := payload.Attempted
		if len(attempted) > maxPayloadApproaches {
			attempted = attempted[len(attempted)-maxPayloadApproaches:]
		}
		for _, approach := range attempted {
			lines = append(lines, "  "+truncateString(approach, 150))
		}
	}
	if payload.FinalAttempt && payload.NoProgressStalls > 0 {
		lines = append(lines, "", repairNoProgressConstraint)
	}
	return strings.Join(lines, "\n")
}

// changeSummaryForStage returns the prior summary line for one stage, or the
// empty string when the stage has not run.
func changeSummaryForStage(priorSummaries map[string]string, stageName string) string {
	return strings.TrimSpace(priorSummaries[stageName])
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

// runRepairStage runs a stage under the pass wall deadline, mirroring the
// pass loop's deadline scoping. Re-entry composes its input through the same
// preparation module as the normal pass, so repair retrieves current memory,
// applies admission and compaction, and records post-compaction counts; it
// receives the full stage budget, not only OutputMax.
func runRepairStage(ctx context.Context, wallDeadline time.Time, input schemas.HarnessStageInput, stage stages.Stage, iteration int, selection agent.ModelSelection, options PipelineRunConfig, workDir string, runner ToolRunner, mem MemoryStore, budget schemas.StageBudget, tier schemas.PipelineTier, tr *runTraceAccumulator) (schemas.HarnessStageOutput, error) {
	prepared, err := prepareStageInput(ctx, stageInputPreparation{
		Input:     input,
		Stage:     stage,
		Budget:    budget,
		Tier:      tier,
		Iteration: iteration,
		WorkDir:   workDir,
		Options:   options,
		Memory:    mem,
		Trace:     tr,
		NowUnix:   time.Now().Unix(),
	})
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	input = prepared

	stageCtx := ctx
	var cancel context.CancelFunc
	if !wallDeadline.IsZero() {
		stageCtx, cancel = context.WithDeadline(ctx, wallDeadline)
	}
	if cancel != nil {
		defer cancel()
	}
	return runStageWithContext(stageCtx, input, stage, iteration, selection, options, workDir, runner, mem, budget.OutputMax, tr)
}

// stageBudgetByName returns the full stage budget for a named plan stage, or
// the zero budget when the stage is absent.
func stageBudgetByName(plan schemas.ExecutionPlan, stageName string) schemas.StageBudget {
	for _, s := range plan.Stages {
		if s.Name == stageName {
			return s.Budget
		}
	}
	return schemas.StageBudget{}
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
		// Invocation-ordered reviews: a re-entry appends its own review and
		// never replaces the initial invocation's record.
		if output.MemoryReview != nil {
			rec.MemoryReviews = append(rec.MemoryReviews, *output.MemoryReview)
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
	if output.MemoryReview != nil {
		rec.MemoryReviews = []schemas.MemoryReview{*output.MemoryReview}
	}
	if !modelFree {
		applyStageUsage(&rec, output.Usage)
	}
	*records = append(*records, rec)
	return rec
}
