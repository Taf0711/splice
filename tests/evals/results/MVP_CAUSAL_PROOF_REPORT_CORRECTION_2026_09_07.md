# Dated correction, 2026-09-07 (matched-run stabilization assignment)

An implementation assignment (SPLICE_MATCHED_RUN_AGENT_HANDOFF.md, reviewed
commit 0316bcc) verified the findings below against the checkout and landed
fixes on this branch. This appendix corrects interpretations in the report
above without rewriting it. Prior raw results are preserved. Each fixed
contract links to its regression test. Empirical claims link to the
experiment artifacts, not to code.

## Corrected claims

1. ONE failed precursor, not repeated target failures.
   The matched billing file contains one failed Task A snapshot and six
   skipped target slots. The earlier phrasing suggested repeated target
   executions. Row arithmetic in
   /private/tmp/mvp-match3/families-attempts.jsonl (sha256 aa8b0bd9...) shows
   1 executed billing Task A plus 6 placeholder rows. Fixed by F12;
   pinned by TestOneFailedPrecursorCountsOneSetupFailureAndCorrectSkips
   (internal/cli/mvp_matched_test.go).

2. The billing failure cause is unknown without verifier evidence.
   The failed snapshot consumed 9,394 tokens with trace_status "completed"
   and no recoverable verifier artifact. Model incapability is NOT
   established. The artifact defects (F4: capture after verifier; F5: errors
   discarded, exec failures returning before capture) are plausible
   non-model explanations and are now fixed (package A).

3. The earlier 8-to-2 read reduction did not require exact-anchor retrieval.
   The semantic-priority helper existed but was UNREACHABLE through the
   runtime gate (F1: the gate required CognitionResolved, which semantic-only
   plans set false). The earlier reduction cannot be attributed to any
   retrieval policy that actually executed. Fixed by F1+F2; pinned by
   TestSemanticOnlyPlan_ReachesContextSwapAtCallSite,
   TestSemanticPriorityHintedDuplicate_MovesNotDuplicates,
   TestSemanticOnlyPlan_DoesNotNarrowToolRunner
   (internal/splice/scope_runtime_test.go).

4. Semantic-priority helper existence did not prove runtime execution.
   TestSemanticOnlyPlan_PrioritizesWithoutNarrowing called the helper
   directly. Helper tests cannot prove the runtime reaches the helper. The
   call-site tests listed above now run through runStageWithContext.

5. Initial commit equality was not a per-attempt clean-working-tree
   assertion (F7). The matched loop asserted once before the target loop,
   comparing HEAD commits (trivially identical across arms). A dirty tree
   can coexist with the correct HEAD. Fixed: every B now re-asserts commit
   AND HEAD^{tree} AND clean index/working tree (including untracked files)
   immediately before launch, with NUL-delimited porcelain parsing.
   Pinned by TestCleanSnapshotRejectsModifiedTrackedFileAtSameHead,
   TestCleanSnapshotRejectsUnexpectedUntrackedFile,
   TestCleanSnapshotDistinguishesCommitFromTreeMismatch,
   TestRematerializationClearsPreviousAttemptLeftovers
   (internal/cli/mvp_matched_b_test.go). Commit and tree are separate row
   fields (FixtureCommit/FixtureTree, StartCommit/StartTree).

6. Reconstructed capture with substituted provenance is not proof of
   automatic persisted capture (F8). seedCognitionFromSnapshot hardcoded
   the "completed" status, the "go test ./..." command, and a synthetic
   "snapshot-<familyID>" run ID, and fabricated a "test command exited 0"
   evidence record. Fixed: the seed freezes the actual Task A session ID
   and the actually-executed verifier command (explicit "unknown" marker
   when unrecoverable), replays the frozen payload with only the project
   identity remapped, and carries a Reconstructed flag.
   Pinned by TestSeedCaptureSetCarriesRealRunIDAndExecutedCommand,
   TestSeedCaptureSetUnknownCommandIsExplicitNotFabricated,
   TestReplaySeedCapturesRemapsProjectOnly,
   TestFailedTaskAProducesNoCaptures (internal/cli/mvp_matched_b_test.go).
   Claims about automatic capture still rest only on live-run evidence.

7. One failed snapshot and six skipped slots are different counts. The
   summary now reports them separately (see 1).

