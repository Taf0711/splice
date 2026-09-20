# W5: offline verification and the paired measurement runner

Date: 2026-09-11.
Revision: `54bb470` on `wip/evidence-substitution-production`.
Worktree: `/Users/tafseerhaque/Documents/splice-archeval`.
Method: the four gate commands, the targeted guard nets, and an offline
self-test of the new runner with a scripted child. No provider call.
Nothing committed or pushed.

## 1. Offline verification

| Item | Command | Result |
| --- | --- | --- |
| 1a gofmt | `gofmt -l .` | PASS, no output, exit 0 |
| 1a vet | `go vet ./...` | PASS, exit 0 |
| 1a test | `go test ./...` | PASS, exit 0, no FAIL line |
| 1a sidecar | `cd memd && go test ./...` | PASS, exit 0 |
| 1b guard nets | `go test ./internal/splice/ -run '<guard regex>' -count=1 -v` | PASS, exit 0 |
| 1c default off | `go test ./internal/splice/ -run 'TestEvidencePlanNotBuiltWhenSubstitutionOff\|TestResolveEvidenceSubstitution' -count=1 -v` | PASS, exit 0 |
| 1d round split | `go test ./internal/splice/ -run 'TestRequestLedgerCarriesSpendIdentity\|TestRequestAttributionCellReclassification\|TestSliceSpendSourceMapping\|TestSpendSourceForInvocation' -count=1 -v` | PASS, exit 0 |
| 1e cold identity (switch-gated) | `go test ./internal/splice/ -run 'TestScopeOffKeepsMemoryDeliveryUnchanged\|TestScopedContextRequest_ColdFallbackIsDefault\|TestNoEligibleRecordMeansWarmEqualsCold' -count=1 -v` | PASS, exit 0 |
| 1e cold identity (full result and trace) | see section 1e | FAIL |

The full gate log is `/tmp/p1_gate.log`. The exact summary lines are:

```text
GOFMT_EXIT=0
VET_EXIT=0
ok   github.com/Taf0711/splice/internal/cli      235.270s
?    github.com/Taf0711/splice/tests/evals/warmcost [no test files]
TEST_EXIT=0
ok   github.com/Taf0711/splice/memd
ok   github.com/Taf0711/splice/memd/store
MEMD_TEST_EXIT=0
```

`internal/cli` is slow (235 s, a sandbox filesystem walk) but passes. The
targeted guard nets all pass. The test output names every PASS line.

### 1d round split

`run.go:1349` reclassifies round 1 and later to `SpendSourceExpansion`; round 0
keeps its generation attribution. `TestRequestAttributionCellReclassification`
and `TestRequestLedgerCarriesSpendIdentity` pin that generation, expansion, and
repair stay distinct. `TestSliceSpendSourceMapping` pins the F2 report mapping.
All pass.

### 1e cold path identity

The switch-gated mechanisms are behavior-preserving when both switches are
unset: `SPLICE_SCOPE_MODE` defaults to off and `SPLICE_EVIDENCE_SUBSTITUTION`
defaults to off, so no evidence plan is built (`TestEvidencePlanNotBuiltWhenSubstitutionOff`),
no context request is swapped (`TestScopedContextRequest_ColdFallbackIsDefault`),
and no tool suppression runs (`TestScopeOffKeepsMemoryDeliveryUnchanged`).
`TestNoEligibleRecordMeansWarmEqualsCold` pins that an empty eligible set
leaves the warm plan equal to the cold plan.

The full pipeline result and trace are NOT byte-identical to the pre-fold
revision (`8e68846`). `ed9146d` added the W3 billed-cost rule and wired it into
the live loop at `run.go:757` to `run.go:773`. That path is not switch-gated.
When cost coverage is complete, `iterationCostSignal` returns a non-nil signal
from iteration 2 onward, and `EvaluateTrajectoryWithCost` runs the new
`cost_without_progress` rule. Offline proof on one fixture:

```text
TestTrajectoryCostRuleFiresWhenRoundsFallButCostRises  PASS
TestTrajectoryCostRuleInactiveWithoutSignal            PASS
```

The same flat-correctness history returns `step_back` when the billed-cost
signal is present and `continue` when it is absent. A cold run with complete
pricing and a stalled second iteration therefore follows a different
trajectory than the pre-fold revision. This is an intentional W3 behavior, not
a defect, but it is a real break of the literal byte-identity requirement. No
production code was patched.

## 2. Paired measurement runner

Added (uncommitted): `tests/evals/warmcost/`.

- `types.go`: arm definitions and the per-attempt and per-request artifact
  shapes.
- `runner.go`: the exec seam, attempt loop, ledger capture, verifier, and
  artifact writing.
