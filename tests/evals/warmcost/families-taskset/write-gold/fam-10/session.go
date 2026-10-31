package main

import (
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned when a session id is unknown or has expired.
var ErrNotFound = errors.New("session not found")

// ErrInvalidID is returned when a session id fails the validation rule.
var ErrInvalidID = errors.New("invalid session id")

// ValidSessionID reports whether id satisfies the session id rule: 2..64
// characters, ASCII letters, digits, and hyphens only.
func ValidSessionID(id string) bool {
	if len(id) < 2 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// Session is one signed-in user.
type Session struct {
	ID       string
	User     string
	Created  time.Time
	LastSeen time.Time
}

// Store keeps sessions in memory. Everything is lost when the process exits.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]Session
	ttl      time.Duration
	now      func() time.Time
}

// NewStore returns an empty store. Sessions idle for longer than ttl expire.
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
