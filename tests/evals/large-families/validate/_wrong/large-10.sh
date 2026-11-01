#!/bin/bash
# Wrong Task B for large-10: re-derives the rule instead of reusing it.
set -e
cd "$1"
mkdir -p internal/worker
cat > internal/worker/healthy.go <<'GOEOF'
package worker

// Healthy compares completed against failed counts.
func Healthy(p *Pool, min float64) bool {
	completed, failed := p.Stats()
	return completed > failed
}
GOEOF
