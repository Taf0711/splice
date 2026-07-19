# Multi-Provider Support

## Why This Is Foundational

Provider lock-in is a real liability. Engineers using Flug will have strong opinions about which models they trust. Designing this right also opens up local model support (Ollama, llama.cpp), which is a significant feature for privacy-conscious users.

Build it as foundational, not bolted on later.

## The Right Abstraction Layer

Don't use LiteLLM, OpenRouter, or similar meta-libraries. They abstract away exactly the details we need to control: prompt caching, structured output mechanics, provider-specific retry logic, and cost accounting.

Build a thin adapter layer ourselves. Roughly 150-200 lines per provider. Full control retained.

## The Single Interface

Every adapter implements:

```python
from typing import Protocol
from pydantic import BaseModel

class LLMProvider(Protocol):
    name: str                              # "anthropic", "openai", "google", "ollama"

    async def structured_call(
        self,
        messages: list[Message],
        output_schema: type[BaseModel],
        model: str,
        max_tokens: int,
        cache_segments: list[CacheSegment] | None = None,
        temperature: float = 0.0,
    ) -> StructuredResponse: ...

    async def text_call(
        self,
        messages: list[Message],
        model: str,
        max_tokens: int,
        cache_segments: list[CacheSegment] | None = None,
    ) -> TextResponse: ...

    async def count_input_tokens(
        self,
        messages: list[Message],
        model: str,
        cache_segments: list[CacheSegment] | None = None,
        output_schema: type[BaseModel] | None = None,
    ) -> int: ...

    def supports(self, capability: Capability) -> bool: ...
    def cost_per_token(self, model: str) -> CostInfo: ...

class StructuredResponse(BaseModel):
    parsed: BaseModel              # already validated
    raw_json: str
    tokens_input: int
    tokens_output: int
    tokens_cached: int
    tokens_cache_write: int
    model_used: str
    latency_ms: int
    cost_usd: float
```

Every agent calls `provider.structured_call(...)`. Agents don't know which provider it is. The orchestrator picks based on configuration and task tier. The budget wrapper may call `count_input_tokens(...)` before generation when the provider supports exact counting; otherwise it uses a conservative estimate and records that the count was estimated.

## Capability Matrix

Providers differ in non-obvious ways. Encode the differences explicitly so the orchestrator can route around limitations:

| Capability | Anthropic | OpenAI | Google | Groq/Together | Ollama (local) |
|---|---|---|---|---|---|
| Structured output (guaranteed) | tool use, ~99% | native (constrained decoding) | function calling | varies | best-effort + retry |
| Prompt caching | yes (5min TTL) | automatic (50% discount) | yes (explicit) | no | n/a |
| Tool use | yes | yes | yes | varies | varies |
| Vision | yes | yes | yes | varies | varies |
| Reasoning models | claude-thinking | o1, o3, o4 | gemini-thinking | no | qwq, deepseek-r1 |
| Streaming | yes | yes | yes | yes | yes |
| Context window | up to 200k | up to 128k | up to 2M | varies | model-dependent |

The orchestrator queries `provider.supports(Capability.PROMPT_CACHING)` before deciding whether to chunk a prompt for caching. If a provider doesn't support a capability, it either degrades gracefully (extra retry on validation) or routes to a different provider.

## Configuration Hierarchy

Five layers, later overriding earlier:

1. **Built-in defaults** (in code): sane fallback chain
2. **Global config** (`~/.config/flug/config.toml`): user's personal setup
3. **Project config** (`./.flug/config.toml`): repo-specific overrides
4. **Environment variables** (`FLUG_*`): for CI/CD
5. **CLI flags** (`--model=...`): one-shot overrides

### Example global config

```toml
# ~/.config/flug/config.toml

[providers.anthropic]
enabled = true
# api_key stored in OS keychain, not here

[providers.openai]
enabled = true

[providers.ollama]
enabled = true
base_url = "http://localhost:11434"

[providers.google]
enabled = false

# Map abstract tiers to concrete models
[models.tiers]
nano = { provider = "anthropic", model = "claude-haiku-4-5" }
small = { provider = "anthropic", model = "claude-haiku-4-5" }
medium = { provider = "anthropic", model = "claude-sonnet-4-5", thinking_level = "medium" }
large = { provider = "anthropic", model = "claude-sonnet-4-5", thinking_level = "high" }
reasoning = { provider = "openai", model = "o3-mini", thinking_level = "high" }

# Per-stage overrides (optional)
[stages.code_writer]
tier = "medium"
thinking_level = "high"
fallback = ["openai/gpt-5", "google/gemini-2.5-pro"]

[stages.security_auditor]
tier = "medium"

[stages.architect]
tier = "large"

[budget]
default_max_usd = 1.00
abort_on_overage = true
```

### Project override example

A team that's adopted Gemini company-wide:

