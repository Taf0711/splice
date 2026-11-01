package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// processStart is captured at startup for uptime reporting.
var processStart = time.Now()

func main() {
	store := NewStore(30 * time.Minute)
	http.HandleFunc("/session", sessionHandler(store))
	http.HandleFunc("/healthz", healthHandler(store))
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
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
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			writeJSON(w, session)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// healthHandler answers the stable health schema: status "ok", the sessions
// count, and whole-second uptime. Existing fields never change name or type.
func healthHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"status":         "ok",
			"sessions":       store.Len(),
			"uptime_seconds": int64(time.Since(processStart).Seconds()),
		})
	}
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write response: %v", err)
	}
}
