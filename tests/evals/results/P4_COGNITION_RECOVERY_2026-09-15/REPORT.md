# P4 cognition recovery live diagnostic

**Outcome and verdict: `LIVE_SETUP_BLOCKED`.** The prepared three-condition
diagnostic was run in stages and stopped at the P1 smoke/precursor gate. A
concrete, reproducible protocol defect blocks it: the compact/1 modify contract
requires a model-supplied `base_ref`, but the handle is a digest of the delivered
view text and is never made model-visible, so a live model cannot submit a valid
modify. The clock-helper READ (create-only) is clean; every modify task I ran
either burned format retries or hard-failed. No cognition or token conclusion is
drawn.

Date: 2026-09-15. Role: test and eval agent. Harness and test code only; no
product behavior modified. Worktree
`/Users/tafseerhaque/Documents/splice-archeval`, branch
`wip/evidence-substitution-production`, product/harness/sidecar revision
`27a1eedb09407ab5517f515554bb0dae6f002ff1`.

## 1. Experiment identity

| item | value |
| --- | --- |
| product + harness revision | `27a1eedb09407ab5517f515554bb0dae6f002ff1` |
| sidecar revision | `27a1eed` (nested `memd/`, same tree) |
| product binary | `/tmp/p4-splice`, SHA-256 `387c11ecc8dcaec810cb952b7b12c4dfe21322ecad3e23b4c902f60380cafe07` |
| sidecar binary | `/tmp/p4-memd`, SHA-256 `152c2c3e8b127a396444470d7be38195a32a7d94e7ed38ca1fe9a86ef49ce6bf` |
| model | `z-ai/glm-5.3-flash` (kept for comparability) |
| provider | openrouter (`OPENROUTER_API_KEY` from the operator env; not logged) |
| build state | clean at preflight; the one-family manifest below was added untracked, so the campaign manifests record `vcs_dirty=true` |
| treatment env (parent, inherited by children) | `SPLICE_MEMORY_PREFETCH=on`, `SPLICE_EVIDENCE_SUBSTITUTION=off` |
| treatment per condition (resolved by the campaign) | cold: treatment `cold`, `--memory off`; automatic: treatment `full`, `--memory on`; manual: treatment `full`, `--memory on`, diagnostic-only |
| scheduling seed | `20260915`; derived arm order `[warm cold manual]` |
| attempt ceiling (default) | 1 A seed + 9 B attempts (3 conditions x 3) |
| spend cap | $0.25 |

`SPLICE_MEMORY_PREFETCH=on` is inherited by every arm (the treatment env builder
filters only `SPLICE_TREATMENT`/`SPLICE_SCOPE_MODE`/`SPLICE_EXEMPLAR_MODE`).
Cold has memory off, so prefetch is inert there; automatic and manual run it.
`SPLICE_EVIDENCE_SUBSTITUTION=off` isolates the prefetch/delivery path from the
substitution path, as required.

## 2. Selected task pair and the cold-trace evidence

Pair: **A = `mvp-02-ttl-source-of-truth` precursor, B = its target**, from
`tests/evals/mvp-families/cognition-mvp-families.json` (one-family manifest
`tests/evals/mvp-families/p4-ttl-pair.json`).

- A: introduce `DefaultTTL` in `internal/session` as the single lifetime source
  and make the construction site read it.
- B: add `Sweep()` that deletes expired sessions and "must read the same single
  configured lifetime source the rest of the repository uses, not its own
  duration literal". B's prompt does NOT name `DefaultTTL`, `store.go`, or the
  construction site, so cold must discover the lifetime source.

Justification and its limits:

- The committed 20-row larger evaluation records `fam-02-ttl-config-source` as a
  both-success warm reduction lead (cold 24,468 vs warm 17,842 raw tokens);
  `LARGER_EVAL_2026-09-11.md` explicitly labels these as leads, not a
  confirmation set.
- The committed `mvp-final4-attempts.jsonl` shows the TTL Task B cold rows at 11
  tool calls / 5 file reads (summary counters only). I could NOT find a committed
  full raw stream for a TTL cold attempt; the only committed full raw stream in
  `tests/evals/results/` is the clock-helper retention read, which is the
  regression, not the efficiency pair. This pair is therefore selected on
  structural evidence (B does not name the dependency) plus summary counters, not
  a committed dependency-discovery stream. Stated as a limitation.

Corpus validation (offline, before spend): `tests/evals/mvp-families/validate/validate_mvp.py`
printed `all families validate [base FAIL, goldA PASS, baseB FAIL, goldB PASS,
wrongB FAIL]` for mvp-01/02/03. The verifiers are real Go tests (`go test
-count=1 ./internal/session/`) with concrete assertions, so a no-op or renamed
test cannot pass by executing zero relevant tests.

## 3. Stage 0: P1 live smoke (clock-helper regression pair, cold, memory off)

