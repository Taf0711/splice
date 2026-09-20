# Warm-cost measurement: wc-ab-armA-20260915T023826Z-try0

Generated: 2026-09-14 22:39:51 EDT

Revision: `e3e97b1cada3ac2df2bdf4a06f54ee4d2a108496`  Sidecar: `2f4ccde`  Model: `z-ai/glm-5.3-flash`

## Pre-registration

- Primary correctness endpoint: verifier pass or fail per attempt
- Noninferiority margin: 0.05
- Primary cost endpoint: billed USD per verified completion
- Secondary cost endpoint: billed USD per attempt
- Win rule: warm is a win only when the matched-pair cost delta is negative with a task-clustered interval that excludes zero, and correctness is noninferior to the margin
- Stopping rule: if warm does not reduce provider requests per verified completion on a corpus that triggers expansions, stop

## Retention protocol

- Mode: shared
- Sidecar root: `/tmp/wc-ab-armA-sidecar`
- warm: one shared sidecar for every attempt: <sidecar-root>/shared-warm
- Ordering rule: one workspace per (arm, sequence); the write-phase task and the read-phase tasks that follow it run in that workspace in order, and the runner interleaves arm order per repeat, so a write on the shared sidecar and in the workspace precedes its read

## Cost coverage

Attempts: 6. Partial attempts: 3. Complete: false.

Total-cost claim withheld: at least one attempt has partial coverage.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | input tok | output tok | cached tok | cache-write tok | reasoning tok | input tok/attempt | input tok/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warm | 6 | 3 | 3 | 1.00 | 0.0025 | 0.0004 | 0.0008 | 8157 | 2545 | 0 | 0 | 111 | 1360 | 2719 | 0.000 | 0.000 |

## Cost decomposition by spend source (warm minus cold)

| source | cold USD | warm USD | delta USD |
| --- | ---: | ---: | ---: |
| unspecified | 0.0000 | 0.0025 | +0.0025 |
| total | | | +0.0025 |

Cache channel: +0 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 10000. Tasks: 0. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | +0.0000 | +0.0000 |
| requests/attempt | +0.000 | +0.000 |
| success rate | +0.000 | +0.000 |

## Cache layout stability

Attempts: 6. With cache telemetry: 0. Without: 6.

Total prompt layout flips: 0.

6 of 6 attempt(s) emitted no cache telemetry, so the layout view is partial and the flip count is unknown for those attempts, not zero

Per-attempt per-round rows (requests, input tokens, cached tokens, cache hit
rate, spend source) and per-attempt distinct hashes are in each `attempt-*.json`
artifact under the run directory.

## Claim

Allowed: false. at least one run has partial cost coverage, so total cost is unknown

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |

