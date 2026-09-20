# Live smoke: cold vs warm token usage, 2026-09-11

Owner-requested short live run. Revision `54bb470` (`wip/evidence-substitution-production`).
Model `z-ai/glm-5.3-flash` through OpenRouter. Isolated sidecar
(`/tmp/memd-smoke-dir`, fresh DB). Temp config directory with no
`stage-models.json`, so every stage used the cheap model.

## What ran

1. `splice eval pe --taskset /tmp/pe1 (1 task) --rollouts 1` with
   `SPLICE_SCOPE_MODE=on SPLICE_EVIDENCE_SUBSTITUTION=on`.
2. `warmcost-eval` (ledger runner) on 3 tasks from `taskset-v0`, arms cold and
   warm, 1 repeat, complete cost coverage.

## Result 1: eval pe, 1 task (sessions-active-since)

| arm | success | tokens |
| --- | --- | ---: |
| cold | true | 26,755 |
| warm | true | 3,496 |

Verdict: inconclusive (1/10 pairs). Cold is counted from stream-json usage;
warm is counted from the trace. The two sources are not symmetric, so this
number is not an apples-to-apples comparison.

## Result 2: ledger runner, 3 tasks, complete coverage

All six attempts verified. Every number is the authoritative request ledger.

| arm | attempts | verified | requests | input | output | billed USD | USD/attempt |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold | 3 | 3 | 10 | 33,751 | 4,165 | 0.0056 | 0.0019 |
| warm | 3 | 3 | 7 | 22,425 | 2,754 | 0.0030 | 0.0010 |

Aggregate warm is about 46 percent cheaper and uses 3 fewer requests. Per task:

| task | cold USD | warm USD | delta |
| --- | ---: | ---: | ---: |
| healthz-detail-gating | 0.00239 | 0.00068 | -0.0017 |
| len-skips-expired | 0.00188 | 0.00057 | -0.0013 |
| sessions-active-since | 0.00137 | 0.00172 | +0.0004 |

Task-clustered bootstrap (3 tasks, 1000 samples): billed USD per attempt delta
95 percent interval `[-0.0017, +0.0004]`. The interval includes zero, so the
runner withheld the total-cost claim. Correctness was 3/3 in both arms.

## Findings

1. The welcome news: there is a visible warm-cheaper direction on 2 of 3 tasks
   and in the aggregate, with complete ledger coverage. The unwelcome news: one
   task reversed, and with three tasks the interval includes zero. This is not
   a demonstrated saving.
2. The warm arm had no retained prior experience: the sidecar DB was fresh and
   each attempt used a different workspace, so no memory was restored. The
   difference therefore comes from run-to-run variance and the enabled
   mechanisms running without evidence, not from memory retrieval. Warm memory
   savings remain unproven.
3. Eval-stack break: `splice eval mvp` and `splice eval families` fail before
   any provider call with `treatment: unknown treatment "memory_off"` (and
   `"memory_on"`). The branch resolver accepts only cold, retrieval-only,
   delivery-only, scope-only, full. `eval pe` works because it uses the legacy
   no-treatment path. The mvp/families/campaign harnesses are unusable on this
   revision.
4. Ledger attribution break: in all 6 live attempts every request is labeled
   `spend_source=expansion`, `context_round=1`. There is no `generation`
   record. `CodeWriter.Run` (stages/code_writer.go:32-43) returns a context
   handshake on round 0 without calling the provider, so the first provider
   call is round 1 and is labeled expansion. The F2 round share is therefore
   1.0 and the round-versus-payload split is degenerate in live runs. The unit
   tests pass because they exercise the reclassification function, not the live
   loop.

## Verdict

Immediate behavior: warm ran cheaper in aggregate and on 2 of 3 tasks, but the
claim is withheld and one task reversed. No memory was restored, so this is not
a warm-memory effect. Fix the two eval-stack breaks before treating any live
number as evidence.
