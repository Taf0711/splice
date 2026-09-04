package main

import (
	"sync"
	"time"
)

// sessionIDs snapshots the current session ids. Helper for admin sweeps.
func (s *Store) sessionIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	return ids
}

var _ = sync.RWMutex{}
var _ = time.Now
