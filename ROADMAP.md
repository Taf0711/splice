# Splice Roadmap

Splice is a local-first, deterministic multi-stage coding pipeline forked from
`gitlawb/zero`. It runs as a CLI, passes typed Go schemas (ported from Flug's
Pydantic models) between deterministic stages, and publishes structured
stream-JSON events for headless clients.

> Note: dated plan files referenced as `plans/...` and the `MEMORY.md`
> development log live in the maintainers' private archive and are not part
> of the public repository.

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
- `[-]` **CP6** (deferred): design-phase `Crystallize` separation. Pull
  `Crystallize` out of the `DesignConversation` struct into its own typed
  agent. Prerequisite for independent model routing in the topology editor.
  Ready to plan as a single checkpoint when CP1-CP4 land.

## Future direction: user-configurable pipeline (not scheduled)

Recorded 2026-07-16 from the user's design intent. Not a committed track;
no checkpoint may build toward it without an approved plan.

The concept: the current Splice pipeline (classifier, planner, stage agents,
trajectory monitor as wired by `splicerun.Run`) is the **default** pipeline,
not the final shape. In the near future the user intends to add a way for
users to change the direction and structure of the pipeline itself, in the
spirit of a network-topology editor:

- The user prompts, and Splice opens a local web page GUI.
- The GUI lets the user directly change the models assigned to stages or
  agents, add or remove stages, insert additional steps, and register their
  own custom agents or stages.
- The edited topology becomes the pipeline that `splice exec` and the TUI run.

Consequences for current work:

1. **Reinforce the default pipeline first.** The prerequisite for a
   configurable pipeline is a solid, well-tested default one. Current tracks
   (hardening, cleanup, evals) serve that.
2. **Do not hardwire speculative stage contracts.** In the configurable
   architecture, stage contracts are defined by the pipeline editor, not baked
   into the default pipeline. This is why AU6 cuts the dormant `TechnicalSpec`
   handoff instead of wiring it: any such handoff would be redefined by the
   editor anyway. The same test applies to future "pre-wire it for later"
   proposals: later can scaffold for itself.
3. **Local-first still holds.** The GUI is a local web page served by the
   Splice binary, consistent with commitment #4 (no required external
   services).

When this direction is picked up, it starts with a Planning / Design role
pass: a dated plan under `plans/`, schema-as-contract for the topology
definition, and an explicit decision on how user-defined stages honor the
safety substrate (commitment #8) before any implementation checkpoint.

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
- `[ ]` **F11b**: docs refresh and migration guide from Zero to Splice.
  Interim pass 2026-07-09: `AGENTS.md` rewritten for the Go reality, README
  pipeline/exec sections brought current (EN + ZH), `UPSTREAM.md` planned
  divergences replaced with landed ones, design docs 03/11 marked archived
  with Splice-current status notes. F12a through F12f and F13 are landed. The
  migration guide and a sweep of the remaining Zero-era `docs/` pages are still open.
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
- `[ ]` **F14**: fast deterministic verification. Approved plan:
  `plans/fast-deterministic-verification-2026-07-12.md`. Static quality,
  security, and test stages make zero provider calls; missing analysis is
  explicit rather than reported as clean; blocking findings flow back to a
  bounded repair iteration.
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

## Deferred / known gaps

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
  transition notes; the rename to `SPLICE_` is deferred (see `UPSTREAM.md`).
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

## Archived: Python-era (Flug) roadmap

The pre-Go Python-era roadmap and older track notes are preserved in git
history. See revisions of `ROADMAP.md` and `MEMORY.md` before 2026-07-08 for
the original Flug Tracks V, M, W, S, P, O, U, and R checkpoints.
