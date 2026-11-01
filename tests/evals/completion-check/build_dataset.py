"""Build the completion-check dataset from recorded warmcost campaigns.

For each attempt that splice reported as completed, rebuild the workspace
before and after the attempt from the fixture and the write_file calls in the
raw streams (the precursor write attempt runs first in the same workspace),
and label the attempt with the independent harness verifier result.

Usage: python3 build_dataset.py <run_dir> [<run_dir> ...] > dataset.json
"""
import difflib
import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
TASKSET = os.path.join(HERE, "..", "warmcost", "families-taskset")
MAX_DIFF_CHARS = 8000


def fixture_files():
    root = os.path.join(TASKSET, "fixture")
    files = {}
    for dirpath, _, names in os.walk(root):
        for name in names:
            path = os.path.join(dirpath, name)
            with open(path, encoding="utf-8", errors="replace") as fh:
                files[os.path.relpath(path, root)] = fh.read()
    return files


def writes(raw_path, workspace):
    """Return the final content per relative path written in one attempt."""
    out = {}
    if not os.path.exists(raw_path):
        return out
    for line in open(raw_path, encoding="utf-8", errors="replace"):
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get("type") != "tool_call" or event.get("name") != "write_file":
            continue
        args = event.get("args") or {}
        path, content = args.get("path"), args.get("content")
        if not isinstance(path, str) or not isinstance(content, str):
            continue
        for prefix in (workspace + "/", "/private" + workspace + "/"):
            if path.startswith(prefix):
                path = path[len(prefix):]
        out[path[2:] if path.startswith("./") else path] = content
    return out


def unified(before, after):
    chunks = []
    for path in sorted(set(before) | set(after)):
        old, new = before.get(path, ""), after.get(path, "")
        if old == new:
            continue
        chunks.extend(difflib.unified_diff(old.splitlines(True), new.splitlines(True), "a/" + path, "b/" + path, n=2))
    text = "".join(chunks)
    return text[:MAX_DIFF_CHARS], len(text) > MAX_DIFF_CHARS


def main(run_dirs):
    base = fixture_files()
    rows = []
    for run_dir in run_dirs:
        attempts = {}
        for path in glob.glob(os.path.join(run_dir, "attempt-*.json")):
            a = json.load(open(path))
            attempts[(a["task_id"], a["arm"], a["repeat"])] = a
        for (task, arm, repeat), a in sorted(attempts.items()):
            if a.get("run_status") != "completed":
                continue
            before = dict(base)
            if task.endswith("-read"):
                precursor = attempts.get((task[:-5] + "-write", arm, repeat))
                if precursor is None:
                    continue
                before.update(writes(os.path.join(run_dir, precursor["raw_stream"]), precursor["workspace"]))
            after = dict(before)
            after.update(writes(os.path.join(run_dir, a["raw_stream"]), a["workspace"]))
            diff, truncated = unified(before, after)
            task_file = json.load(open(os.path.join(TASKSET, "tasks", task + ".json")))
            rows.append({
                "id": f"{os.path.basename(run_dir)}/{task}/{arm}/r{repeat}",
                "task": task,
                "request": task_file["prompt"],
                "diff": diff,
                "diff_truncated": truncated,
                "verified": a["verifier_result"],
                "verifier_output": a["verifier_output"][-600:],
            })
    json.dump({"version": 1, "source": [os.path.basename(d) for d in run_dirs], "rows": rows}, sys.stdout, indent=1)


if __name__ == "__main__":
    main(sys.argv[1:])
