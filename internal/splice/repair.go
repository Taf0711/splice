package splice

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/flags"
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
	priorScope *StageScopePlan,
	wallDeadline time.Time,
	execBudget *StageExecutionBudget,
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
	// A topology may rename a builtin node, so every stage lookup, record, and
	// summary below uses the plan's name for the role.
	writerName := roleStageName(plan, "code_writer")
	testName := roleStageName(plan, "test_runner")
	codeWriterStage, ok := registry[writerName]
	if !ok {
		return false, nil, fmt.Errorf("repair: code_writer stage not registered")
	}
	testRunnerStage, ok := registry[testName]
	if !ok {
		return false, nil, fmt.Errorf("repair: test_runner stage not registered")
	}

	currentOutput := testOutput
	attempts := 0
	totalLatency := 0
	var lastMessage schemas.StageMessage
	progress := newRepairProgressState()
	var lastWriterOutput schemas.HarnessStageOutput
	// repairScope is the scope state for this repair loop. Each re-entry
	// executes with the scope freshly prepared for THAT re-entry and then
	// replaces repairScope, so repair N+1 validates against repair N's
	// plan, never the initial pass pointer. Keyed to this repair call's
	// stage set; an unrelated stage's scope cannot leak in.
	repairScope := priorScope
	// prevStep is the attempt before the next repair boundary: the pass's
	// own change and failure, then each repair in turn (traj_v1 state).
	var prevStep TrajectoryStep

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
		if repairN == 1 {
			prevStep = TrajectoryStep{Change: changeSummaryForStage(*priorSummaries, writerName), Failure: strings.Join(evidence, "\n"), FailureKind: schemas.NodeFailureTestFailure}
		}
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
			emitStageEvent(options, iteration-1, testName, "message", fmt.Sprintf("%s: identical failure with no new evidence after %d repair(s); stopping repair loop", repairNoProgressReason, attempts), 0, nil)
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
			ChangeSummary:    changeSummaryForStage(*priorSummaries, writerName),
			ChangedFiles:     (*priorChangedFiles)[writerName],
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
			From:     testName,
			To:       writerName,
			Kind:     schemas.MessageKindRevisionRequest,
			Evidence: names,
			Payload: schemas.RevisionRequest{
				FailingEvidence: evidence,
				ChangedFiles:    append([]string(nil), (*priorChangedFiles)[writerName]...),
				Instruction:     instruction,
			},
		}
		if err := message.Validate(); err != nil {
			return false, nil, fmt.Errorf("repair: build revision message: %w", err)
		}
		lastMessage = message
		attempts++

		emitStageEvent(options, iteration-1, testName, "message", fmt.Sprintf("revision_request -> code_writer: %d failing tests", len(names)), 0, nil)

		// Re-enter code_writer with the focused revision context.
		writerInput := repairStageInput(runID, writerName, plan, stageNames, *priorSummaries, *priorChangedFiles, &revisionContext)
		writerStart := time.Now()
		var writerScope *StageScopePlan
		writerPlanStage, _ := stageByPlanName(plan, writerName)
		writerCaps := effectiveCaps(writerPlanStage, codeWriterStage.Capabilities())
		writerSelection, selErr := repairSelection(options, provider, writerName, writerPlanStage.Model, false)
		if selErr != nil {
			return false, nil, fmt.Errorf("repair: code_writer re-entry: %w", selErr)
		}
		writerOutput, werr := runRepairStage(ctx, wallDeadline, writerInput, codeWriterStage, iteration, writerSelection, options, workDir, runner, mem, writerCaps, stageBudgetByName(plan, writerName), plan.Tier, tr, repairScope, repairN, &writerScope, execBudget)
		totalLatency += int(time.Since(writerStart).Milliseconds())
		if werr != nil {
			if ctx.Err() != nil || errors.Is(werr, context.Canceled) || errors.Is(werr, errWallTimeExceeded) {
				return false, nil, fmt.Errorf("repair: code_writer re-entry: %w", werr)
			}
			// A failed re-entry stops the repairs, not the run: the pass ends
			// with the failing tests, and the iteration boundary decides
			// (trajectory rules, a fresh budget in the next pass). The
			// re-entry's requests are in the ledger, so its usage joins the
			// code_writer record. (Chain pilot: 15 of 16 failures ended the
			// run here with iterations left.)
			var metered interface{ StageUsage() *schemas.StageUsage }
			if errors.As(werr, &metered) {
				if rec, ok := addRepairUsage(records, iteration, writerName, metered.StageUsage()); ok && tr != nil {
					tr.replaceStageRecord(rec)
					tr.persistPartial(ctx)
				}
			}
			// The failed re-entry is a node result of this pass too.
			noteNodeOutcome(options, iteration, writerName, schemas.StageFailed, failedStageOutput(werr))
			emitStageEvent(options, iteration-1, testName, "message", fmt.Sprintf("repair stopped: code_writer re-entry failed: %v", werr), 0, nil)
			return false, &schemas.InteractionRecord{
				Message:   lastMessage,
				Iteration: iteration,
				Repairs:   attempts,
				Resolved:  false,
				LatencyMs: totalLatency,
			}, nil
		}
		if writerScope != nil {
			repairScope = writerScope
		}
		mergedWriter := mergeRepairRecord(records, iteration, writerName, writerOutput, false)
		// The re-entry is a node result of this pass too, so the pass
		// attribution names its files.
		noteNodeOutcome(options, iteration, writerName, mergedWriter.Status, &writerOutput)
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
			// F4: the same mutation evidence invalidates the admission
			// digest memo. New evidence (recorded writes) means cached
			// content versions may no longer match the worktree.
			if memo := runDigestMemo(); memo != nil {
				for _, files := range stageChangedFilesMap(writerOutput) {
					for _, f := range files {
						memo.Invalidate(workDir, strings.TrimPrefix(f, "./"))
					}
				}
			}
		}
		*outputs = append(*outputs, writerOutput)
		lastWriterOutput = writerOutput
		progress.recordApproach(DerefString(mergedWriter.OutputSummary))

		// Re-run test_runner (model-free: zero usage and zero selection). The
		// re-entry itself already emits running/completed through
		// runStageWithContext; this labeled note is what makes the stream show
		// WHY a second test_runner run exists mid-iteration.
		emitStageEvent(options, iteration-1, testName, "message", fmt.Sprintf("repair re-entry %d: re-running tests", attempts), 0, nil)
		testInput := repairStageInput(runID, testName, plan, stageNames, *priorSummaries, *priorChangedFiles, nil)
		testStart := time.Now()
		var runnerScope *StageScopePlan
		testPlanStage, _ := stageByPlanName(plan, testName)
		testCaps := effectiveCaps(testPlanStage, testRunnerStage.Capabilities())
		newTestOutput, terr := runRepairStage(ctx, wallDeadline, testInput, testRunnerStage, iteration, agent.ModelSelection{}, options, workDir, runner, mem, testCaps, stageBudgetByName(plan, testName), plan.Tier, tr, repairScope, repairN, &runnerScope, execBudget)
		totalLatency += int(time.Since(testStart).Milliseconds())
		if terr != nil {
			return false, nil, fmt.Errorf("repair: test_runner re-run: %w", terr)
		}
		if runnerScope != nil {
			repairScope = runnerScope
		}
		mergedRunner := mergeRepairRecord(records, iteration, testName, newTestOutput, true)
		noteNodeOutcome(options, iteration, testName, mergedRunner.Status, &newTestOutput)
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
			emitStageEvent(options, iteration-1, testName, "repaired", "revision resolved: tests pass", 100, nil)
			(*priorSummaries)[writerName] = *mergedWriter.OutputSummary
			(*priorSummaries)[testName] = *mergedRunner.OutputSummary
			return true, &schemas.InteractionRecord{
				Message:   lastMessage,
				Iteration: iteration,
				Repairs:   attempts,
				Resolved:  true,
				LatencyMs: totalLatency,
			}, nil
		}
		// Only an active classifier can defer an intervention to the pass boundary.
		// Shadow observations and rules fallbacks preserve the repair loop.
		if newResults, ok := newTestOutput.Data["test_results"].(schemas.TestRunResults); ok && options.ClassifierPolicy != nil {
			latestEvidence, _ := extractFailingTests(newResults)
			latest := TrajectoryStep{
				Change:      DerefString(mergedWriter.OutputSummary),
				Failure:     strings.Join(latestEvidence, "\n"),
				FailureKind: schemas.NodeFailureTestFailure,
			}
			repairChange := summarizeWorkspaceChanges(ctx, workDir)
			var repairShadow []schemas.PassEvidence
			var repairPassChanged *bool
			if tracker := options.passEvidence; tracker != nil {
				// A repair runs inside its pass, so its change is the change
				// so far in that pass. The baseline is not moved.
				m := tracker.measure(ctx, iteration, repairChange)
				m.pair.usedPass = options.Flags.Enabled(flags.PassEvidence) && m.ok
				noteDegradedPass(options, iteration, m)
				if m.pair.usedPass {
					repairChange = m.summary
					changed := len(m.summary.ChangedFiles) > 0
					repairPassChanged = &changed
				}
				repairShadow = tracker.shadowList(m.pair)
			}
			repairState, stateErr := ComputeIterationState(iteration, *outputs, *records, repairChange, nil)
			if repairPassChanged != nil {
				// The flag-on state reads the pass builder's progress, as a
				// pass boundary does.
				repairState.PassChanged = repairPassChanged
			}
			if stateErr != nil {
				recordBoundaryProjectionFailure(options, iteration, repairN, stateErr, repairShadow, tr)
			} else {
				boundaryHistory := append(append([]schemas.IterationState{}, options.ClassifierPolicy.history...), repairState)
				rules := evaluateTrajectoryJudgmentsFor(boundaryHistory, nil, attributedWriter(options, plan))
				target, targetOK := selectBestSnapshot(options.ClassifierPolicy.snapshots, iteration)
				boundary := TrajectoryBoundary{
					WriterStage:    writerName,
					RunID:          runID,
					ProjectID:      memoryProjectRoot(options, workDir),
					Intent:         plan.RequestIntent,
					Iteration:      iteration,
					Repair:         repairN,
					History:        boundaryHistory,
					EvidenceShadow: repairShadow,
					Previous:       &prevStep,
					Latest:         &latest,
					Rules:          rules,
					RollbackTarget: targetOK && target.score > ComputeScore(repairState),
				}
				decision, classified := classifyBoundary(ctx, options, boundary, tr)
				if classified && (decision.Action == schemas.ActionStepBack || decision.Action == schemas.ActionSurfaceToUser) {
					emitStageEvent(options, iteration-1, testName, "message", fmt.Sprintf("trajectory monitor: %s; the remaining repairs of this pass are skipped", decision.Action), 0, nil)
					return false, &schemas.InteractionRecord{
						Message:   lastMessage,
						Iteration: iteration,
						Repairs:   attempts,
						Resolved:  false,
						LatencyMs: totalLatency,
					}, nil
				}
			}
			prevStep = latest
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
	emitStageEvent(options, iteration-1, testName, "message", fmt.Sprintf("repair_exhausted: still failing after %d repair(s)", attempts), 0, nil)
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
	ChangedFiles     []string
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
	// The files the run changed are what the repair edits. Naming them
	// puts them in the re-entry's first round (D0 reads the evidence's
	// paths); without them the repair spent its expansion rounds finding
	// its own edits and exhausted the budget (P2b: 6 of 14 failures).
	if len(payload.ChangedFiles) > 0 {
		lines = append(lines, "Files this run changed: "+strings.Join(payload.ChangedFiles, ", "), "")
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
		PriorSummaries:    withRoleAlias(maps.Clone(priorSummaries), plan, "code_writer"),
		PriorChangedFiles: withRoleAlias(cloneChangedFiles(priorChangedFiles), plan, "code_writer"),
		WriterStage:       writerStageName(plan),
		RevisionContext:   revisionContext,
		PipelineStages:    stageNames,
		NextStage:         nextStage,
	}
}

