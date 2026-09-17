# ADDENDUM 3 - fam-05 mechanism observation (option 1): A derives and anchors the need; the automatic arm does not complete

**Outcome and verdict: `MECHANISM_OBSERVED` for the derivation/anchor link
(M1 + M2); `NOT_OBSERVED` for delivery-removal (M3 + M4), which the automatic
arm did not reach because the model never submitted.**

The offline wiring and the pre-registered gate both behaved as designed. Task A
verified, and the pre-B match matrix was **non-empty**: B derives
`main.go#mapStoreError` (locate-named-operation, required) and A's natural
capture carries a `main.go` fact anchored with the `mapStoreError` symbol. The
automatic arm then replayed that capture, but ran for 6.8 minutes, emitted
12,223 reasoning events and no `submit_code`, and was stopped to bound spend. A
new, verifiable defect explains why: the pinned `reasoningEffort=medium` is
**not supported by `z-ai/glm-5.3-flash`** (the provider advertises only
`max/high/low`), so the provider silently substituted `high`. No token or
benefit claim is made; the 10 percent target was waived by pre-registration.

Date: 2026-09-17. Role: test and eval agent. Harness/eval code only; no product
behaviour changed by this session. Worktree
`/Users/tafseerhaque/Documents/splice-archeval`, branch
`wip/evidence-substitution-production`.

## 1. Identity

| item | value |
| --- | --- |
| product fix under test | `cee57c7` keep raw-anchored edits in the hedged modify; never diff an empty content |
| harness revision | `ad72d8b` + this session's working-tree changes (`splice_dirty=true`) |
| binary | `/tmp/p4-fam05-splice`, SHA-256 `3c0f091cc151948c7926c283a4bb8b48cdddc5bf808243eb18d1653bef71e225` |
| sidecar | `/tmp/p4b-memd`, SHA-256 `152c2c3e8b127a396444470d7be38195a32a7d94e7ed38ca1fe9a86ef49ce6bf` |
| model | `z-ai/glm-5.3-flash` (resolved and asserted) |
| reasoning effort | **pinned medium; provider does not support it and used high** (Section 6) |
| run id | `mvp-matched-1789671344099883000` |
| arm order | `[warm manual cold]` (scheduling seed `20260917`) |
| spend cap | $0.05 |

## 2. Offline prep (no spend)

`tests/evals/cognition-families/fam-05-pair.json` wires the fam-05
precursor/target pair into the campaign pair format, using the cognition fixture
(`../taskset-v0/fixture`) and two external verifiers:

- `verifiers/fam-05-a.sh` (Task A): `mapStoreError` is declared in `main.go`,
  maps `ErrNotFound -> 404` and the unknown default to `500`, and
  `sessionHandler` routes its store-error branch through it.
- `verifiers/fam-05-b.sh` (Task B): a new admin handler answers 404 through the
  same `mapStoreError` table and no store-failure status is hand-coded outside
  the table.

The offline gate (`validate_pair.py`, the pair-format extension of the existing
`validate_mvp.py`) proves against real fixture copies:

```
fam-05-handler-error-mapping: [FPFPF] OK (base/goldA/baseB/goldB/wrongB)
all pairs validate [base FAIL, goldA PASS, baseB FAIL, goldB PASS, wrongB FAIL]
```

The required `base FAIL / goldA PASS / baseB FAIL / goldB PASS / wrongB FAIL`
matrix holds. Full output: `offline-validation.txt`, registry:
`registry-pairs.json`.

The pre-B match-matrix gate (`--match-matrix-gate`) is implemented in the
campaign runner and is the same `RecordSpeaksOfSubject` code path `seedManualArm`
uses. It derives the target task's non-open-discovery needs against the frozen A
tree, tests every frozen capture record, writes `match-matrix.json`, and stops
the run before any Task B provider request when no need matches. Regression
tests: `internal/cli/mvp_match_matrix_test.go`
(`TestBuildMatchMatrixMatchesNamedOperation`, `TestMatchMatrixGateStopsOnEmptyMatrix`,
`TestFam05PairManifestResolves`). No stage-0 re-run was performed.

