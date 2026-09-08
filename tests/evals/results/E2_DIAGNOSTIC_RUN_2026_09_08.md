# E2 diagnostic run results, 2026-09-08

Binary: branch feat/mvp-paired-proof at babfc74 (worktree /private/tmp/splice-restore).
Model: z-ai/glm-5.3-flash via openrouter (same provider/model as the original rounds).
Protocol: matched-snapshots, 1 verified natural precursor per family, 3 B rollouts per arm,
SPLICE_TREATMENT process-wide per batch. Artifacts under /tmp/eval-e2/.

IMPORTANT realized-treatment note: the harness runs two arms (cold: memory off, warm: memory on)
and applies SPLICE_TREATMENT to the whole process. The realized conditions are therefore:
- cold arm in every batch: true cold (memory off, baseline context)
- batch-cold warm arm: retrieval-only (memory on + cold-treatment env)
- batch-delivery warm arm: delivery-only
- batch-full2 warm arm: full
The rows in families-attempts.jsonl before commit babfc74 mislabeled the warm treatment as
memory_on alone; babfc74 fixes the label for future runs. The realized mapping above comes
from the batch invocation env, not the row labels.

Retention family (large-02-audit-retention-enforcer), Task B outcomes:

| realized condition | n | success | median tokens | median reads |
| --- | --- | --- | --- | --- |
| true cold (baseline) | 3 | 3/3 | 6018 | 8 |
| retrieval-only | 2 | 2/2 | 5968.5 | 8.0 |
| delivery-only | 3 | 3/3 | 6232 | 8 |
| full | 3 | 3/3 | 7171 | 9 |

Correctness: the warm arms never scored below cold (retrieval-only 2/2, delivery-only 3/3,
full 3/3 vs baseline 3/3). Correctness retained.

Efficiency: NO token advantage for warm. Delivery costs are visible and scale with how much
cognition prose reaches the model: retrieval-only ~5969 median (near-parity with cold),
delivery-only +274 vs cold, full +1221 vs cold. Reads stayed 8 (warm 9 under full) in every
condition: the semantic-priority context path reordered reads but did not reduce them.
discovery_resolved_by_cognition = 2 with anchors validated 2 and semantic hits 3, yet
expansions_performed = 0 and no suppression was claimed: the resolved questions did not
authorize omitting any default operation (authority stayed local, as designed).

Telemetry: context_queries_default=9, executed=10 for the semantic-priority warm path,
context_failures=0. Honest expansion accounting (performed=0, remaining=2) confirmed live.

Billing family (large-01-billing-dunning-notice): the precursor failed again in every batch
(1 failed snapshot, targets skipped and counted honestly). E1 DIAGNOSIS COMPLETE WITH ARTIFACTS
(/tmp/eval-e2/billing-diag/debug/...): the verifier rejected the implementation at
TestProbeRecordDunningNoticeIsolation - the third notice on one account returned LevelFinal
instead of LevelSuspended. The captured patch shows a hand-rolled two-level escalation
(None->First->Final) that never grants LevelSuspended, and does not use the fixture helper
DunningLevel.Next(). Classification: source-level implementation bug (missing escalation tier),
NOT a verifier defect, NOT a harness defect, NOT a model-capability ceiling: the same model
passed this precursor 6/6 in the bridge4 rounds. The earlier rounds left no artifacts (the F5
defects), so their cause is unrecoverable; this reproduction is new evidence with full artifacts.

Verdict (diagnostic, single family, 3 rollouts, one snapshot): correctness retained with no
measured efficiency gain. This is a diagnostic result per the handoff, not a broad MVP verdict.
