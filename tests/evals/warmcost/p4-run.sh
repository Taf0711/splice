#!/usr/bin/env bash
# P4 diagnostic launcher: assert the resolved code_writer stage model equals
# MODEL (a stage-models.json override must not silently outrank --model), then
# run `splice eval mvp` with the caller's flags. Harness only.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../../.." && pwd)"
cd "$REPO"

SPLICE="${SPLICE:-/tmp/p4r-splice}"
MODEL="${MODEL:-z-ai/glm-5.3-flash}"
PREFLIGHT_BIN="${PREFLIGHT_BIN:-/tmp/p4-preflight}"
SPLICE_DIR="${SPLICE_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/splice}"

go build -o "$PREFLIGHT_BIN" ./tests/evals/warmcost/cmd/p4preflight
"$PREFLIGHT_BIN" --splice-dir "$SPLICE_DIR" --stage code_writer --want "$MODEL" --check-reasoning

exec "$SPLICE" eval mvp "$@"
