<p align="center">
  <img src="docs/assets/splice-logo.png" alt="splice" width="560">
</p>

<p align="center"><strong>A terminal coding agent with a deterministic, multi-stage pipeline.</strong></p>

<p align="center">
  <a href="https://github.com/Taf0711/splice/actions/workflows/ci.yml?branch=main"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Taf0711/splice/ci.yml?branch=main"></a>
  <a href="https://www.npmjs.com/package/@taf0711/splice"><img alt="npm version" src="https://img.shields.io/npm/v/@taf0711/splice"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-blue"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white">
  <img alt="25+ providers" src="https://img.shields.io/badge/providers-25+-34E2EA">
  <br>
  <strong>English</strong> | <a href="README_ZH.md">中文</a>
</p>

Splice is an AI coding agent for your local terminal. It inspects a repository,
edits files, runs commands, uses browser and terminal helpers, and keeps
durable local sessions while you choose the model and the permission level.
On top of that engine, Splice layers an orchestrator-mediated, schema-as-contract
pipeline: a request is classified, turned into a typed execution plan, and run
through specialized stages (code writer, static analyzer, test generator,
security auditor, test runner) with a deterministic trajectory monitor that
catches death spirals before they waste tokens.

```bash
splice
splice exec "fix the failing test in ./pkg"
splice exec --output-format stream-json < turns.jsonl
```

## Why Splice

- **Use the model you want.** Bring OpenAI, Anthropic, Gemini, Groq, OpenRouter,
  DeepSeek, Mistral, xAI, Qwen, Kimi, GitHub Models, Ollama, LM Studio, or any
  OpenAI- or Anthropic-compatible endpoint.
- **A pipeline, not just a chat.** A request is classified into a tier, turned
  into a typed execution plan, and run through specialized stages whose inputs
  and outputs are Go structs with `Validate()` methods. The orchestrator is the
  foreman: agents never pass data to each other directly, and summaries flow
  forward, not raw outputs.
- **Deterministic-first.** Anything answerable with code (ripgrep, AST, Bandit,
  pytest, git diff) is answered with code. The LLM is the last resort. The
  context builder pulls real file, directory, and grep results from Zero's tool
  registry so a stage prompt sees the workspace, not a guess.
- **Trajectory-aware.** A deterministic scorer watches every iteration. Hard
  limit, budget exhaustion, state-hash cycles, oscillation, regression, and
  confidence collapse each escalate explicitly instead of looping silently.
- **Stay in control.** File writes, shell commands, network access, and
  out-of-workspace writes go through Splice's permission and sandbox policy.
- **Works in the terminal.** The TUI has model and provider pickers, image
  input, slash commands, live plan and tool rendering, scrollback, themes, and
  resume and fork support.
- **Works without the TUI.** `splice exec` is scriptable, supports text, JSON,
  and stream-JSON I/O, isolated worktrees, spec-first runs, and meaningful exit
  codes for CI.
- **Keeps context local.** Sessions are stored on disk, searchable, resumable,
  and never uploaded as telemetry by Splice.
- **Extensible when you need it.** Use MCP servers, skills, plugins, hooks, and
  specialist subagents from the same CLI.

## Install

