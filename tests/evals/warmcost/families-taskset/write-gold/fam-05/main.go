package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

func main() {
	store := NewStore(30 * time.Minute)
	http.HandleFunc("/session", sessionHandler(store))
	http.HandleFunc("/healthz", healthHandler(store))
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// mapStoreError is the single source of truth for translating store errors
// into HTTP status codes. Handlers never hand-code status numbers for store
// failures; they call this.
//
// TODO: map ErrConflict -> 409, ErrInvalidID -> 400, ErrUserAtCap -> 429
// once those sentinels exist in the fixture. Only ErrNotFound exists today.
func mapStoreError(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// sessionHandler creates a session on POST and reads one on GET.
func sessionHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			id := r.URL.Query().Get("id")
			user := r.URL.Query().Get("user")
			if id == "" || user == "" {
				http.Error(w, "id and user are required", http.StatusBadRequest)
				return
			}
			writeJSON(w, store.Create(id, user))
		case http.MethodGet:
			session, err := store.Get(r.URL.Query().Get("id"))
			if err != nil {
				http.Error(w, err.Error(), mapStoreError(err))
				return
			}
			writeJSON(w, session)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// healthHandler reports how many sessions are currently held.
func healthHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]int{"sessions": store.Len()})
	}
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write response: %v", err)
	}
}
