#!/bin/sh
# Prepares the TW3 demo fixture and starts the TUI.
# Used by scripts/tui-worktree-reject.tape. Requires SPLICE_TUI_DEMO=worktree-reject.
set -e
src=${SPLICE_SRC:-$(cd "$(dirname "$0")/.." && pwd)}
bin=${SPLICE_BIN:-/tmp/splice-tw3-bin}
if [ ! -x "$bin" ]; then
	(cd "$src" && go build -o "$bin" ./cmd/splice)
fi
repo=$(mktemp -d /tmp/splice-tw3-XXXXXX)
home=$(mktemp -d /tmp/splice-tw3-home-XXXXXX)
mkdir -p "$home/.config/splice"
cat > "$home/.config/splice/config.json" <<'EOF'
{"activeProvider":"demo","providers":[{"name":"demo","provider_kind":"openai-compatible","baseURL":"http://127.0.0.1:9","apiKey":"demo","model":"demo"}]}
EOF
cd "$repo"
git init -q
git config user.email demo@local
git config user.name demo
printf '%s\n' 'package add' '' 'func Add(a, b int) int { return a }' > add.go
printf '%s\n' 'package add' 'import "testing"' 'func TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(2) } }' > add_test.go
git add .
git commit -q -m seed
export HOME="$home"
export XDG_CONFIG_HOME="$home/.config"
export XDG_DATA_HOME="$home/.local/share"
export XDG_STATE_HOME="$home/.local/state"
export SPLICE_TUI_DEMO=worktree-reject
export SPLICE_NO_RESUME_PROMPT=1
exec "$bin" --trust --skip-permissions-unsafe
