#!/bin/bash
# Gold solution for large-08 Task A.
set -e
cd "$1"
mkdir -p internal/session
cat > internal/session/expiry.go <<'GOEOF'
package session

// Expired reports whether id is unknown or idle past the ttl.
func (s *Store) Expired(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	if !ok {
		return true
	}
	return s.now().Sub(session.LastSeen) > s.ttl
}
GOEOF