```toml
# ./.flug/config.toml
[models.tiers]
nano = { provider = "google", model = "gemini-2.5-flash-lite" }
medium = { provider = "google", model = "gemini-2.5-flash" }
large = { provider = "google", model = "gemini-2.5-pro" }
```

Every Flug run in that repo uses Gemini regardless of personal preferences. Matters for compliance and cost-allocation.

## API Key Management

Never store keys in plaintext config. Use the OS keychain via the `keyring` library. Cross-platform, secure, standard:

```python
import keyring

# On first run or via `flug config set-key`
keyring.set_password("flug", "anthropic_api_key", api_key)

# At provider initialization
api_key = keyring.get_password("flug", "anthropic_api_key")
```

Resolution hierarchy:

1. CLI flag: `--anthropic-key=sk-...` (rare, mainly for CI)
2. Environment variable: `ANTHROPIC_API_KEY` (standard for CI/CD)
3. OS keychain entry: `flug:anthropic_api_key` (default for human users)
4. If none found: prompt interactively or fail with helpful error

The `flug config` subcommand handles key management without exposing keys to terminal history:

```bash
flug config set-key anthropic
# Prompts: "Enter your Anthropic API key: " (input hidden)
# Stored in keychain.

flug config list-keys
# anthropic: ✓ (set, last validated 2 hours ago)
# openai: ✓ (set, last validated 2 days ago)
# google: ✗ (not set)
# ollama: n/a (local, no key needed)

flug config validate
# Tests each configured key with a tiny call. Reports working/broken.

flug config rotate-key anthropic
# Replaces existing key after validating new one works.
```

Multiple keys per provider (work vs personal) handled via named profiles:

```bash
flug --profile=work "fix this bug"
```

## Provider Adapters in Detail

Each adapter is a single file, ~200 lines. Showing the structured-output method for two so the differences are concrete.

### Anthropic

```python
# providers/anthropic.py
import anthropic
import json
from .base import LLMProvider, StructuredResponse

class AnthropicProvider(LLMProvider):
    name = "anthropic"

    def __init__(self, api_key: str):
        self.client = anthropic.AsyncAnthropic(api_key=api_key)

    async def structured_call(self, messages, output_schema, model, **kwargs):
        # Anthropic uses tool use for guaranteed structured output
        tool_def = {
            "name": "submit",
            "description": "Submit the structured response",
            "input_schema": output_schema.model_json_schema(),
        }

        # Apply prompt caching to system messages if requested
        system = self._build_system(messages, kwargs.get("cache_segments"))

        response = await self.client.messages.create(
            model=model,
            system=system,
            messages=[m for m in messages if m.role != "system"],
            tools=[tool_def],
            tool_choice={"type": "tool", "name": "submit"},
            max_tokens=kwargs.get("max_tokens", 4096),
        )

        tool_use = next(b for b in response.content if b.type == "tool_use")
        parsed = output_schema.model_validate(tool_use.input)

        return StructuredResponse(
            parsed=parsed,
            raw_json=json.dumps(tool_use.input),
            tokens_input=response.usage.input_tokens,
            tokens_output=response.usage.output_tokens,
            tokens_cached=response.usage.cache_read_input_tokens or 0,
            model_used=model,
            cost_usd=self._calc_cost(response.usage, model),
        )
```

### OpenAI

```python
# providers/openai.py
from openai import AsyncOpenAI
from .base import LLMProvider, StructuredResponse

class OpenAIProvider(LLMProvider):
    name = "openai"

    def __init__(self, api_key: str):
        self.client = AsyncOpenAI(api_key=api_key)

    async def structured_call(self, messages, output_schema, model, **kwargs):
        # OpenAI has native Pydantic support via parse()
        response = await self.client.beta.chat.completions.parse(
            model=model,
            messages=[m.to_openai() for m in messages],
            response_format=output_schema,
            max_tokens=kwargs.get("max_tokens", 4096),
        )

        parsed = response.choices[0].message.parsed
        usage = response.usage

        return StructuredResponse(
            parsed=parsed,
            raw_json=parsed.model_dump_json(),
            tokens_input=usage.prompt_tokens,
            tokens_output=usage.completion_tokens,
            tokens_cached=usage.prompt_tokens_details.cached_tokens or 0,
            model_used=model,
            cost_usd=self._calc_cost(usage, model),
        )
```

The two are noticeably different. That's why the adapter pattern matters.

### Adapters to ship in v1

- Anthropic (Claude family)
- OpenAI (GPT and o-series)
- Google (Gemini)
- Ollama (local models, OpenAI-compatible)
- Generic OpenAI-compatible (handles Groq, Together, DeepSeek, Mistral, Fireworks, self-hosted vLLM out of the box, just requires base_url and API key)

That last one is huge. Any new provider exposing an OpenAI-compatible endpoint works immediately without writing a new adapter.

## Cost Tracking Across Providers

Costs vary 100x between providers. Flug tracks every call in USD and aggregates per run:

