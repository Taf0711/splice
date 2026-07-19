# Agent Configuration

## Why This Is Its Own Doc

`docs/06-multi-provider.md` introduced the per-stage config block as a footnote inside the broader provider-abstraction story. In practice it's the user-facing surface that matters most: the knob that lets a team say "use Sonnet for the writer, o3 for the critic, Qwen on Ollama for the static analyzer, and don't touch the memory agent." This doc codifies that knob, what it can express, what guarantees Flug makes around it, and what it deliberately does not let users do.

The short version: **users can pin any agent to any provider/model combination, with per-agent fallback chains and per-agent escalation targets, because schema-as-contract makes mix-and-match safe.** A handful of agents are tier-locked rather than model-locked to keep the local-first and offline guarantees intact.

## The Guarantee That Makes This Safe

Per-agent model swapping is only safe because of the architecture commitment in `docs/01-architecture.md`: every agent input and output is a Pydantic v2 schema, validated at the boundary. The orchestrator does not care which model produced the JSON as long as it parses. Adapters in `docs/06-multi-provider.md` either provide guaranteed structured output (Anthropic tool use, OpenAI parse, Google function calling) or fall through a validate-and-retry wrapper for best-effort providers (Ollama, generic OpenAI-compatible).

This is the contract:

- A user can pin the design conversation to OpenAI and the plan critic to Anthropic, and the pipeline still composes, because both produce validated `DesignPlan` and `PlanCritique` instances.
- A user can pin the static analyzer to a 7B local model. If structured output fails three times the orchestrator escalates per `06`, but the surrounding pipeline is unaffected.
- A user can never produce a state where one agent's output type doesn't match the next agent's input type, because schemas are checked at validation, not at config time.

If you remember one thing from this doc: configuration changes models, not interfaces.

## Configuration Hierarchy (Recap)

Five layers, later overriding earlier (see `docs/06-multi-provider.md` for the canonical version):

1. Built-in defaults (in code)
2. Global config: `~/.config/flug/config.toml`
3. Project config: `./.flug/config.toml`
4. Environment variables: `FLUG_*`
5. CLI flags: `--model=`, `--tier-medium=`, `--profile=`

Within any layer, resolution for a single stage is:

```
stage-specific override   ->   tier mapping   ->   built-in default
```

A stage with no `[stages.<name>]` block falls back to its tier (`nano | small | medium | large | reasoning`), which is mapped to a concrete provider/model in `[models.tiers]`.

## The Full Per-Stage Block

Every agent listed in the roster (`docs/01-architecture.md`) accepts the same configuration shape. Most users will set zero of these. Power users can set all of them.

```toml
[stages.<agent_name>]

# Identity. Either pin a tier (recommended) or pin a concrete model.
tier        = "medium"                       # symbolic; resolves via [models.tiers]
provider    = "openai"                       # optional, only when pinning a model
model       = "gpt-5"                        # optional, only when pinning a model

# Sampling and thinking.
temperature    = 0.2                         # default 0.0
max_tokens     = 4096                        # output cap, also enforces budget
thinking_level = "medium"                    # optional: off, minimal, low, medium, high, xhigh

# Reliability.
fallback        = ["anthropic/claude-sonnet-4-5", "google/gemini-2.5-pro"]
fallback_on     = ["rate_limit", "timeout", "5xx"]
retry_on_validation = 3                      # for non-guaranteed structured output

# Quality control.
confidence_threshold = 0.85                  # per-agent (overrides global)
escalate_to          = "large"               # tier name or provider/model target
escalate_max_attempts = 1

# Caching.
cache_segments = ["knowledge_base", "system_prompt", "project_context"]
```

Field semantics:

| Field | Default | Notes |
|---|---|---|
| `tier` | per-agent default in `01` | Use this unless you have a reason not to. |
| `provider` + `model` | unset | If both set, takes precedence over `tier`. |
| `temperature` | 0.0 | Most agents should stay deterministic. Critic and architect can benefit from 0.2-0.4. |
| `max_tokens` | per-tier default | Hard ceiling on output. Also feeds the budget allocator in `04`. |
| `thinking_level` | tier value or unset | Optional reasoning level: `off`, `minimal`, `low`, `medium`, `high`, `xhigh`. Stage value overrides tier value. |
| `fallback` | empty | List of `provider/model` pairs. Tried in order on `fallback_on` errors. |
| `fallback_on` | `["rate_limit", "timeout", "5xx"]` | Never includes validation or auth errors. See `06`. |
| `retry_on_validation` | 3 | Only meaningful for adapters where `STRUCTURED_OUTPUT_GUARANTEED = False`. |
| `confidence_threshold` | 0.7 (most agents), 0.85 (architect, critic) | Below this, the orchestrator escalates. |
| `escalate_to` | next tier up | Accepts either a tier name such as `large` or a concrete `provider/model` target. If user pins a specific model, the orchestrator must respect their escalation target rather than guessing. |
| `escalate_max_attempts` | 1 | After this many escalations within a stage, surface to the user. |
| `cache_segments` | full set | Which prompt segments to mark cacheable. Drop segments to debug cache behavior. |

