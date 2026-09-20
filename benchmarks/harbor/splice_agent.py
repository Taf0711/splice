"""Splice custom Harbor agent for Terminal-Bench 2.0.

Implements the Harbor installed-agent path documented at pinned Harbor
commit 4407eb5227a2ff4f0d3f16b2eb48849382fdf276
(src/harbor/agents/installed/base.py):

    class BaseInstalledAgent(BaseAgent, ABC):
        options_model = <InstalledAgentOptions subclass>
        @staticmethod def name() -> str: ...
        async def install(self, environment: BaseEnvironment) -> None: ...
        @with_prompt_template
        async def run(self, instruction, environment, context) -> None: ...
        def populate_context_post_run(self, context: AgentContext) -> None: ...

Usage:

    PYTHONPATH=benchmarks/harbor harbor run --dataset terminal-bench@2.0 \
        --agent splice_agent:SpliceAgent --model <provider>/<model-id>

Inside the task container the agent launches:

    splice exec --output-format stream-json <instruction>

with the exact task instruction, from the task workspace. The workspace is
preserved (the agent itself writes only under /logs/agent). The raw Splice
stream is exported to /logs/agent/splice-stream.jsonl and parsed into an
ATIF trajectory plus Harbor AgentContext token totals.

This module is statically validated by smoke_verify.py against stubs of the
Harbor API surface; installing `harbor==0.22.0` provides the real classes at
run time.
"""

from __future__ import annotations

import json
import os
import shlex
from pathlib import Path
from typing import Any

from harbor.agents.installed.base import BaseInstalledAgent, with_prompt_template
from harbor.agents.options import InstalledAgentOptions
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

ADAPTER_VERSION = "0.1.0"

SPLICE_BINARY_ENV = "SPLICE_AGENT_BINARY"
SPLICE_DOWNLOAD_URL_ENV = "SPLICE_AGENT_DOWNLOAD_URL"
DEFAULT_SPLICE_INSTALL_ROOT = "/opt/splice"

LOG_DIR = "/logs/agent"
STREAM_FILENAME = "splice-stream.jsonl"
FINAL_FILENAME = "splice-final.json"
STDERR_FILENAME = "splice.stderr.log"


class SpliceOptions(InstalledAgentOptions):
    """CLI/env options accepted by `harbor run --agent splice_agent:SpliceAgent`."""

    binary_path: str | None = None
    download_url: str | None = None
    max_turns: int | None = None
    extra_args: list[str] | None = None


