#!/usr/bin/env bash
# J1 — fresh-install-and-configure journey.
# Fresh state root, doctor + config from a pristine state. No maintainer knowledge;
# only normal CLI surface. Exit 0 iff every expectation holds.
set -u
fail() { echo "J1 FAIL: $*" >&2; exit 1; }

STATE_ROOT="$(mktemp -d /tmp/splice-j1.XXXXXX)"
export XDG_STATE_HOME="$STATE_ROOT/state"
export XDG_DATA_HOME="$STATE_ROOT/data"
export XDG_CONFIG_HOME="$STATE_ROOT/config"
mkdir -p "$XDG_STATE_HOME" "$XDG_DATA_HOME" "$XDG_CONFIG_HOME"
trap 'rm -rf "$STATE_ROOT"' EXIT

SPLICE="${SPLICE_BIN:-splice}"
command -v "$SPLICE" >/dev/null 2>&1 || fail "splice binary not on PATH (set SPLICE_BIN)"

# 1. doctor runs and does NOT leak key material. Exit semantics (internal/cli/observability.go):
#    0 = report OK (provider configured), 3 = exitProvider (report not OK: fresh install with no
#    provider is the EXPECTED fresh-state outcome, not a crash). Any other code = crash.
DOCTOR_RC=0
DOCKER_OUT=""
DOCTOR_OUT="$("$SPLICE" doctor 2>&1)" || DOCTOR_RC=$?
case "$DOCTOR_RC" in
  0|3) : ;;
  *) fail "doctor crashed with exit $DOCTOR_RC: $DOCTOR_OUT" ;;
esac
echo "$DOCTOR_OUT" | grep -qE 'config|provider|sandbox' || fail "doctor report missing check labels"
if echo "$DOCTOR_OUT" | grep -qiE 'sk-[a-z0-9]{8,}|api[_-]?key[[:space:]]*[:=][[:space:]]*["'\'']?[A-Za-z0-9]{16,}'; then
  fail "doctor output contains key material"
fi

# 2. config runs, exits 0, and (when it prints JSON) the JSON parses.
CONFIG_OUT="$("$SPLICE" config 2>&1)" || fail "config exited non-zero: $CONFIG_OUT"
if echo "$CONFIG_OUT" | grep -qiE 'sk-[a-z0-9]{8,}'; then
  fail "config output contains key material"
fi
if echo "$CONFIG_OUT" | grep -q '^'; then
  # best-effort JSON parse: only required if output starts with { or [
  FIRST=$(echo "$CONFIG_OUT" | head -c 1)
  if [ "$FIRST" = "{" ] || [ "$FIRST" = "[" ]; then
    echo "$CONFIG_OUT" | jq empty >/dev/null 2>&1 || fail "config printed invalid JSON"
  fi
fi

# 3. sessions list works on a fresh state (empty listing is fine, crash is not).
"$SPLICE" sessions list >/dev/null 2>&1 || fail "sessions list failed on fresh state"

echo "J1 PASS"
