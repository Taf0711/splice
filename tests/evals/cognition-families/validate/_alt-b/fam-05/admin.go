package main

import "net/http"

// adminHandler is the same deliverable under a DIFFERENT name than the
// cognition corpus gold. The name-agnostic verifier must accept it.
func adminHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := store.Get(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), mapStoreError(err))
			return
		}
		writeJSON(w, session)
	}
}
