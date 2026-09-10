#!/bin/bash
set -e
cd "$1"
cat > internal/audit/latest.go <<'GOEOF'
package audit

// Wrong: returns the FIRST matching event, not the latest.
func LatestDunningEvent(t *Trail, action string) *Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.events {
		if t.events[i].Action == action {
			e := t.events[i]
			return &e
		}
	}
	return nil
}
GOEOF
