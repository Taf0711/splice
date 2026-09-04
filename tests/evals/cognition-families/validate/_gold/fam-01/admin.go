package main

import "time"

// AdminForceSignOut invalidates every session held by userID, the same
// invalidation rule the password reset path established: a user-scoped
// delete sweep over the whole store. Signing out a user with no sessions
// is not an error.
func AdminForceSignOut(store *Store, userID string) error {
	for _, id := range store.sessionIDs() {
		if session, err := store.Get(id); err == nil && session.User == userID {
			store.Delete(id)
		}
	}
	_ = time.Now
	return nil
}