### Pinning a tier

The 90% case. Recommended for almost all users.

```toml
[stages.architect]
tier = "large"

[stages.security_auditor]
tier = "medium"

[stages.static_analyzer]
tier = "nano"
```

Tiers are mapped once in `[models.tiers]`. If the team switches from Anthropic to Google, they edit one block and every agent moves with them. A tier can also set `thinking_level`, so the same model can back multiple tiers with different reasoning levels.

### Pinning a model

The 10% case. For when a team has a strong opinion about a specific stage.

```toml
[stages.critic]
provider = "openai"
model    = "o3-mini"
thinking_level = "high"
temperature = 0.3                # let the adversarial agent vary its attacks
confidence_threshold = 0.9       # critic mistakes propagate; bias toward escalation
escalate_to = "openai/o3"

[stages.code_writer]
provider = "anthropic"
model    = "claude-sonnet-4-5"
fallback = ["openai/gpt-5", "google/gemini-2.5-pro"]
```

When `provider` and `model` are both set, the tier mapping is bypassed. Cost tracking, fallback chains, and confidence escalation all still work because they're orthogonal to how the model was selected.

## Tier-Locked Agents

A small number of agents are tier-locked, not model-locked. The user can change the tier mapping (and therefore the underlying model), but the stage's tier itself is not exposed in the standard configuration UI. Hard pinning these to a specific provider/model is possible only via explicit advanced config and comes with no support guarantees.

| Agent | Locked tier | Why |
|---|---|---|
| `orchestrator` (classifier) | `nano` | Runs on every request. Must stay cheap to preserve the cost story in `04`. |
| `memory_updater` | `nano` | Writes into `learnings.md` and `context.md`, which compound across runs. Voice/format consistency matters more than capability. |
| `summarizer` | `nano` | Called after every stage. The hierarchical summarization win in `04` only holds if it stays nearly free. |
| `test_runner` | none (deterministic) | Not a model call. See `01`. |

Critically, these are tier-locked, not provider-locked. An offline user with `nano = ollama/qwen2.5-coder:7b` runs the memory updater on their local model. The local-first commitment in `CLAUDE.md` and the offline mode in `06` both stay intact.

If a power user really wants to override one of these, they can:

```toml
[stages.memory_updater]
override_lock = true              # explicit opt-in
provider = "anthropic"
model    = "claude-haiku-4-5"
```

`flug config validate` will warn that this voids the consistency guarantee around the persistent learnings files.

## Capability-Aware Routing

Mix-and-match is safe but not free. Some provider/model combinations have weaker guarantees than others, and the orchestrator routes around them automatically.

For each call, the orchestrator queries the adapter's capability flags (`docs/06-multi-provider.md`):

| Capability | What the orchestrator does if missing |
|---|---|
| `STRUCTURED_OUTPUT_GUARANTEED` | Wrap call in validate-and-retry up to `retry_on_validation` (default 3). On final failure, escalate to `escalate_to`. |
| `PROMPT_CACHING` | Skip cache marking for this stage. Warn at validation if cache-heavy stages route to non-caching providers. |
| `TOOL_USE` | Skip JIT context loading via `CodebaseTools` (`04`). Fall back to pre-loaded context. |
| `REASONING_TOKENS` | Track separately in cost accounting. Trajectory monitor token budget includes them. |

The user does not need to know any of this. They pin a model; the orchestrator adapts.

## Confidence Escalation Respects User Pins

The model tiering pattern in `docs/04-token-optimization.md` says: run on the cheap model first, escalate to a stronger one if confidence is low. This still works under per-agent pinning, with one critical adjustment.

**If the user pins a specific model, escalation goes to their `escalate_to`, not a hardcoded next tier.** Otherwise we silently override their choice.

Resolution rules:

1. If `[stages.<name>] escalate_to` is set to a tier name: resolve that tier through `[models.tiers]`.
2. If `[stages.<name>] escalate_to` is set to `provider/model`: use that concrete target.
3. Else if the stage uses a `tier`: escalate one tier up (`nano -> small -> medium -> large`).
4. Else if the stage pins a `provider/model` with no `escalate_to`: escalate is disabled. Surface to user on low confidence.

This means a user who pins `code_writer = anthropic/claude-sonnet-4-5` with no `escalate_to` will never silently get a more expensive model behind their back. Predictable cost is more important than rescue logic for users who explicitly opted out.

## Cost Accounting Per Agent

Every adapter reports `tokens_input`, `tokens_output`, `tokens_cached`, `tokens_cache_write`, and `cost_usd` per call (see `StructuredResponse` in `06`). The orchestrator persists per-stage cost into the `stages` table (`02`). Per-agent reporting falls out for free:

