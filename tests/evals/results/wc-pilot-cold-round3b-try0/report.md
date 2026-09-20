# Warm-cost measurement: wc-pilot-cold-round3b-try0

Generated: 2026-09-12 13:59:05 EDT

Revision: `5585c7daa40fa3dfefe4c5cbc01161b101501311`  Sidecar: `5585c7d`  Model: `z-ai/glm-5.3-flash`

## Pre-registration

- Primary correctness endpoint: verifier pass or fail per attempt
- Noninferiority margin: 0.05
- Primary cost endpoint: billed USD per verified completion
- Secondary cost endpoint: billed USD per attempt
- Win rule: warm is a win only when the matched-pair cost delta is negative with a task-clustered interval that excludes zero, and correctness is noninferior to the margin
- Stopping rule: if warm does not reduce provider requests per verified completion on a corpus that triggers expansions, stop

## Retention protocol

- Mode: fresh
- Sidecar root: (ambient, operator-managed)
- cold: a fresh sidecar per attempt: <sidecar-root>/fresh-<session-id>
- Ordering rule: write-phase tasks run before read-phase tasks, and the runner interleaves arm order per repeat, so a write on the shared sidecar precedes a read of it
- Note: no sidecar root is set, so every attempt uses the ambient operator sidecar

## Cost coverage

Attempts: 3. Partial attempts: 1. Complete: false.

Total-cost claim withheld: at least one attempt has partial coverage.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | input tok | output tok | cached tok | cache-write tok | reasoning tok | input tok/attempt | input tok/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold | 3 | 2 | 17 | 8.50 | 0.0131 | 0.0044 | 0.0065 | 74315 | 8973 | 21312 | 0 | 1045 | 24772 | 37158 | 0.294 | 0.287 |

## Cost decomposition by spend source (warm minus cold)

| source | cold USD | warm USD | delta USD |
| --- | ---: | ---: | ---: |
| expansion | 0.0053 | 0.0000 | -0.0053 |
| format_retry | 0.0033 | 0.0000 | -0.0033 |
| generation | 0.0032 | 0.0000 | -0.0032 |
| repair | 0.0013 | 0.0000 | -0.0013 |
| total | | | -0.0131 |

Cache channel: -21312 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 10000. Tasks: 0. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | +0.0000 | +0.0000 |
| requests/attempt | +0.000 | +0.000 |
| success rate | +0.000 | +0.000 |

## Claim

Allowed: false. at least one run has partial cost coverage, so total cost is unknown

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |

