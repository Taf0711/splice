# Retention sequence: harness change and capped confirmation

Date: 2026-09-12.
Branch: `wip/evidence-substitution-production` at HEAD
`8c496067ed31e04b1d5f7273ec63b356078897ff` (`8c49606`).
Role: test and eval agent. No product code changed. Nothing committed, staged,
or pushed.

## 0. Verdict

- Part 1 HOLDS. The runner now runs one workspace per (arm, sequence), so a
  write-phase task and the read-phase task after it share a workspace and a
  memory project identity. The offline smoke test and the unit tests confirm it.
- Part 2 RAN within cap: 1 sequence, 2 arms, 1 repeat, 4 task attempts.
- The read task retrieved NO graph evidence. The sidecar held the write task's
  nodes under the same project path, and retrieval found them, but freshness
  rejected them. The captured stream says
  `discovery: 2 anchor(s) failed freshness validation`.
- The read task still passed its verifier, by emitting a `request_context`
  expansion for `clock_test.go` and using the delivered file. So the task
  succeeded through the context-request route, not through retained evidence.
- No cost or token saving is claimed. The total-cost claim is withheld.

## 1. Harness change: sequence attempts

Files changed, all test and eval harness code:

- `tests/evals/warmcost/runner.go`
- `tests/evals/warmcost/types.go`
- `tests/evals/warmcost/warmcost_test.go`
- `tests/evals/warmcost/README.md`

What changed:

1. `groupSequences` groups the ordered tasks. A write-phase task starts a
   sequence; the tasks after it, up to the next write-phase task, belong to it.
   A taskset with no write-phase task keeps one sequence per task, which
   preserves the previous per-task workspace behavior.
2. `Run` iterates repeat, then sequence, then arm. `runSequence` prepares ONE
   workspace for the (arm, sequence) and runs its tasks in order in that
   directory. It resets the workspace contents at the start of each repeat and
   never between the tasks of a sequence.
3. Each task still produces its own attempt, verifier run, raw stream, and
   ledger record. Every attempt records `sequence`, `sequence_ordinal`,
   `sequence_tasks`, and `workspace`.
4. The workspace path is stable across repeats for one (arm, sequence), so the
   runtime memory project identity is stable. That is the fix for the round-1
   blocker: a read task can now query the same `project_path` the write task
   captured under.
5. A sequence that contains a write-phase task initializes its workspace as a
   git repository with one initial commit and a local identity. The runtime
   capture path anchors evidence at a git revision
   (`internal/splice/run.go:566` `verifiedRevision`), and a workspace with no
   repository produces no reusable record. Plain tasksets keep the previous
   behavior.

No new CLI flag. No product code. No change to the context-request contract.

Unit tests added: `TestGroupSequencesWriteThenRead`,
`TestGroupSequencesWithoutWriteSplitsPerTask`,
`TestGroupSequencesRejectsMixedFixtures`, and `TestRunSequenceSharesOneWorkspace`.
The last one runs the runner with a seam and proves the write and read tasks saw
the same workspace directory.

## 2. Corpus and validation

`tests/evals/warmcost/retention-taskset/` holds one write task and one read task
over one fixture.

| task | phase | what it does | unnamed dependency | verifier |
| --- | --- | --- | --- | --- |
| `clock-helper-write` | write | adds `newClockStore(ttl)` to `clock_test.go` and routes `runClockTable` through it | none (it names `clock_test.go`) | probe test calls `newClockStore`, advances the clock, and asserts expiry, then `go test` |
| `clock-helper-read` | read | adds `TestStoreExpiryBoundary` in a new file, reusing `runClockTable` | `clock_test.go` (the prompt names no file) | a `*_test.go` file other than `clock_test.go` must reference `runClockTable`, and `go test -run TestStoreExpiryBoundary` must pass |

Why the read dependency is real and fresh after the write: the write task
changes `clock_test.go` by adding `newClockStore` and routing `runClockTable`
through it. It keeps `runClockTable`'s name and signature and the `clockCase`
field names. The read task needs that helper and its case fields. Because the
two tasks share ONE workspace, the write's bytes are still present when the read
runs, so the dependency is not reset.

Validation with `taskset-v0/validate/validate.py`: 2 tasks validated, 0 broken.
Each task fails its verifier on the untouched fixture, passes with its gold
patch, and fails with its wrong patch.

## 3. Run facts

Exact command, printed before the first provider request:

    ARMS=cold,warm \
    REPEATS=1 \
    MAX_RETRIES=0 \
    RETENTION=shared \
    MODEL=z-ai/glm-5.3-flash \
    TASKSET_SRC=tests/evals/warmcost/retention-taskset \
    TASKS_ENV="clock-helper-write clock-helper-read" \
    SIDECAR_ROOT=/tmp/warmcost-retention-seq-sidecar \
    RUN_BASE=wc-retention-seq-20260912T224429Z \
    tests/evals/warmcost/paid-run.sh

