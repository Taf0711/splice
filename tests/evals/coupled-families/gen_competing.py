#!/usr/bin/env python3
"""Competing records: memory holds several work records, and a read needs one
(plans/SPEC_JEV_DECISION_LAYER_V2, phase P3).

Three coupled families share one workspace. The three writes run first, then
the three reads. At each read, memory holds a work record from each write,
and each record names a different coupled file. Selection is the decision:
prefetch the record the read needs, not all of them.

The fixture and the families are the coupled corpus (gen_coupled.py). A
check runs only the task's own probe and completeness test, so a failed
earlier task in the sequence does not fail a later check (a compile error
still does).

Usage:
  gen_competing.py            write the taskset
  gen_competing.py --validate write, then replay the gold edits in order
"""

import json
import pathlib
import re
import shutil
import sys
import tempfile

import gen_coupled as cpl

TASKS = cpl.EVALS / "warmcost" / "competing-taskset" / "tasks"
FIXTURE_REF = "../../coupled-families/fixture"

# Each trio is one workspace sequence. Its three families have different
# coupled files, except cpl-01 and cpl-02 (both config/catalog.go).
TRIOS = {
    "admin": ["cpl-04", "cpl-05", "cpl-06"],
    "server": ["cpl-01", "cpl-02", "cpl-03"],
}

COMPLETENESS = {
    "cpl-01": "TestEveryNotificationKindIsInTheCatalog",
    "cpl-02": "TestEveryWebhookEventIsInTheCatalog",
    "cpl-03": "TestEveryScheduledJobHasAnOwner",
    "cpl-04": "TestEveryRoleHasPermissions",
    "cpl-05": "TestPricingPageShowsEveryPlan",
    "cpl-06": "TestEveryAuditActionHasALabel",
}


def scoped_check(family, probe, comment):
    pkg, name, body = probe
    test = re.search(r"func (Test\w+)\(", body).group(1)
    return cpl.check(probe, comment).replace(
        "go test -count=1 ./...",
        f"go test -count=1 -run '^({test}|{COMPLETENESS[family]})$' ./...",
    )


def sequence(trio, fixture_ref=FIXTURE_REF):
    """The trio's tasks in run order: (file stem, task)."""
    fams = {f["id"]: f for f in cpl.FAMILIES}
    first = f"cmp-{trio}-1-{TRIOS[trio][0]}-write"
    out = []
    for i, fid in enumerate(TRIOS[trio]):
        f = fams[fid]
        stem = f"cmp-{trio}-{i + 1}-{fid}-write"
        task = dict(name=stem, prompt=f["a_prompt"], check=scoped_check(fid, f["a_probe"], f"{fid} A"), fixture=fixture_ref)
        if i == 0:
            task["phase"] = "write"
        else:
            task["precursor"] = first
        out.append((stem, task, f, "a"))
    for i, fid in enumerate(TRIOS[trio]):
        f = fams[fid]
        stem = f"cmp-{trio}-{i + 4}-{fid}-read"
        task = dict(name=stem, phase="read", precursor=first, prompt=f["b_prompt"],
                    check=scoped_check(fid, f["b_probe"], f"{fid} B"), fixture=fixture_ref)
        out.append((stem, task, f, "b"))
    return out


def write_tasks(tasks=TASKS, fixture_ref=FIXTURE_REF):
    tasks.mkdir(parents=True, exist_ok=True)
    manifest = []
    for trio in TRIOS:
        for stem, task, f, half in sequence(trio, fixture_ref):
            (tasks / f"{stem}.json").write_text(json.dumps(task, indent=2) + "\n")
            if half == "b":
                others = sorted({g["coupled"] for g in cpl.FAMILIES if g["id"] in TRIOS[trio]} - {f["coupled"]})
                manifest.append(dict(id=stem, family=f["id"], coupled=f["coupled"], distractors=others))
    (tasks.parent / "competing.json").write_text(json.dumps(manifest, indent=2) + "\n")


def validate(fixture=cpl.FIXTURE):
    """Replay each trio in order on one workspace. Before its gold edits a
    task's check fails; with its gold minus the coupled file it fails; with
    its full gold it passes. The earlier tasks' gold stays applied."""
    failures = []
    for trio in TRIOS:
        repo = pathlib.Path(tempfile.mkdtemp(prefix="competing-validate-"))
        shutil.copytree(fixture, repo, dirs_exist_ok=True)
        for stem, task, f, half in sequence(trio):
            gold = f[f"gold_{half}"]
            feature_only = [e for e in gold if e[0] != f["coupled"]]
            rc, out = cpl.run(task["check"], repo)
            if rc == 0:
                failures.append(f"{stem}: passes before its change")
            snapshot = pathlib.Path(tempfile.mkdtemp(prefix="competing-snap-"))
            shutil.copytree(repo, snapshot, dirs_exist_ok=True)
            cpl.apply(snapshot, feature_only)
            rc, out = cpl.run(task["check"], snapshot)
            if rc == 0:
                failures.append(f"{stem}: passes without the coupled file")
            shutil.rmtree(snapshot)
            cpl.apply(repo, gold)
            rc, out = cpl.run(task["check"], repo)
            if rc != 0:
                failures.append(f"{stem}: fails on its gold: {out}")
        rc, out = cpl.run("go vet ./... && go test -count=1 ./...", repo)
        if rc != 0:
            failures.append(f"{trio}: the full sequence gold does not pass: {out}")
        shutil.rmtree(repo)
        print(f"{trio}: checked {len(sequence(trio))} tasks")
    if failures:
        print("FAILURES:")
        for x in failures:
            print(" ", x)
        sys.exit(1)
    print("all sequences valid")


if __name__ == "__main__":
    write_tasks()
    if "--validate" in sys.argv:
        validate()
