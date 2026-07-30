<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/splice-logo-inverted.svg">
    <img src="docs/assets/splice-logo.svg" alt="splice" width="560">
  </picture>
</p>

<p align="center"><strong>The agent that turns a prompt into a checked change.</strong></p>

<p align="center">
  <a href="https://github.com/Taf0711/splice/actions/workflows/ci.yml?branch=main"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Taf0711/splice/ci.yml?branch=main"></a>
  <a href="https://www.npmjs.com/package/@taf0711/splice"><img alt="npm version" src="https://img.shields.io/npm/v/@taf0711/splice"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-blue"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white">
</p>

Splice is a local-first coding agent for people who want more than a model in a
shell loop. It can inspect a repository, edit files, run checks, and use the
model and provider you choose. For substantial work, it classifies the request,
assembles only the context a stage needs, and routes the change through a
specialized execution plan.

## Start in three commands

```bash
npm install -g @taf0711/splice
splice
```

Then describe the change. For a direct, scriptable run:

```bash
splice exec "fix the failing test in ./pkg"
```

The first launch opens setup for a provider and model. See
[Install](docs/INSTALL.md) for source builds, platform notes, and the optional
memory sidecar.

## Why Splice exists

Most coding agents have one long conversation responsible for understanding,
editing, testing, and deciding whether to continue. Splice separates those
jobs without turning them into a noisy agent swarm.

- **A typed pipeline.** Stages exchange validated Go structures, not loose chat
  transcripts. The orchestrator decides what each stage receives and what runs
  next.
- **Deterministic first.** Repository reads, search, AST checks, static analysis,
  tests, diffs, and exit codes come from local tools. The model is used where
  judgment is needed, not where a command is more reliable.
- **Trajectory awareness.** The run monitor detects hard limits, repeated state,
  oscillation, regression, and collapsing confidence. A stuck run surfaces the
  problem instead of spending tokens forever.
- **Least context by default.** The original request is distilled before it
  reaches later stages. Each stage receives a small summary and the evidence it
  actually needs.
- **Zero's safety substrate.** File writes, shell commands, network access, and
  additional write roots still pass through Zero's permissions, sandbox, hooks,
  and tool registry.
- **Local by default.** Sessions and optional memory stay on disk. Splice does
  not add telemetry or require a hosted coordinator.

## The Splice loop

```text
request
   │
   ▼
classify ──► typed execution plan ──► focused context
                                      │
                                      ▼
             write ──► analyze ──► test ──► audit
                                      │
                                      ▼
                         trajectory decision
                         continue, escalate, or stop
```

The pipeline has five request tiers, from trivial to architectural. A normal
run may use a model-backed code writer and test generator, while static
analysis, security checks, and test execution stay deterministic and local.
Every stage has a contract and validation. Malformed output is an error with
its offending field, not an empty fallback.

## Choose your mode

### Interactive

```bash
splice
```

Use the TUI when you want a live conversation, provider and model pickers,
permission prompts, plan review, themes, image input, session resume, or forked
exploration.

A new session starts in design mode. Describe the work, use `/crystallize` to
turn the conversation into a typed plan, then `/approve` to execute it. If the
plan is already in your head, use `/exec <prompt>` and go straight to the
pipeline.

These controls set the appearance:

| Control | Effect |
|---|---|
| `NO_COLOR=<anything>` | disables color output |
| `SPLICE_THEME=<name>` | selects the startup theme (`auto`, `dark`, `light`, or a color theme like `dracula`, `nord`, `gruvbox`, `tokyo-night`, `catppuccin`, `one-dark`, `solarized-dark`, `rose-pine`, `everforest`, `solarized-light`) |
| `--theme <name>` | selects the TUI theme from the CLI (same names) |
| `/theme` | opens the theme picker inside the TUI (live preview; `/theme <name>` switches directly) |
| `SPLICE_NO_FADE=1` | disables streaming fade animation |

Meaning does not rely on color alone. Diffs, permissions, and statuses also use
text or glyph markers.

### Headless

```bash
splice exec "explain internal/agent/loop.go"
splice exec --use-spec "add rate limiting to the API client"
splice exec --worktree "try the migration in isolation"
splice exec --worktree --merge-back "run isolated, then merge the result back"
splice exec --plan design-plan.json
```

