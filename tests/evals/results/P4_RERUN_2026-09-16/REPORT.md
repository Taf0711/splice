# P4 re-run after the base_ref repair

**Outcome and verdict: `LIVE_SETUP_BLOCKED`.** The `base_ref` repair is
offline-verified and was live-observed to produce the path form, but the
three-condition diagnostic could not be completed: the $0.25 spend ceiling was
consumed by an unintended expensive model (a provider-config override) plus two
killed slow/stalled glm attempts, leaving no budget for the campaign. No token
benefit or mechanism claim is made.

Date: 2026-09-16. Role: test and eval agent. Harness/eval code only; no product
behavior changed by this session. Worktree
`/Users/tafseerhaque/Documents/splice-archeval`, branch
`wip/evidence-substitution-production`.

## 1. Identity

| item | value |
| --- | --- |
| product revision | `b5cc78040cd16de1e8de99ba6b1bedffbb8256bd` (`b5cc780`, the base_ref fix) |
| harness change | `d3ce20d` "test(evals): report billed dollars per campaign attempt" |
| sidecar revision | `b5cc780` (nested `memd/`, same tree; binary unchanged from the prior build) |
| product+harness binary | `/tmp/p4r-splice`, SHA-256 `5ecb42497a4c39f1ee8a4ae9d271d151374f2c57fd085ccb796c3889c3d8870e` |
| sidecar binary | `/tmp/p4r-memd`, SHA-256 `152c2c3e8b127a396444470d7be38195a32a7d94e7ed38ca1fe9a86ef49ce6bf` |
| build state | clean (0 dirty paths) at build time |
| model (frozen) | `z-ai/glm-5.3-flash` |
| disk before run | `/System/Volumes/Data` 63% used, 158 GiB free |

## 2. Repair verification (offline)

`b5cc780` teaches `base_ref` as the delivered view path and accepts it in
`ProposalBaseRegistry.Resolve` alongside the host handle. Guard tests pass:

```
go test ./internal/splice/stages -run 'TestResolve|TestBaseRef' -count=1
  --- PASS: TestResolveAcceptsTheDeliveredViewPath
  --- PASS: TestResolveStillRejectsInventedHandles
  --- PASS: TestBaseRefWordingNamesThePath
```

## 3. Harness fix (billed dollars per row)

Campaign rows carried tokens but not dollars. Added: `eval.RunOutput.BilledUSD/
BilledUSDEstimated/BilledUSDSource`, `familyPairRow.billed_usd/
billed_usd_estimated/billed_usd_source`, a `parsePipelineResultSpend` reader over
the authoritative ledger, and a per-family spend summary. Unknown stays nil, and
a partial-coverage total is marked estimated. Tests:

```
go test ./internal/cli -run 'TestParsePipelineResultSpend|TestSpendSum' -count=1   # PASS
```

Gate on the committed content: `gofmt -l .` empty, `go vet ./...` clean,
`go test ./...` exit 0 (100 packages, 0 FAIL), `cd memd && go test ./...` exit 0.

## 4. Stage 0 write smoke (the gate that failed last time)

Target: clock-helper WRITE, memory off, one attempt, `z-ai/glm-5.3-flash`.
Fixture re-validated first: `validate_mvp.py` -> all families
`[base FAIL, goldA PASS, baseB FAIL, goldB PASS, wrongB FAIL]`.

Four smoke launches, in order:

1. Auth failure. `XDG_CONFIG_HOME` pointed at an empty temp config, so the child
   selected provider `openai` with no key. Zero tokens, $0. Infrastructure only.
2. **Wrong model.** With `XDG_CONFIG_HOME=$HOME/.config`, the operator's
   `stage-models.json` mapped `code_writer` to `gpt-5.6-sol` (chatgpt profile),
   overriding `--model z-ai/glm-5.3-flash`. Result: 6 requests over 2 iterations,
   4 `format_retry` requests, verifier not run, `TypedOutputError ... proposal
   modify clock_test.go: mixed representation (content with base_ref/edits)`.
   Cost **$0.1648064** (66% of the cap). Raw stream and ledger committed as
   `smoke2-wrongmodel-raw.jsonl` / `smoke2-wrongmodel-attempt.json`.
   *Repair evidence:* all six `submit_code` calls cited
   `base_ref="clock_test.go"` — the delivered path form — and there were **no
   `unknown base_ref` errors**. The old failure mode (a model that cannot form a
   handle) did not recur; the failure was a different, model-specific compact/1
   violation (content alongside base_ref/edits) correctly rejected by the
   decoder.
3. glm smoke, correct config: stalled ~8 minutes on a provider stream (near-zero
   CPU), killed. No artifact (the runner writes the raw stream only on child
   exit). Cost by OpenRouter key delta: **$0.0091616**.
4. glm smoke, correct config: killed at ~110 s when the key usage jumped.
   Cost: **$0.071211**. No artifact.

A direct OpenRouter glm call returned in 1.4 s, so the provider was reachable;
the stalls were request-level.

Because the smoke gate requires zero format retries with a passing verifier
**on `z-ai/glm-5.3-flash`**, and no clean glm run completed, the write gate is
**not verified live**. Per the assignment ("If the model still cannot produce a
resolvable base_ref, STOP"), I stopped rather than spend the P4 campaign.

## 5. Spend against the cap

| item | USD |
| --- | ---: |
| wrong-model smoke (ledger `total_cost_usd`) | 0.164806 |
| killed slow glm smoke (key usage delta) | 0.009162 |
| killed glm smoke (key usage delta) | 0.071211 |
| **total** | **0.245179** |

Cap $0.25; **$0.004821 left**. OpenRouter key cumulative usage after the runs:
`10.687668549`. All failures are included.

## 6. Why the three-condition comparison did not run

The task's stage 0 must pass before the A seed and B attempts. It did not pass on
the required model. The spend ceiling is effectively exhausted, so the prepared
experiment (1 A seed + 9 B attempts) is not affordable. No cold/automatic/manual
comparison, no paired causal example, and no token-benefit assessment exist.

## 7. Unverified / not done

- Clean glm write smoke (0 format retries + verifier pass). The path form was
  observed accepted once, but only on the wrong model (`gpt-5.6-sol`), whose run
  failed for a different reason.
- The three-condition diagnostic (cold / automatic / manual): not run.
- Manual-selection capture gap: not re-checked (no A run completed).
- Whether improved cold resolves the TTL dependency cheaply: not measured.
- Paired causal example (cold discovery vs warm delivery): not established.
- The killed glm runs left no raw stream, so their model and request paths could
  not be confirmed; their cost is known only from the provider key.

## 8. Verdict

`LIVE_SETUP_BLOCKED` — a specific setup constraint (an unintended expensive
`stage-models.json` model plus provider stalls) consumed almost the entire $0.25
ceiling before a clean stage-0 smoke, so the prepared P4 diagnostic could not be
run. The repair itself is offline-verified and was observed producing the path
form live.
