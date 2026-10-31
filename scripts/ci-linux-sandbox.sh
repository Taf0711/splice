#!/usr/bin/env bash
# Installs and verifies the Linux native stage sandbox for CI jobs that run
# stage subprocesses: build-and-test and e2e. Both jobs call this script so the
# setup cannot drift between them.
#
# Usage: scripts/ci-linux-sandbox.sh install|verify
#
# The deterministic pipeline profiles (splice.stage, splice.dtools) fail closed
# without native platform confinement. On Linux that needs bubblewrap and the
# splice-linux-sandbox helper. Without them every stage subprocess is refused
# and its tests skip, so CI reports green on code it never ran.
#
# Ubuntu 24.04 sets kernel.apparmor_restrict_unprivileged_userns=1, which blocks
# the unprivileged user namespace that bwrap --unshare-user needs. The symptom
# is "bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted". Clearing
# the sysctl restores unprivileged namespaces.
#
# The helper must be on PATH: under `go test` the running executable is the test
# binary, which does not serve the hidden helper subcommand, so the PATH tier is
# the only one that yields a working helper.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

install() {
  . /etc/os-release && echo "runner image: $PRETTY_NAME (kernel $(uname -r))"
  sudo apt-get update
  sudo apt-get install -y bubblewrap
  bwrap --version
  echo "apparmor_restrict_unprivileged_userns before: $(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || echo 'not present')"
  if [ -e /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
    sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
  fi
  go build -o "$HOME/.local/bin/splice-linux-sandbox" ./cmd/splice-linux-sandbox
  if [ -n "${GITHUB_PATH:-}" ]; then
    echo "$HOME/.local/bin" >> "$GITHUB_PATH"
  fi
}

# Prove the backend actually confines before trusting the suite. The
# network-namespace check is the exact operation that failed on 24.04, so a
# regression here is loud instead of silently downgrading to skips.
verify() {
  command -v bwrap
  command -v splice-linux-sandbox
  bwrap --unshare-user --unshare-net --ro-bind / / -- /bin/true
  echo "bwrap unshare-net OK"
}

case "${1:-}" in
  install) install ;;
  verify) verify ;;
  *)
    echo "usage: $0 install|verify" >&2
    exit 2
    ;;
esac
