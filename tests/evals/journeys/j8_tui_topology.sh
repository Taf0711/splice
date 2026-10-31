#!/usr/bin/env bash
# J8: TUI topology run (live ChatGPT provider, tmux 120x40).
# Trust the fixture, pick the imported library topology with /pipeline use,
# submit a light-tier task, open the Ctrl+G pane and a node drawer during the
# run, wait for the receipt, then stop. The EXTERNAL verifier decides whether
# the receipt kind is right. Captures are taken every 2 s into $STATE_ROOT/cap.
set -u
LIVE_NAME=j8
. "$(cd "$(dirname "$0")" && pwd)/live_common.sh"
CHECK="$(cd "$(dirname "$0")" && pwd)/live_check.py"
live_tmux_ok

live_setup "$LIVE_NAME"
WORK="$STATE_ROOT/workspace"
live_fixture "$WORK"

TOPO_SRC="$LIVE_REPO_ROOT/docs/examples/pipeline-write-vet-test.json"
"$SPLICE" pipeline import "$TOPO_SRC" --yes >"$STATE_ROOT/import.out" 2>&1 \
  || live_setup_fail "pipeline import failed (see $STATE_ROOT/import.out)"

PROMPT='In main.go, add the line // journey-j8 topology marker directly above func main. Change nothing else.'
VERIFY='go build ./... && go vet ./... && grep -Fq "journey-j8 topology marker" main.go'

live_dry_stop

SESSION="j8_$$"
CAPDIR="$STATE_ROOT/cap"; mkdir -p "$CAPDIR"
# A receipt is a card whose title line carries its kind (the top border has no title),
# or the Ctrl+G pane's "RECEIPT" section. live_check.py receipt matches both forms and
# never the sidebar counter ("PIPELINE EXECUTING | FAILED 1/1").
live_tmux_start "$SESSION" "$WORK" "$SPLICE"

live_wait_for "$SESSION" 'Trust this folder' 20 || live_fail "trust card did not appear"
# The cursor starts on the last row, and the row count varies. Select the row by its label.
live_select_row "$SESSION" 'Trust this folder' Up || live_fail "trust menu has no selectable 'Trust this folder' row"
"$TMUX_CMD" send-keys -t "$SESSION" Enter
sleep 3
live_capture "$SESSION" > "$STATE_ROOT/after_trust.txt"
grep -q 'untrusted' "$STATE_ROOT/after_trust.txt" && live_fail "workspace still untrusted after the trust choice"
jq -e '[.workspaces // {} | to_entries[] | select(.value.trusted == true)] | length > 0' \
  "$XDG_CONFIG_HOME/splice/trust.json" >/dev/null 2>&1 || live_fail "trust.json records no trusted workspace"

live_keys "$SESSION" "/pipeline"; sleep 2
live_keys "$SESSION" "/pipeline use write-vet-test"; sleep 2
live_capture "$SESSION" > "$STATE_ROOT/after_use.txt"
grep -q "write-vet-test" "$STATE_ROOT/after_use.txt" || live_fail "topology write-vet-test not selected"

# The default mode is design. /exec runs the task through the pipeline directly.
live_keys "$SESSION" "/exec $PROMPT"
sleep 8
"$TMUX_CMD" send-keys -t "$SESSION" C-g; sleep 2          # open the Ctrl+G pane during the run
"$TMUX_CMD" send-keys -t "$SESSION" Down; sleep 1
"$TMUX_CMD" send-keys -t "$SESSION" Enter; sleep 1        # open the node drawer

# Capture every 2 s until the receipt appears (10 min budget).
n=0; deadline=$(( $(date +%s) + 600 )); RECEIPT=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  n=$((n + 1))
  f="$(printf '%s/%04d.txt' "$CAPDIR" "$n")"
  live_capture_full "$SESSION" > "$f"
  # The run stops at "Worktree review" until the user decides. Accept merges the change into the
  # checkout (the external verifier reads that checkout). Select Accept by its label, once.
  if [ -z "${ACCEPTED:-}" ] && live_capture "$SESSION" | grep -q 'Worktree review'; then
    LIVE_MARK='▸' live_select_row "$SESSION" '1. Accept' Up || live_fail "worktree review has no '1. Accept' row"
    "$TMUX_CMD" send-keys -t "$SESSION" Enter; ACCEPTED=1; sleep 2
  fi
  if python3 "$CHECK" receipt-seen "$f" >/dev/null 2>&1; then RECEIPT=1; break; fi
  sleep 2