Install via `npm install -g @taf0711/splice` (downloads the matching GitHub Release binary) or download archives directly from [GitHub Releases](https://github.com/Taf0711/splice/releases).

```bash
npm install -g @taf0711/splice
splice
```

The npm package will install a small wrapper plus the matching Splice binary for
your platform from GitHub Releases. It will support Linux, macOS, and Windows
on x64 and arm64.

### Bun (planned)

Bun does not run dependency lifecycle scripts by default, so the `postinstall`
that fetches the Splice binary is skipped and the first run fails with
`No native binary found next to the npm wrapper`.

The simplest fix is to trust the package after installing, which runs the
blocked postinstall. This works for project and global installs:

```bash
# project install
bun add @taf0711/splice
bun pm trust @taf0711/splice

# global install
bun add -g @taf0711/splice
bun pm -g trust @taf0711/splice
```

Alternatives: allow the postinstall up front by adding
`"trustedDependencies": ["@taf0711/splice"]` to your project's package.json
before `bun add`, or run the installer manually
(`node node_modules/@taf0711/splice/scripts/postinstall.mjs`) on Bun versions
that do not have `bun pm trust`.

### Install scripts (planned)

Linux/macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.ps1 | iex
```

### From source (works today)

Source builds require Go 1.25+.

```bash
git clone https://github.com/Taf0711/splice.git
cd splice
go run ./cmd/splice
```

If you are testing before the first public release, build from source:

```bash
go build -o splice ./cmd/splice
```

On Linux, build the sandbox helper too if you want native sandboxing:

```bash
go build -o splice-linux-sandbox ./cmd/splice-linux-sandbox
go build -o splice-seccomp ./cmd/splice-seccomp   # optional compatibility wrapper
```

Put `splice` and `splice-linux-sandbox` in the same directory on `PATH`
(`~/.local/bin` is a good default). macOS does not need an extra helper binary.
Windows source builds can use the main `splice.exe` as their sandbox helper;
release archives will still ship standalone Windows helper executables.

More install details: [docs/INSTALL.md](docs/INSTALL.md).

## First Run

Start the TUI:

```bash
splice
```

The setup wizard helps you pick a provider and model. You can also configure
providers from the command line:

```bash
splice setup
splice providers list
splice models list
splice doctor
```

For API providers, set the matching environment variable before setup or enter
the key in the wizard:

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
export LONGCAT_API_KEY=...
```

To configure Meituan LongCat (LongCat-2.0) directly, run:

```bash
splice providers setup longcat --set-active
```

For local models, run Ollama or LM Studio and then use `splice setup` or
`splice providers detect`. Model-backed pipeline stages require tool-calling
support. If a model omits the required typed tool call or returns invalid JSON,
Splice gives it up to two corrective retries and then reports an actionable
error. Splice never falls back from a local model to a cloud provider.

### Your first prompt: planning vs execution

A fresh interactive session starts in **planning mode** (design). Describe
what you want to build; Splice runs a design conversation, then `/crystallize`
turns it into a typed plan and `/approve` hands it to the execution pipeline.
This two-phase flow is for work that benefits from a plan first.

To skip planning and run a prompt straight through the pipeline, use
`/exec <prompt>` in the TUI or `splice exec "<prompt>"` headlessly:

```bash
splice exec "fix the failing test in ./pkg"
```

`/exec` is the escape hatch when you already know what you want and do not
need a planning step. Type `/design` to re-enter planning mode.

## Daily Use

### Interactive TUI

```bash
splice
```

Useful controls:

| Control | Action |
|---|---|
| `Enter` | send the prompt |
| `/` | open slash-command suggestions |
| `Shift+Tab` | cycle permission mode |
| `Ctrl+B` | show/hide the sidebar |
| `Ctrl+C` | cancel or exit |

Common slash commands:

| Command | Purpose |
|---|---|
| `/model`, `/provider` | switch the active model/provider |
| `/stages` | route the model-backed code writer and test generator, plus defaults and escalation |
| `/spec`, `/plan` | draft and review a plan before building |
| `/image` | attach an image for vision-capable models |
| `/resume`, `/rewind` | continue or roll back local sessions |
| `/loop` | repeat a prompt or custom `/command` on an interval (`/loop 5m /babysit-prs`) or self-paced |
| `/compact`, `/context` | manage context usage |
| `/permissions`, `/tools` | inspect available tools and policy |
| `/add-dir` | allow an extra write directory for this session |
| `/theme`, `/doctor`, `/config` | adjust appearance and inspect setup |

### Headless `exec`

```bash
splice exec "explain internal/agent/loop.go"
splice exec --model claude-sonnet-4.5 "refactor the config loader"
splice exec --use-spec "add rate limiting to the API client"
splice exec --worktree "try the migration in an isolated worktree"
splice exec --worktree --merge-back "run it isolated, then merge the result back"
splice exec --plan design-plan.json
splice exec --resume
splice exec --fork <session-id> "try the other approach"
```

`--plan` executes a design plan JSON file: tasks are ordered topologically by
their dependencies and each task runs as its own pipeline run, failing fast on
the first task that does not complete. `--merge-back` (requires `--worktree`)
commits the worktree's work, pins a `splice/<name>` recovery branch, and merges
into the source repo with `--no-ff`; a dirty source tree or a merge conflict
never forces anything, and the recovery branch survives every non-merged case.

Programmatic use:

```bash
splice exec --input-format stream-json --output-format stream-json < turns.jsonl
```

The stream-JSON contract is documented in
[docs/STREAM_JSON_PROTOCOL.md](docs/STREAM_JSON_PROTOCOL.md).

## The Pipeline

Splice's distinguishing layer is the deterministic pipeline in
`internal/splice/`. It is ported from the Flug paradigm (Flug is the archived
Python predecessor) and built new in Go on top of Zero's engine.

The flow:

1. **Classify.** `ClassifyRequest` inspects the request and assigns one of five
   tiers (trivial, light, standard, substantial, architectural) using
   keyword and risk-domain detection with rune-count thresholds that match the
   Python source.
2. **Plan.** `BuildExecutionPlan` turns the tier into a typed `ExecutionPlan`
   with per-stage `TokenBudget` allocations. Unknown tiers fail loudly with an
   error, matching Python's `KeyError`.
3. **Fulfill context.** Before a stage runs, its `ContextRequest` is fulfilled
   deterministically through `FulfillContextRequest`, which calls Zero's real
   `tools.Registry` (`read_file`, `list_directory`, `grep`). All six legal query
   types are handled; payloads are text, truncation is rune-safe, and a failed
   tool becomes an errored `ContextItem` rather than a silent empty payload.
4. **Run stages.** Each pipeline stage is a typed Go function in
   `internal/splice/stages/`. The code writer and test generator call the
   configured provider. Static analysis, security audit, and test execution are
   model-free and run through deterministic local tools. The `splicerun.Run`
   orchestrator drives them under Zero's full callback contract (streaming,
   usage, tool call/result pairs, permission events) and is wired into headless
   `splice exec`.
