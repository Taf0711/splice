package main

import (
	"errors"
	"net/http"
)

// mapStoreError is the single source of truth for translating store errors
// to HTTP status codes. Handlers never hand-code status numbers for store
// failures; they call this.
func mapStoreError(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// adminSessionHandler serves one session through the admin path, mapping
// failures through the shared table.
func adminSessionHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := store.Get(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), mapStoreError(err))
			return
		}
		writeJSON(w, session)
	}
}
