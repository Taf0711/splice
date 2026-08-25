# Splice Contributor Guide

Guidance for AI coding assistants and human contributors working in this repo.
Read this before making changes. Tool-specific entry-point files (`CLAUDE.md`,
`.cursorrules`) are thin pointers to this file.

## What Splice Is

Splice is a terminal coding agent written in Go. It is a fork of the
open-source Zero CLI (`gitlawb/zero`, MIT) with a deterministic,
orchestrator-mediated multi-stage pipeline layered on top. See `README.md`
for the user-facing pitch.

Two layers, one binary:

- **The Zero substrate**: interactive TUI, session storage, provider adapters
  (25+), tool registry, sandbox and permission policy, worktrees, MCP/skills/
  plugins. Inherited upstream code, with Splice-specific extensions in
  `internal/splice/` and thin seams in `internal/cli/`.
- **The Splice pipeline** (`internal/splice/`): a request is classified into a
  tier, turned into a typed `ExecutionPlan`, and run through specialized stages
  (code writer, static analyzer, test generator, security auditor, test runner)
  under a deterministic trajectory monitor. Wired into headless `splice exec`;
  the TUI conditionally runs `splicerun.Run` for non-spec-draft runs.

Design goals: local-first (no required services; SQLite/FTS5 sidecar for
memory), provider-agnostic, deterministic-first, trajectory-aware (no death
spirals), token-honest.

## Core Architectural Commitments

These are non-negotiable for the pipeline layer. A change that violates one is
almost certainly wrong.

1. **Agents are typed function interfaces, not chat participants.** Every stage
   input/output is a Go struct in `internal/splice/schemas/` with a `Validate()`
   method. No raw strings or maps cross stage boundaries.

2. **The orchestrator is the foreman.** Stages don't pass data directly to each
   other. `splicerun.Run` receives outputs, decides what the next stage needs,
   constructs the input struct, and invokes. Summaries flow forward, not raw
   outputs.

3. **Information minimalism.** Each stage receives only what it needs. The
   user's verbose original request never propagates past the orchestrator
   (`DistillRequestIntent`).

4. **Local-first.** No required external services. SQLite (via the `splice-memd`
   sidecar) and the filesystem only.

5. **Deterministic-first.** Anything answerable with code (the tool registry,
   grep, `go/parser`, `py_compile`, Bandit, test runners, git diff) is answered
   with code. The LLM is the last resort.

6. **Provider-agnostic.** All LLM calls go through Zero's provider layer. Never
   import provider SDKs directly in `internal/splice/`.

7. **Fail loudly.** Malformed payloads, unknown tiers, and empty-state cycles
   return errors that name the offending data; nothing silently defaults. Silent
   fallbacks in a deterministic pipeline are bugs that look like features.

8. **The pipeline must honor Zero's safety substrate.** `splicerun.Run` accepts
   `agent.Options` (registry, sandbox, permission mode, file tracker, hooks) and
   must keep honoring them; bypassing them bypasses the sandbox.

## Style and Conventions

- **Go 1.25+**, single root module `github.com/Taf0711/splice` plus the nested
  `memd/` module.
- **gofmt is law.** `gofmt -l .` must be empty; run `go vet ./...` before
  committing. CI runs build, vet, and `go test ./...` (and the memd job runs the
  same inside `memd/`).
- **Errors are values.** Wrap with `%w` and name the failing input; no panics in
  library code.
- **No em-dashes in new or actively-edited user-facing strings or documentation.**
  Use periods, commas, or parentheses.
- **Documentation follows ASD-STE100 Simplified Technical English.** All
  user-facing documentation (README.md, `docs/`, user-facing Go
  comments) uses the controlled vocabulary and rules of ASD-STE100: approved
  words only (use an approved synonym when a word is not in the STE word list;
  do not use synonyms for an approved word), short sentences (maximum 20 words
  for procedures, 25 for descriptive), active voice, imperative mood for
  instructions, one topic per sentence, articles before nouns, no gerunds
  (-ing verb forms) or nouns used as verbs, maximum three lines per paragraph.
  Keep the two README mirrors consistent under this standard.
- **Tests**: standard `testing` package, table-driven where natural, `_test.go`
  next to the code under test. Prefer extending an existing test file over
  creating a new one; create a new file only for a new package or clearly
  distinct component. Mock providers at the provider seam (see
  `execStageAwareProvider` in `internal/cli/` and the mocked-provider tests in
  `internal/splice/stages/stages_test.go`). Real-git tests are fine where git
  behavior is the contract (`internal/worktrees/worktrees_test.go`).
- **Prefer extending existing abstractions over inventing new ones.**

## Regression Discipline

The pipeline and learning logic is deterministic code over typed structs.
Test it that way. Every feature lands with a regression net that pins its
failure modes, not only its happy path.

- **Test the guards harder than the features.** A broken feature fails
  loudly. A broken guard fails silently and looks like success. Floor
  refusals, rollback triggers, invalidation rules, permission floors, and
  gate protections get the most adversarial fixtures.
- **Fabricate corpora for learning logic.** Fitters, bucket keys, verdict
  mapping, and floors are pure functions over typed structs. Build fake trace
  corpora in table tests. No provider, no model, milliseconds.
