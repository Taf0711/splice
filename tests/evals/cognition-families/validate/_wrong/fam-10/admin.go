package main

import (
	"errors"
	"net/http"
	"regexp"
)

// ErrInvalidID is returned when a session id fails the validation rule.
var ErrInvalidID = errors.New("invalid session id")

// validIDPattern is the id rule.
var validIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{2,64}$`)

// ValidSessionID reports whether id satisfies the session id rule. The
// rule exists (the /session boundary uses it) but the admin path SKIPS it.
func ValidSessionID(id string) bool {
	return validIDPattern.MatchString(id)
}

// adminSessionHandler serves GET /admin/session?id=X. WRONG variant: it
// skips the id validation rule entirely and serves the raw store lookup,
// so invalid ids reach the store instead of the 400 boundary.
func adminSessionHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := store.Get(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, session)
	}
}
