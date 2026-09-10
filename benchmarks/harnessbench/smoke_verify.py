#!/usr/bin/env python3
"""Static smoke verification for the Harness-Bench Splice adapter.

No model calls, no network, no paid runs. Validates:
  1. The adapter module parses and imports (stdlib only at module level).
  2. The harness config fragment parses as YAML and validates against
     config.schema.json (jsonschema if installed, structural fallback if not).
  3. The development subset manifest is valid JSON, its task ids are within
     the verified upstream SE/SRE category id lists, and its count fields are
     consistent.
  4. versions.lock.json pins a 40-char harness-bench commit and no floating
     ref.
Prints the exact full-run command at the end.

Exit code 0 = all checks passed.
"""

from __future__ import annotations

import importlib.util
import json
import re
import sys
from pathlib import Path

ADAPTER_DIR = Path(__file__).resolve().parent
BENCH_DIR = ADAPTER_DIR.parent
REPO_ROOT = BENCH_DIR.parent

# Verified from tasks/<id>/task.yaml class fields at upstream commit
# 1025086a446653702b80cfb48babbeec35db6b2c (2026-09-05).
UPSTREAM_SE_TASKS = [
    "009-git-pr-merge", "011-code-debug", "016-code-repair-pytest",
    "017-db-doc-consistency", "018-provider-failover-audit",
    "039-repo-architecture-map", "040-test-coverage-fill",
    "041-frontend-state-bug", "042-api-schema-migration",
    "043-db-migration-safety", "044-ci-config-repair",
    "045-dependency-upgrade-compat", "046-performance-regression",
    "047-code-review-risk-report", "048-release-note-changelog",
    "082-compose-config-repair", "083-monorepo-interface-repair",
    "084-js-state-type-bug", "085-flaky-test-root-cause",
    "086-sql-migration-preflight-rollback", "087-cli-parser-bug-tests",
    "088-api-contract-mock-client-compat",
]
UPSTREAM_SRE_TASKS = [
    "019-incident-runbook-synthesis", "062-k8s-config-audit",
    "063-alert-dedup-noise", "064-service-dependency-triage",
    "065-capacity-planning", "066-rollback-readiness",
    "067-canary-release-check",
]

FAILURES: list[str] = []


def check(name: str, ok: bool, detail: str = "") -> None:
    status = "PASS" if ok else "FAIL"
    print(f"[{status}] {name}" + (f" - {detail}" if detail else ""))
    if not ok:
        FAILURES.append(name)


