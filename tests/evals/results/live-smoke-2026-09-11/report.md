# Warm-cost measurement: wc-pe3

Generated: 2026-09-11 12:03:00 EDT

Revision: `54bb470af0ae9f0c1b89fe4c5d9315a0e5c9e9b3`  Sidecar: ``  Model: `z-ai/glm-5.3-flash`

## Pre-registration

- Primary correctness endpoint: verifier pass or fail per attempt
- Noninferiority margin: 0.05
- Primary cost endpoint: billed USD per verified completion
- Secondary cost endpoint: billed USD per attempt
- Win rule: warm is a win only when the matched-pair cost delta is negative with a task-clustered interval that excludes zero, and correctness is noninferior to the margin
- Stopping rule: if warm does not reduce provider requests per verified completion on a corpus that triggers expansions, stop

## Cost coverage

Attempts: 6. Partial attempts: 0. Complete: true.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold | 3 | 3 | 10 | 3.33 | 0.0056 | 0.0019 | 0.0019 | 1.000 | 0.372 |
| warm | 3 | 3 | 7 | 2.33 | 0.0030 | 0.0010 | 0.0010 | 1.000 | 0.659 |

## Cost decomposition (warm minus cold)

| channel | USD |
| --- | ---: |
| total delta | -0.0027 |
| round channel | -0.0017 |
| payload channel | -0.0010 |

Cache channel: +2240 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 1000. Tasks: 3. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | -0.0017 | +0.0004 |
| requests/attempt | -3.000 | +1.000 |
| success rate | +0.000 | +0.000 |

## Claim

Allowed: false. the task-clustered interval for the cost delta does not exclude zero

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |
| healthz-detail-gating | 0.0024 | 0.0007 | -0.0017 | -3.00 | +0.00 |
| len-skips-expired | 0.0019 | 0.0006 | -0.0013 | -1.00 | +0.00 |
| sessions-active-since | 0.0014 | 0.0017 | +0.0004 | +1.00 | +0.00 |

