# Design Phase

## Status

Archived Python-era design. The body below describes the Flug (Python) target
shape and its file paths; the Week 4 / MEMORY.md references are to the
archived private repository.

Splice (Track F-Zero) note: the Go port implements this phase's execution
half. The `design_conversation` and `plan_critic` stages live in
`internal/splice/stages/` (F6), and `RunDesignPlan` in
`internal/splice/design_runner.go` (F8a) executes a crystallized design plan:
tasks are ordered topologically with cycle detection, each task runs as an
independent pipeline run through `splicerun.Run`, and failures are in-band
and fail-fast. `splice exec --plan <path.json>` is the CLI entry point. The
interactive conversation-to-crystallization flow in the TUI still runs Zero's
`agent.Run` loop by decision; wiring the pipeline design phase into the TUI
is a known gap in `ROADMAP.md`.

## The Problem This Solves

Every existing AI coding tool (Claude Code, Aider, Cursor, GitHub Copilot
Workspace) is fundamentally a **great executor of bad plans**. They take
whatever the user typed and run with it. If the user doesn't know to say
"use the repository pattern, avoid N+1 queries, add idempotency keys to the
webhook handler," they don't get those things.

The market opportunity is the inverse: a system that's great at making good
plans, *then* executing them. The design phase is what makes Flug
differentiated.

## Two Failure Modes

The design phase exists to solve two distinct failure modes:

**1. Ambiguous prompts.** User says "add a queue." Did they mean in-memory?
Redis? SQS? Persistent? At-least-once or exactly-once? An agent that just
picks one gets it wrong half the time.

**2. Clear but suboptimal prompts.** User says "store user sessions in
localStorage." That's a clear instruction. It's also a security hole. A
junior engineer would just do it. A senior engineer would push back.

Most agent systems do neither. The design phase does both, but unlike the
first version of this document, it does not try to automate the senior
engineer's judgment out of the loop. It puts the user's own judgment at the
center and gives it leverage: deterministic grounding, curated knowledge, a
forced adversarial audit, and an execution loop that can act on the result
unattended.

## What Changed From the First Version of This Document

The original design here was a fixed, linear, mostly-automated chain of four
typed agents (Requirements Analyzer -> Architect -> Architecture Critic ->
Architect revise -> Spec Writer), gated by a one-shot classifier output
(`design_intensity: skip|light|full`) that picked the depth automatically.

That shape does not match how design actually happens. Real design is not a
form to fill out; it is open-ended reasoning that escalates in depth on the
human's say-so, not a classifier's. You cannot make the exploratory part of
that reasoning typed without defeating the purpose of agentic reasoning:
forcing `Ambiguity{question, options, severity}` objects onto organic
back-and-forth ("actually, let's use Postgres instead") doesn't work.

The resolution: **separate "is this typed" from "is this multi-turn."** Code
Writer already reasons freely internally; only its *final output* is
schema-validated. The same pattern applies to design. The exploration stays
free, open conversation. Typing only has to hold at the **handoff boundary**,
the moment the conversation crystallizes into a concrete plan, because that
artifact is what the execution engine (and the adversarial auditor) actually
consume. Free exploration, typed handoff. Not one or the other.

This is not a novel pattern for this codebase. It is the exact shape of this
project's own plan-mode workflow: loose, read-only exploration, a hard
crystallization point (a plan file), a deliberate human-gated handoff into a
more constrained execution mode. The design phase below is that same shape,
generalized.

## The Real Workflow This Matches

Described directly against how design actually happens day to day:

1. Brief plus a rough idea of the app/stack: ask the model for an overview,
   taking rough thinking and making it more concrete.
2. Multi-round back-and-forth Q&A with the model.
3. Ask for a **broad, simple** architecture and workflow overview: explicitly
   shallow, just enough to validate direction.
4. More back-and-forth on that sketch until satisfied.
5. Explicit, user-triggered escalation: "do a thorough pass, go in-depth," a
   fuller architecture and workflow design. **The user decides when to go
   deeper; it is not an automatic classifier decision.**
6. Ask the model to document it all and break it into tasks, checkpoints, or
   phases. This is the crystallization moment.
7. Run a separate, rigorous audit/critique pass over that concrete plan, not
   over abstract architecture options.
