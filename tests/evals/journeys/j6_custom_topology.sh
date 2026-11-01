#!/usr/bin/env bash
# J6: custom topology, headless exec (live ChatGPT provider).
# Runs splice exec with docs/examples/pipeline-write-vet-test.json on a copy of
# the taskset-v0 fixture. A light-tier task (no classifier keyword). The
# EXTERNAL verifier (build, vet, grep for the change) decides the truth.
# Asserts: stream-json parses; each always-run topology node has exactly one
# terminal stage event; the vet command node passed; splice's verified claim
# equals the external verifier (no false success, no false failure).
set -u
LIVE_NAME=j6
. "$(cd "$(dirname "$0")" && pwd)/live_common.sh"
CHECK="$(cd "$(dirname "$0")" && pwd)/live_check.py"

live_setup "$LIVE_NAME"
WORK="$STATE_ROOT/workspace"
live_fixture "$WORK"

TOPO="$STATE_ROOT/topology.json"
cp "$LIVE_REPO_ROOT/docs/examples/pipeline-write-vet-test.json" "$TOPO" || live_setup_fail "example topology missing"

# Light tier: no trivial keyword (typo, rename, format, comment, spelling), no
# substantial or architectural keyword (auth, service, database, ...).
PROMPT='In main.go, add the line // journey-j6 topology marker directly above func main. Change nothing else.'
VERIFY='go build ./... && go vet ./... && grep -Fq "journey-j6 topology marker" main.go'

live_dry_stop

OUT="$STATE_ROOT/stream.jsonl"
cd "$WORK" || live_setup_fail "cd workspace"
live_bounded 900 "$SPLICE" exec -o stream-json --trust --pipeline "$TOPO" -p "$PROMPT" >"$OUT" 2>"$STATE_ROOT/stderr"
RC=$?
[ "$RC" -eq 0 ] || live_fail "splice exec exited $RC (stderr in $STATE_ROOT/stderr)"

# 1. stream-json parses line by line.
python3 "$CHECK" stream "$OUT" >/dev/null || live_fail "stream-json does not parse"

# 2. each always-run topology node: exactly one terminal stage event.
python3 "$CHECK" topology "$OUT" code_writer vet test_runner acceptance_verifier >/dev/null \
  || live_fail "a topology node did not report exactly one terminal stage event"

# 3. the vet command node completed (run.go emits "completed" for a passing stage; go vet prints nothing on success).
python3 - "$OUT" <<'PY' || live_fail "vet command node did not report completed"
import json, sys
ok = False
for line in open(sys.argv[1], encoding="utf-8", errors="replace"):
    if not line.strip():
        continue
    e = json.loads(line)
    if e.get("type") == "stage" and e.get("name") == "vet" and e.get("status") == "completed":
        ok = True
sys.exit(0 if ok else 1)
PY

# 4. the EXTERNAL verifier decides.
EXT_OK=0
bash -c "$VERIFY" >/dev/null 2>"$STATE_ROOT/verify_err" && EXT_OK=1

# 5. splice's own verified claim must equal the external verifier.
CLAIM="$(python3 "$CHECK" verdict "$OUT")" || live_fail "could not read splice's verdict"
SPLICE_OK="$(echo "$CLAIM" | jq -r '.splice_verified')"
[ "$EXT_OK" -eq 1 ] || live_fail "external verifier failed: $(tail -3 "$STATE_ROOT/verify_err")"
[ "$SPLICE_OK" = "true" ] || live_fail "splice did not report verified although the external verifier passed ($CLAIM)"

live_guard_check
echo "J6 PASS"