// repairSelection resolves a repair stage's model selection with the same
// precedence as the pass loop: default run selection, then the per-stage
// resolver for model-backed stages; model-free stages get a zero selection.
// A node pin that cannot resolve is an error: repair never swallows it.
func repairSelection(options PipelineRunConfig, provider agent.Provider, stageName string, model *schemas.StageModelConfig, modelFree bool) (agent.ModelSelection, error) {
	selection, err := repairRouteSelection(options, provider, stageName, model, modelFree)
	if err != nil || modelFree {
		return selection, err
	}
	// Fit the effort to the chosen model, as the pass loop does for a normal
	// stage (stageReasoningEffort), so a repair never sends a level its model
	// does not support.
	selection.ReasoningEffort = stageReasoningEffort(options, selection)
	return selection, nil
}

// repairRouteSelection picks the model for a repair re-entry. The effort is
// the raw route effort; repairSelection fits it.
func repairRouteSelection(options PipelineRunConfig, provider agent.Provider, stageName string, model *schemas.StageModelConfig, modelFree bool) (agent.ModelSelection, error) {
	selection := agent.ModelSelection{
		Provider:        provider,
		ProviderName:    options.ProviderName,
		Model:           options.Model,
		ReasoningEffort: options.ReasoningEffort,
	}
	if modelFree {
		return agent.ModelSelection{}, nil
	}
	// Same precedence as the pass loop: a node declaration is the strongest
	// rung, so a repair re-entry resolves the model the pass resolved.
	if model != nil {
		if options.NodeModelResolver == nil {
			return agent.ModelSelection{}, fmt.Errorf("stage %s model: node model pin has no resolver", stageName)
		}
		resolved, rerr := options.NodeModelResolver(stageName, nodeModelOverride(model))
		if rerr != nil {
			return agent.ModelSelection{}, fmt.Errorf("stage %s model: %w", stageName, rerr)
		}
		if resolved.Provider == nil {
			return agent.ModelSelection{}, fmt.Errorf("stage %s model: node model pin resolved no provider", stageName)
		}
		return resolved, nil
	}
	if options.StageModelResolver != nil {
		resolved, rerr := options.StageModelResolver(stageName)
		var tierFallback *stageTierFallback
		if rerr != nil && !errors.As(rerr, &tierFallback) {
			return agent.ModelSelection{}, fmt.Errorf("stage %s model: %w", stageName, rerr)
		}
		if rerr == nil && resolved.Provider != nil {
			return resolved, nil
		}
		if rerr != nil {
			emitNotice(options, fmt.Sprintf("[%s] tier route failed in repair, using the run model: %v", stageName, rerr))
		}
	}
	return selection, nil
}

