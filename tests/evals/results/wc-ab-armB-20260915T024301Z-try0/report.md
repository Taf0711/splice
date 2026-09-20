# Warm-cost measurement: wc-ab-armB-20260915T024301Z-try0

Generated: 2026-09-14 22:47:17 EDT

Revision: `d9c52b3560fbab839ee377a57a8ad8ae1414801d`  Sidecar: `2f4ccde`  Model: `z-ai/glm-5.3-flash`

## Pre-registration

- Primary correctness endpoint: verifier pass or fail per attempt
- Noninferiority margin: 0.05
- Primary cost endpoint: billed USD per verified completion
- Secondary cost endpoint: billed USD per attempt
- Win rule: warm is a win only when the matched-pair cost delta is negative with a task-clustered interval that excludes zero, and correctness is noninferior to the margin
- Stopping rule: if warm does not reduce provider requests per verified completion on a corpus that triggers expansions, stop

## Retention protocol

- Mode: shared
- Sidecar root: `/tmp/wc-ab-armB-sidecar`
- warm: one shared sidecar for every attempt: <sidecar-root>/shared-warm
- Ordering rule: one workspace per (arm, sequence); the write-phase task and the read-phase tasks that follow it run in that workspace in order, and the runner interleaves arm order per repeat, so a write on the shared sidecar and in the workspace precedes its read

## Cost coverage

Attempts: 6. Partial attempts: 3. Complete: false.

Total-cost claim withheld: at least one attempt has partial coverage.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | input tok | output tok | cached tok | cache-write tok | reasoning tok | input tok/attempt | input tok/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| warm | 6 | 3 | 3 | 1.00 | 0.0019 | 0.0003 | 0.0006 | 8142 | 2593 | 4780 | 0 | 123 | 1357 | 2714 | 0.000 | 0.587 |

## Cost decomposition by spend source (warm minus cold)

| source | cold USD | warm USD | delta USD |
| --- | ---: | ---: | ---: |
| unspecified | 0.0000 | 0.0019 | +0.0019 |
| total | | | +0.0019 |

Cache channel: +4780 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 10000. Tasks: 0. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | +0.0000 | +0.0000 |
| requests/attempt | +0.000 | +0.000 |
| success rate | +0.000 | +0.000 |

## Cache layout stability

Attempts: 6. With cache telemetry: 3. Without: 3.

Total prompt layout flips: 0.
No attempt reported a non-zero flip count.

| stage | distinct prompt layout hashes |
| --- | --- |
| code_writer | cc09a35d5dcfbe6e0e4a757243b998f4edc54934dbd382323cbc0abbbf78f692 |

3 of 6 attempt(s) emitted no cache telemetry, so the layout view is partial and the flip count is unknown for those attempts, not zero

Per-attempt per-round rows (requests, input tokens, cached tokens, cache hit
rate, spend source) and per-attempt distinct hashes are in each `attempt-*.json`
artifact under the run directory.

## Claim

Allowed: false. at least one run has partial cost coverage, so total cost is unknown

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |

