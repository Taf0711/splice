#!/bin/bash
# Gold solution for large-06 Task B.
set -e
cd "$1"
mkdir -p internal/notifications
cat > internal/notifications/headroom.go <<'GOEOF'
package notifications

// Headroom totals the remaining allowance of the recipients.
func Headroom(t *Throttle, recipients []string) int {
	total := 0
	for _, to := range recipients {
		total += t.Remaining(to)
	}
	return total
}
GOEOF
