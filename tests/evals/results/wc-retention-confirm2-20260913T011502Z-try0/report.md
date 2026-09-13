# Warm-cost measurement: wc-retention-confirm2-20260913T011502Z-try0

Generated: 2026-09-12 21:16:48 EDT

Revision: `8c496067ed31e04b1d5f7273ec63b356078897ff`  Sidecar: `8c49606`  Model: `z-ai/glm-5.3-flash`

## Pre-registration

- Primary correctness endpoint: verifier pass or fail per attempt
- Noninferiority margin: 0.05
- Primary cost endpoint: billed USD per verified completion
- Secondary cost endpoint: billed USD per attempt
- Win rule: warm is a win only when the matched-pair cost delta is negative with a task-clustered interval that excludes zero, and correctness is noninferior to the margin
- Stopping rule: if warm does not reduce provider requests per verified completion on a corpus that triggers expansions, stop

## Retention protocol

- Mode: shared
- Sidecar root: `/tmp/warmcost-retention-confirm2-sidecar`
- warm: one shared sidecar for every attempt: <sidecar-root>/shared-warm
- Ordering rule: one workspace per (arm, sequence); the write-phase task and the read-phase tasks that follow it run in that workspace in order, and the runner interleaves arm order per repeat, so a write on the shared sidecar and in the workspace precedes its read

## Cost coverage

Attempts: 2. Partial attempts: 0. Complete: true.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | input tok | output tok | cached tok | cache-write tok | reasoning tok | input tok/attempt | input tok/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warm | 2 | 1 | 12 | 12.00 | 0.0077 | 0.0039 | 0.0077 | 63097 | 4219 | 32064 | 0 | 533 | 31548 | 63097 | 0.250 | 0.508 |

## Cost decomposition by spend source (warm minus cold)

| source | cold USD | warm USD | delta USD |
| --- | ---: | ---: | ---: |
| expansion | 0.0000 | 0.0030 | +0.0030 |
| format_retry | 0.0000 | 0.0023 | +0.0023 |
| generation | 0.0000 | 0.0024 | +0.0024 |
| total | | | +0.0077 |

Cache channel: +32064 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 10000. Tasks: 0. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | +0.0000 | +0.0000 |
| requests/attempt | +0.000 | +0.000 |
| success rate | +0.000 | +0.000 |

## Claim

Allowed: false. warm billed cost is not lower than cold

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |

