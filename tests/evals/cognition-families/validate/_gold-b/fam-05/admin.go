package main

import (
	"net/http"
)

// adminSessionHandler serves one session through the admin path. It maps
// store failures through the SAME mapStoreError table the /session handler
// uses, so the table stays the single source of truth.
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