```bash
flug cost --by-stage --period=7d
```

```
Stage              Calls   Input    Output   Cached   Cost
code_writer        47      89,210   24,180   12,400   $1.84
architect          12      31,400   18,900       0    $0.94
critic             12      24,800    9,720       0    $0.72  (o3-mini, 1 escalation)
static_analyzer    47      18,900    4,200    8,100   $0.11  (qwen on ollama)
memory_updater     47       7,050    1,880       0    $0.04
...
Total                                                  $4.11  (vs $11.80 baseline)
```

If a user pins something expensive to a small agent, this report makes it obvious within one run. No magic, no hidden costs.

## Validation: Warnings, Not Errors

`flug config validate` is the user-facing safety net. It runs deterministic checks against the resolved configuration and emits warnings (not blocks) for things that are likely user error but might be intentional.

| Check | Severity | Trigger |
|---|---|---|
| Critic on tier <= `small` | warning | Defeats adversarial design phase (`03`). |
| Architect on tier <= `small` | warning | Designs without sufficient capability propagate weak choices. |
| Cache-heavy stage on non-caching provider | info | Loses prompt-cache savings (`04`). Common with mixed-provider setups. |
| Code writer with no fallback | info | Pipeline aborts on writer outage with no recovery. |
| `escalate_to` points to weaker model than primary | error | Almost certainly a typo. |
| `escalate_to` references unconfigured provider | error | Will fail at runtime. |
| Tier-locked agent overridden without `override_lock` | error | Refuse the config. |
| Tier-locked agent overridden with `override_lock` | warning | Voids consistency guarantees. |
| Memory updater pinned to a different provider than nano tier | warning | Local-first / offline mode may break for that stage. |

The philosophy is: tell the user, don't decide for them. The exception is the tier-lock override, which requires explicit opt-in because the failure mode (corrupted persistent memory) is silent and hard to diagnose later.

## What This Looks Like to the User

### First-time setup

```bash
$ flug init

Configuring providers... [Anthropic, OpenAI, Ollama detected]
Setting up tier mappings:
  nano    -> anthropic / claude-haiku-4-5
  small   -> anthropic / claude-haiku-4-5
  medium  -> anthropic / claude-sonnet-4-5
  large   -> anthropic / claude-sonnet-4-5

Use these defaults? [Y/n]
Per-agent overrides can be configured later with: flug config models --advanced
```

The standard flow never mentions per-agent configuration. New users see tiers only.

### Switching one agent

```bash
$ flug config models --advanced
Current per-agent overrides:
  (none, all agents use tier defaults)

Pin a specific agent to a model? [agent name, or Q to quit]
> critic
Provider: openai
Model: o3-mini
Temperature [0.0]: 0.3
Confidence threshold [0.85]:
Escalate to [openai/o3]:

Saved. Validating...
  ✓ critic -> openai/o3-mini (escalation: openai/o3)
  ✓ All other agents follow tier defaults

Run validation: flug config validate
```

### Inspecting the resolved config

```bash
$ flug config models --show

Resolved per-agent configuration:
  orchestrator        nano    (locked)        anthropic/claude-haiku-4-5
  requirements        small                   anthropic/claude-haiku-4-5
  architect           large                   anthropic/claude-sonnet-4-5
  critic              -       (pinned)        openai/o3-mini
  spec_writer         small                   anthropic/claude-haiku-4-5
  code_writer         medium                  anthropic/claude-sonnet-4-5
  static_analyzer     nano                    anthropic/claude-haiku-4-5
  test_generator      small                   anthropic/claude-haiku-4-5
  test_runner         -       (deterministic) -
  security_auditor    medium                  anthropic/claude-sonnet-4-5
  reviewer            small                   anthropic/claude-haiku-4-5
  reporter            nano                    anthropic/claude-haiku-4-5
  summarizer          nano    (locked)        anthropic/claude-haiku-4-5
  memory_updater      nano    (locked)        anthropic/claude-haiku-4-5
```

Locked rows are visually distinct. Pinned rows show their override. Tier rows show the resolved model from the current `[models.tiers]` block.

### One-shot CLI overrides

```bash
flug --stage-model architect=openai/gpt-5 "design the rate limiter"
flug --stage-tier code_writer=large "implement the spec"
flug --offline "do everything local for this run"
```

These compose: `--offline` forces all stages to providers in the local set, even if the resolved config would normally route to cloud.

## The Pitch

> Flug exposes per-agent model configuration as a first-class feature: pin the architect to one provider, the critic to another, the static analyzer to a local model on Ollama, and the pipeline still composes because every agent communicates via validated Pydantic schemas. A handful of cheap, voice-sensitive agents are tier-locked rather than model-locked, so the local-first guarantee survives. Cost accounting and confidence escalation respect user pins instead of silently overriding them.

Mix and match without surprises. That's the feature.
