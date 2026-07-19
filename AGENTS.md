# Splice Project Context (for AI assistants)

This is the canonical instructions file for any AI coding assistant working in this repo (Claude Code, Cursor, Codex, Aider, GitHub Copilot, or otherwise). Read it before making changes. Tool-specific entry-point files (`CLAUDE.md`, `.cursorrules`) are thin pointers to this file.

## Two Files You Must Read First

1. **This file** (`AGENTS.md`): the rules of engagement, architectural commitments, conventions.
2. **`MEMORY.md`** (maintainer-local, absent from the public repository): append-only development log. Current state, recent decisions, open questions, next steps. Read it at the start of every session, append to it before ending.

> **Maintainer-local files.** `MEMORY.md`, `plans/`, and `docs/audits/` are the maintainers' internal development records and are not part of the public repository. They are referenced throughout this file because Splice is developed by AI assistants across sessions; those references apply to the maintainers' private archive. Outside contributors work from `AGENTS.md`, `ROADMAP.md`, `UPSTREAM.md`, and `docs/`.

Then identify your working role and read the matching section under "Working in a Role: Planner vs Implementer" below. If you are designing or architecting, follow the Planning / Design role. If you are implementing an approved checkpoint, follow the Implementation role.

## What This Project Is

Splice is a terminal coding agent written in Go. It is a full-history clone of the open-source Zero CLI (`gitlawb/zero`, MIT) with a deterministic, orchestrator-mediated multi-stage pipeline layered on top. The pipeline paradigm was designed and first built in Python under the name Flug; that repo is archived at `Taf0711/flug_archive`, and the paradigm was ported to Go here as Track F-Zero.

Two layers, one binary:

- **The Zero substrate**: interactive TUI, session storage, provider adapters (25+), tool registry, sandbox and permission policy, worktrees, MCP/skills/plugins. Upstream code; divergences are tracked in `UPSTREAM.md`.
- **The Splice pipeline** (`internal/splice/`): a request is classified into a tier, turned into a typed `ExecutionPlan`, and run through specialized stages (code writer, static analyzer, test generator, security auditor, test runner) under a deterministic trajectory monitor. Wired into headless `splice exec`; the TUI conditionally runs `splicerun.Run` (for non-spec-draft runs) while the interactive spec-draft flow intentionally still runs Zero's `agent.Run` loop.

Design goals: local-first (no required services; SQLite/FTS5 sidecar for memory), provider-agnostic, deterministic-first, trajectory-aware (no death spirals), token-honest (measured cost ratios against a fair baseline, not claims).

## Status

Track F-Zero is complete through F9e: rebrand (F1a/F1), schemas (F2), classifier/planner/budget (F3), trajectory monitor (F4), context builder (F5, hardened in G5), stage agents (F6), the `splicerun.Run` orchestrator wired into headless exec with the full callback/event contract (F7), design runner plus `splice exec --plan` (F8a), worktree merge-back behind `--worktree --merge-back` (F8b), the memd sidecar rebrand with its own CI job (F9a), the Go sidecar client in `internal/memd` with auto-spawn/no-op-degrade (F9b), orchestrator retrieval injection (F9c), deterministic writes (F9d/F9e), the eval harness with cost/token accounting (F10), structured stage events (F12a), and the PIPELINE sidebar with conditional swap to `splicerun.Run` (F12b). The audit remediation track AR0 through AR10d are also complete. The TUI now runs `splicerun.Run` for non-spec-draft runs; the interactive spec-draft flow intentionally still runs Zero's `agent.Run` loop by design. Current progress and the decision log live in `MEMORY.md`; checkpoint order lives in `ROADMAP.md`.

## Documentation Map

