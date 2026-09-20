# Retention sequence confirmation: commit-and-reanchor

Date: 2026-09-13 (UTC). Local wall clock 2026-09-12 evening.
Branch: `wip/evidence-substitution-production` at HEAD
`8c496067ed31e04b1d5f7273ec63b356078897ff` (`8c49606`).
Role: test and eval agent. No product code changed. Nothing committed, staged,
or pushed.

## 0. Verdict

- Part 1 HOLDS. The sequence harness now commits the verified write tree and
  reanchors that run's capture set, and a unit test pins both the passed and the
  failed write cases.
- Part 2 RAN at the attempt ceiling. Run A (cold plus shared warm, 4 attempts)
  was inconclusive: the warm write task failed its verifier, so nothing was
  captured and there was no anchor to check. Run B (shared warm only, 2
  attempts) exercised the missing step: the warm write passed, the harness
  committed and reanchored, and the warm read's discovery admitted the write's
  file-anchored fact as FRESH.
- The literal pass condition "zero failed freshness validation lines" is NOT met.
  The warm read reports `1 anchor(s) failed freshness validation` next to
  `1 question(s) resolved by cognition, 1 node(s)`. The failing anchor is the
  procedure node, which carries only a test anchor; the admission rule fails
  closed for any node with no file anchor. The write's dependency fact, which
  carries a `clock_test.go` file anchor, was admitted.
- The read was not satisfied by memory. It received the delivered node but no
  substitution fired, it still issued two `request_context` expansions, and its
  verifier failed for model-output reasons. Memory and expansion are reported
  separately below.
- No cost or token saving is claimed. The total-cost claim is withheld.

## 1. Harness change

Files changed, all test and eval harness code:

- `tests/evals/warmcost/runner.go`
- `tests/evals/warmcost/types.go`
- `tests/evals/warmcost/warmcost_test.go`
- `tests/evals/warmcost/README.md`
- `tests/evals/warmcost/paid-run.sh`

The commit-and-reanchor step, mirroring `internal/cli/mvp_eval.go:372` and
`reanchorVerifiedCognition` (`internal/cli/mvp_eval.go:733`):

1. `runSequence` runs a write-phase task, then, when the task's verifier passed,
   calls `commitAndReanchor` before the next task of the sequence runs.
2. `commitAndReanchor` records `preHead`, stages and commits the verified tree
   with a local identity, records `postHead`, and fails loud when a dirty tree
   produces no new revision. It commits in every arm, so the workspace treatment
   is identical, and it reanchors only when the arm has memory on, because a cold
   arm captures nothing.
3. The reanchor resolves the sidecar for the sequence's sidecar class
   (`memd.NewClient` on `<sidecar-root>/<class>/mem.sock`, or `memd.Resolve` for
   the ambient sidecar), canonicalizes the workspace path, and calls
   `client.CaptureSetIDsForRun(ctx, project, preHead, producerRunID)` followed by
   `client.ReanchorGraphByIDs(ctx, project, ids, preHead, postHead)`.
   `producerRunID` is the write run's pipeline run id, now recorded on the
   attempt as `run_id`.
4. A write task whose verifier failed is never committed.

`paid-run.sh` now prints the cap as `arms x tasks x repeats`. It previously
printed `tasks x repeats` and dropped the arm count.

Unit tests added:

- `TestRunSequenceCommitsAndReanchorsAfterVerifiedWrite` proves the workspace
  HEAD moves (at least two commits at reanchor time) and the reanchor is invoked
  once with distinct revisions and the write run's id.
- `TestRunSequenceFailedWriteDoesNotCommitOrReanchor` proves a failed write
  invokes neither.

## 2. Corpus and validation

`tests/evals/warmcost/retention-taskset/`, unchanged: `clock-helper-write`
(phase write) adds `newClockStore` to `clock_test.go` and routes `runClockTable`
through it; `clock-helper-read` (phase read) adds `TestStoreExpiryBoundary` in a
new file reusing `runClockTable`. The read prompt names no file, so its
dependency is not fetched by the default context request, and the two tasks
share one workspace, so the write's bytes persist for the read.

