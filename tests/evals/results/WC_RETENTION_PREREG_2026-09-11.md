# Pre-registration: warm-cost retention run

Date: 2026-09-11.
Status: NOT RUN. Awaiting explicit owner approval for the exact command.
Entry point: `tests/evals/warmcost/paid-run.sh` with the retention flags below.
Runner: `tests/evals/warmcost/cmd/warmcost-eval`.

This document fixes the design before the first provider request. Nothing here
has run yet.

## 1. Why this run exists

The paid run `wc-paid-k3-20260911T141847-try0` used a fresh sidecar, so the warm
arms had no retained experience and every request had `context_round = 0`. That
run measured the enabled mechanisms at cold memory. It cannot test the warm-cost
claim.

This run adds the retention protocol: the warm arms share one sidecar that
persists across the tasks and repeats, so an earlier attempt's captured
evidence can be retrieved by a later attempt.

## 2. Pre-registration

- Model: to be named in the approval. One model for every arm.
- Arms: `cold`, `warm`, `warm-retrieval-only`.
- Repeats per task per arm: 3.
- Correctness noninferiority margin: 0.05.
- Bootstrap: 10,000 task-clustered samples, seed 1.
- Retention: `shared`.
- Sidecar root: a run-scoped directory, cleared before the run.
- Primary correctness endpoint: verifier pass or fail per attempt.
- Primary cost endpoint: billed USD per verified completion.
- Secondary cost endpoint: billed USD per attempt.
- Win rule: warm is a win only when the matched-pair cost delta is negative
  with a task-clustered interval that excludes zero, and correctness is
  noninferior to the margin.
- Stopping rule: if warm does not reduce provider requests per verified
  completion on a corpus that triggers expansions, stop.

## 3. Retention protocol

- `warm` and `warm-retrieval-only` share `<sidecar-root>/shared-warm`.
- `cold` gets a fresh sidecar per attempt, so it stays a clean control.
- Every task marked `"phase": "write"` runs before every other task. The
  runner interleaves arm order across repeats. A write therefore precedes a
  read on the shared sidecar.
- The runner sets `SPLICE_MEMD_SOCKET` and `SPLICE_MEMD_DB` per sidecar class.
  The splice binary auto-spawns one daemon per distinct socket.

## 4. Corpus

The corpus must do two things that the previous corpus did not:

1. Pair a write task with a read task, so captured evidence has a reader.
2. Trigger expansion rounds, so the round channel is measurable.

The corpus is not yet final. Section 5 states the blocker. The run does not
start until a corpus meets both conditions.

## 5. Blocker: expansion rounds are not deterministic

A provider-bearing round 1 needs the model to return a `request_context`
action. That is model behavior, not a host guarantee. The host-side handshake
in `internal/splice/stages/code_writer.go:32` issues no provider request, and
after the F1 fix the first provider-bearing round is labeled `generation`, not
`expansion`.

Two experiments decide it. They are small and they run before this
pre-registration is executed:

1. Offline: a stage test with a scripted provider that returns a
   `request_context` action once, then submits. It proves the expansion
   plumbing and the ledger label without a provider.
2. Live probe: one cheap run over candidate tasks, reporting `round_share` per
   task. Keep the tasks with a round share above zero.

## 6. Data capture and analysis

Per request: sequence, stage, iteration, invocation ordinal, context round,
spend source, input, output, cached, cache-write, and reasoning tokens, cost in
USD, and cost status.

Reported per arm: requests per verified completion, billed USD per attempt and
per verified completion, input and output token totals, cached and cache-write
tokens, reasoning tokens, input tokens per attempt and per verified completion,
round share, and cache share.

Cost decomposition is by SPEND SOURCE (`generation`, `repair`, `expansion`),
not by round and payload. Per-source deltas must sum to the total delta.

Analysis: task-clustered bootstrap over tasks, per-task effects before the
aggregate, failed attempts included in total spend.

## 7. Claim gating

A `fresh` retention run can never claim a total-cost result. A `shared` run
still withholds the claim when cost coverage is partial, when the cost delta is
not negative, or when the task-clustered interval includes zero.

## 8. Provenance

Per attempt: binary revision, sidecar revision, fixture digest, model id and
settings, treatment environment, and the session id. Run level: the retention
report, the arm list, the task list, and the repeat count.

## 9. Gate

```bash
gofmt -l .
go vet ./...
go test ./... -count=1
cd memd && go test ./... -count=1
go build ./tests/evals/warmcost/...
```

## 10. Approval

No paid run starts without explicit owner approval for the exact command, the
model, the task count, the repeat count, and the estimated request count.
