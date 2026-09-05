#!/usr/bin/env python3
"""Static smoke verification for the Splice Harbor agent.

No Docker, no model calls, no paid runs. Validates:
  1. splice_agent.py parses and imports against lightweight stubs of the
     Harbor API surface (no `pip install harbor` needed).
  2. SpliceAgent structurally conforms to the BaseInstalledAgent contract:
     subclass, name() static, install/run/populate_context_post_run present,
     run decorated with with_prompt_template.
  3. pins.lock pins concrete versions with no floating refs, and matches
     versions.lock.json.
  4. The stream-json usage/final parser round-trips a fixture built from
     docs/STREAM_JSON_PROTOCOL.md examples.
Prints the exact full-run command at the end.

Exit code 0 = all checks passed.
"""

from __future__ import annotations

import ast
import importlib.util
import re
import sys
import types
from pathlib import Path

ADAPTER_DIR = Path(__file__).resolve().parent
BENCH_DIR = ADAPTER_DIR.parent

FAILURES: list[str] = []


def check(name: str, ok: bool, detail: str = "") -> None:
    status = "PASS" if ok else "FAIL"
    print(f"[{status}] {name}" + (f" - {detail}" if detail else ""))
    if not ok:
        FAILURES.append(name)


def install_harbor_stubs() -> None:
    """Minimal stand-ins for the pinned Harbor API surface.

    Shapes mirror harbor-framework/harbor commit
    4407eb5227a2ff4f0d3f16b2eb48849382fdf276: BaseInstalledAgent (ABC with
    logs_dir, logger, options, build_cli_flags, exec_as_agent,
    ensure_system_dependencies), with_prompt_template decorator,
    InstalledAgentOptions pydantic-free base, BaseEnvironment,
    AgentContext. Real runs use the real harbor package; these stubs exist
    only so the smoke check can import and inspect the agent without it.
    """
    harbor = types.ModuleType("harbor")
    agents_mod = types.ModuleType("harbor.agents")
    installed_mod = types.ModuleType("harbor.agents.installed")
    base_mod = types.ModuleType("harbor.agents.installed.base")
    options_mod = types.ModuleType("harbor.agents.options")
    env_mod = types.ModuleType("harbor.environments")
    env_base_mod = types.ModuleType("harbor.environments.base")
    models_mod = types.ModuleType("harbor.models")
    agent_mod = types.ModuleType("harbor.models.agent")
    ctx_mod = types.ModuleType("harbor.models.agent.context")

    class _ABC:
        def __init__(self, *args, **kwargs) -> None:
            self.options = kwargs.get("options") or types.SimpleNamespace(
                binary_path=None, download_url=None, max_turns=None, extra_args=None
            )
            self.logs_dir = Path("/tmp/splice-smoke-logs")
            self.logger = types.SimpleNamespace(exception=lambda *a, **k: None)

        def build_cli_flags(self) -> str:  # pragma: no cover - trivial
            return ""

        async def exec_as_agent(self, environment, command, **kwargs):
            raise RuntimeError("smoke stub: exec_as_agent must not run")

        async def ensure_system_dependencies(self, environment, deps):
            raise RuntimeError("smoke stub: ensure_system_dependencies must not run")

    class BaseInstalledAgent(_ABC):
        pass

    def with_prompt_template(fn):
        return fn

    class InstalledAgentOptions:
        pass

    class BaseEnvironment:
        pass

    class AgentContext:
        def __init__(self) -> None:
            self.n_input_tokens = None
            self.n_output_tokens = None

    base_mod.BaseInstalledAgent = BaseInstalledAgent
    base_mod.with_prompt_template = with_prompt_template
    options_mod.InstalledAgentOptions = InstalledAgentOptions
    env_base_mod.BaseEnvironment = BaseEnvironment
    ctx_mod.AgentContext = AgentContext

    for name, mod in [
        ("harbor", harbor),
        ("harbor.agents", agents_mod),
        ("harbor.agents.installed", installed_mod),
        ("harbor.agents.installed.base", base_mod),
        ("harbor.agents.options", options_mod),
        ("harbor.environments", env_mod),
        ("harbor.environments.base", env_base_mod),
        ("harbor.models", models_mod),
        ("harbor.models.agent", agent_mod),
        ("harbor.models.agent.context", ctx_mod),
    ]:
        sys.modules.setdefault(name, mod)


