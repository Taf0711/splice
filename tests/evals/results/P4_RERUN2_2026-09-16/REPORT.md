# P4 re-run attempt 2: stage-0 write smoke after the hedge normalization

**Outcome and verdict: `LIVE_SETUP_BLOCKED`.** Both mandatory preflight additions
work and the base_ref hedge is fixed offline, but the stage-0 WRITE smoke did not
pass: `z-ai/glm-5.3-flash` spent the entire 10-minute per-attempt bound gathering
context and generating a very long reasoning chain, and the bound killed it
while it was still composing the submission. It never emitted `submit_code`, so
`base_ref` was never exercised live and the three-condition diagnostic was not
run. No cognition or token claim is made.

Date: 2026-09-16. Role: test and eval agent. Harness/eval code only; no product
behavior changed by this session. Worktree
`/Users/tafseerhaque/Documents/splice-archeval`, branch
`wip/evidence-substitution-production`.

## 1. Identity

| item | value |
| --- | --- |
| product fix | `ad71955` normalize the hedged modify (content + base_ref + edits) |
| harness additions | `d93149a` resolved-stage-model assertion + artifacts-on-kill |
| revision used | `d93149a71f7a15177c814a365a27b113e42ed37e` |
| binary | `/tmp/p4a-splice`, SHA-256 `3c1d0bead4800af77250a0b7eacf6148e8e563ea7016b5c33950389e529c3b3f` |
| sidecar | `/tmp/p4a-memd`, SHA-256 `152c2c3e8b127a396444470d7be38195a32a7d94e7ed38ca1fe9a86ef49ce6bf` |
| build state | clean (0 dirty paths) |
| model | `z-ai/glm-5.3-flash` (resolved, asserted) |
| per-attempt timeout | 10m (frozen for this assignment; the runner default stays 30m) |

Gate on the committed revision: `gofmt -l .` empty, `go vet ./...` clean,
`go test ./...` exit 0 (0 FAIL), `cd memd && go test ./...` exit 0.

## 2. Mandatory preflight 1: resolved-stage-model assertion (works)

A new helper (`tests/evals/warmcost/cmd/p4preflight`, backed by
`ResolveStageModel`) resolves the stage model exactly as the pipeline does -
per-stage `stage-models.json` override, then the file `Default`, then the active
provider model - and aborts unless it equals MODEL. Wired into `paid-run.sh` and
the campaign launcher `p4-run.sh`. Offline guards:
`TestResolveStageModelOverrideBeatsPrimary`, `...FallsBackToPrimary`,
`...DefaultBeatsPrimary` pass.

Live check before spend:

```
$ /tmp/p4-preflight --splice-dir /tmp/p4r-cfg/splice --stage code_writer --want z-ai/glm-5.3-flash
p4preflight: stage=code_writer resolved_model=z-ai/glm-5.3-flash source=stage-models.json:code_writer ...   (exit 0)

$ /tmp/p4-preflight --splice-dir ~/.config/splice --stage code_writer --want z-ai/glm-5.3-flash
MODEL ASSERTION FAILED: stage code_writer resolves to "gpt-5.6-sol" ... aborting before any provider request   (exit 1)
```

The negative control is the exact defect that spent $0.1648 last time; it now
aborts before any request.

## 3. Mandatory preflight 2: artifacts on kill (works)

`runExec` now gives the child the raw file as a direct fd, so stdout streams to
the run directory; context cancellation is returned as an error even when the
killed process also yields an `*exec.ExitError`; and `runSequence` preserves the
partial attempt instead of replacing it. Guards:
`TestRunExecStreamsOutputToPath` and
`TestTimeoutAttemptKeepsPartialRawStream` pass.

Live proof: the smoke's 10m timeout produced `run_status=timeout`,
`ledger_error="attempt timeout after 10m0s; partial raw stream retained"`,
`raw_stream=raw-clock-helper-write-cold-r0.jsonl` (538 KB), and
`duration_ms=600016`. The two earlier killed runs left nothing; this one left
everything.

## 4. Offline replay of the recorded hedged payloads (required before spend)