## 3. A seed (paid) and the match matrix

Task A ran once and its verifier passed (`ok demo`): it added `mapStoreError` to
`main.go`, routed `sessionHandler` through it, and also created the missing
`ErrConflict`/`ErrInvalidID`/`ErrUserAtCap` sentinels in `session.go`. The tree
was frozen and the natural capture bundle exported.

| item | value |
| --- | --- |
| A outcome | success, verifier pass |
| A commit / tree | `ba125c8db48926f368bec9d0ea23ac2ce1688d14` / `3dd698085b04a17ad249dc3a4202e79ba8676b90` |
| A bundle capture origin | `natural` |
| A bundle capture digest | `096685d54be24fc659ac5d31e386b53a28f05de152510275e2292937ac795137` (3 nodes) |
| A tokens / billed | 30,147 / $0.00318519 |

The pre-B match matrix (full artifact `match-matrix.json`):

- Derived non-open needs (2): `locate:main.go#main` and
  `locate:main.go#mapStoreError` - both `locate-named-operation`, origin
  `cold-symbol-index`, required. One open-discovery need.
- Records (3): one `main.go` fact whose supporting refs include
  `main.go#mapStoreError` (and `main.go#main`) - **MATCHED**; one `session.go`
  fact - unmatched; one node carrying no typed reuse record - a hint.

`matched_records = 1`, so the gate passed and the campaign proceeded. This
answers:

- **M1 - YES.** B derives the resolvable need: `main.go#mapStoreError`,
  `NeedLocateNamedOperation`, required.
- **M2 - YES.** A's natural capture anchors it: a `main.go` fact with the
  `mapStoreError` symbol anchor.

Note the pre-registered contract caveat applies: the need's origin is
`cold-symbol-index`, so under the current contract warm cannot claim token
savings on it. That is reported, not framed as a benefit.

## 4. Automatic B (paid) - the arm did not complete

The automatic arm (`--memory on`, prefetch on, substitution off) replayed the
frozen capture (`capture_replayed=true`, `warm_setup_valid=true`), then stalled:

| item | value |
| --- | --- |
| outcome | executed, **success=false**, `failure_category=agent_noncompletion` |
| submission | none (`proposed_digest` = sha256 of empty) |
| tokens | 18,152 |
| requests (exec stream) | 2 usage events, second at the 8192 completion cap |
| reasoning events | 12,223 |
| tool calls / file reads | 7 / 3 (`main.go`, `session.go`, `session_test.go`) |
| latency | 406,109 ms (stopped at ~6.8 min to bound spend) |
| cost | ledger `unavailable` (killed mid-stream; unknown is not zero); the two reported usage events total **$0.0041303** |

**M3 - NOT OBSERVED.** The capture was replayed and delivery was enabled
(`effective_prompt_delivery=true`), but the arm produced no completed request
path and no trace, so "delivered into B's first request" is unproven.
**M4 - NOT OBSERVED.** Cold B was not run, so no file-read comparison exists.
The warm arm did read `main.go` (`raw_file_read` + `read_file`), so on this
evidence the main.go read was not removed.

The arm was stopped rather than letting the 30-minute per-attempt bound run: it
had already emitted ~3 MB and the spend ceiling was at risk.

## 5. Spend against the cap

| item | value |
| --- | --- |
| cap | $0.05000 |
| A seed (ledger-authoritative) | $0.00318519 |
| automatic B (ledger) | unavailable (killed mid-stream) |
| automatic B (exec-stream reported usage) | $0.0041303 (lower bound) |
| manual B / cold B | not run (no spend) |
| **ledger-authoritative total** | **$0.00318519** (automatic unknown is not zero) |
| shared-key delta over the run window (upper bound only) | 12.798811871 -> 12.825507559 = +$0.02670 (shared/contaminated; not attributed) |