done
[ -n "$RECEIPT" ] || live_fail "no receipt within 10 minutes (captures in $CAPDIR)"

# After the receipt, the open drawer walks the graph. test_generator must read as not run at
# light (its drawer status is "skipped at light"). Then Up to code_writer, which ran, and its
# drawer must show the model it ran with.
NOTRUN=0; MODELSEEN=0
for step in 1 2 3 4 5 6 7 8; do
  f="$(printf '%s/post-%02d.txt' "$CAPDIR" "$step")"
  live_capture "$SESSION" > "$f"
  if grep -qE 'NODE test_generator' "$f" && grep -qE 'status +(skipped at light|not run)' "$f"; then NOTRUN=1; fi
  if grep -qE '│ +model +' "$f"; then MODELSEEN=1; fi
  [ "$NOTRUN" -eq 1 ] && [ "$MODELSEEN" -eq 1 ] && break
  if [ "$NOTRUN" -eq 1 ]; then "$TMUX_CMD" send-keys -t "$SESSION" Up; else "$TMUX_CMD" send-keys -t "$SESSION" Down; fi
  sleep 1
done
live_capture_full "$SESSION" > "$STATE_ROOT/final_full.txt"
# The Ctrl+G drawer covers the transcript, so the receipt card is not on screen yet.
# Each Esc backs out one level (node details, then the pane). Press Esc up to three
# times, capturing after each, until the transcript card shows (30 captures at most).
ESC_PRESSES=0
for _ in $(seq 1 30); do
  if [ "$ESC_PRESSES" -lt 3 ]; then
    "$TMUX_CMD" send-keys -t "$SESSION" Escape; ESC_PRESSES=$((ESC_PRESSES + 1))
  fi
  sleep 2
  n=$((n + 1))
  f="$(printf '%s/%04d.txt' "$CAPDIR" "$n")"
  live_capture_full "$SESSION" > "$f"
  if python3 "$CHECK" receipt "$f" >/dev/null 2>&1; then break; fi
done
live_stop_tui "$SESSION"

# Receipt kind, from the last receipt card on screen, cross-checked against the pane status.
RECEIPT_JSON="$(python3 "$CHECK" receipt "$CAPDIR"/*.txt "$STATE_ROOT/final_full.txt")" \
  || live_fail "receipt check failed (captures in $CAPDIR)"
KIND="$(printf '%s' "$RECEIPT_JSON" | jq -r '.kind')"

EXT_OK=0
(cd "$WORK" && bash -c "$VERIFY") >/dev/null 2>&1 && EXT_OK=1
if [ "$KIND" = "verified" ] && [ "$EXT_OK" -ne 1 ]; then live_fail "FALSE SUCCESS: receipt verified, external verifier failed"; fi
if [ "$KIND" != "verified" ] && [ "$EXT_OK" -eq 1 ]; then live_fail "receipt $KIND but the external verifier passed"; fi
echo "receipt=$KIND external=$EXT_OK"

# Screen assertions: statuses move, drawer shows a model line, a node reads "not run", no panic.
python3 "$CHECK" captures "$CAPDIR" >/dev/null || live_fail "screen check failed (statuses static or panic text)"
[ "$MODELSEEN" -eq 1 ] || live_fail "drawer shows no model line for a node that ran"
[ "$NOTRUN" -eq 1 ] || live_fail "test_generator did not read 'skipped at light' or 'not run' after the receipt"
if grep -qiE 'panic:|goroutine [0-9]+ \[' "$STATE_ROOT/tui_stderr" "$STATE_ROOT/final_capture.txt" 2>/dev/null; then
  live_fail "panic text in TUI output"
fi
live_guard_check
echo "J8 PASS"