`splice exec` supports text, JSON, and stream-JSON input and output, isolated
git worktrees, resumable sessions, and meaningful exit codes for automation.
For a streaming client:

```bash
splice exec \
  --input-format stream-json \
  --output-format stream-json < turns.jsonl
```

The event contract lives in
[docs/STREAM_JSON_PROTOCOL.md](docs/STREAM_JSON_PROTOCOL.md).

## Providers and local models

Bring the provider that fits the job. Splice supports OpenAI, Anthropic,
Gemini, Groq, OpenRouter, DeepSeek, Mistral, xAI, Qwen, Kimi, GitHub Models,
Ollama, LM Studio, and OpenAI- or Anthropic-compatible endpoints.

```bash
splice setup
splice providers list
splice models list
splice doctor
```

Set a provider key before setup or enter it in the wizard:

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
```

Local models work through Ollama or LM Studio. Model-backed stages require tool
calling. If a provider returns an invalid typed payload, Splice retries the
correction and then reports an actionable error. It does not silently switch a
local run to a cloud provider.

## Safety is part of the workflow

Splice makes side effects visible instead of hiding them behind an autonomous
mode:

- workspace reads are allowed by default;
- writes stay inside the workspace unless you grant another root;
- shell commands, network access, destructive actions, and elevated actions are
  permission-gated;
- `--add-dir <path>` grants a specific additional write root;
- unsafe and autonomous modes are explicit opt-ins;
- sandbox policy and grants can be inspected before a run.

```bash
splice sandbox policy
splice sandbox grants list
splice exec --add-dir ../shared "update both repositories"
```

Read [SECURITY.md](SECURITY.md) for the threat model and reporting process.

## A small CLI surface for big workflows

| Command | What it is for |
|---|---|
| `splice` | Interactive TUI |
| `splice exec` | One-shot and scripted runs |
| `splice setup` | First-run provider setup |
| `splice providers` / `models` | Provider profiles and model capabilities |
| `splice doctor` | Setup and connectivity checks |
| `splice spec` | Spec-mode drafts |
| `splice sessions` | Resume, fork, and inspect sessions |
| `splice specialist` | Specialist subagents |
| `splice skills` / `plugins` / `hooks` | Extend the agent locally |
| `splice mcp` | Configure MCP servers and tools |
| `splice worktrees` | Prepare isolated git worktrees |
| `splice verify` | Find and run repository checks |
| `splice update` / `upgrade` | Keep the binary current |

## Documentation

- [Install Splice](docs/INSTALL.md)
- [Update flow](docs/UPDATE.md)
- [Stream-JSON protocol](docs/STREAM_JSON_PROTOCOL.md)
- [Specialists](docs/SPECIALISTS.md)
- [GitHub Action](docs/GITHUB_ACTION.md)
- [Benchmarks](docs/BENCHMARK.md)
- [Performance](docs/PERFORMANCE.md)
- [Agent evaluations](docs/AGENT_EVALS.md)
- [OAuth logins and subscriptions](docs/oauth-subscriptions.md)

## For contributors

Splice-specific pipeline code lives in `internal/splice/`. It is pure Go and
keeps provider SDKs out of the pipeline layer. The optional memory sidecar is a
separate module in `memd/` and communicates over a Unix socket.

```bash
go test ./...
go vet ./...
gofmt -l .
cd memd && go test ./...
```

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a change. The project is
currently using an issue-first contribution policy.

## Built on Zero's Engine

Splice is built on top of [Gitlawb's Zero Engine](https://github.com/gitlawb/zero),
an open-source MIT-licensed terminal coding agent. Zero provides the TUI,
provider adapters, sessions, tools, sandbox, permissions, worktrees, MCP,
skills, plugins, and hooks. Splice keeps that foundation and adds the
orchestrator-mediated pipeline, typed stage contracts, deterministic checks,
trajectory monitoring, and the optional memory sidecar.

Upstream Zero code retains its original copyright and license. See
[LICENSE](LICENSE) and [SECURITY.md](SECURITY.md).

## License

Splice is released under the [MIT License](LICENSE).