`TestReplayRecordedP4HedgedPayloads` replays the six recorded `submit_code`
payloads from the blocked run (`smoke2-wrongmodel-raw.jsonl`, committed as
`internal/splice/stages/testdata/p4_smoke2_hedged.json`: all `modify
clock_test.go` with content + `base_ref="clock_test.go"` + edits).

- The `ad71955` normalizer collapses all six to exactly one representation and
  `Validate` accepts them - the "mixed representation" rejection is gone. This is
  the fix's stated contract and it holds.
- **Residual:** all six still fail `MaterializeProposal` with `matched span
  content not found in raw source; no write was attempted`. When the delivered
  base is the line-numbered `read_file` display view, the content-diff fallback
  derives edits over the display header/prefix lines, which the display-view
  hydration cannot map to the raw source. The explicit edits in these payloads
  actually match the *raw* source, so preferring them (rather than falling back
  to the content diff) would reach the materializer's raw-fallback path. This is
  a concrete next hypothesis for the implementing agent; it is not a harness
  defect.

## 5. Stage 0: clock-helper WRITE smoke (did not pass)

```
XDG_CONFIG_HOME=/tmp/p4r-cfg ARMS=cold REPEATS=1 MAX_RETRIES=0 RETENTION=fresh \
MODEL=z-ai/glm-5.3-flash TASKSET_SRC=tests/evals/warmcost/retention-taskset \
TASKS_ENV='clock-helper-write' RUN_BASE=wc-p4a-smoke-20260916T182032Z \
BIN=/tmp/p4a-splice BIN_PREBUILT=1 MEMD_BIN=/tmp/p4a-memd TIMEOUT=10m \
tests/evals/warmcost/paid-run.sh
```

| field | value |
| --- | --- |
| run_status | `timeout` (10m0s) |
| verifier | not run |
| submission | none - no `submit_code` tool call |
| tool calls | `list_directory`, `raw_file_read`, `read_file` (context only) |
| base_ref cited | none (nothing submitted) |
| format retries | unknown - no ledger (killed before the final event) |
| cost coverage | partial; 0 priced records |

The stream shows 5,899 reasoning events (~24 KB) ending with *"Output shape:
single submit_code call with the payload. Let me write the final JSON
carefully..."*. The model had the files and was mid-submission when the bound
fired. This is glm's reasoning latency (the config pins `reasoningEffort=high`),
not a protocol defect and not the base_ref contract. The same model stalled in
both attempts of the prior run.

Because the gate requires a submission that applies and verifies, the gate is
**not passed**, and the three-condition diagnostic (cold / automatic / manual)
was not started.

## 6. Spend

The attempt was killed before `applyRequestLedger`, so the pipeline ledger
carries no priced records; the attempt's billed USD is **unknown**, not zero.
The OpenRouter credential is a shared operator key, so its cumulative delta is
only an upper bound:

| item | USD |
| --- | ---: |
| key usage before smoke | 11.300143463 |
| key usage after smoke | 11.836105760 |
| upper-bound delta (shared key) | 0.535962 |
| ledger-reported cost | unknown (partial coverage; no final event) |

The addendum proposed a $0.20 envelope. Even allowing for contamination, the
observed rise is large, so all spend stopped here; the campaign is not
affordable on reliable accounting.

## 7. Unverified / not done

- The WRITE gate (submission, apply, verifier, zero format retries). The model
  never submitted.
- Path-cited `base_ref` in a live run - the fix was exercised only offline, and
  the recorded payloads still do not materialize (Section 4).
- The three-condition diagnostic; manual-selection capture gap recheck; whether
  improved cold resolves the TTL dependency.
- This attempt's real cost (ledger partial; shared key contaminated).

## 8. Verdict

`LIVE_SETUP_BLOCKED` - the stage-0 gate did not complete because glm exhausted
the 10-minute per-attempt bound in reasoning before submitting, and the spend
ceiling is effectively consumed. The two preflight additions are implemented and
proven live; the base_ref hedge normalizes offline but its recorded payloads do
not yet materialize through the display-view hydration path.
