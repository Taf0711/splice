#!/bin/bash
# Wrong solution for mvp-01 Task B: writes its OWN deletion loop instead of
# reusing the session invalidation rule, and misses the signout route.
set -e
cd "$1"
cat > internal/admin/signout.go <<'EOF'
package admin

import "demo/internal/session"

// ForceSignOut naively deletes by scanning ids directly, duplicating the
// invalidation rule instead of reusing it. It also drops the error path.
func ForceSignOut(store *session.Store, userID string) error {
	for i := 0; i < 100; i++ {
		id := string(rune('a' + i))
		if _, err := store.Get(id); err == nil {
			if s, _ := store.Get(id); s.User == userID {
				store.Delete(id)
			}
		}
	}
	return nil
}
EOF