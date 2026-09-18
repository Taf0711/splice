# ADDENDUM 5 - fam-05 re-run after the delivery repair: frozen A reused, delivery still zero

**Outcome and verdict: `MECHANISM_OBSERVED` for M1 + M2 (matrix gate re-passed on
the reused frozen A); `NOT_OBSERVED` for M3 and M4. The automatic arm delivered
zero cognition again despite a non-empty matrix - the pre-registered STOP row -
and the live run does not emit the discovery plan, so the break is now
downstream of seeding and needs the discovery-plan telemetry the addendum
names.**

The product repair (`c938218`) was exercised on the reused frozen A with a
pinned `low` effort. The automatic and manual arms both replayed/imported the
capture, the sidecar held the `main.go` fact with its `mapStoreError` anchor,
and admission still supplied 0. The automatic arm then failed on context-budget
exhaustion (not the glm reasoning loop). Cold B and manual B passed the
name-agnostic verifier; the manual-arm cost attribution is fixed (estimated,
never $0).

Date: 2026-09-18. Role: test and eval agent. Harness/eval code only; the only
product change exercised is `c938218`, which the operator committed. Worktree
`/Users/tafseerhaque/Documents/splice-archeval`, branch
`wip/evidence-substitution-production`.

## 1. Identity, reuse, and route

| item | value |
| --- | --- |
| product revision served | `c938218` key bare source filenames so named-file captures can deliver |
| harness revision | `cedf9f2` + this session's working-tree changes (`splice_dirty=true`) |
| binary | `/tmp/p4-fam05-ds2`, SHA-256 `86c6663bea197dfb4d981456408286ce07517d729da6a69c5453478f62f45260` |
| sidecar | `/tmp/p4b-memd`, SHA-256 `152c2c3e8b127a396444470d7be38195a32a7d94e7ed38ca1fe9a86ef49ce6bf` |
| model / route | `deepseek/deepseek-v4.1-flash` via OpenRouter (Baseten upstream) |
| reasoning effort | **pinned `low`** (advertised `max/high/low`), asserted |
| run id | `mvp-matched-1789754224675464000` |
| arm order | `[cold warm manual]` (scheduling seed `20260913`) |
| per-attempt timeout | 10m |
| cap / spent before | $0.05 total; $0.009422162 spent across addendum 4 |

**Frozen A reuse (no producer model call).** `--reuse-snapshot-dir` +
`--reuse-snapshot-bundle` rematerialized the frozen tree from the pristine
fixture plus the supplied A tree and asserted the rebuilt identity against the
recorded bundle before anything downstream ran:

```
REUSED frozen Task A (bundle .../snapshot-bundle.json
  commit 817d95067a22752cb2ba997a9101261647ca19c4
  tree   28a49a80b85bba590df8c3908ccce1a52cd11db5
  capture_digest 3638a087a08bbb8bac97ff3144fbe1d484c666c1a84b004ffcffd5ef42c520e2
  origin natural)
```

`reuse-record.json` records `reused=true`, `producer_model_rerun=false`, and
`changed_files=[main.go, session.go]`.

Correction to the addendum-4 record: the deepseek A bundle digest is
`3638a08...` (the addendum-4 report mistakenly carried the glm digest
`096685d5...`). The capture node set is identical across glm and deepseek
(same three claim hashes); the bundle digests differ because the exported node
payloads differ in non-claim fields, and the post-verify commits differ
(`ba125c8`/`3dd698` glm vs `817d950`/`28a49a8` deepseek). The addendum-4 report
is corrected alongside this one.

## 2. Preflight chain (before any provider request)

```
p4preflight: stage=code_writer resolved_model=deepseek/deepseek-v4.1-flash source=stage-models.json:code_writer ... want=deepseek/deepseek-v4.1-flash
p4preflight: stage=code_writer reasoning_effort=low source=stage-models.json:code_writer supported_efforts=[max high low] model=deepseek/deepseek-v4.1-flash
```

Both assertions pass (`preflight.txt`).

## 3. Match-matrix gate re-run (offline, before spend)

Against the reconstructed frozen A tree and the recorded bundle:

```
derived needs:
  locate:main.go#main            locate-named-operation  cold-symbol-index  required
  locate:main.go#mapStoreError   locate-named-operation  cold-symbol-index  required
  inspect:main.go                inspect-edit-target     task-text          required
bundle ... commit=817d950... tree=28a49a8... digest=3638a08... nodes=3
  record f6aade87... matched=[]
  record b8865a05... matched=[locate:main.go#main locate:main.go#mapStoreError inspect:main.go]
  record da630237... matched=[]
matched_records=1 non_open_needs=3
```

`matched_records=1`, unchanged as expected. The repair also adds the bare
`inspect:main.go` task-text need (the same bare-filename miner), which the
`main.go` fact answers as well. Full output: `match-matrix-offline.txt`.

## 4. Task B arms

| row | condition | executed | success | tokens | billed USD | source | reads | tools | latency |
| --- | --- | --- | --- | ---: | ---: | --- | ---: | ---: | ---: |
| A snapshot | reuse | no (reused) | true (provenance) | 0 | - | - | - | - | - |
| B cold | cold | yes | **true** | 11,601 | 0.0011601 | estimated | 4 | 14 | 11.5 s |
| B warm | automatic | yes | **false** `agent_noncompletion` | 28,998 | 0.000897 | ledger | 5 | 21 | 18.6 s |
| B manual | manual (diagnostic) | yes | **true** | 11,377 | 0.0011377 | estimated | 3 | 13 | 11.5 s |

