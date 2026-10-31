#!/bin/bash
# Gold solution for large-08 Task B.
set -e
cd "$1"
mkdir -p internal/session
cat > internal/session/sweep.go <<'GOEOF'
package session

// Sweep deletes every expired session and returns how many it deleted.
func (s *Store) Sweep() int {
	s.mu.RLock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	n := 0
	for _, id := range ids {
		if s.Expired(id) {
			s.Delete(id)
			n++
		}
	}
	return n
}
GOEOF
