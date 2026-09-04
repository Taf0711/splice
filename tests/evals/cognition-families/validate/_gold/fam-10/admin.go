package main

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

// ErrInvalidID is returned when a session id fails the validation rule.
var ErrInvalidID = errors.New("invalid session id")

// validIDPattern is the id rule: 2..64 chars, ASCII letters, digits, and
// hyphens only.
var validIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{2,64}$`)

// ValidSessionID reports whether id satisfies the session id rule.
func ValidSessionID(id string) bool {
	return validIDPattern.MatchString(id)
}

// adminSessionHandler serves GET /admin/session?id=X. It enforces the SAME
// id validation rule at the boundary and reuses the store-level sentinel
// path, exactly like the /session handler.
func adminSessionHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if !ValidSessionID(id) {
			http.Error(w, fmt.Sprintf("%s: %q", ErrInvalidID, id), http.StatusBadRequest)
			return
		}
		session, err := store.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, session)
	}
}
