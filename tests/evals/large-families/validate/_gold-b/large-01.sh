#!/bin/bash
set -e
cd "$1"
cat > internal/audit/latest.go <<'GOEOF'
package audit

// LatestDunningEvent is the latest-match query: the most recent event with
// the given action, or nil when none exists.
func LatestDunningEvent(t *Trail, action string) *Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	var latest *Event
	for i := range t.events {
		if t.events[i].Action == action {
			e := t.events[i]
			latest = &e
		}
	}
	return latest
}
GOEOF
