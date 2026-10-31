#!/usr/bin/env python3
"""Generate a warmcost taskset from the SWE-bench CL curriculum."""

import argparse
import json
import os
import pathlib
import shlex
import sys
from typing import Dict, List, Optional


DATASET = pathlib.Path.home() / ".cache" / "splice-bench" / "agents-never-forget" / "data" / "SWE-Bench-CL-Curriculum.json"


def _load_invalid(path: Optional[str]) -> Dict[str, str]:
    if not path:
        return {}
    with open(path, "r", encoding="utf-8") as stream:
        data = json.load(stream)
    rows = data.get("tasks", data) if isinstance(data, dict) else data
    invalid: Dict[str, str] = {}
    if isinstance(rows, dict):
        for task_id, row in rows.items():
            if isinstance(row, dict) and (not row.get("base_fail") or not row.get("gold_pass")):
                invalid[str(task_id)] = str(row.get("error") or "validation failed")
    elif isinstance(rows, list):
        for row in rows:
            if isinstance(row, dict) and (not row.get("base_fail") or not row.get("gold_pass")):
                task_id = str(row.get("id") or row.get("instance_id") or "")
                if task_id:
                    invalid[task_id] = str(row.get("error") or "validation failed")
    return invalid


def _json_script(base_commit: str) -> str:
    # Keep this a POSIX shell script. The runner invokes /bin/sh and the
    # config file must survive a fresh checkout before every task.
    config = '{"verify":{"testCommand":["python","bin/test","-C","{tests}"]}}'
    return "git checkout -qf %s && git clean -qfdx -e .splice && mkdir -p .splice && printf '%%s\\n' %s > .splice/config.json" % (shlex.quote(base_commit), shlex.quote(config))


def _task_json(task: dict, fixture: str, data_path: pathlib.Path, first: bool) -> dict:
    metadata = task["metadata"]
    evaluation = task["evaluation"]
    task_id = metadata["instance_id"]
    result = {
        "id": task_id,
        "phase": "write" if first else "read",
        "prompt": task["task"]["problem_statement"],
        "setup": _json_script(metadata["base_commit"]),
        "check": "python3 %s --task %s" % (shlex.quote(str((pathlib.Path(__file__).resolve().parent / "grade.py"))), shlex.quote(str(data_path.resolve()))),
        "fixture": fixture,
    }
    if not first:
        result["precursor"] = task_id  # replaced by the caller with task 1
    return result


def generate(repo: str, fixture: str, out: str, limit: Optional[int] = None,
             exclude_invalid: Optional[str] = None) -> List[str]:
    fixture_path = pathlib.Path(fixture).expanduser().resolve()
    if not fixture_path.is_dir():
        raise ValueError("fixture does not exist: %s" % fixture_path)
    if limit is not None and limit < 1:
        raise ValueError("limit must be at least 1")
    with open(DATASET, "r", encoding="utf-8") as stream:
        corpus = json.load(stream)
    sequence = next((item for item in corpus.get("sequences", []) if item.get("repo") == repo), None)
    if sequence is None:
        raise ValueError("dataset has no sequence for %s" % repo)
    source_tasks = list(sequence.get("tasks", []))
    if limit is not None:
        source_tasks = source_tasks[:limit]
    invalid = _load_invalid(exclude_invalid)
    selected = [task for task in source_tasks if task["metadata"]["instance_id"] not in invalid]
    if not selected:
        raise ValueError("no tasks remain after applying the limit and validation filter")

    output = pathlib.Path(out).resolve()
    tasks_dir = output / "tasks"
    data_dir = output / "data"
    tasks_dir.mkdir(parents=True, exist_ok=True)
    data_dir.mkdir(parents=True, exist_ok=True)
    for old in tasks_dir.glob("*.json"):
        old.unlink()
    for old in data_dir.glob("*.json"):
        old.unlink()
    first_id = selected[0]["metadata"]["instance_id"]
    written: List[str] = []
    for index, task in enumerate(selected):
        metadata = task["metadata"]
        evaluation = task["evaluation"]
        task_id = metadata["instance_id"]
        data_path = data_dir / (task_id + ".json")
        data = {
            "id": task_id,
            "instance_id": task_id,
            "repo": metadata["repo"],
            "base_commit": metadata["base_commit"],
            "created_at": metadata.get("created_at"),
            "difficulty": metadata.get("difficulty"),
            "gold_patch": evaluation.get("patch", ""),
            "test_patch": evaluation.get("test_patch", ""),
            "FAIL_TO_PASS": evaluation.get("FAIL_TO_PASS", []),
            "PASS_TO_PASS": evaluation.get("PASS_TO_PASS", []),
        }
        data_path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        row = _task_json(task, str(fixture_path), data_path, index == 0)
        if index > 0:
            row["precursor"] = first_id
        task_name = "%03d-%s.json" % (index + 1, task_id)
        (tasks_dir / task_name).write_text(json.dumps(row, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        written.append(task_id)

    dropped = ["%s: %s" % (task["metadata"]["instance_id"], invalid[task["metadata"]["instance_id"]]) for task in source_tasks if task["metadata"]["instance_id"] in invalid]
    readme = ["# SWE-bench continual-learning taskset", "", "This taskset follows the SymPy sequence order.", "", "Tasks: %d." % len(written)]
    if dropped:
        readme.extend(["", "Dropped by validation:", ""] + ["- " + item for item in dropped])
    (output / "README.md").write_text("\n".join(readme) + "\n", encoding="utf-8")
    return written


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--fixture", required=True)
    parser.add_argument("--limit", type=int)
    parser.add_argument("--out", required=True)
    parser.add_argument("--exclude-invalid")
    args = parser.parse_args(argv)
    try:
        ids = generate(args.repo, args.fixture, args.out, args.limit, args.exclude_invalid)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print("gen_taskset.py: %s" % exc, file=sys.stderr)
        return 2
    print("wrote %d tasks to %s" % (len(ids), pathlib.Path(args.out).resolve()))
    return 0


if __name__ == "__main__":
    sys.exit(main())
