# Warm-cost measurement: wc-pilot-cold-20260912T121001-try0

Generated: 2026-09-12 12:11:24 EDT

Revision: `0508a3c8988d170952b8d4819bd56d8e1c33c556`  Sidecar: `0508a3c`  Model: `z-ai/glm-5.3-flash`

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

Attempts: 3. Partial attempts: 0. Complete: true.

## Arms

| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | input tok | output tok | cached tok | cache-write tok | reasoning tok | input tok/attempt | input tok/verified | round share | cache share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| cold | 3 | 1 | 27 | 27.00 | 0.0102 | 0.0034 | 0.0102 | 113745 | 5856 | 81920 | 0 | 882 | 37915 | 113745 | 0.148 | 0.720 |

## Cost decomposition by spend source (warm minus cold)

| source | cold USD | warm USD | delta USD |
| --- | ---: | ---: | ---: |
| expansion | 0.0028 | 0.0000 | -0.0028 |
| generation | 0.0074 | 0.0000 | -0.0074 |
| total | | | -0.0102 |

Cache channel: -81920 tokens. ledger carries cache-read and cache-write tokens but no per-token cache price, so the cache channel is reported in tokens and is not folded into the USD split

## Task-clustered bootstrap

Unit: task. Samples: 10000. Tasks: 0. Seed: 1.

| delta | lower 2.5% | upper 97.5% |
| --- | ---: | ---: |
| billed USD/attempt | +0.0000 | +0.0000 |
| requests/attempt | +0.000 | +0.000 |
| success rate | +0.000 | +0.000 |

## Claim

Allowed: false. the retention protocol is fresh, so the warm arm has no retained experience and a total-cost claim is not available

## Per-task effects

| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |
| --- | ---: | ---: | ---: | ---: | ---: |

