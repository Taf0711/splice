# ADDENDUM 4 - fam-05 mechanism observation, model switch (option 1): the new model completes, but the automatic arm delivers zero cognition

**Outcome and verdict: `MECHANISM_OBSERVED` for the derivation/anchor link
(M1 + M2, re-confirmed on the new model); `NOT_OBSERVED` for delivery-removal
(M3 + M4). The run STOPPED on the pre-registered failure-table row
"automatic delivers zero cognition despite a matched matrix".**

`deepseek/deepseek-v4.1-flash` (pinned `low`) completed every arm with no
reasoning loop - the glm non-completion mode did **not** reproduce, so the
"two model families loop" escalation did not trigger. But the automatic arm's
trace shows `memory.items = 0` and `memory admission: code_writer supplied 0`
even though the pre-B match matrix was non-empty. The model never saw the
matched `mapStoreError` record and re-read `main.go`; the read was not removed.

Two harness defects found: the automatic delivery path supplies zero cognition
(the STOP row), and the Task B verifier pinned a function name the task never
states (all three arms were `verifier_rejected` for that reason). The verifier
defect is fixed offline with a regression arm; the delivery defect is product
behaviour and is reported, not changed.

Date: 2026-09-18. Role: test and eval agent. Harness/eval code only; no product
behaviour changed. Worktree `/Users/tafseerhaque/Documents/splice-archeval`,
branch `wip/evidence-substitution-production`.

## 1. Identity and model change

| item | value |
| --- | --- |
| product fix under test | `cee57c7` (unchanged) |
| harness revision | `ad72d8b` + addendum-3/4 working-tree changes (`splice_dirty=true`) |
| binary | `/tmp/p4-fam05-ds`, SHA-256 `f5ed48bea5988a677962ec002065652a4efb8985eb566d4595693db930d1ac33` |
| sidecar | `/tmp/p4b-memd`, SHA-256 `152c2c3e8b127a396444470d7be38195a32a7d94e7ed38ca1fe9a86ef49ce6bf` |
| model | `deepseek/deepseek-v4.1-flash` on the openrouter profile |
| reasoning effort | **pinned `low`** (advertised) |
| provider route served | **OpenRouter** (slug routes to Baseten via OpenRouter); no reasoning-content error, so the Baseten-direct fallback was not needed |
| run id | `mvp-matched-1789746241309092000` |
| arm order | `[cold warm manual]` (scheduling seed `20260913`) |
| per-attempt timeout | **10m** (`--attempt-timeout 10m`, new flag) |
| spend cap | $0.05 |

## 2. Preflight chain (before any provider request)

Advertised efforts for the slug: `reasoning.supported_efforts = ["max","high","low"]`,
`default_effort = high`. `low` is a small advertised effort, so per the
addendum the run **pin(s) low and assert(s) it** (never unpinned, never a
silent substitution).

```
p4preflight: stage=code_writer resolved_model=deepseek/deepseek-v4.1-flash source=stage-models.json:code_writer ... want=deepseek/deepseek-v4.1-flash
p4preflight: stage=code_writer reasoning_effort=low source=stage-models.json:code_writer supported_efforts=[max high low] model=deepseek/deepseek-v4.1-flash
```

Model assertion and reasoning-effort assertion both pass. Full text:
`provider-route-preflight.txt`. (`BASETEN_API_KEY` is unset in this
environment, so a Baseten-direct fallback could not have authenticated; it was
not required.)

## 3. Offline prep

The fam-05 campaign pair, its verifiers, and the gold/wrong overlays validate
offline via the pair-format matrix:

```
fam-05-handler-error-mapping: [FPFPF] OK (base/goldA/baseB/goldB/wrongB, altB=P)
all pairs validate [base FAIL, goldA PASS, baseB FAIL, goldB PASS, wrongB FAIL]
```

The `altB=P` row is the regression arm for the verifier defect in Section 6
(a differently named handler must pass). See `offline-validation.txt`.

