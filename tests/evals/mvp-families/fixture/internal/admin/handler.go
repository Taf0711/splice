// Package admin serves the admin console endpoints.
package admin

import (
	"encoding/json"
	"net/http"

	"demo/internal/session"
)

// Handler serves admin routes over a shared session store.
type Handler struct {
	store *session.Store
}

// NewHandler builds an admin handler over a session store.
func NewHandler(store *session.Store) *Handler {
	return &Handler{store: store}
}

// ServeHTTP dispatches an admin request to its route.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/admin/health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		http.NotFound(w, r)
	}
	// Placeholder: the force sign-out route belongs here and must use
	// the shared session store.
}

// writeJSON writes body as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
