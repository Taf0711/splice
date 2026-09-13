# Warm-cost measurement: wc-retention-seq-20260912T224429Z-try0

Generated: 2026-09-12 18:47:30 EDT

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
- Sidecar root: `/tmp/warmcost-retention-seq-sidecar`
- cold: a fresh sidecar per attempt: <sidecar-root>/fresh-<session-id>
- warm: one shared sidecar for every attempt: <sidecar-root>/shared-warm
- Ordering rule: one workspace per (arm, sequence); the write-phase task and the read-phase tasks that follow it run in that workspace in order, and the runner interleaves arm order per repeat, so a write on the shared sidecar and in the workspace precedes its read

## Cost coverage

Attempts: 4. Partial attempts: 0. Complete: true.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | input tok | output tok | cached tok | cache-write tok | reasoning tok | input tok/attempt | input tok/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold | 2 | 1 | 12 | 12.00 | 0.0073 | 0.0037 | 0.0073 | 55906 | 3859 | 24768 | 0 | 357 | 27953 | 55906 | 0.000 | 0.443 |
| warm | 2 | 2 | 7 | 3.50 | 0.0065 | 0.0032 | 0.0032 | 31316 | 4012 | 1920 | 0 | 194 | 15658 | 15658 | 0.143 | 0.061 |

## Cost decomposition by spend source (warm minus cold)

| source | cold USD | warm USD | delta USD |
| --- | ---: | ---: | ---: |
| expansion | 0.0000 | 0.0011 | +0.0011 |
| format_retry | 0.0035 | 0.0027 | -0.0008 |
| generation | 0.0029 | 0.0027 | -0.0002 |
| repair | 0.0009 | 0.0000 | -0.0009 |
| total | | | -0.0009 |

Cache channel: -22848 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 10000. Tasks: 2. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | -0.0029 | +0.0020 |
| requests/attempt | -6.000 | +1.000 |
| success rate | +0.000 | +1.000 |

## Claim

Allowed: false. the task-clustered interval for the cost delta does not exclude zero

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |
| clock-helper-read | 0.0047 | 0.0019 | -0.0029 | -6.00 | +1.00 |
| clock-helper-write | 0.0026 | 0.0046 | +0.0020 | +1.00 | +0.00 |

