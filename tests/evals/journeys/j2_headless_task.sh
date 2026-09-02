#!/usr/bin/env bash
# J2 — headless-task-exec journey.
# Runs a real task in the pristine fixture workspace via splice exec with
# stream-json output; asserts stream validity, a final result event, a real
# workspace diff, and that the EXTERNAL verifier (taskset check command)
# passes. Splice's own verdict is never the authority.
set -u
HOME_REAL_CONFIG="${SPLICE_JOURNEY_CONFIG:-$HOME/.config/splice/config.json}"
fail() { echo "J2 FAIL: $*" >&2; exit 1; }

STATE_ROOT="$(mktemp -d /tmp/splice-j2.XXXXXX)"
export XDG_STATE_HOME="$STATE_ROOT/state"
export XDG_DATA_HOME="$STATE_ROOT/data"
export XDG_CONFIG_HOME="$STATE_ROOT/config"
mkdir -p "$XDG_STATE_HOME" "$XDG_DATA_HOME" "$XDG_CONFIG_HOME"

# Seed the provider credential WITHOUT printing it (section 17: secrets never
# surface in journey output). A fresh state has no provider config, and splice
# exec correctly refuses to run without one (exit 3, fail-loud). The runner or
# a human points SPLICE_JOURNEY_CONFIG at a config.json to copy in; missing
# config is an infrastructure failure (exit 3), not an agent failure.
JOURNEY_CFG="${SPLICE_JOURNEY_CONFIG:-$HOME_REAL_CONFIG}"
if [ -z "$JOURNEY_CFG" ] || [ ! -f "$JOURNEY_CFG" ]; then
  echo "SKIP: no provider config available (set SPLICE_JOURNEY_CONFIG)" >&2
  exit 3
fi
mkdir -p "$XDG_CONFIG_HOME/splice"
cp "$JOURNEY_CFG" "$XDG_CONFIG_HOME/splice/config.json"
trap 'rm -rf "$STATE_ROOT"' EXIT

SPLICE="${SPLICE_BIN:-splice}"
command -v "$SPLICE" >/dev/null 2>&1 || fail "splice binary not on PATH (set SPLICE_BIN)"
TASKSET_ROOT="$(cd "$(dirname "$0")/../taskset-v0" && pwd)"
[ -d "$TASKSET_ROOT/fixture" ] || fail "taskset-v0/fixture not found"

# Optional JSON file mapping journey task name -> verifier shell command.
# Falls back to the built-in healthz task below.
TASK_JSON="${J2_TASK_JSON:-}"
PROMPT='Add a GET /healthz endpoint to main.go that responds 200 with the JSON body {"status":"ok"}. Keep the change minimal: handler function, route registration, and the writeJSON helper already in the file.'
CHECK_CMD='go build ./... && go vet ./... && grep -q "healthz" main.go'

WORK="$STATE_ROOT/workspace"
cp -R "$TASKSET_ROOT/fixture" "$WORK"
git -C "$WORK" init -q 2>/dev/null || fail "git init failed"
git -C "$WORK" add -A && git -C "$WORK" -c user.email=j@eval -c user.name=eval commit -qm base

# The task JSON (when provided) supplies prompt + check so journeys track the taskset.
if [ -n "$TASK_JSON" ] && [ -f "$TASK_JSON" ]; then
  PROMPT="$(python3 -c "import json;print(json.load(open('$TASK_JSON'))['prompt'])")"
  CHECK_CMD="$(python3 -c "import json;print(json.load(open('$TASK_JSON'))['check'])")"
fi

OUT="$STATE_ROOT/stream.jsonl"
cd "$WORK" || fail "cd workspace"
"$SPLICE" exec --output-format stream-json --prompt "$PROMPT" >"$OUT" 2>"$STATE_ROOT/stderr" \
  || fail "splice exec exited non-zero (see $STATE_ROOT/stderr)"

# 1. every non-empty line is valid JSON.
python3 - "$OUT" <<'PY' || fail "stream-json contains a non-JSON line"
import json, sys
for i, line in enumerate(open(sys.argv[1])):
    line = line.strip()
    if not line:
        continue
    try:
        json.loads(line)
    except Exception as e:
        print(f"line {i+1}: {e}")
        sys.exit(1)
PY

# 2. a final result event exists with a type field (schema-versioned JSONL).
grep -q '"type"' "$OUT" || fail "no typed events in stream"
if ! grep -qiE '"result"|"run_result"|"turn.completed"|"final"' "$OUT"; then
  fail "no result-class event found in stream"
fi

# 3. the workspace actually changed (real diff, not a self-reported success).
git -C "$WORK" diff --quiet && fail "no workspace diff after exec run"

# 4. the EXTERNAL verifier decides (section 17: Splice's verdict is not authoritative).
bash -c "$CHECK_CMD" >/dev/null 2>"$STATE_ROOT/verify_err" || fail "external verifier failed: $(cat "$STATE_ROOT/verify_err" | tail -3)"

echo "J2 PASS"
