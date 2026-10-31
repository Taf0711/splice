#!/bin/bash
# Gold solution for mvp-01 Task A: per-user invalidation on Store + reset
# wiring. Applied to a fixture copy by validate_mvp.py. $1 = repo root.
set -e
cd "$1"
mkdir -p internal/session
cat > internal/session/invalidation.go <<'EOF'
package session

// InvalidateUserSessions deletes every session that belongs to userID and
// returns how many it removed. Unknown users remove nothing.
func (s *Store) InvalidateUserSessions(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, sess := range s.sessions {
		if sess.User == userID {
			delete(s.sessions, id)
			removed++
		}
	}
	return removed
}
EOF
python3 - <<'PYEOF'
import re
path = "internal/auth/password.go"
src = open(path).read()
old = """	p.passwords[userID] = newPassword
	// Extension point: a successful reset must also invalidate the
	// sessions of userID so old logins stop working. The wiring for
	// that belongs here.
	return nil"""
new = """	p.passwords[userID] = newPassword
	p.store.InvalidateUserSessions(userID)
	return nil"""
assert old in src, "password.go extension point not found"
open(path, "w").write(src.replace(old, new))
PYEOF