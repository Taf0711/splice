#!/usr/bin/env bash
# run_journeys.sh [J1|J2|J3|J4|J5|J6|J7|J8|J9|J10|all]
# J6 to J10 are live (ChatGPT provider, tmux). SPLICE_JOURNEY_DRYRUN=1 runs
# only their setup and stops before any model request; use it with a single
# journey, not 'all' (J1 to J5 do not honor it). A dry run does not write
# journey-results.json.
# Runner contract per DESIGN.md: each journey script self-contains its own
# fresh state root (mktemp); classify failures (section 33); write
# journey-results.json under SPLICE_JOURNEY_ROOT (default
# ~/.cache/splice-journeys), not into the repository. A skipped journey
# (infrastructure_skipped) is recorded and does not fail the overall result.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="${SPLICE_JOURNEY_ROOT:-$HOME/.cache/splice-journeys}"
RESULTS="$RESULTS_DIR/journey-results.json"
SPLICE_BIN="${SPLICE_BIN:-splice}"
export SPLICE_BIN

select_journeys() {
  case "${1:-all}" in
    J1|j1) echo "j1_fresh_setup.sh" ;;
    J2|j2) echo "j2_headless_task.sh" ;;
    J3|j3) echo "j3_tui_launch.sh" ;;
    J4|j4) echo "j4_session_reopen.sh" ;;
    J5|j5) echo "j5_cognition_reuse.sh" ;;
    J6|j6) echo "j6_custom_topology.sh" ;;
    J7|j7) echo "j7_failing_command.sh" ;;
    J8|j8) echo "j8_tui_topology.sh" ;;
    J9|j9) echo "j9_tui_hostile.sh" ;;
    J10|j10) echo "j10_design_invalid_routes.sh" ;;
    all|"") echo "j1_fresh_setup.sh j2_headless_task.sh j3_tui_launch.sh j4_session_reopen.sh j5_cognition_reuse.sh j6_custom_topology.sh j7_failing_command.sh j8_tui_topology.sh j9_tui_hostile.sh j10_design_invalid_routes.sh" ;;
    *) echo "unknown journey: $1" >&2; exit 2 ;;
  esac
}

classify() { # $1 = exit code, $2 = output
  local rc="$1" out="$2"
  if [ "$rc" -eq 0 ]; then echo "pass"
  elif echo "$out" | grep -q "not on PATH\|tmux not found"; then echo "infrastructure_failure"
  elif echo "$out" | grep -q "no provider config available"; then echo "infrastructure_skipped"
  elif echo "$out" | grep -q "FALSE SUCCESS"; then echo "false_success"
  elif echo "$out" | grep -q "GUARD FAIL"; then echo "guard_failure"
  elif echo "$out" | grep -q "SETUP FAIL"; then echo "setup_failure"
  elif echo "$out" | grep -q "external verifier failed\|no workspace diff\|non-JSON line\|no result-class\|direct memory hit\|no cognition evidence"; then echo "agent_failure"
  else echo "task_failure"
  fi
}

STARTED=$(date +%s)
TMP_ROWS="$(mktemp)"
: > "$TMP_ROWS"
OVERALL=0
for script in $(select_journeys "${1:-all}"); do
  name="${script%%_*}"
  out="$(bash "$HERE/$script" 2>&1)"; rc=$?
  status="$(classify "$rc" "$out")"
  # infrastructure_skipped (no credential) is recorded, not hidden, and does
  # not fail the overall suite: the journey never ran.
  if [ "$status" != "pass" ] && [ "$status" != "infrastructure_skipped" ]; then
    OVERALL=1
  fi
  printf '%s\t%s\t%s\n' "$name" "$status" "$(echo "$out" | tail -1)"
  J_NAME="$name" J_STATUS="$status" J_DETAIL="$out" python3 -c '
import json, os
print(json.dumps({"journey": os.environ["J_NAME"], "status": os.environ["J_STATUS"], "detail": os.environ["J_DETAIL"][:300]}))
' >> "$TMP_ROWS"
done
ENDED=$(date +%s)
RESULTS_OUT="$RESULTS"
if [ -n "${SPLICE_JOURNEY_DRYRUN:-}" ]; then
  RESULTS_OUT=/dev/null
else
  mkdir -p "$RESULTS_DIR" || { echo "cannot create $RESULTS_DIR" >&2; exit 1; }
fi

python3 - "$TMP_ROWS" "$((ENDED-STARTED))" > "$RESULTS_OUT" <<'PY'
import json, sys
rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
print(json.dumps({
    "schema": "splice.eval.journey-results.v1",
    "duration_seconds": int(sys.argv[2]),
    # Same rule as the exit code: a skipped journey never ran, so it does
    # not fail the result.
    "overall": "pass" if rows and all(r["status"] in ("pass", "infrastructure_skipped") for r in rows) else "fail",
    "journeys": rows,
}, indent=2))
PY
rm -f "$TMP_ROWS"

if [ -n "${SPLICE_JOURNEY_DRYRUN:-}" ]; then
  echo "--- dry run: journey-results.json not written ---"
  exit $OVERALL
fi
echo "--- journey results (also written to $RESULTS) ---"
cat "$RESULTS"
exit $OVERALL