No arm looped (the glm non-completion mode did not reproduce); the automatic
arm failed fast on `repair: code_writer re-entry: context expansion budget
exhausted while fulfilling the stage context request`.

**M3 - NOT OBSERVED (STOP row).** Despite `capture_replayed=true` and the
non-empty matrix, the automatic arm received nothing:

- trace `memory`: `{"status":"active","items":0,"chars":0}`;
- `memory admission: code_writer supplied 0` (three times);
- the manual arm (which imports the natural nodes) also supplied 0;
- the sidecar **did** hold the capture: the warm project's
  `cognition_nodes` carried the `main.go` fact with anchors
  `file|main.go`, `symbol|main.go#mapStoreError`, `verified_revision=817d950`,
  and natural metadata (`verification_status=passed`, supporting digests).

So the payload was present and correctly anchored; the break is in the live
retrieval/admission path, not in the seed. The run's trace records
`iterations=null`, `stages=[]`, i.e. **no discovery-plan telemetry is
emitted**, which is the next link to instrument.

**M4 - NOT OBSERVED.** The `main.go` read is present in the automatic arm, and
the automatic arm read more than cold, not less:

```
cold      reads: main.go, main.go, session.go, session.go, session_test.go, session_test.go, README.md, README.md
automatic reads: main.go, main.go, session.go, session.go, session_test.go, session_test.go, main.go, main.go, main.go, main.go
manual    reads: main.go, main.go, session.go, session.go, session_test.go, session_test.go
```

Full lists: `reads-per-arm.txt`, `delivery-evidence.txt`.

## 5. Offline fixes made this session

1. **Frozen-A reuse** (`--reuse-snapshot-dir` / `--reuse-snapshot-bundle`):
   rematerialize the recorded tree, assert the rebuilt commit/tree equal the
   bundle's, reuse the capture verbatim, write `reuse-record.json`. Regression:
   `TestFrozenSnapshotReuseReproducesRecordedIdentity`.
2. **Manual-arm cost attribution (defect 3)**: a result with tokens but zero
   priced cost is now a token-based ESTIMATE (`source=estimated`), never a
   confirmed $0. Regression:
   `TestParsePipelineResultSpendEstimatesZeroPricedTokens`. The cold and manual
   rows above show `estimated=true`; the warm row kept its real ledger total.
3. **Name-agnostic Task B verifier** (from addendum 4) is exercised here: cold
   and manual PASS with the model's own handler name; the automatic arm's
   failure is the context-budget abort, not the verifier.

## 6. Failure-table status

| row | result |
| --- | --- |
| Automatic delivers zero cognition despite a non-empty matrix | **YES - STOP** (both automatic and manual) |
| Any arm loops without submitting | no (all arms 11.5-18.6 s) |
| Cold B correctness failure | no (cold PASS) |

## 7. Spend against the envelope

| item | USD |
| --- | ---: |
| cap (addenda 4 + 5) | 0.050000000 |
| addendum 4 | 0.009422162 |
| addendum 5 A seed | 0.000000000 (reused, no producer call) |
| addendum 5 B cold | 0.001160100 (estimated) |
| addendum 5 B automatic | 0.000897000 (ledger) |
| addendum 5 B manual | 0.001137700 (estimated) |
| **total 4 + 5** | **0.012616962** |
| **remaining** | **0.037383038** |

## 8. Unverified / not done

- M3 positive delivery is unverified; the observed value is zero on both the
  automatic and manual arms.
- M4 unverified; the automatic arm read at least as much as cold.
- The live discovery plan (derived keys, resolved/unresolved questions) is not
  emitted by the run; the sidecar evidence proves only that the payload was
  present and anchored.
- No repeats (the STOP row fired).
- Whether the natural-vs-reconstructed payload difference (verification_status
  `passed` vs `unverified`) matters is not established: the manual arm imported
  the natural payload and still supplied 0.

## 9. Verdict and next step

`MECHANISM_OBSERVED` for M1 + M2; M3 + M4 `NOT_OBSERVED`. The repair's
bare-filename key derivation is present and the offline pairing chain passes,
but the live run still admits nothing even though the capture sits in the
sidecar with the right anchors. The next link is exactly what the addendum
names: instrument the live discovery-plan telemetry (derived keys and
resolved/unresolved questions, and where `tryDirectCognition` /
`planStageDiscovery` stop), then one cheap targeted run on the reused frozen A.
If that shows the plan resolving but admission still zero, the break is in the
node-to-observation bridge; if the plan is unresolved, the keys used at runtime
differ from `cognition.DeriveKeys` over the raw intent.

## 10. Evidence index (this directory)

- `binary-sha256.txt`, `preflight.txt`
- `reuse-record.json` - frozen-A reuse identity and digest match
- `match-matrix-offline.txt` - offline gate re-run
- `attempt-rows.jsonl`, `spend-ledger.txt`
- `reads-per-arm.txt`, `delivery-evidence.txt`
- Raw run: `../p4-fam05-ds2-stage1/` (reuse record, warm trace, all exec
  streams, patches, verifier output)
