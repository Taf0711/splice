# EVAL-V0 Phase E0: Evaluation Infrastructure Audit

Status: complete audit against the current branch (`feat/c1c-miss-path-
precision`, based on `dev` at `43a7766`). Sources are file-level and
verified in code, not recalled from design docs. This document is the E0
deliverable of the evaluation-first handoff (section 41.A) and the
inventory that section 6 asked for.

## 1. What already exists (verified)

### 1.1 Runner surfaces (three, with distinct jobs)

| Surface | Entry | Contract | Job |
|---|---|---|---|
| agenteval suite runner | `splice eval --suite <path>` (`internal/cli/agent_eval.go`) | `splice.agenteval.report.v1` (`internal/agenteval/score.go`) | Curated task suites scored by hidden verifier commands, changed-file checks, forbidden-file checks, required trace events, context checks |
| benchmark runner | `splice eval bench` (`internal/agenteval/benchmark.go`) | same report contract + model matrix + mean latency | Multi-model matrix runs with per-run latency and usage samples |
| paired arms harness | `splice eval pe --taskset <dir>` (`internal/cli/pair_eval.go`) | `internal/eval` v1 report + decision gates | Cold/warm paired execution with per-pair JSONL logging |

### 1.2 Paired cognition machinery (already encodes handoff invariants)

`internal/eval/harness.go` + `internal/eval/decide.go`:

- Two materialized arm copies; stable per-arm root across the run so warm
  memory is same-project (section 47 invariant).
- Pristine fixture reset before every task pair.
- Decision gates: warm success >= cold (ties resolve to the incumbent),
  cost margin 0.90 (warm must be strictly cheaper per success), explicit
  INCONCLUSIVE state when the cold arm has no successes.
- Paired difference logging per task (`pe-pairs.jsonl`).

`internal/eval/v2/` (28 files, ~6k lines) adds the governance layer:

- `Manifest` with sidecar identity locking.
- `MemorySnapshot` with holdout-task validation and structural rekeying
  (cognition state as an explicit entity: section 47).
- `TrialJournal` with resume of incomplete trials (raw evidence
  preservation: section 31).
- Telemetry completeness checks with REQUIRED fields, including
  `provider_cost_usd` non-negative finite (unknown cost stays a
  validation error, not a silent zero: section 30), symmetric telemetry
  checks, selection audit, grader isolation, QA sealing, memory write
  policy denial, deny-rule labels, route verification, preflight,
  schedule generation.

### 1.3 Task corpora (existing, small)

- `tests/evals/golden_tasks.json`: 4 golden tasks.
- `~/Documents/splice-eval-taskset`: 12 tasks with shell `check`
  verifiers (exit 0 = pass, never LLM-judged), pristine fixture, validate
  scratch module, prior run reports. Pinned to `internal/eval/taskset.go`.

### 1.4 Telemetry already captured per run

- Usage: `UsageSamples` per stream-json usage event (input/output/cached
  tokens, `UsageReported`, sequence) in `agent_command.go`.
- Cost: pipeline `UsageRecords` + `CostUSD` in `schemas/plan.go`; v2
  completeness requires explicit cost fields.
- Latency: `LatencyMs` per agent command and mean per benchmark model.
- Cognition: trace `MemoryLookupMode` (direct/search), `DirectHits`,
  `StaleHits`, `DirectCandidates` per stage (`schemas/trace.go`).
- Stage breakdown: `internal/agenteval/stage_breakdown.go`.
- Trace events: `ParseTraceEventKeys` + required-trace-event scoring.

### 1.5 Cognition knobs needed by the eval program

- Cognition OFF: `SPLICE_EXEC_MEMORY=off` -> `MemoryStatus=off`
  (`internal/cli/exec.go`), plus runtime nil-store path.
- Budget source: static per-tier defaults vs `learn.Calibrated` fits with
  provenance strings (`run.go` LN2).
- Freshness: `cognition.ClassifyFreshness` / batched memoized cache;
  contract FRESH/STALE/UNKNOWN, unknown fails closed.
- Ablation: `SPLICE_EXEMPLAR_MODE` (both/obs-only/exemplar-only/none),
  fail-loud invalid values.

### 1.6 Deterministic suites landed this phase (E1 first slice)

