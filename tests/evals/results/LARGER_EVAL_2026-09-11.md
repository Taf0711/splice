# Larger seeded warm-cost eval, 2026-09-11

Revision `54bb470` plus the eval-stack fixes (uncommitted). Model
`z-ai/glm-5.3-flash`. Frozen-warm protocol: the families harness resets each
warm attempt to pristine fixture bytes and re-seeds the family observation, so
cold and warm differ only by retained evidence.

Command:

```bash
SPLICE_EVIDENCE_SUBSTITUTION=on \
splice eval families \
  --manifest tests/evals/cognition-families/cognition-families.json \
  --taskset tests/evals/taskset-v0 \
  --out /tmp/fam-all-out --model z-ai/glm-5.3-flash --rollouts 1
```

10 families, 20 attempts, run 12:35:52 to 13:26:53. Raw rows:
`tests/evals/results/larger-eval-2026-09-11/families-attempts.jsonl`.

## Aggregate

| arm | attempts | success | abort | sum tokens | median tokens |
| --- | ---: | ---: | ---: | ---: | ---: |
| cold | 10 | 5/10 | 2 | 148,491 | 15,526 |
| warm | 10 | 4/10 | 2 | 161,668 | 15,401 |

Warm-minus-cold token delta: mean +1,318, median +367. Task-clustered
bootstrap (10 families, 2000 resamples) 95 percent interval
`[-6,701, +9,407]`, which includes zero. Warm is cheaper on 4 families and
more expensive on 6.

## Per family

| family | cold tok | warm tok | delta | cold ok | warm ok |
| --- | ---: | ---: | ---: | --- | --- |
| fam-01-password-reset-invalidation | 12,976 | 23,168 | +10,192 | true | false |
| fam-02-ttl-config-source | 24,468 | 17,842 | -6,626 | true | true |
| fam-03-delete-idempotence | 18,076 | 12,960 | -5,116 | true | true |
| fam-04-json-envelope-convention | 2,801 | 2,919 | +118 | false | false |
| fam-05-handler-error-mapping | 7,274 | 26,100 | +18,826 | false | false |
| fam-06-race-safety-rule | 7,085 | 29,767 | +22,682 | true | true |
| fam-07-probe-test-style | 19,605 | 20,221 | +616 | false | false |
| fam-08-toolchain-pin | 19,951 | 5,447 | -14,504 | true | true |
| fam-09-healthz-schema | 4,193 | 12,723 | +8,530 | false | false |
| fam-10-id-validation-rule | 32,062 | 10,521 | -21,541 | false | false |

Both-success subset (n=4): deltas [-6,626, -5,116, +22,682, -14,504],
mean -891, median -5,871. One warm regression of +22,682 dominates the mean.
Both-failure subset (n=5): mean +1,310.

## Verdict

The larger eval does not confirm the hypothesis. The point estimate is warm
slightly more tokens (mean +1,318, median +367), the interval includes zero,
and warm success is 4/10 against cold 5/10. Neither the cost nor the
correctness criterion of the win rule is met. Per-family variance
(-21,541 to +22,682 tokens) is far larger than the mean effect.

Aborts were balanced (2 cold, 2 warm): two duplicate-create repair aborts and
two repeated-unchanged-stage failures. They add variance but do not bias one
arm.
