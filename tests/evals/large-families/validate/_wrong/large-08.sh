#!/bin/bash
# Wrong Task B for large-08: re-derives the rule instead of reusing it.
set -e
cd "$1"
mkdir -p internal/session
cat > internal/session/sweep.go <<'GOEOF'
package session

import "time"

// Sweep re-derives the idle rule with an inclusive bound.
func (s *Store) Sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, session := range s.sessions {
		if s.now().Sub(session.LastSeen) >= s.ttl-time.Second {
			delete(s.sessions, id)
			n++
		}
	}
	return n
}
GOEOF
