# P4 re-run attempt 3: stage-0 gate passes; the TTL pair does not exercise the mechanism

**Outcome and verdict: `INCONCLUSIVE`.** The `base_ref`/hedge repair is verified
live: the stage-0 WRITE smoke completed with one request, zero format retries,
a path-cited `base_ref`, and a passing verifier. The staged diagnostic then ran
the A seed plus one improved-cold and one automatic B attempt, but the intended
mechanism never engaged: A's frozen capture produced no record that matched B's
needs, the automatic arm delivered zero memory and issued no prefetch, and cold
already resolved the dependency with the same two requests. The pair measures
protocol behavior, not memory-assisted prefetch, so no mechanism or token claim
is made.

Date: 2026-09-16/17. Role: test and eval agent. Harness/eval code only; no
product behavior changed by this session. Worktree
`/Users/tafseerhaque/Documents/splice-archeval`, branch
`wip/evidence-substitution-production`.

## 1. Identity

| item | value |
| --- | --- |
| product fix | `cee57c7` keep raw-anchored edits in the hedged modify; never diff an empty content |
| harness additions | `d93149a` resolved-stage-model assertion + artifacts-on-kill |
| revision used | `cee57c7942e76f7638189e08e3df6ffb21e9f244` |
| binary | `/tmp/p4b-splice`, SHA-256 `c4cce988d52f3431adec87b33eb4343798969350ef4ef879a7f0e5cab7c6e0d3` |
| sidecar | `/tmp/p4b-memd`, SHA-256 `152c2c3e8b127a396444470d7be38195a32a7d94e7ed38ca1fe9a86ef49ce6bf` |
| build state | clean (0 dirty paths) |
| model | `z-ai/glm-5.3-flash` (resolved and asserted) |
| reasoning effort | **medium** (eval config; the operator profile pinned high) |
| per-attempt timeout | 10m |

## 2. Config change (mandatory) and its effect

The eval config (`/tmp/p4r-cfg/splice/`) now pins `reasoningEffort: "medium"`
in both `config.json` (openrouter profile) and `stage-models.json`
(`default` / `code_writer` / `test_generator`). Recorded in
`eval-config-reasoning.txt`.

Effect, same task and model: the previous attempt at high effort burned the full
10-minute bound (5,899 reasoning events, no submission, unknown cost). At medium
the same smoke completed in **26 seconds** with **one** provider request and
**zero** format retries. This pins the earlier timeout as a reasoning-effort
effect, not a protocol or provider defect.

## 3. Stage 0 gate: PASS

```
XDG_CONFIG_HOME=/tmp/p4r-cfg ARMS=cold REPEATS=1 MAX_RETRIES=0 RETENTION=fresh \
MODEL=z-ai/glm-5.3-flash TASKSET_SRC=tests/evals/warmcost/retention-taskset \
TASKS_ENV='clock-helper-write' RUN_BASE=wc-p4b-smoke-20260917T161527Z \
BIN=/tmp/p4b-splice BIN_PREBUILT=1 MEMD_BIN=/tmp/p4b-memd TIMEOUT=10m \
tests/evals/warmcost/paid-run.sh
```

| check | result |
| --- | --- |
| outcome | `completed`, verifier **pass**, coverage complete |
| requests | 1 (generation only) |
| format retries | **0** |
| `base_ref` | **`"clock_test.go"`** (the delivered path form), no content, 1 edit |
| duration | 25.9 s |
| billed (ledger) | $0.00107562 |

The mandatory model preflight ran inside `paid-run.sh` and resolved
`code_writer -> z-ai/glm-5.3-flash` (the negative control against the operator
config still aborts on `gpt-5.6-sol`). The artifacts-on-kill behavior from
`d93149a` remains in place; this attempt did not need it.

## 4. Staged diagnostic on `mvp-02-ttl-source-of-truth`

Command (A seed + one cold + one automatic + the manual arm), with the
three-condition campaign, prefetch isolated from substitution:

