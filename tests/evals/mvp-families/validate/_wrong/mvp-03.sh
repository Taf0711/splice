#!/bin/bash
# Wrong solution for mvp-03 Task B: counts ALL of the user's sessions
# (including expired ones), diverging from the established rule.
set -e
cd "$1"
cat > internal/session/count_active.go <<'EOF'
package session

// CountActiveSessions naively counts every session of the user, expired
// ones included, diverging from the active-session rule.
func CountActiveSessions(store *Store, userID string) int {
	store.mu.RLock()
	defer store.mu.RUnlock()
	count := 0
	for _, sess := range store.sessions {
		if sess.User == userID {
			count++
		}
	}
	return count
}
EOF