```python
# providers/pricing.py - keep updated
PRICING = {
    "anthropic": {
        "claude-sonnet-4-5": {"input": 3.00, "output": 15.00, "cache_read": 0.30, "cache_write": 3.75},
        "claude-haiku-4-5": {"input": 1.00, "output": 5.00, "cache_read": 0.10, "cache_write": 1.25},
    },
    "openai": {
        "gpt-5": {"input": 2.50, "output": 10.00, "cached_input": 1.25},
        "gpt-5-mini": {"input": 0.15, "output": 0.60, "cached_input": 0.075},
        "o3-mini": {"input": 1.10, "output": 4.40},
    },
    "google": {
        "gemini-2.5-pro": {"input": 1.25, "output": 5.00},
        "gemini-2.5-flash": {"input": 0.075, "output": 0.30},
    },
    "ollama": {"*": {"input": 0.0, "output": 0.0}},  # local, no API cost
}
# Prices in USD per million tokens. Update via `flug update-pricing`.
```

Adapter calculates exact cost per call. Orchestrator aggregates. Eval harness shows cost-per-pipeline-run broken down by provider and tier.

A nice touch: `flug cost --period=7d` shows accumulated spend by provider, by stage, by project. Engineers care about this.

## Fallback Chains

Real production setups need fallbacks. If Anthropic has an outage, Flug should fail over to OpenAI, not crash.

```toml
[stages.code_writer]
tier = "medium"
fallback = ["openai/gpt-5", "google/gemini-2.5-pro"]
fallback_on = ["rate_limit", "timeout", "5xx"]
```

Orchestrator wraps each call:

```python
async def call_with_fallback(stage_config, ...):
    primary = stage_config.primary
    fallbacks = stage_config.fallback

    for attempt, target in enumerate([primary] + fallbacks):
        try:
            return await target.structured_call(...)
        except (RateLimitError, TimeoutError, ServerError) as e:
            if attempt == len([primary] + fallbacks) - 1:
                raise
            log.warning(f"Falling back from {target} due to {e}")
            continue
        except Exception:
            raise  # don't fall back on schema validation, auth, etc.
```

**Important**: don't fall back on validation errors or auth errors. Those mean something is wrong, and silently routing hides bugs. Only fall back on genuinely transient failures.

## Local Models (Ollama)

Killer feature for privacy-conscious or cost-sensitive users. Ollama exposes OpenAI-compatible API on localhost. Flug auto-detects:

```toml
[providers.ollama]
enabled = true
base_url = "http://localhost:11434/v1"

[models.tiers]
nano = { provider = "ollama", model = "qwen2.5-coder:7b" }
small = { provider = "ollama", model = "qwen2.5-coder:14b" }
medium = { provider = "ollama", model = "qwen2.5-coder:32b" }
large = { provider = "anthropic", model = "claude-sonnet-4-5" }  # cloud for hard tasks
```

Hybrid setups like the above are realistic: local for cheap stages (orchestrator, classifier, summarizer, static analyzer interpretation), cloud for substantive ones (architect, code writer, security auditor). Cuts costs another 30-50% for power users with decent hardware.

Capability flags matter here: local models often don't reliably emit valid structured output. The adapter sets `supports(STRUCTURED_OUTPUT_GUARANTEED) = False` and the orchestrator wraps calls in a "validate and retry up to 3 times" loop, with automatic escalation to a cloud model after 2 retries.

## What This Looks Like to the User

First-time setup:

```bash
$ flug init
Welcome to Flug.

Which providers would you like to configure?
  [✓] Anthropic
  [✓] OpenAI
  [ ] Google
  [✓] Ollama (detected on localhost:11434)

Enter Anthropic API key: ************************
Validating... ✓
Stored in keychain.

Enter OpenAI API key: ************************
Validating... ✓
Stored in keychain.

Ollama detected with 4 models:
  qwen2.5-coder:32b  (recommended for medium tier)
  llama3.3:70b
  deepseek-r1:32b
  codellama:13b

Configuration written to ~/.config/flug/config.toml
You're ready. Try: flug "fix this bug"
```

Switching providers per-run:

```bash
flug --provider=openai "implement OAuth"
flug --tier-medium=ollama/qwen2.5-coder:32b "refactor this"
flug --offline "analyze this code"   # local-only, no cloud calls
```

That `--offline` flag is a strong feature for users in regulated environments.

## The Pitch

> Flug supports any LLM provider behind a unified interface: Anthropic, OpenAI, Google, Groq, Together, DeepSeek, Mistral, and self-hosted models via Ollama or vLLM. Configure per-stage routing, set fallback chains, mix cloud and local models, or run fully offline. API keys are stored in your OS keychain. No vendor lock-in, no telemetry by default.

The "fully offline mode" line gets attention. It's the feature finance, healthcare, and government engineers actually care about and that almost no agent system supports cleanly.
