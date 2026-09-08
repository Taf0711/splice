# E2 treatment program run ledger (2026-09-08)

Durable record of every live eval run in the E2 treatment-execution
campaign, for later publication. Raw JSONL lives beside each report in
tests/evals/results/. /tmp artifacts are NOT durable; everything below
is either in the repo or quoted here.

## Campaign state

- Fix branch: fix/e2-treatment-execution @ bdc3344 (base c572088).
  Review verdict: ACCEPT (all gates re-run, R1-R4 verified, negative
  controls reproduced). NOT yet merged to feat/mvp-paired-proof.
- Original E2 binary provenance: splice_commit f60da30, splice_dirty
  true on three batches. Re-run binary: bdc3344, clean tree.
- Model: z-ai/glm-5.3-flash via openrouter throughout.

## Run inventory

| run | date | treatment | family | rollouts/arm | attempts | outcome |
| --- | --- | --- | --- | --- | --- | --- |
| smoke-cold/delivery/delivery2/full/full2 (original) | 2026-09-08 | various | retention | 1 | ~5 | harness shakedown |
| batch-cold | 2026-09-08 | cold (ambient) | billing + retention | 3 | 14 | retention 3/3 vs 2/2; billing precursor failed |
| batch-delivery | 2026-09-08 | delivery-only | billing + retention | 3 | 14 | retention 3/3 vs 3/3; billing precursor failed |
| batch-full | 2026-09-08 | full | billing + retention | 3 (interrupted) | 13 | cold arm lost attempt 3 to harness timeout; batch abandoned |
| batch-full2 | 2026-09-08 | full | retention only | 3 | 7 | 3/3 vs 3/3 |
| billing-diag | 2026-09-08 | n/a (debug) | billing | n/a | n/a | E1 diagnosis: missing escalation tier in model patch, full artifacts |
| scope-only smoke | 2026-09-08 | scope-only | retention | 1 | 3 | contract realized; 1/1 vs 1/1 |
| scope-only batch | 2026-09-08 | scope-only | retention | 3 | 7 | 3/3 vs 3/3; +716 paired |

## Key numbers (paired per-attempt warm minus cold, within batch)

- retrieval-only: -100, -87 (median -94) - the only warm token saving
- delivery-only: +274, +270, +171 (median +270)
- scope-only: +552, +764, +716 (median +716)
- full: +1262, +1124, +2357 (median +1262)
- Aggregate: mean +664, median +552, warm wins 2, ties 0, losses 9
- Correctness: warm >= cold in 11/11 paired attempts. Zero regressions.
- Reads: 8 everywhere except scope-only and full warm arms (9).

## Standing conclusions (this campaign, this model tier)

1. Warm correctness invariant holds in every realized condition.
2. No warm token win in any delivering condition; savings exist only
   when nothing is delivered (retrieval-only).
3. Cost gradient tracks delivery: retrieval-only < delivery-only <
   scope-only < full. Scope policy is the expensive half of full
   (+716 with zero prose delivered, one extra read, zero expansions,
   zero suppression claimed).
4. Scope-only measured its intended triple for the first time in this
   re-run (retrieval on, delivery off, scope on). Pre-fix runs would
   have realized it as cold.

## Durable artifacts

- tests/evals/results/E2_DIAGNOSTIC_RUN_2026_09_08.md (corrected by
  bdc3344)
- tests/evals/results/E2_SCOPE_ONLY_RERUN_2026_09_08.md
- tests/evals/results/e2-scope-only-attempts.jsonl (7 rows)
- tests/evals/results/e2-scope-only-batch.log
- Skill record: devtools/splice-cognition-eval ->
  references/matched-run-findings.md (E2 scope-only re-run section)

## Publication gate

Per the claims discipline: no learning/cost-savings claims publicly
before the EV2-10 owner gate. The numbers above are diagnostic (one
family, one snapshot per batch, n=2-3). They may be published as
descriptive results with the caveats intact; they do not support any
efficiency claim for warm memory on GLM-5.3-flash, and none should be
written.