8. Scope-off does not by itself make model input identical to cold (F11).
   SPLICE_SCOPE_MODE=off gates context acquisition only. Prompt memory
   delivery is independent (gated by SPLICE_EXEMPLAR_MODE and the memory
   flag). Scope-off with prompt delivery is the delivery-only condition.
   Pinned by TestScopeOffKeepsMemoryDeliveryUnchanged,
   TestScopeOnDeliversSameMemory (internal/splice/treatment_seam_test.go),
   and the typed treatment matrix (internal/splice/treatment.go,
   TestResolveTreatmentMatrix).

9. A provider model cannot skip host reads that occur before its request.
   Context reads are fulfilled host-side before the model's completion
   (runStageWithContext -> FulfillContextRequest). Read-count differences
   across arms measure host policy, not model choice.

## Mechanism corrections this appendix does not re-litigate

- Repair executed with the stale scope pointer and omitted PriorScope from
  preparation (F3). Fixed; pinned by TestRepairScopeStateCarriesAcross
  RepairInvocations and TestRepairExecutesWithFreshlyPreparedScope
  (internal/splice/repair_scope_test.go).
- Trace metrics overwrote {stage, iteration} on repair re-entry, and the
  remaining expansion budget was recorded as expansions performed (F10).
  Fixed with invocation ordinals and a measured ExpansionsPerformed counter;
  pinned by TestTraceKeepsSeparateRecordsPerInvocation
  (internal/splice/repair_scope_test.go) and
  TestEightDefaultReadsWithTwoRetainedYieldSixOmitted +
  TestOutlineReplacementIsRecordedAsWork
  (internal/splice/treatment_accounting_test.go).
- CaptureSetIDs conflated runs at the same project+revision, and reanchor
  could partially update (F9). Fixed with an optional producer-run filter
  and a transactional all-or-nothing reanchor; pinned by
  TestCaptureSetIDsBySourceRun, TestReanchorByIDsAllOrNothing,
  TestReanchorByIDsValidationBeforeUpdate (memd/store) and
  TestCaptureSetIDsForRunRoundTrip (internal/memd).
- Verifier verdicts derived from an exit marker could be overridden by
  output text; artifacts were best-effort and invisible to rows (F4, F5).
  Fixed: process exit is authoritative; proposals are captured BEFORE the
  verifier with a complete manifest including untracked files; rows carry
  artifact references, digests, and failure categories. Pinned by
  TestVerifierOutputMarkerCannotOverrideNonzeroExit,
  TestCapturedProposalSurvivesVerifierDestruction,
  TestCapturedProposalIncludesNewUntrackedGoFile,
  TestArtifactWriteFailureVisibleWithoutChangingVerdict,
  TestFillAttemptRowCarriesArtifactPathsFromSeamOutput,
  TestPairEvalRealShellVerifierVerdictStableWithAndWithoutArtifacts,
  TestIndependentPerAttemptTimeoutContexts,
  TestCancellationAfterCompletedTargetRetainsRow,
  TestExecFailureRetainsOutputAndPartialProposal
  (internal/cli/mvp_matched_test.go, internal/cli/mvp_matched_b_test.go).

## Status of the matched-snapshot conclusion

The report's measured outcomes (equal correctness, ~200-token delivery cost,
same 8-file read pattern on large-02) stand as recorded: they describe the
binary and prompts of that run. What changes is interpretation: the
"required exact-anchor retrieval" conclusion rested on a semantic-priority
path that never executed, so the read-reduction lever is untested, not
disproven. The corrected harness (semantic priority reachable, dedupe by
move-not-duplicate, fresh repair scope, per-attempt clean assertions, honest
treatments) is the instrument for that measurement; the diagnostic protocol
and its bounded run are specified in the assignment and have not executed as
of this appendix.

## Environment failures excluded from the above

Three test failures in this macOS environment reproduce identically on the
untouched base commit and are inherited, not regressions:
TestTrustedWorkspaceReadAndWriteDoNotPromptInAskMode (internal/splice),
TestExecScopeReRegistrationSwapsCoreToolsByName and
TestRunSandboxCheckJSONDeniesOutOfWorkspaceWrite (internal/cli). All other
root-module and memd-module tests pass.
