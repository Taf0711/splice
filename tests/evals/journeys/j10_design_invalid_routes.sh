#!/usr/bin/env bash
# J10: design mode with an invalid stage-models.json (live provider, tmux 120x40).
# The copied config dir gets a malformed stage-models.json. /crystallize needs at
# least one conversation message (CrystallizeAndCritique), so the journey sends
# exactly ONE short design message first (one cheap request). Route resolution
# runs before the first model request, so the crystallize failure must name the
# invalid routing cause. Asserts: the failure text names "stage routing config
# invalid" and the file; no plan is produced.
# Not scripted: the spec's drawer-left-open-across-a-tier-change check.
set -u
LIVE_NAME=j10
. "$(cd "$(dirname "$0")" && pwd)/live_common.sh"
CHECK="$(cd "$(dirname "$0")" && pwd)/live_check.py"
live_tmux_ok

live_setup "$LIVE_NAME"
WORK="$STATE_ROOT/workspace"
live_fixture "$WORK"

# Malformed on purpose: the copied config dir only (the real file is never touched).
printf '{"stages": {\n' > "$XDG_CONFIG_HOME/splice/stage-models.json"

live_dry_stop

SESSION="j10_$$"
live_tmux_start "$SESSION" "$WORK" "$SPLICE" --trust
sleep 5

live_keys "$SESSION" "/design"
live_wait_for "$SESSION" 'Design conversation mode' 20 || live_fail "design mode did not start"

live_keys "$SESSION" "Name one file this fixture uses."
# The design turn is a real request. Wait for it to finish (the reply, or the
# invalid cause) before /crystallize, because crystallize is refused while a run is
# active (design_mode.go "Cannot crystallize while a run is active.").
live_wait_full "$SESSION" 'stage routing config invalid|main\.go|Plan|Next:' 120 || true
sleep 5

# /crystallize: retry while the design turn is still active.
CRYSTAL_OK=0
OUTCOME='stage routing config invalid|Crystallization failed|Cannot crystallize'
for _ in 1 2 3 4 5; do
  live_keys "$SESSION" "/crystallize"
  sleep 2
  # If the palette swallowed the first Enter, the command is still in the composer. Submit once more.
  live_capture_full "$SESSION" | grep -Eq "$OUTCOME" || "$TMUX_CMD" send-keys -t "$SESSION" Enter
  if live_wait_full "$SESSION" "$OUTCOME" 60; then
    if live_capture_full "$SESSION" | grep -q 'Cannot crystallize while a run is active'; then
      sleep 20; continue
    fi
    CRYSTAL_OK=1; break
  fi
done
[ "$CRYSTAL_OK" -eq 1 ] || live_fail "crystallize never reached the route check (see scrollback in $STATE_ROOT)"
live_capture_full "$SESSION" > "$STATE_ROOT/crystallize_capture.txt"

# The error card wraps, so the cause can break across lines. Read the card's text joined.
python3 "$CHECK" errorbox "$STATE_ROOT/crystallize_capture.txt" > "$STATE_ROOT/error_text.txt"
grep -q 'Crystallization failed:' "$STATE_ROOT/error_text.txt" \
  || live_fail "no crystallization error card on screen (see crystallize_capture.txt)"
grep -q 'stage routing config invalid' "$STATE_ROOT/error_text.txt" \
  || live_fail "crystallize did not fail with the invalid stage routing cause: $(cat "$STATE_ROOT/error_text.txt")"
grep -q 'stage-models.json' "$STATE_ROOT/error_text.txt" \
  || live_fail "the crystallize failure does not name stage-models.json: $(cat "$STATE_ROOT/error_text.txt")"
if grep -q 'Plan ready\|Plan:' "$STATE_ROOT/crystallize_capture.txt"; then
  live_fail "a plan was produced despite the invalid stage routing file"
fi

live_stop_tui "$SESSION"
if grep -qiE 'panic:|goroutine [0-9]+ \[' "$STATE_ROOT/tui_stderr" "$STATE_ROOT/final_capture.txt" 2>/dev/null; then
  live_fail "panic text in TUI output"
fi
live_guard_check
echo "J10 PASS"
