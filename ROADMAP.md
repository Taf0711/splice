# Splice Roadmap

Splice is a local-first, deterministic multi-stage coding pipeline forked from
`gitlawb/zero`. It runs as a CLI, passes typed Go schemas (ported from Flug's
Pydantic models) between deterministic stages, and publishes structured
stream-JSON events for headless clients.

## Incremental Development Cadence

Each checkpoint is a small, reviewable slice that lands green. Before starting
the next one we run focused local validation, update `MEMORY.md`, push, and
wait for CI.

1. Explain what is being added and why.
2. Show any new schema if stage communication changes.
3. Implement the smallest useful slice.
4. Add or update focused tests; prefer extending existing test files.
5. Run focused local validation (`gofmt`, `go vet`, `go test`).
6. Update `ROADMAP.md` and `MEMORY.md` in the same checkpoint.
7. Commit, push, and wait for GitHub Actions before the next checkpoint.

## North Star and how to read this file

**Charter**: `plans/SPLICE_ADAPTIVE_HARNESS_ARCHITECTURE.md` (adopted
2026-08-14, see `plans/adaptive-harness-adoption-2026-08-14.md`). The target
product: Splice learns how to work on a project; tokens and user
interventions per successful task trend down as project knowledge grows.
Workflow topology is data; the LLM proposes, deterministic infrastructure
governs; learning replaces hard-coded policy; local-first holds (Veritas is
optional, memd is the built-in store).

**Reading order for new work**:

1. `plans/adaptive-harness-adoption-2026-08-14.md` — the decision, the three
   amendments, and the canonical charter-phase-to-track map. Check it before
   opening any new track: several charter phases already have tracks.
