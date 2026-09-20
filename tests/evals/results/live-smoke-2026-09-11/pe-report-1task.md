# Paired eval: pe1

model: z-ai/glm-5.3-flash
provider: -
timestamp: 2026-09-11T11:53:50-04:00

## Verdict

**inconclusive** — insufficient pairs: 1/10

## Gates

| gate | outcome |
| --- | --- |
| evidence | fail — 1/10 pairs |

## Tasks

| task | cold success | warm success | cold tokens | warm tokens |
| --- | --- | --- | --- | --- |
| sessions-active-since | true | true | 26755 | 3496 |

## Arms

cold: 1 successes, 26755 tokens, 0 weighted interventions
warm: 1 successes, 3496 tokens, 0 weighted interventions

## Constants

pair floor: 10
cost margin: 0.90
success tolerance: 0
