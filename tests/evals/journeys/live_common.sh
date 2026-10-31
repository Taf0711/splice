#!/usr/bin/env bash
# live_common.sh: shared helpers for the Layer 2 live journeys J6 to J10.
# Sourced by each jN script; never run directly.
#
# Rules every live journey follows:
#   - Provider: the developer's ChatGPT sign-in profile ("chatgpt") only. The
#     copied config.json gets activeProvider=chatgpt. OpenRouter is never used.
#   - No SPLICE_OAUTH_STORAGE or SPLICE_CRED_STORAGE. The OAuth token lives in
#     the shared macOS keyring; an encrypted-file store would be empty.
#   - The real ~/.config/splice files are read only. Their hashes are taken
#     before and after the journey; a change fails the journey (GUARD FAIL).
#   - State root: a fresh mktemp dir under SPLICE_JOURNEY_ROOT (default
#     ~/.cache/splice-journeys), never /tmp (AGENTS.md: the sandbox treats
#     /tmp paths as special; evaluation config stays out of /tmp).
#   - SPLICE_JOURNEY_DRYRUN=1 stops after setup, before any splice command
#     that can reach a provider. It proves the scripts parse and set up.
#   - live_setup unsets every SPLICE_* variable except SPLICE_BIN and
#     SPLICE_JOURNEY_*, so a developer shell override (for example
#     SPLICE_TRUST_WORKSPACE or SPLICE_PROVIDER) cannot change the journey.
#   - live_fail copies the saved TUI screen to
#     $SPLICE_JOURNEY_ROOT/failures/ (default ~/.cache/splice-journeys) before
#     the EXIT trap removes the state root, and prints that path.

REAL_SPLICE_DIR="$HOME/.config/splice"
LIVE_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
LIVE_TASKSET="$LIVE_REPO_ROOT/tests/evals/taskset-v0/fixture"

LIVE_JOURNEY_ROOT="${SPLICE_JOURNEY_ROOT:-$HOME/.cache/splice-journeys}"

# live_fail saves the TUI screen (with scrollback) before it exits, when a TUI
# session is open. The EXIT trap removes STATE_ROOT, so the capture is copied
# to a stable path under $LIVE_JOURNEY_ROOT/failures/.
live_fail() {
  if [ -n "${SESSION:-}" ] && [ -n "${TMUX_CMD:-}" ] && [ -n "${STATE_ROOT:-}" ]; then
    local saved="$LIVE_JOURNEY_ROOT/failures/${LIVE_NAME:-journey}-$(date +%Y%m%d-%H%M%S)-$$.txt"
    if mkdir -p "$LIVE_JOURNEY_ROOT/failures" \
      && "$TMUX_CMD" capture-pane -t "$SESSION" -p -S - > "$saved" 2>/dev/null; then
      echo "screen saved: $saved" >&2
    else
      echo "screen not saved: capture to $saved failed" >&2
    fi
  fi
  echo "$LIVE_NAME FAIL: $*" >&2; exit 1
}
live_setup_fail() { echo "SETUP FAIL ($LIVE_NAME): $*" >&2; exit 1; }
live_guard_fail() { echo "GUARD FAIL ($LIVE_NAME): $*" >&2; exit 1; }

# real_config_hashes prints one sha256 per guarded file ("absent" if missing).
real_config_hashes() {
  local f
  for f in config.json trust.json stage-models.json; do
    if [ -f "$REAL_SPLICE_DIR/$f" ]; then
      shasum -a 256 "$REAL_SPLICE_DIR/$f" | cut -d' ' -f1
    else
      echo absent
    fi
  done | paste -sd, -
}