- Model: `z-ai/glm-5.3-flash`. Credential present (`OPENROUTER_API_KEY`).
- Tasks with phases: `clock-helper-write` (write), `clock-helper-read` (read).
- Repeats: 1. Arms: `cold`, `warm`. Retention: `shared`.
- Sequences: 1. Cap: 1 sequence x 2 arms x 1 repeat = 2 sequence runs, which is
  4 task attempts. Used: 2 sequence runs, 4 task attempts. Limit 6 was not
  exceeded.
- HEAD: `8c496067ed31e04b1d5f7273ec63b356078897ff`.
- Run id: `wc-retention-seq-20260912T224429Z-try0`.
- Sidecar root: `/tmp/warmcost-retention-seq-sidecar`. Warm used
  `shared-warm`; cold used `fresh-<session>`.
- Wall time: about 3 minutes (`18:44:29` to `18:47:30`).
- The shared project identity is confirmed: both warm attempts record
  `workspace` ending `warm-seq00`, and both cold attempts record `cold-seq00`.

Caveat: `paid-run.sh` prints its own cap line as
`tasks=2 x arms=cold,warm x repeats=1 = 2 attempts`. That formula drops the arm
count, so the printed number is wrong. The true cap is 4 task attempts. I did
not change the script. The correct cap was printed by the launcher before the
run.

## 4. Answers

### 4.1 Did the read task retrieve the write task's evidence?

No graph evidence was delivered, and no substitution fired.

- Memory items delivered to the read task: 1. The stream reports
  `memory admission: code_writer supplied 1`. The sidecar holds two
  project-scoped observations, "Pipeline run configuration" and "Discovered
  test command". Each run writes those two, and the sidecar dedupes them by
  content, so the captured fields do not show which observation was admitted.
