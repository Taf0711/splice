package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// mapStoreError is the single source of truth for translating store errors
// to HTTP status codes. Handlers call this; they never hand-code numbers.
func mapStoreError(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func main() {
	store := NewStore(30 * time.Minute)
	mux := http.NewServeMux()
	mux.HandleFunc("/session", sessionHandler(store))
	mux.HandleFunc("/healthz", healthHandler(store))
	log.Println("listening on :8080")
	server := &http.Server{Addr: ":8080", Handler: mux}
	log.Fatal(server.ListenAndServe())
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

// newSessionMux builds a fresh mux over one store (test seam).
func newSessionMux(store *Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/session", sessionHandler(store))
	mux.HandleFunc("/healthz", healthHandler(store))
	return mux
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write response: %v", err)
	}
}
