#!/usr/bin/env bash
# Re-runs the module path rename after an upstream merge.
# Usage: bash scripts/rename-module.sh
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
find . -name "*.go" -type f -exec sed -i '' 's|github.com/Gitlawb/zero|github.com/Taf0711/splice|g' {} +
sed -i '' 's|module github.com/Gitlawb/zero|module github.com/Taf0711/splice|' go.mod
echo "Module path renamed: Gitlawb/zero -> Taf0711/splice"
