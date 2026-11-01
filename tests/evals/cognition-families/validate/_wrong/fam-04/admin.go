package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// AdminSessionView is the admin projection of one session. WRONG variant:
// it drops the explicit JSON tag convention and falls back to default
// marshaling, so the wire keys diverge from Session's tagged envelope.
type AdminSessionView struct {
	ID       string
	User     string
	Created  time.Time
	LastSeen time.Time
	Admin    bool
}

// adminSessionsHandler serves the admin view of sessions.
func adminSessionsHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, AdminSessionView{Admin: false})
	}
}

var _ = log.Println
var _ = json.Marshal
