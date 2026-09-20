# Harness cache telemetry (T1/T2) and the A/B run status (T3)

Date: 2026-09-14 (UTC).
Role: test and eval agent. Harness and test code only. No product behavior was
changed. Nothing was staged, committed, or pushed.
Worktree: `/Users/tafseerhaque/Documents/splice-archeval`.
Branch: `wip/evidence-substitution-production` at `2f4ccde`, plus uncommitted
harness changes under `tests/evals/warmcost/`.

## 0. Verdict

- T1 DONE. `RequestRecord` now carries `PromptLayoutHash`, `CacheHit` and
  `MemoryPosition`; `Attempt` carries `PromptLayoutFlips` (a `*int`, so a binary
  that emits no telemetry reports unknown, never zero). The values are decoded
  from the raw final result JSON with harness-local structs (Option B).
- T2 DONE. Each attempt now carries per-(stage, iteration) `rounds` and
  per-stage `layout_stability`; the aggregate adds a `cache_layout` section that
  names every attempt with a non-zero flip count and the distinct hashes per
  stage. Partial telemetry coverage is stated, not smoothed over.
- T3 NOT RUN. No owner-approved cap was given, so no provider request was made.
  The comparison is pre-registered below. The section 12 question is therefore
  NOT yet settled by a live run.
- GATE PASS. `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` exit 0,
  `cd memd && go test ./...` exit 0. Captured outputs are in
  `tests/evals/results/harness-cache-telemetry-20260914/`.

## 1. T1 — record the telemetry

Files changed (harness only):

- `tests/evals/warmcost/types.go` — new `RequestRecord` fields, new `Attempt`
  fields.
- `tests/evals/warmcost/telemetry.go` (new) — Option B decode + merge, the
  per-round and per-stage aggregation, and the aggregate layout summary.
- `tests/evals/warmcost/runner.go` — extract `finalEventText`, decode the
  telemetry from the same final event, merge it onto the typed records, and
  compute the round and layout views.
- `tests/evals/warmcost/report.go` — render the `cache_layout` section.
- `tests/evals/warmcost/README.md` — document the fields, Option B, and the A/B
  plan.
- `tests/evals/warmcost/telemetry_test.go` (new) — coverage.

New artifact fields:

- `RequestRecord.prompt_layout_hash` (string, omitempty).
- `RequestRecord.cache_hit` (`*bool`, always present; `null` is unknown, `false`
  is a reported miss, `true` is a reported hit).
- `RequestRecord.memory_position` (string, omitempty).
- `Attempt.cache_telemetry_reported` (bool).
- `Attempt.prompt_layout_flips` (`*int`, omitempty).
- `Attempt.rounds` (per stage/iteration) and `Attempt.layout_stability` (per
  stage).

### Build dependency: Option B chosen

The harness branch does not contain the telemetry commits, and the two branches
diverged at `43a7766`. Option A (cherry-pick the six telemetry commits onto a
branch off the harness branch) was attempted and conflicts immediately:
`970510f` alone conflicts in `internal/splice/stages/code_writer.go` against the
harness branch's D1 `request_context` action decode, and `cfcaec8`/`5e627cf`/
`0e6911c` also touch `internal/splice/run.go`, which differs from `e3e97b1` by
about 570 lines. Resolving those conflicts is product-behavior work, which this
role forbids ("You are NOT the implementer of product behavior"), and it would
put the telemetry product schemas onto the harness branch.

Option B was therefore taken: the harness decodes `prompt_layout_hash`,
`cache_hit`, `memory_position` and `prompt_layout_flips` from the same `final`
stream-json event it already parses, into harness-local structs
(`telemetry.go`), and merges them onto the typed records by ledger `sequence`.
Product schemas are untouched, the harness branch history is not rewritten, and
the same runner decodes both a telemetry-emitting binary (Arm B) and an older
binary (Arm A): an older binary simply reports `cache_telemetry_reported=false`
and a nil flip count.

The cherry-pick attempt was fully reverted (`git reset --hard 2f4ccde`); the
scratch branch was deleted.

## 2. T2 — per-round cache behavior and layout stability

`computeRounds` groups the authoritative request records by `(stage, iteration)`
and reports requests, input tokens, cached tokens, the round cache hit rate
(cached/input, same definition as the arm-level `CacheShare`) and the ledger
spend source set. A round with no hit therefore shows rate 0.

`computeLayoutStability` groups by stage and reports the distinct
`prompt_layout_hash` values, how many requests carried a hash, and whether the
stage was stable. A stage with more than one distinct hash is a flip. A stage
with no hashed request is reported as `hashed=false, stable=false` — unknown,
not stable.

