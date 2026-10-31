#!/bin/bash
# Gold solution for large-09 Task B.
set -e
cd "$1"
mkdir -p internal/worker
cat > internal/worker/retry_plan.go <<'GOEOF'
package worker

import "time"

// RetryPlan lists the delays of the next attempts retries.
func RetryPlan(b Backoff, attempts int) []time.Duration {
	out := make([]time.Duration, 0, attempts)
	for i := 0; i < attempts; i++ {
		out = append(out, b.Next())
	}
	return out
}
GOEOF