Under the cap either way; the run stopped because the automatic arm did not
complete, not because the cap was reached.

## 6. Defect found and fixed offline: unsupported reasoning effort

The automatic arm's exec stream begins with the provider's own line:

```
reasoning effort "medium" is not supported by z-ai/glm-5.3-flash; using high instead
```

The provider catalog confirms `z-ai/glm-5.3-flash` advertises
`reasoning.supported_efforts = ["max","high","low"]` (`default_effort=max`). The
standing pins `reasoningEffort=medium`, so the pre-registered condition could not
be honoured and the provider silently used `high` - the reasoning-loop /
non-completion mode the earlier runs hit.

Offline fix + regression test (no product behaviour):

- `tests/evals/warmcost/reasoning_effort.go`: `ResolveStageReasoningEffort`,
  `ParseModelReasoningInfo`, `EffortSupported`, `FetchModelReasoningInfo`.
- `tests/evals/warmcost/reasoning_effort_test.go`: pins the real glm metadata
  (`max/high/low`), asserts **medium is rejected**, and covers fetch
  errors/missing metadata as loud failures.
- `p4preflight --check-reasoning` now asserts the pinned effort is advertised
  and **aborts before any provider request** on a mismatch; wired into
  `p4-run.sh` and `paid-run.sh`. With the campaign config it now exits:

```
REASONING EFFORT ASSERTION FAILED: model z-ai/glm-5.3-flash advertises
supported_efforts=[max high low] (default "max"); stage code_writer pins "medium"
(stage-models.json:code_writer); aborting before any provider request
```

The `paid_run_guards_test.sh` build guards still pass (an unset effort is
reported and not asserted, so unconfigured tasksets keep working).

## 7. Harness offline gate

`go build ./...` clean; `gofmt` clean on changed files; `go vet ./internal/cli/`
clean; the match-matrix, campaign, manual-seeding, matched-runner and
reasoning-effort test sets pass.

## 8. Unverified / not done

- M3 (delivery into the automatic arm's first request) and M4 (reads removed vs
  cold B) are unverified; the automatic arm produced no submission and cold B
  was not run.
- The manual arm was not run, so no reviewer-selected-seed comparison exists.
- No repeats, no held-out confirmation.
- The automatic arm's billed cost has no ledger row (killed mid-stream); the
  exec-stream figure is a lower bound and the shared-key figure is only an upper
  bound.
- The medium reasoning effort is unsupported for this model, so a run under the
  pre-registered standing cannot be produced with `z-ai/glm-5.3-flash` as
  configured.

## 9. Verdict and next step

`MECHANISM_OBSERVED` for M1 + M2 (`MECHANISM_OBSERVED` ceiling reached for the
derivation/anchor link); M3 + M4 remain `NOT_OBSERVED`. The TTL-pair failure
mode (empty match matrix) does **not** reproduce for fam-05: A's capture does
speak of B's derived need. The blocker is now the model/provider: the pinned
medium effort is unsupported and the substituted high effort does not complete.

Concrete next step: either (a) pick an effort the model advertises (high, max or
low) and re-pre-register the reasoning-effort standing, or (b) run the same pair
on a model that advertises medium; then re-run the staged diagnostic with the
new preflight guard so no spend occurs on a substituted effort.

## 10. Evidence index (this directory)

- `binary-sha256.txt` - binary and sidecar digests.
- `offline-validation.txt`, `registry-pairs.json` - offline pair matrix.
- `match-matrix.json` - derived needs x records with match/unmatched reasons.
- `attempt-rows.jsonl` - full per-attempt rows.
- `spend-ledger.txt` - token/correctness/cost rows and the ledger sum.
- `provider-capability.txt` - advertised reasoning efforts.
- Raw run artifacts: `../p4-fam05-stage1/` (A patch/verifier, snapshot bundle,
  exec streams, warm debug transcript).
