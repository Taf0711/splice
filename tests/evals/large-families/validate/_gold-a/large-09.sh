#!/bin/bash
# Gold solution for large-09 Task A.
set -e
cd "$1"
mkdir -p internal/worker
cat > internal/worker/peek.go <<'GOEOF'
package worker

import "time"

// Peek returns the delay Next would return, without changing b.
func (b *Backoff) Peek() time.Duration {
	next := *b
	return next.Next()
}
GOEOF
