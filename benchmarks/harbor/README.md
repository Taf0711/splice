# Harbor Adapter (Terminal-Bench 2.0)

Runs Splice inside Terminal-Bench 2.0 task containers as a Harbor custom
installed agent.

## Upstream Verification (2026-09-05)

Verified over the network, not from memory:

- Harbor lives at `harbor-framework/harbor` (the `laude-institute/harbor`
  URL permanently redirects there). Latest release `v0.22.0`
  (2026-08-22), annotated-tag commit `4407eb5227a2ff4f0d3f16b2eb48849382fdf276`.
  PyPI package `harbor` reports `0.22.0`.
- Harbor is the official harness for Terminal-Bench 2.0
  (`harbor run --dataset terminal-bench@2.0 --agent <agent> ...`).
- The Harbor registry (`registry.json` at the pinned commit) lists dataset
  entry `terminal-bench` version `2.0` with 89 tasks, all from
  `github.com/laude-institute/terminal-bench-2.git` at commit
  `69671fbaac6d67a7ef0dfec016cc38a64ef7a77c`.
- Custom agents load by import path: `harbor run --agent <module.path:ClassName>`
  (`AgentFactory.create_agent_from_import_path`, format documented in
  `src/harbor/utils/import_path.py`).
- The official path for agents that install themselves into the task
  container is `BaseInstalledAgent` (`src/harbor/agents/installed/base.py`),
  a subclass of `BaseAgent`. Subclasses set an options model, implement
  `name()`, `install(environment)` (runs as agent user inside the container),
  and `run(instruction, environment, context)` decorated with
  `@with_prompt_template`. `exec_as_agent(environment, command=...)` executes
  commands inside the container. After `run`, Harbor calls
  `populate_context_post_run(context)` where agents may write an ATIF
  trajectory to `self.logs_dir / "trajectory.json"` (see the built-in kimi
  agent for the reference pattern) and report token totals on `context`.

## Status: VERIFIED UPSTREAM, SCAFFOLDING ONLY

`splice_agent.py` implements `BaseInstalledAgent` per the verified interface.
It has been statically validated (parse, import with stubs, structural
conformance) but has not been executed inside a real Harbor trial. Live runs
need Docker, provider credentials, and a Splice binary artifact.

How Splice runs inside the task container:

1. `install()` downloads the pinned Splice release artifact into the
   container (or uses a local `SPLICE_AGENT_BINARY` path copied in by the
   operator) and checks `splice --version`.
2. `run()` executes
   `splice exec --output-format stream-json <instruction>` with the exact
   task instruction as the prompt, from the task workspace. The instruction
   is passed verbatim, never paraphrased.
3. The workspace is preserved: the agent writes no files into the task
   workspace and changes no task state; Splice's stream output is redirected
   to `/logs/agent/`.
4. `populate_context_post_run()` exports the raw Splice stream-json trace and
   parses the terminal usage events into the Harbor `AgentContext` totals.

## Files

- `splice_agent.py` - The Harbor custom agent (`module.path:ClassName`
  import path: `splice_agent:SpliceAgent` when this directory is on
  `PYTHONPATH`).
- `pins.lock` - Pinned terminal-bench@2.0 dataset, Harbor version, and
  adapter version. Mirrored in `../../versions.lock.json`.
- `smoke_verify.py` - Static validation, no Docker and no model calls.
- `results/` - Created by real runs (not committed). Layout below.

## Raw Result Preservation Layout

Harbor writes trial results under its `--jobs-dir`. This adapter additionally
exports Splice-specific evidence into the trial agent logs directory:

```
<jobs_dir>/<job>/<trial>/agent/   (Harbor-managed logs_dir)
  splice-stream.jsonl   Raw splice exec --output-format stream-json output
  splice-final.json     Parsed terminal `final` event (if present)
  trajectory.json       ATIF trajectory (written by populate_context_post_run)
  result.json           Harbor's authoritative evaluator verdict (upstream-owned)
```

The Harbor/Terminal-Bench test suite verdict in `result.json` is the only
authoritative score. The Splice trace is evidence, not a verdict.

## Smoke Verification (no paid run, no Docker)

```bash
python3 benchmarks/harbor/smoke_verify.py
```

Checks: the agent module parses; its imports resolve against lightweight
stubs of the Harbor API surface (so the module loads without `pip install
harbor`); the class structurally conforms to the `BaseInstalledAgent`
contract (subclass, `name()` static, `install`/`run`/`populate_context_post_run`
signatures); the pin file pins concrete versions with no floating refs; and
the stream-json event parser round-trips a fixture. It prints the exact
full-run command.

## Full Run (paid; consumes provider tokens)

```bash
pip install harbor==0.22.0
export SPLICE_AGENT_BINARY=/path/to/splice        # or SPLICE_AGENT_DOWNLOAD_URL
export <provider API key env>                      # e.g. OPENROUTER_API_KEY
harbor run --dataset terminal-bench@2.0 \
  --agent splice_agent:SpliceAgent \
  --model <provider>/<model-id> \
  --n-concurrent 1
```

Run it with this directory on `PYTHONPATH` so the import path resolves:

```bash
PYTHONPATH=benchmarks/harbor harbor run --dataset terminal-bench@2.0 \
  --agent splice_agent:SpliceAgent --model <provider>/<model-id>
```

A single-task smoke against the free-flowing dataset is not possible without
credentials; every trial invokes the model. The cheapest paid verification is
one task:

```bash
PYTHONPATH=benchmarks/harbor harbor run -d terminal-bench@2.0 \
  -a splice_agent:SpliceAgent -m <provider>/<model-id> \
  -t <task-name>
```
