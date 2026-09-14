# Output-reduction eval, 2026-09-10

Deterministic, offline evaluation of the native tool-output reducers.
No provider call occurs. The result is reproducible on any machine.

Eval id: `native-output-reduction`, version 1.
Machine-readable result: `tests/evals/results/REDUCE_EVAL_2026_09_10.json`.

## 1. Scope

This eval measures one thing: does the native reducer remove redundant tool
output and keep every signal?

It answers three questions:

1. Does the reducer fire on the output shapes that it should reduce?
2. Does the reducer refuse the output shapes that it cannot reduce safely?
3. Does every declared signal survive a reduction?

## 2. Out of scope

This eval does not measure a model. It does not measure a provider. It does not
measure an end-to-end agent run.

Tool-calling benchmarks such as BFCL v4, tau-squared-bench, and ToolPrivBench
require model inference. They cost provider tokens. They are a separate
activity.

This eval covers the reducer only. The reducer runs inside the bash tool path
and the tool registry path.

## 3. Method

Corpus: six files under `internal/tools/testdata/reduction/`. Every file is
real captured output from this machine.

| Case | Source | Declared behaviour |
| --- | --- | --- |
| go-test-pass | `go test -v ./internal/eval/...` in this repo | reduce |
| go-test-fail | `go test -v ./...` in a fixture with a failing subtest | reduce |
| go-compile-error | `go test -v ./...` in a fixture with a type error | refuse |
| go-bench | `go test -bench=. -benchtime=10x -v ./...` in a fixture | reduce |
| git-diff | `git diff` in this repo | refuse |
| git-status | `git status --porcelain` in this repo | refuse |

Each case declares a list of signal substrings. The eval checks five things for
every case:

1. **Expectation.** The observed behaviour equals the declared behaviour.
2. **Signal preservation.** Every declared signal substring survives.
3. **Order.** The output is an in-order subsequence of the input. The reducer
   removes lines only. It never reorders and never rewrites a line.
4. **Idempotence.** A second pass changes nothing.
5. **Redundancy removal.** Every declared redundant substring is gone.

Baseline: head-and-tail truncation at 32 KiB. That is the current bash emit
budget (`bashOutputBudgetBytes`). The baseline column shows what the reducer
replaces.

## 4. Results

Command: `go test ./internal/tools/ -run TestReduceEval -count=1 -v`

Cases: 6. Passed: 6.

| Case | Expect | Observed | Bytes in | Bytes out | Saved | Baseline (32 KiB) | Signals | Order | Idem | Verdict |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | --- | --- | --- |
| go-test-pass | reduce | reduce | 24,345 | 117 | 99.5% | 24,345 | 3/3 | true | true | pass |
| go-test-fail | reduce | reduce | 926 | 570 | 38.4% | 926 | 11/11 | true | true | pass |
| go-compile-error | refuse | refuse | 145 | 145 | 0.0% | 145 | 2/2 | true | true | pass |
| go-bench | reduce | reduce | 355 | 207 | 41.7% | 355 | 4/4 | true | true | pass |
| git-diff | refuse | refuse | 51,705 | 51,705 | 0.0% | 32,768 (truncated) | 3/3 | true | true | pass |
| git-status | refuse | refuse | 1,056 | 1,056 | 0.0% | 1,056 | 2/2 | true | true | pass |

Totals: 78,532 bytes in, 53,800 bytes out, 31.5% saved. The total includes the
diff refusal, which saves nothing. Read the per-case numbers.

## 5. Findings

**F1. The reducer removes passing-test blocks only.** The fully passing log of
377 lines becomes 5 lines, or 117 bytes. The five remaining lines are the two
package summaries. The reduction is 99.5%.

**F2. Every failure signal survives.** The failing log keeps 11 of 11 declared
signals. These include the parent failure, the subtest failure, the skip, the
pause and continue lines, the failure detail, and the final summary.

**F3. The reducer refuses four shapes.** A compile error, a diff, a porcelain
status, and a benchmark log without passing tests produce no change. Refusal is
a safety property. It is not a defect.

**F4. A `-v` benchmark run contains passing-test blocks.** The reducer removes
them and keeps every benchmark number. Four of four signals survive. This
behaviour was not predicted before the eval.

**F5. Two eval expectations were wrong in the first run.** The first run
reported two failures. Both were errors in this eval, not in the reducer:

- `go-bench` was declared as a refusal. A `-v` benchmark run emits `=== RUN` and
  `--- PASS` lines for its passing tests. The reducer correctly removed them.
- `git-status` asserted a branch name. Porcelain output does not contain one.

Both expectations are corrected. The reducer was correct in both cases. This
note is recorded because a silently corrected expectation hides a real result.

## 6. Known gap

The diff case is not solved. A diff has no passing-test blocks, so the reducer
refuses. The output stays at 51,705 bytes.

Head-and-tail truncation would reduce that to 32,768 bytes, but it drops the
middle by offset. On this exact diff, the truncation loses 6 of 17 changed files
and 12 of 36 hunks.

The reducer therefore trades a token saving for content preservation on diffs. A
structural diff budgeter is the next slice. It must keep every file header and
every hunk header, and mark each elision.

## 7. Reproduction

```bash
# Run the eval. It prints the Markdown report.
go test ./internal/tools/ -run TestReduceEval -count=1 -v

# Run the eval and write the JSON report.
SPLICE_REDUCE_EVAL_JSON=tests/evals/results/REDUCE_EVAL_<date>.json \
  go test ./internal/tools/ -run TestReduceEval -count=1
```

The JSON write is off by default. A normal `go test` run has no side effect.

## 8. Limitations

- The corpus holds one input for each shape. It is small.
- The corpus comes from one machine, one operating system, and one repository.
- The signal lists are hand-written. The eval checks the listed signals only. An
  unlisted signal is not checked.
- The eval does not compare against the external reducer. An earlier manual
  measurement showed that the external generic reducer removes more diff
  content than the baseline. That measurement is not part of this eval.
- The eval does not measure the registry path or the bash spill path end to end.
  Unit tests cover both paths.
- The aggregate saving is sensitive to the case mix. The diff case alone holds
  66% of the input bytes and saves nothing.
