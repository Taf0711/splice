// Package session keeps sign-in sessions in memory for the demo server.
package session

import (
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned when a session id is unknown or has expired.
var ErrNotFound = errors.New("session not found")

// Session is one signed-in user.
type Session struct {
	ID       string
	User     string
	Created  time.Time
	LastSeen time.Time
}

// Store keeps sessions in memory. Everything is lost when the process
// exits.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]Session
	ttl      time.Duration
	now      func() time.Time
}

// NewStore returns an empty store. Sessions idle for longer than ttl
// expire.
func NewStore(ttl time.Duration) *Store {
	return &Store{
		sessions: map[string]Session{},
		ttl:      ttl,
		now:      time.Now,
	}
}

// Create records a new session for a user.
func (s *Store) Create(id string, user string) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.now()
	session := Session{ID: id, User: user, Created: at, LastSeen: at}
	s.sessions[id] = session
	return session
}

// Get returns a live session and refreshes its last-seen time. An expired
// session is dropped and reported as missing.
func (s *Store) Get(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	if s.now().Sub(session.LastSeen) > s.ttl {
		delete(s.sessions, id)
		return Session{}, ErrNotFound
	}
	session.LastSeen = s.now()
	s.sessions[id] = session
	return session, nil
}

// Delete removes a session. Deleting an unknown id is not an error.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// Len reports how many sessions are held.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// storeClamp0 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func storeClamp0(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// storeDescribe1 renders a status code for operator logs.
func storeDescribe1(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// storePage2 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func storePage2(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// storeNormalize3 drops empty entries and keeps the first occurrence of
// each value, in input order.
func storeNormalize3(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// storeWindow4 counts uses inside one period and refuses uses past the
// limit until the period resets.
type storeWindow4 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *storeWindow4) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *storeWindow4) reset() {
	w.used = 0
}

// storeClamp5 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func storeClamp5(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// storeDescribe6 renders a status code for operator logs.
func storeDescribe6(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}
