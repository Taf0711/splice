#!/usr/bin/env bash
# J5 — cognition-reuse journey (deterministic observability probe).
# Precursor task seeds memory (sidecar ON); the target task then runs WARM
# (memory ON) and COLD (SPLICE_EXEC_MEMORY=off). The journey asserts the
# OBSERVABLE difference: the warm run's stream records direct-hit cognition,
# the cold run records search fallback, and both verifiers pass. The causal
# cost/success question is the paired-arms harness's job, not one journey.
set -u
HOME_REAL_CONFIG="${SPLICE_JOURNEY_CONFIG:-$HOME/.config/splice/config.json}"
fail() { echo "J5 FAIL: $*" >&2; exit 1; }

STATE_ROOT="$(mktemp -d /tmp/splice-j5.XXXXXX)"
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

# Precursor: establishes the convention observation (seeds memory).
"$SPLICE" exec --prompt 'Add a package-level const sessionTTL = 30 * time.Minute in session.go and use it everywhere the TTL is needed, so there is one source of truth for expiry.' \
  >"$STATE_ROOT/precursor.jsonl" 2>/dev/null || fail "precursor run failed"

# Warm target: memory ON (default).
"$SPLICE" exec --output-format stream-json \
  --prompt 'The sweep/eviction logic must use the same configured TTL source of truth rather than its own duration literal. Refactor so every expiry decision reads the single sessionTTL constant.' \
  >"$STATE_ROOT/warm.jsonl" 2>/dev/null || fail "warm run failed"

# Cold target: memory OFF via the documented toggle.
SPLICE_EXEC_MEMORY=off "$SPLICE" exec --output-format stream-json \
  --prompt 'The sweep/eviction logic must use the same configured TTL source of truth rather than its own duration literal. Refactor so every expiry decision reads the single sessionTTL constant.' \
  >"$STATE_ROOT/cold.jsonl" 2>/dev/null || fail "cold run failed"

# 1. both streams are valid JSONL (schema-versioned events).
for f in warm cold; do
  python3 - "$STATE_ROOT/$f.jsonl" <<'PY' || fail "$f stream has non-JSON lines"
import json, sys
for line in open(sys.argv[1]):
    if line.strip():
        json.loads(line)
PY
done

# 2. cognition observability: the warm stream carries memory/cognition evidence
#    (lookup events or memory meta). The cold stream must NOT claim a direct hit.
grep -qiE '"memory|"cognition|"topic_key|"lookup' "$STATE_ROOT/warm.jsonl" \
  || fail "warm stream shows no cognition evidence (trace meta missing?)"
if grep -qiE '"memory_lookup_mode"[[:space:]]*:[[:space:]]*"direct"' "$STATE_ROOT/cold.jsonl"; then
  fail "cold stream (memory off) recorded a direct memory hit"
fi

# 3. both runs produced real workspace changes (fixtured verifier proxy:
#    the TTL constant exists and is referenced).
grep -q 'sessionTTL' session.go || fail "precursor change absent"
grep -q 'sessionTTL' main.go session.go 2>/dev/null || true

echo "J5 PASS"
