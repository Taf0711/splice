# Agent Harness Principles and References

Flug is an agent harness, not a chat wrapper (see `docs/01-architecture.md`, the
"Agent Harness Model" section). This doc distills external references on
harness design, maps them onto Flug's existing commitments, and records where
Flug deliberately diverges. It exists so future contributors (human or AI) share
a vocabulary and do not accidentally drift Flug toward patterns it rejected on
purpose.

Raw extraction notes live in `flug/knowledge/agent-harness-research.md` (local,
gitignored). Sources are listed at the bottom.

## What an agent harness is

Synthesizing the harness references: an agent harness is the system that wraps a raw
LLM and makes it useful for sustained, autonomous work by supplying tools,
memory, context, constraints, and feedback loops. The load-bearing idea, stated
most sharply by the NVIDIA guidelines, is this:

> Every structural component operating outside the model (lint rules, type
> constraints, tests) is a guarantee that does not depend on non-deterministic
> token prediction.

That sentence is essentially Flug's Commitment 5 (deterministic-first) written
from the outside in. It is the reason Flug invests in `tools/`, schemas, and CI
rather than in prompt tuning.

## Reference model A: Deep Agents (LangChain), four capability categories

1. Execution environment: tools, a pluggable virtual filesystem
   (`ls`, `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `execute`),
   declarative filesystem permissions (allow/deny rules over operation and glob,
   first-match-wins), and sandboxed code execution.
2. Context management: skills (directories with `SKILL.md`, loaded by
   progressive disclosure), memory (persistent files on the `AGENTS.md`
   standard, always loaded), summarization and context offloading, and prompt
   caching of static system sections.
3. Delegation: a `write_todos` planning tool and stateless `task` subagents with
   isolated context that run in parallel and return a compressed final report.
4. Steering: human-in-the-loop interruption via `interrupt_on`, which gates
   destructive or costly tool calls behind approval.

It also has `HarnessProfile`, per-provider and per-model default bundles (system
prompt tweaks, tool overrides, middleware) that merge at model resolution.

## Reference model B: NVIDIA Elements, agent-harness guidelines

- Three layers: domain harness (business intent), project harness (code-space
  constraints and horizontal infrastructure), execution harness (the agent loop
  plus the raw model).
- Three engineering disciplines: prompt engineering (one inference call),
  context engineering (curate what the model observes at runtime), and harness
  engineering (treat failures as system-design problems to fix permanently). The
  anti-pattern is fixing agent failures by iterating prompts instead of adding a
  deterministic guard that makes the failure impossible.
- Investment strategy: invest heavily in the project harness (lint, strict
  types, tests); grow the domain harness incrementally (AGENTS.md, MCPs,
  skills); avoid custom model training; and do not build execution harnesses,
  delegate to Claude Code or Cursor.
- Interface parity: expose the same API as a CLI for humans and an MCP for
  agents, a 1:1 mirror.

## Reference model C: ShadowGit, local shadow history for AI work

ShadowGit automatically captures code history in a local shadow repository and exposes
that history to AI assistants through an MCP server. Its MCP workflow also includes an
AI session API: start a session, make changes, create a clean checkpoint, then end the
session. The relevant ideas for Flug are local shadow history, clean AI checkpoints,
restore-oriented debugging, and preserving ordinary user git workflows.

Flug adopts the underlying pattern but not the product dependency. The 2A snapshot
layer stores per-run, per-iteration commits in `runs/<id>/snapshot.git` while using
the real project as the work tree. It is narrower than ShadowGit: no background app,
no save-level auto-commits, no Node server, and no MCP dependency for snapshots. This
keeps Flug local-first and Python-core-only while preserving the useful shadow-history
primitive for trajectory rollback.

## Reference model D: Claude Agent SDK, deterministic permission callback

Anthropic's Claude Agent SDK exposes a `can_use_tool` callback plus declarative
permission rules around model-requested tool use. The useful pattern for Flug is
not the open-loop tool executor, which remains rejected below. The useful pattern
is the deterministic permission pipeline: hooks and deny rules are evaluated
before ask or allow paths, rule strings can scope a tool to a path pattern, and
the harness, not the model, makes the final approval decision.

Flug adopts that permission-boundary shape at its own narrower write boundary:
generated `FileChange` objects still reach disk only through
`tools/file_changes.py`, and configured deny rules can block a path before any
write occurs. Flug does not adopt Claude SDK subagent delegation, raw chat
session semantics, or model-driven arbitrary tool calls.

## Reference model E: OpenAI Codex SDK, presets over richer wire policies

OpenAI's Codex SDK separates friendly sandbox presets from a richer underlying
policy object, and treats approvals as server-initiated requests that must be
answered outside the model. The useful pattern for Flug is the decoupling: users
should configure simple, readable permission intent, while the harness evaluates
a deterministic policy at the action boundary.

Flug adopts the preset-vs-policy lesson by keeping user configuration small
(`pattern`, `decision`, `reason`) and evaluating it inside a pure permission
helper before file writes. Flug does not adopt the Codex SDK event loop,
sandbox execution engine, or subagent-like delegation model.

## Reference model F: Pydantic-AI CLI coding agent (Fowler article)

The article builds a single coding agent on the Pydantic-AI framework and gives
it capabilities through Model Context Protocol (MCP) tool servers (sandboxed
Python execution, file and terminal operations, library-doc lookup, web search,
structured reasoning) plus a few custom tools such as `run_unit_tests()`. The
model drives an implicit loop: read the request, pick tools, execute, keep
context, iterate. Its three load-bearing lessons are that deterministic
execution beats token generation (running real Python is more reliable than
predicting a calculation), that current context beats stale training data (live
docs and search improve reliability), and that behavior must be steered by
explicit instructions (fix the implementation not the test, make minimal
changes, practice TDD). Named anti-patterns: editing a failing test instead of
the implementation, timeouts too short for reasoning, and starving the agent of
context.

Flug adopts the deterministic-execution-over-token-generation lesson
(Commitment 5). The same conviction is why Flug's facts come from `tools/` (AST,
search, Bandit, pytest) before any model call. Flug also turns the article's
"fix the implementation, not the test" instruction into a structural guarantee
rather than a prompt: Test Generator and Test Runner are separate typed stages,
and the Test Runner gates on real results, so a writer cannot quietly rewrite a
test to pass. Flug does not adopt the single open-loop agent that drives MCP
tools at will (Commitment 2, see the divergence below), nor an agent framework
as its foundation (Commitment 7). Pydantic-AI sits in the same category as the
frameworks Flug builds its own orchestrator and adapters in place of. Flug
treats MCP as a future interface-parity surface (expose `flug` as an MCP server)
rather than the internal mechanism by which agents obtain tools.

## Reference model G: agentic-cli, domain-specific agentic CLI framework

agentic-cli is a Python framework for building domain-specific agentic CLI apps.
It layers a terminal UI (`BaseCLIApp`) over a swappable workflow manager
(Google ADK by default, LangGraph optionally for multi-provider and cyclic
state) over declarative `AgentConfig` objects. It ships capability-based,
fail-closed permission gating with a human-in-the-loop approval gate, a
slash-command palette (`/help`, `/status`, `/settings`, `/sessions`, and more),
session persistence, context-window and token-usage visibility, a hybrid
BM25-plus-vector knowledge base with reciprocal-rank-fusion and per-document
markdown sidecars, and semantic memory with contradiction detection and
forgetting policies.

Flug adopts several patterns it already shares. The fail-closed permission gate
with human confirmation matches `tools/permissions.py` plus the TUI safety
confirm gate (the same shape as reference models D and E). The slash-command
palette matches the TUI `/plan`, `/approve`, `/status`, `/diff`, `/login`,
`/model` palette. Token-usage visibility matches `optimizer/metering` plus
`flug cost`. Flug deliberately diverges on two points. First, agentic-cli is
built on agent frameworks (LangGraph, Google ADK) as its orchestration
foundation, which Commitment 7 rejects: Flug builds the orchestrator itself so
it controls the loop, the typed boundaries, and information minimalism. Second,
agentic-cli's knowledge and memory layers depend on embeddings and a vector
store (FAISS) with RRF fusion, while Flug's structured-memory sidecar (Track M,
`docs/10-structured-memory.md`) is deliberately deterministic and local-first:
BM25 over SQLite FTS5 with no required embedding service. agentic-cli's
contradiction detection and forgetting policies are a useful sketch of what
Flug's deferred memory updater (M8) could own, recorded in
`docs/10-structured-memory.md`.

## How Flug already maps to these models

| Harness concept (source) | Flug embodiment |
|---|---|
| Guarantees outside the model (NVIDIA) | Deterministic-first (Commitment 5); `tools/ast_inspect`, `tools/search`, `tools/codebase`, `tools/file_changes`; Static Analyzer; Test Runner |
| Project harness: lint/types/tests (NVIDIA) | CI (ruff, mypy strict, pytest, Bandit); schema-as-contract (Commitment 1) |
| Memory as always-loaded files (both) | `AGENTS.md` + `MEMORY.md` + `.flug/context.md` + `learnings.md` (docs/02) |
| Context engineering, offloading, summarization (both) | Information minimalism (Commitment 3); `optimizer/budget`, summarizer (TBD); the typed pull channel (`orchestrator/context.py`, `CodebaseTools`) |
| Prompt caching (Deep Agents) | `CacheSegment` marking in the provider layer |
| Execution environment: filesystem + permissions (Deep Agents, Claude SDK, Codex SDK) | Read via `CodebaseTools`; writes only through the `tools/file_changes` safe boundary; optional config-driven allow/deny rules over generated write paths; in-place edit with the `tools/workspace` git-safety net (E1) |
| Local shadow history for AI work (ShadowGit) | Per-run `runs/<id>/snapshot.git` shadow git dirs for iteration snapshots and future rollback |
| Planning (Deep Agents `write_todos`) | `orchestrator/planner` `ExecutionPlan` with typed stages |
| Steering / human-in-the-loop (Deep Agents) | Git-safety warning before in-place edits (E1); approval gates are a future generalization |
| Interface parity CLI + MCP (NVIDIA) | `flug` CLI today; planned `flug run --json` event protocol and MCP server |
| Per-provider/model defaults (Deep Agents `HarnessProfile`) | Provider/model resolution and per-agent config (docs/06, docs/07) |
| Single agent plus MCP tool servers (Pydantic-AI article) | Rejected as the internal mechanism; Flug uses an orchestrator-mediated typed pull channel (Commitment 2); MCP kept only as a future external interface-parity surface |
| Deterministic execution over token generation (Pydantic-AI article) | Deterministic-first (Commitment 5); `tools/` (AST, search, Bandit, pytest) run before any model call |
| Behavioral steering "fix the impl, not the test" (Pydantic-AI article) | Made structural: separate typed Test Generator and Test Runner stages; Test Runner gates on real results |
| Fail-closed permission gate plus HITL (agentic-cli; also Claude/Codex SDK) | `tools/permissions.py` allow/deny over write paths; TUI safety confirm gate (TUI-9) |
| Slash-command palette (agentic-cli) | TUI command palette (`/plan`, `/approve`, `/status`, `/diff`, `/login`, `/model`, `/help`, `/quit`) |
| Hybrid BM25+vector KB, semantic memory with forgetting (agentic-cli) | Track M sidecar (docs/10): deterministic BM25 over SQLite FTS5, local-first, no embedding dependency; forgetting and contradiction handling sketched for deferred M8 |
| Agent framework as foundation (Pydantic-AI, LangGraph, Google ADK) | Rejected (Commitment 7); Flug builds its own orchestrator and provider adapters |

## Where Flug deliberately diverges

These are intentional, not gaps to close. Document them so nobody "fixes" them
back toward the reference designs.

1. Orchestrator-as-foreman over open-loop tool calling. Deep Agents lets the
   model drive tools and spawn autonomous subagents in a loop. Flug rejects
   open-loop, model-driven tool use (Commitment 2). Context retrieval is a
   deterministic, harness-mediated pull channel: an agent emits a typed,
   bounded `ContextRequest`, the harness fulfills it through `CodebaseTools` and
   re-invokes. Rationale (2026-05-28 decision log): open-loop tool use is
   exactly where weak and local models fail, and it contradicts information
   minimalism. Flug keeps the foreman deterministic and the agents typed.
2. Typed stages over `write_todos` and stateless chat subagents. Flug stages are
   typed function interfaces with Pydantic input/output (Commitment 1), not
   free-form todo items or conversational subagents. Same delegation benefit
   (isolation, compression) without raw strings crossing boundaries.
3. The contrarian bet on building an execution harness. NVIDIA explicitly says
   "do not build execution harnesses, delegate to Claude Code or Cursor." Flug's
   thesis is the opposite: the execution harness is the product, built to be
   provider-agnostic, local-first, and token-efficient (target 35 percent of a
   naive baseline). This is a deliberate wager, and it sets the bar Flug must
   clear: Flug has to be meaningfully better on cost, control, or auditability
   than handing the task to an existing agent loop. If it cannot, the bet is
   wrong. Keep that bar visible.
4. Framework-on-frameworks over build-the-orchestrator. Both the Pydantic-AI
   article and agentic-cli stand on an agent framework (Pydantic-AI; LangGraph
   or Google ADK). Flug's Commitment 7 rejects that foundation on purpose so it
   owns the loop, the typed stage boundaries, and information minimalism. This is
   the same wager as divergence 3: if a hand-built orchestrator is not
   meaningfully better on cost, control, or auditability than a framework loop,
   the bet is wrong. Keep that bar visible.

## Principles adopted for Flug

1. Treat agent failures as harness bugs. Prefer a deterministic guard (a schema,
   an AST check, a lint rule, a typed boundary) over another round of prompt
   tuning. This is the single most important idea from the harness references.
2. Keep guarantees outside the model. Any claim the pipeline makes (compiles,
   passes tests, has no obvious vulnerability) should be backed by deterministic
   verification wherever one exists.
3. Practice context engineering, not context dumping. Pull bounded, just-in-time
   context; pass summaries forward, never raw upstream outputs.
4. Steer before irreversible actions. Surface and, where appropriate, confirm
   before destructive edits. The E1 git-safety warning is the first instance;
   approval gates are the generalization.
5. Memory is files, plus progressive disclosure. Always-loaded project memory
   (`AGENTS.md`, `.flug/context.md`) plus on-demand knowledge (a skills-like
   mechanism) when it lands.
6. Mirror the human and machine interfaces. The CLI and a future machine
   interface (`flug run --json` / MCP) should expose the same capabilities.

## Implications for the roadmap

- E1b (give the Code Writer a bounded context request) is precisely context
  engineering: pull the relevant existing files before writing.
- Declarative filesystem permissions (allow/deny over globs) now harden the
  `tools/file_changes` boundary for generated writes. Git-safety-as-a-block is
  a separate future approval checkpoint because it needs interactive UX.
- Human-in-the-loop approval gates generalize the E1 git-safety warning into an
  `interrupt_on`-style mechanism for destructive operations.
- The CLI-plus-MCP parity principle reinforces the already-planned
  `flug run --json` event protocol as the shared machine surface.
- ShadowGit reinforces the value of local shadow history and clean AI checkpoints.
  Flug keeps this as an internal per-run snapshot primitive rather than an external
  app dependency.

## Sources

- LangChain Deep Agents, "Harness" (accessed 2026-06-05):
  https://docs.langchain.com/oss/python/deepagents/harness
- NVIDIA Elements, "Agent Harness" guidelines (accessed 2026-06-05):
  https://nvidia.github.io/elements/docs/internal/guidelines/agent-harness/
- ShadowGit, automatic code versioning with AI memory (accessed 2026-06-09):
  https://www.shadowgit.com/
- ShadowGit MCP Server (accessed 2026-06-09):
  https://github.com/aflsolutions/shadowgit-mcp
- Anthropic Claude Agent SDK for Python (accessed 2026-06-24):
  https://github.com/anthropics/claude-agent-sdk-python
- OpenAI Codex SDK for Python (accessed 2026-06-24):
  https://github.com/openai/codex/tree/main/sdk/python
- Martin Fowler site, "Building Your Own CLI Coding Agent with Pydantic-AI"
  (accessed 2026-06-30):
  https://martinfowler.com/articles/build-own-coding-agent.html
- agentic-cli, framework for domain-specific agentic CLI applications, PyPI
  v0.5.3 (accessed 2026-06-30): https://pypi.org/project/agentic-cli/
- Raw extraction notes: `flug/knowledge/agent-harness-research.md` and
  `flug/knowledge/build-your-own-agent-research.md` (local).
