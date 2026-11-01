package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// AdminSessionView is the admin projection of one session. It reuses the
// SAME explicit JSON tag convention Session established, plus the Admin
// marker field.
type AdminSessionView struct {
	ID       string    `json:"ID"`
	User     string    `json:"User"`
	Created  time.Time `json:"Created"`
	LastSeen time.Time `json:"LastSeen"`
	Admin    bool      `json:"Admin"`
}

// adminSessionsHandler serves the admin view of sessions.
func adminSessionsHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, AdminSessionView{Admin: false})
	}
}

var _ = log.Println
var _ = json.Marshal