Command (`tests/evals/warmcost/paid-run.sh`, ARMS=cold, REPEATS=1, MAX_RETRIES=0):

```
ARMS=cold REPEATS=1 MAX_RETRIES=0 RETENTION=fresh MODEL=z-ai/glm-5.3-flash \
TASKSET_SRC=tests/evals/warmcost/retention-taskset \
TASKS_ENV="clock-helper-write clock-helper-read" \
RUN_BASE=wc-p4-smoke-20260915T191548Z \
BIN=/tmp/p4-splice BIN_PREBUILT=1 MEMD_BIN=/tmp/p4-memd \
tests/evals/warmcost/paid-run.sh
```

Result (run `wc-p4-smoke-20260915T191548Z-try0`; raw streams committed):

| attempt | status | verifier | requests | tokens (in+out) | format retries | billed USD |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| clock-helper-read | completed | pass | 2 (1 generation, 1 expansion) | 10,768 | **0** | 0.001739 |
| clock-helper-write | completed | pass | 4 (1 gen, 1 expansion, **2 format_retry**) | 19,041 | **2** | 0.002369 |

- The READ matches the required P1 behavior exactly: one
  `request_codebase_context` (query `read_file clock_test.go`) is fulfilled by
  the host, the following model request is a single clean `submit_code` with a
  `create` entry, the file applies, the verifier runs and passes, and there are
  zero format retries.
- The WRITE does not. It issued `submit_code` (guessed
  `base_ref="clock_test.go@read_file:49-lines"`), was rejected (`unknown
  base_ref`), requested context, submitted again (`base_ref="clock_test.go@view1"`,
  also rejected), then submitted a `create` with full contents, which passed. The
  two rejections are the ledger's two `format_retry` requests (48.5% of the
  write's 19,041 tokens), reproducing the "format failures dominate" failure.

Interpretation: the action-contract repair works for create-only submissions
(the READ) but is NOT reliable for modify submissions (the WRITE). This is the
early signal that became a hard blocker in the campaign.

## 4. Campaign stage 1 and the debug rerun

Command template (one family, three conditions):

```
SPLICE_MEMD_BIN=/tmp/p4-memd SPLICE_MEMD_SOCKET=/tmp/p4-memd-dir/mem.sock \
SPLICE_MEMD_DB=/tmp/p4-memd-dir/mem.db \
SPLICE_MEMORY_PREFETCH=on SPLICE_EVIDENCE_SUBSTITUTION=off \
SPLICE_MVP_DEBUG=1 \
/tmp/p4-splice eval mvp \
  --manifest tests/evals/mvp-families/p4-ttl-pair.json \
  --taskset  tests/evals/mvp-families \
  --out      tests/evals/results/<run> \
  --model    z-ai/glm-5.3-flash \
  --rollouts 1 --matched-snapshots --conditions three-condition \
  --scheduling-seed 20260915
```

### 4.1 Run A (`p4-ttl-stage1`) — A verified, three B conditions ran

| row | task/condition | executed | success | tokens (in+out) | tool calls | reads |
| --- | --- | --- | --- | ---: | ---: | ---: |
| snapshot A | precursor | yes | true | 6,190 | 17 | 5 |
| B manual | manual (diagnostic) | **no** | – | 0 | – | – |
| B warm | automatic | yes | true | 25,195 | 27 | 10 |
| B cold | cold | yes | true | 37,433 | 29 | 11 |

- Natural capture worked: `snapshot-bundle.json` `capture_origin=natural`, 3
  nodes (1 procedure, 2 file facts). Automatic B recorded
  `capture_replayed=true`, `seed_status=replayed`.
- **Manual selection has a capture gap.** The campaign failed loud:
  `seed manual arm: no bundle record matched any target need`. The manual
  condition is recorded as `manual_seed_unavailable` / `setup_failed`. Per
  handoff 8.4 this is a capture gap, not a mechanism result.
- Automatic B used 12,238 fewer raw tokens than cold B (-32.7%), both success.
  Single attempt, so this is not a claim. The warm trace
  (`mem.db` `run_traces`) shows the two code_writer requests carried
  `memory_items=0`; the automatic arm's context came from `context_items=6`
  (prefetch queries), not delivered cognition prose.

### 4.2 Run B (`p4-ttl-debug1`) — precursor hard-failed, diagnostic aborted

Task A failed with `agent_noncompletion`, `verifier_ran=false`, after 3 typed
output attempts in each of 2 iterations (total_cost_usd 0.003363). The final
error is exact:

```
*stages.TypedOutputError: model "z-ai/glm-5.3-flash" failed typed output after
3 attempts: required tool "submit_code+request_codebase_context" with valid JSON
arguments: normalize compact proposals: proposals[0]: proposal modify
internal/session/store.go: base_ref is required
```