# live_setup NAME: fresh state root, copied config (chatgpt active), guard hashes.
live_setup() {
  LIVE_NAME="$1"
  SPLICE="${SPLICE_BIN:-splice}"
  command -v "$SPLICE" >/dev/null 2>&1 || live_setup_fail "splice binary not on PATH (set SPLICE_BIN)"
  command -v jq >/dev/null 2>&1 || live_setup_fail "jq not on PATH"

  # Unset every SPLICE_* override from the developer shell. Keep only the
  # binary path and the journey controls.
  local var
  for var in $(compgen -e); do
    case "$var" in
      SPLICE_BIN|SPLICE_JOURNEY_*) ;;
      SPLICE_*) unset "$var" ;;
    esac
  done

  local root="$LIVE_JOURNEY_ROOT"
  mkdir -p "$root" || live_setup_fail "cannot create $root"
  STATE_ROOT="$(mktemp -d "$root/$LIVE_NAME.XXXXXX")" || live_setup_fail "mktemp failed"
  export XDG_STATE_HOME="$STATE_ROOT/state"
  export XDG_DATA_HOME="$STATE_ROOT/data"
  export XDG_CONFIG_HOME="$STATE_ROOT/config"
  mkdir -p "$XDG_STATE_HOME" "$XDG_DATA_HOME" "$XDG_CONFIG_HOME/splice"
  trap 'live_cleanup' EXIT

  local src="${SPLICE_JOURNEY_CONFIG:-$REAL_SPLICE_DIR/config.json}"
  if [ ! -f "$src" ]; then
    echo "SKIP: no provider config available (set SPLICE_JOURNEY_CONFIG)" >&2
    exit 3
  fi
  # Copy, then select the ChatGPT profile in the COPY only. The jq output is
  # written to the copy and never printed.
  jq -e '.providers | map(.name) | index("chatgpt")' "$src" >/dev/null 2>&1 \
    || live_setup_fail "no 'chatgpt' provider profile in the source config"
  jq '.activeProvider = "chatgpt"' "$src" > "$XDG_CONFIG_HOME/splice/config.json" \
    || live_setup_fail "could not write the copied config"
  chmod 600 "$XDG_CONFIG_HOME/splice/config.json"

  GUARD_BEFORE="$(real_config_hashes)"
}

live_cleanup() {
  # A journey that failed mid-run may leave its tmux session behind.
  if [ -n "${SESSION:-}" ] && [ -n "${TMUX_CMD:-}" ]; then
    "$TMUX_CMD" kill-session -t "$SESSION" 2>/dev/null || true
  fi
  if [ -n "${STATE_ROOT:-}" ] && [ -d "$STATE_ROOT" ]; then
    if [ -n "${SPLICE_JOURNEY_KEEP:-}" ]; then
      echo "KEPT STATE ROOT ($LIVE_NAME): $STATE_ROOT (holds a copied config.json; delete when done)" >&2
    else
      rm -rf "$STATE_ROOT"
    fi
  fi
}

# live_dry_stop: in dry-run mode, stop before the first provider-reaching call.
live_dry_stop() {
  if [ -n "${SPLICE_JOURNEY_DRYRUN:-}" ]; then
    echo "DRYRUN OK ($LIVE_NAME): setup complete; stopped before the first model request"
    live_guard_check
    exit 0
  fi
}

live_guard_check() {
  local after
  after="$(real_config_hashes)"
  [ "$after" = "$GUARD_BEFORE" ] || live_guard_fail "real splice config files changed during the journey"
}

# live_fixture DIR: copy the taskset fixture, git init, commit the base state.
live_fixture() {
  local dir="$1"
  cp -R "$LIVE_TASKSET" "$dir" || live_setup_fail "fixture copy failed"
  git -C "$dir" init -q || live_setup_fail "git init failed"
  git -C "$dir" add -A && git -C "$dir" -c user.email=j@eval -c user.name=eval commit -qm base \
    || live_setup_fail "fixture commit failed"
}

# live_bounded SECS cmd...: run cmd, kill it after SECS seconds. Keeps the
# caller's redirections. Returns the command's exit code (or 124 on timeout).
live_bounded() {
  local secs="$1"; shift
  "$@" &
  local pid=$!
  ( sleep "$secs"; kill -TERM "$pid" 2>/dev/null ) &
  local watcher=$!
  wait "$pid"; local rc=$?
  kill "$watcher" 2>/dev/null; wait "$watcher" 2>/dev/null
  [ "$rc" -eq 143 ] && rc=124
  return "$rc"
}

