# W4: C3 and C4 verification

Date: 2026-09-11.
Revision: `ed9146d` on `wip/evidence-substitution-production`.
Method: source inspection plus the existing regression tests. No provider run.

## Check 1. Live prompt cache hit. STRUCTURAL PASS. LIVE PROOF PENDING.

Confirmed:

- `stageOptions` builds the key as `SessionID + ":" + stage` (`internal/splice/registry.go`).
- The key flows through `stages.StageOptions.PromptCacheKey` into the provider call (`internal/splice/stages/code_writer.go:82`, `step_back.go:42`, `test_generator.go:119`).
- `TestCallToolUsePromptCacheKey` (`internal/splice/stages/stages_test.go:2416`) proves the key reaches the provider request.
- The OpenAI provider sends the key when prompt caching is not disabled (`internal/providers/openai/provider.go:528`).
- The ledger records cache-read and cache-write tokens (`internal/splice/run.go:116-117`), and `applyRequestLedger` sums them (`run.go:2368-2369`).

Not proven: a live hit, meaning `CachedTokens > 0` on a repeated round. That needs a provider run, which is owner-gated.

Limit: the key is a prefix hint, not a hit guarantee. The key is stable across rounds of one stage. The prefix is not, because an expansion or a front-load changes the context after the system prompt. C3 is therefore still conditional on the measured hit share.

## Check 2. Round split in the F2 report. PASS.

- Round 1 and later reclassify to `SpendSourceExpansion` (`internal/splice/run.go:1349`).
- `BuildWorkflowCostReport` groups spend by source (`internal/splice/workflow_cost.go`).
- `warmcost_ledger_test.go:96` asserts that generation, expansion, and repair stay distinct sources.

So round 0 (generation) and round 1 and later (expansion) are separable in the F2 report. No change.

## Check 3. Typed-need non-fit path. PASS.

- `admitRecord` returns `AdmissionHintOnly` when a record does not satisfy the need kind (`internal/splice/admission_test.go:161,176,183,216,237`).
- `TestOneResolvedNeedLeavesOthersOpen` proves that one resolved need does not suppress the others.
- The W1 gate adds a second rejection at the same function. A record the gate rejects is hint-only, never a substitution.

A record whose content does not satisfy the need kind cannot suppress a discovery operation. No change.

## Result

No code change. Two checks pass. One check passes at the structural level and stays unproven until an owner-approved provider run records a live cache hit.