5. **Monitor trajectory.** `ComputeIterationState` builds a state vector from
   stage outputs; `EvaluateTrajectory` scores it and decides continue,
   escalate, rollback, step back, or surface to user. Repeated state hashes
   escalate as a cycle, including empty hashes, so an orchestrator bug surfaces
   loudly instead of looping.
6. **Iterate or abort.** The orchestrator acts on the trajectory decision.
   Failures are typed, not swallowed: a malformed stage payload names the data
   key and offending index.

Everything in `internal/splice/` is deterministic Go with full test coverage:
JSON round-trip tests cover every `Validate()`-bearing struct, and the context
builder has an end-to-end test against the real tool registry.

## Safety Model

Splice is designed to make side effects visible.

- Workspace reads are allowed by default.
- File writes are limited to the workspace unless you grant another directory.
- Shell commands, network access, destructive commands, and elevated actions are
  permission-gated.
- `--add-dir <path>` and `/add-dir <path>` grant additional write roots without
  giving the agent the whole filesystem.
- Unsafe and autonomous modes are explicit opt-ins.
- Secrets are redacted from tool output and logs where Splice controls the
  surface.

Example:

```bash
splice --add-dir ../docs-site
splice exec --add-dir ../shared "update both repos"
```

Sandbox behavior can be inspected with:

```bash
splice sandbox policy
splice sandbox grants list
```

## Web And Local Control

Splice includes local file, search, edit, and shell tools, `web_fetch` for
public URLs, and MCP support for additional tools.

For local dev servers, use shell commands such as `curl` through `exec_command`
so the normal sandbox and permission policy applies. Long-running commands stay
attached to a background terminal session and can be listed or stopped from the
TUI.

The npm package will also include browser and terminal helper packages used by
local browser and terminal tools. Source builds can use the same helpers when
they are on `PATH` or configured in Splice's local-control settings.

## Common Commands

```text
splice                interactive TUI
splice exec           one-shot or scripted agent run
splice setup          first-run provider setup
splice auth           OAuth/login helpers for supported providers
splice login          subscription login helpers for supported providers
splice models         model registry and capabilities
splice providers      provider profiles and detection
splice doctor         setup, key, and connectivity checks
splice context        context-budget report
splice repo-map       deterministic repository map
splice repo-info      local repository summary
splice search | find  search local session history
splice sessions       inspect, resume, fork, and rewind sessions
splice spec           manage spec-mode drafts
splice specialist     manage specialist subagents
splice skills         manage markdown instruction skills
splice plugins        manage plugins
splice hooks          manage lifecycle hooks
splice mcp            manage MCP servers and tools
splice serve --mcp    expose Splice tools over MCP stdio
splice sandbox        inspect sandbox policy and grants
splice worktrees      prepare isolated git worktrees
splice verify         detect and run local verification checks
splice changes        inspect and commit local git changes
splice usage          token usage and estimated cost
splice cron           scheduled agent jobs
splice update         check for newer releases
splice upgrade        apply a newer release
```

## Extending Splice

### Project and personal instructions

Splice appends project-specific guidance to the system prompt from the first
`AGENTS.md`, `SPLICE.md`, or `.splice/AGENTS.md` file found in each directory
from the git root down to your current working directory (checked in that order
per directory). Files are injected general-to-specific, capped at 8 KiB per
file and 32 KiB total.

A personal `SPLICE.md` under
`config.UserConfigDir()/splice/SPLICE.md`
(`$XDG_CONFIG_HOME/splice/SPLICE.md` or `~/.config/splice/SPLICE.md` on
Linux/macOS, `%AppData%\Roaming\splice\SPLICE.md` on Windows) applies across
every workspace, ahead of any project guidelines.

