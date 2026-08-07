#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
install_dir=${XDG_BIN_HOME:-"$HOME/.local/bin"}
target="$install_dir/splice-dev"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/splice-dev.XXXXXX")
staged="$install_dir/.splice-dev.$$"

cleanup() {
	rm -rf "$tmp"
	rm -f "$staged"
}
trap cleanup EXIT HUP INT TERM

command -v git >/dev/null 2>&1 || {
	echo "install-dev: git is required" >&2
	exit 1
}
command -v go >/dev/null 2>&1 || {
	echo "install-dev: Go is required" >&2
	exit 1
}

echo "Fetching origin/dev..."
git -C "$repo" fetch --quiet origin dev

git -C "$repo" archive --output "$tmp/source.tar" origin/dev
mkdir "$tmp/source"
tar -xf "$tmp/source.tar" -C "$tmp/source"

revision=$(git -C "$repo" rev-parse --short origin/dev)
echo "Building origin/dev at $revision..."
(
	cd "$tmp/source"
	go build -o "$tmp/splice-dev" ./cmd/splice
)

mkdir -p "$install_dir"
install -m 0755 "$tmp/splice-dev" "$staged"
mv -f "$staged" "$target"

echo "Installed $target"
"$target" version
if [ "$(command -v splice-dev 2>/dev/null || true)" != "$target" ]; then
	echo "Add $install_dir to PATH, then run: splice-dev" >&2
fi