- `internal/splice/cognition/freshness_eval_test.go`: 93 materialized
  repo-state cases (file/package/symbol/generated families), section 10
  counters (false fresh hard-gated to 0, false stale, unknown
  expected/observed, 9 recorded limits), batch-vs-single exactness
  across the 53-case core matrix.
- `internal/splice/cognition/keys_eval_test.go`: 37 explicit-expectation
  cases + no-fuzzy-slop property sweep + bounded-intent property. Found
  and fixed two real precision defects (longest-extension match,
  IP-host rejection).

## 2. Gap analysis against the eval program (section 30 metrics schema)

| Requirement | State | Smallest change needed |
|---|---|---|
| verified_success + verifier results | EXISTS | none |
| forbidden modifications | EXISTS (agenteval) | none |
| usage tokens | EXISTS (UsageSamples, pipeline records) | normalize into the unified report row |
| estimated cost | EXISTS (v2 required field; pipeline CostUSD) | none |
| cost unknown stays unknown | EXISTS in v2 (validation error on missing) | none |
| tool/search/file-read/shell counts | PARTIAL | aggregate per-tool counters from the trace into the report row |
| model_calls, repair_loops, orchestrator_calls | PARTIAL | lift from existing trace fields into report row |
| cognition counters (keys generated, lookup attempts, hits, misses, fallbacks, retrieved, admitted, exemplars, delivered tokens) | PARTIAL (direct/stale/candidates in trace; delivered counts on InputMeta) | add keys_generated + misses + fallback + exemplar fields to trace meta, then surface |
| budget_source / learned_budget / fit_sample_count / budget_abort | PARTIAL (provenance strings exist) | add structured fields beside the provenance text |
| total/stage latency | PARTIAL (agent wall latency + mean; stage breakdown exists) | emit stage rows into the unified report |
| retrieval_latency, freshness_latency | MISSING | instrument the cognition cache + lookup path with duration fields |
| verification_latency | MISSING | time the verifier command block in agenteval |
| raw JSONL trace preservation | EXISTS (journal + TraceStdout) | none |
| suite/task/fixture/commit revision stamping | PARTIAL (suite ID, contract version; no fixture commit) | record fixture git SHA + splice commit in the report header |
| confidence intervals / bootstrap | MISSING | post-processing only; no runtime change |
| failure taxonomy (section 33) | PARTIAL (Status pass/fail/blocked/error) | add a taxonomy field derived from status + stage evidence |
| task quality registry (section 34) | MISSING for agenteval suites | small JSON registry file + validation hook |

## 3. Verdict: no second framework

Section 6's bar is met by extending the existing stack. The unified
report row (section 38) should be ONE new type consumed by both
`agenteval` reports and `eval/v2` journal entries, not a third runner.
Everything in the section 30 schema is either present or a field-add +
lift, except retrieval/freshness/verification latency (instrumentation,
small and local) and the statistics pass (offline tooling).

## 4. Ordered E0 exit work (smallest changes, per section 6)

1. Add `ReportRow` (section 30 schema) to `internal/agenteval`; populate
   from existing report + trace data. No runner changes.
2. Add `keys_generated`, `lookup_misses`, `fts_fallback`,
   `exemplars_retrieved/admitted` to trace stage meta (schema-add with
   validation, goldens regenerate).
3. Instrument freshness + retrieval durations in the cognition cache
   (single timing point each).
4. Stamp `fixture_commit` + `splice_commit` + `suite_revision` into the
   report header.
5. Failure taxonomy mapping in the scorer (derived, not new state).
6. Task quality registry JSON + suite validation hook (section 34).
7. Offline `evalstats` step (bootstrap CIs, paired medians) as a small
   command reading journals; no model involvement.

Items 1-7 complete section 41.A and unblock the section 42 acceptance
checklist. The E1 deterministic slice (item D/E of section 41) is
already partially landed (freshness 93 cases, keys 37 cases); the next
expansion there is volume, not architecture.

## 5. Explicitly out of scope until eval evidence demands it

Per section 40: no C1d/C1e continuation, no C1c weight/budget retuning
without ablation results, no go-git/libgit2/FSMonitor/SCIP/tree-sitter,
no TUI redesign, no cross-project memory. The two C1c precision fixes
landed during E1 are section 40's allowed exception (eval failure ->
implementation -> re-eval).