Validation (`taskset-v0/validate/validate.py`): 2 tasks validated, 0 broken. Each
task fails its verifier on the untouched fixture, passes with its gold patch, and
fails with its wrong patch.

## 3. Run facts

Both runs used model `z-ai/glm-5.3-flash`, `RETENTION=shared`, `REPEATS=1`,
`MAX_RETRIES=0`, the same corpus, and HEAD `8c49606`. The pre-run facts (command,
model, task list, repeat count, cap) were printed before each first request.

Run A. Exact command:

    ARMS=cold,warm REPEATS=1 MAX_RETRIES=0 RETENTION=shared \
    MODEL=z-ai/glm-5.3-flash \
    TASKSET_SRC=tests/evals/warmcost/retention-taskset \
    TASKS_ENV="clock-helper-write clock-helper-read" \
    SIDECAR_ROOT=/tmp/warmcost-retention-seq-sidecar \
    RUN_BASE=wc-retention-confirm-20260913T004622Z \
    tests/evals/warmcost/paid-run.sh

- Run id `wc-retention-confirm-20260913T004622Z-try0`. Wall time 20:46:22 to
  20:48:17 (about 2 minutes). Cap: 1 sequence x 2 arms x 1 repeat = 2 sequence
  runs, 4 task attempts. Used 4.

Run B. Exact command:

    ARMS=warm REPEATS=1 MAX_RETRIES=0 RETENTION=shared \
    MODEL=z-ai/glm-5.3-flash \
    TASKSET_SRC=tests/evals/warmcost/retention-taskset \
    TASKS_ENV="clock-helper-write clock-helper-read" \
    SIDECAR_ROOT=/tmp/warmcost-retention-confirm2-sidecar \
    RUN_BASE=wc-retention-confirm2-20260913T011502Z \
    tests/evals/warmcost/paid-run.sh

- Run id `wc-retention-confirm2-20260913T011502Z-try0`. Wall time 21:15:02 to
  21:17:30 (about 2.5 minutes). Cap: 1 sequence x 1 arm x 1 repeat = 1 sequence
  run, 2 task attempts. Used 2.
- PART 2 total task attempts: 4 + 2 = 6, at the stated ceiling of 6. A third
  launch at 20:57 UTC aborted in `go build` before any provider request and used
  0 attempts.

## 4. Answers

### 4.1 Freshness: admitted or rejected?

Run A is inconclusive. The warm write task's verifier failed
(`./probe_newclock_test.go:9:20: undefined: newClockStore`), so the pipeline
status was `failed`, nothing was captured, and no anchor existed to check. The
zero failed-freshness count in run A is therefore vacuous.

Run B, the confirmation. The warm write passed its verifier and the pipeline
status was `completed`, so `commitAndReanchor` committed the verified tree and
reanchored the run's capture set. The captured stream shows:

- Write task: `[cognition] anchored at HEAD (snapshot unavailable: exit status 1)`,
  then `[cognition] captured node 1 (procedure)` and
  `[cognition] captured node 2 (fact)`. This is the in-run anchor at the
  pre-commit HEAD.
- Read task, for each of its two iterations:
  `[code_writer] discovery: 1 anchor(s) failed freshness validation` and
  `[code_writer] discovery: 1 question(s) resolved by cognition, 1 node(s)`.

The sidecar confirms which anchor failed and which was admitted:

- Node 2, kind `fact`, claim "clock_test.go defines runClockTable, newClockStore,
  TestClockTableHelperSelfCheck", anchors `file:clock_test.go` plus the three
  symbol anchors, `verified_revision = fece68dd1d0c1a0d7bc4a3b96f379d42a20137ed`,
  `source_run_id` = the write run's id.
- Node 1, kind `procedure`, anchor `test:go test ./...` only, same
  `verified_revision`.

