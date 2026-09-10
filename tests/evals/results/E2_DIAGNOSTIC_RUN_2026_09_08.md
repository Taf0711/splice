# E2 diagnostic run results, 2026-09-08

Model: z-ai/glm-5.3-flash via openrouter. This is the same provider and model as the
original rounds.

Protocol: matched snapshots, 1 verified natural precursor per family, 3 Task B rollouts
per arm, SPLICE_TREATMENT applied per batch to the whole process. Artifacts are under
/tmp/eval-e2/.

## Binary provenance

The attempt rows record `splice_commit=f60da30`. An earlier version of this report named
babfc74. The rows are the authority, because the harness writes them at run time. The
batches batch-delivery, batch-full, and batch-full2 also record `splice_dirty=true`. The
binary for those batches had uncommitted changes. No commit alone identifies those bytes.

## How to read the arms

The harness runs two arms per batch. The cold arm sets memory off. The warm arm sets
memory on. SPLICE_TREATMENT applies to the whole process, so the treatment modifies the
warm arm of that batch. The realized conditions are:

- batch-cold warm arm: retrieval only
- batch-delivery warm arm: delivery only
- batch-full2 warm arm: full

EACH BATCH HAS ITS OWN COLD ARM. The cold arms are not one shared baseline. The fixture
tree differs between batches: the four batches recorded four distinct `fixture_tree`
hashes (5b44beef..., 50d4bba4..., a7ecb472..., 51ede561...). Matching holds INSIDE a
batch, because both arms of a batch share one snapshot. Matching does NOT hold across
batches. Compare each treatment median against the cold median of its own batch.

## Retention family results

Family large-02-audit-retention-enforcer, Task B outcomes. Each row shows the treatment
and the cold arm of the SAME batch.

| batch | condition | n | success | median tokens | own cold median | delta vs own cold |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| batch-cold | retrieval only | 2 | 2/2 | 5968.5 | 6018 (n=3, 3/3) | -49.5 |
| batch-delivery | delivery only | 3 | 3/3 | 6232 | 5958 (n=3, 3/3) | +274 |
| batch-full2 | full | 3 | 3/3 | 7171 | 5950 (n=3, 3/3) | +1221 |

The retrieval-only row has n=2. The third warm rollout was killed during the run. Its row
records `failure_category=agent_noncompletion` and the error `signal: killed`. The kill is
an infrastructure event, not a model failure. Two rollouts are too few to support a claim
about retrieval-only cost.

Median reads were 8 in every condition. The full condition read 9.

## Excluded batch

The batch-full data is not in the table above. That batch was interrupted. Its warm arm
completed 2 rollouts and the third never ran. Its cold arm also lost attempt 3 to a
harness timeout, so that arm is 2/3. Every "full" row in this report comes from
batch-full2, which completed 3 rollouts per arm.

## What the data supports

Correctness was retained. No warm arm scored below the cold arm of its own batch.

No token advantage was measured for any warm condition. Cost increased with the quantity
of cognition prose that reached the model. Delivery only cost +274 tokens against its own
cold arm. Full cost +1221 tokens against its own cold arm.

Reads did not decrease. The semantic priority context path changed the order of reads. It
did not remove any read.

Telemetry from the semantic priority warm path: `context_queries_default=9`,
`executed=10`, `context_failures=0`. The value `discovery_resolved_by_cognition` was 2,
with 2 anchors validated and 3 semantic hits. The value `expansions_performed` was 0 and
no suppression was claimed. The resolved questions did not authorize the harness to omit
a default operation. Authority stayed local, as designed.

## What the data does not support

This run cannot rank the three treatments against each other. Each treatment ran against a
different snapshot, so a cross batch difference includes the snapshot difference.

This run cannot measure retrieval-only cost. That condition has 2 rollouts.

This run is one family, one snapshot per batch, and 3 rollouts per arm. It is a diagnostic
result. It is not a broad MVP verdict.

## Billing family

Family large-01-billing-dunning-notice. The precursor failed in every batch. The harness
counted 1 failed snapshot and skipped the targets.

The E1 diagnosis is complete, with artifacts under /tmp/eval-e2/billing-diag/debug/. The
verifier rejected the implementation at `TestProbeRecordDunningNoticeIsolation`. The third
notice on one account returned `LevelFinal` instead of `LevelSuspended`. The captured
patch shows a hand written two level escalation (None to First to Final). That escalation
never grants `LevelSuspended`, and it does not call the fixture helper
`DunningLevel.Next()`.

Classification: a source level implementation bug, specifically a missing escalation tier.
It is not a verifier defect. It is not a harness defect. It is not a model capability
limit, because the same model passed this precursor 6 times out of 6 in the bridge4 rounds.

The earlier rounds saved no artifacts, because of the F5 defects. The cause of those
earlier failures cannot be recovered. This reproduction is new evidence with full
artifacts.

## Verdict

Correctness was retained. No efficiency gain was measured. Read this as a diagnostic
result for one family, per the handoff.
