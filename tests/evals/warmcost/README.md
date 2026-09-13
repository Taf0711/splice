# Warm-cost paired measurement runner

This directory holds the runner for the design in
`tests/evals/cognition-families/MEASUREMENT_DESIGN.md`.

It is out of CI for the paid run. The unit tests in this package are
provider-free and fast, so `go test ./...` runs only those. The paid run
itself runs on the release cadence.

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
attempt, billed USD per verified completion, the input and output token
totals, cached and cache-write tokens, reasoning tokens, input tokens per
attempt, input tokens per verified completion, round share, and cache share.

It splits the warm-minus-cold bill by SPEND SOURCE, not by round and payload.
The source names come from the ledger (`generation`, `repair`, `expansion`,
`format_retry`, `capture`), so a repair-cost difference is never relabeled as a
round effect. The per-source deltas must sum to the total delta, and the runner
fails loud when they do not. The cache channel is reported in tokens because
the ledger has no per-token cache price. It bootstraps over tasks, not
requests, and reports per-task effects before the aggregate.

## Retention protocol

`--retention fresh|shared` selects the sidecar lifetime protocol.

- `fresh` (default): every attempt gets its own sidecar under
  `<sidecar-root>/fresh-<session-id>`. The warm arms then have no retained
  experience, so the run measures the enabled mechanisms at cold memory. A
  fresh run can never claim a total-cost result.
- `shared`: the warm and warm-retrieval-only arms share ONE sidecar under
  `<sidecar-root>/shared-warm` that persists across the tasks and repeats, so
  an earlier attempt's captured evidence can be retrieved by a later attempt.
  The cold arm keeps a per-attempt sidecar so it stays a clean control.
  `shared` requires `--sidecar-root`.

With no `--sidecar-root`, every attempt uses the ambient operator sidecar.
The runner assigns `SPLICE_MEMD_SOCKET` and `SPLICE_MEMD_DB` per class, and the
splice binary auto-spawns a sidecar daemon for each distinct socket.

Task order: a task with `"phase": "write"` runs before every other task, so a
write on the shared sidecar precedes a read of it. Tasks keep their taskset
order inside each phase.

## Sequences

One workspace serves each (arm, sequence). A write-phase task starts a sequence.
The tasks after it, up to the next write-phase task, run in that same workspace,
in order, with one verifier and one ledger record each. The runner resets the
workspace contents at the start of every repeat and never between the tasks of a
sequence, so a write task's bytes persist for the read task after it. A taskset
with no write-phase task keeps one workspace per task.

The workspace path is stable across repeats for one (arm, sequence), so the
runtime memory project identity is also stable. That is what lets a later read
task retrieve an earlier write task's captured evidence on the shared sidecar.

When a sequence contains a write-phase task, the runner initializes the
workspace as a git repository with one initial commit and a local identity
(`warmcost@example.invalid`). The runtime capture path anchors evidence at a git
revision, and a workspace with no repository produces no reusable record. A
taskset with no write-phase task keeps the previous plain workspace behavior.

After a write-phase task's verifier passes, and before the next task of the
sequence runs, the runner commits the verified tree and advances that run's
captured nodes from the pre-verify HEAD to the post-verify commit. The stage
sandbox refuses the write-shaped `git stash create`, so in-run capture anchors
at the pre-verify HEAD. The commit does not change what the run verified, it
only names the same bytes at a new revision, so the read task's freshness diff
is empty and the write task's evidence is admissible. The commit runs in every
arm, so the workspace treatment is identical. Only arms with memory on reanchor,
because a cold arm captures nothing. A write task whose verifier fails is never
committed.

Clear the sidecar root before a `fresh` run. Keep it for the duration of a
`shared` run.

## Taskset format

`--tasks <dir>` expects:

```text
<dir>/tasks/*.json      one task per file:
                        {"id": "...", "prompt": "...", "check": "shell command",
                         "fixture": "optional path under <dir>",
                         "phase": "optional: write or read"}
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
  --retention shared \
  --sidecar-root /tmp/warmcost-sidecar \
  --sidecar-revision <revision>
```

Provider runs are owner-gated. Do not start one without the exact approval
described in the measurement design.

## Artifacts

One JSON per attempt plus `aggregate.json` and `report.md`, under
`<out>/<run-id>/`.
