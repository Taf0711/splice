# Harness-Bench Adapter

Runs Splice as a harness configuration in
[Qihoo360/harness-bench](https://github.com/Qihoo360/harness-bench)
(paper: arXiv 2605.27922, "Harness-Bench: Measuring Harness Effects across
Models in Realistic Agent Workflows").

## Upstream Verification (2026-09-05)

Verified over the network, not from memory:

- arXiv abs page `2605.27922` returns HTTP 200 with title
  "Harness-Bench: Measuring Harness Effects across Models in Realistic Agent
  Workflows".
- GitHub repo `Qihoo360/harness-bench` exists, default branch `main`, head
  commit `1025086a446653702b80cfb48babbeec35db6b2c`
  ("Update Harness-Bench 2.0 benchmark suite"). The repo has no git tags, so
  the head commit is pinned in `../../versions.lock.json`.
- The upstream extension interface is `src/harnessbench/adapters/base.py`:
  a `BaseAdapter` ABC with one abstract method
  `run(self, ctx: AdapterRunContext) -> AdapterRunResult`. Adapters are
  selected by name through the `models:` entries in
  `config/harness.yaml` (`"adapter": <name>`).
- Upstream task classes were enumerated from the `class:` field of all 106
  `tasks/<task_id>/task.yaml` files at the pinned commit:
  "Software Engineering & Codebase Maintenance" has 22 tasks,
  "SRE, DevOps & Release Ops" has 7 tasks.

## Status: VERIFIED UPSTREAM, SCAFFOLDING ONLY

The adapter scripts and config below implement a Splice harness entry per the
verified upstream interface. No paid run has been executed. The Splice side of
a live run needs: a built `splice` binary on PATH inside the harness
environment, provider credentials, and `HARNESSBENCH_SKIP_PROCESS_GRADE=1`
only if you want to skip the upstream LLM process rubric (not recommended; it
is part of the official score).

## Files

- `splice_adapter.py` - Drop-in upstream adapter module. Implements
  `BaseAdapter` and invokes `splice exec --output-format stream-json` with the
  task prompt, then preserves stdout, stderr, and the raw Splice stream-json
  trace under the run workspace.
- `harnessbench.splice.yaml` - Harness config fragment declaring the
  `splice-local` model entry. Copy into the upstream repo as
  `config/harness.yaml` (merged with the upstream example) or point
  `HARNESSBENCH_HARNESS_CONFIG` at it.
- `config.schema.json` - Static schema for the Splice adapter entry,
  validated by `smoke_verify.py`.
- `smoke_verify.py` - Static validation. No model calls. See below.
- `results/` - Created by a real run. Raw result preservation layout below.

## Deterministic Development Subset

`../../manifests/splice-harnessbench-se-sre.json` fixes 15 of the 22
Software Engineering & Codebase Maintenance tasks and all 7 SRE, DevOps &
Release Ops tasks, by task id, at the pinned upstream commit. Task selection
is committed and never derived from solved-status. Any number produced from
this subset is a `DEVELOPMENT SUBSET - NOT FULL BENCHMARK SCORE`.

## Raw Result Preservation Layout

Upstream writes results under its configured `results_dir`
(`results/<model_id>/<api_model_slug>/<task_id>.json`) and sandboxes under
`work_root/...`. This adapter adds a per-task evidence directory next to
nothing upstream owns; it writes only inside the adapter's own evidence root
passed via `SPLICE_HB_EVIDENCE_DIR` (defaults to
`<workspace>/.splice-harnessbench-evidence`):

```
<evidence_root>/<task_id>/
  stream.jsonl          Raw Splice --output-format stream-json output, byte-for-byte
  stdout.txt            Raw adapter process stdout
  stderr.txt            Raw adapter process stderr
  final.json            Parsed final Splice event (if present)
  metadata.json         Task id, model id, session id, command, exit code, timestamps
```

Never edit files under the upstream `results_dir` or this evidence root after
a run. The upstream result JSON remains the authoritative score artifact.

## Smoke Verification (no paid run)

```bash
python3 benchmarks/harnessbench/smoke_verify.py
```

Checks: the harness config parses as YAML, validates against
`config.schema.json`, the subset manifest is valid JSON whose task ids all
match the verified upstream category task lists, and the adapter module
parses as Python. It prints the exact full-run command.

## Full Run (paid; consumes provider tokens)

```bash
git clone https://github.com/Qihoo360/harness-bench
cd harness-bench && git checkout 1025086a446653702b80cfb48babbeec35db6b2c
pip install -e .
# merge benchmarks/configs/harnessbench.splice.yaml into config/harness.yaml,
# then, with SPLICE_BIN on PATH and provider credentials exported:
PYTHONPATH=src python3 -m harnessbench.cli run-suite \
  --harness splice-local --mode live
```

To run only the development subset, run the fixed task ids one at a time, for
example:

```bash
PYTHONPATH=src python3 -m harnessbench.cli run-task \
  --task 011-code-debug --harness splice-local --mode live
```

Every subset score must be published with the label
`DEVELOPMENT SUBSET - NOT FULL BENCHMARK SCORE`.