2. `## MVP integration waves` — the active e2e functionality push.
3. The supporting tracks filed from the 2026-08-13 architecture audit: WL
   (worktree lifecycle), SD (stage decoupling, the charter's Phase 0), MD
   (memd hardening), PC (plan composer, charter Phase 5), LN (learning loops,
   charter Phase 9), TW (TUI worktree execution).
4. Older tracks (F-Zero through CR) are completed or runway work; Tracks
   T/W/A are the approved v0.3 runway and correspond to charter Phases 1-2
   plus user-defined topology.

**One-standing-rule from the RR/WG learned shape**: before adding a field,
config key, or callback, name its consumer. Produced-but-never-consumed is
the project's dominant defect class (six instances found 2026-08-13 alone).

**Module convention (owner directive, 2026-08-15)**: the codebase leans toward
feature-sets-as-modules so fixes and additions are local. A module is: one
package, an exported seam with a small surface, its own tests, a registration
point where it plugs in, and a docs section when user-facing. Applied
opportunistically to touched code; no mass restructure. `Stage.Capabilities()`
(SD2) is the reference shape for stages; `internal/plugins` is the reference
for user-supplied extensions.

## Track F-Zero (Splice on Zero)

F-Zero ports Splice's multi-stage pipeline on top of the Zero agent/tooling
substrate (`internal/agent`, `internal/tools`, `internal/zerogit`,
`internal/zeroruntime`).

- `[x]` **F1a**: archive the Python-era `flug` repo, clone Zero, create
  `Taf0711/splice`, import carried-over assets.
- `[x]` **F1**: rebrand repo, module, and binaries; config/data paths,
  user-facing strings, CI, install scripts.
- `[x]` **F2**: schema package (`internal/splice/schemas`) — Go structs with
  `Validate()`, ported from the Pydantic models.
- `[x]` **F3**: classifier, planner, and token-budget wiring.
- `[x]` **F4**: trajectory monitor with state vectors, oscillation/cycle
  detection, rollback, and abort rules.
- `[x]` **F5**: deterministic context builder (initial port; hardened and
  wired to Zero's real tool registry in G5).
- `[x]` **G1–G6**: post-audit remediation — green CI (G1), fail-loud parity in
  the deterministic core (G2), Python rune/timestamp semantics (G3), JSON
  round-trip tests for all schemas (G4), registry-backed context builder (G5),
  backfilled `MEMORY.md` / this roadmap / `UPSTREAM.md` (G6).
- `[x]` **F6**: ported stage agents in `internal/splice/stages/` (code_writer,
  test_generator, static_analyzer, security_auditor, test_runner,
  design_conversation, plan_critic).
- `[x]` **F7**: orchestrator `splicerun.Run` in `internal/splice/run.go` with
  the full `agent.Options` callback contract (streaming, usage, tool
  call/result pairs, permissions), wired into headless exec at
  `internal/cli/exec.go`. Completed 2026-07-08. Migrated 32 CLI exec tests to
  the shared `execStageAwareProvider` and deterministic pipeline semantics;
  stream-JSON gained additive `tool_call_start` / `tool_call_delta` events
  (see `docs/STREAM_JSON_PROTOCOL.md`).

## Upcoming

- `[x]` **F15**: design phase TUI wiring. Revised plan (D0-D6 slicing,
  adopted after an adversarial review rejected the original D1-D4 plan):
  `plans/design-phase-tui-wiring-2026-07-13.md`. Wire the Flug design phase
  (conversation, crystallization, adversarial critic, human-gated approval,
  execution handoff) into the interactive TUI. The execution phase is already
  wired (F12b through F14). The design stages (`design_conversation`,
  `plan_critic`) and schemas (`DesignPlan`, `PlanCritique`) exist in Go but are
  not reachable from the TUI. Session events are authoritative (no global
  plan file). Engine owns orchestration (not the TUI). Seven checkpoints:
  - `[x]` **D0** (2026-07-13): lifecycle and persistence contract. Seven
    design lifecycle event types in `internal/sessions/store.go`, `DesignPhase`
    enum and `PlanRevision` type in `schemas/design.go`, and
    `ReconstructDesignState` pure function in `internal/splice/design_lifecycle.go`
    that replays raw session events to derive design state. Fork inherits,
    rewind clears, compaction does not delete events from the raw log. No TUI
    changes, no LLM calls.
  - `[x]` **D1** (2026-07-13): stage contract repair. Fix `DesignConversation.Crystallize`
    (route through `callValidatedToolUse`, set `Source` before validation)
    and `PlanCritic` tool schema field mismatches (`intent`/`statement`/
    `suggested_mitigation`). Add `DesignConversationPrompt()` accessor and
    `ExtractPlanCritique()` typed helper. Captured-request tests prove the
    advertised field names match the Go structs. No TUI changes.
    (route through `callValidatedToolUse`, set Source before validation) and
    `PlanCritic` tool schema field mismatches (`intent`/`statement`/
    `suggested_mitigation`). Captured-request tests.
  - `[x]` **D2** (2026-07-13): read-only design conversation mode. Typed
    `tuiRunKind` (`pipeline`/`spec_draft`/`design`) replaces `specDraft bool`.
    `/design` and `/exec` commands. Read-only cloned registry (read tools +
    `ask_user`). Design conversation system prompt via `stages.DesignConversationPrompt()`.
    Status strip shows "design". `design_mode_entered` event persisted.
  - `[ ]` **D3**: crystallize and critic engine operation. Split into:
    - `[x]` **D3a** (2026-07-13): event-to-`ConversationMessage` mapper in
      `internal/splice/session_history.go` (pure function, 8-step contract).
    - `[x]` **D3b** (2026-07-13): engine-level design workflow API in
      `internal/splice/design_workflow.go` (`DesignWorkflow.CrystallizeAndCritique`:
      maps history, crystallizes, runs critic, persists `plan_crystallized` +
      `critique_recorded` events, provider routing via stage model resolver).
  - `[x]` **D4** (2026-07-14): resumable design runner. `RunDesignPlanWithResume`
    accepts `RunDesignPlanOptions` (unique `PlanID`, `CompletedTaskIDs` for
    resume, `OnTaskLifecycle` callback). `BuildExecutionPlanForTaskWithFacts`
    propagates acceptance facts into the task intent. `TaskResult` carries the
    full `PipelineResult` per task. Existing `RunDesignPlan` is a backward-
    compatible wrapper. 5 new tests.
  - `[ ]` **D5**: TUI execution and task-aware panel. Split into:
    - `[x]` **D5a** (2026-07-14): `/crystallize` command calls engine API,
      persists events, displays plan + critique in transcript.
    - `[x]` **D5b** (2026-07-14): `/approve` calls `RunDesignPlanWithResume`,
      lifecycle callback persists task events, result displayed in transcript.
  - `[x]` **D6** (2026-07-14): startup and project discovery.
    `reconstructDesignState` on `/resume` rebuilds design mode, pending plan,
    and critique from session lifecycle events. `/new` clears design state.
    Conversation/review phases restore design mode; executing/completed do not.

- `[x]` **F16**: functional memory (memd) in the TUI. Plan:
  `plans/functional-memory-tui-2026-07-14.md`. The memory sidecar is wired
  end-to-end but has no functional use: binary discovery fails silently, the
  user has zero visibility, and there is no way to browse or check memory.
  Four checkpoints:
  - `[x]` **D1** (2026-07-14): binary discovery (sibling-binary check) +
    `Client.Stats` + `make install-memd`.
  - `[x]` **D2** (2026-07-14): memory status in the TUI status line (`🧵 N` /
    `🧵 off`) + one-time transcript notice on state transition.
  - `[x]` **D3** (2026-07-14): `/memory` command (stats, search, recent).
  - `[x]` **D4** (2026-07-14): compact memory section in the sidebar.

- `[x]` **F17** (2026-07-15): TUI cosmetic port from an interactive HTML
  mockup. Plan: `plans/tui-cosmetic-port-2026-07-15.md`. Three deterministic,
  zero-token checkpoints: `teal:helix` palette in `theme_palettes.go` (with
  WCAG-AA contrast asserted by the existing theme-select test), `λ` user-prompt
  gutter glyph in `rendering.go`/`model.go`, and the `arc` running-stage spinner
  (reusing the existing ~80ms spinner tick, no new timer). Recon found 6 of 9
  approved toggles were already TUI defaults (no-ops); a ponytail cleanup shrunk
  the helix comment and deleted the 595-line mockup.

- `[x]` **F18** (2026-07-17): per-stage cost in eval CSV. The eval
  harness (`splice eval bench`) already captured per-task token/cost totals
  from stream-json `usage` events. The `final` event already carries the full
  `PipelineResult` JSON (with per-stage `StageRecord` usage from AR8) but the
  harness never parsed it. This checkpoint adds a consumer-side bridge:
  `parsePipelineStagesFromStdout` extracts the per-stage ledger from the
  `final` event's text, `StageBreakdown` mirrors the relevant `StageRecord`
  fields without coupling `agenteval` (Zero substrate) to
  `internal/splice/schemas` (pipeline layer), and `WriteBenchmarkCSV` gains a
  `stageBreakdown` column (`name:in=N,out=N,cost=F;...`). No protocol change,
  no `exec.go` change; the data was already emitted. Non-pipeline agents
  produce an empty `stageBreakdown` (graceful no-op).

## Track AU: 2026-07-15 ponytail-audit cleanup

Source of truth: `plans/ponytail-audit-cleanup-2026-07-15.md`. Verified dead
 code and stdlib re-implementations across the Splice-owned layers, with a
 no-contract-change-first ordering. Three scout false-positives dropped
 (`TestRunResults.Passed/Failed`, `redact`, `ComplexityClassifierInput`). The
 `stages.ToolResult` vs `splice.ToolResult` merge is explicitly NOT cut (it
 crosses the typed stage-boundary seam, AGENTS.md commitment #1).

- `[x]` **AU1** (2026-07-15): dead code + stdlib swaps, zero-risk. Delete
  `marshalJSON` (0 callers), `NewRegistryToolRunnerWithOptions` + its `options`
  field (0 callers), `RunEvent`/`AsRunEvent` (0 callers); swap
  `containsDomain`/`toolListContains` for `slices.Contains`; inline the
  single-use `timeNow()` wrapper. ~25 lines, no behavior change.
- `[x]` **AU2** (2026-07-15): orchestrator finish-helper merge, gated by the
  end-to-end pipeline test. Merged `finishFailed`/`finishAborted` into one
  `finishWithReason(..., status, reason)` (16 call sites). Two scout items
  DROPPED after parent verification: `joinShell`/`shellJoin` consolidation
  (display format change, not a pure refactor) and `test_generator` context
  bypass (the duplication is load-bearing — it deliberately pulls only
  `code_writer` prior + omits ContextBundle). ~10 lines.
- `[x]` **AU3** (2026-07-15): worktrees `Options.Env` yagni. Dropped the
  never-populated `Options.Env` field (`Prepare` now passes `nil` to
  `DefaultBaseDir`, whose `envValue` already falls through to `os.Getenv`).
  Three scout items DROPPED after parent verification: `MergeBackOptions.RunGit`
  (dead but mirrors a used test seam; kept by user call),
  `resolveBinary` DI params (6 test fakes use them), and `sha256.Sum256` ->
  `runID[:8]` (runID is `run_<ts>_<hex>`, not a UUID; the hash guards
  uniqueness + git-ref safety). ~3 lines.
- `[-]` **AU4** (skipped 2026-07-15): CLI exec wiring shrinks. Verify-first
  review found 5 of 7 scout claims were false positives (the "duplicate"
  writers operate on different writer types; the single-use structs have
  multiple consumers or do real resolution work). Only 2 real cuts remained
  (~14 lines: inline `writeExecToolList`, merge `parseExecDepth`/
  `parseExecMaxTurns`); user chose to skip as payoff too small. Available
  later if desired.
- `[x]` **AU5** (2026-07-15) `@needs-human`: typed-contract field trims in
  `DesignPlan`/`TechnicalSpec`. Deleted 5 fields collected from the LLM during
  the design conversation but never read, validated, displayed, or threaded
  downstream: `DesignPlan.Assumptions`/`OpenQuestions`/`SequenceDiagrams`/
  `Wireframes` + `TechnicalSpec.ObservabilityHooks`. The 4 DesignPlan
  properties were also removed from the hand-written `designPlanToolDefinition`
  LLM tool schema (lockstep). The Flug design corpus intended these for a
  human-in-the-loop design panel + code-writer handoff that the Go port never
  wired; they were collected-then-dropped since F2. Re-addable in ~5 lines if a
  future checkpoint wires the consumption. ~9 lines, zero behavior change.
- `[x]` **AU6** (2026-07-16): cut the dead
  `TechnicalSpec` cluster. Follow-up to AU5 after tracing every path: the
  design conversation tool schema never exposed `technical_spec` (the model
  cannot produce one), no authored/ingested path exists, and
  `CodeWriterInput.TechnicalSpec` (`*string`) has zero writers and readers.
  Cut `Entity`/`Endpoint`/`ComponentSpec`/`FilePlan`/`TestRequirement`/
  `TechnicalSpec` structs + validator from `schemas/design.go`, the
  `DesignPlan.TechnicalSpec` field + nil-check, `CodeWriterInput.TechnicalSpec`,
  the `schemas_test.go` Validate entry, and the stale "technical spec" phrase
  in `prompts/code_writer.md`. Keep the `Source` enum. Decision reinforced by
  the user-configurable pipeline direction below (stage contracts will be
  defined by the pipeline editor, so a hardwired spec handoff in the default
  pipeline is legacy either way). Full contract:
  `plans/ponytail-audit-cleanup-2026-07-15.md` (AU6 section). ~100 lines,
  zero LLM tokens, zero behavior change.
- `[x]` **AU7** (2026-07-18): ponytail-audit 2026-07-17 finding 1, deleted
  orphaned `cmd/splice-pr-review` + `internal/review/` (-537 lines).
  Remaining findings tracked in the 2026-07-17 audit report.
- `[x]` **AU8** (2026-07-18): ponytail-audit findings 3/M1/M2, deleted four zero-reference TUI helpers (-~25 lines).
- `[x]` **AU9** (2026-07-18): ponytail-audit findings 4/5, deduplicated git-output tail into `commandOutput` and made `defaultRunGit` delegate to `defaultEnvRunGit` (-~35 lines).
- `[x]` **AU10** (2026-07-18): ponytail-audit findings 8/7/6, stdlib swaps (`slices.Contains`, `slices.Equal`) and deleted duplicate `firstNonEmptyString` (-~27 lines plus test churn).
- `[x]` **AU11** (2026-07-18): ponytail-audit finding 2 + #9, removed eleven test-only TUI helpers from the prod surface and rerouted likelySandboxDenied (-~103 prod lines).
- `[x]` **AU12** (2026-07-18): ponytail-audit M3, deleted dead run-event schema family for the never-built `splice run --json` (-~90 lines plus tests). ChangeSummary/ChangedFile kept (live).

## Track: TUI/workflow redesign (2026-07-16)

Source of truth: `plans/tui-workflow-redesign-2026-07-16.md`. Turn Splice's
surface from "Zero's chat with a hidden pipeline" into a two-phase workflow
(planning then execution) with a cost-conscious, provider-agnostic
model-routing default. Six checkpoints; CP5 and CP6 are deferred to separate
plans. Each checkpoint lands green with focused tests plus the end-to-end
pipeline guard where relevant.

- `[x]` **CP1** (2026-07-16): tier-label stage contract + batteries-included
  resolver. Stages stop hardcoding model IDs and use tier labels; a new
  `internal/splice/stage_tier_resolver.go` picks the cheapest tool-capable
  model in the user's provider family, returning a real provider (not a model
  string, since `CompletionRequest` has no `Model` field). Composes as a
  middle layer in `BuildStageModelResolvers`: explicit `stage-models.json`
  override -> tier fallback -> primary. Survived two adversarial passes before
  code. Behavior change (documented): users with no override now get the
  tier-resolved model for execution stages instead of the primary for every
  stage. CI green (run 29529837756).
- `[x]` **CP2** (2026-07-17): onboarding rewrite (Option C-batteries-included).
  After the model-pick step, a new `setupStagePipeline` step shows the stages
  pre-filled by CP1's `ResolveStageTierModel`, each model-backed stage
  (`code_writer`/`test_generator`/`design_conversation`/`plan_critic`) an
  editable dropdown of the provider's tool-capable models; deterministic
  stages labeled and non-editable. `completeSetup` writes `stage-models.json`
  (mode 0o600) from first run. Custom-compatible providers skip dropdowns and
  write a default-only file. Prerequisite refactor: extracted pure
  `ResolveStageTierModel` from `NewStageTierResolver` so onboarding builds no
  throwaway providers.
- `[x]` **CP4** (2026-07-17): default entry phase = planning. A fresh
  interactive session (new user via onboarding, or `/new`) starts in design
  mode, not execution. Compose -> planner -> `/crystallize` -> `/approve` ->
  `/exec`; `/exec [prompt]` is the skip-planning escape hatch. Extracted
  `enterDesignMode` helper; construction sets `designMode = true`, `/new` and
  onboarding-exit call the helper; a one-time orientation notice (not a
  setting) mitigates the behavior change; `reconstructDesignState` still
  overrides for resumed sessions. Headless `splice exec` unchanged
  (execution-direct).
- `[x]` **CP3** (2026-07-17): phase-adaptive layout, one toggle. Added
  `/layout` toggling `planPanelPersistent`: when on + design mode + a
  crystallized plan, the `DesignPlan` (epic/requirements/tasks) renders as a
  bordered header pinned above the chat column so it survives transcript scroll
  during design revisions. Reuses `formatDesignPlan` + `borderedBlock`; inert
  outside its valid context (no behavior change when off). The original plan's
  second toggle (`pipelinePromoted`) is deferred as speculative: the sidebar
  PIPELINE section already shows stage status during runs, and demoting the
  live streaming chat has a real cost. Re-add when a real pain point surfaces.
- `[-]` **CP5** (deferred): security auditor LLM augment. Add an LLM
  security-engineer stage that reasons about gaps the deterministic scanners
  miss. Needs its own dated plan + eval contract before implementation.
- `[x]` **CP6** (2026-07-18): design-phase `Crystallize` separation. Pulled
  `Crystallize` out of the `DesignConversation` struct into its own typed agent
  `DesignCrystallizer` (own struct, prompt, tool definition, and stage name
  `design_crystallize`). The `DesignConversation` struct is deleted; the chat
  prompt accessor `DesignConversationPrompt()` stays. `DesignWorkflow.CrystallizeAndCritique`
  now resolves the crystallize call under `design_crystallize` (previously
  `design_conversation`), so the crystallization step is independently
  model-routable from the free-form chat (which runs on the primary provider
  via `agent.Run`). Tier preserved as `medium`. The stale crystallize prompt
  (referenced `open_questions`/`sequence_diagrams`/`wireframes` deleted in
  AU5/AU6) was fixed on the move. Prerequisite for independent model routing
  in the topology editor.
- `[x]` **CP6a** (2026-07-19): setup wizard pipeline-step model picker. The
  `setupStagePipeline` step used blind Left/Right model cycling (no visible
  option list, no search), inconsistent with `setupStageModel`, `/model`, and
  `/stages` (all type-to-search filtered lists). Replaced with an overview +
  drill-in picker: Right opens a search-and-filtered-list picker for the
  focused stage (reusing `filterStageModelOptions`); Enter commits, Esc/Left
  cancels; typing in overview auto-opens. `cycleSetupPipelineModel` deleted;
  `setupPipelineLeftAtTop` simplified. All four model-picking surfaces now
  behave the same way. `.go`-only change ported to public.

## Track FND: adaptive-harness foundation (2026-08-19)

Source of truth: `plans/adaptive-harness-direction-2026-08-19.md`. Purpose:
clear the rubble and lay the slab before any adaptive-harness feature work
(stage messaging, epistemic authority, topology). Every item fixes a silent
failure or installs an invariant the learning loop stands on. Done criterion:
the eight F items and the routed items land, nothing more. The failure mode of
this track is foundation creep; no additions without a named failure they
prevent.

Rubble (silent failures in the current system):

- `[ ]` **F1: deterministic-stage sandbox bypass.**
  `makeRecordedCommandCallback` (`internal/splice/registry.go:118`) wraps
  eventing only, and `runRecordedOutput` (`internal/splice/stages/commands.go:33`)
  runs raw `exec.CommandContext`; only the `RunTool` path is sandboxed.
  Contradicts commitment #8. Pulled out of T5, where it existed only as a side
  effect. Gate: `go test ./internal/splice/...`.
- `[ ]` **F2: trajectory doc/behavior mismatch.** `schemas/trajectory.go`
  documents decisions as report-only recommendations; `run.go:502-620`
  enforces every non-continue action. Semantics must match reality before
  decisions become evidence. Gate: `go test ./internal/splice/`.
- `[ ]` **F3: persist trajectory decisions.** Rule fired, rule-table version,
  and input state hash per iteration, into the trace. Makes the decision layer
  replayable and auditable. Gate: `go test ./internal/splice/`.
- `[x]` **F4: incremental event writes.** The trace writes once at run end; a
  crash loses everything and a write failure is non-fatal (`run.go:361`).
  Write incrementally with a degraded-mode contract (extend the `MemoryRecord`
  three-state pattern). Gate: `go test ./internal/splice/` plus `cd memd &&
  go test ./...`. Shipped `74873a9`, CI `32283467819`. Outcome status gains
  `running`; `UpsertTrace` is a true upsert guarded so a partial write never
  clobbers a settled row; mid-run failure marks `events_status: partial`.

Slab (invariants the learning loop stands on):

- `[ ]` **F5: authority math spec.** One pure function `Evidence ->
  AuthorityDelta`: named prior, decay, contradiction rule, hand-computable,
  fabricated-corpus tests. Spec document plus tests BEFORE any schema work
  (audit A1). Gate: the spec doc plus `go test ./internal/splice/learn/`.
- `[ ]` **F6: one key-derivation function.** scope_key / stage_role / model
  derived in exactly one place, used by both the evidence write path and the
  projection read path. The loop-closure invariant. Gate: pairing test between
  writer and reader.
- `[ ]` **F7: broad-first priors stated.** Global priors are the default
  operating mode; specificity is earned with evidence. Prevents the LN2 0/20
  stall (audit A2). Documented decision plus the fallback order in `learn/`.
- `[ ]` **F8: north-star metric defined.** Track efficiency and management
  burden as separate axes. Tokens, amortized total cost, latency, retries, and
  weighted interventions per kept successful task trend down. Deterministic
  success and kept-rate stay stable or improve. Wire the baseline into LX3a.

Routed from other tracks:

- `[ ]` **RR17** (from Track RR): `CollectStreamWithOptions` fires
  `OnUsageResult` and `OnUsage` independently when both are set
  (`internal/zeroruntime/helpers.go:122-126`). Nothing enforces the exclusion;
  a future caller setting both double-counts every request silently, poisoning
  cost priors. Fire one or state the exclusion next to the callbacks.
- `[ ]` **MD4** (from Track MD): topic-key identity contradiction. The
  revision lookup ignores `memory_type` but the comment claims type is part of
  the identity, so cross-type same-topic writes overwrite content and keep the
  old type. Include type in the key, or fail loudly on cross-type collision.
  Key-consistency family.
- `[ ]` **MD5** (from Track MD): macOS redundant daemon should exit cleanly as
  already-running instead of fataling with bind:EEXIST (socket hygiene).
  Evidence-layer availability.
- `[ ]` **S2c** (from Track S): the credential defaults forbid the unsandboxed
  retry during escalation. Asserted only in a comment in
  `internal/tools/bash_auto_allow_test.go` and pinned by no test. Same family
  as F1: behavior changed and nothing verifies it.
- `[ ]` **S2d** (from Track S, cleanup): `defaultDenyReadPaths`
  (`internal/sandbox/types.go`) hand-rolls `XDG_CONFIG_HOME` resolution that
  `os.UserConfigDir` already does, including a `filepath.Abs` fallback the XDG
  spec says to ignore. About 25 lines to remove.
- `[ ]` **PE7c** (from Track PE): full validation and manual real-provider
  acceptance. The measurement instrument must be trustworthy before learned
  policies are gated through it (F8's instrument).

Deliberately NOT foundation (stay in their tracks): RR13 (wall-time perf),
RR14 (escalation feature), RR16 (retention, deferred until observations
accumulate), RR18 (design-mode registry pairing, not in the learning path),
LN3/LN4 and LX1/LX2/LX4 (features that consume the foundation).

## Track DM: adaptive repair-loop demo slice (2026-08-19)

Source of truth: `plans/adaptive-harness-direction-2026-08-19.md` plus the
sequencing decision from the external design conversation (behavior -> events
-> TUI -> learning). Owner context: the goal is a sandboxed, truncated, LIVE
demo that proves the core adaptive premise to a non-technical audience.
One loop: test_runner reports a failure, the orchestrator routes a typed
`revision_request`, code_writer re-enters with focused evidence, patch v2, the
normal test command runs again, pass. No learning, authority, topology, or cost
optimization claim. Prereqs: F4 (incremental events) minimum; F1 for the
sandbox claim to be literally true.

- `[x]` **DM1: minimal StageMessage.** Typed `StageMessage{From, To, Kind,
  Evidence, Payload}` with `Validate()`; one kind only (`revision_request`).
  Added a new bounded field on `HarnessStageOutput`, which supplies the missing
  control-channel contract. The current demo route constructs the message in
  the orchestrator. Gate: `go test ./internal/splice/schemas/`. Shipped
  `0a6e638`, CI `32283467819`. Capped at 4 messages per stage output.
- `[x]` **DM2: orchestrator re-entry path.** `run.go`: when test_runner
  completes with failures and repair budget remains, route the revision to
  code_writer with focused context (failure evidence + changed files), then
  invoke test_runner with its normal discovered command. Bounded: max 2 repairs
  per run, charged against the run token budget (audit B5). The rest of the
  pass is untouched. Gate: `go test ./internal/splice/`. Shipped `f3ecfba`, CI
  `32286709616`. Re-invocations merge into the existing iteration record
  (replaceStageRecord) because applyRequestLedger keys usage by {name,
  iteration}. A second record would error the run. Exhaustion falls back to
  the existing trajectory behavior.
- `[x]` **DM3: events + trace.** `stage.message_sent` / `stage.reentered` /
  revision lifecycle events, written incrementally (F4) and into an
  interaction-shaped trace record. Pairing test pins producer and consumer.
  Gate: `go test ./internal/splice/`. Shipped `a3e7d90`, CI `32290274774`.
  `InteractionRecord{Message, Iteration, Repairs, Resolved, LatencyMs}`,
  one per repair sequence, capped at 20; carried through partial + final
  builds; `TestBuildRunOutcomeCoversEveryField` now guards drift.
- `[x]` **DM4: TUI interaction card.** Extended the existing pipeline panel
  with a read-only message card (from -> to, kind, resolution state). Shipped
  `195ab88`, CI `32292135967`. Gate: `go test ./internal/tui/`.
- `[ ]` **DM5: demo fixture and live acceptance.** The private fixture shipped
  in `5624f2b`; anti-rewrite task wording shipped in `785050d`. Remaining work:
  use a prompt that names `users/users.go`, produces TierLight, and excludes
  test_generator; hide the planted audit invariant from the first writer
  context; run matching dev `splice` and `splice-memd` binaries; prove the
  failure, message, re-entry, repair, test rerun, resolved trace, and completed
  run across repeated fresh worktrees. F1 remains required for a sandbox claim.

Done criterion: a live run of the loop on the fixture, in the TUI, in a
worktree, with the events in the trace. The demo is real: actual re-entry and
actual events. The planted trap is the only scripted part. This proves bounded
autonomous recovery, not learned cost reduction.

## Tracks T / W / A (v0.3 runway): configurable pipeline + native review surfaces (planned)

Source of truth: `plans/configurable-pipeline-and-review-surfaces-2026-07-23.md`
(recorded decisions 1-20, contracts, checkpoint contracts, UI performance
contract, open questions). Planned 2026-07-23 from an extended design
discussion; supersedes the "Future direction" note recorded 2026-07-16.
Target release: v0.3.x, long runway. Every Track T core checkpoint is zero
behavior change until T5/T6, so the runway can pause after any checkpoint
with the repo fully shippable.

Three tracks, one shared foundation:

- **Track T (configurable pipeline):** the pipeline becomes a user-editable
  topology (`pipeline.json`: nodes, typed edges with info-flow payloads,
  per-node model/effort/budget/capabilities). The default pipeline becomes
  an embedded topology in the same schema (byte-identical behavior, dogfood
  equivalence test). Custom `prompt` and `command` node types; command nodes
  run only through the sandboxed tool registry (commitment #8 honored by
  construction). Shareable files: user library, trust-gated project file,
  import consent flow. Tier envelope caps cost unless the topology declares
  `budget.total`.
- **Track W (web foundation):** one lazy local HTTP+SSE server in the splice
  binary (stdlib only, `127.0.0.1:0`, token URLs, `go:embed` assets, zero
  runtime JS dependencies). Serves both other tracks.
- **Track A (native review surfaces):** `/annotate` opens the crystallized
  `DesignPlan` in a browser surface; typed-anchor feedback returns to the
  design conversation as revision input. `/review` does the same for a run's
  diff before merge-back. Headless never blocks on a browser.

Runway order (v0.3.x): T1-T8 -> W1-W3 -> A1-A2 -> T9 -> A4/A3 -> T10 -> A5
optional. Checkpoints below are transcribed from plan section 8 (each lands
green, updates ROADMAP/MEMORY, commits, pushes, waits for CI; T2/T5/A2/T9/T10
are delegation candidates per the model routing table).

### Track T checkpoints: topology core (no UI)

Every T-core checkpoint is zero behavior change until T5/T6.

- `[ ]` **T1: topology schema.** `schemas/topology.go` (structs + `Validate()`
  per section 4.1: version rule, type/prompt/command consistency, budget vs
  `model_free` consistency, unique names, DAG). Table-driven tests naming
  each violation. Gate: `go test ./internal/splice/schemas/`.
- `[ ]` **T2: embedded default + compiler.** `topology.go`
  (`defaultTopology()`, tier filter, compile-time rules, topo order, budget
  resolution, `CompileTopology`); planner compiles the default. Dogfood test
  asserts the section 4.2 equivalence relation for every tier. Gate:
  `go test ./internal/splice/...`.
- `[ ]` **T3: executor honors the DAG.** `run.go`: topological schedule,
  edge-scoped `PriorSummaries` (summary/output/none), additive `ChangedFiles`
  on `HarnessStageOutput`, `(name, iteration)` stage-event keys. Default-graph
  equivalence + custom-graph tests (diamond, disconnected node). Gate:
  `go test ./internal/splice/... ./internal/tui/`.
- `[ ]` **T4: capability flags.** Node caps drive model-free / pull-context /
  memory gating; `isModelFreeStage` and the `stageOptions` name check deleted;
  the builtin profile map (4.1) is the default source. `StageTierLabels` and
  TUI enumerations read the active topology. Gate:
  `go test ./internal/splice/... ./internal/tui/`.
- `[ ]` **T5: custom node types.** `stages/prompt_stage.go` (one LLM call,
  bounded template context, no tools in v1) + `stages/command_stage.go` (one
  fixed shell tool, never arbitrary tool names); registry built from topology;
  the `commands.go` raw-exec fallback becomes an error (fail loud). Mock
  provider + mock `RecordCommand` tests prove the sandboxed path is the only
  path. Gate: `go test ./internal/splice/...`.
- `[ ]` **T6: loader, library, precedence, CLI verbs.** `topology_load.go`
  (snapshot-at-start), `internal/cli/pipeline.go` (`list`/`use`/`show`/
  `import`/`export`), trust-gated project file (loaded after trust resolution
  in exec.go and app.go), hardened import client (https-only, no cross-host
  redirects, 1 MB cap, timeout), `--pipeline` flag, the section 5 model
  ladder, `topology_name` into `PipelineResult` + eval CSV (round-trip test).
  Gate: `go test ./internal/splice/... ./internal/cli/ ./internal/agenteval/`.
- `[ ]` **T7: `splice config describe`.** Origin-annotated effective config,
  bounded to the section 5 key set. Gate: `go test ./internal/cli/`.
- `[ ]` **T8: trajectory adaptation.** Canonical `verification_report` key +
  legacy aliases, capability-aware collection in `ComputeIterationState`,
  state-hash fallback to verification findings, load-time + run-start notice
  for verification-free graphs. Gate: `go test ./internal/splice/...`.

### Track W checkpoints: web foundation (one local server, stdlib only)

- `[ ]` **W1: server + SSE hub + security baseline.** `internal/web/`: lazy
  server on `127.0.0.1:0`, Host-header validation, one-time URL token ->
  `SameSite=Strict` cookie, Origin checks on POST/SSE, CSP header, `go:embed`
  assets, SSE hub with coalescing, health endpoint, idle timeout + shutdown.
  Tests: every section 7 security row + the <100 KB asset budget. Gate:
  `go test ./internal/web/`.
- `[ ]` **W2: event wiring.** `agent.Options.EventSink`, redaction at the sink
  boundary (section 4.7), `--serve` on exec, TUI auto-start on first surface
  request. Gate: `go test ./internal/web/ ./internal/splice/ ./internal/cli/`.
- `[ ]` **W3: surface sessions.** Session-scoped tokens, `/new` `/resume`
  invalidation + `session-changed` broadcast, browser-open plumbing
  (interactive TTY only), per-session action tokens for privileged actions.
  Gate: `go test ./internal/web/ ./internal/tui/`.

### Track A checkpoints: native annotate/review surfaces

- `[ ]` **A1: annotation core (no UI).** `schemas/annotate.go`; the plan-family
  fix (persistent `planID`, real revision increments, `plan_approved`
  references the crystallized ID); new session event types + reconstruction
  arms; `DesignWorkflow.ReviseWithFeedback`; stale-version rejection. Gate:
  `go test ./internal/splice/... ./internal/tui/`.
- `[ ]` **A2: `/annotate` plan surface.** Server-rendered plan page (typed
  anchors), annotation islands, submit -> `AnnotationFeedback` -> TUI via the
  `RuntimeMessageSink` path (ID-validated `tea.Msg`; approve runs through
  `handleApproveCommand` guards). Gate:
  `go test ./internal/web/ ./internal/tui/ ./internal/splice/...`.
- `[ ]` **A3: plan revision diff.** Typed plan diff (v(N) vs v(N+1)), highlight
  in the surface on SSE revision push. Gate:
  `go test ./internal/web/ ./internal/splice/...`.
- `[ ]` **A4: `/review` diff surface.** Diff source = `PipelineResult.ChangedFiles`
  + `git diff` (never `FileTracker`); worktree runs gate `MergeBack` on the
  action token; non-worktree runs are post-hoc (no merge gate). Line anchors,
  server-side git rows. Headless never blocks. Gate:
  `go test ./internal/web/ ./internal/worktrees/ ./internal/tui/`.
- `[ ]` **A5 (optional, nod 18): mid-conversation annotate.** Last agent
  message as the document. Only after A2 lands.

### Track T surfaces (on W)

- `[ ]` **T9: pipeline viewer.** Read-only graph page: deterministic Go DAG
  layout, static SVG, live SSE class flips keyed by `(name, iteration)`, edge
  pulse on summary flow, node drawer, post-run timeline scrubber. Gate:
  `go test ./internal/web/`.
- `[ ]` **T10: topology editor.** Drag-drop nodes, edge wiring (server
  authoritative on cycle check), node config panel, autosave to a staging
  file with explicit publish, inline validation errors. Largest checkpoint;
  split at the canvas/drag seam first if the diff outgrows one review pass.
  Gate: `go test ./internal/web/`.

Standing consequences for current work (unchanged):

1. **Reinforce the default pipeline first.** Current tracks (hardening,
   cleanup, evals) remain the prerequisite.
2. **Do not hardwire speculative stage contracts.** Later can scaffold for
   itself (the AU6 test).
3. **Local-first still holds.** The GUI is local pages served by the Splice
   binary.

Each checkpoint starts only on user go-ahead and follows the cadence:
implement the approved slice, focused validation, ROADMAP/MEMORY update,
commit, push, wait for CI.

**Branch strategy (user-set, 2026-07-23):** all Tracks T/W/A implementation
lands on a single long-lived feature branch, `tracks-twa`, not on `main`.
Each checkpoint commits and pushes to `tracks-twa` and waits for its CI run
(the branch-CI run replaces main-CI as the checkpoint gate). The user tests
from the branch between checkpoints. Merge to `main` happens once, when the
user is satisfied, ahead of the v0.2 release. Docs-only process commits
(plan, ROADMAP, MEMORY) keep landing directly on `main` as before.

## Track R: pipeline reinforcement (2026-07-17)

Source of truth: `plans/pipeline-reinforcement-go-security-2026-07-17.md`. Harden
the default pipeline's deterministic floor. CP5 (LLM security advisor) is
tabled behind its eval contract; this track is the safe, deterministic
alternative. Each checkpoint lands green with focused tests, then commits,
pushes, and waits for CI.

- `[x]` **R1** (2026-07-17): Go security floor (gosec deterministic adapter).
  Extended the security floor from Python-only (Bandit) to Go via a `gosec`
  `VerificationCheck` adapter mirroring the F14c Bandit adapter. New
  `dtools/gosec.go` tool + `stages/security_gosec.go` check (filters `.go`
  internally, missing gosec -> incomplete, parses the `Issues[]` JSON with
  severity mapping and line-as-string/range parsing). The `security_auditor`
  stage is now multi-language (all-source discovery via `boundedSourceFiles`/
  `gitChangedSourceFiles`; the Python-only short-circuit removed; each check
  filters by extension, F14b-stated intent). Bandit gained an internal `.py`
  filter (behavior-preserving). Root-cause fix: the `[]string` vs `[]any`
  mismatch in the `RunTool` args path (a latent bug in BOTH adapters, never
  triggered for Bandit because Python repos short-circuited) fixed via a
  shared `toStringAny` helper. Go repos now get a real security signal
  instead of `VerificationIncomplete`. Dogfoods on Splice itself. Zero LLM
  tokens.
- `[x]` **R2** (2026-07-17): SARIF security layer. A generic
  SARIF-parsing `VerificationCheck` adapter: one Go parser, N scanners, zero
  per-language Go code. A new language becomes one config line + the scanner
  installed. Delivers JS/TS security coverage as a side effect (eslint via
  SARIF), and makes future languages config-driven additions. ADDITIVE to the
  hand-tuned Bandit/gosec adapters (which stay as proven defaults); the
  default scanner map covers JS/TS only (no `go`, to avoid duplicating gosec).
  New `dtools/sarif.go` (arbitrary-command runner, missing -> incomplete) +
  `stages/security_sarif.go` (generic SARIF v2.1.0 parser, level->severity
  map, nested message.text handling). CI-confirmed.
- `[x]` **R3** (2026-07-17): secret + dependency scanning via trivy
  (SARIF). A workspace-level `trivyCheck` that runs `trivy fs --format sarif
  --scanners vuln,secret` once on the workdir, reusing R2's shared SARIF
  parser. Covers two vulnerability classes the language lint scanners
  fundamentally miss: hardcoded credentials (any file) and known CVEs
  (dependency manifests). `Required: false` (additive augmentation, not the
  primary floor; missing trivy -> incomplete in the tool list but does not
  force the overall report status, per the F14 opt-in policy). Extracted
  `parseSarifResults`/`mapSarifFindings`/`isMissingScannerError` as shared
  helpers from `sarifCheck`. CI-confirmed.

## Track S: security hardening (2026-07-19)

Source of truth: the 2026-07-19 security audit + adversarial triage (see
MEMORY.md decision log). Eight parallel surface audits plus an adversarial
debunk loop over every finding. govulncheck is clean; the confirmed work
converges on three root causes: no workspace-trust concept, secrets crossing
the child-process boundary, and update supply-chain origin trust. Each
checkpoint lands green with focused tests, then commits, pushes, and waits
for CI.

- `[x]` **S0** (2026-07-20): npm package name fix. `internal/update/
  installmethod.go:14` had `npmPackageName = "@gitlawb/splice"` (rebrand
  leftover); `applyNpmUpdate` would `npm install -g @gitlawb/splice@latest`, a
  name the maintainer does not own. Fixed to `@taf0711/splice` + the one test
  asserting the value. Delegated to a deepseek-v4-flash worker; parent ran the
  gate. Internal commit `3a7ecac`, CI green. Ported to public via
  `git format-patch` as PR #5 (`fix/update-npm-package-name`).
- `[x]` **S1a** (2026-07-20): workspace trust gate (store + resolver + CLI
  wiring), modeled on pi's project trust. S1a-1: `internal/config/trust.go`
  (TrustStore, ~/.config/splice/trust.json, ancestor lookup) +
  `trust_resolve.go` (ResolveTrust: flag > env > store > setting) +
  `defaultProjectTrust` config setting. S1a-2a: `--trust`/`--no-trust` flags +
  `resolveWorkspaceTrust` helper + seams (hooks.DisableProject,
  tui Options.Trusted). S1a-2b: gate wired into app.go + exec.go startup
  before MCP/hooks/plugins load; untrusted skips project executables + warns
  loud; headless defaults untrusted under ask (fail-safe). S1a-2c: closed the
  last gate gap (`splice mcp tools list` was passing projectTrusted=true).
  E2E-verified: untrusted blocks project MCP spawn + hook; --trust spawns;
  persisted trust loads from store; --no-trust declines without persisting;
  defaultProjectTrust=always trusts. Closes the cloned-repo RCE Critical.
  Commits 36a96ff, 2bbf31c, 2e19a71, 3e5386f, cd756da.
- `[x]` **S1b** (completed 2026-08-03, `901e6fa`): interactive TUI trust prompt + `/trust` command (UX on top
  of the S1a gate; not security-critical). One-time prompt before session
  start when `defaultProjectTrust=ask` and no saved decision; `/trust`
  persists trust for the cwd + parent folder (restart required).
  The prompt landed 2026-08-01 (`8590900`, `internal/tui/trustprompt.go`):
  ASCII-only, inline rather than alt-screen, Ctrl+C declines. Verifying it by
  screenshot failed twice and both failures were the harness, not the prompt —
  Bubble Tea holds the first frame until the terminal answers its capability
  queries, so a control run of the ordinary TUI rendered nothing either.
  `/trust` and the footer marker landed 2026-08-03 in `901e6fa`. The marker
  appears only when the workspace is untrusted, since a permanent trusted chip
  teaches people to stop reading the line. `/trust` records the decision and
  says it takes effect on restart: this session already decided at startup
  whether to load project commands, hooks, MCP servers and plugins, so claiming
  the workspace is now trusted would be false.
- `[x]` **S2a** (2026-07-20): scrub credential env vars from child
  processes. `internal/secrets/env.go` `ScrubChildEnv` (exact list +
  `_API_KEY`/`_TOKEN`/`_SECRET`/... suffixes, `SPLICE_CHILD_ENV_ALLOWLIST`
  passthrough) applied at all 5 child-spawn sites (sandbox runner, hooks,
  MCP stdio, plugins, bash unsandboxed fallback). Closes the env-exfil High.
  Commit afd904d.
- `[x]` **S2b** (`cd3cf5f`): deny reads of credential paths by default. `DenyRead`
  was honoured by Landlock, Seatbelt, grep, glob and the agent loop, and set by
  nothing, while read roots include `/`. Every sandboxed command could read the
  user's secrets. True on macOS since the sandbox shipped, not new with the
  Linux fix. Ships with `sandbox.allowRead` to re-include a nested path, global
  config and CLI only. A denied SSH key stays denied when the network is
  approved, so SSH based Git needs the override; `docs/INSTALL.md` says so.
- **S2c / S2d** routed to Track FND (2026-08-19): sandbox substrate
  integrity (same family as F1).
- `[ ]` **F14b**: a deterministic stage failing from local tooling state fails
  the whole run. `TestRunHonorsMaxTurnsAsIterationCap` reports
  `pipeline status = failed, want aborted` because `security_auditor` fails;
  trivy is not even installed on the machine that reproduces it. Bisected across
  `bd75d05`, `5c699f7`, `97fa65f`, `102ff2f` and `4c750d9`, failing on all five
  while CI is green on all five, so it is environment dependent rather than a
  regression. `security_trivy.go:18` promises a missing scanner degrades to
  incomplete; something on that path does not hold. This is exactly F14's stated
  goal, that missing analysis is explicit rather than reported as clean.
- `[x]` **S3** (2026-07-20): sandbox decision hardening. TooComplex/
  unparseable shell commands now force `ActionPrompt` instead of auto-allowing
  under an active sandbox (`engine.go`). The safe-git classifier rejects
  `--git-dir`, `--work-tree`, `-c` (global and inline) so an approved prefix
  cannot operate on an arbitrary repository. Commit 54036e1.
- `[x]` **S4** (2026-07-20): medium cluster. dtools path resolver now
  calls `EvalSymlinks` and rejects symlinks pointing outside the workspace
  (deduped into a shared `resolve.go`); MCP config warns on plaintext
  `http://` URLs (non-loopback); opt-in seccomp unix-socket block fails
  closed (exit 125). Commit 3c9d266.
- Deferred (design judgment, not scheduled): OAuth store default to
  `encrypted-file` (needs a migration path for existing plaintext token
  files); update flow reject `http://` (could break loopback dev update
  servers).
- Deferred/accepted (recorded in MEMORY.md): memd socket peer-auth and
  PATH-resolution (no marginal risk vs same-user file access), write-tool
  TOCTOU/hardlink (active same-machine adversary, out of scope), plaintext
  user messages in events.jsonl (0600, shell-history precedent), OpenRouter
  OAuth `state` (PKCE + random port make it defense-in-depth), keyring `-w`
  argv exposure (no stdin alternative in the `security` CLI).

### Track S continuation: procrun process chokepoint (2026-08-21 to 2026-08-23)

Source of truth: `docs/audits/2026-08-21-security-egress-audit.md` (copied
from the public working tree; it catalogs internal attack surface and lives
here, never in the public repo). The audit proposes one audited funnel for
every child process: callers declare a profile id plus an optional binary
allowlist, and `internal/sandbox/procrun` enforces it through the existing
zero-sandbox engines, emitting one structured audit record per spawn.

Landed on public dev:
- Phase 0 plus Phase 1 (commit `846d6b3` and successors): all deterministic
  stage and dtools subprocesses route through procrun under fixed-binary
  allowlists with workspace-scoped network-deny engines.
- Slice A (`0499c11`): hooks dispatch and imageinput spawns emit audit records
  under behavior-preserving nil engines; the direct path honors explicit env
  verbatim so pre-existing scrubbing semantics could not drift silently.
- Eval harness hardening rode the same track (`9dcd56c`): per-run failures no
  longer abort a paired eval, and pairs persist incrementally.

Remaining, in order: keyring, LSP servers, and SPLICE_PROVIDER_COMMAND
migration (slice B, owner sign-off pending); env-scrub policy
(drop *KEY/*TOKEN/*SECRET from child env); enforce-mode profiles for the
slice-A sites. Confinement deliberately lags the audit funnel so parity tests
land before any behavior change.

## Upcoming (legacy)

- `[x]` **F8**: design runner + worktree lifecycle; turn planning output into
  isolated workspace execution with safe merge-back.
  - `[x]` **F8a** (2026-07-08): design runner. `RunDesignPlan` in
    `internal/splice/design_runner.go` sequences plan tasks topologically and
    runs each as an independent pipeline run; `splice exec --plan <path.json>`
    executes a design plan JSON file (strict decode, usage guards against
    `--use-spec`, `--file`, prompts, stream-json input, `--resume`, `--fork`).
    Plan-mode events documented in `docs/STREAM_JSON_PROTOCOL.md`.
  - `[x]` **F8b** (2026-07-08): worktree merge-back. `worktrees.MergeBack`
    commits the worktree's work, pins the `splice/<name>` recovery branch, and
    merges into the source repo with `--no-ff` behind safety guards (dirty
    source tree skips, conflicts abort, branch always survives). Opt-in via
    `splice exec --worktree --merge-back`; inherited `--worktree` behavior is
    unchanged without the flag. Per-agent commit stacks and worktree-based
    rollback (Python-era W3/W4) are deferred, see known gaps.
- `[x]` **F9**: memd Go client + sidecar integration for structured,
  searchable memory.
  - `[x]` **F9a** (2026-07-08): memd rebrand and CI. Module path
    `github.com/Taf0711/splice/memd`, binary `splice-memd`, env vars
    `SPLICE_MEMD_SOCKET` / `SPLICE_MEMD_DB`, data dir `.../splice`. Removed
    the committed `flug-memd` binary from git; added a dedicated memd CI job
    (vet, test, build) since the nested module is invisible to the root
    workflow.
  - `[x]` **F9b** (2026-07-09): Go sidecar client in `internal/memd`. `client.go`
    implements `Health`, `Upsert`, `Search`, `MarkReviewed`, plus `Resolve`
    auto-spawn (env/PATH/dev-checkout binary resolution) and no-op degrade
    when no binary resolves. Tests in `internal/memd/client_test.go` cover all
    four endpoints, the include-flags wire contract, and `resolveBinary`, over
    a Unix-socket httptest harness. Contract fix landed with the client:
    `schemas.MemoryQuery.IncludePrivate`/`IncludeShareable` changed `bool` →
    `*bool` with `omitempty` so a zero-value query omits the flags (server
    defaults to true) instead of silently sending `false` and returning zero
    results.
  - `[x]` **F9c** (2026-07-09): orchestrator retrieval injection. Wire the
    memory client into `splicerun.Run`/`RunDesignPlan` via a nilable
    `MemoryRetriever` interface (Search only; `*memd.Client` satisfies it
    implicitly, so `run.go` never imports `internal/memd`). At the `runPass`
    stage-input build site, build a bounded `MemoryQuery` (owner_agent=stage
    name, query=first 200 runes of the distilled request intent,
    project_path=work dir, limit=5) and inject the returned `MemoryBundle`
    onto `HarnessStageInput`. nil means memory off (no injection, no error,
    byte-identical). exec resolves the client once via `memd.Resolve` and
    degrades with a warning when no binary resolves or the daemon is
    unreachable; memory is never load-bearing.
  - `[x]` **F9d** (2026-07-09): orchestrator deterministic writes
    (mechanism + discovered test command). Evolve the nilable interface
    `MemoryRetriever` → `MemoryStore` (add `Upsert`; `*memd.Client`
    satisfies both). After a stage completes, `runPass` calls
    `extractWriteObservations` and persists each non-fatally (memory writes
    are never load-bearing). The one write in this checkpoint is the
    discovered test command: `test_runner` surfaces `cmd` in its output
    `Data`, and the orchestrator persists a `shareable`
    `MemoryObservation` (`memory_type=test_command`,
    `topic_key=test_command`, `owner_agent=orchestrator`,
    `project_path=workDir`, `source_run_id`/`source_stage` set) so the
    sidecar's topic_key upsert updates rather than stacks. exec's
    nil-interface handling is unchanged.
  - `[x]` **F9e** (2026-07-09): remaining deterministic write categories
    (config observations + tool-degradation events per design doc M7). At
    run start `runExecutionPlan` persists one per-project `run_config`
    observation (`topic_key=run_config` so it updates in place, not
    stacks; content is tier+stages shape only, never the raw intent). In
    `runStageWithContext`, after `FulfillContextRequest` returns, errored
    `ContextItem`s (e.g. the v1-deferred `get_symbol`) become private
    `tool_degradation` observations (`topic_key=tool_degradation:<query>`).
    All writes are non-fatal and gated by the nilable `MemoryStore`. The
    tool-not-found / permission-denied degradation from the
    `RegistryToolRunner` path is deferred (it flows through the agent-loop
    tool machinery, not the orchestrator's `extract*` seam; needs a write
    hook threaded into the tool runner) and recorded under known gaps.
- `[x]` **F10** (2026-07-09): eval harness + honest cost/token evidence.
  Extended Zero's `internal/agenteval/` harness to capture cost/tokens/latency
  alongside the existing pass/fail scoring, taking the best of Zero's
  task-scoring harness and pi-bench's cost/token/latency capture. Extended
  `streamjson.Event` with `cachedInputTokens`, `cacheWriteTokens`,
  `reasoningTokens` (optional, backward-compatible) so the stream-json usage
  event carries the full token breakdown. The harness parses usage events from
  agent stdout post-hoc (zero overhead on the run), computes cost via
  `modelregistry.Registry.EstimateCost`, and outputs a CSV (taskId, model,
  status, pass, inputTokens, outputTokens, cachedInputTokens, costUSD,
  latencyMs) via `--csv-output`. `BenchmarkSummary` reports
  `MeanCostPerPassedTask` (cost per solved task, not cost per attempted). No
  extra LLM calls, no extra tool calls, no extra stages.
- `[x]` **F11a** (2026-07-18): release infrastructure. Closed the stale
  v1.0.0 Release Please PR (#1), deleted upstream Zero-era tags (v0.1.0,
  v0.2.0) that predated the Splice rebrand, bootstrapped
  `.release-please-manifest.json` at `0.0.0` so Splice's own versioning
  starts at v0.1.0 (feat: -> minor in 0.x mode). Added
  `release-artifacts.yml`: triggers on release publication, cross-compiles
  `splice` + `splice-memd` (both pure-Go, `CGO_ENABLED=0`) for 6
  platform/arch combos (linux/macos/windows, x64/arm64), creates
  `.tar.gz`/`.zip` archives with SHA-256 checksums, uploads as release
  assets. Updated `package.json` to 0.1.0 and `docs/INSTALL.md` status.
- `[x]` **F11b** (2026-07-19): docs refresh. Reframed from the stale "migration
  guide from Zero" framing (the public audience is new users, not Zero switchers)
  to first-run/onboarding docs. INSTALL.md stale "first release will be cut"
  status note dropped (v0.1.1 is released: GitHub Releases + npm via OIDC).
  README First Run (EN + ZH) gained a "Your first prompt: planning vs execution"
  section explaining the two-phase workflow (design mode default, /crystallize,
  /approve, /exec escape hatch, /design re-entry). CP4 made planning the
   default entry, so a new user's first prompt landed in a design conversation
  with no explanation; this closes that onboarding gap. The Zero-era `docs/`
  page sweep is deferred (low reader value; the public repo no longer carries
  `docs/flug-design/` or `ROADMAP.md`, and `AGENTS.md` is contributor-facing).
- `[x]` **F12a** (2026-07-09): structured stage events via OnReasoning
  marker. `emitStageEvent` in `run.go` embeds a null-delimited JSON marker
  (`\x00STAGE{...}\x00`) in the OnReasoning stream at each stage lifecycle
  point (started/running/completed/failed/skipped). Avoids adding a new
  callback to `agent.Options` (upstream Zero, do not modify).
- `[x]` **F12b** (2026-07-10): PIPELINE sidebar section + conditional
  swap. A `pipelinePanelState` view model (`pipeline_panel.go`) renders a
  vertical stage list with status glyphs (`✓`/`●`/`○`/`✗`/`↩`), a CURRENT
  detail block with a progress bar, and changed-files. Added to the existing
  `renderContextSidebar` between PLAN and FILES (additive). The TUI
  conditionally swaps `agent.Run` → `splicerun.Run` (not for spec-draft);
  wires `memd.Resolve`; parses stage markers from `OnReasoning`; and formats
  the `PipelineResult` as a one-line summary instead of raw JSON.
- `[x]` **F12c** (2026-07-11): per-stage model/tier/effort wizard. A new
  `/stages` TUI command opens an interactive overlay that lets the user view
  and edit `~/.config/splice/stage-models.json` (the per-stage model routing
  config that AR11 made loadable). The wizard loads the existing config (or
  seeds the default from the active provider profile), shows the default,
  optional escalation, and each pipeline stage with its current override, and
  lets the user edit provider/model/effort per target or remove overrides. The
  target editor follows inherited Zero menu behavior: Up/Down traverses rows,
  Enter opens provider/model/effort list pickers, the model picker supports the
  same ranked type-to-search behavior as `/model`, and an explicit Apply row
  returns the draft to the overview. A key-driven feature test covers the full
  command-to-render-to-disk flow. Changes are saved from the overview as
  validated JSON (mode 0o600) and take effect on the next pipeline run. Tier
  defaults are not included (the config schema has no tier field); the wizard
  is stage-model config only.
- `[x]` **F12d** (2026-07-12): shared stage-model routing for exec and TUI.
  Resolver construction now lives in `internal/splice/model_routing.go` with
  lazy per-run provider caching. Headless exec uses the shared helper, and
  each non-spec TUI prompt reloads `stage-models.json` before invoking
  `splicerun.Run`, so `/stages` changes affect the next prompt without a
  restart. Invalid config is shown in the transcript and falls back to default
  routing. This also fixes the existing default-route defect: a valid
  `default` entry applies to stages without overrides; an absent zero config
  remains a no-op.
- `[x]` **F12e** (2026-07-12): real Bubble Tea pipeline feature test. The
  test submits a normal prompt through `model.Update`, executes the production
  `splicerun.Run`, feeds callback messages back through `Update`, and verifies
  completed sidebar stages, formatted final output, raw session result,
  generated files, nil recovery authority, and selected local backend routing.
  The existing spec-draft feature test continues to prove `submit_spec` is
  advertised while mutating tools are absent.
- `[x]` **F12f** (2026-07-12): pipeline prompt contract tests. Request-level
  assertions now prove code writer, test generator, static analyzer, plan
  critic, and step-back each receive `pipeline_meta.md` exactly once in their
  system prompt. No outer-agent prompt text was added because executable Go
  routing, not model instructions, enforces pipeline use.
- `[x]` **F13** (2026-07-12): Ollama and LM Studio typed-output hardening.
  LLM-backed stages retry missing, malformed, or schema-invalid required tool
  output at most twice with bounded corrective feedback. Transport errors,
  cancellation, and deterministic application failures do not retry. Usage is
  summed across attempts, including exhausted failures in `StageRecord`s.
  F14a later removes the optional static-analysis model path. Exhaustion names the model/tool and the
  local tool-calling requirement. A real keyless OpenAI-compatible HTTP test
  proves missing-tool recovery and absence of an Authorization header. The
  local model picker now distinguishes an unavailable runtime from a runtime
  with no loaded models. There is no cloud fallback.
- `[x]` **F14**: fast deterministic verification. Approved plan:
  `plans/fast-deterministic-verification-2026-07-12.md`. All children
  (F14a/F14b/F14c) shipped 2026-07-13; parent checkbox was stale.
  - `[x]` **F14a** (2026-07-13): make deterministic stages model-free. Removed
    static-analysis LLM interpretation and its prompt, skipped model resolution
    and attribution for static analysis, security audit, and test execution,
    assigned those stages zero token budgets, and limited `/stages` built-in
    targets to the model-backed code writer and test generator. Reserved hidden
    JSON entries remain loadable and are preserved on save; unknown extension
    rows remain editable.
  - `[x]` **F14b** (2026-07-13): add the typed verification report and modular check seam.
    Replaced `StaticAnalyzerOutput`/`StaticIssue` with `VerificationReport`/
    `VerificationFinding` across stages, trajectory, orchestrator, and TUI.
    Added modular `VerificationCheck` adapters (go syntax, python syntax, bandit)
    and a pure report aggregator that normalizes, sorts, deduplicates, and derives
    stable SHA-256 fingerprints. Distinguishes passed, findings, incomplete, and
    not-applicable. Missing coverage is surfaced once via `StageIncomplete`
    without triggering another iteration. High/critical findings block completion
    and flow back to the next code-writer revision as bounded evidence.
  - `[x]` **F14c** (2026-07-13): tighten fast local checker profiles. Added
    go/format equivalence to the Go adapter, batched Python py_compile into one
    process with optional Ruff, added JavaScript `node --check` and TypeScript
    `tsc --noEmit` adapters, detected TypeScript before JavaScript, sorted
    paths for stable fingerprints, added 30-second subprocess timeouts, and made
    non-Python security stages report incomplete instead of clean.

## Track AR: 2026-07-10 audit remediation

Source of truth: `plans/audit-remediation-2026-07-10.md`. This track precedes
F12c. Each item is a separate green-to-green checkpoint.

- `[x]` **AR0**: patch root and memd Go toolchain security pins.
- `[x]` **AR1**: make pipeline file application correct, cancellable, and
  fail-loud for create, modify, delete, and permission denial.
- `[x]` **AR2**: remove implicit current-project memd binary execution.
- `[x]` **AR3** (2026-07-10): split memd spawning by platform and restore
  Windows builds. Extracted `configureSpawn` from `spawnDaemon` into
  build-tagged `internal/memd/spawn_unix.go` (`Setsid`) and
  `internal/memd/spawn_windows.go` (`CREATE_NEW_PROCESS_GROUP`). CI now
  cross-vets internal/memd on Windows and cross-builds `cmd/splice` for
  Windows amd64 and Linux amd64.
- `[x]` **AR4** (2026-07-10): secure first-run memd directory and database modes.
- `[x]` **AR5** (2026-07-10): route deterministic test execution through the
  safety substrate. `test_runner` now invokes the registered `bash` tool when
  `RunTool` is configured, honoring permission mode, sandbox, and cancellation.
  `RecordCommand` remains observability-only wrapping. `ToolResult` gained a
  `Meta` field so exit status flows from the tool result back to the stage.
- `[x]` **AR6** (2026-07-10): register and harden deterministic Bandit execution.
- `[x]` **AR7** (2026-07-10): make scoped memory retrieval affect typed stage
  input. Added a bounded `SelectedMemory` field to `CodeWriterInput` and
  `TestGeneratorInput`, wired the consuming stages to map from `MemoryBundle` via
  `selectMemory`, fixed `newMemoryQuery` to scope retrieval to `project` and `global`,
  updated the stage prompts with guidance on using the memory field, and added
  focused tests for selection, truncation, payload presence/absence, and the full
  orchestrator-to-stage flow.
- `[x]` **AR8** (2026-07-10): record per-stage usage (input, output, cached,
  cache-write, cost, latency) in StageRecord, sum into PipelineResult totals,
  and merge context-fulfillment usage so retries are accounted exactly once.
- `[x]` **AR9** (2026-07-10): enforce execution-plan DAG and stage-output
  validation. `ExecutionPlan.Validate` rejects dependency cycles; `runPass`
  validates harness input/output before marking completed; `runExecutionPlan`
  validates the final `PipelineResult`.
- `[x]` **AR10** (Option A chosen): real trajectory recovery.
  - `[x]` **AR10a** (2026-07-10): restore a regressed isolated worktree. The approved contract
    in `plans/audit-remediation-2026-07-10.md` adds a Splice-owned
    `WorkspaceRecovery` seam, captures git-plumbing snapshots without touching
    the real index or `HEAD`, restores the highest-scoring prior iteration only
    when exec explicitly prepared `--worktree`, and aborts without further
    mutation when recovery is unavailable or fails. In-place exec and TUI runs
    never receive destructive git authority.
  - `[x]` **AR10b** (2026-07-11): fresh step-back analysis on plateau. Added a
    typed `StepBackAnalysis` schema and a `StepBack` stage function that makes
    a single-turn `submit_step_back` tool-use call on a compressed report
    (distilled intent, last 3 scores, failing tests, changed files, plateau
    reason). When `EvaluateTrajectory` returns `ActionStepBack`, the
    orchestrator calls `stages.StepBack(...)` and replaces the
    iteration-history dump in revision context with the hypothesis, so the
    next code_writer sees a reframed problem. Provider errors propagate as
    pipeline failures (stop, do not retry). It is orchestrator-level, not a
    registered pipeline stage. One medium-tier LLM call per step-back
    decision; only fires on plateau (3+ iterations without improvement).
  - `[x]` **AR10c** (2026-07-11): model escalation for cycle and oscillation
    actions. Added `EscalationModelResolver` to `agent.Options` and an
    optional `"escalation"` entry to `stage-models.json`. When
    `ActionEscalateCycleDetected` or `ActionEscalateOscillation` fires, the
    orchestrator calls the resolver at most once per run and swaps the default
    provider/model/effort for subsequent iterations. Best-effort and non-fatal:
    nil resolver, nil provider, or error emits a progress note and continues.
    Per-stage `StageModelResolver` overrides still take precedence.
  - `[x]` **AR10d** (2026-07-11): a real user-intervention boundary for
    `ActionSurfaceToUser` rather than another retry. Added `OnSurfaceToUser`
    callback on `agent.Options` with typed `SurfaceToUserRequest`/`SurfaceToUserDecision`.
    When the callback is nil (headless), the pipeline aborts with a clear message.
    When wired, the user can continue (with guidance that becomes revision
    context) or abort. Removed the now-empty `isRecoveryAction` function.
- `[x]` **AR11a** (2026-07-10): stage model config schema + JSON loading
  (`internal/splice/schemas/stage_model.go`). Maps stage names to
  `{provider_profile, model, reasoning_effort}` with a default fallback.
  Absent file is a graceful no-op.
- `[x]` **AR11b** (2026-07-10): wire the `StageModelResolver` into exec.
  `internal/cli/exec.go` loads `stage-models.json` next to the user config,
  builds a resolver that maps per-stage entries to cloned provider profiles,
  caches constructed providers, and sets `runOptions.StageModelResolver`. Missing
  or invalid config files are non-fatal; the orchestrator falls back to the
  default provider when the resolver returns nil.
- `[x]` **AR11c** (2026-07-10): multi-model system prompt. Added
  `internal/splice/stages/prompts/pipeline_meta.md` with a shared system prompt
  that explains the multi-model pipeline architecture and the typed
  input/output contract; embedded it in `provider.go` and prepended it to each
  LLM-backed stage's own prompt via `composeSystemPrompt`.
- `[x]` **AR11d** (2026-07-10): `StageRecord` records which model/provider
  was used. The `Model` and `Provider` fields (already on the struct, previously
  nil) are now populated from the resolved per-stage model.
- `[ ]` **AR12**: reconcile release, protocol/TUI, packaging, and canonical
  documentation findings as separately reviewable slices.
  - `[x]` **AR12a** (2026-07-11): canonical docs and metadata reconciliation.
    Fixed AGENTS.md status (through F9e/F10/F12b/AR0-AR10d), ROADMAP F9 parent
    checkbox, BENCHMARK self-correct note, STREAM_JSON stage marker description,
    install script names (Zero -> Splice), NPM_WRAPPER_SMOKE env vars,
    package.json description, CodeRabbit config (Go paths), and narrowed the
    no-em-dash rule to new/edited text with flug-design exemption.
  - `[x]` **AR12b** (2026-07-11): CLI correctness. Reject
    `--use-spec --merge-back` at parse time (A-15); route worktree prepare
    and merge-back through the signal-aware run context (A-17); make
    merge-back conflict/dirty outcomes exit non-zero (A-22).
  - `[x]` **AR12c** (2026-07-11): TUI and protocol polish. Sidebar shows
    during pipeline runs (A-13); changedFiles populated in stage events and
    displayed in the PIPELINE panel (A-14); spec-draft exec mirrors
    OnReasoning/OnToolCallStart/OnToolCallDelta callbacks (A-16). A-23
    (TUI history raw result) deferred to AR12c-2.
  - `[x]` **AR12c-2** (2026-07-11): TUI history raw pipeline result (A-23).
    Session events store raw PipelineResult JSON instead of the formatted
    one-line summary; formatting applied at render time (both live and on
    resume).
  - `[x]` **AR12d** (2026-07-11): memd robustness. Semantic request
    validation at the daemon boundary (A-24) and HTTP resource limits:
    server timeouts, MaxHeaderBytes, MaxBytesReader, client non-2xx status
    check (A-25).
  - `[x]` **AR12e** (2026-07-11): packaging and release metadata.
    A-04, A-27, A-30, A-32 (partial, earlier); A-28 (Node >=24), A-29 (drop
    Android), A-31 (Release Please workflow). All audit findings now
    addressed.

## Track DG: design-phase diagrams (2026-07-21)

Terminal-rendered architecture diagrams for the design phase. Shaped by an
adversarial plan review (16 findings) that killed the original design (a
default-on background preview stage: O(turns^2) token cost, the claimed
light tier does not exist, the transcript is append-only, and the file-tree
adapter had no data source because `Task.TargetPaths` is never populated).

- `[x]` **DG0**: `design_conversation.md` prompt teaches the agent to draw
  small ASCII diagrams (box-drawing, <=70 columns) in fenced blocks when
  architecture comes up. The transcript already renders fenced blocks
  preformatted, so the conversation-phase feature is prompt-only (the
  Claude Code model: the model draws, the terminal shows it).
- `[x]` **DG1**: `schemas.Diagram` (flow/sequence kinds, `Validate()` naming
  offending data) + deterministic ASCII renderer (`RenderDiagram`, layered
  flow layout, actor-column sequence layout, width < 58 or overflow degrades
  to an indented list) + `TaskGraphFromPlan` adapter over `Tasks[].DependsOn`
  + seam in `persistentPlanHeader` only (View-time surface with real width;
  static transcript rows rewrap and would mangle box art). Zero tokens at
  render time.
- `[ ]` **DG2 (optional upgrade path, not scheduled)**: if model-drawn ASCII
  quality disappoints on weaker providers, have the conversation agent emit
  the DG1 node/edge structure instead of drawing, and route it through
  `RenderDiagram`. The UX does not change, only who draws the boxes.
- `[ ]` **DG3 (@needs-human)**: crystallizer emits 1-3 diagrams persisted as
  `DesignPlan.Diagrams`. Requires the AU5-documented lockstep edit (struct +
  `designPlanToolDefinition` + prompt + `Validate`). This is the
  consumption-wiring re-add of the AU5-deleted `SequenceDiagrams`/
  `Wireframes` fields; it must own the per-crystallization output-token cost
  honestly (only DG1's adapters are zero-token).

## Deferred / known gaps

- **Windows specialist grandchild process leak** (`@needs-human`, cannot be
  verified from macOS without a Windows CI runner): `hardenSpecialistChild`
  (`internal/specialist/exec_proc_windows.go`) kills only the direct child
  process on cancel/timeout, because Windows has no equivalent to a POSIX
  process group. The Unix counterpart (`exec_proc_unix.go`) puts the child
  in its own process group and kills the whole group. A leaked grandchild
  (a build tool or shell the specialist spawned) survives on Windows;
  `WaitDelay` (2s) only stops Splice itself from hanging on the leak, it
  does not reap it. Documented in `docs/SPECIALISTS.md`. Fix: process
  termination through a Windows Job Object (`CreateJobObject` +
  `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`), as its own checkpoint with
  Windows-runner CI coverage.
- **Per-agent commit stacks and manual recovery UX**: AR10a lands
  iteration-level snapshot and restore for trajectory rollback in an explicit
  `--worktree`. Per-agent commits, user-invoked iteration diff/rollback commands,
  and snapshot-ref pruning from the Python-era W3/W4 design remain deferred.
  Port them deliberately if per-stage provenance or manual recovery becomes a
  checkpoint.
- **TUI + spec-draft pipeline wiring**: F12a through F12f are landed. The
  TUI runs `splicerun.Run` for non-spec-draft runs, shows pipeline progress,
  and reloads `/stages` routing for the next prompt. F12e provides the real
  Bubble Tea pipeline feature test, and F12f pins the pipeline prompt contract.
  The spec-draft flow still uses `agent.Run` by design.
- **Mid-run escalation is inert under pipeline exec**: escalation lives inside
  `agent.Run`'s loop, so `--allow-escalation` has no effect on pipeline runs.
  Whether F-Zero supports live model escalation, escalation-on-failure, or
  staged fallback is an open product decision.
- **First-class `stage` protocol event type**: pipeline stage lifecycle
  currently rides `reasoning` events and stage output streams as
  `tool_call_start` / `tool_call_delta`. A dedicated `stage` event would be
  clearer for protocol consumers.
- **`ZERO_` env-var prefix**: still read by code and documented with
  transition notes; the rename to `SPLICE_` is deferred.
- **npm / install-script releases**: nothing is published yet; building from
  source is the only working install path. GitHub Releases and the npm
  wrapper are planned.
- **Reasoning-event verbosity**: the pipeline forwards every stage progress
  line via `OnReasoning` (~400 events for a small run); damping or batching is
  not yet implemented.
- **Unwired port remnants**: a `StageUsageMeter` and `SemanticCache` from the
  Flug orchestrator port were never wired into `splicerun.Run` (quarantined
  out of the tree 2026-07-08); re-introduce deliberately if per-stage usage
  metering or output caching becomes a checkpoint.
- **Tool-runner degradation writes**: F9e persists `tool_degradation`
  observations only from errored `ContextItem`s (the deterministic context
  path). Tool-not-found and permission-denied results from the
  `RegistryToolRunner` path are NOT persisted; they flow through the
  agent-loop tool machinery, not the orchestrator's `extract*` seam, so
  capturing them needs a write hook threaded into the tool runner. Port it
  deliberately if cross-run recall of tool failures becomes a checkpoint.
- **Broader deterministic security profiles**: F14 keeps the hot path local
  and bounded. Multi-language SAST, secret scanning, and dependency scanning
  remain later opt-in `VerificationCheck` adapters that must define database
  freshness, scan scope, installation policy, and measured latency budgets
  before becoming default.
- **Optional LLM security advisor**: F14 preserves an immutable typed
  `VerificationReport` boundary for a future `security_advisor` model stage.
  It may annotate findings or propose advisory candidates, but it cannot
  replace, suppress, or downgrade deterministic evidence. It becomes a
  `/stages` target only after the orchestrator wiring exists.

## Track PX: TUI performance and responsiveness (2026-07-24)

Measurement-driven performance work for the interactive TUI. Symptom: the
"agent thinking" animation and scrolling feel janky, worse on long sessions.
Shaped by a fresh-context adversarial review (verdict: directionally right,
three holes closed below) and by the frame-consistency principle below.

Design principle (research-grounded: Ratatui, ncurses, and Bubble Tea v2 all
converge on it): View() produces the COMPLETE logical frame every tick; the
renderer cell-diffs and flushes with synchronized output (Mode 2026, on by
default in bubbletea v2.0.7, supported by Ghostty). Jitter comes from
inconsistent or dropped frames, not from full redraws. So: no app-level
partial painting tricks; make the full frame cheap instead. Verified root
causes, ranked by evidence:

1. O(n)-per-frame cost growth in alt-screen. `settleTranscript` no-ops in
   alt-screen (`flush.go`), so `transcriptBodyItems` iterates every row and
   `buildRowContext` scans the whole transcript on every frame. Rendering is
   viewport-gated and per-row cached; the uncached cost is the items
   construction loop plus the context scan.
2. `agentReasoningMsg` is not coalesced (`coalesce.go` only batches
   `agentTextMsg`), so each reasoning delta fires a full Update+View at
   provider speed during thinking.
3. Scroll-path throttles and glides (`mouseEventThrottleInterval`,
   `dragEdgeScrollTickMsg`) are uninvestigated; PX0 data decides if they
   matter after PX1.

- `[x]` **PX0** (2026-07-24): instrumentation scaffold. `internal/tui/perf_metrics.go`
  ring buffer of View()/Update() durations (single-goroutine eventLoop, no
  lock needed; verified against bubbletea v2.0.7), always-on, surfaced via
  the existing `/debug` command together with the already-collected-but-
  never-read `renderCacheStats`. Records frame times during scroll bursts
  and streaming, not per-section attribution (that comes from the PX1
  benchmark). Added Frames/Render cache/Transcript sections to `/debug`;
  hooks are two deferred lines at the top of View() and Update().
- `[x]` **PX1** (2026-07-24): settled alt-screen transcript snapshot. Manual
  port of Zero `d74ceb1` #647 to Splice's diverged code (not a cherry-pick;
  ~195-line divergence in session_controls.go). Delegated to a fresh-context
  `impl-worker` subagent with the full spec; parent verified the gate and all
  four review-closed holes in the diff. Caches the assembled settled body so
  View() re-renders only the live tail. The four holes: (a)
  `transcriptRenderInteraction` (transcript_selection.go:46) — shared pointer
  allocated in newModel, published via .set() in transcriptBodyItems (line 237),
  read via .get() in the snapshot builder (line 190), so settled-row closures
  read LIVE selection/hover; (b) collapseRepeatedStatusCard invalidates at
  model.go:2442; (c) invalidation is frontier/width (flush.go:128:
  `m.flushed != oldFlushed || m.altScreenSettledWidth != bodyWidth ||
  m.altScreenSettledFrontier != m.flushed`), NOT row count; (d) the alt-screen
  no-op is removed from settleTranscript — m.flushed advances in alt-screen,\  all reads are mode-agnostic. Benchmark `transcript_issue561_bench_test.go`:
  50/500/5000 turns at 792/787/822 ns/op (flat, 0 allocs). Full tui suite
  green. startup.go untouched (only the pre-existing welcome tile).
- `[x]` **PX2** (2026-07-24): coalesce `agentReasoningMsg` like `agentTextMsg`.
  Extended `textCoalescer` (coalesce.go) with a `kind` tag (text/reasoning)
  on the single buffer; a kind-switch or runID-switch flushes first, preserving
  arrival order. `drainAndForwardLocked` forwards the right message type. The
  consumer side (model.go agentReasoningMsg case) is unchanged. 4 new tests;
  full tui suite green. Delegated to impl-worker; parent verified the gate
  and the diff.
- `[x]` **PX3**: scroll-path investigation (mouse throttle, drag-edge
  glide, two-column `joinColumns`/`viewLines`). **PX3-inst** (2026-07-24,
  landed): the gate was not readable because perf_metrics did not tag
  frames by trigger. Added per-msg-kind frame tagging (`tagForMsg` +
  `byTag` rings + `perf.lastTag` stashed in Update) and a "Frames by
  trigger" section in `/debug` (worst view-p95 first). No scroll-behavior
  change. **PX3 gate read (2026-07-25):** a live session showed scroll
  view p95 3.1ms / max 4.8ms (second-lowest trigger), so the narrow scroll
  gate was unmet — but "Track PX closes" was premature: the user reported
  the real symptom (slight lag / scroll feels slow), which the aggregate
  frame data did not capture. **Root cause:** the mouse throttle dropped
  every `MouseWheelMsg` <15ms after the last; on a trackpad (momentum
  events every ~4-8ms) the filter starved scroll input before it reached
  Update. The cheap p95 measured only rendered frames; dropped deltas
  never became frames. **Fix:** stop throttling wheel, keep throttling
  motion (motion is idempotent, wheel is lossy). Terminal synchronized-
  output (Mode 2026) already bounds the visible redraw rate. Feel is the
  user's test (not verifiable headless). Risk: a fast trackpad swipe may now
  jump far (5 lines/event); step-size tuning is the follow-up if it
  surfaces as too fast.

**2026-08-23 update (merged `c1dcbe0`):** a fresh-context audit found two more
root causes and fixed them. First, the transcript body height cache capped at
512 entries but keyed on full render fingerprints, so any session over ~512
body items thrashed the cache and re-rendered the entire transcript every
spinner frame (~23.5ms and 4.9MB allocs per frame at 600 rows, measured).
Fix: 4096-entry cap plus a 4MiB retained-key-character budget; measured
3.0ms per frame after (7.8x). Second, counter reflow jitter survived in five
surfaces beyond the pipeline strip; consolidated into formatDoneTotal with
regression tests pinning the 9-to-10 crossing. Left open on purpose:
streaming markdown re-parses per document per frame (fits budget today;
incremental rendering risks visual drift and needs its own reviewed slice),
and TestHandleAddDirCommand fails on deep macOS temp paths at pristine HEAD
(pre-existing, environmental).

## Track WG: wiring gaps (2026-07-26)

Source: a directed dependency graph over all 1,182 first-party Go files
(graphify, local analysis, not checked in), cross-checked against both
execution paths. Every item here is a contract with a reader and no writer,
a config that resolves but never reaches its consumer, or code that ships
dead while its tests pass — none of it visible to a green build or green CI.

- `[x]` **WG1**: `internal/splice/dtools/resolve.go` resolved the *target*
  path through symlinks but never the *root*, so `Rel(unresolvedRoot,
  resolvedTarget)` produced `../../../private/var/...` and rejected every
  file as "escapes workspace" on any workspace path traversing a symlink
  (macOS `/tmp`, `/var`; symlinked repo dirs on any platform). This broke
  the entire deterministic security floor: `gosec`, `bandit`, and `sarif`
  all route through the shared resolver (`DefaultSecurityChecks()` is
  `{bandit, gosec, sarif, trivy}`). It was also the root cause of the
  standing red test `TestRunHonorsMaxTurnsAsIterationCap`: rejected path ->
  `stages/security_gosec.go:67` default branch turns a non-OK tool result
  into a stage-killing error -> `run.go:168` returns `failed` instead of
  reaching the abort path at `run.go:304`. CI could not see this: both
  GitHub Actions jobs run `ubuntu-latest`, where `/tmp` is a real directory
  and the namespace mismatch never reproduces. Fix: `filepath.EvalSymlinks`
  on the root after `filepath.Abs`, mirroring the already-symmetric
  `internal/tools/workspace.go:43`. New regression tests build an explicit
  `os.Symlink` root rather than relying on the platform temp dir, so they
  reproduce on Linux CI too.

- `[x]` **WG2**: `internal/cli/exec_spec.go` (`splice exec --spec`, the only
  headless path that runs a real `agent.Run` loop) never read
  `resolved.Compaction`, so a user's compaction config had no effect there
  even though the interactive TUI already honored it
  (`tui/model.go:4816-4823`). The default `splice exec` path is unaffected:
  it runs `splicerun.Run`, and `internal/splice` reads `ContextWindow` zero
  times, so compaction is inert there *by architecture*, not by omission
  (WG3 documents that explicitly). Fix: `specDraftCompactionOptions`, a pure
  helper mirroring the TUI's gate — enabled passes the resolved context
  window plus the configured reserve/keep-recent knobs through; disabled
  forces `ContextWindow` to 0, matching the loop's no-op contract.

- `[x]` **WG3**: the pipeline `runOptions` in `internal/cli/exec.go:604` computed
  `ContextWindow` via `resolveAgentContextWindow` — including a live
  provider-discovery network round trip on a registry miss — for a value
  `internal/splice` reads zero times. Dropped the computation and replaced it
  with a comment naming the invariant (same style as the five existing
  "is inert" comments in the same struct literal). A new test,
  `TestPipelineNeverReadsContextWindow`, source-scans `internal/splice` for
  any `ContextWindow` reference so the claim cannot drift silently; verified
  falsifiable by a temporary trip-wire edit before landing.

- `[x]` **WG4** (`@needs-human`, decisions recorded 2026-07-26): `EventTaskStarted`
  had a full contract (event type, payload struct, decoder arm setting
  `"running"`) and no producer, and separately the decoder built a
  `schemas.TaskRunOutcome{Status: "running"}`, which `TaskRunOutcome.Validate()`
  rejects (it is strictly terminal: completed/failed/aborted). `schemas.TaskRunStatus`
  — the type actually documented as "per-task execution status... including
  in-flight states" — had zero non-test usages and a drifted vocabulary
  (`succeeded` instead of `completed`). Fixed both: aligned `TaskRunStatus`
  to `pending, running, completed, failed, aborted`; added an additive
  `schemas.TaskStartCallback` and `RunDesignPlanOptions.OnTaskStart`
  (`TaskLifecycleCallback` untouched, so existing callers keep compiling);
  wired the emitter in `internal/tui/design_mode.go` alongside the existing
  terminal emitters; switched `DesignState.TaskOutcomes` from
  `TaskRunOutcome` to `TaskRunStatus` since only the latter can legally hold
  `"running"`. A run interrupted mid-task now reconstructs as `running` on
  resume/replay instead of reading as absent (indistinguishable from a task
  that never started).

- `[x]` **WG5**: deleted `internal/reasoning/` (495 lines: `capability.go`,
  `catalog.go`, an embedded `modelsdev_snapshot.json`, `catalog_test.go`) and
  `internal/reltime/` (65 lines). Both inherited from upstream Zero (PRs
  #338, #315) with zero importers in any commit in this repo's history; the
  live equivalent of `internal/reasoning` is `internal/modelregistry`.
  `internal/reasoning`'s nine tests (`TestGroundTruthOpenAI`,
  `TestCoversZeroShippedReasoningModels`, and others) ran green in CI
  against code nothing called, which is what made the package look
  maintained. Upstream still carries both files; this is a deliberate
  divergence and will produce a trivial "deleted by us" conflict on a
  future upstream merge. Full `go build ./...` / `go vet ./...` /
  `go test ./...` (both modules, root and `memd/`) green after deletion.

- `[x]` **WG6**: `buildStageRegistry` (`internal/splice/registry.go`) took
  `provider agent.Provider` and `runner ToolRunner` params that it never
  used (`_ = provider`, `_ = runner`) and computed `detectLanguage(workDir)`
  only to discard it (`_ = language`); dropped both unused parameters and
  the dead computation. Separately, `detectLanguage` runs a
  `filepath.WalkDir` of the whole workspace whenever no
  `go.mod`/`tsconfig.json`/`package.json` marker is present (every Python
  target) and is called via `stageOptions` once per stage per iteration -
  not "twice" as first estimated; threading the value through the five
  call-chain signatures (`runExecutionPlan` -> `runIterationLoop` ->
  `runPass` -> `runStageWithContext` -> `stageOptions`, touching ~20 direct
  test call sites) was judged disproportionate to the actual gain, so a
  small mutex-guarded single-entry memoization cache was added instead
  (`languageCacheDir`/`languageCacheVal` in registry.go), marked
  `ponytail:` with its ceiling (a future daemon interleaving concurrent
  distinct workspaces would thrash it; upgrade to a bounded map if that
  ever happens). New `registry_test.go` (no prior test file existed for
  this component): stage registration, tool-registration idempotence on a
  shared `*tools.Registry`, marker-file detection, and cache correctness
  (stale-after-first-call, per-workDir, not sticky). `go test -race` clean
  on the new shared state.

- `[x]` **WG7**: `docs/PIPELINE.md` gained an "Options that do not apply
  under `splice exec`" section covering specialist delegation, skills, MCP
  tool deferral, `--allow-escalation`, `--self-correct`, file diagnostics,
  and compaction, all inert on the deterministic pipeline path by
  architecture. Investigating surfaced a sharper fact than the audit
  originally stated: `--allow-escalation` and `--self-correct` have no
  effect under **any** `splice exec` invocation today, not just the
  default pipeline path. `exec_spec.go` (the one headless path that runs a
  real `agent.Run` loop, `splice exec --spec`) never reads either flag;
  only the interactive TUI wires them. Documented precisely rather than the
  looser "inert on the pipeline" framing. Added a one-line pointer to
  `docs/PIPELINE.md` in both flags' `--help` text
  (`internal/cli/app.go:1387-1390`).

- `[x]` **WG8**: `docs/SPECIALISTS.md` gained a "Process Lifecycle On Cancel
  Or Timeout" section documenting that Windows kills only a specialist's
  direct child process on cancel/timeout, since Windows has no process-group
  equivalent, while Unix kills the whole group; `WaitDelay` only stops
  Splice itself from hanging on a leaked grandchild, it does not reap it.
  Recorded as a `@needs-human` entry in "Deferred / known gaps" naming the
  Job Object fix and its Windows-CI-runner requirement.
- `[x]` **WG9**: `doctor.go:236`'s connectivity-check fallback message
  claimed "Connectivity probing is not wired in the Go doctor backend
  yet." Traced both real callers (`splice doctor --connectivity` in
  `internal/cli/observability.go`, and the TUI's `doctorOptions` in
  `internal/tui/model.go`): both always supply a `ProviderHealth` probe
  result whenever `Connectivity: true` and the profile resolves, so this
  branch is unreachable from the shipped CLI or TUI today. It fires only
  when a caller passes `Connectivity: true` without running the probe - a
  caller bug, not an unbuilt backend (`providerhealth.Probe` is wired and
  working). Corrected the message to name that condition. Added
  `TestRunReportConnectivityRequestedWithoutProbeResult`, since no test
  previously exercised this exact branch, which is how the stale claim
  went unnoticed.

Track WG (wiring gaps, 2026-07-26) is now complete: all nine checkpoints
landed. See the Decision Log entries dated 2026-07-26 for the full
per-checkpoint record, and the note on GitHub Actions billing (WG6
onward gated by a local replica of `.github/workflows/ci.yml` instead of
live CI; get a real CI confirmation once billing is restored).

## Track PE: first-class pipeline evals (2026-07-26)

Source of truth:
`plans/first-class-pipeline-evals-and-cost-accounting-2026-07-26.md`.
This plan supersedes `plans/f10-eval-harness.md`. The default benchmark will
start the current executable through the production `splice exec` path.
`--agent-command` remains the external-agent override.

Resolve the existing OpenAI-compatible SSE, explicit-trust, and WSL2 sandbox
acceptance defects before PE1. These repairs are preconditions, not PE scope.

- `[x]` **PE1**: complete token dimensions and replace positional stage and
  escalation resolver results with one typed model selection.
- `[x]` **PE2**: emit one attributed usage event for every pipeline provider
  stream, including streams that omit usage.
- `[x]` **PE3**: create and price the request ledger once, persist provenance,
  and derive structured stage and pipeline cost coverage.
  PE1 through PE3 landed on public `dev` in commit `7440a8e`.
- `[x]` **CP-A** (`7389a45`): delete `sumTotals`, collapse the three ledger
  aggregation passes into one, and derive the sequence from the record count.
- `[x]` **CP-B** (`7829e1e`): consolidate the request ledger and coverage tests.
  Executed cases rise from 447 to 469 while the test code drops 153 lines.
  Cleanup item 5 (TUI viewport cache keys) was dropped after review. The five
  keys at `model.go:3145` are redundant with the generation, but `width` and
  `height` at `model.go:2971` are call-site arguments that guard three sharers
  of one cache. The honest yield was about eight lines against a stale-render
  risk.
- `[x]` **PE4a** (`15dc68e`): parse usage events while the agent runs.
  The collector never returns an error from `Write`, because `io.MultiWriter`
  stops at the first failing writer, which aborts the os/exec copier and
  SIGPIPEs the child. Verified with a standalone reproduction.
- `[x]` **PE4b** (`9e137e0`): derive eval cost from the request samples and
  delete `estimateAgentRunCost`.
- `[x]` **PE4c** (`eed860d`): publish `splice.agenteval.benchmark.v2`.
  `meanCostPerPassedTask` was already campaign cost over passed tasks, so it
  aliases `campaignEstimatedCostPerPass`, not the similarly named passed-task
  mean.
- `[x]` **PE5** (`4ff0a2d`): select the production pipeline runner by default.
  The external command runner is unchanged.
- `[x]` **PE6a** (`0215c28`): persist cost estimates with provenance and
  coverage in `internal/usage`.
- `[x]` **PE6b** (`0de1c81`): show honest session cost with explicit coverage.
  Unpriced records now keep their tokens. Dropping them let a session with
  mixed pricing report a confident total that omitted part of the work.
- `[x]` **PE7a** (`a740bb7`): report the runner, the routed models, and cost
  coverage in CSV and text. The wrapper declares `splice.cli.eval.v1`.
- `[x]` **PE7b** (`1fbedd9`): public documentation under ASD-STE100.
  `docs/AGENT_EVALS.md` describes the built-in pipeline runner, the argv it
  starts, and the production setup the child inherits. It records that
  `--agent-command` stays the external override, that a benchmark model is the
  requested primary model, and that routing stays active. It also records that
  the harness parses usage while the agent runs, so totals stay correct when
  diagnostic stdout passes 8 MiB. `docs/STREAM_JSON_PROTOCOL.md` documents the
  request identity and pricing fields on usage events. The README mirrors
  reference the eval document but describe no eval behavior, so they are
  unchanged.
- `[x]` **Pricing repair** (`00d93f2`): derive registry entries for models the
  curated catalog does not carry. This is not a PE checkpoint. It removes the
  condition that made session cost inert, so PE6b and PE7a report real numbers
  on an off-catalog configuration. Derived entries are scoped to an explicit
  provider key, because one model id carries different prices per provider.
  Without provider context the model stays unpriced. Curated entries stay
  authoritative for identity. See the MEMORY.md Decision Log entry "model
  pricing was a wiring gap, not missing data".
- **PE7c** routed to Track FND (2026-08-19): the measurement
  instrument must be validated before learned policies gate through it.

No PE checkpoint adds benchmark concurrency, remote workers, provider invoice
reconciliation, forced single-model routing, or trusted fixture extensions.

### PE run log 2026-08-23 (see plans/paired-eval-run-log-2026-08-23.md)

Cycles 1 through 7 hardened the instrument but produced no claim-bearing
result. The harness now survives arm failures, persists pairs incrementally,
resolves trace roots, reports stream usage when a cold trace is unavailable,
uses stable arm roots, and resets fixture bytes before every pair. Matching
public commits include `9dcd56c`, `66bb4b9`, `4f553e0`, `5cf449e`, and
`ba278ba`.

Run 7's 43.2 percent headline is quarantined. Two probes could not compile,
and task files leaked between pairs. Clean replay changed several outcomes.
The current 12-task suite also influenced probes, prompts, budgets, harness
policy, and the memory contract. It is Development data only. It can validate
the instrument, but it cannot resolve a product claim.

## Track EV2: sealed memory efficacy evaluation (2026-08-23)

Source of truth: `plans/eval-v2/`. Purpose: test whether relevant frozen
memory improves Splice without repeating the false-positive and false-negative
failures from PE cycles 1 through 7. This track is claim work, not a demo.
No paid claim run starts until EV2-10 receives owner approval.

Protocol decisions:

- Use three schema-identical arms: empty, hard placebo, and relevant frozen.
  Relevant must beat empty on net tokens and beat placebo on content value.
- Disable observation and exemplar writes. Keep traces enabled for every arm.
- Separate frozen efficacy, stale/conflict safety, and online adaptation.
- Use a sealed private holdout. The current 12 tasks stay Development only.
- Repeat and counterbalance trials. Derive sample size from a final-protocol
  pilot and owner-approved product margins.
- Require complete telemetry, locked routes, immutable graders, canonical token
  accounting, and fixed-task paired-block intervals before any verdict.
- Deny every agent tool access to hidden roots and sanitize the child
  environment. A hidden path outside the workspace is not isolated by itself.
- Keep all active holdout material on the owner's PC in a no-remote repository.

Checkpoints:

- `[x]` **EV2-P0: reasoning-memory contract prerequisite.** Commits `8a4f9ae`,
  `e9c0bd8`, and `4f2e189` add bounded admission, mandatory consideration,
  non-blocking reconciliation, and shared normal/repair preparation. Review
  repair `edad633` closes input validation, missing-array accounting,
  per-invocation trace aggregation, repair integration coverage, standard
  library, and changed-file compaction gaps. Portable CI repair `f6c2bf7`
  follows. CI run `32675528049` passed both jobs.
- `[x]` **EV2-D0: research and protocol draft.** Synthesize primary evaluation,
  retrieval, memory, coding-task, and local-workflow evidence. Write the
  protocol, threat model, efficacy and safety preregistration templates, and
  PC-local runbook under `plans/eval-v2/`. This checkpoint authorizes no
  execution.
- `[x]` **EV2-0: protocol schemas.** Add typed `Protocol`, `Manifest`,
  `TaskSpec`, `ArmSpec`, `TrialSpec`, `TrialResult`, `AnalysisPlan`, and
  validators under a new `internal/eval/v2` package. Keep Eval v1 unchanged
  for Development smoke tests. Gate: `go test ./internal/eval/v2/...`.
  Shipped `698bc92`, CI `32744403421`.
- `[x]` **EV2-1: durable identity.** Generate the locked Latin-square schedule,
  persist idempotent trial keys before cleanup, and resume without duplicates.
  Gate: crash, replay, duplicate, and schedule property tests.
  Shipped `0962e757` + spec commit `28594fb2`, CI `32756229083`,
  `32756462598`. Review notes: document the MissingTrials precondition
  (schedule must come from GenerateSchedule); Journal alias awaits its
  EV2-7 consumer.
- `[x]` **EV2-2: isolated environment.** Use experiment-specific binaries,
  sidecar socket/database, session root, arm roots, fixture resets, hidden-root
  deny rules, and an allowlisted child environment. Refuse a dirty source tree
  or hash mismatch. Gate: contamination, stale-sidecar, shell/file/symlink,
  environment, and process-metadata leakage fixtures.
  Shipped `0974877a` + spec commit `4cea7386`, CI `32760689531`. Independent
  probes verified symlink escape denial and /var-/private/var alias equality.
  Review notes for the runner: consume or drop BuildDenyRuleSet; give
  DenyRuleSet.Check an explicit tool-class parameter; pin the git binary from
  manifest toolchain versions, not PATH.
- `[ ]` **EV2-3: frozen memory policy.** Import empty, placebo, and relevant
  snapshots; preserve delivered IDs plus content hashes; permit trace writes;
  reject memory writes; verify deterministic selected and post-compaction IDs.
  Freeze the corpus, placebo pool, admission policy, and selector before task
  selection. Gate: write-denial, leakage, ID-pairing, parity, and compaction
  tests.
- `[ ]` **EV2-4: symmetric telemetry.** Query traces by exact run/session ID,
  capture actual stage routes, prove fallback equivalence, and make missing
  telemetry invalid. Gate: the cycle-2 zero-telemetry false positive cannot
  produce a verdict.
- `[ ]` **EV2-5: task QA and sealing.** Reuse the deep offline-eval fields for
  fixture, changed-file, forbidden-file, trace, context, and verification
  checks. Add an append-only candidate registry, reference,
  independent-solution, mutation, grader-isolation, non-claim-route QA, and
  immutable-hash gates. Task acceptance precedes selector audit, and retrieval
  misses stay in the holdout. Gate: both broken Run-7 probes fail sealing.
- `[ ]` **EV2-6: analysis and power.** Implement the canonical normalized
  request-token formula, exact equal-task estimators, within-task paired-block
  intervals, relevant-versus-empty and relevant-versus-placebo ratios of arm
  mean tokens, fixed-sequence primary gates, secondary Holm correction, and joint
  power simulation. Gate: fabricated corpora cover every verdict, token-subset
  trap, placebo-harm trap, and optional-stopping refusal.
- `[ ]` **EV2-7: CLI and reports.** Add thin preflight, task-audit, run, resume,
  status, audit, power, and report commands over `internal/eval/v2`. Reports
  show actual routes, artifact hashes, invalid runs, intervals, pricing
  coverage, and all required cost denominators.
- `[ ]` **EV2-8: adversarial validation.** Replay telemetry loss, stale sidecar,
  budget asymmetry, task contamination, broken checks, route drift, duplicate
  resume, grader access through every tool class, environment and process-path
  leakage, and reward-hacking fixtures. A fresh reviewer must find no
  unresolved blocker.
- `[ ]` **EV2-9: Development smoke.** Run three to five private tasks through
  all three arms on the owner's PC. Validate only the instrument. Do not read
  it as efficacy evidence.
- `[ ]` **EV2-10: preregistration and owner gate.** Build the training-only
  memory snapshot and sealed holdout. Lock task, corpus, model, prompt, route,
  budget, schedule, analysis, and binary hashes. Present exact repetitions,
  `delta_success`, `min_token_gain_empty`, `min_token_gain_placebo`, safety
  limits, expected cost, maximum next-call reserve, and hard spend cap. Tag:
  `@needs-human`.
- `[ ]` **EV2-11: claim run and independent report.** Execute the locked fixed
  sample locally. Do not show efficacy outcomes during the run. Reproduce the
  report from raw trials. Ticket #7 receives only the scoped Level-1 verdict.
- `[ ]` **EV2-12: replication and claim decision.** If EV2-11 supports the
  hypothesis, use a second sealed holdout and model or provider before a
  quantitative public claim. Ticket #12 and exact owner wording approval
  remain mandatory. Tag: `@needs-human`.

Strongest disproof: a relevant-placebo win with no relevant-empty gain only
shows that the placebo is harmful, so the fixed primary sequence rejects the
claim. If relevant saves tokens by failing sooner, the success gate rejects the
claim. If one repository, task order, or model drives the effect, narrow or
reject the product statement.

## Track MP: model pricing accuracy (2026-07-28)

Track MP is v0.1.3 item 3. It moves all price data to models.dev, prices the
long-context tiers, and makes the curated catalog carry identity only. See the
MEMORY.md Decision Log entry "item 3, and the price that was never ours to
keep".

- `[x]` **MP1** (`5d6e762`): price long-context tiers from models.dev. N
  upstream steps become N+1 `ModelCostTier`, because the tier carries an
  inclusive ceiling and models.dev states the size a step applies above. A step
  that omits a cache rate keeps the rate in use. Without that rule, 5 models in
  the live snapshot bill cached tokens at the full input rate.
- `[x]` **MP2** (`eee9f67`): cap the compaction budget at the cheapest pricing
  tier. `CompactionConfig.StayInCheapestPricingTier` turns it off and defaults
  to on. The cap changes the compaction budget only. `ContextLimits` keeps the
  model's true window, so the picker and the reports stay honest.
- `[x]` **MP3** (`8b3ad05`): ship a gzipped models.dev snapshot (305 KB) and
  remove all 18 `ModelCost` literals from `catalog.go`. A newer disk cache wins
  over the embedded snapshot. `SourceLastVerified` reports the date of the
  source that supplied the price.
- `[x]` **MP4** (`6bad51a`): show `~$0.42` when pricing coverage is partial.
  Unavailable coverage keeps its words and never shows a number.
- `[x]` **MP5** (`1dcdf3a`): stop the test suite reaching models.dev, and add a
  weekly workflow that refreshes the embedded snapshot through a pull request.
- `[x]` **MP6** (`2c3089b`): give the TUI catalog its provider profile. The TUI
  built the catalog with a zero-argument `DefaultRegistry`, so it never held
  derived entries, and that catalog feeds the usage tracker, the compaction cap,
  and stage routing. Every request on a derived model was unpriced in the TUI.
  The defect is as old as `480083e`. `00d93f2` repaired the same mistake in the
  `/model` picker and missed this site.
- `[ ]` **MP7**: watch the first real run of `refresh-model-snapshot.yml`. The
  workflow is inert until it reaches `main`, because GitHub runs a schedule
  from the default branch only. Its shell logic is proved against the live
  endpoint; the `gh pr create` path is not.

Track MP does not add per-provider invoice reconciliation, a price history, or
a fallback that guesses a price the feed does not carry. A model models.dev
does not price stays unpriced and reports it.

## Track WS: web search and fetch (2026-07-29)

Track WS moves web search off a splice-executed HTTP backend and onto the
provider. The provider runs the search during inference and bills the user's
own account, so a user needs no key of ours and we run no service.

Decision: the provider does the search, always. There is no client-side
fallback. A provider that cannot search offers no search tool, and the gap
becomes visible instead of hidden behind a backend the user must configure.
Extensions fill the gap later.

- `[x]` **WS1** (`3976759`): runtime plumbing. `CompletionRequest.ServerTools`
  asks for a provider tool that carries no schema. `ServerToolBlock` keeps the
  raw provider payload on the message, because Anthropic refuses a later turn
  whose web search blocks changed; this follows `ReasoningBlock`, which already
  solves the same problem. Adds the server tool stream events, the collector
  callbacks, a `WebSearchRequests` count on usage, and
  `ModelCost.WebSearchPerRequest`. A search with an unknown rate reports as
  unpriced, never as zero.
- `[x]` **WS2** (`fe19ea3`): OpenRouter `openrouter:web_search` server tool, engine pinned
  to `parallel`. The engine must always be sent: without it OpenRouter picks
  `native` for the major vendors and returns provider-native blocks this code
  cannot read. Stops registering the HTTP-backed `web_search`.
- `[x]` **WS3** (`bc34a6b`): delete `internal/tools/web_search.go` and its test, 1245
  lines. Git history is the archive. A Go file left in the tree still compiles
  and still gets vetted, so an unregistered file is not archived, only quieter.
- `[x]` **WS4** (`3976759`): set `ServerTools` on the runs that should search. Until this
  lands WS1 and WS2 are inert, because nothing asks for the tool.
- `[x]` **WS5** (`2e8bd63`, rates verified against OpenRouter's published table,
  which also shows each rate covers up to ten results): price the search. The OpenRouter rate follows the ENGINE
  (`parallel` $0.001, `exa` $0.005), but `WebSearchPerRequest` sits on the
  model entry, so the two do not line up. Until this lands a search counts and
  reports unpriced.
- `[ ]` **WS6**: adopt `openrouter:web_fetch` and delete the HTTP `web_fetch`.
  Free on the OpenRouter engine, and it carries domain controls. Note the cost:
  a provider cannot reach `localhost` or an intranet, so local fetch stops
  working until WS8.

### WS7: the release question (open, blocks a public claim)

The README offers twelve providers. Search that works on OpenRouter only means
a public user who follows that README and picks Anthropic or Ollama installs
splice and finds a design agent that silently cannot research. Three paths:

1. Treat search as a PROVIDER CAPABILITY, like vision or reasoning effort.
   Splice already degrades for those. Needs a visible one-time notice when the
   active provider cannot search. Recommended: small, honest, does not block a
   release, and does not overpromise.
2. Add the native adapters (Anthropic, OpenAI Responses, Gemini). Covers most
   cloud users. Carries the agent-loop work in WS9, none of it verifiable
   without vendor keys.
3. Keep a client-side fallback. The only universal option, and the one this
   track deliberately rejected.

Silence is the wrong failure whichever path wins.

### WS8: Lightpanda as the first extension (future)

Provider-side fetch covers the public web. It cannot reach `localhost`, an
intranet, a page behind the user's session, or an interactive flow. That is
network topology, not quality, so no provider improvement closes it.
Lightpanda ships native MCP support, so it needs no splice code.

### WS9: native provider search (future, if WS7 path 2 wins)

Anthropic needs more than an adapter. Verified against the current docs:

- `pause_turn` is mapped to a normal stop (`internal/providers/anthropic/
  provider.go:634`) with a comment saying the client must resume the turn.
  Nothing does. A paused search turn ends early and silently.
- A turn that mixes a server tool with a client tool returns
  `stop_reason: "tool_use"` with the server call unresolved. The follow-up user
  message must then carry `tool_result` blocks and NOTHING else. Splice injects
  extra user messages in exactly that position: the failure hint, the dropped
  tool-call notice, the async diagnostics nudge, and compaction output. Each is
  a 400.
- The continuation must keep the same `tools` array or the request fails.

OpenAI's web search is Responses-only, so it never reaches an
openai-compatible endpoint. Gemini reports no search count, so the count must
be derived.

## Track IN: install commands and the snapshot gate (2026-07-30)

Track IN is a v0.1.4 item, deferred from the v0.1.3 cut. It publishes the
one-line install commands, and repairs the two installer defects and the one
workflow gate that stop them from working. See
`plans/install-commands-and-snapshot-refresh-2026-07-30.md` for the evidence
behind each item.

- `[x]` **IN1** (`54f9d9d`): make every installer agree with the archive, in one change
  across `install.ps1`, `install.sh`, `postinstall.mjs`, and
  `internal/update/apply.go`. `install.ps1` requires
  `splice-windows-command-runner.exe` and `splice-windows-sandbox-setup.exe`
  and throws through `Find-ZeroExtractedFile` when they are absent, which they
  always are per IN5, so the Windows install has never worked. Reduce its
  required set to `splice.exe` and adopt the optional-binary rule that
  `postinstall.mjs:139-143` has used correctly for four releases. In the same
  change add `splice-memd` to all four paths: the sidecar is in every archive
  and the string `memd` appears in none of them, so `memd/client.go:268`
  resolves nothing and `Resolve` returns "memory is simply off" for every
  released install, silently. `install.ps1` is touched by both halves, so they
  ship together.
- `[x]` **IN2** (`469c25d`): check archive contents in CI. `grep -c smoke
  .github/workflows/*.yml` is zero while `cmd/splice-release smoke` and
  `verify` exist, so nothing has ever checked what an archive holds. This is
  why IN1 and IN5 survived three releases.
- `[x]` **IN3** (`57bfb7d`): publish the commands. `install.sh` needs `bash`, not `sh`, in
  17 places, so the piped command must name `bash`. `install.ps1` declares a
  `param()` block, so a piped `iex` cannot bind named arguments and the
  argument form needs a script block. Add both to `README.md` and
  `docs/INSTALL.md`. Do this after IN1: a published command for a broken
  installer is worse than no command.
- `[x]` **IN5** (`5c699f7`): make the Linux sandbox reachable on a
  released install. **The earlier premise here was wrong and is corrected.** The
  entry said the failure was closed and only bit where `bwrap` was absent.
  It bites on every Linux install, `bwrap` present or not.
  `manager.go:177` selects the Linux backend by a `$PATH` lookup of
  `splice-linux-sandbox`, and `lookupExecutable` at `:145` searches `$PATH`
  alone. No workflow builds that binary and no archive holds it, so the lookup
  always fails and `:184` returns `unavailableBackend`. Track S shipped sandbox
  hardening that has never been active for a Linux user.
  Windows already solved the same problem. `resolveWindowsSandboxHelper`
  (`windows_runner.go:71-85`) has a third tier, self-dispatch: the running
  `splice.exe` invoked with a hidden `__windows-command-runner` subcommand,
  routed at `app.go:255-258`. Linux has no such tier; its third tier is
  `go run` from a source checkout, which a released install never has.
  The decision is to port self-dispatch to Linux, not to ship helper binaries.
  Nothing new enters the archive. `cmd/splice-linux-sandbox/main.go` is three
  lines calling `sandbox.RunLinuxSandboxHelper`, the same shape as the Windows
  helper. Detection and execution both resolve the helper and both need the new
  tier; fixing one leaves the sandbox off. The seam to watch is the argument
  prefix: `runner.go:187-199` drops it, while Windows carries it through
  `Backend.ExecutableArgsPrefix` (`manager.go:196`). The `bwrap` requirement at
  `manager.go:178` must survive unchanged.
  Landed. Red proof recorded the pre-fix behaviour precisely: enforcement fell
  to `degraded` with `Wrapped:false`, so commands ran unsandboxed and nothing
  said so. Note this makes 0.1.4 a behaviour change for Linux users, not only a
  fix: a command that ran unconfined before now runs confined wherever `bwrap`
  is present.
  **Decided 2026-08-05: ships in 0.1.4, after S2b.** Holding it back means
  another release where the Linux sandbox does not work at all. The upgrade
  impact is real and the release note must carry it. S2b goes first because
  read roots include `/` and `DenyRead` is empty, so enforcing on Linux without
  it would turn the sandbox on with the read side wide open.
  Note separately that `cmd/splice-release` is **not** the release path:
  `package` emits an npm-layout tarball of 3253 entries against a release
  archive's 3, and no workflow references it. Do not route the release through
  it.
- `[x]` **IN4b** (`102ff2f`): IN4 fixed the date half only. A refresh
  replaces the snapshot **content** too, and three tests hardcode
  `z-ai/glm-5.2` at `0.6692 / 2.1032 / 0.12428`. models.dev already serves
  `0.76 / 2.42 / 0.14`, so the gate fails today. Verified by running the
  workflow's own steps locally against live data: `modelsdev_test.go:441`,
  `:578` and `:799` all fail. Same defect class as the skipped-record count:
  a literal tracking upstream data nobody here controls. `gpt-5.6-sol` at
  `5 / 30 / 0.5` and its `272_000` tier boundary are the next to go. The fix is
  to assert that the registry reports what the snapshot says, rather than what
  the snapshot happens to say today.
  Closed. Acceptance was the live simulation: download models.dev, gzip it over
  the embedded snapshot, stamp the date, run the gate's own command with
  `-count=1`, restore. All three packages pass.
  Two things surfaced while verifying. The first rewrite compared registry tiers
  against `modelsDevCostTiers(record)`, which is the production converter, so it
  asserted `production == production`. Corrupting every boundary to
  `999999 + step.Tier.Size` left the test green. Rewritten to compare registry
  output against the raw context sizes in the JSON; the same mutation now fails
  with `tier 0 boundary = 1271999, want raw context size 272000`.
- `[ ]` **IN6** (found 2026-08-04): split `SPLICE_DISABLE_MODELS_FETCH`. The
  name says network fetch, but `modelsdev.go:449` also skips the disk cache
  entirely. `ci.yml:41-45` sets it for the whole suite to stop a fetch racing a
  pricing assertion, so every disk-cache precedence test has been reading the
  embedded snapshot instead of the cache it names. Proven: under that variable
  `TestDefaultRegistryRealCachedSnapshot` reports
  `Source: models.dev/api.json (embedded snapshot)`. IN4b worked around it per
  test with `t.Setenv`. The real fix is one flag for the network and another for
  the cache. Not release blocking, because tests point
  `SPLICE_MODELS_CACHE_PATH` at a temp dir.
- `[x]` **IN4** (`bd75d05`): make the snapshot gate survive a refresh. Run `30512786208` on
  2026-07-30 was the first ever run of `refresh-model-snapshot.yml` and it
  failed eight tests in `internal/modelregistry`. The cause is a date
  relationship, not a loose literal. `modelsdev.go:459` prefers the disk cache
  only when its mtime is strictly after the embedded snapshot date, and
  `modelsdev_test.go:730` and `:782` build that cache with the absolute
  `2026-07-28`. That beat the old embedded `2026-07-27` and loses to the new
  `2026-07-30`, so every cache-precedence assertion flips to the embedded
  snapshot. Verified by isolation: changing only
  `modelsdev_snapshot_date.txt` reproduces six of the eight failures with the
  snapshot content untouched. The other two come from the content, where the
  skipped-record count moved 28 to 27. `SPLICE_DISABLE_MODELS_FETCH` was ruled
  out, because `ci.yml:46` sets it too and CI is green. The workflow stamps the
  date with `date -u +%F`, so an absolute past mtime can never win again and
  the gate fails on every future refresh. Fix by deriving the test mtimes from
  the embedded date rather than hardcoding them. It failed safe: no commit, no
  pull request, `main` untouched. A green run is the acceptance test, because
  the gate has never passed.

## Track RR: review remediation (2026-08-01 to 2026-08-03)

Five external review reports (routing/communication, orchestrator handoff,
tools/skills/MCP, TUI rendering, memory) plus a skills-methodology review.
Twenty-seven commits, released as 0.1.4. Every finding was re-verified against
the code before work started; several were narrowed or reframed in the process,
and the conclusions are recorded in `MEMORY.md`.

The findings converged on ONE defect shape, which is the reason this track has
its own entry: **a value is produced, sometimes validated, and never consumed.**
Six independent instances, each shipped green.

- `[x]` **RR1**: usage attribution. Stage/model work units in the report,
  compaction attributed to a stage, plain `exec` runs recorded, provider-reported
  cost preferred over estimates, and the work-unit table rendered in the text
  report at a fixed width. Commits `2725481`, `1113b5b`, `582519e`, `7da9701`,
  `aaaf33f`.
- `[x]` **RR2**: capability honesty. Tools with nothing behind them are not
  advertised; the `Task` tool stopped naming specialists that never existed.
  Commit `a257369`.
- `[x]` **RR3**: terminal safety. `SetConsoleOutputCP` on Windows plus
  `SPLICE_ASCII=1`, a width-preserving fold at the render boundary. Escape
  sequences from tool output are stripped where every card renderer normalizes
  its detail, so a hostile file cannot drive the terminal. Commits `e506376`,
  `9646d20`.
- `[x]` **RR4**: stage handoff. The roster and the next stage reach the model in
  the payloads stages actually marshal, and prior summaries come from the run
  rather than two keys no tier ever contains. Commits `b678d74`, `29e4e13`.
- `[x]` **RR5**: test selection. The test stage runs a test-kind check rather
  than the first check detected, and a project with none reports that
  verification could not run instead of failing to the abort cap on
  `go test ./...`. Commit `1befb20`.
- `[x]` **RR6**: the plan, executed and visible. `/approve` streams and asks for
  permission instead of running behind a spinner and refusing every gated tool;
  a failed run leaves the session current; acceptance criteria carrying commands
  are shown before approval and then actually run, scored apart from tests.
  Commits `ffa277b`, `bcdc8fb`, `0e1e0ac`, `062e15c`, `89474f7`.
- `[x]` **RR7**: design phase. The agent interviews until the design is settled
  rather than reaching for a plan, and crystallizing notes what the conversation
  left open without blocking. Plans in a directory are offered on return.
  Commits `6a6c298`, `77894eb`.
- `[x]` **RR8**: reasoning effort. `xhigh` and `max` reached three adapters and
  none understood them — OpenAI sent no effort, Anthropic and Gemini computed no
  thinking budget, so asking for the most reasoning produced none. Each adapter
  clamps to its own strongest tier. Throughput joins the latency line. Commit
  `a5cf404`.
- `[x]` **RR9**: memory. `/memory search` sent no scopes and no project path, so
  the daemon answered nothing; `/memory recent` asked the text index for `"*"`.
  Recent listing became a request of its own, and an older daemon is reported as
  out of date. Commit `adae5a9`.
- `[x]` **RR10**: skills in a headless run. Core re-registration replaced the
  plugin-aware skill tool by name, so plugin skills vanished while the prompt
  still advertised them. Commit `9285ff2`.
- `[x]` **RR11**: analysis scope and session growth. Files a change created are
  analysed, not only tracked edits. `sessions prune-plan` / `prune` reclaim old
  sessions, following the existing plan-then-execute shape; nothing is removed
  automatically. Commits `96989a2`, `384d125`.

Outstanding from the same reports, deliberately not taken:

- `[ ]` **RR12**: reasoning is billed at the output rate. Providers that price
  thinking differently drift whenever an exact charge is not reported, which
  today is everything except OpenRouter. Needs a reasoning rate in the catalog.
- `[ ]` **RR13**: `static_analyzer` and `security_auditor` run serially though
  both are model-free and order-independent. Running them together would halve
  that segment of wall time.
- **RR17** routed to Track FND (2026-08-19): usage double-fire would
  poison cost priors silently.
- `[ ]` **RR18**: the design-conversation tool registry is a hand-maintained
  allowlist (`internal/tui/design_mode.go`). A new read-only core tool is absent
  from design mode until someone edits that list, and nothing fails when they do
  not. There is no shared read-only predicate to route against.
- `[ ]` **RR14**: escalation is pipeline-only. A chat or design turn that keeps
  failing never routes to a stronger model.
- `[x]` **RR15** (`3ad3503`): the prompt work from the skills-methodology review — prompts
  steer by prohibition in about one line in five, and the test stages have no
  vocabulary for a test that can fail or for where a test belongs. Behavioural,
  so the eval harness is the only honest verification.
- `[ ]` **RR16** (re-specified 2026-08-03): `memd` retains every observation
  forever. The original wording — soft-delete columns with no collector —
  described a mechanism that does not exist, and building the collector it asked
  for would have collected nothing.

  What is actually there: `deleted_at` is declared in the schema and filtered on
  in five queries (`WHERE deleted_at IS NULL`), and **no code path ever writes
  it**, so no row is ever soft-deleted. `review_after` is not a delete marker: it
  arrives from the client on upsert and `MarkReviewed` clears it, meaning
  "surface this again", not "discard this". `memd` contains no `DELETE FROM` and
  no `VACUUM`; the store exposes Upsert, Search, Recent, MarkReviewed, byID and
  Close.

  The real gap is retention, the same shape as the session growth closed by
  `sessions prune`. It is not yet actionable: the database on the developer's
  machine holds zero observations, and per IN1 no installer ships
  `splice-memd`, so no released user has memory running at all. Retention for
  data nobody is generating is speculative. Revisit once IN1 lands and
  observations accumulate. Either wire `deleted_at` to something that writes it
  or drop the column, since a filter on a value nothing sets is a standing
  invitation to build against it.

## Track CR: credential storage at rest (2026-08-03)

From a credentials/OAuth security review. One original finding was actionable;
the rest were deliberate or already documented. CR5 is a later live acceptance
finding.

- `[x]` **CR1**: OAuth tokens defaulted to a 0600 plaintext file while API keys
  defaulted to the macOS keychain or an AES-256-GCM encrypted file, with
  plaintext reachable only through an explicit opt-out. A bearer token is an
  account-level credential, so the stronger secret had the weaker protection.
  `credstore.go:6` ("OAuth tokens stay in internal/oauth") states scope, not a
  justification — there was no recorded reason for the asymmetry, and it reads
  as history: credstore was hardened and oauth never followed.
  `oauth.NewStore` now resolves the same policy, `SPLICE_OAUTH_STORAGE=file`
  remains the plaintext opt-out, and existing tokens migrate at startup only.
  Commit `8b3ac0f`.

  **The first attempt destroyed real data.** Migration was implemented on the
  store's load path, so any read moved and deleted the plaintext file. Running
  `go test -race ./...` deleted the developer's live `oauth-tokens.json`; the
  tokens survived only because the write into the real macOS keychain happened
  to succeed. The trigger was `internal/providers/factory.go:316`
  `codexAccountForKey`, production code that builds a default-path store and
  calls `Load` merely to read an account claim. The reference pattern the spec
  cited does the opposite: `config.MigratePlaintextProviderKeys` is invoked
  explicitly at one site, `internal/cli/app.go:687`, and is never a side effect
  of reading. Migration is now `oauth.MigratePlaintextProviderTokens`, injected
  as `deps.migrateOAuth` and called at four entry points (TUI, `exec`, ACP,
  auth commands); `serve --mcp` is excluded because it never loads provider
  OAuth credentials.

Confirmed and deliberately not changed:

- `[ ]` **CR2** (docs, not code): the encrypted-file backend keeps its AES-256
  key in a 0600 file beside the ciphertext (`securefile.go:38` says so
  outright). That is honest best-effort encryption — it defeats casual reads
  and shared-session leaks, not a same-user attacker who can read both files.
  The risk is a user reading `credentials.enc` as equivalent to keychain
  protection. Worth stating in user-facing docs; the code already states it.
- `[x]` **CR3** (`0c1e138`): on macOS the key is passed to `security add-generic-password`
  as an argv argument, so it is briefly visible in the process table. Linux
  uses stdin. Only the Linux line carries the comment, so the macOS exposure is
  inferred by contrast rather than stated — worth a comment at
  `keyring.go:67`.
- `[x]` **CR4** (`0c1e138`, and it was a latency regression not a smell — the
  per-request store construction became a 24.5 ms keychain subprocess once the
  OAuth default hardened; the account is now resolved once per provider):
  `internal/providers/factory.go:316` built a
  default-path OAuth store inside a library accessor. Harmless now that `Load`
  cannot mutate, but production code reaching for a default credential path is
  the shape that armed the incident above.
- `[x]` **CR5** (`ee5a404`, CI `31826231922`): setup lost the stored-key
  marker when it updated an existing same-name provider. A red test reproduced
  the npm `0.1.4` failure through the public `dev` save path. The key reached
  the temporary encrypted store, and the model changed, but `apiKeyStored`
  stayed false. `UpsertProvider` now promotes the marker after a successful
  credential write. The general profile merge stays unchanged, so project
  config cannot reactivate a stale stored key. The test also proves that no
  inline key reaches `config.json`. Full root tests, race tests, build, vet,
  memd tests, and an independent review passed.
- The config plaintext-migration window and redaction's known-format coverage
  were both reviewed and judged adequate before CR5; no other action.

## MVP integration waves (2026-08-14)

The owner approved three gated waves on public `dev`. Keep one checkpoint per
commit. Push each checkpoint and wait for CI before the next checkpoint.
Do not start Tracks T, PC, LN, SD10-SD15, or a release without new approval.

- `[x]` **Wave 1**: split and land the pending user-visible work.
  `6df95c5` retains specialist tool activity in transcript cards and across
  resume. `a4e257e` retains `ask_user` questions and answers in
  `MapDesignHistory`. A VHS capture verified the specialist card. The first
  CI run exposed an existing Linux-only over-broad permission-event test.
  `2b11f28` scoped that test to write events. CI run `31761416355` passed.
- `[x]` **Wave 2**: landed MD2, SD7, SD6, SD8, SD9, WL1, and WL2 in that
  order. Each item has its own public checkpoint and successful CI run. SD8
  and SD9 stayed local to `run.go`; SD11-SD12 remain separate.
- `[x]` **Wave 3**: completed real-provider TUI, exec, and worktree smoke
  tests with OpenRouter Kimi K3. No credential value was recorded.
  - `[x]` `110a64a` (CI `31813231935`): one Esc on an active `ask_user`
    questionnaire cancels the run. It clears the answer and run state without
    a partial submission. A provider-backed test proves context cancellation
    releases the blocked agent loop. All questionnaire footers now state
    `esc cancel run`.
  - `[x]` `f274921` (CI `31821825233`): live acceptance found Kimi K3 metadata
    with output equal to its full context window. Every request sent that full
    output cap and failed before inference. The provider factory now omits an
    unusable wire cap and keeps valid registry caps unchanged.
  - `[x]` Plain exec completed the trivial `code_writer` in one iteration. It
    reported 1,422 input and 221 output tokens with complete stage attribution.
    No budget abort occurred.
  - `[x]` Worktree exec completed in one iteration. Merge-back reported
    `merged` for `splice/smoke1`. The branch remains and the managed worktree
    directory was removed.
  - `[x]` The real-provider TUI read `main.go`, answered correctly, and touched
    no files. Capture: `/tmp/splice-wave3-tui-live.gif`.
  - Observation: both write runs also changed `fmt.Println("hi")` to
    `fmt.Println("Hello, world!")`. The second prompt prohibited other line
    changes. Lifecycle and accounting passed; model instruction fidelity did
    not. Keep this evidence separate from deterministic harness acceptance.

## Track WL: worktree lifecycle (2026-08-13)

Source of truth: `plans/audit-and-direction-2026-08-13.md` (section 3, decision
D4). The worktree subsystem passed a full end-to-end verification against real
git (prepare/reuse, capture/restore, all four merge-back statuses, the
ancestry edge case). The defect is lifecycle: no removal code exists anywhere,
and this machine had accumulated **690 stale worktrees (12 GB)** since
2026-07-27. Deletion policy: automatic only where deletion cannot lose work;
the sweep criterion is clean + HEAD reachable from a surviving ref, never age
alone; dirty worktrees are reported, never auto-deleted.

- `[x]` **WL1** (`871dc41`, CI `31770363110`): remove the worktree after
  merge-back returns `merged` or `no_changes`. Removal uses `git worktree
  remove` without force. Dirty worktrees survive. Cleanup failure warns with
  the path but does not change an already-successful run and merge exit.
- `[x]` **WL2** (`efcd1a5`, CI `31810110002`): add `splice worktrees prune`
  and the same opportunistic sweep to `Prepare`. Only direct, registered
  Splice worktrees qualify. Active worktrees use the native Git lock. Removal
  requires no tracked, untracked, or ignored files and a HEAD that remains
  reachable from source HEAD or a `splice/*` branch. All other managed
  worktrees stay in place and appear in the report.
- `[ ]` **WL3**: find and fix the paired creation: the stale list is dominated
  by worktrees created one second apart, which suggests something calls
  Prepare twice per run. Eval harness is the suspect; unconfirmed.
- `[ ]` **WL4** (optional): prune `refs/splice/recovery/*` refs alongside the
  worktree they belong to.

## Track SD: stage decoupling (2026-08-13)

Source of truth: `plans/audit-and-direction-2026-08-13.md` (sections 1 and 4,
decision D1). Audit verdict: the execution frame is dynamic (roster-driven
loop, typed `Stage` interface, config-driven model resolution) but the policy
is compile-time (tier tables, budgets, trajectory weights and rule chain,
name-keyed capability switches). SD1-SD5 and SD10-SD13 prepare Track T's
topology compiler and Tracks PC/LN. SD14 is a later live enforcement finding.
The Track RR defect shape (produced, sometimes validated, never consumed)
appears in `ModelTier`, `DependsOn`, `TypeErrors`, and `OutputMax`.

- `[x]` **SD1** (`5f0cdf4`, CI `31919004721`; follow-up `7f67099`, CI
  `31920589697`): deleted `StageBudget.ModelTier`. The field was written and
  validated, then read by no resolution path. Zero versus both-positive budget
  shape remains. The later follow-up closed the rest of the fossil family:
  crystallizer `recommended_tier` / `recommended_model_tier`, and unused
  `Task.EstimatedTier`. Old persisted `estimated_tier` values still parse and
  are ignored. The classifier is the only remaining tier authority.
- `[x]` **SD2** (`4c90115`, CI `31919423164`): each registry stage now declares
  `Capabilities()` for model-free, memory, context, and timeout. The
  orchestrator reads those declarations instead of four name-keyed switches.
  A pairing test matches the legacy outcomes. `extractWriteObservations` still
  keys `test_runner` observations by name.
- `[x]` **SD3** (`a200452`, CI `31922234601`): `EvaluateTrajectory` now
  walks a named rule slice. First non-nil decision wins. Thresholds,
  reasons, and evidence stay the same. A pairing test pins the order:
  iteration limit, token budget, oscillation, cycle, rollback, step-back,
  then confidence.
- `[x]` **SD4** (`c0a6991`, CI `31922546244`): `ComputeIterationState` now
  reads a keyed extractor table. A pairing test fails when a registry stage
  produces trajectory data without a parser. `test_generator` is listed as
  irrelevant. Deleted unused `IterationState.TypeErrors`, its score term,
  and the public type-error score row.
- `[x]` **SD5** (`b316ee5`, CI `31919055111`): deleted `ExecutionStage.DependsOn`.
  The field was always nil and the executor ignored it. Design-task `DependsOn`
  is unchanged. Track T can introduce a real edge type later.
- `[x]` **SD6** (`6a2f27c`, CI `31765161488`): separated the pipeline pass
  cap from agent-loop `MaxTurns`. Pipeline runs now use the fixed five-pass
  cap for trajectory checks and stage-failure recovery. `--max-turns` no
  longer changes that cap. The public pipeline guide states this boundary.
- `[x]` **SD7** (`5c39db2`, CI `31763551557`): fixed budget accounting in
  `tokensConsumed()`. Total usage is input plus output. Cache-read and
  cache-write tokens are input subsets. Reasoning tokens are an output subset.
  The old total double-counted cache reads, so budget aborts could fire early.
- `[x]` **SD8** (`6d6a407`, CI `31766787474`): the pipeline now derives one
  absolute 600-second deadline. It checks that deadline before each pass and
  stage. An expired in-pass deadline aborts the run, retains completed stage
  records, and does not start the next stage. Parent cancellation wins.
- `[x]` **SD9** (`8eb1561`, CI `31768162053`): made active stages obey the
  absolute pipeline wall deadline. Enforcement-point review corrected the old
  premise: `TimeoutSeconds` bounds deterministic subprocesses only. LLM stages
  never used the fixed 120-second value, so no ungrounded TPS timeout was added.
  Parent cancellation wins; a live-parent wall expiry aborts the run.
- `[x]` **SD10**: closed by SD12. Pipeline tool calls now dispatch
  beforeTool/afterTool through the shared agent helpers. Not done as a
  standalone commit.
- `[x]` **SD11** (`8cf01bd`, CI `31897503717` failed on a string-search
  false positive; fixed in SD12): `PipelineRunConfig` holds only the fields
  the pipeline consumes. Public `Run` and design-plan entry points still
  accept `agent.Options` and convert in the splice layer. A reflection test
  fails CI when a new `agent.Options` field is neither copied nor listed in
  `pipelineIgnoredAgentOptionReasons` with a non-empty reason.
- `[x]` **SD12** (`fa1727a`, CI `31898960035`): shared filter, hook, and
  `RunOptions` helpers live in `internal/agent/tool_policy.go`. The pipeline
  runner now denies `DisabledTools`, dispatches beforeTool/afterTool, and
  forwards Progress, OnToolOutput, and Diagnostics. Auto and spec-draft still
  grant prompt-gated tools. Acceptance tests cover a disabled bash denial and
  a beforeTool hook firing on a pipeline tool call. The pairing test now
  classifies Hooks and the filters as consumed.
- `[x]` **SD13** (`5b4f2ee`, CI `31924835596`): typed `stage` events are now
  the documented contract. `emitStageEvent` fires `OnStageEvent` and still
  writes the NUL marker for one release. Live TUI and stream-json exec consume
  the typed event. Marker parsing remains for resume and old sessions.
- `[x]` **SD14** (`7fbda8a`, CI `31860137850`): each model-backed
  `StageBudget.OutputMax` now sets a per-request output cap. The cap flows from
  the typed execution stage through `StageOptions` and `CompletionRequest` to
  OpenAI, Anthropic, and Gemini wire requests. The provider uses the smaller
  positive value when a configured default and request cap both exist.
  Typed-output retries keep the same cap. Anthropic thinking shrinks or turns
  off when it cannot fit below the stage cap. Run and adapter tests prove that
  an 8,192 stage budget sends 8,192, while zero keeps the provider default.
  The `f274921` registry clamp remains unchanged.

- `[x]` **SD15** (`901d99f`, CI `31858201582`): wired
  `OnSurfaceToUser` into the TUI through the existing `ask_user` prompt. The
  prompt shows the reason, evidence, and recent confidence values. Nonempty
  guidance becomes the next revision context. Empty input or cancellation
  aborts. Headless exec keeps the declared nil-callback abort policy. The
  trajectory reason now states only the observed confidence decrease. Run
  tests prove continue and abort behavior. TUI tests prove prompt content,
  answer mapping, and cancellation. Capture:
  `/tmp/splice-sd15-surface-to-user.gif`.

- `[x]` **SD16** (`ac07c49`, CI `31861993067`; follow-up `095b1ac`, CI
  `31921814741`): cycle evidence now includes a compact verification failure
  signature. Matching non-zero signatures say the environment or verifier may
  be stuck. Changing signatures say model thrash is more likely. All-zero
  signatures use a third reason: a no-op pass or thrash against a non-verifying
  gate. Tests cover all three cases.

- `[x]` **SD17** (`4d69ea2`, CI `31863728901`): quality checks now pass a
  joined string to the bash tool. The veritas-cache bench proved that
  `quality_python` never ran `py_compile` in real runs: it sent
  `{"command": []string}` and the bash tool rejected it with
  `command must be a string`. The same array leak existed in the Ruff, Node,
  and tsc paths. Permissive stage mocks hid it. Quality tests now use a
  schema-enforcing fake that rejects a non-string command, and a real bash
  smoke covers the Python path.

- `[x]` **SD18** (`9dccad0`, CI `31924117395`): a no-progress brake sits
  after cycle detection and before rollback. Three empty workspace passes
  request one step-back. A later empty stretch aborts with
  `abort_no_progress`. Rollback, plateau, and confidence cases still show
  real file changes so they keep winning. Tests cover first fire, second
  abort, and a normal changing run.

## Track MD: memd hardening (2026-08-13)

Source of truth: `plans/audit-and-direction-2026-08-13.md` (section 9). Five
defects confirmed by the independent reviewer session's read-only audit with
reproductions; three spot-checked against the code here before recording.
Blocks nothing, but LN1's trace writes will hit MD1 and MD2 immediately.

- `[x]` **MD1** (`6e5208d`, CI `32040532971`): key memory identity on the
  stable repo root, not the cwd. Shipped; checkbox was stale.
- `[x]` **MD2** (`0ae201f`, CI `31762862788`): fixed the daemon startup
  race. `busy_timeout` now precedes the WAL switch. The modernc WAL switch
  bypasses the busy handler, so `Store.New` retries only SQLITE_BUSY with a
  short bound. `TestConcurrentOpen` covers four simultaneous cold openers.
- `[x]` **MD3** (`bf2809d`, CI `31862382229`): search queries with NUL or
  other C0 controls now fail validation with HTTP 400. Tab and newline stay
  valid. The FTS5 path no longer receives an unterminated string.
- **MD4 / MD5** routed to Track FND (2026-08-19): key-consistency
  and evidence-layer availability.

## Track PC: plan composer (2026-08-13, extends Track T)

Source of truth: `plans/audit-and-direction-2026-08-13.md` (decision D2).
One plan schema, two authors: Track T gives the human author
(`pipeline.json`); Track PC adds the LLM author emitting the **same** schema,
validated and user-confirmed before execution. The LLM is a plan compiler,
never a runtime orchestrator. Gated on Track T core (T1-T8) because it emits
the Track T schema.

- `[ ]` **PC1**: composer stage that turns the design conversation into a
  topology in the Track T schema; schema validation plus user confirmation
  before any execution.
- `[ ]` **PC2**: re-planning as a trajectory action: on oscillation/cycle
  detection the monitor can hand its evidence back to the composer for a
  revised plan, instead of only abort/rollback.
- `[x]` **PC3** (`084211e`, CI `32063395849`): retrieval-augmented composition:
  the composer retrieves similar past run traces (Track LN) as exemplars, so
  composed plans improve per repo with use. Shipped as the retrieval half:
  kept-run exemplars join stage memory bundles (top 3 by bm25, deterministic
  score gate, verdict=kept INNER join, repo-scoped).

## Track LN: learning loops (2026-08-13)

Source of truth: `plans/audit-and-direction-2026-08-13.md` (sections 2 and 4,
decision D3). Deterministic verification is the reward signal; learning
replaces hardcoded policy, not the deterministic substrate. memd's schema is
ready for this (provenance, confidence, dedupe) but stores no outcomes today:
three memory types, one read site, nothing to learn from. Ladder order is
mandatory; no rung starts before the one below it works.

- `[x]` **LN1** (`60ddf34` + `ed1256f`, CI `32044673642`, `32045227460`):
  persist a `run_outcome` trace at run end: plan, tier,
  per-stage outcomes, trajectory scores and verdict, tokens actual vs
  budgeted, per-stage latency and derived per-model TPS (from
  `StageRecord.LatencyMs`, which is recorded today and discarded), pass/fail,
  and the user-kept-the-diff bit (without this bit the later rungs tune
  toward the wrong target). Independent of SD; cheap; immediately useful for
  debugging.
- `[x]` **LN2** (`8610484`, CI `32047530317`): budget calibration from traces
  (actual vs allocated per stage
  per repo), with visible provenance in the run report (fail-loudly: show the
  history the adjustment came from). Needs structured aggregation, not FTS.
- `[ ]` **LN3**: learned skip policies and trajectory-weight fitting from
  recovered-vs-spiraled traces. Depends on SD3/SD4 (policy must be data to be
  tuned).
- `[ ]` **LN4** (deferred): learned topology. The harness learns and proposes
  a pipeline diff between runs (never mid-run, never silent, user-approved;
  verifier/auditor gate stages cannot be removed by learning alone).
  Re-decided 2026-08-17 in
  `plans/adaptive-harness-learning-design-2026-08-17.md` as a new track,
  superseding the earlier "train a composer/classifier" reading. Dead last,
  after LN1-LN3: the most corpus, the most oversight.

## Track LX: learning extensions (filed 2026-08-18)

Source of truth: `plans/adaptive-harness-learning-design-2026-08-17.md`.
Decided components of the adaptive harness that sit beyond the LN1-LN4
ladder and the adoption record's SD/MD/T mapping. LX1, LX2, LX3b, and LX4 are
gated on the LN3 + PE verdict corpus. LX3a is baseline measurement only and
can start after its trace and PE inputs are trustworthy. No adaptive policy
changes in LX3a.

- `[ ]` **LX1: cross-project transfer.** Two channels (`model_priors`,
  `user_profile`), separate consume/contribute switches per channel (a client
  repo can consume without contributing). Project-scoped by default;
  `repo_root` in the key; sessions are irrelevant to learning. Priors are
  weak, always overridden by the project's own floor, with loud provenance
  (`"budget from prior (4 repos), 3/20 local runs"` flipping to local
  calibration at floor crossing). Pure functions; fabricated-corpus table
  tests.
- `[ ]` **LX2: adaptive tooling (the ratchet).** Fixed per-stage tool sets
  die. Replacement: user envelope (ceiling, from config) + learned downward
  narrowing (per-stage tool stats from traces) + expansion-as-escalation
  (stage requests; only the user grants; every expansion is a traced
  intervention). No mechanism grants itself power. Narrowing doubles as a
  capability feature for local models (3 tools beat 25).
- `[ ]` **LX3a: baseline efficiency and burden dashboard.** Use trustworthy
  RunOutcome and PE data. Report operational tokens, priced cost, latency,
  retries, model calls, learning/evaluation cost, amortized total cost, and
  weighted interventions per kept successful task. Separate comparable cold
  and warm runs. Show kept-rate and deterministic success beside every trend.
  Add missing producers and pairing tests before claiming coverage. Report an
  unavailable cost component as unavailable, never zero. This layer is
  observational, changes no policy, and makes no causal claim.
- `[ ]` **LX3b: paired promotion and living rollback guard.** Gated on the
  LN3 + PE verdict corpus. Compare fixed-policy and adaptive-policy arms with
  one changed variable. Decide in this order: evidence floor, success
  tolerance, amortized cost margin, management burden, then incumbent on an
  inconclusive result. Treat a material success gain with higher cost as an
  explicit capability tradeoff, not a cost win. Require user approval before
  promotion. Bypass caches in the paired harness. Revert when lower cost or
  burden comes with a correctness regression. Use explicit floors, tolerances,
  and margins, not p-values.
- `[ ]` **LX4: red-capability testing.** Two decided behaviors: (a) editing
  or deleting a preexisting failing test is a trajectory red flag (named
  signal; step-back or surface; no run weakens the gate that judges it); (b)
  sampled red-capability checks: authored tests run against the pre-change
  tree, and tests that pass there too are vacuous, traced as an authored-test
  quality signal that LN3 discounts.

Not filed (deferred or owned elsewhere): training local models (offline,
user-invoked, theoretical ceiling) and the Veritas cache (owned by the
semantic-cache session; splice shipped only the `X-Veritas-Bypass` header
contract, `d8dc747`).

## Track TW: TUI worktree execution (2026-08-13)

Source of truth: `plans/tui-worktree-execution-2026-08-13.md`. The trajectory
rollback is headless-only because the TUI passes nil recovery; the fix is
running the TUI's pipeline inside a prepared worktree and merging back, not
destructive recovery in a live checkout. Six seams decided in the plan:
session identity (`origin_cwd`), memory identity (depends on MD1), ignored-
file seeding (explicit, no magic), sandbox trust domain (fail-closed v1),
per-worktree LSP manager, and merge UX that treats skipped_dirty/conflict as
first-class outcomes. Live sessions hold `git worktree lock`; WL2 must skip
locked worktrees.

- `[x]` **TW1** (`f265ce9`, CI `31928290030`): TUI pipeline runs prepare a
  locked worktree and execute there. Design and spec-draft stay in the live
  checkout. A failed or disabled prepare falls back with a one-line notice
  that rollback is unavailable. The sidebar and model line show a compact
  `wt:<name>` chip.
- `[x]` **TW2** (`f03d163`, CI `31972452523`): TUI pipeline runs in a worktree
  pass `worktrees.NewIterationRecovery` at the `tuiSpliceRun` call. A rollback
  trajectory decision restores the best snapshot. Live-checkout fallback keeps
  nil, so rollback still aborts with the isolated-worktree reason. Design and
  spec-draft runs are unchanged.
- `[x]` **TW3** (`1ba00dd`, CI `31954292809`): post-run review surface.
  Accept merges and removes the worktree. Reject pins `splice/<name>`, then
  removes the worktree. Keep leaves the path. A dirty main checkout hides
  Accept and refuses merge. Esc keeps. The lock is released on every exit.
  Remaining: WL2 skip-locked coordination.
- `[x]` **Reject-reason tap** (`55b7958`, CI `31973024465`): Reject at the
  review asks one follow-up question (wrong_approach / still_failing /
  changed_mind / other). Esc or empty means unspecified and still removes.
  The decision and reason land on a `worktree_review` session event.
- `[x]` **TW demo** (`0efd91e`, CI `31957101861`; re-cut `a27d8da`, CI
  `31971161314`): VHS tape `scripts/tui-worktree-reject.tape` and GIF
  `docs/assets/tui-worktree-reject.gif` (10s, full reject outcome in-frame).
  `SPLICE_TUI_DEMO=worktree-reject` swaps only `tuiSpliceRun`. The tape types
  `/exec`. Worktree prepare and the review stay on the real path. Demo env is
  keychain-free.
- `[x]` **TW4: session identity** (`b537c96`, CI `32162579962`): `origin_cwd`
  metadata, picker labeling, resume from the main repo. Split into:
  - `[x]` **TW4a**: add `OriginCwd` to `sessions.Metadata` and `CreateInput`
    (json `originCwd,omitempty`); persist at create, inherit on fork/child.
  - `[x]` **TW4b**: record `origin_cwd` = source repo root at worktree-run
    prepare (execution `Cwd` stays the worktree). `sessionMatchesWorkspace`
    and its 3 call sites (`session.go:394`, `session.go:520`,
    `command_center.go:275`) accept either `Cwd` or `OriginCwd`.
  - `[x]` **TW4c**: picker labels worktree sessions (`wt:` marker when
    `OriginCwd != ""`); resume from the main repo re-enters worktree mode via
    the persisted toggle. Extend `resume_scope_test.go` for origin matching.
- `[ ]` **TW5: seeding and trust spike** (`.splice/worktree-seed` convention,
  sandbox trust-inheritance flag default off). Split into:
  - `[x]` **TW5a**: `.splice/worktree-seed` convention. Optional file in the
    source repo (RepoRoot) naming paths to copy (`.env`-style) or symlink
    (`node_modules`-style) into the worktree at `worktrees.Prepare` time.
    Explicit, no magic; a missing seed runs and fails honestly. Real-git test
    in `internal/worktrees/worktrees_test.go`.
    Shipped `d1ac44c`, CI `32191731114`.
  - `[x]` **TW5b**: sandbox trust-inheritance spike. A flag (default off) so
    a worktree of a trusted repo inherits trust, proven via common-dir.
    Fail-closed re-prompt stays the default. Spike only; off by default.
    Shipped `d1ac44c`, CI `32191731114`.

## Track LL: local LLM integration (2026-08-18)

Source of truth: `plans/local-llm-integration-2026-08-18.md`. Local LLM support
is strong at the transport layer (two first-class local providers, auto-detect,
no-key onboarding, context discovery, heavy quirk-handling) and weak at the
capability-metadata layer. The track adds live capability discovery, an
installed-model picker, local reasoning-effort mapping, and a deterministic
weak-model tool floor. The learned version of tool narrowing is LX2 (already
filed); LL4 is the deterministic floor, not a duplicate.

- `[x]` **LL1: live capability discovery.** Extend the Ollama `/api/show` probe
  to also return `capabilities` (tool-calling, vision) + `template`; add
  `/api/tags` (installed list) and LM Studio `/v1/models` harvest. Probed
  tool-calling/reasoning/context land on the ModelRegistry entry. Gate:
  `go test ./internal/providermodeldiscovery/`.
  Shipped `7c661bd`, CI `32210468650`. `Registry.OverlayCapabilities` re-registers
  under id/api-model/aliases. The overlay is additive, so the negative signal
  moves to LL4; wired on the headless exec path only.
- `[x]` **LL2: installed-model selection.** The probed installed list becomes
  the wizard's pick list (the catalog default is only the fallback).
  Gate: `go test ./internal/tui/ ./internal/provideronboarding/`.
  Shipped `7c661bd`, CI `32210468650`. `DetectedLocalRuntime.InstalledModelIDs`
  surfaces the list the wizard discarded; `resolveModelSwitchTarget` promotes an
  installed id to its full registry entry.
- `[x]` **LL3: reasoning-effort mapping for local reasoning models.**
  deepseek-r1 / qwq / qwen3-think carry standard reasoning efforts on their
  registry entry; the OpenAI-compat path sends `reasoning_effort` plus the
  Ollama `think` toggle where accepted. Gate: `go test ./internal/providers/openai/`.
  Shipped `b89494f`, CI `32213788355`. Decided against a `think` field: Splice
  talks `/v1/chat/completions` and `think` is native `/api/chat`. Fixed a latent
  drop where an explicit `--reasoning-effort` on a local reasoning model was
  silently discarded (empty efforts resolved to none). Stays opt-in.
- `[x]` **LL4: negative capability signal.** A probed tool-less local model must
  be able to contradict `withBaseCapabilities()`'s blanket tool-calling grant so
  the existing preflight warn can fire. Requires a tri-state probe: an absent
  `/api/show` capabilities array (older Ollama) is unknown and must NOT read as
  a negative, or every model on an older daemon loses tool-calling. Split:
  **LL4a** tri-state probe + surgical capability removal at the existing exec
  probe site (preflight itself needs no change); **LL4b** the TUI overlay LL1
  deferred, via the injected-func seam (`tui/options.go:39`), not a direct
  import. Gate: `go test ./internal/providermodeldiscovery/
  ./internal/modelregistry/ ./internal/splice/ ./internal/tui/`.
  Shipped `5013996`, CI `32252292395`. `OllamaCapabilities.Reported` +
  `Registry.RemoveCapability`. Blast radius of a wrong negative verified bounded:
  `stage_tier_resolver.go` returns early for OpenAI-compatible providers, so
  local model selection is untouched; effects are the preflight warn, a hidden
  tools badge, and onboarding-list filtering (still reachable by typing).
  **Re-scoped 2026-08-19:** tool-set narrowing dropped from LL4. A model that
  cannot tool-call is not helped by fewer tools, and narrowing weak-but-capable
  models needs a weakness signal the capability list does not provide. That is
  LX2's learned ratchet.
- `[ ]` **LL5 (optional, later): hardware-fit hint.** Using the probed context
  window, warn on a likely RAM/VRAM misfit. Gate:
  `go test ./internal/provideronboarding/`.

## Archived: Python-era (Flug) roadmap

The pre-Go Python-era roadmap and older track notes are preserved in git
history. See revisions of `ROADMAP.md` and `MEMORY.md` before 2026-07-08 for
the original Flug Tracks V, M, W, S, P, O, U, and R checkpoints.
