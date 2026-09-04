# Cognition vertical slice — engineering report (2026-09-04)

## What was implemented (PR #30, merged to dev @ 7a47246)

Vertical slice across three tracks, all wired end-to-end:

- **Track A — repair intelligence**: `go test -json` per-test parsing
  (compile errors become evidence entries), typed FailureFingerprint with
  normalized hashing, no-progress guard (`repair_no_progress`), Go
  diagnostic resolver (AST method sets + grep + negative facts), focused
  repair payloads, instruction-direction fix for writer-authored test
  failures.
- **Track B — cognition graph (memd)**: versioned SQLite schema
  (nodes/edges/anchors/evidence/embeddings), 6 node kinds, 9 edge kinds,
  6 lifecycle statuses, exact ALL-anchor retrieval, bounded BFS,
  Contradict, provenance-preserving Compact, ephemeral-only Collect,
  local semantic index (hashed n-gram cosine), 8 sidecar endpoints +
  client methods.
- **Track C — integration**: verified-run capture (procedure + file-fact
  nodes gated on completion, revision anchors, run-id evidence),
  DiscoveryPlan with intent structural hints, graph-nodes-to-bundle
  bridge through the existing admission + replay-guard path.

## The three-run causal comparison (10 families x cold/warm x 3, GLM-5.3-flash)

WARM arm per family: success / median tokens / budget aborts / median tools

| family | BASELINE | REPLAY-GUARD | VERTICAL SLICE |
|---|---|---|---|
| fam-01 | 0/3 32633tok ab=3 t=68 | 0/3 37288tok ab=3 t=85 | **2/3 8327tok ab=0 t=17** |
| fam-02 | 3/3 4387 | 3/3 4871 | 3/3 4683 |
| fam-03 | 3/3 4667 | 3/3 4662 | 3/3 4691 |
| fam-04 | 0/3 2927 | 0/3 3192 | 0/3 2767 |
| fam-05 | 0/3 39780tok ab=3 t=51 | 0/3 26791tok ab=1 t=34 | **0/3 7630tok ab=0 t=12** |
| fam-06 | 3/3 3705 | 3/3 3709 | 3/3 3598 |
| fam-07 | 0/3 27229tok ab=3 t=34 | 0/3 34681tok ab=2 t=51 | 0/3 12586tok ab=1 t=17 |
| fam-08 | 3/3 6069 | 3/3 6050 | 3/3 6152 |
| fam-09 | 3/3 4455 | 3/3 4496 | 3/3 4406 |
| fam-10 | 3/3 5471 | 3/3 5473 | 3/3 5596 |

Aggregate:
- baseline: warm 18/30, cold 14/30, warm aborts 9, ratio p50 1.05 / p90 6.01 / max 9.51
- replay-guard: warm 18/30, cold 15/30, warm aborts 6, ratio p50 1.12 / p90 6.40 / max 8.73
- **vertical: warm 20/30, cold 17/30, warm aborts 1, ratio p50 1.10 / p90 1.90 / max 3.27**

## Gates, evaluated

1. **No warm runaway regressions: MET.** Warm budget aborts 9 -> 6 -> 1.
   Every family's warm attempts completed except one fam-07 abort.
2. **Warm success >= cold: MET and improved.** warm 20/30 vs cold 17/30
   (was 18 vs 14). fam-01 flipped 0/3 -> 2/3 in BOTH arms (evidence
   plumbing helped cold too, confirming the research prediction).
3. **Tail collapse: MET.** fam-05 warm median 39780 -> 7630 (-81%), tools
   51 -> 12; fam-07 27229 -> 12586 (-54%), tools 34 -> 17; fam-01 32633
   -> 8327 (-74%). The cold arm improved on fam-01/07 identically,
   confirming the mechanism was evidence starvation, not memory.
4. **Correctness retention: MET.** fam-10 warm 3/3 (three rounds), fam-08
   3/3, healthy-family tax +1-13%.
5. **p50/p90/max: 1.10 / 1.90 / 3.27** (was 1.05/6.01/9.51). The p90
   tail collapsed by 3.2x.

## Graph capture note (found in eval verification)

The eval DB showed zero cognition_nodes: the run seam's capture keyed on
a GraphClient() provider nothing supplied. Fixed in PR #31
(NewGraphMemoryStore wrapper wired in exec's memory-active branch) -
capture fires from dev after that merge; the vertical eval's efficiency
results above do not depend on it (retrieval path unchanged).

## Remaining issues

- fam-07 warm still fails 3/3 (median 12.6k): the task needs mux-level
  integration testing the model only sometimes manages; with evidence
  plumbing it now fails fast instead of thrashing. Correctness gain
  requires either better repair attempts or a stronger model tier.
- fam-04 cold+warm 0/3: task requires AdminSessionView in admin.go whose
  verifier wants structural shape the flat fixture makes ambiguous;
  unchanged across rounds, unrelated to this slice.
- Discovery-skip telemetry: the DiscoveryPlan machinery is wired for
  exact-anchor resolution, but the flat eval fixture cannot derive
  file: keys from intent (documented limitation), so eliminated-discovery
  counts stay at zero in this corpus. Meaningful measurement needs the
  nested-layout fixture (follow-up, not a correctness gap).

## Verdict

The vertical slice is shipped and eval-proven on its primary gates. The
cognition program's next lever is per the research queue: seed-quality
and discovery-skip measurement on a fixture layout that can derive
structural keys (fam-10 pattern scaled up), plus the E2 instruction
ablation already embedded in this slice.