### Plugins

Plugins are discovered from
`~/.config/splice/plugins/<name>/plugin.json` (user scope, `$XDG_CONFIG_HOME`
or `~/.config` on every OS, independent of the `config.UserConfigDir()` path
used above) and `<cwd>/.splice/plugins/<name>/plugin.json` (project scope,
resolved from the current working directory, not the repo root), and managed
with `splice plugins`. A manifest can declare:

- `tools` — custom tools (`command`, `args`, `inputSchema`, and a
  `permission` of `prompt` or `deny`; `allow` is honored only when manifest
  tool auto-approval is enabled)
- `hooks` — commands run on `beforeTool`, `afterTool`, `sessionStart`, or
  `sessionEnd`
- `prompts` and `skills` — additional prompt and skill files

MCP servers (`splice mcp`) and standalone markdown skills (`splice skills`)
use the same extension points and can also be wired up outside of a plugin
manifest.

## Appearance And Accessibility

| Control | Effect |
|---|---|
| `NO_COLOR=<anything>` | disables color output |
| `ZERO_THEME=<name>` | selects the startup theme (`auto`, `dark`, `light`, or a color theme like `dracula`, `nord`, `gruvbox`, `tokyo-night`, `catppuccin`, `one-dark`, `solarized-dark`, `rose-pine`, `everforest`, `solarized-light`) |
| `--theme <name>` | selects the TUI theme from the CLI (same names) |
| `/theme` | opens the theme picker inside the TUI (live preview; `/theme <name>` switches directly) |
| `ZERO_NO_FADE=1` | disables streaming fade animation |

> Note: the theme environment variables still use the `ZERO_` prefix from the
> upstream engine. A rename to `SPLICE_` is planned but not yet applied; both
> names will be accepted during a transition.

Meaning does not rely on color alone; diffs, permissions, and statuses also use
text or glyph markers.

## Development

```bash
go test ./...
go run ./cmd/splice-release build
go run ./cmd/splice-release smoke
go run ./cmd/splice-perf-bench
```

Cross-compile examples:

```bash
go run ./cmd/splice-release build --goos linux --goarch amd64
go run ./cmd/splice-release build --goos windows --goarch amd64 --output dist/splice.exe
```

The deterministic pipeline layer lives in `internal/splice/` and is pure Go
with no provider SDK imports. Its packages:

- `internal/splice/schemas/` — typed stage and pipeline structs with
  `Validate()` methods.
- `internal/splice/classifier.go` — request-to-tier classification.
- `internal/splice/planner.go` — tier-to-execution-plan, intent distillation.
- `internal/splice/budget.go` — per-tier token budgets.
- `internal/splice/trajectory.go` — iteration state, scoring, trajectory
  decisions.
- `internal/splice/context.go` + `registry_runner.go` — deterministic context
  fulfillment through Zero's tool registry.
- `internal/splice/stages/`: the stage agents (code writer, static analyzer,
  test generator, security auditor, test runner, design conversation, plan
  critic) with embedded prompts.
- `internal/splice/run.go`: the `splicerun.Run` orchestrator loop.
- `internal/splice/design_runner.go`: `RunDesignPlan`, topological task
  sequencing for `splice exec --plan`.

Adjacent Splice-specific pieces: `internal/worktrees/` adds `MergeBack` on top
of Zero's worktree lifecycle, `memd/` is the memory sidecar (its own Go module,
binary `splice-memd`, SQLite/FTS5 over a Unix socket), and `internal/memd/` is
the sidecar's HTTP client (complete).

## Documentation

- [Install](docs/INSTALL.md)
- [Update flow](docs/UPDATE.md)
- [Stream-JSON protocol](docs/STREAM_JSON_PROTOCOL.md)
- [Specialists](docs/SPECIALISTS.md)
- [GitHub Action](docs/GITHUB_ACTION.md)
- [Benchmarks](docs/BENCHMARK.md)
- [Performance](docs/PERFORMANCE.md)
- [Agent evals](docs/AGENT_EVALS.md)

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), run the
relevant tests, and open a focused pull request.

Security reports should follow [SECURITY.md](SECURITY.md).

## License

Splice is released under the [MIT License](LICENSE).

## Attribution

Splice is built on top of [Gitlawb's Zero CLI](https://github.com/gitlawb/zero),
an open-source MIT-licensed terminal coding agent. Splice extends Zero's engine
with a deterministic, orchestrator-mediated multi-stage pipeline paradigm ported
from the archived Flug prototype. All upstream Zero code retains its original
copyright and license. See [UPSTREAM.md](UPSTREAM.md) for the full divergence
list and the upstream sync procedure.
