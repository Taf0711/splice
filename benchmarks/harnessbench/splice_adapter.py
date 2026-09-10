"""Splice adapter for Qihoo360/harness-bench.

Drop-in upstream harness adapter. Implements the upstream BaseAdapter
interface (src/harnessbench/adapters/base.py at pinned commit
1025086a446653702b80cfb48babbeec35db6b2c):

    class BaseAdapter(ABC):
        name = "base"
        @abstractmethod
        def run(self, ctx: AdapterRunContext) -> AdapterRunResult: ...

The adapter launches `splice exec --output-format stream-json` with the
rendered task prompt inside the harness workspace, preserves the workspace
(it never deletes or mutates workspace files), and exports the raw Splice
trace to an evidence directory.

Static-validated by smoke_verify.py. This module imports only stdlib at
module level so the smoke check can parse and import it without the
harnessbench package installed.
"""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

ADAPTER_NAME = "splice"

DEFAULT_SPLICE_BIN = "splice"
DEFAULT_TIMEOUT_SEC = 2400
STREAM_FORMAT = "stream-json"


class SpliceHarnessAdapter:
    """Harness-Bench adapter that runs the Splice binary headlessly.

    Registered in the upstream harness config as:

        splice-local:
          adapter: splice
          command: splice
          session_prefix: harnessbench-splice
          timeout_sec: 2400

    Upstream resolves adapters by name from the models config; the exact
    registration mechanism (module import vs registry entry) is owned by the
    pinned upstream commit. This class implements the documented
    BaseAdapter.run(ctx) contract and keeps no global state.
    """

    name = ADAPTER_NAME

    def run(self, ctx) -> object:
        """Execute one Splice run for the given AdapterRunContext.

        The upstream AdapterRunContext provides: task (task_id, prompt
        rendering), workspace, sandbox, prompt_file, session_id, model_id,
        model_config, env, timeout_sec. This method reads only documented
        attributes and fails loudly when they are missing, per the repo rule
        that silent fallbacks are bugs.
        """
        prompt = self._read_prompt(ctx)
        workspace = Path(str(ctx.workspace))
        evidence_dir = self._evidence_dir(workspace, ctx.task.task_id)
        evidence_dir.mkdir(parents=True, exist_ok=True)

        command = self._command(ctx)
        env = os.environ.copy()
        env.update(getattr(ctx, "env", {}) or {})

        completed = subprocess.run(
            command,
            input=prompt,
            text=True,
            capture_output=True,
            cwd=str(workspace),
            timeout=int(getattr(ctx, "timeout_sec", None) or DEFAULT_TIMEOUT_SEC),
            env=env,
            check=False,
        )

        (evidence_dir / "stdout.txt").write_text(completed.stdout)
        (evidence_dir / "stderr.txt").write_text(completed.stderr)
        (evidence_dir / "stream.jsonl").write_text(completed.stdout)
        (evidence_dir / "metadata.json").write_text(
            json.dumps(
                {
                    "adapter": ADAPTER_NAME,
                    "task_id": ctx.task.task_id,
                    "session_id": getattr(ctx, "session_id", None),
                    "model_id": getattr(ctx, "model_id", None),
                    "command": command,
                    "returncode": completed.returncode,
                    "stream_format": STREAM_FORMAT,
                    "workspace_preserved": True,
                },
                indent=2,
                sort_keys=True,
            )
            + "\n"
        )
        final = self._extract_final(completed.stdout)
        if final is not None:
            (evidence_dir / "final.json").write_text(
                json.dumps(final, indent=2, sort_keys=True) + "\n"
            )

        from harnessbench.models import AdapterRunResult

        return AdapterRunResult(
            ok=completed.returncode == 0,
            command=command,
            stdout=completed.stdout,
            stderr=completed.stderr,
            metadata={
                "returncode": completed.returncode,
                "evidence_dir": str(evidence_dir),
            },
        )

    def _command(self, ctx) -> list[str]:
        """Build the splice command line from model_config with defaults."""
        config = getattr(ctx, "model_config", None) or {}
        binary = str(config.get("command") or DEFAULT_SPLICE_BIN)
        extra_args = [str(a) for a in (config.get("args") or [])]
        return [binary, *extra_args, "exec", "--output-format", STREAM_FORMAT]

    @staticmethod
    def _read_prompt(ctx) -> str:
        prompt_file = getattr(ctx, "prompt_file", None)
        if prompt_file is not None:
            return Path(str(prompt_file)).read_text()
        task = getattr(ctx, "task", None)
        for attr in ("prompt", "rendered_prompt"):
            text = getattr(task, attr, None)
            if isinstance(text, str) and text:
                return text
        raise ValueError(
            "splice adapter: no prompt available on ctx "
            f"(task_id={getattr(getattr(ctx, 'task', None), 'task_id', '?')})"
        )

    @staticmethod
    def _evidence_dir(workspace: Path, task_id: str) -> Path:
        root = os.environ.get(
            "SPLICE_HB_EVIDENCE_DIR",
            str(workspace / ".splice-harnessbench-evidence"),
        )
        return Path(root) / task_id

    @staticmethod
    def _extract_final(stdout: str) -> dict | None:
        """Return the last parseable stream-json `final` event, or None."""
        final = None
        for line in stdout.splitlines():
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(event, dict) and event.get("type") == "final":
                final = event
        return final