```
SPLICE_MEMORY_PREFETCH=on SPLICE_EVIDENCE_SUBSTITUTION=off SPLICE_MVP_DEBUG=1 \
XDG_CONFIG_HOME=/tmp/p4r-cfg MODEL=z-ai/glm-5.3-flash SPLICE=/tmp/p4b-splice \
tests/evals/warmcost/p4-run.sh \
  --manifest tests/evals/mvp-families/p4-ttl-pair.json \
  --taskset tests/evals/mvp-families --out tests/evals/results/p4b-stage1 \
  --model z-ai/glm-5.3-flash --rollouts 1 --matched-snapshots \
  --conditions three-condition --scheduling-seed 20260917
```

Scheduling seed `20260917`, arm order `[warm manual cold]`.

| row | condition | executed | success | tokens | requests (ledger) | billed USD |
| --- | --- | --- | --- | ---: | --- | ---: |
| A snapshot | precursor | yes | true | 43,959 | generation (+ verify) | 0.00345895 |
| B warm | automatic | yes | true | 12,450 | 1 generation + 1 format_retry | 0.00212125 |
| B cold | cold | yes | true | 12,226 | 1 generation + 1 format_retry | 0.00063315 |
| B manual | manual (diagnostic) | **no** | – | 0 | – | unknown |

Warm-minus-cold = **+224 tokens** (automatic slightly more expensive).

## 5. Request-path inspection (stage 3)

- **Automatic delivered no cognition.** The warm trace records
  `memory_items=0`, `context_items=6`, and `memory = {status: active, items: 0,
  chars: 0}`; the exec transcript contains **no `memory prefetch` line**. The
  frozen A capture was replayed into the isolated store, but the selector
  retrieved no record for B, so neither delivery nor prefetch fired.
- **Cold already resolves the dependency cheaply.** Cold B used 2 requests
  (1 generation + 1 format_retry), 5 file reads, no expansion requests. Warm B
  used exactly the same counts. The shared test-source resolver resolves B's
  lifetime-source dependency directly; there is no discovery round for
  memory to remove.
- **Manual selection: capture gap.** The campaign failed loud with
  `seed manual arm: no bundle record matched any target need` and recorded the
  manual row as `manual_seed_unavailable` / `setup_failed` - the legitimate
  section-8.4 capture-gap outcome, reproduced under the fixed contract.
- Both B arms carried **one format retry each** (cold's first submit omitted
  `base_ref`; warm's first had an ambiguous edit). These are generic protocol
  costs shared by both arms, not memory effects; the stage-0 smoke itself had
  zero.

Conclusion for this pair: it now measures protocol behavior, not the prefetch
mechanism. Per the handoff failure table ("cold already obtains the same
dependency cheaply -> keep the shared-resolver improvement and use a genuinely
unresolved related task"), three repeats were not run on an invalid path.

## 6. Spend

| item | USD (ledger) |
| --- | ---: |
| stage-0 smoke | 0.00107562 |
| A snapshot | 0.00345895 |
| B automatic | 0.00212125 |
| B cold | 0.00063315 |
| **total** | **0.00728897** |

Against the proposed $0.30 envelope. The shared OpenRouter key delta is
contaminated (it rose without local processes in the prior attempt), so the
ledger is the authoritative per-attempt figure; the manual row spent $0. No
attempt was killed this run, so no artifact-on-kill path was exercised.

## 7. Unverified / not done

- No mechanism or token-benefit result: the prefetch/delivery never engaged, so
  there is nothing to measure on this pair.
- Three repeats per condition were not run (the staged path was invalid).
- No held-out confirmation.
- The automatic no-delivery outcome is a selection/capture gap for this pair;
  it does not prove the mechanism cannot work on a pair with a genuine
  unresolved dependency.

## 8. Verdict and next step

`INCONCLUSIVE` - the stage-0 gate (and therefore the repair) is verified live,
but the specified pair cannot exercise the mechanism: A's capture carries no
record matching B's needs, and improved cold resolves B's dependency with no
expansion to remove.

Concrete next hypothesis: select a related pair from the committed larger-eval
leads (delete-idempotence, toolchain-pinning) whose cold trace still shows an
expansion round for a dependency B does not name, and where A's captured record
is subject-matched to B's need, then re-run the staged three-condition
diagnostic on that pair.