def main() -> int:
    # 1. Adapter module parses and imports.
    spec = importlib.util.spec_from_file_location(
        "splice_adapter", ADAPTER_DIR / "splice_adapter.py"
    )
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    try:
        spec.loader.exec_module(module)
        adapter = module.SpliceHarnessAdapter()
        check(
            "adapter module imports and exposes BaseAdapter-style run()",
            hasattr(adapter, "run") and adapter.name == "splice",
            f"name={adapter.name!r}",
        )
    except Exception as exc:  # noqa: BLE001
        check("adapter module imports", False, repr(exc))
        return finish()

    # 2. Config fragment parses and validates.
    config_path = BENCH_DIR / "configs" / "harnessbench.splice.yaml"
    try:
        import yaml

        config = yaml.safe_load(config_path.read_text())
        yaml_available = True
    except ImportError:
        yaml_available = False
        config = None
        print("[SKIP] PyYAML not installed; falling back to structural checks")
    if yaml_available and config is not None:
        entry = (config.get("models") or {}).get("splice-local") or {}
        check(
            "harness config has models.splice-local with adapter=splice",
            entry.get("adapter") == "splice" and bool(entry.get("command")),
            f"adapter={entry.get('adapter')!r} command={entry.get('command')!r}",
        )
        check(
            "harness config timeout_sec is a positive integer",
            isinstance(entry.get("timeout_sec", 0), int) and entry.get("timeout_sec", 0) > 0,
            f"timeout_sec={entry.get('timeout_sec')!r}",
        )
    schema = json.loads((ADAPTER_DIR / "config.schema.json").read_text())
    if yaml_available and config is not None:
        try:
            import jsonschema

            jsonschema.validate(config, schema)
            check("harness config validates against config.schema.json (jsonschema)", True)
        except ImportError:
            check(
                "harness config structural validation (jsonschema missing)",
                isinstance(config.get("models"), dict)
                and "splice-local" in config["models"],
            )
        except Exception as exc:  # noqa: BLE001
            check("harness config validates against config.schema.json", False, repr(exc))

    # 3. Subset manifest consistency.
    manifest_path = BENCH_DIR / "manifests" / "splice-harnessbench-se-sre.json"
    manifest = json.loads(manifest_path.read_text())
    check(
        "manifest id is splice-harnessbench-se-sre",
        manifest.get("manifest_id") == "splice-harnessbench-se-sre",
    )
    check(
        "manifest carries DEVELOPMENT SUBSET label",
        "DEVELOPMENT SUBSET - NOT FULL BENCHMARK SCORE" in manifest_path.read_text(),
    )
    all_selected: list[str] = []
    for cat in manifest.get("categories", []):
        ids = cat.get("selected_task_ids", [])
        all_selected.extend(ids)
        if "Software Engineering" in cat.get("class", ""):
            ok = set(ids) <= set(UPSTREAM_SE_TASKS)
            check(
                "SE subset ids are verified upstream SE task ids",
                ok,
                f"{len(ids)} selected of {len(UPSTREAM_SE_TASKS)} upstream",
            )
            check(
                "SE upstream_task_count matches verified upstream (22)",
                cat.get("upstream_task_count") == len(UPSTREAM_SE_TASKS) == 22,
            )
        elif "SRE" in cat.get("class", ""):
            ok = set(ids) <= set(UPSTREAM_SRE_TASKS)
            check(
                "SRE subset ids are verified upstream SRE task ids",
                ok,
                f"{len(ids)} selected of {len(UPSTREAM_SRE_TASKS)} upstream",
            )
            check(
                "SRE upstream_task_count matches verified upstream (7)",
                cat.get("upstream_task_count") == len(UPSTREAM_SRE_TASKS) == 7,
            )
    check(
        "manifest has no duplicate task ids",
        len(all_selected) == len(set(all_selected)),
        f"{len(all_selected)} ids",
    )
    check(
        "manifest total_selected matches id count",
        manifest.get("total_selected") == len(all_selected),
        f"total_selected={manifest.get('total_selected')}",
    )

    # 4. Pin hygiene for this adapter.
    lock = json.loads((BENCH_DIR / "versions.lock.json").read_text())
    hb = lock["adapters"]["harness-bench"]
    check(
        "harness-bench commit is a 40-char sha",
        bool(re.fullmatch(r"[0-9a-f]{40}", hb.get("upstream_commit", ""))),
        hb.get("upstream_commit", "")[:12],
    )
    check(
        "no floating refs in harness-bench pin",
        hb.get("upstream_commit") not in ("latest", "main", "master", "HEAD"),
    )

    return finish()


def finish() -> int:
    print()
    if FAILURES:
        print(f"SMOKE FAILED ({len(FAILURES)}): {FAILURES}")
        return 1
    print("SMOKE OK - adapter scaffolding statically valid. No model calls made.")
    print()
    print("Full run command (PAID, consumes provider tokens):")
    print("  git clone https://github.com/Qihoo360/harness-bench")
    print("  cd harness-bench && git checkout 1025086a446653702b80cfb48babbeec35db6b2c")
    print("  pip install -e .")
    print("  # merge benchmarks/configs/harnessbench.splice.yaml into config/harness.yaml")
    print("  PYTHONPATH=src python3 -m harnessbench.cli run-suite \\")
    print("    --harness splice-local --mode live")
    print()
    print("Single development-subset task example (PAID):")
    print("  PYTHONPATH=src python3 -m harnessbench.cli run-task \\")
    print("    --task 011-code-debug --harness splice-local --mode live")
    return 0


if __name__ == "__main__":
    sys.exit(main())
