#!/bin/bash
# Gold solution for mvp-02 Task B: Sweep on Store reading the shared
# lifetime source. Applied AFTER gold-A.
set -e
cd "$1"
cat > internal/session/sweep.go <<'EOF'
package session

// Sweep deletes every expired session and returns how many it removed.
// The expiry decision reads the store's configured lifetime, the same
// single source the rest of the repository uses.
func (s *Store) Sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	now := s.now()
	for id, sess := range s.sessions {
		if now.Sub(sess.LastSeen) > s.ttl {
			delete(s.sessions, id)
			removed++
		}
	}
	return removed
}
EOF