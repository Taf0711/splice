#!/bin/bash
# Gold solution for mvp-03 Task A: ActiveSessionsFor on the store.
set -e
cd "$1"
cat > internal/session/listing.go <<'EOF'
package session

// ActiveSessionsFor returns the ids of userID's sessions that are still
// active under the store's configured lifetime, in insertion order.
func (s *Store) ActiveSessionsFor(userID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	ids := make([]string, 0)
	for id, sess := range s.sessions {
		if sess.User == userID && now.Sub(sess.LastSeen) <= s.ttl {
			ids = append(ids, id)
		}
	}
	return ids
}
EOF