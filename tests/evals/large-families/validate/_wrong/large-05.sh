#!/bin/bash
# Wrong Task B for large-05: re-derives the rule instead of reusing it.
set -e
cd "$1"
mkdir -p internal/notifications
cat > internal/notifications/queue_welcome.go <<'GOEOF'
package notifications

// QueueWelcome builds the welcome message by hand.
func QueueWelcome(d *Dispatcher, to string) {
	d.Queue(Message{Kind: KindEmail, To: to, Subject: "Welcome", Body: "Welcome " + to})
}
GOEOF
