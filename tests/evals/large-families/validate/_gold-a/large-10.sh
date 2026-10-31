#!/bin/bash
# Gold solution for large-10 Task A.
set -e
cd "$1"
mkdir -p internal/worker
cat > internal/worker/success_rate.go <<'GOEOF'
package worker

// SuccessRate is completed jobs over all submitted jobs, 0 with none.
func (p *Pool) SuccessRate() float64 {
	completed, failed := p.Stats()
	if completed+failed == 0 {
		return 0
	}
	return float64(completed) / float64(completed+failed)
}
GOEOF
