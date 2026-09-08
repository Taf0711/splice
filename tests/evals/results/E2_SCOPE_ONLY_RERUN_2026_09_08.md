# E2 treatment re-run: scope-only realized contract + cold-vs-warm ledger, 2026-09-08

Owner-approved live re-run after the R1-R3 fixes landed on
`fix/e2-treatment-execution` @ bdc3344 (review accepted, not yet merged).
Results are durable here; raw artifacts live in /tmp/eval-e2scope/
(NOT durable) and /tmp/eval-e2/ (original E2, NOT durable).

## Scope decision (what a re-run can and cannot measure)

The matched runner passes NO explicit RunInput.Treatment (the Task B
RunInput in mvp_matched.go leaves Treatment unset; the ambient env
carries the treatment). Therefore:

- R1 (argv corruption on explicit treatment) and R2 (ambient
  SPLICE_TREATMENT override) were NEVER live in the original E2
  batches. Re-running those batches would reproduce the same numbers
  with better labels. Pure spend. Skipped.
- R3 (scope-only degeneration into cold) was the only behavior change,
  and the original E2 never ran scope-only. The scope-only batch is
  the one genuinely new measurement. Ran.
- R4 was report-only. No run needed.

## Run identity and provenance

- Binary: /tmp/splice-e2fix-bin/splice + splice-memd, built from
  bdc3344 with a clean tree. Attempts rows record
  splice_commit=bdc3344, splice_dirty unset (the original E2 binary
  was dirty f60da30 - provenance now clean).
- Model: z-ai/glm-5.3-flash via openrouter.
- Protocol: matched snapshots, family large-02-audit-retention-enforcer
  only (smoke-manifest.json; billing precursor fails 4/4 on this model
  tier with a diagnosed implementation bug, excluded to save spend),
  SPLICE_TREATMENT=scope-only, isolated state under
  /tmp/eval-e2scope (XDG_DATA_HOME + SPLICE_MEMD_DB/SOCKET; HOME
  untouched - keychain trap).
- Smoke first (1 rollout/arm) verified the realized contract before
  the full batch, per the dry-run-before-spend rule.
- Sidecar trap: splice-memd must sit BESIDE the splice binary (or on
  PATH or SPLICE_MEMD_BIN). First launch failed loud with "memory
  sidecar unavailable"; building memd from the memd/ module into the
  same bin dir fixed it.

## Scope-only contract realized for the first time

Before bdc3344, scope-only set --memory off, which takes the
deliberate-cold exec path: nil memory store, no retrieval, no scope
construction. The treatment degenerated into cold while reporting
scope-only. After the fix (--memory on + ExemplarModeRetrieveNoPrompt):

- Warm arm label: memory_on+scope-only (no override suffix).
- Retrieval ON: resolved_by_cognition=2, anchors validated 2,
  semantic hits 3 on every rollout (batch summary; attempts rows carry
  telemetry_found=true).
- Delivery OFF: surviving sidecar trace memory block reports
  items=0, chars=0 (retrieve-no-prompt).
- Scope ON: scope_expansions budget 2, expansions_performed=0,
  context_queries_default=9, executed=10.
- Cold arm label: memory_off+scope-only+retrieval_overridden_by_arm
  (honest: the arm memory flag contradicts the treatment's declared
  retrieval). No sidecar trace: deliberate-cold path.

Sidecar run_traces note: the warm reset-and-reseed before EVERY
attempt deletes the previous attempt's trace rows (by project path),
so only the last attempt's trace survives. Expected per-attempt causal
isolation, not data loss; earlier attempts' evidence lives in the
batch summary and the attempts JSONL.

## Scope-only batch results (retention family, Task B, n=3/arm)

All attempts share one fixture_tree (0dffe61d): matching holds inside
the batch.

| condition | n | success | median tokens | reads |
| --- | ---: | --- | ---: | ---: |
| cold | 3 | 3/3 | 6030 | 8 |
| scope-only | 3 | 3/3 | 6643 | 9 |

Paired per-attempt deltas (warm minus cold): +552, +764, +716
(median +716).

## Cold-vs-warm ledger across ALL realized conditions (11 paired attempts)

Per-attempt paired deltas within each batch (same snapshot, same
family, same model tier). These are the cleanest numbers the E2
program has; cross-batch medians remain invalid (distinct
fixture_tree hashes per batch).

| realized condition | batch | paired deltas | median | reads c/w | success c/w |
| --- | --- | --- | ---: | --- | --- |
| retrieval-only (n=2) | batch-cold | -100, -87 | -94 | 8/8 | 3/3 vs 2/2 |
| delivery-only | batch-delivery | +274, +270, +171 | +270 | 8/8 | 3/3 vs 3/3 |
| scope-only | this re-run | +552, +764, +716 | +716 | 8/9 | 3/3 vs 3/3 |
| full | batch-full2 | +1262, +1124, +2357 | +1262 | 8/9 | 3/3 vs 3/3 |

Aggregate: mean +664, median +552, warm wins 2, ties 0, losses 9.

### Findings

1. Correctness invariant holds: warm >= cold in 11/11 paired attempts
   across every condition. Zero regressions.
2. No warm token win at this model tier in any delivering condition.
   The only warm token saving is retrieval-only (median -94), the one
   condition where nothing reaches the model beyond the recorded
   retrieval trace.
3. Cost climbs monotonically with what is delivered to the model:
   retrieval-only (-94) < delivery-only (+270) < scope-only (+716) <
   full (+1262). The gradient tracks delivered prose plus scope work.
4. Scope policy is the expensive half of full: scope-only (+716)
   delivers zero prose yet costs more than delivery-only (+270, prose
   without scope). The scope policy, not cognition prose, drives most
   of full's overhead over delivery-only, and it converts zero
   expansions into one extra read (8 -> 9) in both scope-only and
   full.
5. If the MVP proof needs a warm arm on this tier, retrieval-only is
   the cheapest correct configuration; full is the most expensive
   measured. Full buys no correctness over retrieval-only here.

### Honest caveats

- One family, one snapshot per batch, n=2-3 per arm. Diagnostic
  weight, not campaign weight. retrieval-only keeps its n=2
  killed-attempt asterisk (agent_noncompletion, signal: killed).
- The scope-only +716 cannot be split into retrieval cost vs scope
  cost by this run alone; the triangulation across the four
  conditions is directional.
- The scope policy performed zero expansions and claimed no
  suppression in every attempt; the reads increase is the only
  behavioral signature it left on the host context path.
- expVerdicts unchanged from the original E2 report: correctness
  retained, no efficiency gain demonstrated, diagnostic not verdict.

## Reproduction

Scripts: /tmp/run-e2-scope-smoke.sh and /tmp/run-e2-scope-batch.sh
(NOT durable; shape recorded in the skill reference). Raw rows:
/tmp/eval-e2scope/batch-scope-only/families-attempts.jsonl (7 rows:
1 snapshot + 3 cold B + 3 warm B). Sidecar DB
/tmp/eval-e2scope/memd.db (11 cognition_nodes, 4 observations from
Task A capture; 2 surviving run_traces).