class SpliceAgent(BaseInstalledAgent):
    """Runs the Splice binary headlessly inside the task container."""

    options_model = SpliceOptions

    @staticmethod
    def name() -> str:
        return "splice"

    @staticmethod
    def import_path() -> str:
        return "splice_agent:SpliceAgent"

    def get_version_command(self) -> str | None:
        return f"{self._binary_path()} --version"

    def parse_version(self, stdout: str) -> str:
        text = stdout.strip().splitlines()
        return text[0].strip() if text else stdout.strip()

    async def install(self, environment: BaseEnvironment) -> None:
        """Install the Splice binary into the task container.

        Two supported sources, in priority order:
          1. SPLICE_AGENT_DOWNLOAD_URL (or options.download_url): download a
             released splice artifact into /opt/splice.
          2. SPLICE_AGENT_BINARY (or options.binary_path): an operator-provided
             binary already present in the container image.
        """
        download_url = self.options.download_url or os.environ.get(
            SPLICE_DOWNLOAD_URL_ENV
        )
        if download_url:
            await self.ensure_system_dependencies(environment, ("curl",))
            await self.exec_as_agent(
                environment,
                command=(
                    "set -euo pipefail; "
                    f"sudo mkdir -p {DEFAULT_SPLICE_INSTALL_ROOT} && "
                    f"curl -fsSL {shlex.quote(download_url)} -o /tmp/splice && "
                    "sudo mv /tmp/splice /opt/splice/splice && "
                    "sudo chmod +x /opt/splice/splice && "
                    "/opt/splice/splice --version"
                ),
            )
            return

        if self._binary_path():
            await self.exec_as_agent(
                environment,
                command=f"set -euo pipefail; {self._binary_path()} --version",
            )
            return

        raise ValueError(
            "splice agent requires SPLICE_AGENT_DOWNLOAD_URL (release artifact "
            "URL) or SPLICE_AGENT_BINARY (binary baked into the task image)"
        )

    @with_prompt_template
    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        """Run `splice exec --output-format stream-json` with the exact task
        instruction. The instruction is passed verbatim."""
        binary = self._binary_path()
        extra_flags = self.build_cli_flags()

        splice_cmd = (
            f"{shlex.quote(binary)} exec --output-format stream-json "
            f"{extra_flags}{shlex.quote(instruction)}"
        )
        command = (
            f"mkdir -p {LOG_DIR} && "
            f"cd \"$HARBOR_TASK_WORKDIR\" 2>/dev/null || true; "
            f"{splice_cmd} "
            f">>{LOG_DIR}/{STREAM_FILENAME} "
            f"2>>{LOG_DIR}/{STDERR_FILENAME}"
        )
        await self.exec_as_agent(environment, command=command)

    def populate_context_post_run(self, context: AgentContext) -> None:
        """Export the Splice trace and surface token totals to Harbor.

        Reads the raw stream back from the environment-side agent logs dir,
        writes the parsed final event and the ATIF trajectory into
        self.logs_dir, and fills context token counters from Splice usage
        events (docs/STREAM_JSON_PROTOCOL.md, `usage` and `final` events).
        """
        stream_path = Path(self.logs_dir) / STREAM_FILENAME
        if not stream_path.exists():
            return
        try:
            events = [
                json.loads(line)
                for line in stream_path.read_text().splitlines()
                if line.strip().startswith("{")
            ]
        except (OSError, json.JSONDecodeError):
            self.logger.exception("Failed to parse splice stream %s", stream_path)
            return

        final = next(
            (e for e in reversed(events) if isinstance(e, dict) and e.get("type") == "final"),
            None,
        )
        if final is not None:
            (Path(self.logs_dir) / FINAL_FILENAME).write_text(
                json.dumps(final, indent=2, sort_keys=True) + "\n"
            )

        usage_totals = self._sum_usage(events)
        if usage_totals:
            context.n_input_tokens = usage_totals.get("promptTokens")
            context.n_output_tokens = usage_totals.get("completionTokens")

        trajectory = self._build_trajectory(events)
        if trajectory is not None:
            (Path(self.logs_dir) / "trajectory.json").write_text(
                json.dumps(trajectory, indent=2, sort_keys=True) + "\n"
            )

    def _binary_path(self) -> str:
        if self.options.binary_path:
            return str(self.options.binary_path)
        env_binary = os.environ.get(SPLICE_BINARY_ENV)
        if env_binary:
            return env_binary
        return f"{DEFAULT_SPLICE_INSTALL_ROOT}/splice"

    @staticmethod
    def _sum_usage(events: list[Any]) -> dict[str, int]:
        """Sum Splice `usage` events; falls back to the `final` event totals."""
        totals = {"promptTokens": 0, "completionTokens": 0}
        seen = False
        for event in events:
            if not isinstance(event, dict):
                continue
            if event.get("type") == "usage":
                seen = True
                totals["promptTokens"] += int(event.get("promptTokens") or 0)
                totals["completionTokens"] += int(event.get("completionTokens") or 0)
        if seen:
            return totals
        for event in events:
            if isinstance(event, dict) and event.get("type") == "final":
                usage = event.get("usage") or {}
                if usage:
                    return {
                        "promptTokens": int(usage.get("promptTokens") or 0),
                        "completionTokens": int(usage.get("completionTokens") or 0),
                    }
        return {}

    @staticmethod
    def _build_trajectory(events: list[Any]) -> dict[str, Any] | None:
        """Minimal ATIF-shaped trajectory from the Splice stream.

        Full ATIF fidelity (per-step tool calls, reasoning) is a follow-up;
        this records the agent identity, the stream event count, and the final
        result so every Splice trial exports at least a trace skeleton.
        """
        final = next(
            (e for e in events if isinstance(e, dict) and e.get("type") == "final"),
            None,
        )
        if final is None and not events:
            return None
        return {
            "schema_version": "ATIF-v1.8",
            "agent": {
                "name": "splice",
                "version": ADAPTER_VERSION,
                "raw_stream": STREAM_FILENAME,
            },
            "steps": [
                {
                    "step_id": 1,
                    "model_responses": [
                        {
                            "type": "output_text",
                            "content": json.dumps(final.get("text", ""))[:2000],
                        }
                    ],
                }
            ],
            "notes": "Derived from splice exec --output-format stream-json.",
        }
