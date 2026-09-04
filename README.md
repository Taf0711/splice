<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/splice-logo-inverted.svg">
    <img src="docs/assets/splice-logo.svg" alt="splice" width="560">
  </picture>
</p>

<p align="center"><strong>A terminal code agent that turns a request into a checked change.</strong></p>

<p align="center">
  <a href="https://github.com/Taf0711/splice/actions/workflows/ci.yml?branch=main"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Taf0711/splice/ci.yml?branch=main"></a>
  <a href="https://www.npmjs.com/package/@taf0711/splice"><img alt="npm version" src="https://img.shields.io/npm/v/@taf0711/splice"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-blue"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white">
</p>

<p align="center">
  <img src="docs/assets/repair-loop.gif" alt="Splice implements a change, the test suite fails, the test runner returns typed failure evidence, and the rerun passes" width="900">
</p>

<p align="center"><em>One unedited run. The code writer implements the change, the test runner
fails on a trap test, it returns the failure evidence to the code writer, and
the rerun passes. Six stages, one repair re-entry.</em></p>

Splice is a local-first terminal agent for repository work. It can inspect code,
edit files, run checks, and use your selected model provider.

Splice combines a chat-first TUI with a typed execution pipeline. The TUI helps
you shape the work. The pipeline turns the approved request into checked stages.

## Quick start

The npm package requires Node.js 24 or newer.

```bash
npm install -g @taf0711/splice
splice
```

The first launch opens provider setup. Enter a request in the TUI, or start a
headless run:

```bash
splice exec "fix the test failure in ./pkg"
```

See [Install Splice](docs/INSTALL.md) for release archives, source builds, and
platform notes.

## Choose a workflow

### Shape a change in the TUI

Run `splice` to open the interactive interface. A new session starts in design
mode.

1. Describe the change.
2. Use `/crystallize` to create a typed design plan.
3. Review the critique and revise the plan if necessary.
4. Use `/approve` to start execution.

Use `/exec <prompt>` when you do not need a design conversation. The command
starts the execution pipeline directly.

The TUI also provides provider and model selection, permission prompts, session
resume, image input, themes, and transcript review.

### Run one request from a script

```bash
splice exec "explain internal/agent/loop.go"
splice exec --use-spec "add rate limits to the API client"
splice exec --worktree "try the migration in isolation"
splice exec --worktree --merge-back "update the parser and merge the result"
splice exec --plan design-plan.json
```

`splice exec` supports text, JSON, and JSONL output. It also supports session
resume, isolated worktrees, and explicit autonomy limits.

### Run in CI

Use the [Splice GitHub Action](docs/GITHUB_ACTION.md), or call `splice exec`
from any job. Use stream JSON when another process must consume live events.

```bash
splice exec --output-format stream-json "run the repository checks"
```

## How the pipeline works

Splice does not give one model turn every responsibility. The orchestrator owns
the run and gives each stage only the data it needs.

```text
request
  |
  v
classify -> typed plan -> focused context
                           |
                           v
                write -> analyze -> test -> verify
                           |
                           v
                  trajectory decision
                           |
             continue, recover, ask, or stop
```

The current classifier selects `trivial`, `light`, `substantial`, or
`architectural`. The schema also retains `standard` for compatible saved plans.

Model-backed stages write code or tests. Local stages perform static checks,
security checks, test execution, and acceptance verification. Each boundary uses
validated Go data instead of an unstructured chat transcript.

The trajectory monitor compares completed passes. It detects repeated code
state, oscillation, score regression, token limits, and confidence decline.
The orchestrator can revise context, select an escalation model, restore a
worktree snapshot, request a fresh analysis, ask the user, or stop.

Read [Pipeline behavior](docs/PIPELINE.md) for stage sets, success rules,
recovery behavior, and current limits.

## Safety and local data

Splice keeps the inherited Zero safety controls in every run.

- Workspace reads are available by default.
- Writes stay inside approved roots.
- Shell, network, destructive, and elevated actions pass through policy checks.
- Unsafe modes require an explicit flag.
- Worktrees reduce the effect of a failed change, but they are not sandboxes.
- Output surfaces redact known secret values.

