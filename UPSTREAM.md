# Upstream Divergence Notes

> **Current state (as of 2026-07-18):** 155 commits on `main` ahead of
> upstream, 0 behind. The Splice-specific work is ~18,050 lines of original
> Go in `internal/splice/` (70 files): a deterministic, schema-as-contract
> pipeline layer ported from the Python Flug prototype, with a classifier,
> planner, trajectory monitor, budget, context builder, tool-registry
> runner, per-stage model routing, escalation resolver, design phase,
> workspace recovery, and specialized stage agents (code writer, static
> analyzer with Go/JS/Python/TS quality modules, test generator, test runner,
> security auditor with Bandit/GoSec/SARIF/Trivy integrations, plan critic,
> step-back, verification). The inherited Zero engine (TUI, 25+ provider
> catalog, sandbox, tool registry, worktrees, base `agent.Run` loop) is
> retained as the substrate; Splice layers the pipeline on top of it rather
> than re-implementing the engine. See the sections below for the precise
> landed divergences.

This repository (`Taf0711/splice`) was not forked on GitHub. It is a full
clone of `github.com/gitlawb/zero` with Splice-specific changes applied on
top. This file documents every divergence so upstream merges stay
mechanical.

## Base import

- **Upstream URL:** `https://github.com/gitlawb/zero.git`
- **Base commit imported:** `008bc9b` (PR #481, 2026-07-06)
- **Local clone path:** `your local splice clone`
- **Upstream remote:** `git remote add upstream https://github.com/gitlawb/zero.git`

## Rename / rebrand applied everywhere

- Module path changed from `github.com/Gitlawb/zero` to
  `github.com/Taf0711/splice` in `go.mod` and all imports (~449 Go files).
- Product name changed from `Zero` to `Splice` in user-visible strings,
  system prompt, config/data directory names, and install scripts.
- `cmd/zero*` binaries renamed to `cmd/splice*`. Helper binaries renamed:
  `splice-linux-sandbox`, `splice-seccomp`,
  `splice-windows-command-runner`, `splice-windows-sandbox-setup`,
  `splice-perf-bench`, `splice-pr-review`, `splice-release`.
- Hardcoded helper name lists updated in:
  - `internal/update/apply.go` (lines 37-38)
  - `internal/release/release.go` (lines 276-290)
- CI workflow names and npm wrapper references updated; npm `bin` entry now
  `"splice"`.

## CI / workflows

- Deleted Zero's release/review/action workflows:
  `release-please.yml`, `release-artifacts.yml`, `pr-auto-review.yml`,
  `zero-action-smoke.yml`.
- Replaced with minimal `ci.yml`: build, vet, and `go test ./...`.
- `cmd/splice-pr-review` and `internal/review/` deleted: the helper was
  orphaned when `pr-auto-review.yml` was removed in F1; the binary was the
  sole importer of the package.
- Four zero-reference TUI helpers deleted: `scrollableTranscriptView`, `transcriptViewportStartForLayout`, `WatchPRState`, `GetLocalDiffStats` (ponytail-audit 2026-07-17 findings 3/M1/M2).
- Ponytail-audit finding 2 (AU11): eleven test-only TUI helpers removed from the prod surface (renderSelectableList deleted with its tests; transcriptBody, transcriptViewportStart, listCommandNames, renderMarkdownInline, overlayMouseTop, retainedCharacters moved into _test.go; completePathQuery, formatCommandHelpLines, chatMaxScrollOffset, toolBodyRendererFor call sites rerouted to live internals) plus likelySandboxDenied in internal/tools rerouted to sandboxDenialKind.

## System prompt

- `internal/agent/system_prompt.go` and `internal/agent/system_prompt.md`
  now say "You are Splice".
- Corresponding test assertions updated in
  `internal/agent/system_prompt_test.go`.

## New Splice-specific packages

- `internal/splice/` — deterministic pipeline layer ported from Python
  Flug. Contains classifier, planner, budget, trajectory monitor,
  context builder, registry runner, the stage agents
  (`internal/splice/stages/`, F6), the `splicerun.Run` orchestrator
  (`run.go`, F7), and the design runner (`design_runner.go`, F8a).
- `internal/splice/schemas/` — Go structs and validations.
- `internal/memd/`: HTTP-over-Unix-socket client for the memory sidecar
  (F9b, complete).
- `memd/`: the `splice-memd` memory sidecar, a separate Go module
  (`github.com/Taf0711/splice/memd`) imported from the Flug archive and
  rebranded in F9a. Invisible to the root `go test ./...`; covered by its
  own `memd` CI job (vet, test, build).

## Divergences in inherited code (landed)

- **`agent.Options` gained `StageModelResolver` (AR11b).** The options struct
  now carries a `StageModelResolver func(stageName string) (Provider, string, string, error)`
  field that the `splicerun.Run` orchestrator calls before each model-backed
  stage to enforce per-stage provider, model, and reasoning-effort routing in
  the Splice pipeline. F14a skips this resolver for static analysis, security
  audit, and test execution because those stages are model-free. Headless exec
  and each non-spec TUI run load `stage-models.json`
  next to the user config and build the hooks through the shared Splice helper
  in `internal/splice/model_routing.go`. When nil
  or when the resolver returns a nil provider, the run falls back to the
  default provider and `options.Model/ReasoningEffort` (byte-identical to
  pre-AR11 behavior).
- **`agent.Options` gained `EscalationModelResolver` (AR10c).** The options
  struct now carries an `EscalationModelResolver func() (Provider, string, string, error)`
  field that the deterministic pipeline orchestrator calls at most once when
  the trajectory monitor fires `ActionEscalateCycleDetected` or
  `ActionEscalateOscillation`. On success, the default provider and
  options.Model/ReasoningEffort are swapped for all subsequent iterations.
  Exec and TUI build the resolver from the `"escalation"` entry through the
  shared stage-routing helper, using the same provider profiles and cache as
  the per-stage resolver. When nil, returns a nil provider, or returns an error,
  escalation is skipped with a progress note (best-effort, non-fatal), keeping
  the pre-AR10c behavior.
- **`agent.Options` gained `OnSurfaceToUser` (AR10d).** The options struct now
  carries an `OnSurfaceToUser func(ctx context.Context, req SurfaceToUserRequest) (SurfaceToUserDecision, error)`
  callback. When the trajectory monitor fires `ActionSurfaceToUser` (strictly
  decreasing confidence), the orchestrator calls this callback instead of
  silently retrying. When nil (headless exec, no interactive user), the pipeline
  aborts with a clear message. New types `SurfaceToUserAction`, `SurfaceToUserRequest`,
  and `SurfaceToUserDecision` are added to `internal/agent/types.go`.
- **`agent.Run` call-site swaps (F7, F12b).** Headless exec
  (`internal/cli/exec.go`) and non-spec-draft TUI runs
  (`internal/tui/model.go`) drive `splicerun.Run` with the full
  `agent.Options` callback contract. Spec draft
  (`internal/cli/exec_spec.go`) intentionally stays on Zero's `agent.Run`
  loop.
- **`/stages` TUI wizard (F12c).** The inherited command and modal-routing
  surfaces in `internal/tui/` now open a Splice-owned overlay for editing
  `stage-models.json`. Its target editor copies inherited Zero menu semantics:
  Up/Down traverses rows and Enter opens nested provider/model/effort pickers;
  model choices reuse the existing saved-provider catalog/live-cache path and
  the inherited `commandPicker` ranked search behavior. Provider-wizard modal
  guards are mirrored so composer, sidebar, mouse,
  clipboard, autocomplete, and transcript input cannot leak through while the
  stage wizard is open. F12d reloads the saved file before every non-spec TUI
  prompt, so edits affect the next deterministic pipeline run without restart.
  F12e adds a real Bubble Tea feature test that temporarily restores the
  production `splicerun.Run` seam and verifies the complete routed workflow;
  the package-wide inherited agent-loop fixture remains for unrelated tests.
  F14a limits editable built-in targets to the model-backed code writer and
  test generator. Existing reserved deterministic or design-stage entries stay
  loadable and are preserved on save, but are hidden from the editor.
- **Local model-picker failure guidance (F13).** The inherited `/model` picker
  now shows distinct actionable messages when Ollama or LM Studio is
  unavailable or reports no loaded models. Cloud-provider discovery fallback
  behavior is unchanged.
- **`internal/cli/` exec surface**: new flags `--plan <path.json>` (F8a) and
  `--merge-back` (F8b, requires `--worktree`), with usage guards and tests
  (`exec_plan_test.go`, `workflow_test.go` additions).
- **`internal/worktrees/worktrees.go`** (AU9): `gitOutput` and
  `defaultRunGit` bodies deduplicated into `commandOutput` and a
  `defaultEnvRunGit` delegate (both live in Splice-owned `recovery.go`).
- **`internal/worktrees/worktrees.go`**: added `MergeBack` (commit worktree
  work, pin `splice/<name>` recovery branch, `--no-ff` merge into the source
  repo, statuses `merged` / `no_changes` / `skipped_dirty` / `conflict`) plus
  a real-git test suite (F8b).
- **`docs/STREAM_JSON_PROTOCOL.md`**: additive `tool_call_start` /
  `tool_call_delta` event types, pipeline execution model, plan-mode events,
  and merge-back outcome events (F7d, F8a, F8b).
- **`internal/tools/delete_file.go` (AR1)**: a new scoped, prompt-gated
  `delete_file` write tool added to the inherited Zero tool substrate. It is
  registered in `CoreWriteToolsScoped`, covered by `MutationTargets` so the
  session rewind layer snapshots deleted paths, and rendered in the TUI as a
  first-class delete mutation. Pipeline file application now routes creates,
  modifications, and deletes through the registered write tools and fails the
  stage on any unapplied change, whereas the inherited code silently skipped
  failed writes and never supported deletion.
- **`WorkspaceRecovery` call-site seam (AR10a).** A Splice-owned
  `WorkspaceRecovery` interface is threaded as the final parameter through
  `splicerun.Run`, `RunDesignPlan`, `runExecutionPlan`, and
  `runIterationLoop`. Only the explicit CLI `--worktree` path
  (`internal/cli/exec.go`) constructs a non-nil recovery via
  `worktrees.NewIterationRecovery`. The in-place CLI (no `--worktree`) and all
  TUI paths pass nil. Nil recovery causes a rollback trajectory decision to
  abort the pipeline with an honest message instead of mutating the workspace.

## Sync procedure

```bash
cd your local splice clone
git fetch upstream
git merge upstream/main
# Resolve any conflicts by taking Zero's version, then re-apply the
git diff -G'Gitlawb/zero'  # sanity check rename scope
bash scripts/rename-module.sh
```

Because every upstream merge touches Go files with `github.com/Gitlawb/zero`
imports, the scripted rename in `scripts/rename-module.sh` should be re-run
after each merge rather than resolving import-path conflicts by hand.

## Suggested sync cadence

- Before each Splice release.
- Otherwise monthly.
- Avoid per-commit syncs unless a critical upstream fix is needed.