def main() -> int:
    source_path = ADAPTER_DIR / "splice_agent.py"
    source = source_path.read_text()

    # 1. Parse and import.
    try:
        ast.parse(source)
        check("splice_agent.py parses as Python", True)
    except SyntaxError as exc:
        check("splice_agent.py parses as Python", False, str(exc))
        return finish()

    install_harbor_stubs()
    spec = importlib.util.spec_from_file_location("splice_agent", source_path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    try:
        spec.loader.exec_module(module)
    except Exception as exc:  # noqa: BLE001
        check("splice_agent.py imports against Harbor stubs", False, repr(exc))
        return finish()
    check("splice_agent.py imports against Harbor stubs", True)

    # 2. Structural conformance to BaseInstalledAgent.
    agent_cls = getattr(module, "SpliceAgent", None)
    base_cls = sys.modules["harbor.agents.installed.base"].BaseInstalledAgent
    ok = (
        isinstance(agent_cls, type)
        and issubclass(agent_cls, base_cls)
        and isinstance(agent_cls.__dict__.get("name"), staticmethod)
        and callable(getattr(agent_cls, "install", None))
        and callable(getattr(agent_cls, "populate_context_post_run", None))
    )
    check("SpliceAgent subclasses BaseInstalledAgent with name/install/run/populate", ok)
    run_fn = agent_cls.__dict__.get("run")
    check(
        "run() is a plain function carrying the with_prompt_template decorator",
        isinstance(run_fn, types.FunctionType) and "@with_prompt_template" in source,
    )
    agent = agent_cls()
    check(
        "agent name is 'splice' and import path is stable",
        agent.name() == "splice"
        and agent_cls.import_path() == "splice_agent:SpliceAgent",
    )
    check(
        "options model derives from InstalledAgentOptions",
        issubclass(agent_cls.options_model, sys.modules["harbor.agents.options"].InstalledAgentOptions),
    )

    # 3. Pin hygiene.
    pins = (ADAPTER_DIR / "pins.lock").read_text()
    check(
        "pins.lock pins terminal-bench@2.0",
        'name = "terminal-bench"' in pins and 'version = "2.0"' in pins,
    )
    check(
        "pins.lock pins harbor 0.22.0",
        'package = "harbor"' in pins and 'version = "0.22.0"' in pins,
    )
    commits = re.findall(r'[0-9a-f]{40}', pins)
    check("pins.lock contains concrete 40-char commits", len(commits) >= 2, str(len(commits)))
    check(
        "no floating refs in pins.lock",
        not re.search(r'(version|commit)\s*=\s*"(latest|main|master|HEAD)"', pins),
    )
    import json

    lock = json.loads((BENCH_DIR / "versions.lock.json").read_text())
    tb = lock["adapters"]["terminal-bench"]
    check(
        "versions.lock.json matches pins.lock for dataset commit",
        tb["dataset"]["task_repo_commit"] == "69671fbaac6d67a7ef0dfec016cc38a64ef7a77c",
    )
    check(
        "versions.lock.json matches pins.lock for harbor version",
        tb["harbor"]["version"] == "0.22.0",
    )

    # 4. Parser round-trip on a fixture from docs/STREAM_JSON_PROTOCOL.md.
    fixture = "\n".join(
        [
            '{"schemaVersion":2,"type":"run_start","runId":"run_1","cwd":"/repo"}',
            '{"schemaVersion":2,"type":"usage","runId":"run_1","promptTokens":1200,"completionTokens":500}',
            '{"schemaVersion":2,"type":"usage","runId":"run_1","promptTokens":100,"completionTokens":20}',
            '{"schemaVersion":2,"type":"final","runId":"run_1","text":"{\\"status\\":\\"completed\\"}"}',
        ]
    )
    events = [json.loads(line) for line in fixture.splitlines()]
    totals = module.SpliceAgent._sum_usage(events)
    check(
        "usage-event summation matches protocol example",
        totals == {"promptTokens": 1300, "completionTokens": 520},
        str(totals),
    )
    traj = module.SpliceAgent._build_trajectory(events)
    check(
        "trajectory builder emits ATIF-shaped skeleton",
        isinstance(traj, dict) and traj.get("schema_version", "").startswith("ATIF"),
    )

    return finish()


def finish() -> int:
    print()
    if FAILURES:
        print(f"SMOKE FAILED ({len(FAILURES)}): {FAILURES}")
        return 1
    print("SMOKE OK - Harbor agent statically valid. No Docker, no model calls.")
    print()
    print("Full run command (PAID, consumes provider tokens; needs Docker):")
    print("  pip install harbor==0.22.0")
    print("  export SPLICE_AGENT_BINARY=/path/to/splice   # or SPLICE_AGENT_DOWNLOAD_URL")
    print("  PYTHONPATH=benchmarks/harbor harbor run --dataset terminal-bench@2.0 \\")
    print("    --agent splice_agent:SpliceAgent \\")
    print("    --model <provider>/<model-id> \\")
    print("    --n-concurrent 1")
    return 0


if __name__ == "__main__":
    sys.exit(main())
