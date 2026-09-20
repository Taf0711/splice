# T1b runner-path fix and the T3 A/B run

Date: 2026-09-15 (UTC). Local wall clock 2026-09-14 evening.
Role: test and eval agent. Harness and test code only. No product code changed.
Worktree: `/Users/tafseerhaque/Documents/splice-archeval`, branch
`wip/evidence-substitution-production` at `2f4ccde`, T1/T2 changes kept, plus the
new T1b changes. Nothing committed or pushed.
Cap: $0.25. Spent: $0.004440.

## 0. Bottom line

- T1b DONE. The runner path no longer forces one binary into both arms. It builds
  the binary under test from a detached worktree at `BUILD_REV`, never overwrites
  an operator-supplied `BIN`, and records the binary revision it is told to use.
  Tests and shell guards pass; the gate is green.
- T3 RAN, both arms, within the cap. But the read task of the retention corpus
  aborts in BOTH arms before it issues any request, because the telemetry-branch
  binaries do not produce capture sets. This is a structural blocker, not model
  noise: `internal/splice/discovery.go` (the capture path) exists on the harness
  branch `2f4ccde` and does not exist at `e3e97b1` or `d9c52b3`.
- The A/B therefore measured only the 3 single-request write runs per arm. Arm B
  `CacheShare` is 0.5871 and Arm A is 0.0000, which meets the pre-registered
  "strictly higher" rule, but the designed flip site never ran, so this is not a
  clean test of the schema fix. Details and caveats below.

## 1. T1b — the runner-path fix

### B1 build-revision selector

`paid-run.sh` resolves `BUILD_REV` (default `HEAD`) with
`git rev-parse --verify --quiet "${BUILD_REV}^{commit}"` and fails loud naming the
value when it does not resolve. It builds the binary under test from a detached
worktree at that SHA:

```
BUILD_WT="$(mktemp -d ...)"
git worktree add --detach --force "$BUILD_WT" "$BUILD_REV_SHA"
( cd "$BUILD_WT" && go build -o "$BIN" ./cmd/splice )
git worktree remove "$BUILD_WT" --force
```

The runner is always built from the harness repo, so the binary under test varies
with `BUILD_REV` and the harness code does not.

### B2 no silent overwrite

`BIN` supplied through the environment is detected before the default is applied.
If it is set and `BIN_PREBUILT` is not `1`, the script exits non-zero with
"refusing to rebuild it" and never touches the file. With `BIN_PREBUILT=1` the
supplied path is used as-is and logged.

### B3 authoritative revision

- `tests/evals/warmcost/cmd/warmcost-eval/main.go`: new `--binary-revision` flag,
  wired to `Config.BinaryRevision`.
- `Runner.binaryRevision()` returns `Config.BinaryRevision` when set and only
  falls back to `git -C RepoDir rev-parse HEAD` when it is empty.
- The revision lands in `Provenance.binary_revision` and in each
  `Attempt.binary_revision`; the binary path lands in `Provenance.binary` and the
  new `Attempt.binary`.
- `paid-run.sh` sets `BINARY_REVISION` from the resolved `BUILD_REV` SHA and
  passes `--binary-revision "$REV"`.

### B4 tests

Go tests (`tests/evals/warmcost/build_revision_test.go`):

- `TestBinaryRevisionHonorsExplicitOverride` — the override wins even when
  `RepoDir` is not a git repo (fallback would be empty).
- `TestBinaryRevisionFallsBackToRepoHead` — empty override uses the repo HEAD.
- `TestAttemptRecordsBinaryPathAndRevision` — the attempt and the aggregate
  provenance carry the override and the binary path.
- `TestPaidRunBuildGuards` — runs the shell guards below.

Shell guards (`tests/evals/warmcost/paid_run_guards_test.sh`, `BUILD_ONLY=1`,
no provider, no sidecar):

- case A: unresolvable `BUILD_REV` exits non-zero and the message names the value.
- case B: operator `BIN` without `BIN_PREBUILT=1` is refused, exit non-zero, and
  the supplied file is byte-unchanged.
- case C: operator `BIN` with `BIN_PREBUILT=1` proceeds, is byte-unchanged, is
  logged as used, and the runner is built.

The real worktree build path was exercised without a provider
(`BUILD_REV=e3e97b1 BUILD_ONLY=1`): binary built from
`e3e97b1cada3ac2df2bdf4a06f54ee4d2a108496`, runner from `2f4ccde`, exit 0, and no
worktree left behind.