- **Pin invariants as property tests.** Append-only stores, self-contained
  traces, key invalidation, and per-consumer filtering get dedicated tests
  that fail on any violation.
- **Pair producers with consumers.** When a schema field, event, or rule
  table has a writer and a reader, add a pairing test that fails CI when one
  side drifts (see `TestTrajectoryRuleOrder` and
  `TestTrajectoryExtractorsCoverRegistryStages`).
- **Mock at the provider seam for integration.** Full runs use the mocked
  provider (`execStageAwareProvider`, `stages_test.go`) to prove the wiring:
  runs write complete traces, floors flip provenance, guards fire.
- **Slow behavioral checks stay out of CI.** The eval harness and live-model
  smokes run on a release cadence, not per commit.

## Release Approval

A general release request does not approve a version.

Before any release action:

1. Show the owner the exact proposed version and explain how you derived it.
2. Get explicit owner approval for that exact version.

Release actions include release pull request merges, tags, GitHub releases,
package publications, and public announcements.

Do not infer product version intent from Release Please, conventional commits,
or Semantic Versioning defaults. If the version is not explicit, stop and ask.

## Upstream Discipline

Splice is a fork of Zero. Splice now owns the product. The old rule to keep
inherited Zero code minimal is discarded. Upstream merges are not a product
goal, and the cost of a manual port is lower than the cost of an
architectural compromise.

New rule: preserve useful functionality. Optimize the implementation for
the Splice product, architecture, and maintainability. Splice may move files,
split large modules, replace inherited flows, delete inherited UI that no
longer fits, and redesign the workspace. Behavior must stay correct and
tested.

Functional parity, not source parity, is the standard. Before you remove or
replace inherited behavior, check the functional parity checklist in the
strategy document. Keep every item or record an explicit decision to drop it.

New pipeline behavior still belongs in `internal/splice/`, `internal/memd/`,
`internal/worktrees/` extensions, or thin seams in `internal/cli/`, because
those seams keep the pipeline testable. This rule now protects architecture,
not upstream merges.

Authority for release order is `SPLICE_TUI_RELEASE_PRIORITY_REVISED.md`
(P0 runtime truth through P6). When this file and that document disagree,
that document wins.

The memd sidecar is a **separate Go module** (`memd/`, module
`github.com/Taf0711/splice/memd`). The root workflow's `go test ./...` does not
see it; it has its own CI job. The client in `internal/memd/` speaks HTTP over
the Unix socket and never imports the sidecar module; the wire contract is the
only coupling.

## File Layout

Everything under `internal/` not listed here is inherited Zero substrate (agent
loop, TUI, tools, sandbox, providers, sessions, MCP, and so on).

```
splice/
  go.mod                        # github.com/Taf0711/splice
  cmd/
    splice/                     # main binary
    splice-*/                   # helper binaries (sandbox, seccomp, release, ...)
  memd/                         # memory sidecar, SEPARATE Go module
    go.mod                      # github.com/Taf0711/splice/memd
    main.go                     # splice-memd entry point
    protocol.go                 # wire types for /health /upsert /search ...
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
      run.go                    # splicerun.Run orchestrator
      design_runner.go          # RunDesignPlan, topological task sequencing
      stages/                   # stage agents, one file each + embedded prompts/
    memd/                       # sidecar HTTP client
    worktrees/                  # Zero's worktrees + Splice MergeBack
    cli/                        # exec wiring: pipeline seam, --plan, --worktree --merge-back
  docs/
    STREAM_JSON_PROTOCOL.md     # event contract incl. pipeline/plan/merge-back events
  scripts/
    rename-module.sh            # re-apply module rename after upstream merges
```

## Building and Testing

Requires Go 1.25+.

```bash
go build ./...
go vet ./...
go test ./...
gofmt -l .                      # must print nothing
```

The memd sidecar has its own module:

```bash
cd memd && go test ./...
```

Cross-compile helper:

```bash
go run ./cmd/splice-release build --goos linux --goarch amd64
```

## Documentation

- `README.md`: project overview.
- `docs/README.md`: public documentation index.
- `docs/COMMANDS.md` and `docs/CONFIGURATION.md`: command and configuration guides.
- `docs/PIPELINE.md`: stage, trajectory, recovery, and usage behavior.
- `docs/STREAM_JSON_PROTOCOL.md`: the JSONL contract for headless clients.
- `docs/DESIGN_TRANSITIONS.md` and `docs/DESIGN_REVISION_CONTEXT.md`: design lifecycle behavior.
- `docs/` also contains install, update, extension, automation, benchmark, and release guides.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md). Open a focused pull request, run the
relevant tests, and keep changes small and reviewable. Security reports should
follow [SECURITY.md](SECURITY.md).

## Model-Agnostic Note

This project is developed across multiple AI assistants. Do not introduce
tool-specific assumptions, magic comments, or formatting that another assistant
won't understand. If you're tempted to use a feature unique to one tool
(Claude's artifacts, Cursor's apply blocks, Codex's tool syntax), put it in the
conversation, not in the repo files.

The only model-aware files are the thin entry-point pointers (`CLAUDE.md`,
`.cursorrules`). Everything substantive lives in `AGENTS.md` and `docs/`.
