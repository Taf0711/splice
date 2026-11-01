#!/usr/bin/env python3
"""EVAL-V0 task quality validator (handoff section 16).

For every task in tasks/*.json, materializes a pristine fixture copy and
proves, in order, using the task's OWN hidden `check` command:

  Check 1  BASE:   the untouched fixture FAILS the check.
  Check 2  GOLD:   a known-correct patch PASSES the check.
  Check 3  WRONG:  an obviously-incorrect patch FAILS the check.
  Check 4  HACK:   a shortcut patch FAILS the check.

Task schema: {name, prompt, check} plus optional audit metadata (gold/wrong/
hack: {path: full file content}) which the validator strips before anything
reaches the harness. Output: registry.json rows + nonzero exit on failure.

Usage: python3 validate.py [--tasks-dir tasks] [--fixture fixture] [--out registry.json]
"""

import argparse
import json
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

AUDIT_KEYS = ("gold", "wrong", "hack")


def run(cmd, cwd):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=600)


def materialize(fixture: Path) -> Path:
    workdir = Path(tempfile.mkdtemp(prefix="taskval-"))
    dst = workdir / "repo"
    shutil.copytree(fixture, dst)
    return dst


def write_files(repo: Path, files: dict) -> None:
    for rel, content in files.items():
        target = repo / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)


def runs_check(check: str, repo: Path) -> bool:
    """The hidden verifier: exit 0 = pass. Run through bash like the harness."""
    proc = subprocess.run(["bash", "-c", check], cwd=repo, capture_output=True, text=True, timeout=600)
    return proc.returncode == 0


def check_task(task: dict, audit: dict, fixture: Path) -> dict:
    row = {"name": task["name"]}

    # Check 1: BASE fails the hidden verifier.
    repo = materialize(fixture)
    row["base-fails-verifier"] = not runs_check(task["check"], repo)
    shutil.rmtree(repo.parent, ignore_errors=True)
    if not row["base-fails-verifier"]:
        row["error"] = "check PASSES on the untouched fixture; the task proves nothing"
        row["status"] = "broken"
        return row

    for key, expect_pass, label in (
        ("gold", True, "gold-passes-verifier"),
        ("wrong", False, "wrong-patch-fails"),
        ("hack", False, "hack-patch-fails"),
    ):
        patch = audit.get(key)
        if not patch:
            continue
        repo = materialize(fixture)
        write_files(repo, patch)
        passed = runs_check(task["check"], repo)
        row[label] = passed == expect_pass
        if label == "gold-passes-verifier" and not passed:
            row["error"] = "gold patch FAILS its own verifier; the task is unsolvable as specified"
        shutil.rmtree(repo.parent, ignore_errors=True)

    row["status"] = "validated" if "error" not in row else "broken"
    return row


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--tasks-dir", default="tasks")
    parser.add_argument("--fixture", default="fixture")
    parser.add_argument("--out", default="registry.json")
    parser.add_argument("--only", default=None, help="validate one task by name")
    args = parser.parse_args()

    tasks_dir = Path(args.tasks_dir)
    fixture = Path(args.fixture).resolve()
    registry = []
    failures = 0

    for task_file in sorted(tasks_dir.glob("*.json")):
        raw = json.loads(task_file.read_text())
        name = raw.get("name", task_file.stem)
        if args.only and name != args.only:
            continue
        task = dict(raw)
        audit = {k: task.pop(k, None) for k in AUDIT_KEYS}
        row = check_task(task, audit, fixture)
        row["audit-keys-present"] = [k for k, v in audit.items() if v]
        if row["status"] != "validated":
            failures += 1
        registry.append(row)
        marks = "".join(
            "Y" if row.get(k) else ("n" if row.get(k) is False else "-")
            for k in ("base-fails-verifier", "gold-passes-verifier", "wrong-patch-fails", "hack-patch-fails")
        )
        print(f"{name:34s} [{marks}] {row['status']}")

    Path(args.out).write_text(json.dumps(registry, indent=2) + "\n")
    print(f"\n{len(registry)} tasks validated, {failures} broken -> {args.out}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