`computeCacheLayout` sums the binary's `prompt_layout_flips` over attempts that
emitted telemetry, names every attempt with a non-zero count, and lists the
distinct hashes per stage across the run. The `report.md` section prints
"Total prompt layout flips: N", the attempts with a non-zero count, and the hash
table. A non-zero count is surfaced as a finding; it is not hidden.

Telemetry-presence rule: `prompt_layout_flips` is `omitempty` in the product
schema, so a reported zero is absent from the JSON. Once any usage record carries
a telemetry value, an absent flip field is decoded as the reported zero; if no
usage record carries any telemetry value, the flip count stays nil (unknown).

## 3. Gate evidence

Captured in `tests/evals/results/harness-cache-telemetry-20260914/`:

- `gate-gofmt.txt` — `gofmt -l .` prints nothing, exit 0.
- `gate-govet.txt` — `go vet ./...` clean, exit 0.
- `gate-gotest.txt` — `go test ./...` exit 0 (98 packages, no `FAIL`).
- `gate-memd.txt` — `cd memd && go test ./...` exit 0.

Commands:

```
cd /Users/tafseerhaque/Documents/splice-archeval
gofmt -l .
go vet ./...
go test ./...
cd memd && go test ./...
```

## 4. Cross-check against the telemetry branch

The telemetry branch's own provider-free test proves the emitted shape and that
a stable prefix reports zero flips:

```
cd /Users/tafseerhaque/Documents/splice          # feat/tui-workflow-surfaces d9c52b3
go test ./internal/cli/ -run TestRunExecRecordsPromptLayoutAndCacheTelemetry -count=1 -v
# --- PASS: TestRunExecRecordsPromptLayoutAndCacheTelemetry (2.92s)
```

Output in `telemetry-branch-crosscheck.txt`. This confirms the JSON field names
(`prompt_layout_hash`, `cache_hit`, `memory_position`, `prompt_layout_flips`)
that the harness decodes. It is the telemetry branch's own test, not a live
provider run.

An additional harness unit test decodes the exact same shape
(`TestRunnerCapturesCacheTelemetry`): a mocked final event with two
`code_writer` hashes and one `test_generator` hash produces
`cache_telemetry_reported=true`, `prompt_layout_flips=1`, three round rows, two
stage-layout rows, and an aggregate that reports the flip.

## 5. T3 — A/B run: pre-registered, not started

No owner-approved cap was given, so per the assignment I stopped after T1 and T2
and did not spend anything.

Pre-registration (fixed here, before any run):

- Arm A: binary built at `e3e97b1` (parent, before the stable-schema fix).
- Arm B: binary built at `d9c52b3` (after the fix).
- Only `970510f` changes what Splice sends to a provider; the other five commits
  add telemetry, docs, or tests. A cache-behavior difference between the arms is
  therefore attributable to the schema requirement.
- Compared per arm: `CacheShare` (cached/input), cached tokens, input tokens,
  total input tokens, requests, requests per verified completion. Arm B also
  reports `prompt_layout_flips`, which must be 0.
- Correctness margin: 0.05 (success-rate noninferiority), as in the prior
  pre-registration.
- Fixed interpretation: strictly higher Arm B `CacheShare` ⇒ the flip was real
  and the lever is confirmed on this corpus. Equal `CacheShare` ⇒ the flip never
  fired on this corpus, so the schema work is correctness hygiene and not a
  saving, and W4 must not be built. A lower Arm B `CacheShare` would be reported
  as a negative, not reinterpreted.
- Corpus requirement (SPEC section 11 risk 4): the corpus must trigger context
  expansions and memory re-entry. The prior retention corpus produced zero
  substitutions and its read task failed for model-output reasons, so it is not
  assumed sufficient. The taskset must first be validated (each task fails on
  the untouched fixture, passes with its gold patch, fails with its wrong patch)
  before any spend.

## 6. Unverified / not done

- No live provider run was made. Whether the cacheable prefix actually flips in
  a real run, and whether stabilizing it raises the provider cache hit rate, is
  NOT answered by a measurement. The harness can now measure it.
- The corpus for T3 was not validated (no cap, so no spend and the runner was
  not pointed at a new taskset).
- Per-arm `CacheShare` and `prompt_layout_flips` for Arm A vs Arm B do not exist.
- The harness was exercised against mocked final events, not against a live
  Arm A or Arm B binary.

## 7. Non-claims

- No token or cost saving is claimed. The prior line remains a structural
  negative; this work does not reopen it.
- No correctness improvement is claimed.
- Missing telemetry is reported as unknown, not as zero. Partial telemetry
  coverage is stated and no complete-cost claim is made.
- Evidence substitution is not enabled by default; no product schema was
  modified.
