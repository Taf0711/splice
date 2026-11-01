#!/usr/bin/env bash
# J7: failing command node, headless exec (live ChatGPT provider).
# Same as J6, but the vet command node is renamed "lint" and runs ["sh","-c","exit 3"].
# Asserts: splice does NOT claim verified (no false success); the failing node
# is named in the stream; the run does not end as a clean success with a verified claim.
set -u
LIVE_NAME=j7
. "$(cd "$(dirname "$0")" && pwd)/live_common.sh"
CHECK="$(cd "$(dirname "$0")" && pwd)/live_check.py"

live_setup "$LIVE_NAME"
WORK="$STATE_ROOT/workspace"
live_fixture "$WORK"

TOPO="$STATE_ROOT/topology.json"
jq '.name = "write-lint-fail"
    | .nodes |= map(if .name == "vet" then .name = "lint" | .command = ["sh","-c","exit 3"] else . end)
    | .edges |= map(if .from == "vet" then .from = "lint" elif .to == "vet" then .to = "lint" else . end)' \
  "$LIVE_REPO_ROOT/docs/examples/pipeline-write-vet-test.json" > "$TOPO" || live_setup_fail "could not build failing topology"
jq -e '.nodes[] | select(.name == "lint" and .command == ["sh","-c","exit 3"])' "$TOPO" >/dev/null \
  || live_setup_fail "failing node not present in the topology"

PROMPT='In main.go, add the line // journey-j7 topology marker directly above func main. Change nothing else.'
VERIFY='go build ./... && go vet ./... && grep -Fq "journey-j7 topology marker" main.go'

live_dry_stop

OUT="$STATE_ROOT/stream.jsonl"
cd "$WORK" || live_setup_fail "cd workspace"
live_bounded 900 "$SPLICE" exec -o stream-json --trust --pipeline "$TOPO" -p "$PROMPT" >"$OUT" 2>"$STATE_ROOT/stderr"
RC=$?
# Exit code is not asserted here: exec may end with a non-zero code or a clean
# completion carrying an unverified reason. The verified claim is what matters.
[ "$RC" -ne 124 ] || live_fail "splice exec timed out"

python3 "$CHECK" stream "$OUT" >/dev/null || live_fail "stream-json does not parse"

# 1. the failing node is named and reached a failed-class status.
python3 "$CHECK" failing "$OUT" lint >/dev/null || live_fail "the failing lint node is not reported as failed"

# 2. no false success: splice must not claim verified.
CLAIM="$(python3 "$CHECK" verdict "$OUT")" || live_fail "could not read splice's verdict"
SPLICE_OK="$(echo "$CLAIM" | jq -r '.splice_verified')"
[ "$SPLICE_OK" = "false" ] || live_fail "FALSE SUCCESS: splice claimed verified with a failing command node ($CLAIM)"

# 3. the external verifier is recorded for the journey log (not the pass criterion here).
bash -c "$VERIFY" >/dev/null 2>&1 && echo "external verifier: pass (splice still correctly unverified)" || echo "external verifier: fail"

live_guard_check
echo "J7 PASS"
