# Matched-run handoff — verification state (2026-09-06)

Source doc: /Users/tafseerhaque/Downloads/SPLICE_MATCHED_RUN_AGENT_HANDOFF.md
Worktree: /private/tmp/mvp-wt (feat/mvp-paired-proof @ 0316bcc, unchanged — no findings pre-fixed)
Evidence files SHA-verified:
- matched: /private/tmp/mvp-match3/families-attempts.jsonl (14 rows, aa8b0bd9…)
- bridge4: /private/tmp/mvp-bridge4/families-attempts.jsonl (24 rows, f883a096…)
- also mirrored in ~/Downloads/splice-mvp-proof/

## Finding status (subagent-verified against checkout)
- F1 CONFIRMED — run.go:1128/1143 gate on priorScope.CognitionResolved; discovery.go:630 sets it false for semantic-only plans → semantic-priority branch (discovery.go:676-696) unreachable in wired path.
- F2 CONFIRMED — discovery.go:683-696 semantic branch prepends KnownFiles then appends default queries verbatim; no dedupe (resolved branch has covered-map at 760-773).
- F3 CONFIRMED — repair.go:588-590 discards fresh scope + suppression; preparation omits PriorScope (stage_input.go:200-204); execution uses old priorScope at repair.go:616.
- F9 CONFIRMED — graph.go:838-846 CaptureSetIDs filters project+revision only (no source_run_id); graph.go:872-908 ReanchorByIDs no transaction, partial update possible; exposed via graph_server.go:372.
- F10 CONFIRMED — trace.go:360-377/406-424 read-modify-write on {stage,iteration} (repair re-entry overwrites); trace.go:372 records remaining ExpansionBudget as scope_expansions.
- F11 CONFIRMED — scope_mode.go:39-57 only consulted at run.go:1124; stage_input.go:233-300 delivers memory bundle regardless of SPLICE_SCOPE_MODE.
- F12 CONFIRMED — mvp_matched.go:72-85 appends Rollouts*2 precursor_failed rows; summarizeMvp (mvp_eval.go:578-584) counts each as setup failure (1 failed snapshot → 6).
- F4 CONFIRMED — pair_eval.go:222-231 verifier runs first, :234-236 writeAttemptArtifacts after (comment at :411-412 contradicts); :425 `git diff HEAD` drops untracked; :429-430 + mvp_matched.go:297-303 treeHash = `git rev-parse HEAD` (commit hash mislabeled).
- F5 CONFIRMED — writeAttemptArtifacts (:417-431) discards all errors; exec failure returns at :207-211 BEFORE verifier/artifacts; familyPairRow (families_eval.go:139-209) has no artifact fields; VerifierOutputPath/PatchPath never copied into rows.
- F6 CONFIRMED — mvp_matched.go:200 raw ctx for Task B (no timeout, unlike families_eval.go:347); no row.LatencyMs assignment (:209-218); Task A runCtx created once at :40 outside family loop (cumulative 30min budget), cancel() never invoked.
- F7 CONFIRMED — mvp_matched.go:156-162 equality asserted once pre-loop; treeHash (:298-303) is HEAD commit, trivially identical for both arms; no clean-working-tree check per attempt.
- F8 CONFIRMED — mvp_matched.go:288: hardcoded "completed" status, "go test ./..." command, "snapshot-"+familyID runID; evidence fabricated (discovery.go:447 "test command exited 0"), real Task A trace never read back.
- Related: mvp_matched.go:55-57 wraps stale err instead of resetErr; :84 discards snapErr; families_eval.go retry rows record base sessionID (retried traces unmatched); WarmSetupValid never serialized (families_eval.go:207).
- F4-F8: second subagent (task-0 log shows F4 artifact-after-verifier collection confirmed, writeAttemptArtifacts after verifier run) — full report landing.

## Seam map (subagent 3)
- eval.RunFunc internal/eval/harness.go:57 is the single runner seam; RunInput {SessionID, Memory, Prompt, Cwd, Check, OutputPath, ArtifactDir}; RunOutput {Success, Tokens, TelemetryFound, ToolCalls, FileReads, ...}.
- Existing mvp_eval_test.go tests helpers + marker parsing only; no real-runner-seam coverage.
- Fixtures: tests/evals/large-families/{fixture,validate(_gold-a/_gold-b/_wrong, validate_large.py),verifiers(large-01-a.sh..large-02-b.sh)}; families JSON: tests/evals/large-families/cognition-mvp-families.json (nested mvp-families copy exists too).
- Attempt rows written to families-attempts.jsonl even in mvp mode (no separate mvp writer).

## Related defects noted
- scope-off still builds scope plan + advances priorScope (run.go:893) though never applied.
- Semantic branch skips covered-read accounting → fixing F1 without F2 inflates ContextQueriesExecuted.
- Repair scope plan never inherits ExpansionBudget (discovery.go:597-599 never sees repair-time plans).
- CaptureSetIDs conflated only when two runs share same verified revision (callers pass unique preHead today, mvp_eval.go:534 / mvp_matched.go:107).

## Evidence cross-check vs handoff
- Billing snapshot: success:false, tokens 9394, trace_status:completed, session mvp-snap-large-01-…-taska ✓
- 6 skipped B billing slots (rows 1-6) ✓ (one failed precursor, not six failures)
- Retention: cold 6095/6130/6013, warm 6369/6289/6250 → +670 total (+3.674%) ✓
