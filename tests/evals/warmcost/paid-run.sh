#!/usr/bin/env bash
# Paid three-arm warm-cost run, pre-registered and abort-retried.
#
# This script is the operator entry point for Fix 3. It pins the binary, the
# taskset, the arm set, the repeat count, and the correctness margin BEFORE the
# first provider request. It retries the whole run when an attempt aborts or
# loses its ledger, so infra noise does not become a measured outcome.
#
# The warm arms run against a fresh sidecar. With no prior observations, this
# run measures the enabled mechanisms at cold memory, not a memory-effect
# claim. The report states that limit.

set -euo pipefail

REPO="$(cd "$(dirname "$0")/../../.." && pwd)"
cd "$REPO"

BIN="${BIN:-/tmp/splice-archeval-paid}"
RUNNER="${RUNNER:-/tmp/warmcost-eval-paid}"
MEMD_BIN="${MEMD_BIN:-/tmp/splice-memd}"
MODEL="${MODEL:-z-ai/glm-5.3-flash}"
REPEATS="${REPEATS:-3}"
MARGIN="${MARGIN:-0.05}"
BOOTSTRAP_SAMPLES="${BOOTSTRAP_SAMPLES:-10000}"
ARMS="${ARMS:-cold,warm,warm-retrieval-only}"
TASKSET_SRC="${TASKSET_SRC:-tests/evals/taskset-v0}"
# TASKS_ENV is a bounded override for a pilot corpus. Every name must exist
# under "$TASKSET_SRC/tasks/<name>.json"; an unknown name is a loud error.
# Leaving it unset preserves the pre-registered default task list exactly.
if [[ -n "${TASKS_ENV:-}" ]]; then
  read -r -a TASKS <<< "$TASKS_ENV"
  if [[ ${#TASKS[@]} -eq 0 ]]; then
    printf 'TASKS_ENV is set but names no tasks\n' >&2
    exit 1
  fi
else
  TASKS=(healthz-detail-gating len-skips-expired sessions-active-since)
fi
IFS=',' read -r -a ARM_LIST <<< "${ARMS// /}"
ARM_COUNT="${#ARM_LIST[@]}"
RUN_BASE="${RUN_BASE:-wc-paid-k3-$(date +%Y%m%dT%H%M%S)}"
OUT_DIR="${OUT_DIR:-tests/evals/results}"
MAX_RETRIES="${MAX_RETRIES:-2}"
MEMD_DIR="${MEMD_DIR:-/tmp/warmcost-paid-memd}"
RETENTION="${RETENTION:-fresh}"
SIDECAR_ROOT="${SIDECAR_ROOT:-}"

export PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin
export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-/tmp/splice-cfg}"
export SPLICE_MEMD_BIN="$MEMD_BIN"
export SPLICE_MEMD_SOCKET="$MEMD_DIR/mem.sock"
export SPLICE_MEMD_DB="$MEMD_DIR/mem.db"

log() { printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*"; }

for name in "${TASKS[@]}"; do
  if [[ ! -f "$TASKSET_SRC/tasks/$name.json" ]]; then
    log "unknown task $name: $TASKSET_SRC/tasks/$name.json does not exist"
    exit 1
  fi
done

log "building binary and runner from $(git rev-parse --short HEAD)"
go build -o "$BIN" ./cmd/splice
go build -o "$RUNNER" ./tests/evals/warmcost/cmd/warmcost-eval

TASKSET="$(mktemp -d "${TMPDIR:-/tmp}/warmcost-paid-taskset.XXXXXX")"
MEMD_PID=""
cleanup() {
  if [[ -n "$MEMD_PID" ]]; then
    kill "$MEMD_PID" 2>/dev/null || true
    wait "$MEMD_PID" 2>/dev/null || true
  fi
  rm -rf "$TASKSET"
}
trap cleanup EXIT

mkdir -p "$TASKSET/tasks"
cp -R "$TASKSET_SRC/fixture" "$TASKSET/fixture"
for name in "${TASKS[@]}"; do
  cp "$TASKSET_SRC/tasks/$name.json" "$TASKSET/tasks/$name.json"
done

rm -rf "$MEMD_DIR"
mkdir -p "$MEMD_DIR"
"$MEMD_BIN" --serve >"$MEMD_DIR/memd.log" 2>&1 &
MEMD_PID=$!
for _ in $(seq 1 100); do
  [[ -S "$SPLICE_MEMD_SOCKET" ]] && break
  sleep 0.1
done
if [[ ! -S "$SPLICE_MEMD_SOCKET" ]]; then
  log "sidecar socket did not appear at $SPLICE_MEMD_SOCKET"
  exit 1
fi

REV="$(git rev-parse HEAD)"
SIDECAR_REV="$(git rev-parse --short HEAD)"
log "pre-registration: model=$MODEL arms=$ARMS tasks=${#TASKS[@]} repeats=$REPEATS margin=$MARGIN bootstrap=$BOOTSTRAP_SAMPLES"
log "attempt cap: arms=$ARM_COUNT x tasks=${#TASKS[@]} x repeats=$REPEATS = $((ARM_COUNT * ${#TASKS[@]} * REPEATS)) attempts; MAX_RETRIES=$MAX_RETRIES"
log "revision=$REV sidecar_revision=$SIDECAR_REV taskset=${TASKS[*]}"
log "arm order: $ARMS; interleaved per repeat by the runner"
log "retention: $RETENTION sidecar_root=${SIDECAR_ROOT:-<ambient>}"

if [[ "$RETENTION" == "shared" && -z "$SIDECAR_ROOT" ]]; then
  log "retention shared requires SIDECAR_ROOT, because the warm arms must share one persistent sidecar"
  exit 1
fi

FINAL_RUN=""
for try in $(seq 0 "$MAX_RETRIES"); do
  RUN_ID="$RUN_BASE-try$try"
  RUN_DIR="$OUT_DIR/$RUN_ID"
  log "run $RUN_ID start"
  set +e
  RUNNER_ARGS=(
    --repo .
    --binary "$BIN"
    --model "$MODEL"
    --tasks "$TASKSET"
    --out "$OUT_DIR"
    --repeats "$REPEATS"
    --arms "$ARMS"
    --correctness-margin "$MARGIN"
    --bootstrap-samples "$BOOTSTRAP_SAMPLES"
    --bootstrap-seed 1
    --run-id "$RUN_ID"
    --sidecar-revision "$SIDECAR_REV"
    --retention "$RETENTION"
  )
  if [[ -n "$SIDECAR_ROOT" ]]; then
    RUNNER_ARGS+=(--sidecar-root "$SIDECAR_ROOT")
  fi
  "$RUNNER" "${RUNNER_ARGS[@]}" 2>&1 | tee "$RUN_DIR.log"
  runner_exit="${PIPESTATUS[0]}"
  set -e
  if [[ "$runner_exit" -ne 0 ]]; then
    log "run $RUN_ID exited $runner_exit; retrying"
    continue
  fi
  if python3 - "$RUN_DIR" <<'PY'
import json
import os
import sys

run_dir = sys.argv[1]
retryable = []
for name in sorted(os.listdir(run_dir)):
    if not name.startswith("attempt-") or not name.endswith(".json"):
        continue
    with open(os.path.join(run_dir, name), encoding="utf-8") as fh:
        attempt = json.load(fh)
    status = attempt.get("run_status") or ""
    if status in {"aborted", "infrastructure_failed"} or attempt.get("ledger_error"):
        retryable.append(name)
if retryable:
    print("RETRYABLE " + ",".join(retryable))
    sys.exit(2)
print("CLEAN")
PY
  then
    log "run $RUN_ID clean; stopping retry loop"
    FINAL_RUN="$RUN_ID"
    break
  fi
  log "run $RUN_ID was retryable; retrying with a fresh workspace"
done

if [[ -z "$FINAL_RUN" ]]; then
  for try in $(seq "$MAX_RETRIES" -1 0); do
    candidate="$RUN_BASE-try$try"
    if [[ -d "$OUT_DIR/$candidate" ]]; then
      FINAL_RUN="$candidate"
      break
    fi
  done
  log "no clean run; keeping last run $FINAL_RUN and reporting it as retry-limited"
fi

log "final run: $FINAL_RUN"
python3 - "$OUT_DIR" "$FINAL_RUN" "$MODEL" "$ARMS" "${#TASKS[@]}" "$REPEATS" "$MARGIN" "$REV" "$SIDECAR_REV" "$RETENTION" <<'PY'
import json
import os
import sys

out_dir, run_id, model, arms, task_count, repeats, margin, revision, sidecar, retention = sys.argv[1:11]
run_dir = os.path.join(out_dir, run_id)
with open(os.path.join(run_dir, "aggregate.json"), encoding="utf-8") as fh:
    agg = json.load(fh)
with open(os.path.join(run_dir, "report.md"), encoding="utf-8") as fh:
    report = fh.read()

attempts = []
for name in sorted(os.listdir(run_dir)):
    if name.startswith("attempt-") and name.endswith(".json"):
        with open(os.path.join(run_dir, name), encoding="utf-8") as fh:
            attempts.append(json.load(fh))
status_counts = {}
for a in attempts:
    status_counts[a.get("run_status") or "missing"] = status_counts.get(a.get("run_status") or "missing", 0) + 1

lines = []
lines.append(f"# Paid three-arm warm-cost run: `{run_id}`")
lines.append("")
lines.append("Pre-registered before the first provider request. This file is generated by")
lines.append("`tests/evals/warmcost/paid-run.sh`.")
lines.append("")
lines.append("## Pre-registration")
lines.append("")
lines.append(f"- Model: `{model}`")
lines.append(f"- Arms: `{arms}`")
lines.append(f"- Tasks: {task_count}")
lines.append(f"- Repeats per task per arm: {repeats}")
lines.append(f"- Correctness noninferiority margin: {margin}")
lines.append(f"- Binary revision: `{revision}`")
lines.append(f"- Sidecar revision: `{sidecar}`")
lines.append(f"- Retention: `{retention}`")
if retention == "fresh":
    lines.append(f"- Sidecar database: fresh for this run, so the warm arms have no retained")
    lines.append("  experience. The result measures the enabled mechanisms at cold memory, not a")
    lines.append("  memory-effect claim.")
else:
    lines.append(f"- Sidecar database: shared across the warm arms for this run, so an earlier")
    lines.append("  attempt's evidence can be retrieved by a later attempt.")
lines.append(f"- Attempt statuses in the final run: {status_counts}")
lines.append("")
lines.append("## Runner report")
lines.append("")
lines.append(report.rstrip())
lines.append("")
with open(os.path.join(out_dir, f"PAID_RUN_{run_id}.md"), "w", encoding="utf-8") as fh:
    fh.write("\n".join(lines) + "\n")
print("wrote", os.path.join(out_dir, f"PAID_RUN_{run_id}.md"))
PY

log "complete"
