#!/bin/bash
# Gold solution for large-06 Task A.
set -e
cd "$1"
mkdir -p internal/notifications
cat > internal/notifications/remaining.go <<'GOEOF'
package notifications

// Remaining reports how many more notifications to may receive.
func (t *Throttle) Remaining(to string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if left := t.limit - t.sent[to]; left > 0 {
		return left
	}
	return 0
}
GOEOF
