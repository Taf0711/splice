# Paired eval run log (instrument history)

Running record of every paired-eval attempt against taskset
`~/Documents/splice-eval-taskset` (12 tasks, fixture = splice-demo a2a1371),
with causes and state of the instrument after each cycle.

## Cycle 1 — crashed mid-run, no report

- Binary: dev @ 0499c11, `--model gpt-5.2`.
- Died at cold task 10/12 (`session-json-tags`): gpt-5.6-sol wrote a broken
  session.go twice, repair exhausted the light-tier token budget,
  `splice exec` exited 4 with `abort_budget: Token budget reached.`, and the
  harness treated the single exec error as fatal. Nine completed pairs were
  discarded; no report written.
- Discovery: `--model` does not govern pipeline stages.
  `~/.config/splice/stage-models.json` pinned every stage to gpt-5.6-sol, so
  all cycles have effectively run on gpt-5.6-sol.
- Transcript preserved at `~/Documents/splice-eval-taskset/report/failed-run-stderr.log`.

## Cycle 2 — completed; mechanical verdict manufactured from missing data

- Binary: dev @ 9dcd56c (`fix(eval): survive per-run failures and persist
  pairs incrementally`). Taskset copied to `-run2` because deterministic
  session ids collide with persisted sessions on rerun ("splice session
  already exists").
- All 12 pairs ran. Mechanical verdict: "conclusive - warm wins: 100.0% fewer
  tokens at equal success". Downgraded: warm telemetry was zero for every run,
  so the cost gate compared measurement against absence.
- Success signal was real but read direction-only: warm 8/12 vs cold 6/12.
- 1 of 24 runs hit abort_budget (cold, snapshot-persistence); absorbed by the
  new fault tolerance instead of killing the run.

## Cycle 3 — completed; second manufactured verdict, decisive finding

- Binary: dev @ e54e1f4 (`fix(eval): resolve repo root symlinks before trace
  lookup and surface missing telemetry`). Taskset copied to `-run3` for fresh
  session ids.
- All 12 pairs ran. Mechanical verdict: "regression - warm successes (0)
  below cold (6)". Downgraded again: every warm run died to
  `abort_budget: Token budget reached.` before its check could run (14 of 24
  runs aborted overall: 12 warm, 2 cold).
- Substantive finding: with light-tier budgets frozen (code_writer
  InputMax 4000 / OutputMax 8192, +1000 reserve, overflow=abort),
  memory-injected context pushes warm runs over the wall on every task while
  cold mostly survives. Under these budgets the instrument structurally
  cannot show warmth succeeding.
- Cold successes still had zero telemetry even after the symlink fix, which
  triggered the deeper investigation below.

## Root cause of dead telemetry (found cycle 3 investigation)

The trace feature is newer than the running sidecar binary:

- Trace persistence shipped in memd + client on Aug 19 (commit 74873a9,
  endpoints `/trace/upsert`, `/trace/query`; client paths in
  `internal/memd/client.go:251,302`).
- The sidecar process on this machine (pid observed Aug 22-23) is a binary
  built Aug 2 03:59. It returns 404 for both trace endpoints (verified live).
- `runTraceAccumulator.persistPartial` / upsert failures are non-fatal by
  design (`internal/splice/trace.go`: `noteTraceWriteFailed()`), so every run
  since Aug 19 silently wrote no traces.
- `collectTrace` then queried `/trace/query` on the same stale binary:
  also nothing. Telemetry zeros were never a path-resolution problem; no
  traces existed to find. The symlink resolution added in e54e1f4 remains
  correct hygiene but was not the defect.

Corroborating evidence: `/stats` on the live socket reports only 21 memory
observations (11 run_config, 10 test_command) and no trace activity across
three eval cycles; `~/.local/share/splice/sessions/eval-*` session dirs exist
because session storage is separate from memd and worked throughout.

## Fix state

- Shipped: 9dcd56c (fault tolerance + incremental pair log), e54e1f4 (symlink
  hygiene + explicit telemetry-found flags + markdown warning).
- Required operationally (owner machine action): rebuild the sidecar
  (`cd memd && go build -o <installed splice-memd> .`) and restart it. Until
  then, any rerun repeats cycle 3's telemetry void.
- Proposed product follow-ups, pending review:
  1. Surface trace-write failure instead of swallowing it silently (warn once
     per run; ideally visible in stream-json) so stale-sidecar situations are
     self-announcing.
  2. Optional capability probe: client checks an endpoint or version field at
     startup and marks memory "degraded/no-trace-support".