8. Smaller, cheaper models implement task by task, iteratively: test, commit,
   push after each task. This step already exists in Flug today as the live
   execution loop (Code Writer through Test Runner, trajectory-scored and
   looped); the design phase's job is to start *feeding* it a plan instead of
   one undifferentiated request.
9. Periodically, or at the end of a substantial project, ask a bigger
   frontier model to audit the actual code, functionality, and the original
   plan together, checking for drift. Occasional and manual, not automatic.
   Not part of the initial build (see "Not Yet Built" below); recorded here
   because it is a clean, no-regret addition once the rest of this exists.

## Pipeline Shape

```
User
  │
  ▼  ── DESIGN PHASE (human-gated, loopable) ──
┌────────────────────────────┐
│ Conversation                │  free exploration: brief -> sketch -> deepen,
│ (untyped, multi-turn)       │  escalates on the user's say-so, grounded in
│                             │  CodebaseTools + injected knowledge files
└──────────────┬──────────────┘
               │ user says "make the plan"
               ▼
┌────────────────────────────┐
│ Crystallization              │  one structured-output call against the
│ (typed handoff boundary)    │  conversation: produces a DesignPlan
└──────────────┬──────────────┘
               │
               ▼
┌────────────────────────────┐
│ Adversarial Plan Critic     │  fresh context, hostile-review prompt,
│ (typed, can loop back up)   │  targets the concrete plan, not abstract
│                             │  options
└──────────────┬──────────────┘
               │ user accepts (human-gated)
               ▼  ── EXECUTION PHASE (live today) ──
   One iteration loop run per Task, in dependency order
   (Code Writer -> Test Generator -> Static Analyzer ->
    Security Auditor -> Test Runner, snapshotted and scored)
```

Two entry points feed the same crystallized artifact:

- **Conversational**: the path above, built into Flug's TUI (Week 4,
  checkpoint 4E). Bare `flug` is the only human entry point and opens
  directly into this conversation by default, replacing today's "collect one
  request, run it immediately" behavior. `flug run` remains, but only as the
  non-interactive automation surface for scripts, CI, and `--json` event
  consumers; it is not a competing entry point because it never opens an
  interactive environment.
- **Ingested**: a `DesignPlan` authored elsewhere (by hand, pasted from a
  separate Claude/Codex session, or generated by some other tool) is handed
  to Flug directly and validated. This path needs none of the conversational
  UI and can land first (checkpoint 4C), independent of and useful without
  4E.

Both paths converge on the same typed `DesignPlan`, the same critic, and the
same execution loop.

## The Crystallized Artifact: `DesignPlan`

Everything the SDLC front-end produces (requirements, scope, system design,
sequence diagrams, wireframes, the task breakdown) lives in one typed
artifact. Most of those fields are structured but free-text (markdown,
mermaid, ASCII), not their own schemas or agents on day one: real system
design and sequence-diagram reasoning does not decompose cleanly into
discrete typed sub-objects without becoming a form. The one field that must
be rigidly typed is the task breakdown, because each task maps to exactly one
run of the existing execution loop.

```python
class AcceptanceFact(BaseModel):
    """One machine-checkable acceptance criterion for a Task."""

    statement: str                                   # what must be true for this task to be done
    automated_verification: bool = False             # True if verification_command can check it
    verification_command: str | None = None          # e.g., "pytest tests/test_feature.py"
    recommended_automated_verification: bool = False  # advisory: recommend automating this check


class Task(BaseModel):
    """One unit of work; maps to exactly one execution-loop run."""

    id: str
    title: str
    intent: str = Field(max_length=320)   # becomes request_intent for the loop
    acceptance_facts: list[AcceptanceFact] = Field(default_factory=list)
    target_paths: list[str] = Field(default_factory=list)
    depends_on: list[str] = Field(default_factory=list)
    estimated_tier: PipelineTier | None = None


class DesignPlan(BaseModel):
    epic: str                              # one-line goal of the whole effort
    requirements: list[str]
    assumptions: list[str] = Field(default_factory=list)
    open_questions: list[str] = Field(default_factory=list)
    in_scope: list[str]
    out_of_scope: list[str]
    system_design: str                     # prose plus a mermaid component diagram
    sequence_diagrams: list[str] = Field(default_factory=list)   # mermaid
    wireframes: list[str] = Field(default_factory=list)          # ASCII or text
    technical_spec: TechnicalSpec | None = None
    tasks: list[Task] = Field(min_length=1)
    source: Literal["authored", "conversation", "ingested"]
    audit_history: list[PlanCritique] = Field(default_factory=list)


class TechnicalSpec(BaseModel):
    data_model: list[Entity]
    api_surface: list[Endpoint]
    components: list[ComponentSpec]
    file_structure: list[FilePlan]
    dependencies: list[str]
    test_requirements: list[TestRequirement]
    observability_hooks: list[str]
    success_criteria: list[str]
```

