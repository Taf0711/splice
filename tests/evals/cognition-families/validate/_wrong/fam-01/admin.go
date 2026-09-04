package main

import "time"

// AdminForceSignOut invalidates only the most recent session of userID
// (WRONG: partial invalidation leaves the other sessions of that user
// live, the exact bug the password reset convention exists to prevent).
func AdminForceSignOut(store *Store, userID string) error {
	var lastID string
	for _, id := range store.sessionIDs() {
		if session, err := store.Get(id); err == nil && session.User == userID {
			lastID = id
		}
	}
	if lastID != "" {
		store.Delete(lastID)
	}
	_ = time.Now
	return nil
}