- `metrics.go`: arm metrics, the round/payload cost split, per-task effects,
  and the task-clustered bootstrap.
- `report.go`: the Markdown report.
- `cmd/warmcost-eval/main.go`: the CLI.
- `README.md` and `taskset-example/`: invocation and taskset format.
- No test files, so `go test ./...` does not run it.

Invocation:

```bash
go run ./tests/evals/warmcost/cmd/warmcost-eval \
  --repo . --binary /path/to/splice --model <model> \
  --tasks tests/evals/warmcost/taskset-example \
  --out tests/evals/results --repeats 3 --arms cold,warm \
  --correctness-margin 0.05 --sidecar-revision <rev>
```

Arms: cold (both switches off, memory off), warm (both on, memory on), and
optional warm-retrieval-only (scope on, substitution off, delivery off).

Per attempt it records the task id, arm, repeat, binary revision, sidecar
revision, fixture digest, model id and settings, treatment environment,
verifier result, run status, cost coverage, and every request from the ledger
that `applyRequestLedger` joined. Per request: sequence, stage, iteration,
invocation ordinal, context round, spend source, input and output tokens,
cached and cache-write tokens, reasoning tokens, cost USD, and cost status.

Coverage rule: `CostCoverage == complete` is required for the total-cost
claim. A partial attempt is marked partial and the claim is withheld. A missing
price is never read as zero.

Aggregate: per-arm requests per verified completion, billed USD per attempt,
billed USD per verified completion, round share, cache share; the round and
payload cost channels; the cache channel in tokens (the ledger has no
per-token cache price, so a USD cache split would be fabricated); per-task
effects; and a bootstrap over tasks, not requests.

### Offline self-test

A scripted child (`/tmp/fake-splice.sh`, not in the repo) emitted valid final
stream-json events with priced ledgers. Command:

```bash
go run ./tests/evals/warmcost/cmd/warmcost-eval \
  --repo . --binary /tmp/fake-splice.sh --model fake \
  --tasks /tmp/wc-tasks --out /tmp/wc-out --repeats 2 \
  --arms cold,warm,warm-retrieval-only --correctness-margin 0.05 \
  --bootstrap-samples 200
```

Output: `arms=3 tasks=1 attempts=6`, one attempt JSON per cell plus
`aggregate.json` and `report.md`. The attempt JSON carries `invocation_ordinal`
and `context_round` explicitly, including zero values. The aggregate carries
arm metrics, the decomposition, the bootstrap interval, the per-task effects,
and the claim. `go vet ./tests/evals/warmcost/...` passes and `gofmt -l` is
clean.

## 3. Paid provider run

NOT-RUN. No owner approval was recorded, and no provider credentials were
used. Before a run, present: the exact command, the model, the task count, the
repeat count, the estimated request count, and the pre-registered correctness
margin. After the run, measure `p` and `H` and install them with
`SetSubstitutionEVInputs`.

## 4. Findings

- The runner exists, builds, and produces the data contract offline. The
  payload and round channels are separable; the cache channel is token-only
  until a per-token cache price is in the ledger.
- Part 1e fails the literal full-result byte-identity check because the W3
  cost rule is unconditional. The switch-gated mechanisms are behavior
  preserving when off.
- No measured saving is claimed. The claim is withheld unless an approved
  provider run supplies complete coverage and a task-clustered interval that
  excludes zero.

## 5. Missing evidence and residual risks

- No live provider run, so no live cache hit proof (W4 check 1) and no `p` or
  `H`. The W1 gate stays inactive by design.
- One fixture per test. The runner's bootstrap is only meaningful with more
  tasks.
- The cache channel in USD is unmeasured; only token deltas are reported.
- The cli test is slow in this environment (235 s). It passed, but an
  under-provisioned machine could time out.

## 6. Sources

- `tests/evals/cognition-families/MEASUREMENT_DESIGN.md`
- `tests/evals/results/W4_C3_C4_VERIFICATION_2026-09-11.md`
- `plans/HANDOFF_WARMCOST_REVIEW_FOLD_2026-09-11.md`
- `internal/splice/substitution_gate.go`, `scope_correctness.go`,
  `run_cost_signal.go`, `trajectory.go`, `run.go:757-773`, `run.go:1349`
- `internal/splice/evidence_substitution_test.go`,
  `substitution_gate_test.go`, `scope_correctness_test.go`,
  `run_cost_signal_test.go`, `warmcost_ledger_test.go`,
  `treatment_seam_test.go`, `scope_plan_test.go`, `substitution_test.go`
- Gate and test logs: `/tmp/p1_gate.log`, `/tmp/p1b.log`, `/tmp/p1c.log`,
  `/tmp/p1d.log`
