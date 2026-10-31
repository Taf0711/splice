#!/bin/bash
# Gold solution for large-10 Task B.
set -e
cd "$1"
mkdir -p internal/worker
cat > internal/worker/healthy.go <<'GOEOF'
package worker

// Healthy reports whether the pool ran a job and meets the minimum rate.
func Healthy(p *Pool, min float64) bool {
	completed, failed := p.Stats()
	return completed+failed > 0 && p.SuccessRate() >= min
}
GOEOF
