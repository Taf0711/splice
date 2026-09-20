#!/bin/bash
# Wrong solution for mvp-02 Task B: Sweep hard-codes its own duration
# literal instead of reading the shared lifetime source.
set -e
cd "$1"
cat > internal/session/sweep.go <<'EOF'
package session

import "time"

// Sweep deletes every expired session with its OWN hard-coded expiry rule,
// ignoring the store's configured lifetime.
func (s *Store) Sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	now := s.now()
	for id, sess := range s.sessions {
		if now.Sub(sess.LastSeen) > 2*time.Hour {
			delete(s.sessions, id)
			removed++
		}
	}
	return removed
}
EOF