## 4. A seed and the match matrix (M1/M2 re-confirmed)

Fresh Task A on the new model; verifier passed; fresh natural capture.

| item | value |
| --- | --- |
| A outcome | success, verifier pass |
| A commit / tree | `ba125c8db48926f368bec9d0ea23ac2ce1688d14` / `3dd698085b04a17ad249dc3a4202e79ba8676b90` |
| A bundle capture origin | `natural`, 3 nodes |
| A bundle capture digest | `3638a087a08bbb8bac97ff3144fbe1d484c666c1a84b004ffcffd5ef42c520e2 (corrected in addendum 5; the original addendum-4 record mis-copied the glm digest)` |
| A tokens / billed | 28,978 / $0.003659776 (ledger) |

Match matrix (`match-matrix.json`): 2 non-open needs derived -
`main.go#main` and `main.go#mapStoreError` (both `locate-named-operation`,
`cold-symbol-index`, required); 1 open-discovery need. One `main.go` fact
(`b8865a05...`, supporting refs including `main.go#mapStoreError`) **matched**;
`matched_records = 1`, so the gate passed.

- **M1 - YES (re-confirmed).** B derives `main.go#mapStoreError`,
  `NeedLocateNamedOperation`, required.
- **M2 - YES (re-confirmed).** A's natural capture anchors it.

Contract caveat unchanged: the need origin is `cold-symbol-index`, so under the
current contract no token saving is claimed.

## 5. Task B arms: zero delivery (M3) and no read removal (M4)

All three arms ran to completion (no loop, no timeout):

| row | condition | executed | success | tokens | billed USD (ledger) | reads | tool calls | latency |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| A snapshot | - | yes | true | 28,978 | 0.003659776 | 5 | 24 | 215.8 s |
| B cold | cold | yes | **false** `verifier_rejected` | 23,320 | 0.002301368 | 5 | 18 | 179.3 s |
| B warm | automatic | yes | **false** `verifier_rejected` | 25,399 | 0.003461018 | 7 | 22 | 136.0 s |
| B manual | manual (diagnostic) | yes | **false** `verifier_rejected` | 18,076 | **0.000000000** | 3 | 13 | 37.8 s |

**M3 - NOT OBSERVED (the STOP row).** The automatic arm replayed the frozen
bundle (`capture_replayed=true`, `warm_setup_valid=true`,
`effective_prompt_delivery=true`) but the code_writer was supplied **zero**
memory items:

- warm trace `memory` section: `{"status": "active", "items": 0, "chars": 0}`;
- exec stream (both code_writer invocations): `memory admission: code_writer supplied 0`;
- the model itself reasoned "Now memory field absent - omit memory_disposition";
- first-request prompt: warm 4,509 tokens vs cold 4,504 (+5 tokens, i.e. no
  record prose).

The manual arm, which imports the E4-subject-matched record directly, **also**
supplied 0 (`memory admission: code_writer supplied 0`). So the E4 matcher and
the E1 admission path disagree: the matched record (a `fact` with an empty
`answered_need`) is selected by subject but not admitted/delivered. Per the
failure table, the run STOPPED here; no repeats were run.

**M4 - NOT OBSERVED.** The `main.go` read is present in the warm arm (twice),
and the automatic arm made **more** reads than cold, not fewer:

```
cold      reads: main.go, main.go, session.go, session.go, session_test.go, session_test.go
automatic reads: main.go, main.go, session.go, session.go, session_test.go, session_test.go, main.go, main.go, session.go, session.go, session_test.go, session_test.go, go.mod, go.mod
manual    reads: main.go, main.go, session.go, session.go, main.go, main.go
```

The automatic arm read `go.mod` in addition to everything cold read.
`reads-per-arm.txt` has the full lists.

## 6. Harness defect fixed offline: named-handler over-specification