`technical_spec` is optional because not every plan needs the full formal
spec shape; a small plan's `system_design` prose plus its `tasks` may be
enough. Two handoff slots already exist in the live code, pre-wired for the
moment a stage populates them: `CodeWriterInput.technical_spec`
(`flug/schemas/agents.py`) and the `("requirements", "spec_writer")`
prior-summary pull in `CodeWriterHarnessAgent._select_relevant_context`
(`flug/agents/code_writer.py`).

## The Adversarial Audit

A **separate, fresh-context** agent, not a continuation of the conversation
that produced the plan. A model is measurably worse at being hostile to its
own just-written reasoning in the same thread than a fresh invocation with no
investment in the idea. This is the same principle as the original
docs/03 critic, retargeted: it critiques the **concrete `DesignPlan`**, not
abstract architecture options.

```python
class Critique(BaseModel):
    category: Literal["scalability", "security", "maintainability",
                      "complexity", "operability", "correctness"]
    severity: Severity
    issue: str
    suggested_mitigation: str

class PlanCritique(BaseModel):
    critiques: list[Critique]
    cross_cutting_concerns: list[str] = Field(default_factory=list)
    must_fix_before_execution: bool
    overall_assessment: str
```

The critic is prompted in adversarial mode:

> Your job is to find every reason this plan will fail in production or
> waste the implementer's time. You are a hostile staff engineer in a design
> review, looking at a concrete task breakdown, not a brainstorm. Be specific
> and ruthless. Do not hedge. Do not be agreeable. If you find no real
> issues, output an empty list rather than inventing problems.

The loop is human-terminated, not capped automatically: critic runs, the
user reviews, optionally revises the plan (back into the conversation or
directly), critic runs again, append each pass to `audit_history`, and the
user decides when it is strong enough to execute. This is the SDLC's
adversarial-audit step, made into discipline that runs every time instead of
only when someone remembers to ask for a second opinion.

## Where Senior Judgment Comes From

You can't just prompt a model with "be a senior engineer" and expect senior
output. Judgment has to come from somewhere concrete, injected at the
conversation step (and again at the critic step). Three sources, in priority
order, unchanged from the original design:

### 1. Bundled Knowledge Base

During development, local reference notes can live in ignored
`flug/knowledge/` files. Stable material should be promoted into tracked docs
or bundled package data before release:

```
flug/knowledge/              # local ignored reference notes during development
  principles.md              # 12-factor app, fail fast, idempotency, etc. (TBD)
  anti_patterns.md           # known bad patterns with explanations (TBD)
  reference_architectures/
    web_app_python.md
    web_app_typescript.md
    cli_tool.md
    background_jobs.md
    auth_system.md
    rate_limiting.md
    webhook_handler.md
  domain_checklists/
    security.md              # what every auth flow must include
    payments.md              # what every money-handling code must include
    data_privacy.md          # PII handling requirements
```

Selectively loaded into the conversation and the critic based on what the
request involves. A login endpoint pulls in `auth_system.md` and
`security.md`. A Stripe integration pulls in `payments.md`. This is how
"things a senior engineer just knows" get encoded without burning tokens
loading everything.

A deterministic selector (not an LLM call) picks which files to inject based
on keyword/path matching against the request, mirroring the existing
deterministic-first classifier. Loaded files are prompt-cached so they are
paid for once per session, not per turn.

### 2. Deterministic Codebase Grounding

The conversation is grounded in real facts about the actual project through
the existing `CodebaseTools` pull channel (`flug/tools/codebase.py`), the
same deterministic, bounded, harness-mediated retrieval the execution loop
already uses. The user does not paste files in, and the model does not
fumble with open-ended tool calls; the harness fulfills bounded context
requests the same way it does for Code Writer today.

