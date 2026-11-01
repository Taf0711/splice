#!/bin/bash
# Gold solution for mvp-03 Task B: CountActiveSessions reusing the
# established active-session rule (routes through ActiveSessionsFor).
set -e
cd "$1"
cat > internal/session/count_active.go <<'EOF'
package session

// CountActiveSessions reports how many active sessions one user holds,
// reusing the same active-session rule the per-user listing established.
func CountActiveSessions(store *Store, userID string) int {
	return len(store.ActiveSessionsFor(userID))
}
EOF