Limit of the shell test: cases B and C use `BUILD_ONLY=1`, so they prove the
guard and the non-overwrite behavior but do not start a sidecar or a provider.

### T1b diff and test names

- diff: `tests/evals/results/T1B_T3_AB_2026-09-15/t1b-working-tree.diff`
- changed files: `git-status.txt`
- test names and shell cases: `t1b-test-names.txt`

### Gate (captured in this directory)

- `gate-gofmt.txt` — `gofmt -l .` prints nothing.
- `gate-govet.txt` — `go vet ./...` clean.
- `gate-gotest.txt` — `go test ./...` exit 0 (98 packages, no `FAIL`).
- `gate-memd.txt` — `cd memd && go test ./...` exit 0.

## 2. T3 — the A/B run

### Corpus validation (before any spend)

```
python3 tests/evals/taskset-v0/validate/validate.py \
  --tasks-dir tests/evals/warmcost/retention-taskset/tasks \
  --fixture  tests/evals/warmcost/retention-taskset/fixture \
  --out /tmp/wc-retention-registry.json
# clock-helper-read  [YYY-] validated
# clock-helper-write [YYY-] validated
# 2 tasks validated, 0 broken
```

`[YYY-]` = base fails the verifier, gold passes, wrong patch fails, no hack patch.

Sidecar: `(cd memd && go build -o /tmp/splice-memd .)`.
Disk before the first request: `/System/Volumes/Data` 74% used, 113 GiB free.

### Exact commands

Arm A (resolved binary revision `e3e97b1cada3ac2df2bdf4a06f54ee4d2a108496`):

```
ARMS=warm REPEATS=3 MAX_RETRIES=0 RETENTION=shared \
MODEL=z-ai/glm-5.3-flash \
TASKSET_SRC=tests/evals/warmcost/retention-taskset \
TASKS_ENV="clock-helper-write clock-helper-read" \
SIDECAR_ROOT=/tmp/wc-ab-armA-sidecar BUILD_REV=e3e97b1 \
RUN_BASE=wc-ab-armA-20260915T023826Z \
MEMD_BIN=/tmp/splice-memd MARGIN=0.05 \
tests/evals/warmcost/paid-run.sh
```

Arm B (resolved binary revision `d9c52b3560fbab839ee377a57a8ad8ae1414801d`):

```
ARMS=warm REPEATS=3 MAX_RETRIES=0 RETENTION=shared \
MODEL=z-ai/glm-5.3-flash \
TASKSET_SRC=tests/evals/warmcost/retention-taskset \
TASKS_ENV="clock-helper-write clock-helper-read" \
SIDECAR_ROOT=/tmp/wc-ab-armB-sidecar BUILD_REV=d9c52b3 \
RUN_BASE=wc-ab-armB-20260915T024301Z \
MEMD_BIN=/tmp/splice-memd MARGIN=0.05 \
tests/evals/warmcost/paid-run.sh
```

Treatment env per attempt: `SPLICE_SCOPE_MODE=on`,
`SPLICE_EVIDENCE_SUBSTITUTION=on`, `SPLICE_MEMD_SOCKET`/`SPLICE_MEMD_DB` under
the arm's `SIDECAR_ROOT/shared-warm`. Model `z-ai/glm-5.3-flash`, warm only,
`REPEATS=3`, `MAX_RETRIES=0`, margin 0.05. 2 arms x 1 sequence x 3 repeats = 6
sequence runs, 12 task attempts, as capped. Each arm used its own sidecar root
and run base, so neither inherited the other's memory.

### Cross-run comparison

| metric | Arm A (e3e97b1) | Arm B (d9c52b3) |
| --- | ---: | ---: |
| attempts | 6 | 6 |
| verified completions | 3 | 3 |
| failed attempts | 3 | 3 |
| requests | 3 | 3 |
| requests per verified completion | 1.00 | 1.00 |
| input tokens | 8157 | 8142 |
| cached tokens | 0 | 4780 |
| cache share | 0.0000 | 0.5871 |
| coverage complete | false | false |
| billed USD (all attempts incl. aborts) | 0.002496 | 0.001944 |
| prompt_layout_flips | UNKNOWN (binary predates the field) | 0 |

Total billed for both arms: **$0.004440** against the $0.25 cap.
Raw attempt rows: `cross-run-table.md`. Per-round and per-stage tables:
`per-round-and-layout.md`. Raw artifacts:
`tests/evals/results/wc-ab-armA-20260915T023826Z-try0/` and
`tests/evals/results/wc-ab-armB-20260915T024301Z-try0/`.

