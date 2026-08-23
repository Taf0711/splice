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

Blast radius beyond the eval: budget calibration starves too. `learn.FitBudget`
(called per stage from `internal/splice/run.go`) reads its calibration corpus
through `QueryTraces`, so with no traces persisting since Aug 19 every run has
calibrated 0/N stages against live data and fallen back to static defaults.
Run 3's own transcript shows `budgets: 0/4 stages calibrated`. The stale
sidecar did not just hide eval telemetry; it froze LN2 learning input for 17
days. The operational fix (rebuild + restart) restores both.

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

## Cycle 4 (run 4/5/6 series) — staging refinement landed; warm arm hits the cumulative wall

Owner approved the staging slice, landed as 904c570: per-stage composed-input
compaction at composition time (deterministic drop order, required content
never touched, loud failure when required content alone overflows), and the
light-tier code_writer raise 4000/8192 -> 20000/8192 derived from run-3
transcripts. Two more instrument defects surfaced and were fixed mid-series:

- 66bb4b9: with a working sidecar, traces store the RAW temp-dir path while
  e54e1f4 queried only the resolved form — join missed in the opposite
  direction. Lookup now tries raw first, then resolved.
- 4f553e0: cold arms never write traces BY DESIGN (--memory off leaves the
  memory client nil and the tracer is wired FROM that client). The runner now
  falls back to summing captured stream-json usage records when the trace
  join finds nothing, so both arms carry measured cost.

Run 6 (dev @ 4f553e0, -run6 ids, gpt-5.6-sol): full telemetry both arms for
the first time. Cold 8/12 succeeded (per-success cost measured, median ~5.5k
tokens). Warm 0/12 — every run aborted on `abort_budget`, burning 526,734
tokens total. Mechanical verdict "regression"; substantive reading: the
light-tier budget counts CUMULATIVE input+output across all stage calls,
while memory cost recurs on every consuming stage call. Compaction bounds
each call's input; it cannot bound the sum over calls, so any multi-call
warm run crosses the ~30.2k wall regardless of per-call trimming. The
remaining levers are product decisions: what the trajectory rule counts
(e.g. output-only vs total) or materially larger totals.

Instrument state after cycle 4: telemetry complete and symmetric, checks
deterministic, harness fault-tolerant with incremental persistence. The eval
mechanics are sound; the budget policy is the open owner question.
