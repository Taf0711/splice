# Warm-cost measurement: wc-p4r-smoke2-20260916T173127Z-try0

Generated: 2026-09-16 13:33:08 EDT

Revision: `d3ce20dc9958d579087a87a43dffcfe3c4fdf2ce`  Sidecar: `d3ce20d`  Model: `z-ai/glm-5.3-flash`

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
- Ordering rule: one workspace per (arm, sequence); the write-phase task and the read-phase tasks that follow it run in that workspace in order, and the runner interleaves arm order per repeat, so a write on the shared sidecar and in the workspace precedes its read
- Note: no sidecar root is set, so every attempt uses the ambient operator sidecar

## Cost coverage

Attempts: 1. Partial attempts: 0. Complete: true.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | input tok | output tok | cached tok | cache-write tok | reasoning tok | input tok/attempt | input tok/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold | 1 | 0 | 6 | 0.00 | 0.1648 | 0.1648 | 0.0000 | 22922 | 4278 | 3456 | 0 | 328 | 22922 | 0 | 0.000 | 0.151 |

## Cost decomposition by spend source (warm minus cold)

| source | cold USD | warm USD | delta USD |
| --- | ---: | ---: | ---: |
| format_retry | 0.1140 | 0.0000 | -0.1140 |
| generation | 0.0508 | 0.0000 | -0.0508 |
| total | | | -0.1648 |

Cache channel: -3456 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 10000. Tasks: 0. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | +0.0000 | +0.0000 |
| requests/attempt | +0.000 | +0.000 |
| success rate | +0.000 | +0.000 |

## Cache layout stability

Attempts: 1. With cache telemetry: 0. Without: 1.

Total prompt layout flips: 0.

1 of 1 attempt(s) emitted no cache telemetry, so the layout view is partial and the flip count is unknown for those attempts, not zero

Per-attempt per-round rows (requests, input tokens, cached tokens, cache hit
rate, spend source) and per-attempt distinct hashes are in each `attempt-*.json`
artifact under the run directory.

## Claim

Allowed: false. the retention protocol is fresh, so the warm arm has no retained experience and a total-cost claim is not available

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |

