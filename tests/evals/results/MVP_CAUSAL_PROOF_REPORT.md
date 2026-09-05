# MVP Causal Proof — Final Report (2026-09-05)

## 1. Verdict

MVP THESIS: PARTIAL

Retrieval, freshness validation, DiscoveryPlan application, and search
suppression all work end to end from naturally captured cognition, and
correctness is retained (warm success never below cold). Measurable
discovery-work elimination in tool counts is NOT demonstrated on this
task/model scale, so the proof is PARTIAL by definition 46.

## 2. What changed (this effort)

Six commits on feat/mvp-paired-proof from origin/dev eb889a3:

1. 3e86fa0 fix+feat: capture defect fixed (sidecar rejected the command
   anchor kind; the loop broke before any fact node persisted).
   DiscoveryPlan (previously dead code) wired into prepareStageInput with
   exact-anchor + semantic retrieval, structural freshness validation,
   MemoryBundle delivery, and broad-search suppression on resolution.
   ResetProject clears per-project graph rows. Nested 3-family fixture
   with verifiers, gold/wrong overlays (15/15 validation matrix), and the
   splice eval mvp paired precursor-target harness.
2. 8d6ba2d capture reads changed files from porcelain git status (the old
   path parsed the writer intent prose and always filtered everything).
3. cda9175 prompts name the target symbols (live runs produced correct
   implementations the over-specified verifiers rejected).
4. 3cdc9d3 semantic-path admission delivers only freshness-validated
   nodes (live run showed anchors_failed=4 alongside resolved=4).
5. 15d7ca0 semantic retrieval scoped to the requesting project (the
   sidecar index ranked all projects, poisoning freshness diffs).
6. ab339b9 + 70bdf1a the anchor story: probe tests pinned that the stage
   sandbox refuses git stash create (write-shaped index access, stderr
   "error: could not write index"), so in-run capture anchors at HEAD.
   The harness commits the verified Task A tree (both arms) and calls the
   new POST /graph/reanchor to advance verified_revision from the
   pre-verify HEAD to the post-verify commit. The commit does not change
   what the run verified, only the revision naming those bytes, so the
   freshness contract is preserved exactly. verifiedRevision surfaces a
   reason when it degrades instead of failing silently.

## 3. Graph capture verification (PR #31 works)

- Sub-agent live probe on the pre-fix binary: [cognition] capture skipped
  (anchor kind "command" rejected), zero nodes.
- Post-fix live smoke: procedure node + file-fact nodes with file and
  symbol anchors, evidence rows with run id and snapshot revision;
  cognition_nodes rows survive a sidecar kill and restart.
- Store-level: persistence across close/reopen
  (TestGraphPersistenceAcrossReopen), reset cascade
  (TestResetProjectClearsGraphRows), GetByIDs/AnchorsFor, Reanchor tests.
  All green.

## 4. Causal task pairs

Three families over the nested session-service fixture (module demo:
cmd/server, internal/session, internal/auth, internal/admin, pkg/errs):

- mvp-01: A implement InvalidateUserSessions and wire it into the
  password reset; B admin ForceSignOut reusing it, wired to
  POST /admin/signout.
- mvp-02: A introduce DefaultTTL constant as the lifetime source of
  truth; B Sweep() reading the same source.
- mvp-03: A ActiveSessionsFor listing; B CountActiveSessions reusing the
  same active-session rule.

Task B prompts name no file paths; cognition must come from Task A.

## 5. Cognition learned (Task A, captured naturally)

Per verified Task A run: 1 procedure node (test anchor: the passing
command) + 1 fact node per changed file (file anchor plus one symbol
anchor per top-level function or method via go/parser; the claim text
names the file and symbols). Task B then retrieved exactly these nodes.

## 6. Retrieval trace (per Task B run, every healthy attempt)

semantic entry (intent text, project-scoped) -> entry nodes with anchors
-> structural freshness (porcelain diff per verified revision, batched,
fail-closed) -> 2 of 3 entry nodes fresh -> bounded 1-hop expansion ->
cognitionBundleFromNodes -> MemoryBundle delivery -> broad search
suppressed. Telemetry per Task B run: discovery_resolved_by_cognition 2,
anchors_validated 2, semantic_hits 3, anchors_failed 1 (the procedure
node has no file anchor and correctly fails closed).

## 7. DiscoveryPlan trace (mvp-02 warm, representative)

- resolved by cognition: which package owns the session lifetime, where
  the lifetime source of truth lives.
- unresolved: the construction site (the intent named cmd/server, a
  modification target the model must read anyway).
- broad FTS search: SKIPPED (the plan resolved a question).

## 8. Cold vs warm (final4, 3 rollouts per arm, GLM-5.3-flash)

Task B outcomes:
- mvp-01: cold 0/3, warm 0/3 (the model cannot complete the composite
  admin-route + reuse task on this model; no warm regression)
- mvp-02: 3/3 vs 3/3 (verifier-proven both arms)
- mvp-03: 3/3 vs 3/3 (verifier-proven both arms)

Task B medians (cold vs warm):
- tokens: 5005 / 5187 / 5979 vs 5326 / 5544 / 6328
- discovery reads: 5 vs 5; searches: 0 vs 0
- paired token deltas: +300 / +360 / +170 (delivered cognition costs
  prompt bytes; the tasks are too small for delivery to pay off)

Primary gate (warm discovery work < cold) NOT met on tool counts. Warm
success >= cold MET everywhere (no correctness regression).

## 9. Regression result

- Full suites green: internal/splice, internal/memd, memd module.
  internal/cli green except two sandbox-refusal tests that fail
  identically on pristine dev eb889a3 in /tmp worktrees
  (location-dependent environment, not this change).
- The repair improvements from the vertical slice are untouched: no
  repair.go, failure.go, diagnostics, or trajectory changes in this
  branch; repair_count telemetry paths unchanged.

## 10. Remaining bottleneck (one)

Discovery savings are invisible because the evaluated tasks are too
small for the mechanism to pay: the model reads about 5 files either
way, and the delivered cognition adds about 300 prompt tokens while
eliminating only the broad search (which this model rarely used). The
lever is a task family large enough that locating the right files is the
dominant cost (multi-package repo, 30+ files, a verifier requiring a
symbol the model cannot guess). The mechanism to prove it (reanchor +
scoped semantic + validated admission + suppression) is in place and
telemetry-proven: resolved_by_cognition=2 with anchors_validated=2 on
every healthy Task B attempt.

## Postscript: why the eval rounds fluctuated

Three real defects were found and fixed by comparing rounds: capture
broke before any fact node persisted (anchor kind plus a prose-parsed
file list), semantic admission delivered stale nodes (false-positive
resolution), and semantic retrieval crossed project boundaries. The
anchor-defect chain (sandbox refuses stash create, silent HEAD fallback,
all anchors stale) was found by probe tests with stderr capture and
fixed with the harness-level commit+reanchor contract rather than
widening the sandbox. Each fix has a pinning test.