The fact carries a file anchor and was admitted as fresh. The procedure carries
no file anchor, so `admitFreshNodes` fails it closed
(`internal/splice/discovery.go:242`: "No file anchor to diff: the node cannot
prove freshness"). That rule is independent of this harness change.

The reanchor is what made the fact admissible. Its revision
`fece68dd...` is not the fixture commit `initGitWorkspace` created; the commit
step created a new revision, and `ReanchorGraphByIDs` moved both nodes from the
pre-commit HEAD to it. The read's diff against that revision is empty for
`clock_test.go` before the read edits anything, so the anchor is FRESH.

Verdict: the write task's file-anchored evidence was ADMITTED. The residual
failed-freshness line is the anchor-less procedure node, not the write's
dependency. The literal zero-line pass condition is not met, and I will not
report it as met.

### 4.2 Retrieval, from captured fields

Run B warm read task, captured stream and ledger:

- Provider calls: 8. `generation` 2, `format_retry` 4, `expansion` 2. Billed USD
  0.0047684. Cost coverage `complete`.
- Direct hits: 1. The stream reports
  `discovery: 1 question(s) resolved by cognition, 1 node(s)` in each iteration.
- Memory items delivered: the same line is the delivery evidence; the harness
  emits no separate memory-admission line for this stage. One cognition node (the
  `clock_test.go` fact) reached the stage's memory bundle.
- Substitutions: 0. The captured stream contains no
  `Evidence-backed exact substitution` marker, and the stage's context handshake
  used the default reason ("Inspect existing project files before writing so
  edits modify real code instead of overwriting it"), not a substitution reason.
  So no admitted record shaped the context request.
- Expansions: 2. The read task emitted two `request_context` actions and the host
  fulfilled both. The first reason: "The intent requires reusing the existing
  runClockTable helper from clock_test.go, but its signature and case fields were
  not delivered in the context views."

### 4.3 Routes, separated

- Memory route: one cognition node was delivered. It states the file and its
  symbol names. It does not carry the helper body or the case-field values.
- Expansion route: the read task still emitted two `request_context` expansions
  for `clock_test.go`, and the host fulfilled them. The read's verifier then
  failed with `no test outside clock_test.go reuses runClockTable`; the task's
  submissions were rejected four times for typed-output format, so no compatible
  test file was applied.
- The read was NOT satisfied by memory. It was routed through
  `request_context` expansion, and that route did not reach a passing verifier in
  this attempt. No expansion is counted as a memory hit.

### 4.4 Per-source spend and coverage

Every number is a captured ledger field. All attempts in both runs have
`cost_coverage = complete`.

Run A (cold plus shared warm):

| task | arm | verifier | status | calls | generation | format_retry | expansion | repair | billed USD |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `clock-helper-write` | cold | PASS | completed | 5 | 2 | 2 | 1 | 0 | 0.00261153 |
| `clock-helper-read` | cold | FAIL | failed | 8 | 2 | 4 | 2 | 0 | 0.00473529 |
| `clock-helper-write` | warm | FAIL | failed | 6 | 2 | 4 | 0 | 0 | 0.00338180 |
| `clock-helper-read` | warm | FAIL | failed | 8 | 2 | 4 | 2 | 0 | 0.00424266 |

Arm totals, run A: cold 13 calls, 3 expansions, USD 0.00734682 (generation
0.00326613 + format_retry 0.00163079 + expansion 0.00244990). Warm 14 calls,
2 expansions, USD 0.00762446 (generation 0.00262277 + format_retry 0.00294659 +
expansion 0.00205510). Both reconstruct their billed total.

Run B (shared warm only):

| task | arm | verifier | status | calls | generation | format_retry | expansion | repair | billed USD |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `clock-helper-write` | warm | PASS | completed | 4 | 1 | 2 | 1 | 0 | 0.00295797 |
| `clock-helper-read` | warm | FAIL | failed | 8 | 2 | 4 | 2 | 0 | 0.00476840 |

Arm total, run B: warm 12 calls, 3 expansions, round share 0.25, USD 0.00772637
(generation 0.00238575 + format_retry 0.00233067 + expansion 0.00300995). The sum
reconstructs the billed total exactly.

Claim gating: the aggregate withholds the total-cost claim ("warm billed cost is
not lower than cold"). No saving is claimed. No cost or token comparison is
meaningful here; one pair and one repeat cannot support one.

### 4.5 If the read retrieved nothing

It retrieved one node, so this section is not the primary outcome. The part that
did not fire is substitution: no substitution-shaped context request appeared.

## 5. Raw artifacts

- `tests/evals/results/wc-retention-confirm-20260913T004622Z-try0/` (run A):
  `raw-<task>-<arm>-r0.jsonl`, `attempt-<task>-<arm>-r0.json`,
  `aggregate.json`, `report.md`.
- `tests/evals/results/wc-retention-confirm2-20260913T011502Z-try0/` (run B):
  the same shape, plus `derived-toolcalls.txt`.
- Also `tests/evals/results/PAID_RUN_wc-retention-confirm-20260913T004622Z-try0.md`,
  `tests/evals/results/PAID_RUN_wc-retention-confirm2-20260913T011502Z-try0.md`,
  and the matching `.log` files.
- Sidecar evidence: `/tmp/warmcost-retention-confirm2-sidecar/shared-warm/mem.db`
  (2 nodes, 2 observations, all project-scoped to the warm workspace). This path
  is outside the repository and may be removed before review.

## 6. What I could not verify, and one environment failure

- The per-node freshness reason string is not in the captured stream. Only the
  aggregate counts are captured, so the reason for the procedure node's failure
  is inferred from `internal/splice/discovery.go:242` rather than quoted from a
  trace.
- Why no substitution was admitted is not captured. The likely cause is that the
  symbol index used to derive locate needs does not cover `_test.go` files, so no
  `clock_test.go#runClockTable` need was derived. I did not verify that.
- Run A's warm write failure is model output quality, not the harness. The same
  task passed in run B, and the cold write passed in run A.
- Environment failure. During this assignment macOS marked many repository files
  with the `compressed,dataless` flag (the data volume is 96 percent full). The
  Go toolchain then read those files as zero bytes or returned
  `resource deadlock avoided`, so `go build` failed on
  `internal/modelregistry/modelsdev_snapshot.json.gz`, then on several `.go`
  files. I restored the files by reading them fully before the runs, and the
  first confirmation launch aborted in `go build` for this reason. The run that
  is reported here built and ran after that restore. The worktree is also a
  linked git worktree of `/Users/tafseerhaque/Documents/splice`, and during the
  incident `git` reported that repository's packfile as truncated.

## 7. Gate

Captured on this revision. The environment was materialized first (see
section 6).

```text
$ gofmt -l .
(no output; exit 0)
$ go vet ./...
(no output; exit 0)
$ go test ./... -count=1 -timeout 30m
ok   github.com/Taf0711/splice/internal/cli                538.634s
ok   github.com/Taf0711/splice/internal/npmwrapper           7.105s
ok   github.com/Taf0711/splice/tests/evals/warmcost          3.493s
?    github.com/Taf0711/splice/tests/evals/warmcost/cmd/warmcost-eval  [no test files]
go test exit=0
(zero FAIL lines across the run)
$ cd memd && go test ./... -count=1
ok   github.com/Taf0711/splice/memd                          0.867s
ok   github.com/Taf0711/splice/memd/store                    2.293s
memd go test exit=0
$ go build ./tests/evals/warmcost/...
build exit=0
$ bash -n tests/evals/warmcost/paid-run.sh
bash -n exit=0
```

An earlier gate run failed four `internal/npmwrapper` tests with
`ECANCELED: operation canceled, read` and postinstall timeouts, both from the
dataless-file incident in section 6. That package passed on an immediate retry
and in the run above. All other packages passed in both runs.
