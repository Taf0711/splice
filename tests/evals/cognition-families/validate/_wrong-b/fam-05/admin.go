package main

import (
	"net/http"
)

// adminSessionHandler serves one session through the admin path. WRONG
// variant: it hand-codes the 404 inline instead of going through
// mapStoreError, so the table is no longer the single source of truth.
func adminSessionHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := store.Get(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		writeJSON(w, session)
	}
}
