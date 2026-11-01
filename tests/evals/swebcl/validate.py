#!/usr/bin/env python3
"""Validate SWE-bench task data against a pristine fixture."""

import argparse
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import time
from typing import Dict, List, Optional


HERE = pathlib.Path(__file__).resolve().parent
SCRATCH_ROOT = pathlib.Path.home() / ".cache" / "splice-bench" / "validate"


def _task_data_path(task_path: pathlib.Path) -> pathlib.Path:
    task = json.loads(task_path.read_text(encoding="utf-8"))
    marker = "--task "
    check = task.get("check", "")
    if marker not in check:
        raise ValueError("task %s has no grade.py --task path" % task_path)
    raw = check.split(marker, 1)[1].strip().split()[0]
    return pathlib.Path(raw.strip("'\""))


def _run(command: List[str], cwd: pathlib.Path, input_text: Optional[str] = None) -> subprocess.CompletedProcess:
    return subprocess.run(command, cwd=str(cwd), input=input_text, text=True,
                          stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                          check=False)


def validate_task(task_path: pathlib.Path, scratch: pathlib.Path) -> Dict[str, object]:
    started = time.monotonic()
    row: Dict[str, object] = {"id": task_path.stem, "base_fail": False, "gold_pass": False,
                              "seconds": 0.0, "error": ""}
    base = ""
    repo_ready = False
    try:
        task = json.loads(task_path.read_text(encoding="utf-8"))
        row["id"] = str(task.get("id") or task_path.stem)
        data_path = _task_data_path(task_path)
        data = json.loads(data_path.read_text(encoding="utf-8"))
        fixture = pathlib.Path(task["fixture"]).expanduser().resolve()
        base = str(data["base_commit"])
        gold = str(data.get("gold_patch") or "")
        if not fixture.is_dir():
            raise ValueError("fixture does not exist: %s" % fixture)
        shutil.copytree(str(fixture), str(scratch), dirs_exist_ok=True)
        checkout = _run(["git", "checkout", "-qf", base], scratch)
        if checkout.returncode != 0:
            raise RuntimeError("checkout base failed: %s" % checkout.stdout.strip())
        clean = _run(["git", "clean", "-qfdx", "-e", ".splice"], scratch)
        if clean.returncode != 0:
            raise RuntimeError("clean failed: %s" % clean.stdout.strip())
        repo_ready = True

        env = os.environ.copy()
        if not env.get("SWEBCL_PYTHON"):
            raise ValueError("SWEBCL_PYTHON is required")
        grade = str(HERE / "grade.py")
        base_run = _run([env["SWEBCL_PYTHON"], grade, "--task", str(data_path)], scratch)
        row["base_fail"] = base_run.returncode == 1
        if base_run.returncode not in (1,):
            raise RuntimeError("base grade returned %d: %s" % (base_run.returncode, base_run.stdout.strip()))

        applied = _run(["git", "apply", "--whitespace=nowarn", "-"], scratch, gold)
        if applied.returncode != 0:
            raise RuntimeError("gold patch does not apply: %s" % applied.stdout.strip())
        gold_run = _run([env["SWEBCL_PYTHON"], grade, "--task", str(data_path)], scratch)
        row["gold_pass"] = gold_run.returncode == 0
        if gold_run.returncode not in (0, 1):
            raise RuntimeError("gold grade returned %d: %s" % (gold_run.returncode, gold_run.stdout.strip()))
        reset = _run(["git", "reset", "--hard", "-q", base], scratch)
        if reset.returncode != 0:
            raise RuntimeError("reset failed: %s" % reset.stdout.strip())
        _run(["git", "clean", "-qfdx", "-e", ".splice"], scratch)
    except (OSError, ValueError, json.JSONDecodeError, RuntimeError) as exc:
        row["error"] = str(exc)
    finally:
        row["seconds"] = round(time.monotonic() - started, 3)
        if repo_ready and base:
            _run(["git", "reset", "--hard", "-q", base], scratch)
            _run(["git", "clean", "-qfdx", "-e", ".splice"], scratch)
        shutil.rmtree(scratch, ignore_errors=True)
    return row


def validate(taskset: str, limit: Optional[int] = None) -> List[Dict[str, object]]:
    task_dir = pathlib.Path(taskset).resolve() / "tasks"
    paths = sorted(task_dir.glob("*.json"))
    if limit is not None:
        paths = paths[:limit]
    SCRATCH_ROOT.mkdir(parents=True, exist_ok=True)
    rows: List[Dict[str, object]] = []
    for path in paths:
        scratch = pathlib.Path(tempfile.mkdtemp(prefix="swebcl-", dir=str(SCRATCH_ROOT)))
        rows.append(validate_task(path, scratch))
    output = pathlib.Path(taskset).resolve() / "validation.json"
    output.write_text(json.dumps({"tasks": rows}, indent=2) + "\n", encoding="utf-8")
    return rows


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--taskset", required=True)
    parser.add_argument("--limit", type=int)
    args = parser.parse_args(argv)
    try:
        rows = validate(args.taskset, args.limit)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print("validate.py: %s" % exc, file=sys.stderr)
        return 2
    for row in rows:
        print(json.dumps(row, sort_keys=True, separators=(",", ":")))
    return 0 if all(row["base_fail"] and row["gold_pass"] and not row["error"] for row in rows) else 1


if __name__ == "__main__":
    sys.exit(main())
