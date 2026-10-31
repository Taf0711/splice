#!/bin/bash
# Wrong Task B for large-06: re-derives the rule instead of reusing it.
set -e
cd "$1"
mkdir -p internal/notifications
cat > internal/notifications/headroom.go <<'GOEOF'
package notifications

// Headroom counts by trying to send, which uses the allowance.
func Headroom(t *Throttle, recipients []string) int {
	total := 0
	for _, to := range recipients {
		for t.Allow(to) {
			total++
		}
	}
	return total
}
GOEOF
