#!/usr/bin/env python3
"""Validation matrix for the MVP cognition families.

For every family it proves, against real fixture copies:
  base   FAIL  : Task A verifier fails on the pristine fixture
  goldA  PASS  : Task A verifier passes on base + gold Task A
  baseB  FAIL  : Task B verifier fails on base + gold Task A
  goldB  PASS  : Task B verifier passes on base + gold A + gold B
  wrongB FAIL  : Task B verifier fails on base + gold A + wrong B

Run from the repo root:  python3 tests/evals/mvp-families/validate/validate_mvp.py
"""

import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

HERE = pathlib.Path(__file__).resolve().parent
FAMILIES_DIR = HERE.parent
MANIFEST = FAMILIES_DIR / "cognition-mvp-families.json"
REPO_ROOT = pathlib.Path.cwd()


def sh(cmd: str, cwd: pathlib.Path) -> tuple[int, str]:
    proc = subprocess.run(
        ["/bin/bash", "-c", cmd], cwd=cwd, capture_output=True, text=True
    )
    return proc.returncode, (proc.stdout + proc.stderr)[-800:]


def apply_overlay(script: pathlib.Path, repo: pathlib.Path) -> None:
    rc, out = sh(f"bash {script} {repo}", repo)
    if rc != 0:
        raise RuntimeError(f"overlay {script.name} failed: {out}")


def fresh_fixture() -> pathlib.Path:
    repo = pathlib.Path(tempfile.mkdtemp(prefix="mvp-validate-"))
    shutil.copytree(FAMILIES_DIR / "fixture", repo, dirs_exist_ok=True)
    sh("git init -q && git add -A", repo)
    return repo


def main() -> int:
    manifest = json.loads(MANIFEST.read_text())
    failures = []
    results = {}
    for family in manifest["families"]:
        fid = family["id"]
        short = fid.split("-")[1]  # "mvp-01-session-invalidation" -> "01"

        # base: Task A verifier must FAIL on pristine fixture.
        repo = fresh_fixture()
        rc, _ = sh(f"bash {FAMILIES_DIR / family['precursor_check_file']}", repo)
        results[f"{fid}:A-vs-base"] = rc
        if rc == 0:
            failures.append(f"{fid}: A verifier unexpectedly PASSED on base")
        shutil.rmtree(repo)

        # goldA: A verifier must PASS with the gold A overlay.
        repo = fresh_fixture()
        apply_overlay(HERE / "_gold-a" / f"mvp-{short}.sh", repo)
        rc, out = sh(f"bash {FAMILIES_DIR / family['precursor_check_file']}", repo)
        results[f"{fid}:A-vs-goldA"] = rc
        if rc != 0:
            failures.append(f"{fid}: A verifier FAILED on gold A: {out}")
        # baseB: B verifier must FAIL on base + gold A (B not implemented).
        rc, _ = sh(f"bash {FAMILIES_DIR / family['target_check_file']}", repo)
        results[f"{fid}:B-vs-goldA"] = rc
        if rc == 0:
            failures.append(f"{fid}: B verifier unexpectedly PASSED without B")
        shutil.rmtree(repo)

        # goldB: B verifier must PASS on base + gold A + gold B.
        repo = fresh_fixture()
        apply_overlay(HERE / "_gold-a" / f"mvp-{short}.sh", repo)
        apply_overlay(HERE / "_gold-b" / f"mvp-{short}.sh", repo)
        rc, out = sh(f"bash {FAMILIES_DIR / family['target_check_file']}", repo)
        results[f"{fid}:B-vs-goldA+goldB"] = rc
        if rc != 0:
            failures.append(f"{fid}: B verifier FAILED on gold B: {out}")
        shutil.rmtree(repo)

        # wrongB: B verifier must FAIL on base + gold A + wrong B.
        repo = fresh_fixture()
        apply_overlay(HERE / "_gold-a" / f"mvp-{short}.sh", repo)
        apply_overlay(HERE / "_wrong" / f"mvp-{short}.sh", repo)
        rc, _ = sh(f"bash {FAMILIES_DIR / family['target_check_file']}", repo)
        results[f"{fid}:B-vs-wrongB"] = rc
        if rc == 0:
            failures.append(f"{fid}: B verifier unexpectedly PASSED on wrong B")
        shutil.rmtree(repo)

    print("validation matrix (exit codes; 0=PASS non-zero=FAIL):")
    for key in sorted(results):
        print(f"  {key}: {results[key]}")
    if failures:
        print("\nFAILURES:")
        for f in failures:
            print("  " + f)
        return 1
    print("\nall families validate [base FAIL, goldA PASS, baseB FAIL, goldB PASS, wrongB FAIL]")
    return 0


if __name__ == "__main__":
    sys.exit(main())