- Direct hits on the write's graph nodes: 0. The write run captured 2 nodes
  under the shared project path: a procedure ("Verification passes with:
  go test ./...") and a fact ("clock_test.go defines newClockStore,
  runClockTable, TestClockTableHelperSelfCheck"). The read run's discovery
  found the anchors and then reported
  `discovery: 2 anchor(s) failed freshness validation`.
- Substitutions: 0. The read task's context handshake used the default reason
  ("Inspect existing project files before writing ..."), not the
  evidence-substitution reason, so no admitted record shaped the request.
- Expansions removed by the evidence: none. The read task still emitted a
  `request_context` action and the host fulfilled it.

The read task's expansion, quoted from the stream:

    [code_writer] requesting context expansion: The intent requires reusing the
    existing runClockTable table helper and its case fields, but clock_test.go
    (and session_test.go) were not delivered in the context views, so I cannot
    write a compatible test without them.

The read task's verifier then passed (`ok demo 0.399s`) using the delivered
`clock_test.go`.

### 4.2 Did freshness admit the evidence, or reject it?

Rejected. The captured stream reports, per read task:

    discovery: 2 anchor(s) failed freshness validation

The per-node freshness reason is not in the captured stream, so it cannot be
quoted. The root cause is captured in both warm attempts:

    [cognition] anchored at HEAD (snapshot unavailable: exit status 1)

The capture felt back to the pre-change HEAD, so every node carries
`verified_revision = eb211482903fd31e5db783ddd049d3f9663dc707`, the initial
fixture commit from `initGitWorkspace`. Freshness then runs
`git diff --name-only <that commit>` (`internal/splice/cognition/batch.go:62`),
sees `clock_test.go` as changed, and marks the fact stale
(`internal/splice/cognition/batch.go:96`). The procedure node fails closed
because it has no file anchor to diff
(`internal/splice/discovery.go:243`).

The sidecar confirms the anchoring: all three captured nodes carry the same
`verified_revision`, and the clock fact's anchors are
`file:clock_test.go` plus `clock_test.go#newClockStore`,
`clock_test.go#runClockTable`, and `clock_test.go#TestClockTableHelperSelfCheck`.

The `git stash create` failure is inside the run: the same command succeeds in a
plain shell, and the run executes it through the native stage sandbox
(`procrun.ProfileSpliceStage`, `internal/splice/stages/exec_stage.go:37`). The
harness does not capture git's stderr, so the exact git error text is not
available. This is reported, not tuned.

### 4.3 Per-source spend and cost coverage

Every number below is a captured ledger field. All four attempts have
`cost_coverage = complete`.

| task | arm | verifier | status | calls | generation | format_retry | expansion | repair | input tok | output tok | cached tok | billed USD |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `clock-helper-write` | cold | PASS | completed | 4 | 2 | 2 | 0 | 0 | 16053 | 2266 | 7808 | 0.00260399 |
| `clock-helper-read` | cold | FAIL | failed | 8 | 2 | 5 | 0 | 1 | 39853 | 1593 | 16960 | 0.00473925 |
| `clock-helper-write` | warm | PASS | completed | 5 | 2 | 3 | 0 | 0 | 20527 | 3500 | 1920 | 0.00459865 |
| `clock-helper-read` | warm | PASS | completed | 2 | 1 | 0 | 1 | 0 | 10789 | 512 | 0 | 0.00187435 |

Arm totals and the source split:

| arm | attempts | verified | calls | expansion calls | round share | generation USD | format_retry USD | expansion USD | repair USD | billed USD |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold | 2 | 1 | 12 | 0 | 0.0000 | 0.00292784 | 0.00354095 | 0 | 0.00087445 | 0.00734324 |
| warm | 2 | 2 | 7 | 1 | 0.1429 | 0.00269385 | 0.00270770 | 0.00107145 | 0 | 0.00647300 |

Reconstruction: cold 0.00292784 + 0.00354095 + 0.00087445 = 0.00734324, which
equals the cold billed total. Warm 0.00269385 + 0.00270770 + 0.00107145 =
0.00647300, which equals the warm billed total. The runner's
`validateSourceIdentity` enforces this at build time and did not error.

Cost coverage: 4 attempts, 0 partial, complete. The matched pair exists for only
one task, so no task-clustered interval is meaningful. The aggregate withholds
the claim: "the task-clustered interval for the cost delta does not exclude
zero". No saving is claimed.

The cold read failed because the model wrote a test file that did not compile:
`./store_expiry_test.go:6:2: "time" imported and not used` and
`unknown field steps in struct literal of type clockCase`. That failure is model
output quality, not the harness.

### 4.4 If the read task retrieved nothing

It retrieved no graph evidence. The exact reason: every captured node is
anchored at the pre-change HEAD because `git stash create` failed inside the
run (`snapshot unavailable: exit status 1`), and the freshness diff against
that revision marks the changed `clock_test.go` stale. The read task therefore
fell back to the context-request route and passed. I did not tune the mechanism
to force a hit.

Next step for the owner: the identity fix is necessary but not sufficient. The
capture's revision snapshot must succeed under the stage sandbox before a
changed-file fact can pass freshness across a write and a read.

## 5. Raw artifacts

Under `tests/evals/results/wc-retention-seq-20260912T224429Z-try0/`:

- `raw-<task>-<arm>-r0.jsonl`: the full child stream-json per attempt.
- `attempt-<task>-<arm>-r0.json`: the ledger projection with `sequence`,
  `sequence_ordinal`, `sequence_tasks`, and `workspace`.
- `aggregate.json`, `report.md`.
- `derived-toolcalls.txt`: reconstructed model tool calls.

Also `tests/evals/results/PAID_RUN_wc-retention-seq-20260912T224429Z-try0.md`
and `tests/evals/results/wc-retention-seq-20260912T224429Z-try0.log`.

Sidecar evidence: `/tmp/warmcost-retention-seq-sidecar/shared-warm/mem.db`
(3 cognition nodes, 2 observations, all project-scoped to the warm workspace).
This path is outside the repository and may be removed before review.

## 6. What I could not verify

- The per-node freshness rejection reason. The run trace holds it, and the
  harness does not write that trace into the raw stream. Only the aggregate
  count is captured.
- Which observation the read task admitted. The stream reports the count (1),
  not the identity, and the model prompt is not streamed.
- The exact git error behind `snapshot unavailable: exit status 1`. The stage
  sandbox does not surface git's stderr to the captured stream.
- A cost or token result. One pair and one repeat cannot support one, and no
  saving is claimed.

## 7. Gate

```text
$ gofmt -l .
(no output; exit 0)
$ go vet ./...
(no output; exit 0)
$ go test ./... -count=1 -timeout 30m
ok   github.com/Taf0711/splice/internal/cli                196.113s
ok   github.com/Taf0711/splice/internal/memd                 1.414s
ok   github.com/Taf0711/splice/internal/splice              43.296s
ok   github.com/Taf0711/splice/tests/evals/warmcost          0.708s
?    github.com/Taf0711/splice/tests/evals/warmcost/cmd/warmcost-eval  [no test files]
go test exit=0
(zero FAIL lines across the run)
$ cd memd && go test ./... -count=1
ok   github.com/Taf0711/splice/memd                          0.866s
ok   github.com/Taf0711/splice/memd/store                    2.051s
memd go test exit=0
$ go build ./tests/evals/warmcost/...
build exit=0
$ bash -n tests/evals/warmcost/paid-run.sh
bash -n exit=0
```
