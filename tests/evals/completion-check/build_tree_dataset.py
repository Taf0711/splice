"""Build completion-check cases from the corpus gold and wrong trees.

For each family read task: the state before the task is the precursor
write-gold tree. The correct change overlays the _gold files and the wrong
change overlays the _wrong files. Each diff is labeled with whether it
satisfies the request (gold: true, wrong: false).

Usage: python3 build_tree_dataset.py > tree_dataset.json
"""
import difflib
import json
import os
import re

HERE = os.path.dirname(os.path.abspath(__file__))
TASKSET = os.path.join(HERE, "..", "warmcost", "families-taskset")
VALIDATE = os.path.join(HERE, "..", "cognition-families", "validate")


LEAK = re.compile(r"(?i)\bwrong\b|\bgold\b|\bvariant\b|violat|\bbug\b|breaking|\bskips?\b|instead of|no longer|disagree|diverge")


def strip_comment_lines(text):
    """Remove each block of consecutive full-line Go comments that gives the
    label away. The wrong trees describe themselves ("WRONG variant: ...")
    across several lines, so the whole block goes. Other comments stay: some
    requirements ask for a comment or a //go:build line. Every tree gets the
    same treatment."""
    out, block = [], []

    def flush():
        if not any(LEAK.search(line) for line in block):
            out.extend(block)
        block.clear()

    for line in text.splitlines(True):
        if line.lstrip().startswith("//") and not line.lstrip().startswith("//go:"):
            block.append(line)
            continue
        flush()
        out.append(line)
    flush()
    return "".join(out)


def read_tree(root):
    files = {}
    for dirpath, _, names in os.walk(root):
        for name in names:
            path = os.path.join(dirpath, name)
            text = open(path, encoding="utf-8", errors="replace").read()
            if name.endswith(".go"):
                text = strip_comment_lines(text)
            files[os.path.relpath(path, root)] = text
    return files


def unified(before, after):
    out = []
    for path in sorted(set(before) | set(after)):
        old, new = before.get(path, ""), after.get(path, "")
        if old != new:
            out.extend(difflib.unified_diff(old.splitlines(True), new.splitlines(True), "a/" + path, "b/" + path, n=2))
    return "".join(out)


rows = []
for i in range(1, 11):
    fid = f"fam-{i:02d}"
    task = json.load(open(os.path.join(TASKSET, "tasks", fid + "-read.json")))
    before = read_tree(os.path.join(TASKSET, "write-gold", fid))
    for kind, label in (("_gold", True), ("_wrong", False)):
        overlay = read_tree(os.path.join(VALIDATE, kind, fid))
        after = dict(before)
        after.update(overlay)
        rows.append({"id": f"{fid}{kind}", "task": fid + "-read", "request": task["prompt"], "diff": unified(before, after), "satisfies": label})
json.dump({"version": 1, "source": "families-taskset write-gold + cognition-families/validate _gold/_wrong", "rows": rows}, open(os.path.join(HERE, "tree_dataset.json"), "w"), indent=1)
print(len(rows), "rows;", sum(1 for r in rows if not r["diff"]), "empty diffs; max diff chars", max(len(r["diff"]) for r in rows))
