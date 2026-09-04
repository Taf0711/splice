#!/usr/bin/env python3
"""Validate the cognition-family verifiers against base/gold/wrong arms.

For each family this materializes a pristine fixture copy, applies an
overlay (or none), runs the family's verifier, and records the letter:
P = verifier passed, F = verifier failed. The required matrix is
[base FAIL, gold PASS, wrong FAIL] per family.

Usage: python3 validate_families.py [--family fam-NN] ...
Writes registry-families.json.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
FIXTURE = os.path.join(ROOT, "..", "taskset-v0", "fixture")
GOLD = os.path.join(ROOT, "validate", "_gold")
WRONG = os.path.join(ROOT, "validate", "_wrong")


def materialize(tmp: str) -> None:
    """Copy the pristine fixture into tmp (which TemporaryDirectory made)."""
    shutil.copytree(FIXTURE, tmp, dirs_exist_ok=True)


def apply_overlay(arm: str, overlay_dir: str) -> None:
    """Copy overlay files over the arm (creating dirs as needed)."""
    for root, _dirs, files in os.walk(overlay_dir):
        for name in files:
            src = os.path.join(root, name)
            rel = os.path.relpath(src, overlay_dir)
            dst = os.path.join(arm, rel)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            shutil.copyfile(src, dst)


def run_verifier(arm: str, verifier: str) -> bool:
    proc = subprocess.run(
        ["/bin/bash", verifier],
        cwd=arm,
        capture_output=True,
        text=True,
        timeout=300,
    )
    return proc.returncode == 0


def main() -> int:
    manifest = json.load(open(os.path.join(ROOT, "cognition-families.json")))
    wanted = None
    args = sys.argv[1:]
    if "--family" in args:
        i = args.index("--family")
        wanted = args[i + 1].split(",")

    results = []
    failures = 0
    for fam in manifest["families"]:
        fid = fam["id"]
        short = fid.split("-")[1]
        if wanted and not any(w == fid or w == short for w in wanted):
            continue
        check_file = os.path.join(ROOT, fam["target_check_file"])
        row = {"id": fid, "letters": ""}
        for arm_name, overlay in (
            ("base", None),
            ("gold", os.path.join(GOLD, f"fam-{short}")),
            ("wrong", os.path.join(WRONG, f"fam-{short}")),
        ):
            with tempfile.TemporaryDirectory() as tmp:
                materialize(tmp)
                if overlay and os.path.isdir(overlay):
                    apply_overlay(tmp, overlay)
                passed = run_verifier(tmp, check_file)
            row[arm_name] = "P" if passed else "F"
            row["letters"] += row[arm_name]
        expect = {"base": "F", "gold": "P", "wrong": "F"}
        ok = all(row[k] == v for k, v in expect.items())
        row["ok"] = ok
        if not ok:
            failures += 1
        print(f"{fid}: [{row['letters']}] {'OK' if ok else 'MISMATCH'}")
        results.append(row)

    registry = {
        "schema": "splice.eval.cognition-families-registry.v1",
        "validated": datetime.now(timezone.utc).date().isoformat(),
        "families": results,
    }
    out = os.path.join(ROOT, "registry-families.json")
    with open(out, "w") as fh:
        json.dump(registry, fh, indent=2)
        fh.write("\n")
    print(f"wrote {out}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())