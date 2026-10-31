#!/usr/bin/env python3
"""Validation matrix for the LARGE cognition families.

Proves per family:
  base   FAIL : A verifier fails on the pristine fixture
  goldA  PASS : A verifier passes on base + gold A
  baseB  FAIL : B verifier fails on base + gold A
  goldB  PASS : B verifier passes on base + gold A + gold B
  wrongB FAIL : B verifier fails on base + gold A + wrong B
"""

import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

HERE = pathlib.Path(__file__).resolve().parent
FAMILIES_DIR = pathlib.Path("/tmp/large-fixture-eval")
MANIFEST = FAMILIES_DIR / "cognition-mvp-families.json"


def sh(cmd, cwd):
    proc = subprocess.run(["/bin/bash", "-c", cmd], cwd=cwd, capture_output=True, text=True)
    return proc.returncode, (proc.stdout + proc.stderr)[-600:]


def apply_overlay(script, repo):
    rc, out = sh(f"bash {script} {repo}", repo)
    if rc != 0:
        raise RuntimeError(f"overlay {script.name} failed: {out}")


def fresh_fixture():
    repo = pathlib.Path(tempfile.mkdtemp(prefix="large-validate-"))
    shutil.copytree(FAMILIES_DIR / "fixture", repo, dirs_exist_ok=True)
    return repo


def main():
    manifest = json.loads(MANIFEST.read_text())
    failures = []
    results = {}
    for family in manifest["families"]:
        fid = family["id"]
        short = fid.split("-")[1]

        repo = fresh_fixture()
        rc, _ = sh(f"bash {FAMILIES_DIR / family['precursor_check_file']}", repo)
        results[f"{fid}:A-base"] = rc
        if rc == 0:
            failures.append(f"{fid}: A passed on base")
        shutil.rmtree(repo)

        repo = fresh_fixture()
        apply_overlay(FAMILIES_DIR / "validate" / "_gold-a" / f"large-{short}.sh", repo)
        rc, out = sh(f"bash {FAMILIES_DIR / family['precursor_check_file']}", repo)
        results[f"{fid}:A-goldA"] = rc
        if rc != 0:
            failures.append(f"{fid}: A failed on gold A: {out}")
        rc, _ = sh(f"bash {FAMILIES_DIR / family['target_check_file']}", repo)
        results[f"{fid}:B-goldA"] = rc
        if rc == 0:
            failures.append(f"{fid}: B passed without B")
        shutil.rmtree(repo)

        repo = fresh_fixture()
        apply_overlay(FAMILIES_DIR / "validate" / "_gold-a" / f"large-{short}.sh", repo)
        apply_overlay(FAMILIES_DIR / "validate" / "_gold-b" / f"large-{short}.sh", repo)
        rc, out = sh(f"bash {FAMILIES_DIR / family['target_check_file']}", repo)
        results[f"{fid}:B-goldB"] = rc
        if rc != 0:
            failures.append(f"{fid}: B failed on gold B: {out}")
        shutil.rmtree(repo)

        repo = fresh_fixture()
        apply_overlay(FAMILIES_DIR / "validate" / "_gold-a" / f"large-{short}.sh", repo)
        apply_overlay(FAMILIES_DIR / "validate" / "_wrong" / f"large-{short}.sh", repo)
        rc, _ = sh(f"bash {FAMILIES_DIR / family['target_check_file']}", repo)
        results[f"{fid}:B-wrongB"] = rc
        if rc == 0:
            failures.append(f"{fid}: B passed on wrong B")
        shutil.rmtree(repo)

    for k in sorted(results):
        print(f"  {k}: {results[k]}")
    if failures:
        print("\nFAILURES:")
        for f in failures:
            print("  " + f)
        return 1
    print("\nall large families validate [base FAIL, goldA PASS, baseB FAIL, goldB PASS, wrongB FAIL]")
    return 0


if __name__ == "__main__":
    sys.exit(main())
