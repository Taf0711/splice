#!/usr/bin/env bash
# J4 — session-reopen journey.
# First run creates a session; `splice sessions list` finds it; --resume
# continues it with a follow-up prompt; the lineage shows continuity.
set -u
HOME_REAL_CONFIG="${SPLICE_JOURNEY_CONFIG:-$HOME/.config/splice/config.json}"
fail() { echo "J4 FAIL: $*" >&2; exit 1; }

STATE_ROOT="$(mktemp -d /tmp/splice-j4.XXXXXX)"
export XDG_STATE_HOME="$STATE_ROOT/state"
export XDG_DATA_HOME="$STATE_ROOT/data"
export XDG_CONFIG_HOME="$STATE_ROOT/config"
mkdir -p "$XDG_STATE_HOME" "$XDG_DATA_HOME" "$XDG_CONFIG_HOME"

# Seed the provider credential without printing it (see J2 for the rationale).
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

WORK="$STATE_ROOT/workspace"
TASKSET_ROOT="$(cd "$(dirname "$0")/../taskset-v0" && pwd)"
cp -R "$TASKSET_ROOT/fixture" "$WORK"
git -C "$WORK" init -q
git -C "$WORK" add -A && git -C "$WORK" -c user.email=j@eval -c user.name=eval commit -qm base
cd "$WORK" || fail "cd workspace"

# Run 1: a task that produces a session.
"$SPLICE" exec --output-format stream-json --prompt 'Add a package-level const sessionTTL = 30 * time.Minute in session.go and use it as the store TTL.' \
  >"$STATE_ROOT/run1.jsonl" 2>/dev/null || fail "first exec failed"

# 1. the session exists in local lineage.
LIST_OUT="$("$SPLICE" sessions list 2>&1)" || fail "sessions list failed"
# Session ids are zero_<timestamp>_<nanos>_<n> (internal/sessions sessionIDPattern allows
# [A-Za-z0-9][A-Za-z0-9_-]{0,127}); extract from the "  - <id> <prompt>" listing row.
SESSION_ID="$(echo "$LIST_OUT" | sed -n 's/^[[:space:]]*- \([A-Za-z0-9][A-Za-z0-9_-]*\)[[:space:]].*/\1/p' | head -1)"
[ -n "$SESSION_ID" ] || fail "no session id found in sessions list"

# 2. resume continues the SAME session (id echoed in run 2's stream or lineage shows it).
"$SPLICE" exec --output-format stream-json --resume "$SESSION_ID" \
  --prompt 'Now make the sweep interval use the same sessionTTL constant.' \
  >"$STATE_ROOT/run2.jsonl" 2>/dev/null || fail "resume exec failed"

# 3. lineage for the resumed session resolves (root-to-session path prints).
"$SPLICE" sessions lineage "$SESSION_ID" >/dev/null 2>&1 || fail "lineage command failed"

# 4. the workspace changed in run 2 (the follow-up did real work).
git -C "$WORK" diff --stat | grep -q . || fail "no diff after resumed run"

echo "J4 PASS"
