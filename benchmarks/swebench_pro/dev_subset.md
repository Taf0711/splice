# SWE-bench Pro development subset

This directory contains the Splice adapter for the OFFICIAL SWE-bench Pro
evaluation. Splice never judges a patch itself. The official evaluator does.

## Upstream pins (verified 2026-09-05)

| What | Value | How verified |
|---|---|---|
| Dataset | `ScaleAI/SWE-bench_Pro`, split `test`, 731 rows | HuggingFace dataset API |
| Dataset revision | `7ab5114912baf22bb098818e604c02fe7ad2c11f` | HuggingFace dataset API `sha` field |
| Evaluator repo | `https://github.com/scaleapi/SWE-bench_Pro-os` | GitHub API |
| Evaluator commit | `ca10a60a5fcae51e6948ffe1485d4153d421e6c5` | GitHub API `commits?per_page=1` |
| Docker images | `jefzda/sweap-images` (tag from the `dockerhub_tag` column) | Official README |

Notes on names:

- The Hugging Face organization is `ScaleAI`. A dataset id
  `scaleapi/SWE-bench_Pro-os` does not exist. The `-os` suffix belongs to the
  GitHub evaluator repository only.
- The public variant used here is the open dataset behind the public
  leaderboard. The commercial (private) split is not covered by this adapter.

## What the runner does

1. Checks out the pinned evaluator repo and fails on any commit drift
   (`VerifyEvaluatorRepo`).
2. For each dev-subset instance: prepares a per-instance checkout at
   `base_commit`, runs `splice exec --input-format stream-json
   --output-format stream-json` with the ORIGINAL issue text as the prompt
   (verbatim, no rewriting), and captures the final `git diff` as the patch.
3. Writes predictions in the exact JSON shape the official evaluator
   consumes: `[{"instance_id", "patch", "prefix"}]`
   (`WritePredictions`).
4. Invokes the official `swe_bench_pro_eval.py` from the pinned checkout
   (`InvokeOfficialEvaluator`). No custom pass/fail logic exists in this
   repository.

An empty patch is written as an empty string. The official evaluator scores
that as a failure, which is the honest result when Splice produced no diff.

## Development subset selection

`dev_subset.json` (mirrored by the `DevSubset` variable in `devsubset.go`)
holds 12 instance ids selected as follows:

```
rank(id) = sha256("swebench-pro-os-dev-subset-v1" + 0x00 + id)
```

Take the 12 lowest ranks over the full pinned id list. The selection is a
pure function of instance ids and the fixed salt. No solvability, difficulty,
or outcome signal was used, and none was available at selection time.
`SelectDevSubset` re-derives the manifest from any id list and fails loudly
on drift, so an upstream dataset edit or an accidental hand-edit surfaces as
an error.

## Running

```bash
go build ./benchmarks/swebench_pro/...
go test ./benchmarks/swebench_pro/ ./benchmarks/report/

# dry run: validate config, verify evaluator pin, write the manifest
go run ./benchmarks/swebench_pro/cmd/swebench-runner \
  -config benchmarks/swebench_pro/config.example.json -out /tmp/sb_out -dry-run
```

Full runs additionally need the dataset rows for the subset as JSON
(`SWEBENCH_PRO_ROWS_JSON`), with `instance_id`, `repo_url`, `base_commit`,
and `issue_text` per row.