The model's own streamed reasoning: *"unknown base_ref; views gave read_file
content but no handle. Best: submit with create for both files."*

All three B rows were skipped (`precursor_failed`). The precursor is
unreliable: 1/2 of my invocations verified it.

## 5. The blocking defect (code-referenced)

`base_ref` for `modify` is a **handle derived from the delivered view text**:

- `stages/helpers.go:376` tells the model `base_ref` is "the source handle of
  the base content you received in context views. The host resolves it to exact
  bytes; you never compute hashes."
- `stages/proposal_bases.go:HandleFor` = first 12 chars of
  `contentDigest(delivered view text)`; `RecordFromBundle` registers it as
  `byHandle[handle]`.
- `context.go:fulfillReadFile` builds the `ContextItem.Payload` with `text`,
  `path`, `version` (the file's SHA-256), `start`, `end`, and a host-only `raw`.
  The `version` is NOT the `base_ref` handle.
- The prompt composer serializes only `text` (the payload comment says the
  registry-only fields are "never model-visible"), and `schemas.ContextItem` has
  no handle field.

So the model receives the file body but never the 12-char handle that
`base_ref` must equal; it cannot compute it (hashing is forbidden and the digest
is over the delivered display text) and guessing fails loud. Every `modify`
submission therefore either burns retries or hard-fails. Create-only tasks (the
clock-helper READ) sidestep it.

This is a live product-contract defect, not an eval-harness defect. Under this
session's role ("Do not modify product behavior; harness and test code only") it
is reported, not repaired.

## 6. Paired causal trace (where the link failed)

The required paired example (cold's discovery step vs what warm delivered) could
not be established. The link failed **before** the comparison: both cold and warm
Task B must submit a `modify` to `store.go`; the model cannot obtain a valid
`base_ref`, so the submission fails typed output. The observable cold/warm
request paths were therefore not a valid mechanism comparison, and the one
favorable single-attempt delta (25,195 vs 37,433) is not attributable to the
prefetch mechanism. The automatic prefetch did add context queries
(`context_items=6`) and replayed the natural capture, but its effect is not
measurable while the modify contract is broken.

## 7. All-attempt results and spend

| run | attempt | requests/tokens | success | format retries | billed USD |
| --- | --- | --- | --- | ---: | ---: |
| smoke | clock-helper-write | 19,041 tok | pass | 2 | 0.002369 |
| smoke | clock-helper-read | 10,768 tok | pass | 0 | 0.001739 |
| stage1 | A snapshot | 6,190 tok | true | n/r | ~0.0009 (est) |
| stage1 | B automatic | 25,195 tok | true | n/r | 0.003704 (traced) |
| stage1 | B cold | 37,433 tok | true | n/r | ~0.0055 (est) |
| stage1 | B manual | 0 | setup_failed (capture gap) | – | 0 |
| debug1 | A snapshot | 37,455 tok | **false** | 3/iter | 0.003363 |
| debug1 | B cold/automatic/manual | 0 | skipped (precursor_failed) | – | 0 |

**Total billed USD ≈ $0.018** against the $0.25 cap (exact where the producer
reported cost; the two stage-1 B estimates are token-derived and labelled).
Failures and the A seed are included. The campaign's per-attempt rows do not
carry billed dollars (only tokens), so cold/manual dollar figures are estimates;
this is a harness reporting gap.

## 8. Failure-table application and next hypothesis

Handoff section 10 row **"Format failures dominate -> Repair protocol
reliability. Preserve the failed attempt's spend."** applies. The dominant spend
is `format_retry` from an unusable `base_ref` contract, and the precursor
hard-fails on modify tasks. This is not a cognition result.

One concrete next hypothesis (for the implementing agent, not this eval role):
make the `base_ref` handle model-visible by including it with the delivered
context view (e.g., serialize the handle alongside `path`/`version` in the
model-visible context item, or accept the delivered `version` as an alias in
`ProposalBaseRegistry.Resolve`). Then re-run the clock-helper WRITE smoke and
require zero format retries before spending on P4 again.

## 9. Unverified / not done

- No valid three-condition token comparison. Manual selection has a capture gap
  (no A record matched B's target needs); automatic-vs-cold was measured once but
  is invalidated by the modify-contract defect.
- Mechanism proof (handoff 9.3) is NOT established: cold's specific discovery
  request vs warm's replacement was not observed.
- The one-family manifest is untracked (`vcs_dirty=true` in the campaign
  manifests); a clean-state run was not completed.
- The TTL pair's cold-trace evidence is summary-level, not a committed raw stream.
- No held-out confirmation (section 8.6) was attempted.

## 10. Verdict

`LIVE_SETUP_BLOCKED` — the prepared live diagnostic is blocked by the
`base_ref` modify-contract defect. The P1 read path is confirmed clean; the P1
modify path is not reliable enough to measure the prefetch mechanism.