# live_tmux_ok: tmux must exist, else the journey is an infrastructure failure.
live_tmux_ok() {
  TMUX_CMD="${TMUX_BIN:-/opt/homebrew/bin/tmux}"
  command -v "$TMUX_CMD" >/dev/null 2>&1 || command -v tmux >/dev/null 2>&1 \
    || live_fail "tmux not found (set TMUX_BIN)"
  [ -x "$TMUX_CMD" ] || TMUX_CMD="$(command -v tmux)"
}

# live_tmux_start SESSION WORKDIR CMD_ARGS...: start splice in a 120x40 pane
# with the journey environment. Extra env (KEY=VALUE) comes before CMD_ARGS.
live_tmux_start() {
  local session="$1" workdir="$2"; shift 2
  local envs="export TERM=xterm-256color XDG_STATE_HOME='$XDG_STATE_HOME' XDG_DATA_HOME='$XDG_DATA_HOME' XDG_CONFIG_HOME='$XDG_CONFIG_HOME' SPLICE_NO_RESUME_PROMPT=1 SPLICE_MEMD_DB='$STATE_ROOT/mem.db' SPLICE_MEMD_SOCKET='$STATE_ROOT/mem.sock';"
  local cmd="" arg
  for arg in "$@"; do cmd="$cmd '$arg'"; done
  "$TMUX_CMD" new-session -d -s "$session" -x 120 -y 40 \
    "$envs cd '$workdir' && $cmd 2>'$STATE_ROOT/tui_stderr'; echo TUI_EXIT=\$?" \
    || live_fail "tmux new-session failed"
  "$TMUX_CMD" set-option -t "$session" remain-on-exit on >/dev/null 2>&1 || true
}

live_capture() { "$TMUX_CMD" capture-pane -t "$1" -p 2>/dev/null; }
# live_capture_full includes scrollback. Use it for text that may have scrolled off.
live_capture_full() { "$TMUX_CMD" capture-pane -t "$1" -p -S - 2>/dev/null; }
live_wait_full() {
  local session="$1" pattern="$2" secs="$3" i
  for i in $(seq 1 "$secs"); do
    live_capture_full "$session" | grep -Eq "$pattern" && return 0
    sleep 1
  done
  return 1
}

# live_wait_for SESSION REGEX SECS: poll the pane until REGEX matches.
live_wait_for() {
  local session="$1" pattern="$2" secs="$3" i
  for i in $(seq 1 "$secs"); do
    live_capture "$session" | grep -Eq "$pattern" && return 0
    sleep 1
  done
  return 1
}

# live_select_row SESSION LABEL [KEY]: press KEY (default Up) until the menu row
# marked with LIVE_MARK (default "❯") contains LABEL. The row count varies with the
# project config, so a fixed key count can wrap past the wanted row. Bounded: 8 presses.
live_select_row() {
  local session="$1" label="$2" key="${3:-Up}" i line mark="${LIVE_MARK:-❯}"
  for i in 1 2 3 4 5 6 7 8; do
    line="$(live_capture "$session" | grep -F "$mark" | head -1)"
    case "$line" in *"$label"*) return 0 ;; esac
    "$TMUX_CMD" send-keys -t "$session" "$key"; sleep 0.4
  done
  return 1
}

live_keys() { "$TMUX_CMD" send-keys -t "$1" -l "$2"; "$TMUX_CMD" send-keys -t "$1" Enter; }

live_stop_tui() {
  "$TMUX_CMD" send-keys -t "$1" C-c; sleep 1
  "$TMUX_CMD" send-keys -t "$1" C-c; sleep 2
  live_capture "$1" > "$STATE_ROOT/final_capture.txt"
  "$TMUX_CMD" kill-session -t "$1" 2>/dev/null || true
}
