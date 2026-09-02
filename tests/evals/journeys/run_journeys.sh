#!/usr/bin/env bash
# run_journeys.sh [J1|J2|J3|J4|J5|all]
# Runner contract per DESIGN.md: each journey script self-contains its own
# fresh state root (mktemp); classify failures (section 33); write
# journey-results.json.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
RESULTS="$HERE/journey-results.json"
SPLICE_BIN="${SPLICE_BIN:-splice}"
export SPLICE_BIN

select_journeys() {
  case "${1:-all}" in
    J1|j1) echo "j1_fresh_setup.sh" ;;
    J2|j2) echo "j2_headless_task.sh" ;;
    J3|j3) echo "j3_tui_launch.sh" ;;
    J4|j4) echo "j4_session_reopen.sh" ;;
    J5|j5) echo "j5_cognition_reuse.sh" ;;
    all|"") echo "j1_fresh_setup.sh j2_headless_task.sh j3_tui_launch.sh j4_session_reopen.sh j5_cognition_reuse.sh" ;;
    *) echo "unknown journey: $1" >&2; exit 2 ;;
  esac
}

classify() { # $1 = exit code, $2 = output
  local rc="$1" out="$2"
  if [ "$rc" -eq 0 ]; then echo "pass"
  elif echo "$out" | grep -q "not on PATH\|tmux not found"; then echo "infrastructure_failure"
  elif echo "$out" | grep -q "no provider config available"; then echo "infrastructure_skipped"
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
  [ "$status" = "pass" ] || OVERALL=1
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

python3 - "$TMP_ROWS" "$((ENDED-STARTED))" > "$RESULTS" <<'PY'
import json, sys
rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
print(json.dumps({
    "schema": "splice.eval.journey-results.v1",
    "duration_seconds": int(sys.argv[2]),
    "overall": "pass" if rows and all(r["status"] == "pass" for r in rows) else "fail",
    "journeys": rows,
}, indent=2))
PY
rm -f "$TMP_ROWS"

echo "--- journey results (also written to $RESULTS) ---"
cat "$RESULTS"
exit $OVERALL
