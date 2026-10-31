#!/bin/bash
# Gold solution for large-05 Task B.
set -e
cd "$1"
mkdir -p internal/notifications
cat > internal/notifications/queue_welcome.go <<'GOEOF'
package notifications

// QueueWelcome queues the welcome email for one recipient.
func QueueWelcome(d *Dispatcher, to string) {
	d.Queue(RenderFor(Welcome, to))
}
GOEOF
