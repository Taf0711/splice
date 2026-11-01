# Splice Benchmark Stack

Splice measures itself with external benchmark harnesses. This directory holds
the adapter scaffolding for those harnesses. It contains no Splice pipeline
code and adds no Python dependencies to the Go binary.

## Stack Philosophy

1. **The benchmark evaluator is authoritative.** Splice never computes its own
   benchmark score. Each harness runs its own oracle, rubric, or test suite,
   and that output is the only published verdict. Splice adapters launch the
   agent and preserve raw evidence. They do not judge tasks.

2. **No cross-benchmark aggregation.** Harness-Bench, Terminal-Bench 2.0, and
   SWE-bench Pro scores are never summed, averaged, or ranked against each
   other. Each benchmark measures a different construct. Report each score
   separately, next to its own pinned versions and commit ids.

3. **Subsets are labeled.** Any task subset is a development convenience. It
   must carry the label `DEVELOPMENT SUBSET - NOT FULL BENCHMARK SCORE` in the
   manifest, in the run output, and in every published number derived from it.
   A subset score is never a benchmark score.

4. **Pins are explicit.** Every upstream repo, commit, dataset version, and
   adapter version is recorded in `versions.lock.json`. No pin may use a
   floating ref such as `latest` or `main`. `benchmarks/versions_lock_test.go`
   enforces this at build time.

5. **Raw results are preserved.** Adapters keep the unmodified upstream result
   files plus the raw agent trace. Derived summaries live beside them, never
   in place of them. See each adapter README for its layout.

## Layout

```
benchmarks/
  README.md                 This file. Stack philosophy and rules.
  versions.lock.json        Pinned upstream versions and commits.
  versions_lock_test.go     Go test: schema, no floating pins, README checks.
  configs/                  Run configs for each harness.
  manifests/                Fixed development subset manifests.
  harnessbench/             Harness-Bench adapter (Qihoo360/harness-bench).
  harbor/                   Terminal-Bench 2.0 adapter (Harbor custom agent).
```

## Adapters

| Adapter | Upstream | Status |
|---|---|---|
| `harnessbench/` | Qihoo360/harness-bench (arXiv 2605.27922) | Verified upstream, scaffolding in place |
| `harbor/` | harbor-framework/harbor + terminal-bench@2.0 | Verified upstream, scaffolding in place |
| SWE-bench Pro | ScaleAI/SWE-bench_Pro | Placeholder in `versions.lock.json`, filled by another workstream |

## Rules for Contributors

- Run the smoke script before any paid run. `smoke_verify.py` in each adapter
  directory validates config and loading with no model calls.
- Fill `splice.commit`, `model.provider`, and `model.id` in
  `versions.lock.json` at run time. Never commit live credentials.
- Never select subset tasks by solved status. Selection is by task id, fixed
  in a committed manifest, with the selection rule written in the manifest.
