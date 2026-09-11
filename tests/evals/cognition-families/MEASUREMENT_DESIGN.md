# Warm-cost measurement design

Date: 2026-09-11.
Status: design. Out of CI. Runs on the release cadence.
Purpose: settle whether warm sessions lower total cost at noninferior correctness, and whether round reduction or payload reduction carries the saving.

## 1. Question

The product claim is: a warm session costs less than a cold session at equal or better correctness. Two sub-questions decide it:

1. Does the warm arm lower cost per verified completion?
2. Does the saving come from fewer provider requests (rounds), from a smaller payload per request, or from neither?

The round is only one factor. Total cost is a sum over provider requests: `C = sum_i (I_i * c_in + q * O_i) + cache terms`. A change that lowers `I_i` at a fixed request count lowers `C`. So a round-only criterion is wrong. This design measures both channels.

## 2. Arms

Run the same task list in every arm. Treat the task as the cluster.

- **cold**: no retained experience. Scope off. Substitution off.
- **warm**: retained experience available. Scope on. Substitution on (`SPLICE_EVIDENCE_SUBSTITUTION=on`).
- **warm-retrieval-only** (optional): scope on for retrieval telemetry, delivery off. This separates retrieval overhead from delivery and from work removal.

The scope default is now off (`internal/splice/scope_mode.go`). Set `SPLICE_SCOPE_MODE=on` explicitly for the warm arm.

## 3. Pre-registration, before the run

State these in the report before any run:

- **Primary correctness endpoint**: verifier pass or fail per attempt.
- **Noninferiority margin**: the largest acceptable drop in warm success rate against cold. Derive it from pilot variance. Do not guess it after the run.
- **Primary cost endpoint**: billed USD per verified completion.
- **Secondary cost endpoint**: billed USD per attempt.
- **Secondary efficiency endpoints**: provider requests per verified completion; input, output, cache-read, and cache-write tokens per request.
- **Win rule**: warm is a win only when the matched-pair cost delta is negative with an interval that excludes zero, and correctness is noninferior to the margin.
- **Stopping rule**: if warm does not reduce provider requests per verified completion on a corpus that triggers expansions, stop. Do not tune the mechanism further.

## 4. Data capture per request

Use the authoritative request ledger (`applyRequestLedger`, `internal/splice/run.go`). For each request record:

- `Sequence`, `Stage`, `Iteration`, `InvocationOrdinal`, `ContextRound`.
- `SpendSource`: one of `generation`, `expansion`, `format_retry`, `repair`, `capture`.
- `InputTokens`, `OutputTokens`, `CachedTokens`, `CacheWrite`.
- `CostUSD` and the cost status.

Rules:

- Assert `CostCoverage == complete` for a run. If a run is partial, report it as partial and withhold the total-cost claim. Never read a missing price as zero.
- Round split: `generation` is round 0; `expansion` is round 1 and later (`run.go:1349`). Report both.
- Report cache-read and cache-write tokens apart from raw input tokens. Report the cache key (`SessionID:stage`) and the observed hit share.
- Attribute every request. A request that carries no `SpendSource` is a capture gap, not a baseline request.

## 5. Estimands

Per arm, over tasks:

- `R`: provider requests per verified completion.
- `I = sum I_i`, `O = sum O_i`, `C = sum cost`.
- Round share: requests with `SpendSource == expansion` divided by `R`.
- Cache share: cache-read tokens divided by total input tokens.

Decompose the cost change `C_warm - C_cold`:

1. **Payload channel**: hold the request count fixed and change per-request input and output.
2. **Round channel**: change the request count and hold per-request payload fixed.
3. **Cache channel**: the change in cache-read and cache-write terms at fixed tokens.

Report the three parts. Do not attribute a total difference to the round count without this split.

## 6. Design and uncertainty

- Paired: run every task in both arms. Repeat `k` times per arm. Interleave the arm order across repeats to reduce drift.
- Include failed attempts in total spend. Report spend per attempt and per verified completion.
- Cluster the uncertainty by task. Bootstrap over tasks, not over requests, because requests within a task are not independent.
- Report per-task effects before the aggregate.
- Derive the sample size from the pre-registered margin and the pilot per-task variance. Do not infer power from a small number of successes.

## 7. Provenance per attempt

Record with each attempt: binary revision, sidecar revision, fixture digest, model id and settings, treatment environment, and a memory snapshot digest.

## 8. Feeds the C1 gate

The same run measures the two inputs the expected-value gate needs (`internal/splice/substitution_gate.go`):

- `p`: the predictor match rate by need class.
- `H`: the delivered overhead, meaning added prompt tokens plus the validation read.

Install them with `SetSubstitutionEVInputs` only after measurement. With no inputs the gate is inactive and selects nothing.

## 9. What would falsify the claim

- Warm cost per verified completion is greater than or equal to cold.
- Warm correctness falls below the margin.
- The apparent saving disappears in the payload channel split, so the round count did not carry it.