- `README.md`: project overview and pitch (English; `README_ZH.md` is the Chinese mirror, keep them in sync)
- `ROADMAP.md`: Track F-Zero checkpoint plan and known gaps
- `MEMORY.md`: development log (maintainer-local; read at session start, append at session end)
- `UPSTREAM.md`: every divergence from `gitlawb/zero` and the upstream sync procedure
- `AGENTS.zero.md`: Zero's upstream extension guide (project AGENTS.md files, specialists, skills, hooks, plugins), kept for reference
- `docs/STREAM_JSON_PROTOCOL.md`: the stream-JSON event contract for headless clients, including pipeline, plan-mode, and merge-back events
- `docs/` (INSTALL, UPDATE, oauth-subscriptions, GITHUB_ACTION, SPECIALISTS, BENCHMARK, PERFORMANCE, AGENT_EVALS, NPM_WRAPPER_SMOKE): Zero-era operational docs, updated as surfaces change
- `docs/flug-design/01..11`: the archived Python-era design corpus. The architecture *ideas* (schema-as-contract, trajectory monitoring, structured memory, execution worktrees) remain the reference for pipeline work, but file paths and code snippets in them describe the archived Python repo. Where the Go port diverges, the doc carries a "Splice note" in its Status section and `MEMORY.md` records the decision.
- `plans/`: dated implementation plans (maintainer-local archive). The Track F-Zero plan is `plans/flug-on-zero-implementation-2026-07-06.md`.

## Core Architectural Commitments (Do Not Violate)

These are non-negotiable for the pipeline layer:

1. **Agents are typed function interfaces, not chat participants.** Every stage input/output is a Go struct in `internal/splice/schemas/` with a `Validate()` method. No raw strings or maps cross stage boundaries.

2. **The orchestrator is the foreman.** Stages don't pass data directly to each other. `splicerun.Run` receives outputs, decides what the next stage needs, constructs the input struct, and invokes. Summaries flow forward, not raw outputs.

3. **Information minimalism.** Each stage receives only what it needs. The user's verbose original request never propagates past the orchestrator (`DistillRequestIntent`).

4. **Local-first.** No required external services. SQLite (via the `splice-memd` sidecar) and the filesystem only. Optional opt-in remote tracing only.

5. **Deterministic-first.** Anything answerable with code (the tool registry, grep, `go/parser`, `py_compile`, Bandit, test runners, git diff) must be answered with code. The LLM is the last resort.

6. **Provider-agnostic.** All LLM calls go through Zero's provider layer. Never import provider SDKs directly in `internal/splice/`.

7. **Fail loudly.** Malformed payloads, unknown tiers, and empty-state cycles return errors that name the offending data; nothing silently defaults. This parity rule (G2) exists because silent fallbacks in a deterministic pipeline are bugs that look like features.

8. **The pipeline must honor Zero's safety substrate.** `splicerun.Run` accepts `agent.Options` (registry, sandbox, permission mode, file tracker, hooks) and must keep honoring them; bypassing them bypasses the sandbox.

9. **AI work is educational, incremental, committed, pushed, and CI-confirmed at checkpoints.** Do not silently stack features and report after the fact. Before non-trivial changes, state the feature or rule being added, why it exists, and the implementation path. During work, keep a concise running change log in the conversation. Prefer smaller increments with explicit checkpoints, especially when token cost is rising. If planning, priorities, checkpoint order, or scope changes, update `ROADMAP.md` in the same checkpoint so the plan stays current. After every completed checkpoint, run focused local validation when practical, update `MEMORY.md`, make a git commit, push to `origin/main` so CI starts, and wait for GitHub Actions to finish. CI is the authoritative full validation pipeline. Do not require a full local suite by default unless the user asks, CI is unavailable, or the change is risky enough to justify it. The user is using Splice to learn, not to mindlessly vibe code.

Additional rules of the road:

- **Upstream discipline.** Changes to inherited Zero code (`internal/agent`, `internal/tui`, `internal/tools`, and the rest) should stay minimal and be recorded in `UPSTREAM.md`, because every divergence is a future merge conflict. New Splice behavior belongs in `internal/splice/`, `internal/memd/`, `internal/worktrees/` extensions, or thin seams in `internal/cli/`.
- **The memd sidecar is a separate Go module** (`memd/`, module `github.com/Taf0711/splice/memd`). The root workflow's `go test ./...` does not see it; it has its own CI job. The client in `internal/memd/` speaks HTTP over the Unix socket and never imports the sidecar module; the wire contract is the only coupling.

