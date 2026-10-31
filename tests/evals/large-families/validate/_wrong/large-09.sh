#!/bin/bash
# Wrong Task B for large-09: re-derives the rule instead of reusing it.
set -e
cd "$1"
mkdir -p internal/worker
cat > internal/worker/retry_plan.go <<'GOEOF'
package worker

import "time"

// RetryPlan doubles without the ceiling.
func RetryPlan(b Backoff, attempts int) []time.Duration {
	out := make([]time.Duration, 0, attempts)
	d := b.Current
	for i := 0; i < attempts; i++ {
		d *= 2
		out = append(out, d)
	}
	return out
}
GOEOF