The Task B text says only "use it from any new admin handlers you add"; it
never names the handler. The verifier (inherited from the cognition corpus'
gold) required `adminSessionHandler`. Every model arm produced the right
deliverable under its own name - cold `adminHandler`, warm `adminHandler`,
manual `adminSessionsHandler` - each routed through `mapStoreError`, and each
failed the probe with `build failed` because the probe referenced the unnamed
`adminSessionHandler`.

Fix (harness only): `verifiers/fam-05-b.sh` is now name-agnostic - it requires
that some function other than the pre-existing `sessionHandler` returns
`http.HandlerFunc` and references `mapStoreError`, and still forbids any
hand-coded store-failure status outside the table. Regression arm
`validate/_alt-b/fam-05/admin.go` (a differently named `adminHandler`) is run
by `validate_pair.py` and must pass (`altB=P`). The required
`base FAIL / goldA PASS / baseB FAIL / goldB PASS / wrongB FAIL` matrix is
unchanged.

This fixes the *correctness gate*; it does not change the M3 finding, which is
about delivery and would have failed regardless.

## 7. Spend against the cap

| item | USD (ledger) |
| --- | ---: |
| cap | 0.050000000 |
| A seed | 0.003659776 |
| B cold | 0.002301368 |
| B automatic | 0.003461018 |
| B manual | 0.000000000 (accounting gap: 18,076 tokens billed 0) |
| **total** | **0.009422162** |

Under the $0.05 cap. The OpenRouter key delta is unusable as a bound here: the
evaluation agent itself runs through the same shared key
(`MSWEA_MODEL_NAME=openrouter/deepseek/deepseek-v4.1-flash`), so key deltas
include the agent's own usage, not just the experiment. The manual row's $0 is
a separate ledger-accounting gap (tokens were spent; the priced-cost field is
0).

## 8. Failure-table status

| row | result |
| --- | --- |
| Match matrix empty after A | no - `matched_records=1` |
| One reasoning-loop non-completion on the new model | no - all arms completed (37.8-215.8 s) |
| Automatic delivers zero cognition despite a matched matrix | **YES - STOP** |
| Correctness failure on cold B | yes (`verifier_rejected`), cause: the verifier defect in Section 6, not the mapping |

## 9. Unverified / not done

- M3 is observed as **zero delivery**; the positive form ("delivery reaches the
  first request") is unverified because it did not happen.
- M4 is unverified; the warm arm read at least as much as cold.
- No repeats (the STOP row fired), no held-out confirmation.
- The delivery defect is not root-caused in product code (not permitted here);
  the observed signature is empty `answered_need` on the captured `fact` and
  `memory admission: code_writer supplied 0` on every seeding arm.
- The Baseten-direct fallback was never exercised (no credential; no
  reasoning-content error on OpenRouter).

## 10. Verdict and next step

`MECHANISM_OBSERVED` for M1 + M2 on the second model. M3 + M4 remain
`NOT_OBSERVED`, and the blocker has moved: from a glm reasoning-effort
substitution (addendum 3) to a delivery-path defect that supplies zero
cognition for a subject-matched capture. The glm reasoning-loop mode did not
reproduce on `deepseek/deepseek-v4.1-flash` at `low`, so the "two model families
loop" escalation did not trigger.

Next step (outside this pre-registration): root-cause why the E4-matched record
is admitted as 0 items - likely its empty `answered_need` makes it a hint for
the E1 admission path - then re-run the staged diagnostic with the name-agnostic
verifier and the 10m bound. Alternatively, if the current contract's
index-resolved/`answered_need` requirement cannot answer this pair, close with
the structural verdict as option 3 anticipated.

## 11. Evidence index (this directory)

- `binary-sha256.txt`, `provider-route-preflight.txt`
- `offline-validation.txt`, `registry-pairs.json`
- `match-matrix.json`
- `attempt-rows.jsonl`, `spend-ledger.txt`
- `warm-delivery-evidence.txt` (trace memory section, admission lines, prompt sizes)
- `reads-per-arm.txt`
- Raw run artifacts: `../p4-fam05-ds-stage1/` (A patch/verifier, snapshot
  bundle, warm trace, all exec streams, patches)
