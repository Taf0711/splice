# Warm-cost paired measurement runner

This directory holds the runner for the design in
`tests/evals/cognition-families/MEASUREMENT_DESIGN.md`.

It is out of CI. It has no test files, so `go test ./...` never runs it. It
runs on the release cadence.

## What it measures

Two or three arms over the same tasks, repeated:

- `cold`: `SPLICE_SCOPE_MODE=off`, `SPLICE_EVIDENCE_SUBSTITUTION=off`, memory off.
- `warm`: both switches on, memory on.
- `warm-retrieval-only` (optional): scope on, substitution off, memory delivery
  off via `SPLICE_EXEMPLAR_MODE=retrieve-no-prompt`.

Per attempt it captures the authoritative request ledger from the run's final
stream-json event. That JSON is the `PipelineResult` produced after
`applyRequestLedger`, so every number is the orchestrator's own total, not a
hand sum.

For each request it records the sequence, stage, iteration, invocation
ordinal, context round, spend source, input and output tokens, cached and
cache-write tokens, reasoning tokens, cost in USD, and the cost status.

It marks an attempt partial when `cost_coverage` is not complete and withholds
the total-cost claim for the run. A missing price is never read as zero.

The aggregate reports per arm: requests per verified completion, billed USD per
attempt, billed USD per verified completion, round share, and cache share. It
splits the warm-minus-cold bill into a round channel and a payload channel. The
cache channel is reported in tokens because the ledger has no per-token cache
price. It bootstraps over tasks, not requests, and reports per-task effects
before the aggregate.

## Taskset format

`--tasks <dir>` expects:

```text
<dir>/tasks/*.json      one task per file:
                        {"id": "...", "prompt": "...", "check": "shell command",
                         "fixture": "optional path under <dir>"}
<dir>/fixture/          optional default fixture, copied per attempt
```

A task passes when its `check` exits 0 in the attempt workspace.

## Invocation

Offline shape (replace the binary with a real build for a provider run):

```bash
go run ./tests/evals/warmcost/cmd/warmcost-eval \
  --repo  . \
  --binary /path/to/splice \
  --model  <approved-model> \
  --tasks  tests/evals/warmcost/taskset-example \
  --out    tests/evals/results \
  --repeats 3 \
  --arms cold,warm \
  --correctness-margin 0.05 \
  --sidecar-revision <revision>
```

Provider runs are owner-gated. Do not start one without the exact approval
described in the measurement design.

## Artifacts

One JSON per attempt plus `aggregate.json` and `report.md`, under
`<out>/<run-id>/`.
