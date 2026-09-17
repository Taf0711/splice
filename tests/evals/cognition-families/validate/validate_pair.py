#!/usr/bin/env python3
"""Offline validation matrix for a campaign PAIR (precursor A -> target B).

This is the pair-format extension of the existing validate infrastructure
(validate_mvp.py). For one manifest family it proves, against real fixture
copies:

  base   FAIL  : the Task A verifier fails on the pristine fixture
  goldA  PASS  : the Task A verifier passes on base + gold Task A
  baseB  FAIL  : the Task B verifier fails on base + gold Task A (B absent)
  goldB  PASS  : the Task B verifier passes on base + gold A + gold B
  wrongB FAIL  : the Task B verifier fails on base + gold A + wrong B

Overlays are directory trees under validate/_gold-a/<short>,
validate/_gold-b/<short>, and validate/_wrong-b/<short>; every file in the
tree is copied over the arm (cognition-family overlay style).

Run from the repo root:
  python3 tests/evals/cognition-families/validate/validate_pair.py \
      [--manifest tests/evals/cognition-families/fam-05-pair.json] \
      [--family fam-05-handler-error-mapping]

Exit 0 only when every family matches [base FAIL, goldA PASS, baseB FAIL,
goldB PASS, wrongB FAIL]. Writes registry-pairs.json next to the manifest.
"""

import argparse
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile
import time

HERE = pathlib.Path(__file__).resolve().parent
DEFAULT_MANIFEST = HERE.parent / "fam-05-pair.json"


def sh(cmd: str, cwd: pathlib.Path) -> tuple[int, str]:
    proc = subprocess.run(
        ["/bin/bash", "-c", cmd], cwd=cwd, capture_output=True, text=True
    )
    return proc.returncode, (proc.stdout + proc.stderr)[-1200:]


def fresh_fixture(fixture: pathlib.Path) -> pathlib.Path:
    repo = pathlib.Path(tempfile.mkdtemp(prefix="cog-pair-"))
    shutil.copytree(fixture, repo, dirs_exist_ok=True)
    sh("git init -q && git add -A", repo)
    return repo


def apply_overlay_dir(overlay_dir: pathlib.Path, repo: pathlib.Path) -> None:
    if not overlay_dir.is_dir():
        return
    for root, _dirs, files in os_walk(overlay_dir):
        for name in files:
            src = pathlib.Path(root) / name
            rel = src.relative_to(overlay_dir)
            dst = repo / rel
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(src, dst)


def os_walk(path: pathlib.Path):
    import os

    return os.walk(path)


def run_pair(manifest_path: pathlib.Path, wanted: list[str] | None) -> int:
    families_dir = manifest_path.parent
    manifest = json.loads(manifest_path.read_text())
    fixture = (families_dir / manifest["fixture"]).resolve()
    if not fixture.is_dir():
        print(f"fixture not found: {fixture}", file=sys.stderr)
        return 2

    registry = {
        "schema": "splice.eval.cognition-families-pairs-registry.v1",
        "validated": time.strftime("%Y-%m-%d"),
        "manifest": str(manifest_path),
        "families": [],
    }
    failures: list[str] = []
    for family in manifest["families"]:
        fid = family["id"]
        short = fid.split("-")[1]
        if wanted and not any(w == fid or w == short or w == f"fam-{short}" for w in wanted):
            continue
        row = {"id": fid, "letters": ""}
        golda = HERE / "_gold-a" / f"fam-{short}"
        goldb = HERE / "_gold-b" / f"fam-{short}"
        wrongb = HERE / "_wrong-b" / f"fam-{short}"

        # base: Task A verifier must FAIL on pristine fixture.
        repo = fresh_fixture(fixture)
        rc, _ = sh(f"bash {families_dir / family['precursor_check_file']}", repo)
        row["base"] = "P" if rc == 0 else "F"
        shutil.rmtree(repo, ignore_errors=True)
        if rc == 0:
            failures.append(f"{fid}: A verifier unexpectedly PASSED on base")

        # goldA: Task A verifier must PASS; Task B verifier must FAIL.
        repo = fresh_fixture(fixture)
        apply_overlay_dir(golda, repo)
        rc, out = sh(f"bash {families_dir / family['precursor_check_file']}", repo)
        row["goldA"] = "P" if rc == 0 else "F"
        if rc != 0:
            failures.append(f"{fid}: A verifier FAILED on gold A: {out}")
        rc, _ = sh(f"bash {families_dir / family['target_check_file']}", repo)
        row["baseB"] = "P" if rc == 0 else "F"
        if rc == 0:
            failures.append(f"{fid}: B verifier unexpectedly PASSED without B")
        shutil.rmtree(repo, ignore_errors=True)

        # goldB: Task B verifier must PASS on gold A + gold B.
        repo = fresh_fixture(fixture)
        apply_overlay_dir(golda, repo)
        apply_overlay_dir(goldb, repo)
        rc, out = sh(f"bash {families_dir / family['target_check_file']}", repo)
        row["goldB"] = "P" if rc == 0 else "F"
        if rc != 0:
            failures.append(f"{fid}: B verifier FAILED on gold B: {out}")
        shutil.rmtree(repo, ignore_errors=True)

        # wrongB: Task B verifier must FAIL on gold A + wrong B.
        repo = fresh_fixture(fixture)
        apply_overlay_dir(golda, repo)
        apply_overlay_dir(wrongb, repo)
        rc, _ = sh(f"bash {families_dir / family['target_check_file']}", repo)
        row["wrongB"] = "P" if rc == 0 else "F"
        if rc == 0:
            failures.append(f"{fid}: B verifier unexpectedly PASSED on wrong B")
        shutil.rmtree(repo, ignore_errors=True)

        row["letters"] = row["base"] + row["goldA"] + row["baseB"] + row["goldB"] + row["wrongB"]
        row["ok"] = row["letters"] == "FPFPF"
        if not row["ok"] and f"{fid}:" not in " ".join(failures):
            failures.append(f"{fid}: matrix {row['letters']} != FPFPF")
        registry["families"].append(row)
        state = "OK" if row["ok"] else "MISMATCH"
        print(f"{fid}: [{row['letters']}] {state} (base/goldA/baseB/goldB/wrongB)")

    out_path = manifest_path.parent / "registry-pairs.json"
    out_path.write_text(json.dumps(registry, indent=2) + "\n")
    print(f"wrote {out_path}")

    if failures:
        print("\nFAILURES:")
        for f in failures:
            print("  " + f)
        return 1
    print("\nall pairs validate [base FAIL, goldA PASS, baseB FAIL, goldB PASS, wrongB FAIL]")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=pathlib.Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--family", action="append", default=None)
    args = parser.parse_args()
    return run_pair(args.manifest, args.family)


if __name__ == "__main__":
    sys.exit(main())
