package main

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

// DeleteAllForUser deletes every session of a user and returns the count.
// Deleting when the user holds nothing returns 0 (the idempotence contract
// Delete established).
func (s *Store) DeleteAllForUser(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for id, session := range s.sessions {
		if session.User == userID {
			delete(s.sessions, id)
			count++
		}
	}
	return count
}

// Len reports how many sessions are held.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}
