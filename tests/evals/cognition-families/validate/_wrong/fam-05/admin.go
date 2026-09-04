package main

import (
	"errors"
	"net/http"
)

// mapStoreError is the single source of truth for translating store errors
// to HTTP status codes.
func mapStoreError(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

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
