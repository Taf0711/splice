# Warm-cost measurement: wc-paid-k3-20260911T141847-try0

Generated: 2026-09-11 14:41:40 EDT

Revision: `64fec8f7587e598ceb6d0e385d7e5eb5585c9261`  Sidecar: `64fec8f`  Model: `z-ai/glm-5.3-flash`

## Pre-registration

- Primary correctness endpoint: verifier pass or fail per attempt
- Noninferiority margin: 0.05
- Primary cost endpoint: billed USD per verified completion
- Secondary cost endpoint: billed USD per attempt
- Win rule: warm is a win only when the matched-pair cost delta is negative with a task-clustered interval that excludes zero, and correctness is noninferior to the margin
- Stopping rule: if warm does not reduce provider requests per verified completion on a corpus that triggers expansions, stop

## Cost coverage

Attempts: 27. Partial attempts: 0. Complete: true.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold | 9 | 6 | 30 | 5.00 | 0.0119 | 0.0013 | 0.0020 | 0.000 | 0.678 |
| warm | 9 | 8 | 26 | 3.25 | 0.0101 | 0.0011 | 0.0013 | 0.000 | 0.714 |
| warm-retrieval-only | 9 | 7 | 44 | 6.29 | 0.0293 | 0.0033 | 0.0042 | 0.000 | 0.606 |

## Cost decomposition (warm minus cold)

| channel | USD |
| --- | ---: |
| total delta | -0.0018 |
| round channel | -0.0016 |
| payload channel | -0.0002 |

Cache channel: -7808 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 10000. Tasks: 3. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | -0.0006 | +0.0005 |
| requests/attempt | -1.000 | +0.333 |
| success rate | +0.000 | +0.667 |

## Claim

Allowed: false. the task-clustered interval for the cost delta does not exclude zero

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |
| healthz-detail-gating | 0.0009 | 0.0004 | -0.0005 | -0.67 | +0.00 |
| len-skips-expired | 0.0007 | 0.0012 | +0.0005 | +0.33 | +0.00 |
| sessions-active-since | 0.0023 | 0.0018 | -0.0006 | -1.00 | +0.67 |