## Style and Conventions

- **Go 1.25+**, single root module `github.com/Taf0711/splice` plus the nested `memd/` module.
- **gofmt is law.** `gofmt -l .` must be empty; run `go vet ./...` before committing. CI runs build, vet, and `go test ./...` (and the memd job runs the same inside `memd/`).
- **Errors are values.** Wrap with `%w` and name the failing input; no panics in library code.
- **No em-dashes in new or actively-edited user-facing strings or documentation.** Archived material in `docs/flug-design/` is exempt. Use periods, commas, or parentheses.
- **Tests**: standard `testing` package, table-driven where natural, `_test.go` next to the code under test. Prefer extending an existing test file over creating a new one; create a new file only for a new package or clearly distinct component. Mock providers at the provider seam (see `execStageAwareProvider` in `internal/cli/` and the mocked-provider tests in `internal/splice/stages/stages_test.go`). Real-git tests are fine where git behavior is the contract (`internal/worktrees/worktrees_test.go`).
- **Focused validation before a checkpoint commit**: `gofmt -l .`, `go vet ./...`, and the focused package tests (`go test -count=1 ./internal/splice/... ./internal/cli/ ...` as relevant). Full `go test -count=1 ./...` when the blast radius is unclear.

## File Layout (Splice-relevant subset)

Zero's full tree is large; this lists what pipeline work touches. Everything under `internal/` not listed here is inherited Zero substrate (agent loop, TUI, tools, sandbox, providers, sessions, MCP, and so on).

```
splice/
  go.mod                        # github.com/Taf0711/splice
  cmd/
    splice/                     # main binary
    splice-*/                   # helper binaries (sandbox, seccomp, release, ...)
  memd/                         # memory sidecar, SEPARATE Go module
    go.mod                      # github.com/Taf0711/splice/memd
    main.go                     # splice-memd entry point
    protocol.go                 # wire types for /health /upsert /search /mark_reviewed /stats
    server.go                   # Unix-socket HTTP server, socket hygiene, data dir
    store/                      # SQLite/FTS5 store (schema, upsert/dedupe, search)
  internal/
    splice/                     # the deterministic pipeline layer
      schemas/                  # typed stage/pipeline/design/event/memory structs + Validate()
      classifier.go             # request -> tier, risk domains, rune-count thresholds
      planner.go                # tier -> ExecutionPlan, DistillRequestIntent
      budget.go                 # per-tier stage token budgets
      trajectory.go             # iteration state vectors, scoring, trajectory decisions
      context.go                # deterministic ContextRequest fulfillment
      registry_runner.go        # RegistryToolRunner over Zero's tools.Registry
      registry.go               # config-aware stage construction
      run.go                    # splicerun.Run orchestrator + runExecutionPlan seam
      design_runner.go          # RunDesignPlan, topological task sequencing
      helpers.go                # shared helpers (Ptr, DerefString, SummarizeStageOutput)
      stages/                   # stage agents, one file each + embedded prompts/
    memd/                       # sidecar HTTP client (F9b, in progress)
    worktrees/                  # Zero's worktrees + Splice MergeBack
    cli/                        # exec wiring: pipeline seam, --plan, --worktree --merge-back
  docs/
    STREAM_JSON_PROTOCOL.md     # event contract incl. pipeline/plan/merge-back events
    flug-design/                # archived Python-era design corpus (see Documentation Map)
  plans/                        # dated implementation plans (maintainer-local, not in the public repo)
  scripts/
    rename-module.sh            # re-apply module rename after upstream merges
```

## Working with MEMORY.md (maintainer-local)

`MEMORY.md` is the running log of project development. Treat it like a senior engineer's notebook.