### Arm B telemetry

Arm B's three write requests each carry `prompt_layout_hash`
`cc09a35d5dcfbe6e0e4a757243b998f4edc54934dbd382323cbc0abbbf78f692` — identical
across the three repeats — so the prefix was byte-stable for that stage, and the
reported `prompt_layout_flips` is 0. `cache_hit` is false, true, true on
r0/r1/r2, matching cached tokens 0, 2048, 2732. Arm A emitted no telemetry, so
its flip count is UNKNOWN, not 0.

### The read task aborts in both arms (blocker)

Every `clock-helper-read` attempt is `infrastructure_failed` with zero requests:

```
warmcost: sequence 0 task clock-helper-write: warmcost: no capture set for run
"wc-wc-ab-armA-20260915T023826Z-try0-clock-helper-write-warm-r0" at revision ...
```

Cause (verified, not inferred): the harness reanchors the write run's captured
nodes before the read runs, and the telemetry-branch binary produces none.
`sqlite3 .../shared-warm/mem.db "select count(*) from cognition_nodes"` = 0 in
both arm sidecars, while `run_traces`=3 and `observations`=2. And
`internal/splice/discovery.go` (which defines `GraphCapture`,
`captureFromVerifiedRun`, `persistGraphCapture`) exists at `2f4ccde` and does not
exist at `e3e97b1` or `d9c52b3`. So the six telemetry commits are on a branch
line that has no cognition-capture path at all, and the retention write->read
sequence cannot complete there in either arm.

Because the read task never ran, the corpus did not exercise the mid-run flip
site (a stage with memory and context expansions issuing several requests).
The write task issues exactly one request, so no schema flip can fire inside it.

## 3. One-line answer to SPEC section 12

Mechanically the pre-registered rule is met — Arm B `CacheShare` 0.5871 is
strictly higher than Arm A 0.0000 — but the experiment did not run its flip site
(the read task aborted in both arms), the write runs contain no memory-presence
change within a stage, and Arm B's flip counter is 0, so this corpus does **not**
show the cacheable prefix flipping and does **not** attribute the hit-rate
difference to the stable-schema change. The section 12 question stays open.

Comparison behind it: per-arm `CacheShare`, cached/input tokens, requests and
requests/verified are in the cross-run table above; Arm A cached 0 of 8157 input
tokens, Arm B cached 4780 of 8142; both arms lost the same 3 read attempts to the
reanchor abort.

## 4. Why the difference is not attributed to the schema fix

- Both arms' read tasks aborted, so the designed multi-request flip site did not
  run. `prompt_layout_flips` from Arm B is 0 on the only requests that ran.
- Arm A's own three write runs had memory present in all three (memory items
  1,2,2; the ~5-token per-request input gap versus Arm B is the memory-dependent
  `required` entry), so Arm A's write prefix was constant across its repeats.
  A constant prefix that still records zero cached tokens is not explained by the
  schema flip and points at provider-side / run-order caching behavior.
- Arm A ran first (22:38–22:39 UTC) and Arm B second (22:43–22:47 UTC); the
  provider's prefix-cache state or commit latency across that window is a
  confound I could not control or rule out.

## 5. Unverified / not done

- No mid-run prompt layout flip was observed in either arm. The flip site (read
  task) did not run.
- The cause of Arm A's zero cache and Arm B's progressive cache is not
  established. The schema fix is one candidate; provider/order effects are not
  excluded.
- Arm A's `prompt_layout_flips` is unknown (binary predates the field).
- The retention corpus did not deliver retained cognition to the read task,
  because the read task never ran.
- W4 was not built and should not be built on this evidence.
- No token or cost saving is claimed.

## 6. What would unblock the section 12 question

The reanchor step is a harness feature for the capture-enabled harness line; the
telemetry line has no capture path, so the runner aborts. Two options, either of
which needs an owner decision:

1. Make the harness reanchor non-fatal when the producer run has no capture set
   (record it on the attempt and continue), so the read task runs and issues its
   context expansions. This is a harness change beyond T1b B1-B4.
2. Build a corpus for the telemetry line that produces several requests in one
   stage without depending on cross-run capture, so a memory-presence change
   inside the stage can flip the schema.

Until one of those is chosen, further A/B runs on the retention corpus will
reproduce this same abort.
