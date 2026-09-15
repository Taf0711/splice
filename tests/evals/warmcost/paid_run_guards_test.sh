#!/usr/bin/env bash
# Shell-level guards for paid-run.sh's build selection (T1b B1/B2/B3).
#
# These run with BUILD_ONLY=1, a no-provider seam that stops right after the
# builds. Nothing here starts a sidecar or calls a provider.
#
# Cases:
#   A. An unresolvable BUILD_REV exits non-zero and names the bad value.
#   B. An operator-supplied BIN without BIN_PREBUILT=1 is refused, and the
#      supplied file is not overwritten.
#   C. An operator-supplied BIN with BIN_PREBUILT=1 proceeds, is not
#      overwritten, is logged as used, and the runner is still built.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/paid-run.sh"
TASKSET_SRC="$HERE/retention-taskset"
TASKS_ENV_VALUE="clock-helper-write clock-helper-read"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

[ -f "$SCRIPT" ] || fail "paid-run.sh not found at $SCRIPT"
[ -d "$TASKSET_SRC" ] || fail "retention taskset not found at $TASKSET_SRC"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/paid-run-guards.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

# Case A: BUILD_REV that does not resolve.
out="$tmp/a.out"
set +e
TASKSET_SRC="$TASKSET_SRC" TASKS_ENV="$TASKS_ENV_VALUE" \
  BUILD_REV="definitely-not-a-revision-xyz" BUILD_ONLY=1 \
  BIN="$tmp/bin-a" RUNNER="$tmp/runner-a" \
  bash "$SCRIPT" >"$out" 2>&1
rc=$?
set -e
[ "$rc" -ne 0 ] || fail "case A: unresolvable BUILD_REV exited 0"
grep -q "definitely-not-a-revision-xyz" "$out" \
  || fail "case A: message does not name the bad BUILD_REV value: $(cat "$out")"
echo "case A ok: unresolvable BUILD_REV refused (exit $rc)"

# Case B: operator BIN without BIN_PREBUILT must not be overwritten.
printf 'operator-binary-payload' >"$tmp/bin-b"
before="$(cat "$tmp/bin-b")"
out="$tmp/b.out"
set +e
TASKSET_SRC="$TASKSET_SRC" TASKS_ENV="$TASKS_ENV_VALUE" \
  BUILD_REV=HEAD BUILD_ONLY=1 \
  BIN="$tmp/bin-b" RUNNER="$tmp/runner-b" \
  bash "$SCRIPT" >"$out" 2>&1
rc=$?
set -e
[ "$rc" -ne 0 ] || fail "case B: operator BIN without BIN_PREBUILT exited 0"
grep -q "refusing to rebuild" "$out" \
  || fail "case B: no refusal message: $(cat "$out")"
[ "$(cat "$tmp/bin-b")" = "$before" ] \
  || fail "case B: operator-supplied BIN was overwritten"
echo "case B ok: operator BIN refused and left untouched (exit $rc)"

# Case C: operator BIN with BIN_PREBUILT=1 proceeds and is left untouched.
printf 'operator-binary-payload' >"$tmp/bin-c"
before="$(cat "$tmp/bin-c")"
out="$tmp/c.out"
set +e
TASKSET_SRC="$TASKSET_SRC" TASKS_ENV="$TASKS_ENV_VALUE" \
  BUILD_REV=HEAD BUILD_ONLY=1 BIN_PREBUILT=1 \
  BIN="$tmp/bin-c" RUNNER="$tmp/runner-c" \
  bash "$SCRIPT" >"$out" 2>&1
rc=$?
set -e
[ "$rc" -eq 0 ] || fail "case C: BIN_PREBUILT=1 should proceed (exit $rc): $(cat "$out")"
[ "$(cat "$tmp/bin-c")" = "$before" ] \
  || fail "case C: operator-supplied BIN was overwritten"
grep -q "using operator-supplied binary" "$out" \
  || fail "case C: did not log that the supplied binary is used: $(cat "$out")"
[ -x "$tmp/runner-c" ] || fail "case C: runner was not built"
echo "case C ok: operator BIN used as-is and runner built"

echo "all paid-run build guards passed"