**At the start of a session:**
- Read `MEMORY.md` from top to bottom (it's small, do the whole thing)
- Pay attention to "Current State", "Open Questions", and the most recent "Decision Log" entries

**During work:**
- If you make a non-trivial decision, note it for the log
- If you discover a constraint or learn something the user might forget, note it
- If you finish a planned task, mark it

**Before ending the session:**
- Append a new dated entry under "Decision Log" with what changed and why
- Update "Current State" to reflect reality
- Update "Open Questions" if any were resolved or new ones surfaced
- Update "Next Steps" so the next assistant (possibly a different model) can pick up cleanly

**What MEMORY.md is for:**
- Cross-session continuity (different model next time, same project)
- Decision rationale (why we picked X over Y)
- Things-that-aren't-obvious-from-the-code

**What MEMORY.md is NOT for:**
- Verbose narrative (be concise, like git commit messages)
- Architectural decisions that belong in docs (put them there, reference from MEMORY.md)
- Implementation details visible from code (let the code speak)

## Working in a Role: Planner vs Implementer

This project is built in two working modes, usually mapped to two model tiers in the tool harness (for example, a higher-capability model in plan mode for design, and a faster model for execution). The mapping from a specific model to a role lives in the tool harness configuration, never in this repo, so these rules stay model-agnostic (see the Model-Agnostic Note). Identify your role from what you are doing, not from which model you are.

Both roles remain bound by the Core Architectural Commitments, Style and Conventions, and the MEMORY.md rules above. The two sections below add role-specific guidance on top of those shared rules.

### Role: Planning / Design

Goal: turn a request into a checkpoint plan concrete enough that an implementer can execute it without making architectural decisions.

- Read the relevant design material deeply before proposing anything: the `docs/flug-design/` corpus for pipeline concepts, `UPSTREAM.md` for what is inherited, and the current plan under `plans/`. The architecture decisions there should not be revisited without strong reason.
- Produce a file-level plan: which files change, the input and output structs for any new stage communication, prompt templates and default model tier for new stages, and an estimated token cost.
- Resolve or explicitly raise Open Questions (see `MEMORY.md`). Do not leave one for an implementer to trip over mid-task.
- Decide checkpoint scope and ordering. Update `ROADMAP.md` in the same step if the plan, priorities, or order changed.
- Tag any checkpoint that needs human judgment (an Open Question, an irreversible or destructive action, a cross-cutting architectural choice) as `@needs-human` so it is not silently auto-implemented.
- Do not write implementation code in this role. Produce the spec, then hand off to the Implementation role.
- Exit criteria: a named, ordered checkpoint with files, structs, tests to add or update, and a focused validation command, with no open architectural decisions left.

#### Slicing work into checkpoints

A checkpoint is a green-to-green slice: one coherent contract, lands with tests passing, reviewable in one pass, and writable as one commit message without the word "and". Sizing the work is finding the seams.

Litmus test: can the work be described in one commit message without "and", landed green, and reviewed in one sitting? If yes, it is one checkpoint. If no, find the green seam (a point where the repo compiles and tests pass) and split there. If no green seam exists, it is genuinely one long checkpoint; do not force a split.

Split into multiple checkpoints when any of these is true:

- It crosses a schema or contract boundary (a new or changed stage input/output struct, a wire-protocol change). Each contract is a natural checkpoint.
- It contains an architectural decision. The decision ends one checkpoint (tag it `@needs-human`); the implementation after it is the next.
- There is a meaningful green midpoint: a sub-slice that compiles, passes its own tests, and is useful on its own.
- The diff is too big to review in one pass, or it touches unrelated modules.

Keep as one (long) checkpoint when all of these hold:

- The edits are entangled and cannot be staged or validated separately.
- There is no green stopping point; any partial slice leaves the repo red.
- The pieces are individually meaningless (a struct with no consumer, a rename plus its only call site).

The slice then maps to the execution mode (which model tier runs it, see "Working in a Role" above):

- One small checkpoint with light churn: run it linearly in the same session.
- One long, irreducible, churn-heavy checkpoint: delegate it to a lower-tier implementer with isolated context, so its file reads, diffs, and test output stay out of the higher-tier planning context.
- Multiple checkpoints: run them linearly, compacting context between each, and delegate any individual churn-heavy one.

### Role: Implementation

Goal: execute one approved checkpoint exactly as specified, leaving the repo reviewable and recoverable.

- **Resume-and-delegate:** after a session restart, when resuming in-flight checkpoint work that already has an approved plan and file-level contract, delegate the bounded implementation slice to a worker-class subagent rather than editing inline. The parent owns planning, review, synthesis, commits, and CI gates; the implementation slice is what the worker lane is for. Inline edits are reserved for trivial mechanical fixes (typos, one-line tweaks, doc copy), unblocking a stuck child, or when delegation is genuinely unavailable. This rule exists because the parent reliably defaults to inline implementation after a restart and burns context doing work the worker lane should own.
- Implement the approved checkpoint and nothing more. Do not re-architect, expand scope, or introduce new patterns or abstractions the plan did not call for.
- Follow the Incremental Development Cadence in `ROADMAP.md`: explain what is being added, show the struct if stage communication changed, implement the smallest useful slice, add or update focused tests, run focused validation, update `ROADMAP.md` and `MEMORY.md`, then commit, push, and wait for CI.
- Prefer extending existing files, tests, and abstractions over creating new ones.
- Escalate, do not improvise. If the plan is ambiguous, incomplete, or would require an architectural decision or a destructive action, stop and hand back to the Planning / Design role rather than guessing. A wrong guess from a lower-capability model is the exact failure mode this split exists to prevent.
- Stay within the files and scope named in the checkpoint. If you find you need to touch something outside it, that is an escalation, not a side quest.

## When Suggesting Code

- Always reference the relevant doc by filename (`docs/flug-design/05-trajectory-monitor.md`, `UPSTREAM.md`)
- Show the struct first if introducing new stage communication
- For new stages, show: input struct, output struct, prompt template, default model tier
- Estimated token cost matters. State it.
- Explain what is being added and how it will be implemented before changing code.
- For multi-step work, keep a short "Change log so far" in the conversation so the user can follow the reasoning.
- Break large features into small increments with explicit checkpoints instead of piling on scope silently.
- Add or update focused tests for changed behavior, usually in existing test files. Create a new `_test.go` file only for a new package or clearly distinct component; `go test ./...` and CI pick it up automatically.
- Update `ROADMAP.md` whenever planning, priorities, checkpoint order, or scope changes. Do this in the same checkpoint as the planning change.
- Run focused local validation when practical, then commit, push, and wait for GitHub Actions after every completed checkpoint before starting the next checkpoint, unless the user explicitly says not to push or not to wait. Treat CI as the authoritative full validation pipeline.
- Prefer extending existing abstractions over inventing new ones

## When in Doubt

Read the design docs before writing code. The architecture decisions in `docs/flug-design/` are the result of extensive design work; the Go port diverges from them only deliberately, with the divergence recorded in `MEMORY.md` (and `ROADMAP.md`'s known gaps when it is a deferral). If something seems off, raise it as a question first rather than refactoring around it. For inherited Zero behavior, check `UPSTREAM.md` before assuming a bug is ours.

## Model-Agnostic Note

This project is intentionally developed across multiple AI assistants. Do not introduce tool-specific assumptions, magic comments, or formatting that another assistant won't understand. If you're tempted to use a feature unique to one tool (e.g., Claude's artifacts, Cursor's apply blocks, Codex's tool syntax), put it in the conversation, not in the repo files.

The only model-aware files are the thin entry-point pointers (`CLAUDE.md`, `.cursorrules`). Everything substantive lives in `AGENTS.md`, `MEMORY.md`, `UPSTREAM.md`, `ROADMAP.md`, and `docs/`.
