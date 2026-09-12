# Cold-only pilot round 3: contract confirmation

Date: 2026-09-12.
Branch: `wip/evidence-substitution-production` at `5585c7d`.
Model: `z-ai/glm-5.3-flash`. Cold arm only, 1 repeat, 3 tasks, cap 3 attempts, `MAX_RETRIES=0`, fresh retention.
Run id: `wc-pilot-cold-round3b-try0`.
Scope: reachability and attribution confirmation. No warm arm, no retention run, no cost claim.

## 0. Verdict

All three contract checks hold on a live model.

- **Fix C holds.** Five `request_context` actions were emitted, and **zero** lacked a `reason`. Round 2 rejected 4 of 8 for a missing `reason`.
- **Fix B holds.** Five `format_retry` records exist with prices. Round 1 had none.
- **Fix D holds.** Two payloads still declared `action: "submit_changes"` while carrying a `request_context` payload. Both were followed by `format_retry` records, which only occur on a typed-output validation failure. No silent downgrade was observed. The stream does not log the rejection text, so this check rests on the code path, its unit test, and the consistent retry records.

The bounded handling also fired live: `repair: code_writer re-entry: context expansion budget exhausted: 6 provider request(s) spent`. That is the explicit limitation the design requires, not an invented workaround.

## 1. Run facts

- HEAD: `5585c7d` (`5585c7daa40fa3dfefe4c5cbc01161b101501311`).
- Command: `ARMS=cold REPEATS=1 MAX_RETRIES=0 RETENTION=fresh MODEL=z-ai/glm-5.3-flash TASKSET_SRC=tests/evals/warmcost/pilot-taskset TASKS_ENV="error-envelope-from-doc clock-table-helper-reuse listen-address-from-config" RUN_BASE=wc-pilot-cold-round3b`.
- Wall time: about 13.6 minutes. Cap respected: 3 attempts.
- Cost coverage: `complete` on two attempts, `partial` on the aborted one, so the harness withheld the total-cost claim. That is correct behavior, not a failure of the run.

## 2. Request-contract checks

| round | HEAD | request_context emitted | without `reason` | missing a query bound | accepted | expansions fired |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1 | `0508a3c` | 22 | 1 (of the 22) | 18 | 3 | 3 |
| 2 | `c56e78e` | 8 | 4 | 0 | 4 | 4 |
| 3 | `5585c7d` | 5 | 0 | 0 | 5 | 5 |

The two prior invalid shapes are gone: no `read_file` query carries `symbol` instead of `path`, and no query omits `max_results` or `max_chars`.

## 3. Per-source spend

| source | calls | priced | unpriced | USD |
| --- | ---: | ---: | ---: | ---: |
| generation | 5 | 4 | 1 | 0.00322950 |
| format_retry | 5 | 5 | 0 | 0.00329297 |
| expansion | 5 | 5 | 0 | 0.00527195 |
| repair | 2 | 2 | 0 | 0.00128189 |
| **total** | **17** | **16** | **1** | **0.01307631 priced** |

The single unpriced record is the aborted `listen-address-from-config` attempt. No missing price was read as zero.

## 4. Per task

| task | verifier | run_status | provider calls | expansions | format_retry | outcome |
| --- | --- | --- | ---: | ---: | ---: | --- |
| `clock-table-helper-reuse` | PASS | completed | 5 | 2 | 1 | pass |
| `error-envelope-from-doc` | FAIL | failed | 7 | 2 | 2 | repair re-entry exhausted the expansion budget |
| `listen-address-from-config` | PASS | aborted | 5 | 1 | 2 | verifier passed, then wall time exceeded |

Two of three tasks pass the verifier, up from one of three in round 2. The `error-envelope-from-doc` failure is model output quality: the repair loop ran out of expansion budget after the change still failed verification.

## 5. What this does not establish

- No token or cost saving. This run measures whether the contract is reachable and correctly attributed.
- No warm-versus-cold effect. That requires the retention protocol with a shared sidecar and a write/read task pair.
- Fix D's rejection is inferred from the code path and the retry records, not from a logged error string.

## 6. Provenance

Per-attempt artifacts, raw streams, aggregate, and the harness report are under
`tests/evals/results/wc-pilot-cold-round3b-try0/`. The harness report is
`tests/evals/results/PAID_RUN_wc-pilot-cold-round3b-try0.md`.

The provider credential used for this run was supplied only as a process
environment variable. It was not written to any file in this repository, and a
grep of `tests/evals/` finds no occurrence of the key.