// scopePlanIsUncomputed reports whether a scope plan is the zero value
// preparation produces when it never ran the Track C bridge (nil memory
// store or a stage that does not consume memory). Such a plan carries no
// authority, so the caller must not let it replace a real prior scope.
func scopePlanIsUncomputed(s StageScopePlan) bool {
	return !s.CognitionResolved && !s.SemanticResolved && len(s.KnownFiles) == 0 &&
		len(s.KnownSymbols) == 0 && s.ExpansionBudget == 0
}

// runRepairStage runs a stage under the pass wall deadline, mirroring the
// pass loop's deadline scoping. Re-entry composes its input through the same
// preparation module as the normal pass, so repair retrieves current memory,
// applies admission and compaction, and records post-compaction counts; it
// receives the full stage budget, not only OutputMax. The freshly prepared
// scope plan (not the caller's prior pointer) executes, and the fresh plan
// flows back through freshScope so the next repair validates against this
// invocation's scope rather than the initial pointer. invocationOrdinal
// separates repair re-entry trace records from the initial-pass record.
func runRepairStage(ctx context.Context, wallDeadline time.Time, input schemas.HarnessStageInput, stage stages.Stage, iteration int, selection agent.ModelSelection, options PipelineRunConfig, workDir string, runner ToolRunner, mem MemoryStore, caps stages.Capabilities, budget schemas.StageBudget, tier schemas.PipelineTier, tr *runTraceAccumulator, priorScope *StageScopePlan, invocationOrdinal int, freshScope **StageScopePlan, execBudget *StageExecutionBudget) (schemas.HarnessStageOutput, error) {
	prepared, freshPlan, _, err := prepareStageInput(ctx, stageInputPreparation{
		Input:         input,
		Stage:         stage,
		Caps:          caps,
		Budget:        budget,
		Tier:          tier,
		Iteration:     iteration,
		WorkDir:       workDir,
		Options:       options,
		Memory:        mem,
		Trace:         tr,
		NowUnix:       time.Now().Unix(),
		PriorScope:    priorScope,
		RepairReentry: true,
	})
	if err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	input = prepared
	// The fresh plan, not the stale prior pointer, governs this execution
	// and becomes the scope the next repair invocation validates against.
	// Preparation only computes a scope plan when the stage consumes memory
	// through a store; when it did not run (nil store), the zero plan is
	// not authoritative and the prior scope persists unchanged.
	plan := freshPlan
	if freshScope != nil {
		if scopePlanIsUncomputed(freshPlan) && priorScope != nil {
			*freshScope = priorScope
		} else {
			*freshScope = &plan
		}
	}

	stageCtx := ctx
	var cancel context.CancelFunc
	if !wallDeadline.IsZero() {
		stageCtx, cancel = context.WithDeadline(ctx, wallDeadline)
	}
	if cancel != nil {
		defer cancel()
	}
	// The freshly prepared scope carries into repair so already-granted
	// files persist; runStageWithContext revalidates via the cognition
	// resolved in THIS repair preparation (partial scope keeps default
	// reads for unresolved questions, so newly needed context is not
	// denied). Historical locations are hints, not stale authority.
	return runStageWithContextBudgeted(stageCtx, input, stage, iteration, selection, options, workDir, runner, mem, caps, budget.OutputMax, tr, &plan, invocationOrdinal, execBudget)
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
// addRepairUsage adds a failed re-entry's usage to the stage's record for
// the iteration, leaving its status and summary as they were.
func addRepairUsage(records *[]schemas.StageRecord, iteration int, stageName string, usage *schemas.StageUsage) (schemas.StageRecord, bool) {
	if usage == nil {
		return schemas.StageRecord{}, false
	}
	for i := range *records {
		rec := &(*records)[i]
		if rec.Name != stageName || rec.Iteration != iteration {
			continue
		}
		existing := &schemas.StageUsage{
			InputTokens:       rec.TokensInput,
			OutputTokens:      rec.TokensOutput,
			CachedInputTokens: rec.TokensCached,
			CacheWriteTokens:  rec.TokensCacheWrite,
			ReasoningTokens:   rec.TokensReasoning,
			WebSearchRequests: rec.WebSearchRequests,
			WebSearchEngine:   rec.WebSearchEngine,
		}
		applyStageUsage(rec, mergeStageUsage(existing, usage))
		return *rec, true
	}
	return schemas.StageRecord{}, false
}

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
		if cmd := testCommandFromOutput(output); cmd != "" {
			rec.TestCommand = cmd
		}
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
	if cmd := testCommandFromOutput(output); cmd != "" {
		rec.TestCommand = cmd
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
