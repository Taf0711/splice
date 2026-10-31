#!/usr/bin/env python3
"""Grade one SWE-bench continual-learning task in the current workspace."""

import argparse
import ast
import json
import os
import re
import subprocess
import sys
from typing import Dict, List, Optional, Sequence, Tuple


STATUS_RE = re.compile(r"^(?P<name>.+?)\s+(?P<status>ok|[FEXfs])\s*$")
DIFF_FILE_RE = re.compile(r"^diff --git a/(.+?) b/(.+?)$")


def _clean_name(name: str) -> str:
    """Normalize the test name printed before a result token."""
    return re.sub(r"\s+\.\.\.\s*$", "", name.strip()).strip()


def parse_test_results(output: str) -> Dict[str, str]:
    """Return the last SymPy verbose result for each printed test name.

    SymPy uses ``ok`` for a pass. ``F``, ``E``, ``f``, ``X``, and ``s`` are
    not passes for SWE-bench grading, even when they are expected outcomes.
    """
    results: Dict[str, str] = {}
    for raw_line in output.splitlines():
        line = re.sub(r"\x1b\[[0-9;]*m", "", raw_line).strip()
        match = STATUS_RE.match(line)
        if not match:
            continue
        name = _clean_name(match.group("name"))
        if name:
            results[name] = match.group("status")
    return results


def _as_test_list(value: object) -> List[str]:
    if isinstance(value, list):
        return [str(item) for item in value]
    if isinstance(value, str):
        text = value.strip()
        if not text:
            return []
        try:
            parsed = ast.literal_eval(text)
        except (ValueError, SyntaxError):
            return [text]
        if isinstance(parsed, (list, tuple)):
            return [str(item) for item in parsed]
        return [text]
    return []


def touched_files(patch: str) -> List[str]:
    """Extract the repository-relative paths from a unified git patch."""
    paths: List[str] = []
    for line in patch.splitlines():
        match = DIFF_FILE_RE.match(line)
        if not match:
            continue
        left, right = match.groups()
        path = right if right != "/dev/null" else left
        if path == "/dev/null" or path.startswith("/") or ".." in path.split("/"):
            raise ValueError("unsafe test patch path: %s" % path)
        if path not in paths:
            paths.append(path)
    if not paths:
        raise ValueError("test_patch has no diff files")
    return paths


def _run(command: Sequence[str], cwd: str) -> Tuple[int, str]:
    try:
        proc = subprocess.run(command, cwd=cwd, text=True, stdout=subprocess.PIPE,
                              stderr=subprocess.STDOUT, check=False)
    except OSError as exc:
        raise RuntimeError("cannot run %s: %s" % (command[0], exc))
    return proc.returncode, proc.stdout


def reset_test_patch_files(base_commit: str, files: Sequence[str], cwd: str) -> None:
    """Restore test-patch paths to the task base before applying the patch.

    SWE-bench applies its test patch to a clean base tree. Reset model edits
    first so a model cannot make an otherwise valid patch fail to apply.
    Paths absent from the base are patch-created files and must be removed.
    """
    code, output = _run(
        ["git", "rev-parse", "--verify", "%s^{commit}" % base_commit], cwd
    )
    if code != 0:
        raise RuntimeError("base commit is not available: %s" % output.strip())

    for path in files:
        code, output = _run(["git", "ls-tree", "-r", "--name-only", base_commit, "--", path], cwd)
        if code != 0:
            raise RuntimeError("cannot inspect base path %s: %s" % (path, output.strip()))
        exists_at_base = path in output.splitlines()
        if exists_at_base:
            code, output = _run(["git", "checkout", base_commit, "--", path], cwd)
            if code != 0:
                raise RuntimeError("cannot reset test patch file %s: %s" % (path, output.strip()))
            continue

        target = os.path.join(cwd, *path.split("/"))
        if os.path.lexists(target):
            try:
                os.unlink(target)
            except OSError as exc:
                raise RuntimeError("cannot remove patch-created file %s: %s" % (path, exc))


def _summary(status: str, task_id: str, **extra: object) -> None:
    result = {"status": status, "task_id": task_id}
    result.update(extra)
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))


def grade(task_path: str) -> int:
    python = os.environ.get("SWEBCL_PYTHON", "").strip()
    if not python:
        _summary("harness_error", "", error="SWEBCL_PYTHON is required")
        return 2
    if not os.path.isfile(python) or not os.access(python, os.X_OK):
        _summary("harness_error", "", error="SWEBCL_PYTHON is not executable: %s" % python)
        return 2

    try:
        with open(task_path, "r", encoding="utf-8") as stream:
            data = json.load(stream)
        task_id = str(data.get("instance_id") or data.get("id") or "")
        if not task_id:
            raise ValueError("task data has no instance_id or id")
        base_commit = str(data.get("base_commit") or "").strip()
        if not base_commit:
            raise ValueError("task data has no base_commit")
        patch = str(data.get("test_patch") or "")
        files = touched_files(patch)
        fail_to_pass = _as_test_list(data.get("FAIL_TO_PASS", data.get("fail_to_pass", [])))
        pass_to_pass = _as_test_list(data.get("PASS_TO_PASS", data.get("pass_to_pass", [])))
        required = fail_to_pass + pass_to_pass
        if not required:
            raise ValueError("task data has no required tests")
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        _summary("harness_error", "", error=str(exc))
        return 2

    applied = False
    statuses: Dict[str, str] = {}
    result_status = "harness_error"
    result_extra: Dict[str, object] = {}
    result_code = 2
    try:
        reset_test_patch_files(base_commit, files, os.getcwd())
        apply_proc = subprocess.run(["git", "apply", "--whitespace=nowarn", "-"],
                                    input=patch, text=True, stdout=subprocess.PIPE,
                                    stderr=subprocess.STDOUT, check=False)
        if apply_proc.returncode != 0:
            _summary("harness_error", task_id, error="test patch does not apply: %s" % apply_proc.stdout.strip())
            return 2
        applied = True
        for path in files:
            code, output = _run([python, "bin/test", "-C", "--verbose", path], os.getcwd())
            statuses.update(parse_test_results(output))
            # Continue through every touched file, so the summary contains a
            # complete result set even when one file fails.
            if code < 0:
                raise RuntimeError("test runner terminated by signal for %s" % path)
        missing = [name for name in required if statuses.get(name) != "ok"]
        if missing:
            result_status = "fail"
            result_code = 1
            result_extra = {"missing": missing, "results": {name: statuses.get(name) for name in required}}
        else:
            result_status = "pass"
            result_code = 0
            result_extra = {"results": {name: statuses.get(name) for name in required}}
    except (OSError, RuntimeError) as exc:
        result_extra = {"error": str(exc)}
    finally:
        if applied:
            try:
                reverse = subprocess.run(["git", "apply", "-R", "--whitespace=nowarn", "-"],
                                         input=patch, text=True, stdout=subprocess.PIPE,
                                         stderr=subprocess.STDOUT, check=False)
                reverse_error = "" if reverse.returncode == 0 else reverse.stdout.strip()
            except OSError as exc:
                reverse_error = str(exc)
            if reverse_error:
                result_status = "harness_error"
                result_code = 2
                result_extra = {"error": "cannot revert test patch: %s" % reverse_error}
    _summary(result_status, task_id, **result_extra)
    return result_code


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--task", required=True, help="path to the per-task JSON data")
    args = parser.parse_args(argv)
    return grade(os.path.abspath(args.task))


if __name__ == "__main__":
    sys.exit(main())