Inspect the active policy before a sensitive run:

```bash
splice sandbox policy
splice sandbox grants list
splice exec --add-dir ../shared "update both repositories"
```

Sessions stay on the local machine. The optional `splice-memd` sidecar stores
bounded project observations in a local SQLite database. Splice adds no required
hosted coordinator and no product telemetry.

Read [Security in Splice](SECURITY.md) before you use external tools on a
sensitive repository.

## Providers and models

Splice supports hosted providers, local model servers, and compatible custom
endpoints. Run setup to select a profile and model.

```bash
splice setup
splice providers list
splice models list
splice doctor
```

Provider keys can come from environment variables, the setup flow, or the local
credential store. Splice also supports selected OAuth flows.

```bash
export OPENAI_API_KEY=...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
```

Local models can use Ollama or LM Studio. Pipeline stages that return typed data
need a model with tool-call support. Splice reports invalid responses. It does not select another provider without
your request.

Read [Configuration](docs/CONFIGURATION.md) and
[OAuth and provider login](docs/oauth-subscriptions.md) for supported setup
paths.

## Extensions and repository tools

Splice includes the Zero extension surfaces for interactive agent runs:

- MCP servers and tools
- local skills and plugins
- hooks and plugin tools
- named specialists
- session search and lineage
- repository maps and verification commands
- background jobs and editor protocols

The deterministic pipeline uses a smaller fixed tool set. It does not load every
interactive extension into each stage. See [Pipeline behavior](docs/PIPELINE.md)
for the boundary.

## Command map

| Command | Purpose |
|---|---|
| `splice` | Open the interactive TUI. |
| `splice exec` | Run one request or a saved plan. |
| `splice setup` | Configure the first provider and model. |
| `splice config` | Inspect resolved configuration without secret values. |
| `splice providers` / `models` | Manage provider profiles and inspect models. |
| `splice doctor` | Check configuration and provider health. |
| `splice sessions` / `search` | Inspect local session history. |
| `splice spec` | Review saved specification drafts. |
| `splice specialist` | Manage named specialists. |
| `splice skills` / `plugins` / `hooks` / `mcp` | Manage local extensions. |
| `splice sandbox` | Inspect policy and persistent grants. |
| `splice worktrees` / `changes` | Isolate and manage repository changes. |
| `splice verify` | Detect and run repository checks. |
| `splice usage` | Report token use and available cost estimates. |
| `splice update` / `upgrade` | Check for or install a release. |

Read [Command guide](docs/COMMANDS.md) for the complete command groups. The live
`splice --help` output remains the exact flag reference.

## Documentation

- [Documentation index](docs/README.md)
- [Install Splice](docs/INSTALL.md)
- [Configuration](docs/CONFIGURATION.md)
- [Command guide](docs/COMMANDS.md)
- [Pipeline behavior](docs/PIPELINE.md)
- [Stream-JSON protocol](docs/STREAM_JSON_PROTOCOL.md)
- [Update Splice](docs/UPDATE.md)
- [Specialists](docs/SPECIALISTS.md)
- [GitHub Action](docs/GITHUB_ACTION.md)
- [Benchmarks and evaluations](docs/BENCHMARK.md)

## For contributors

Splice is a Go project. The optional memory sidecar is a separate Go module.

```bash
make check
make test-memd
make test-cli-tui
```

Read [CONTRIBUTING.md](CONTRIBUTING.md) before you start work. The project uses
an issue-first contribution policy.

## Zero foundation

Splice is a fork of [Zero](https://github.com/gitlawb/zero), an MIT-licensed
terminal code agent. Zero supplies the TUI, provider adapters, tool registry,
sandbox, sessions, worktrees, and extension systems.

Splice adds the typed pipeline, design workflow, deterministic verification,
trajectory control, and optional local memory. Upstream code retains its
original copyright and license.

## License

Splice is available under the [MIT License](LICENSE).