### 3. Project's `.flug/context.md`

Project-specific conventions override or augment the bundled knowledge. If
the team has a strong opinion ("we always use SQLAlchemy 2.0 style"), it
lives here and the conversation and critic respect it. See
`02-storage-and-memory.md`.

### 4. Learned Patterns

Over time, Flug logs decisions that were made and whether they led to
passing tests, security flags, or user revisions. The critic gets a small
"lessons learned" injection: *"In this codebase, attempts to use SQLite for
concurrent writes have failed twice in past runs."* Opt-in, bounded, and
powerful. Not part of the initial build; see "Not Yet Built" below.

## `design_intensity` Is Advisory, Not a Gate

The classifier already computes `design_intensity: none|light|full`
(`flug/schemas/pipeline.py`), but nothing downstream reads it today, and
nothing should auto-gate the design phase on it going forward. Depth is the
user's call: type "fix this typo" and get a one-task plan immediately with no
ceremony; type a vague one-liner and the conversation will naturally ask
what's missing; explicitly say "go deeper" and it does. `design_intensity` is
kept and surfaced as a hint Flug can show the user ("this looks substantial,
want to go deeper?"), never as something that silently decides for them.

## What This Looks Like to the User

```
$ flug
> i want to add OAuth2 login with Google to this Flask app

▸ A few quick things before I sketch this:
  - Should existing email/password users be able to link a Google
    identity, or are they kept separate?
  - Server-side sessions or stateless JWTs?

> link existing accounts, server-side sessions is fine

▸ Rough shape: Authlib + Flask-Session, OAuth2 state validated server-side,
  linked by email match on first Google login. Want me to go deeper, or is
  this enough to start breaking into tasks?

> go deeper, also check token refresh

▸ [deepens: revocation, refresh handling, scopes, csrf state details,
   grounded against this project's actual auth module via CodebaseTools]

> ok, make the plan

▸ Crystallized into a DesignPlan: 4 tasks, in-scope/out-of-scope, one
  sequence diagram for the OAuth callback flow. Spec saved to
  .flug/runs/<id>/plan.json

▸ Running adversarial audit (fresh context)...
  [HIGH] No mention of state parameter validation (CSRF)
  [MED]  Token refresh handling underspecified for task 3

> fix those and re-audit

▸ Plan revised, re-audited, no remaining high-severity issues.
  Execute this plan? [y/n]

> y

▸ Task 1/4: add OAuth2 client config and routes... (executing, normal
  iteration loop continues, tested and committed per task)
```

The user sees the system reasoning like an engineer, escalates depth on
their own terms, sees the critic catch a real issue, and only then watches
the unattended loop take over. The handoff is visible and reviewable at every
boundary, not just at the end.

## Not Yet Built

Recorded here so future work knows what is deliberately deferred, not
forgotten:

- Promoting individual SDLC fields (sequence diagrams, wireframes,
  requirements) into their own typed sub-agents, if the single-conversation
  shape proves insufficient.
- Persistent cross-project learned-pattern memory (source 4 above).
- The periodic frontier-model audit of code, functionality, and plan
  fidelity together (workflow step 9 above): a no-regret addition once
  substantial implementation exists, but execution-side, not part of the
  design phase itself.
- An auditable record tying the design conversation to the run that
  implemented it, beyond what `audit_history` and the run directory already
  capture.

## The Pitch

> "An AI coding agent that argues with itself before writing code, on your
> terms."
>
> Most AI coding tools execute the user's plan, even when the plan is bad,
> or force a rigid Q&A form onto reasoning that should be a conversation.
> Flug lets you design the way you actually design: a free back-and-forth
> that escalates in depth when you say so, grounded in your actual codebase
> and a curated knowledge base instead of a blank chat window. The moment you
> say "make the plan," it crystallizes into a concrete, typed task breakdown.
> A separate, fresh-context critic then runs a hostile review against that
> plan, not abstract options, and the loop can repeat until you're satisfied.
> Only then does the already-built, unattended, trajectory-monitored
> execution loop take over: cheap-tier, tested, committed, and rolled back
> automatically if it starts thrashing. The result: senior-level design
> discipline from a conversation you'd have anyway, handed off to execution
> that doesn't need you to babysit it.
